package migration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigsData holds collected config files from the source server.
type ConfigsData struct {
	Files map[string][]byte `json:"files"` // path -> content
	Count int               `json:"count"`
}

// ConfigsBackup holds the target's original config files.
type ConfigsBackup struct {
	Files map[string][]byte `json:"files"`
}

// ConfigsCollector collects config files from the source server via SFTP.
type ConfigsCollector struct {
	Paths []string // paths to collect (default: /etc/)
}

// Collect downloads config files from the source server.
func (c *ConfigsCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"/etc/"}
	}

	data := ConfigsData{
		Files: make(map[string][]byte),
	}

	for _, path := range paths {
		// List files in the directory
		stdout, _, _, err := ssh.Exec(fmt.Sprintf("find %s -type f 2>/dev/null", strings.TrimRight(path, "/")))
		if err != nil {
			continue // non-fatal
		}

		for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
			file = strings.TrimSpace(file)
			if file == "" {
				continue
			}

			// Download the file content
			buf := new(bytes.Buffer)
			if err := ssh.Download(file, buf); err != nil {
				continue // non-fatal
			}
			data.Files[file] = buf.Bytes()
		}
	}

	data.Count = len(data.Files)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "configs", Data: raw}, nil
}

// ConfigsApplier uploads config files to the target server.
type ConfigsApplier struct{}

// Backup saves the target's current config files.
func (a *ConfigsApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	backup := ConfigsBackup{
		Files: make(map[string][]byte),
	}

	// Backup /etc/ on the target
	stdout, _, _, err := ssh.Exec("find /etc -type f 2>/dev/null")
	if err != nil {
		return BackupData{}, err
	}

	for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		buf := new(bytes.Buffer)
		if err := ssh.Download(file, buf); err != nil {
			continue
		}
		backup.Files[file] = buf.Bytes()
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "configs", Data: raw}, nil
}

// Apply uploads config files to the target server.
func (a *ConfigsApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		return err
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "configs:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Uploading %d config files", cd.Count),
		})
	}

	count := 0
	for path, content := range cd.Files {
		if err := ssh.Upload(bytes.NewReader(content), path); err != nil {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "configs:apply",
					Status: "error",
					Error:  fmt.Sprintf("failed to upload %s: %v", path, err),
				})
			}
			return fmt.Errorf("failed to upload %s: %w", path, err)
		}
		count++
		if onProgress != nil && count%10 == 0 {
			onProgress(WSMessage{
				Step:   "configs:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Uploaded %d/%d", count, cd.Count),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "configs:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d config files uploaded", count),
		})
	}

	return nil
}

// Rollback restores the target's original config files.
func (a *ConfigsApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var cb ConfigsBackup
	if err := json.Unmarshal(backup.Data, &cb); err != nil {
		return err
	}

	for path, content := range cb.Files {
		if err := ssh.Upload(bytes.NewReader(content), path); err != nil {
			// Continue even if some files fail
			continue
		}
	}

	return nil
}
