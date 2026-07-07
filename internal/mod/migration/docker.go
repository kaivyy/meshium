package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"meshium/internal/shared"
)

// DockerContainer represents a running container on the source server.
type DockerContainer struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	Status  string            `json:"status"`
	Ports   string            `json:"ports"`
	Env     map[string]string `json:"env,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// DockerVolume represents a Docker volume.
type DockerVolume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
}

// DockerComposeFile represents a docker-compose.yml found on the source.
type DockerComposeFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
}

// DockerData holds all Docker state collected from the source server.
type DockerData struct {
	Containers  []DockerContainer  `json:"containers"`
	Images      []string           `json:"images"`
	Volumes     []DockerVolume     `json:"volumes"`
	ComposeFiles []DockerComposeFile `json:"composeFiles"`
	Count       int               `json:"count"`
}

// DockerBackup holds the target's Docker state before migration.
type DockerBackup struct {
	Containers  []string `json:"containers"`
	Images      []string `json:"images"`
	Volumes     []string `json:"volumes"`
}

// DockerCollector collects Docker state from the source server.
type DockerCollector struct{}

// Collect gathers running containers, images, volumes, and compose files.
func (c *DockerCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	data := DockerData{}

	// Check if Docker is installed
	stdout, _, exitCode, err := ssh.ExecContext(ctx, "which docker 2>/dev/null")
	if err != nil || exitCode != 0 || strings.TrimSpace(stdout) == "" {
		return CategoryData{Type: "docker", Data: []byte("{}")}, nil
	}

	// Collect running containers with JSON format
	stdout, _, _, err = ssh.ExecContext(ctx, `docker ps -a --format '{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}' 2>/dev/null`)
	if err == nil {
		containerIDs := make([]string, 0)
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|", 5)
			if len(parts) < 5 {
				continue
			}
			container := DockerContainer{
				ID:     parts[0],
				Name:   parts[1],
				Image:  parts[2],
				Status: parts[3],
				Ports:  parts[4],
			}
			data.Containers = append(data.Containers, container)
			containerIDs = append(containerIDs, container.ID)
		}

		// BATCH: Get env vars and labels for ALL containers in ONE command each
		if len(containerIDs) > 0 {
			// Batch env vars: use docker inspect with format that outputs container_id|env_line
			idList := strings.Join(containerIDs, " ")
			envOut, _, _, _ := ssh.ExecContext(ctx, fmt.Sprintf(
				`docker inspect --format '{{.Id}}|{{range .Config.Env}}{{println .}}{{end}}|||' %s 2>/dev/null`, idList))
			if strings.TrimSpace(envOut) != "" {
				// Parse batch env output: container_id|env_line\nenv_line\n|||\ncontainer_id|...
				envMap := parseBatchInspect(envOut)
				for i := range data.Containers {
					if envs, ok := envMap[data.Containers[i].ID]; ok {
						data.Containers[i].Env = envs
					}
				}
			}

			// Batch labels: same approach
			labelOut, _, _, _ := ssh.ExecContext(ctx, fmt.Sprintf(
				`docker inspect --format '{{.Id}}|{{range $k, $v := .Config.Labels}}{{println $k "=" $v}}{{end}}|||' %s 2>/dev/null`, idList))
			if strings.TrimSpace(labelOut) != "" {
				labelMap := parseBatchInspect(labelOut)
				for i := range data.Containers {
					if labels, ok := labelMap[data.Containers[i].ID]; ok {
						data.Containers[i].Labels = labels
					}
				}
			}
		}
	}

	// Collect images
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			data.Images = append(data.Images, line)
		}
	}

	// Collect volumes
	stdout, _, _, _ = ssh.ExecContext(ctx, `docker volume ls --format '{{.Name}}|{{.Driver}}|{{.Mountpoint}}' 2>/dev/null`)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) >= 3 {
			data.Volumes = append(data.Volumes, DockerVolume{
				Name:       parts[0],
				Driver:     parts[1],
				Mountpoint: parts[2],
			})
		}
	}

	// Collect docker-compose files
	stdout, _, _, _ = ssh.ExecContext(ctx, `find / -maxdepth 5 -name 'docker-compose*.yml' -o -name 'docker-compose*.yaml' -o -name 'compose*.yml' -o -name 'compose*.yaml' 2>/dev/null | head -20`)
	for _, filePath := range strings.Split(strings.TrimSpace(stdout), "\n") {
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			continue
		}
		buf := new(bytes.Buffer)
		if err := ssh.Download(filePath, buf); err == nil {
			data.ComposeFiles = append(data.ComposeFiles, DockerComposeFile{
				Path:    filePath,
				Content: buf.String(),
			})
		}
	}

	data.Count = len(data.Containers) + len(data.Images) + len(data.Volumes) + len(data.ComposeFiles)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "docker", Data: raw}, nil
}

// parseBatchInspect parses the output of batch docker inspect.
// Format: containerId|key=value\nkey=value\n|||\ncontainerId|key=value\n...
// Returns map[containerID]map[key]value
func parseBatchInspect(output string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	currentID := ""
	currentMap := make(map[string]string)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "|||" {
			if currentID != "" {
				result[currentID] = currentMap
			}
			currentID = ""
			currentMap = make(map[string]string)
			continue
		}
		if idx := strings.Index(line, "|"); idx > 0 {
			// New container section
			if currentID != "" {
				result[currentID] = currentMap
			}
			currentID = line[:idx]
			rest := line[idx+1:]
			if rest != "" {
				if eqIdx := strings.Index(rest, "="); eqIdx > 0 {
					currentMap = make(map[string]string)
					currentMap[rest[:eqIdx]] = rest[eqIdx+1:]
				}
			} else {
				currentMap = make(map[string]string)
			}
			continue
		}
		// key=value line within current container
		if idx := strings.Index(line, "="); idx > 0 && currentID != "" {
			currentMap[line[:idx]] = line[idx+1:]
		}
	}
	if currentID != "" {
		result[currentID] = currentMap
	}
	return result
}

// DockerApplier applies Docker state to the target server.
type DockerApplier struct{}

// Backup saves the target's current Docker state.
func (a *DockerApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	backup := DockerBackup{}

	// Save current container names
	stdout, _, _, _ := ssh.ExecContext(ctx, "docker ps -a --format '{{.Names}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			backup.Containers = append(backup.Containers, line)
		}
	}

	// Save current images
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			backup.Images = append(backup.Images, line)
		}
	}

	// Save current volumes
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker volume ls --format '{{.Name}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			backup.Volumes = append(backup.Volumes, line)
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "docker", Data: raw}, nil
}

// Apply pulls images, recreates compose files, and recreates containers on the
// target.
//
// Image migration is REGISTRY-ONLY: each image is transferred with `docker pull`
// (step 2 below), so it must exist in a registry the target can reach. Images
// that were built locally on the source and never pushed to a registry cannot
// be migrated by this path — their pull is reported as a warning and the
// migration continues, so any container depending on such an image will fail to
// start. Saving/streaming image tarballs (docker save|load) is not implemented.
func (a *DockerApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var dd DockerData
	if len(data.Data) == 0 {
		// Docker not present on source — nothing to apply
		return nil
	}
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		return err
	}

	// Check if Docker is installed on target
	stdout, _, exitCode, err := ssh.ExecContext(ctx, "which docker 2>/dev/null")
	if err != nil || exitCode != 0 || strings.TrimSpace(stdout) == "" {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "error",
				Error:  "Docker is not installed on the target server",
			})
		}
		return fmt.Errorf("docker is not installed on the target server")
	}

	// 1. Upload compose files
	for _, cf := range dd.ComposeFiles {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Uploading %s", cf.Path),
			})
		}
		if err := ssh.Upload(bytes.NewReader([]byte(cf.Content)), cf.Path); err != nil {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "docker:apply",
					Status: "warning",
					Value:  fmt.Sprintf("Failed to upload %s: %v", cf.Path, err),
				})
			}
			continue
		}
	}

	// 2. Pull images (registry-only). A locally-built image that was never
	// pushed to a registry the target can reach will fail here; that failure is
	// surfaced as a warning rather than aborting, so the operator can see which
	// images did not transfer.
	pullFailures := 0
	for i, image := range dd.Images {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Pulling image %d/%d from registry: %s", i+1, len(dd.Images), image),
			})
		}
		_, stderr, exitCode, _ := ssh.ExecContext(ctx, fmt.Sprintf("docker pull %s 2>&1", shared.ShellQuote(image)))
		if exitCode != 0 {
			pullFailures++
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "docker:apply",
					Status: "warning",
					Value:  fmt.Sprintf("Failed to pull %s from registry (locally-built images that were never pushed cannot be migrated): %s", image, stderr),
				})
			}
		}
	}

	// 3. Create volumes
	for _, vol := range dd.Volumes {
		_, _, _, _ = ssh.ExecContext(ctx, fmt.Sprintf("docker volume create %s 2>/dev/null", shared.ShellQuote(vol.Name)))
	}

	// 4. Recreate containers from compose files
	for _, cf := range dd.ComposeFiles {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Running docker-compose up for %s", cf.Path),
			})
		}
		dir := cf.Path
		if idx := strings.LastIndex(dir, "/"); idx >= 0 {
			dir = dir[:idx]
		}
		_, stderr, exitCode, _ := ssh.ExecContext(ctx, fmt.Sprintf("cd %s && docker compose up -d 2>&1 || docker-compose up -d 2>&1", shared.ShellQuote(dir)))
		if exitCode != 0 && onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "warning",
				Value:  fmt.Sprintf("docker-compose up failed for %s: %s", cf.Path, stderr),
			})
		}
	}

	// 5. If no compose files, try to recreate containers directly
	if len(dd.ComposeFiles) == 0 {
		for _, container := range dd.Containers {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "docker:apply",
					Status: "progress",
					Value:  fmt.Sprintf("Recreating container %s", container.Name),
				})
			}
			// Build docker run command from collected data
			cmd := fmt.Sprintf("docker run -d --name %s", shared.ShellQuote(container.Name))
			if container.Env != nil {
				for k, v := range container.Env {
					cmd += fmt.Sprintf(" -e %s=%s", shared.ShellQuote(k), shared.ShellQuote(v))
				}
			}
			if container.Labels != nil {
				for k, v := range container.Labels {
					cmd += fmt.Sprintf(" --label %s=%s", shared.ShellQuote(k), shared.ShellQuote(v))
				}
			}
			cmd += fmt.Sprintf(" %s", shared.ShellQuote(container.Image))
			_, stderr, exitCode, _ := ssh.ExecContext(ctx, cmd + " 2>&1")
			if exitCode != 0 && onProgress != nil {
				onProgress(WSMessage{
					Step:   "docker:apply",
					Status: "warning",
					Value:  fmt.Sprintf("Failed to recreate %s: %s", container.Name, stderr),
				})
			}
		}
	}

	if onProgress != nil {
		summary := fmt.Sprintf("Docker migration finished: %d containers, %d images (registry pull), %d volumes, %d compose files", len(dd.Containers), len(dd.Images), len(dd.Volumes), len(dd.ComposeFiles))
		if pullFailures > 0 {
			summary += fmt.Sprintf("; %d image(s) failed to pull from a registry and were NOT migrated (see warnings above)", pullFailures)
		}
		onProgress(WSMessage{
			Step:   "docker:apply",
			Status: "success",
			Value:  summary,
		})
	}

	return nil
}

// Rollback removes containers, images, and volumes that were added by the migration.
func (a *DockerApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	var db DockerBackup
	if err := json.Unmarshal(backup.Data, &db); err != nil {
		return err
	}

	// Get current containers
	stdout, _, _, _ := ssh.ExecContext(ctx, "docker ps -a --format '{{.Names}}' 2>/dev/null")
	currentContainers := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			currentContainers[line] = true
		}
	}

	// Remove containers that weren't in the backup
	for name := range currentContainers {
		if !contains(db.Containers, name) {
			ssh.ExecContext(ctx, fmt.Sprintf("docker rm -f %s 2>/dev/null", shared.ShellQuote(name)))
		}
	}

	// Get current images
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	currentImages := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			currentImages[line] = true
		}
	}

	// Remove images that weren't in the backup
	for image := range currentImages {
		if !contains(db.Images, image) {
			ssh.ExecContext(ctx, fmt.Sprintf("docker rmi %s 2>/dev/null", shared.ShellQuote(image)))
		}
	}

	// Get current volumes
	stdout, _, _, _ = ssh.ExecContext(ctx, "docker volume ls --format '{{.Name}}' 2>/dev/null")
	currentVolumes := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			currentVolumes[line] = true
		}
	}

	// Remove volumes that weren't in the backup
	for vol := range currentVolumes {
		if !contains(db.Volumes, vol) {
			ssh.ExecContext(ctx, fmt.Sprintf("docker volume rm %s 2>/dev/null", shared.ShellQuote(vol)))
		}
	}

	return nil
}
