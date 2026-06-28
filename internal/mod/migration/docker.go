package migration

import (
	"bytes"
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
func (c *DockerCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	data := DockerData{}

	// Check if Docker is installed
	stdout, _, exitCode, err := ssh.Exec("which docker 2>/dev/null")
	if err != nil || exitCode != 0 || strings.TrimSpace(stdout) == "" {
		return CategoryData{Type: "docker", Data: []byte("{}")}, nil
	}

	// Collect running containers with JSON format
	stdout, _, _, err = ssh.Exec(`docker ps -a --format '{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}|{{.Ports}}' 2>/dev/null`)
	if err == nil {
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

			// Collect env vars for the container
			envOut, _, _, _ := ssh.Exec(fmt.Sprintf("docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' %s 2>/dev/null", shared.ShellQuote(container.ID)))
			if strings.TrimSpace(envOut) != "" {
				container.Env = make(map[string]string)
				for _, envLine := range strings.Split(strings.TrimSpace(envOut), "\n") {
					envLine = strings.TrimSpace(envLine)
					if envLine == "" {
						continue
					}
					if idx := strings.Index(envLine, "="); idx > 0 {
						container.Env[envLine[:idx]] = envLine[idx+1:]
					}
				}
			}

			// Collect labels
			labelOut, _, _, _ := ssh.Exec(fmt.Sprintf("docker inspect --format '{{range $k, $v := .Config.Labels}}{{println $k \"=\" $v}}{{end}}' %s 2>/dev/null", shared.ShellQuote(container.ID)))
			if strings.TrimSpace(labelOut) != "" {
				container.Labels = make(map[string]string)
				for _, labelLine := range strings.Split(strings.TrimSpace(labelOut), "\n") {
					labelLine = strings.TrimSpace(labelLine)
					if labelLine == "" {
						continue
					}
					if idx := strings.Index(labelLine, "="); idx > 0 {
						container.Labels[labelLine[:idx]] = labelLine[idx+1:]
					}
				}
			}

			data.Containers = append(data.Containers, container)
		}
	}

	// Collect images
	stdout, _, _, _ = ssh.Exec("docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			data.Images = append(data.Images, line)
		}
	}

	// Collect volumes
	stdout, _, _, _ = ssh.Exec(`docker volume ls --format '{{.Name}}|{{.Driver}}|{{.Mountpoint}}' 2>/dev/null`)
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
	stdout, _, _, _ = ssh.Exec(`find / -maxdepth 5 -name 'docker-compose*.yml' -o -name 'docker-compose*.yaml' -o -name 'compose*.yml' -o -name 'compose*.yaml' 2>/dev/null | head -20`)
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

// DockerApplier applies Docker state to the target server.
type DockerApplier struct{}

// Backup saves the target's current Docker state.
func (a *DockerApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	backup := DockerBackup{}

	// Save current container names
	stdout, _, _, _ := ssh.Exec("docker ps -a --format '{{.Names}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			backup.Containers = append(backup.Containers, line)
		}
	}

	// Save current images
	stdout, _, _, _ = ssh.Exec("docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "<none>:<none>" {
			backup.Images = append(backup.Images, line)
		}
	}

	// Save current volumes
	stdout, _, _, _ = ssh.Exec("docker volume ls --format '{{.Name}}' 2>/dev/null")
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			backup.Volumes = append(backup.Volumes, line)
		}
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "docker", Data: raw}, nil
}

// Apply pulls images, recreates compose files, and recreates containers on the target.
func (a *DockerApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var dd DockerData
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		return err
	}

	// Check if Docker is installed on target
	stdout, _, exitCode, err := ssh.Exec("which docker 2>/dev/null")
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

	// 2. Pull images
	for i, image := range dd.Images {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Pulling image %d/%d: %s", i+1, len(dd.Images), image),
			})
		}
		_, stderr, exitCode, _ := ssh.Exec(fmt.Sprintf("docker pull %s 2>&1", shared.ShellQuote(image)))
		if exitCode != 0 && onProgress != nil {
			onProgress(WSMessage{
				Step:   "docker:apply",
				Status: "warning",
				Value:  fmt.Sprintf("Failed to pull %s: %s", image, stderr),
			})
		}
	}

	// 3. Create volumes
	for _, vol := range dd.Volumes {
		_, _, _, _ = ssh.Exec(fmt.Sprintf("docker volume create %s 2>/dev/null", shared.ShellQuote(vol.Name)))
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
		_, stderr, exitCode, _ := ssh.Exec(fmt.Sprintf("cd %s && docker compose up -d 2>&1 || docker-compose up -d 2>&1", shared.ShellQuote(dir)))
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
			_, stderr, exitCode, _ := ssh.Exec(cmd + " 2>&1")
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
		onProgress(WSMessage{
			Step:   "docker:apply",
			Status: "success",
			Value:  fmt.Sprintf("Docker migration complete: %d containers, %d images, %d volumes, %d compose files", len(dd.Containers), len(dd.Images), len(dd.Volumes), len(dd.ComposeFiles)),
		})
	}

	return nil
}

// Rollback removes containers, images, and volumes that were added by the migration.
func (a *DockerApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var db DockerBackup
	if err := json.Unmarshal(backup.Data, &db); err != nil {
		return err
	}

	// Get current containers
	stdout, _, _, _ := ssh.Exec("docker ps -a --format '{{.Names}}' 2>/dev/null")
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
			ssh.Exec(fmt.Sprintf("docker rm -f %s 2>/dev/null", shared.ShellQuote(name)))
		}
	}

	// Get current images
	stdout, _, _, _ = ssh.Exec("docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null")
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
			ssh.Exec(fmt.Sprintf("docker rmi %s 2>/dev/null", shared.ShellQuote(image)))
		}
	}

	// Get current volumes
	stdout, _, _, _ = ssh.Exec("docker volume ls --format '{{.Name}}' 2>/dev/null")
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
			ssh.Exec(fmt.Sprintf("docker volume rm %s 2>/dev/null", shared.ShellQuote(vol)))
		}
	}

	return nil
}
