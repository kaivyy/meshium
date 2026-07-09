package migration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"meshium/internal/shared"
)

// configExclusions is a list of file paths and directory prefixes that must
// never be overwritten during config migration. These are OS-critical files
// that would break the target server if replaced.
var configExclusions = []string{
	"/etc/fstab",
	"/etc/hostname",
	"/etc/machine-id",
	"/etc/hosts",
	"/etc/shadow",
	"/etc/passwd",
	"/etc/group",
	"/etc/subuid",
	"/etc/subgid",
	"/etc/resolv.conf",
	"/etc/network/",
	"/etc/netplan/",
	"/etc/sysconfig/network-scripts/",
	"/etc/udev/",
	"/etc/crypttab",
	"/etc/mdadm.conf",
	"/etc/dracut.conf",
	"/etc/kernel/",
	"/etc/grub.d/",
	"/etc/default/grub",
}

// maxConfigFileSize is the per-file cap for collected config content. A plan
// stores every file's full body in one migration_steps row, so an unbounded
// /etc scan writes ~100MB+ per plan (logs, caches, state DBs in /etc) and
// bloats SQLite. 1MiB covers real config files; anything larger is skipped.
// ponytail: raise or make per-path configurable if large config files must
// be migrated verbatim.
const maxConfigFileSize = 1 << 20 // 1 MiB

// isExcluded returns true if the given path matches any exclusion entry.
// Matches both exact file paths and directory prefixes (ending with /).
func isExcluded(path string) bool {
	for _, excl := range configExclusions {
		if strings.HasSuffix(excl, "/") {
			if strings.HasPrefix(path, excl) {
				return true
			}
		} else {
			if path == excl {
				return true
			}
		}
	}
	return false
}

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

// Collect downloads config files from the source server using tar streaming
// for maximum performance (single SSH round-trip instead of hundreds of SFTP reads).
func (c *ConfigsCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
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
			cleanPath = "/etc"
		}

		// Build a find command that excludes OS-critical files and oversized
		// files, then tar the result. The -size cap keeps logs/caches/state DBs
		// out of the archive so the plan step doesn't pull and store ~100MB+
		// per /etc scan. parseTarArchive re-checks the size as a guard.
		excludeArgs := buildExcludeArgs()
		cmd := fmt.Sprintf(
			`find %s -type f -size -2M %s 2>/dev/null | tar -cf - -T - 2>/dev/null | base64`,
			shared.ShellQuote(cleanPath), excludeArgs,
		)

		stdout, _, _, err := ssh.ExecContext(ctx, cmd)
		if err != nil {
			// Fallback: try individual file download (old method)
			c.collectSlow(ctx, ssh, cleanPath, &data)
			continue
		}

		// Decode base64 and parse tar archive
		tarData, err := base64Decode(stdout)
		if err != nil {
			c.collectSlow(ctx, ssh, cleanPath, &data)
			continue
		}

		// Parse tar archive in memory
		files, err := parseTarArchive(tarData)
		if err != nil {
			c.collectSlow(ctx, ssh, cleanPath, &data)
			continue
		}

		for path, content := range files {
			if isExcluded(path) {
				continue
			}
			data.Files[path] = content
		}
	}

	data.Count = len(data.Files)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "configs", Data: raw}, nil
}

// collectSlow is the fallback method that downloads files one-by-one via SFTP.
func (c *ConfigsCollector) collectSlow(ctx context.Context, ssh SSHExecuter, path string, data *ConfigsData) {
	stdout, _, _, err := ssh.ExecContext(ctx, fmt.Sprintf("find %s -type f 2>/dev/null", shared.ShellQuote(path)))
	if err != nil {
		return
	}

	for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
		file = strings.TrimSpace(file)
		if file == "" || isExcluded(file) {
			continue
		}
		buf := new(bytes.Buffer)
		if err := ssh.Download(file, buf); err != nil {
			continue
		}
		data.Files[file] = buf.Bytes()
	}
}

// buildExcludeArgs builds find exclude arguments for OS-critical files.
func buildExcludeArgs() string {
	var args []string
	for _, excl := range configExclusions {
		if strings.HasSuffix(excl, "/") {
			args = append(args, fmt.Sprintf("-not -path '%s*'", excl))
		} else {
			args = append(args, fmt.Sprintf("-not -name '%s'", filepath.Base(excl)))
		}
	}
	return strings.Join(args, " ")
}

// base64Decode decodes base64 encoded data, trimming whitespace.
func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty base64 input")
	}
	return base64.StdEncoding.DecodeString(s)
}

// parseTarArchive parses a tar archive in memory and returns map[path]content.
// Files larger than maxConfigFileSize are skipped: a plan stores every config's
// full content in one migration_steps row, so without a cap a single /etc scan
// writes ~100MB+ per plan and bloats the DB (and stalls the step). Large files
// in /etc are almost always logs/caches/state DBs, not real config.
func parseTarArchive(data []byte) (map[string][]byte, error) {
	files := make(map[string][]byte)
	r := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size > maxConfigFileSize {
			// Discard the body so the tar reader stays aligned.
			io.CopyN(io.Discard, r, header.Size)
			continue
		}
		content, err := io.ReadAll(r)
		if err != nil {
			continue
		}
		files[header.Name] = content
	}
	return files, nil
}

// ConfigsApplier uploads config files to the target server.
type ConfigsApplier struct{}

// Backup saves the target's current config files.
func (a *ConfigsApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	backup := ConfigsBackup{
		Files: make(map[string][]byte),
	}

	// Backup /etc/ on the target
	stdout, _, _, err := ssh.ExecContext(ctx, "find /etc -type f 2>/dev/null")
	if err != nil {
		return BackupData{}, err
	}

	for _, file := range strings.Split(strings.TrimSpace(stdout), "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		// Skip OS-critical files — they should never be overwritten
		if isExcluded(file) {
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
func (a *ConfigsApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
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
		// Safety net: skip OS-critical files even if they somehow got into the data
		if isExcluded(path) {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "configs:apply",
					Status: "warning",
					Value:  fmt.Sprintf("Skipping excluded file: %s", path),
				})
			}
			continue
		}
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
func (a *ConfigsApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
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
