package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// DryRunResult is the preview of what a migration would change on the target.
type DryRunResult struct {
	MigrationID int              `json:"migrationId"`
	Categories   []DryRunCategory `json:"categories"`
	Summary      DryRunSummary    `json:"summary"`
}

type DryRunCategory struct {
	Category string         `json:"category"`
	Changes  []DryRunChange `json:"changes"`
	Summary  string         `json:"summary"`
}

type DryRunChange struct {
	Type     string `json:"type"`     // "add", "modify", "remove"
	Resource string `json:"resource"`  // e.g. "package:nginx", "file:/etc/nginx/nginx.conf"
	Detail   string `json:"detail"`    // human-readable description
}

type DryRunSummary struct {
	TotalChanges int `json:"totalChanges"`
	AddCount     int `json:"addCount"`
	ModifyCount  int `json:"modifyCount"`
	RemoveCount  int `json:"removeCount"`
}

// DryRun simulates the migration without applying any changes.
// It compares collected source data with the target's current state.
func (e *Executor) DryRun(ctx context.Context, migrationID int, onProgress StepCallback) (*DryRunResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		return nil, fmt.Errorf("migration not found: %w", err)
	}

	// 2. Get SSH connection to target
	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return nil, fmt.Errorf("target server not found: %w", err)
	}

	onProgress(WSMessage{Step: "dryrun", Status: "progress", Value: "Connecting to target server..."})

	sshClient, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		return nil, fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "dryrun", Status: "success", Value: "Connected to target server"})

	// 3. Load collected steps
	steps, err := e.repo.GetSteps(migrationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load steps: %w", err)
	}

	result := &DryRunResult{
		MigrationID: migrationID,
	}

	// 4. For each category, compute what would change
	for _, step := range steps {
		if step.Action != "collect" || step.Status != StepStatusCompleted {
			continue
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		mod, ok := e.registry.Get(step.Category)
		if !ok {
			continue
		}

		onProgress(WSMessage{
			Step:   "dryrun:" + step.Category,
			Status: "progress",
			Value:  "Analyzing " + step.Category + "...",
		})

		var data CategoryData
		if err := json.Unmarshal([]byte(step.Data), &data); err != nil {
			continue
		}

		changes := e.computeDryRunChanges(sshClient, step.Category, data, mod)

		result.Categories = append(result.Categories, DryRunCategory{
			Category: step.Category,
			Changes:  changes,
			Summary:  fmt.Sprintf("%d changes", len(changes)),
		})

		for _, c := range changes {
			switch c.Type {
			case "add":
				result.Summary.AddCount++
			case "modify":
				result.Summary.ModifyCount++
			case "remove":
				result.Summary.RemoveCount++
			}
			result.Summary.TotalChanges++
		}

		onProgress(WSMessage{
			Step:   "dryrun:" + step.Category,
			Status: "success",
			Value:  fmt.Sprintf("Found %d changes for %s", len(changes), step.Category),
		})
	}

	// Sort categories for consistent output
	sort.Slice(result.Categories, func(i, j int) bool {
		return result.Categories[i].Category < result.Categories[j].Category
	})

	onProgress(WSMessage{Step: "dryrun", Status: "complete", Value: fmt.Sprintf("Dry run complete: %d total changes", result.Summary.TotalChanges)})

	return result, nil
}

// computeDryRunChanges compares source data with target state for a category.
func (e *Executor) computeDryRunChanges(ssh SSHExecuter, category string, data CategoryData, mod CategoryModule) []DryRunChange {
	switch category {
	case "packages":
		return e.dryRunPackages(ssh, data)
	case "configs":
		return e.dryRunConfigs(ssh, data)
	case "services":
		return e.dryRunServices(ssh, data)
	case "users":
		return e.dryRunUsers(ssh, data)
	case "docker":
		return e.dryRunDocker(ssh, data)
	default:
		return nil
	}
}

func (e *Executor) dryRunPackages(ssh SSHExecuter, data CategoryData) []DryRunChange {
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		return nil
	}

	info, err := DetectDistro(ssh)
	if err != nil {
		return nil
	}
	adapter := GetAdapter(info)

	stdout, _, _, _ := ssh.Exec(adapter.ListPackages())
	installed := make(map[string]bool)
	for _, pkg := range parsePackageList(stdout, adapter.PackageManager()) {
		installed[pkg] = true
	}

	var changes []DryRunChange
	for _, pkg := range pd.Packages {
		if !installed[pkg] {
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "package:" + pkg,
				Detail:   fmt.Sprintf("Package %s will be installed", pkg),
			})
		}
	}
	return changes
}

