package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

		changes := e.computeDryRunChanges(ctx, sshClient, step.Category, data, mod)

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

	// Persist the preview so step 4 keeps its change list across a page
	// refresh (otherwise dryRunResult only lives in frontend memory and is
	// lost on reload). Reuses migration_steps with action='dryrun'.
	if data, err := json.Marshal(result); err == nil {
		if _, serr := e.repo.CreateStep(migrationID, "all", "dryrun", string(data)); serr != nil {
			// Non-fatal: the live result is still returned to the caller.
			onProgress(WSMessage{Step: "dryrun", Status: "warning", Value: "could not persist dry run preview"})
		}
	}

	onProgress(WSMessage{Step: "dryrun", Status: "complete", Value: fmt.Sprintf("Dry run complete: %d total changes", result.Summary.TotalChanges)})

	return result, nil
}

// computeDryRunChanges compares source data with target state for a category.
func (e *Executor) computeDryRunChanges(ctx context.Context, ssh SSHExecuter, category string, data CategoryData, mod CategoryModule) []DryRunChange {
	switch category {
	case "packages":
		return e.dryRunPackages(ctx, ssh, data)
	case "configs":
		return e.dryRunConfigs(ctx, ssh, data)
	case "services":
		return e.dryRunServices(ctx, ssh, data)
	case "users":
		return e.dryRunUsers(ctx, ssh, data)
	case "docker":
		return e.dryRunDocker(ctx, ssh, data)
	default:
		return nil
	}
}

func (e *Executor) dryRunPackages(ctx context.Context, ssh SSHExecuter, data CategoryData) []DryRunChange {
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		return nil
	}

	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return nil
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return []DryRunChange{{
			Type:     "blocked",
			Resource: "packages",
			Detail:   fmt.Sprintf("package migration is not supported on this distro: %v", err),
		}}
	}

	stdout, _, _, _ := ssh.ExecContext(ctx, adapter.ListPackages())
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

// maxHashPaths bounds how many paths we pass to a single sha256sum call.
// Above this we fall back to the old per-file approach; a single huge shell
// command is its own latency/quoting risk.
const maxHashPaths = 2000

// dryRunConfigs compares the planned config files against the target's current
// state. The naive path is one SFTP Download per file (N+1 round-trips) which
// is the dominant cost of Dry Run on targets with many files. Instead we ask the
// target for the sha256 of every planned path in ONE command, then flag a file
// as "add" (missing from the checksum output) or "modify" (hash differs from
// the hash of the source content) without downloading anything. Falls back to the
// per-file Download only when sha256sum is unavailable or there are too many
// paths — so behavior never silently degrades.
func (e *Executor) dryRunConfigs(ctx context.Context, ssh SSHExecuter, data CategoryData) []DryRunChange {
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return nil
	}

	// Compute the source-content hash for every planned file once, in memory.
	sourceHashes := make(map[string][32]byte, len(cd.Files))
	paths := make([]string, 0, len(cd.Files))
	for path, content := range cd.Files {
		paths = append(paths, path)
		sourceHashes[path] = sha256.Sum256([]byte(content))
	}
	sort.Strings(paths)

	if len(paths) == 0 || len(paths) > maxHashPaths {
		return e.dryRunConfigsFallback(ctx, ssh, cd)
	}

	// Build the checksum command. Quote each path; collect stdout into one pass.
	if len(paths) == 0 {
		return nil
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellQuote(p)
	}
	cmd := "sha256sum " + strings.Join(quoted, " ")
	stdout, _, exitCode, err := ssh.ExecContext(ctx, cmd)
	// sha256sum exits 1 when any listed path is missing — that is the normal
	// "add" case, not a failure. Only fall back when the binary is absent
	// (127/126: command not found / not executable) or the command never ran.
	// ponytail: a target with a sha256sum shim that exits 1 on success would
	// force the fallback; not seen in practice — if it appears, detect via stderr.
	if err != nil || exitCode == 127 || exitCode == 126 {
		return e.dryRunConfigsFallback(ctx, ssh, cd)
	}

	// sha256sum output: "<hash>  <path>" per line.
	targetHashes := make(map[string][32]byte)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var h [32]byte
		if _, derr := hex.Decode(h[:], []byte(fields[0])); derr != nil {
			continue
		}
		targetHashes[fields[1]] = h
	}

	var changes []DryRunChange
	for _, path := range paths {
		want, ok := targetHashes[path]
		switch {
		case !ok:
			changes = append(changes, DryRunChange{
				Type:     "add",
				Resource: "file:" + path,
				Detail:   fmt.Sprintf("File %s will be created", path),
			})
		case want != sourceHashes[path]:
			changes = append(changes, DryRunChange{
				Type:     "modify",
				Resource: "file:" + path,
				Detail:   fmt.Sprintf("File %s will be overwritten (content differs)", path),
			})
		}
	}
	return changes
}

// dryRunConfigsFallback is the pre-fix per-file path: download each target
// file and compare in memory. Kept as a fallback for targets without sha256sum
// or with path counts above maxHashPaths.
func (e *Executor) dryRunConfigsFallback(ctx context.Context, ssh SSHExecuter, cd ConfigsData) []DryRunChange {
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

// shellQuote single-quotes a path for safe inclusion in a shell command.
func shellQuote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func (e *Executor) dryRunServices(ctx context.Context, ssh SSHExecuter, data CategoryData) []DryRunChange {
	var sd ServicesData
	if err := json.Unmarshal(data.Data, &sd); err != nil {
		return nil
	}

	stdout, _, _, _ := ssh.ExecContext(ctx, "systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null")
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

func (e *Executor) dryRunUsers(ctx context.Context, ssh SSHExecuter, data CategoryData) []DryRunChange {
	var ud UsersData
	if err := json.Unmarshal(data.Data, &ud); err != nil {
		return nil
	}

	stdout, _, _, _ := ssh.ExecContext(ctx, "cat /etc/passwd")
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

func (e *Executor) dryRunDocker(ctx context.Context, ssh SSHExecuter, data CategoryData) []DryRunChange {
	var dd DockerData
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		return nil
	}

	// Check if Docker is installed
	stdout, _, exitCode, _ := ssh.ExecContext(ctx, "which docker 2>/dev/null")
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
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
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
				Detail:   fmt.Sprintf("Image %s will be pulled from a registry (locally-built images not pushed to a reachable registry cannot be migrated)", image),
			})
		}
	}

	// Get existing containers
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker ps -a --format '{{.Names}}' 2>/dev/null")
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
