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

// sensitiveFiles are files that should never be collected or backed up
// because they contain password hashes, private keys, or other secrets.
var sensitiveFiles = map[string]bool{
	"/etc/shadow":     true,
	"/etc/gshadow":    true,
	"/etc/shadow-":    true,
	"/etc/gshadow-":   true,
	"/etc/ssh/ssh_host_rsa_key":     true,
	"/etc/ssh/ssh_host_dsa_key":     true,
	"/etc/ssh/ssh_host_ecdsa_key":   true,
	"/etc/ssh/ssh_host_ed25519_key": true,
}

// isSensitiveFile returns true if the file path is a sensitive file
// that should not be collected or backed up.
func isSensitiveFile(path string) bool {
	return sensitiveFiles[path]
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
		cleanPath := strings.TrimRight(path, "/")
		if cleanPath == "" {
			cleanPath = "/"
		}

		candidates := []string{path}
		if cleanPath != path {
			candidates = append(candidates, cleanPath)
		}

		for _, candidate := range candidates {
			if !validatePath(candidate) {
				continue
			}

			stdout, _, _, err := ssh.Exec(fmt.Sprintf("find %s -type f 2>/dev/null", shellQuote(candidate)))
			if err != nil {
				continue // non-fatal
			}

			for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
				file = strings.TrimSpace(file)
				if file == "" {
					continue
				}
				if isSensitiveFile(file) {
					continue // skip sensitive files
				}

				buf := new(bytes.Buffer)
				if err := ssh.Download(file, buf); err != nil {
					continue // non-fatal
				}
				data.Files[file] = buf.Bytes()
			}

			if len(data.Files) > 0 {
				break
			}
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
		if isSensitiveFile(file) {
			continue // skip sensitive files
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