func (e *Executor) dryRunConfigs(ssh SSHExecuter, data CategoryData) []DryRunChange {
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return nil
	}

	var changes []DryRunChange
	for path, content := range cd.Files {
		// Check if file exists on target
		buf := new(bytes.Buffer)
		if err := ssh.Download(path, buf); err != nil {
			// File doesn't exist
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "file:" + path,
				Detail:   fmt.Sprintf("File %s will be created", path),
			})
		} else if !bytes.Equal(buf.Bytes(), content) {
			// File exists but differs
			changes = append(changes, DryRunChange{
				Type:     "modify",
				Resource: "file:" + path,
				Detail:   fmt.Sprintf("File %s will be overwritten (content differs)", path),
			})
		}
	}
	return changes
}

func (e *Executor) dryRunServices(ssh SSHExecuter, data CategoryData) []DryRunChange {
	var sd ServicesData
	if err := json.Unmarshal(data.Data, &sd); err != nil {
		return nil
	}

	stdout, _, _, _ := ssh.Exec("systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null")
	enabled := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.Contains(line, ".service") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 1 {
				enabled[strings.TrimSuffix(fields[0], ".service")] = true
			}
		}
	}

	var changes []DryRunChange
	for _, svc := range sd.Services {
		if !enabled[svc] {
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "service:" + svc,
				Detail:   fmt.Sprintf("Service %s will be enabled and started", svc),
			})
		}
	}
	return changes
}

func (e *Executor) dryRunUsers(ssh SSHExecuter, data CategoryData) []DryRunChange {
	var ud UsersData
	if err := json.Unmarshal(data.Data, &ud); err != nil {
		return nil
	}

	stdout, _, _, _ := ssh.Exec("cat /etc/passwd")
	existingUsers := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 1 {
			existingUsers[fields[0]] = true
		}
	}

	var changes []DryRunChange
	for _, user := range ud.Users {
		if !existingUsers[user.Name] {
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "user:" + user.Name,
				Detail:   fmt.Sprintf("User %s (UID %d) will be created", user.Name, user.UID),
			})
		}
	}
	return changes
}

func (e *Executor) dryRunDocker(ssh SSHExecuter, data CategoryData) []DryRunChange {
	var dd DockerData
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		return nil
	}

	// Check if Docker is installed
	stdout, _, exitCode, _ := ssh.Exec("which docker 2>/dev/null")
	if exitCode != 0 || strings.TrimSpace(stdout) == "" {
		return []DryRunChange{
			{
				Type:     "add",
				Resource: "docker:install",
				Detail:   "Docker is not installed on the target server - installation required",
			},
		}
	}

	// Get existing images on target
	stdout, _, _, _ = ssh.Exec("docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	existingImages := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			existingImages[line] = true
		}
	}

	var changes []DryRunChange
	for _, image := range dd.Images {
		if !existingImages[image] {
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "docker:image:" + image,
				Detail:   fmt.Sprintf("Image %s will be pulled", image),
			})
		}
	}

	// Get existing containers
	stdout, _, _, _ = ssh.Exec("docker ps -a --format '{{.Names}}' 2>/dev/null")
	existingContainers := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			existingContainers[line] = true
		}
	}

	for _, container := range dd.Containers {
		if !existingContainers[container.Name] {
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "docker:container:" + container.Name,
				Detail:   fmt.Sprintf("Container %s (image: %s) will be created", container.Name, container.Image),
			})
		} else {
			changes = append(changes, DryRunChange{
				Type:     "modify",
				Resource: "docker:container:" + container.Name,
				Detail:   fmt.Sprintf("Container %s will be recreated", container.Name),
			})
		}
	}

	for _, vol := range dd.Volumes {
		changes = append(changes, DryRunChange{
			Type:     "add",
			Resource: "docker:volume:" + vol.Name,
			Detail:   fmt.Sprintf("Volume %s will be created", vol.Name),
		})
	}

	for _, cf := range dd.ComposeFiles {
		changes = append(changes, DryRunChange{
			Type:     "add",
			Resource: "docker:compose:" + cf.Path,
			Detail:   fmt.Sprintf("Compose file %s will be uploaded and started", cf.Path),
		})
	}

	return changes
}
