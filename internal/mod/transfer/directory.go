package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// DirectoryTransfer transfers an entire directory from source to destination.
// It lists files at the source, transfers them one by one, and supports
// resume by skipping files that are already fully transferred (matched by
// filename + size).
type DirectoryTransfer struct {
	selector *StrategySelector
	verifier *ChecksumVerifier
}

// NewDirectoryTransfer creates a DirectoryTransfer with the given strategy selector.
func NewDirectoryTransfer(selector *StrategySelector) *DirectoryTransfer {
	return &DirectoryTransfer{
		selector: selector,
		verifier: NewChecksumVerifier(),
	}
}

// DirectoryTransferResult holds the outcome of a directory transfer.
type DirectoryTransferResult struct {
	TotalFiles       int           `json:"totalFiles"`
	TransferredFiles int           `json:"transferredFiles"`
	SkippedFiles     int           `json:"skippedFiles"`
	FailedFiles      int           `json:"failedFiles"`
	TotalBytes       int64         `json:"totalBytes"`
	TransferredBytes int64         `json:"transferredBytes"`
	Duration         time.Duration `json:"duration"`
	FileResults      []FileTransferInfo `json:"fileResults"`
}

// FileTransferInfo holds info about a single file in a directory transfer.
type FileTransferInfo struct {
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	Transferred  bool   `json:"transferred"`
	Skipped      bool   `json:"skipped"`
	Error        string `json:"error,omitempty"`
}

// Transfer transfers all files in a directory from src to dst.
//
// For each file:
//  1. Check if the file already exists at the destination with the same size
//     (resume by filename + size). If so, skip it.
//  2. Transfer the file using the selected strategy.
//  3. Verify the checksum.
//
// The progress callback receives aggregate progress for the entire directory.
func (dt *DirectoryTransfer) Transfer(
	ctx context.Context,
	src, dst TransferTarget,
	opts TransferOptions,
) (*DirectoryTransferResult, error) {
	start := time.Now()

	// List files at the source
	files, err := dt.listFiles(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("list source files: %w", err)
	}

	// Ensure destination directory exists
	if err := dt.ensureDestDir(ctx, dst); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}

	result := &DirectoryTransferResult{
		TotalFiles:  len(files),
		FileResults: make([]FileTransferInfo, 0, len(files)),
	}

	// Calculate total bytes
	for _, fi := range files {
		result.TotalBytes += fi.Size
	}

	var transferredBytes int64

	for _, fi := range files {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			result.TransferredBytes = transferredBytes
			return result, ctx.Err()
		default:
		}

		fileInfo := FileTransferInfo{
			RelativePath: fi.RelativePath,
			Size:         fi.Size,
		}

		// Build per-file targets
		fileSrc := src
		fileDst := dst
		if src.IsLocal {
			fileSrc.Path = filepath.Join(src.Path, fi.RelativePath)
		} else {
			fileSrc.Path = src.Path + "/" + fi.RelativePath
		}
		if dst.IsLocal {
			fileDst.Path = filepath.Join(dst.Path, fi.RelativePath)
		} else {
			fileDst.Path = dst.Path + "/" + fi.RelativePath
		}

		// Ensure parent directory exists at destination
		if err := dt.ensureParentDir(ctx, fileDst); err != nil {
			fileInfo.Error = fmt.Sprintf("create parent dir: %v", err)
			result.FailedFiles++
			result.FileResults = append(result.FileResults, fileInfo)
			continue
		}

		// Check if file already exists at destination with same size (resume)
		dstExists, _ := FileExists(ctx, fileDst)
		if dstExists {
			dstSize, err := GetFileSize(ctx, fileDst)
			if err == nil && dstSize == fi.Size {
				// File already fully transferred — skip
				fileInfo.Skipped = true
				fileInfo.Transferred = true
				result.SkippedFiles++
				result.TransferredFiles++
				transferredBytes += fi.Size
				result.FileResults = append(result.FileResults, fileInfo)
				continue
			}
		}

		// Select strategy for this file
		strategy := dt.selector.Select(ctx, fi.Size, opts, fileSrc)

		// Transfer the file
		fileOpts := opts
		// Wrap the progress callback to report aggregate progress
		baseTransferred := transferredBytes
		if opts.ProgressCallback != nil {
			outerCallback := opts.ProgressCallback
			fileOpts.ProgressCallback = func(p TransferProgress) {
				// Report aggregate progress
				outerCallback(TransferProgress{
					BytesTransferred: baseTransferred + p.BytesTransferred,
					TotalBytes:       result.TotalBytes,
					SpeedBPS:         p.SpeedBPS,
					ETA:              p.ETA,
					Percentage:       float64(baseTransferred+p.BytesTransferred) / float64(result.TotalBytes) * 100,
				})
			}
		}

		_, err = strategy.Transfer(ctx, fileSrc, fileDst, fileOpts)
		if err != nil {
			fileInfo.Error = err.Error()
			result.FailedFiles++
			result.FileResults = append(result.FileResults, fileInfo)
			continue
		}

		// Verify checksum
		_, err = dt.verifier.Verify(ctx, fileSrc, fileDst)
		if err != nil {
			fileInfo.Error = fmt.Sprintf("checksum verification failed: %v", err)
			result.FailedFiles++
			result.FileResults = append(result.FileResults, fileInfo)
			continue
		}

		fileInfo.Transferred = true
		result.TransferredFiles++
		transferredBytes += fi.Size
		result.FileResults = append(result.FileResults, fileInfo)
	}

	result.TransferredBytes = transferredBytes
	result.Duration = time.Since(start)
	return result, nil
}

// FileInfo holds information about a file in a directory listing.
type FileInfo struct {
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	Mode         string `json:"mode,omitempty"`
}

// listFiles lists all files in a directory at the given target.
// For local targets, it uses filepath.Walk. For remote targets, it uses
// `find` via SSH.
func (dt *DirectoryTransfer) listFiles(ctx context.Context, target TransferTarget) ([]FileInfo, error) {
	if target.IsLocal {
		return dt.listLocalFiles(target.Path)
	}
	if target.SSHClient == nil {
		return nil, fmt.Errorf("remote target has no SSH client")
	}
	return dt.listRemoteFiles(ctx, target.SSHClient, target.Path)
}

// listLocalFiles lists all files in a local directory.
func (dt *DirectoryTransfer) listLocalFiles(rootPath string) ([]FileInfo, error) {
	var files []FileInfo

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}

		files = append(files, FileInfo{
			RelativePath: filepath.ToSlash(relPath),
			Size:         info.Size(),
			Mode:         info.Mode().String(),
		})
		return nil
	})

	if err != nil {
		return nil, err
	}
	return files, nil
}

// listRemoteFiles lists all files in a remote directory via SSH.
// Uses `find` with `-type f` to list only files, and `-printf` to get
// size and relative path.
func (dt *DirectoryTransfer) listRemoteFiles(ctx context.Context, ssh transport.SSHExecuter, rootPath string) ([]FileInfo, error) {
	// Use find to list files with size and relative path
	// Format: size\tpath (relative to rootPath)
	cmd := fmt.Sprintf("cd '%s' && find . -type f -printf '%%s\\t%%P\\n' 2>/dev/null", rootPath)
	stdout, stderr, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list remote files: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("list remote files: exit code %d: %s", exitCode, stderr)
	}

	var files []FileInfo
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		var size int64
		fmt.Sscanf(parts[0], "%d", &size)
		files = append(files, FileInfo{
			RelativePath: parts[1],
			Size:         size,
		})
	}

	return files, nil
}

// ensureDestDir ensures the destination directory exists.
func (dt *DirectoryTransfer) ensureDestDir(ctx context.Context, dst TransferTarget) error {
	if dst.IsLocal {
		return os.MkdirAll(dst.Path, 0755)
	}
	if dst.SSHClient == nil {
		return fmt.Errorf("remote destination has no SSH client")
	}
	cmd := fmt.Sprintf("mkdir -p '%s'", dst.Path)
	_, stderr, exitCode, err := dst.SSHClient.ExecContext(ctx, cmd)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("mkdir failed: %s", stderr)
	}
	return nil
}

// ensureParentDir ensures the parent directory of a file exists at the destination.
func (dt *DirectoryTransfer) ensureParentDir(ctx context.Context, target TransferTarget) error {
	var dirPath string
	if target.IsLocal {
		dirPath = filepath.Dir(target.Path)
		return os.MkdirAll(dirPath, 0755)
	}
	if target.SSHClient == nil {
		return fmt.Errorf("remote target has no SSH client")
	}
	dirPath = filepath.Dir(target.Path)
	// For remote paths, use Unix-style separators
	dirPath = strings.ReplaceAll(dirPath, "\\", "/")
	cmd := fmt.Sprintf("mkdir -p '%s'", dirPath)
	_, stderr, exitCode, err := target.SSHClient.ExecContext(ctx, cmd)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("mkdir -p failed: %s", stderr)
	}
	return nil
}

// DirectoryResumeState holds the state of a directory transfer for resume.
type DirectoryResumeState struct {
	TransferID      string                   `json:"transferId"`
	CompletedFiles  map[string]int64         `json:"completedFiles"` // relativePath → size
}

// SerializeResumeState serializes a DirectoryResumeState to JSON.
func (s *DirectoryResumeState) Serialize() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeserializeResumeState deserializes a DirectoryResumeState from JSON.
func DeserializeResumeState(data string) (*DirectoryResumeState, error) {
	var state DirectoryResumeState
	err := json.Unmarshal([]byte(data), &state)
	return &state, err
}

// Verify verifies checksums for all files in the transferred directory.
// Returns a list of files that failed verification.
func (dt *DirectoryTransfer) Verify(ctx context.Context, src, dst TransferTarget) ([]string, error) {
	files, err := dt.listFiles(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("list source files: %w", err)
	}

	var failed []string
	for _, fi := range files {
		select {
		case <-ctx.Done():
			return failed, ctx.Err()
		default:
		}

		fileSrc := src
		fileDst := dst
		if src.IsLocal {
			fileSrc.Path = filepath.Join(src.Path, fi.RelativePath)
		} else {
			fileSrc.Path = src.Path + "/" + fi.RelativePath
		}
		if dst.IsLocal {
			fileDst.Path = filepath.Join(dst.Path, fi.RelativePath)
		} else {
			fileDst.Path = dst.Path + "/" + fi.RelativePath
		}

		_, err := dt.verifier.Verify(ctx, fileSrc, fileDst)
		if err != nil {
			failed = append(failed, fi.RelativePath)
		}
	}

	return failed, nil
}

// Rollback removes all transferred files from the destination directory.
func (dt *DirectoryTransfer) Rollback(ctx context.Context, dst TransferTarget) error {
	if dst.IsLocal {
		return os.RemoveAll(dst.Path)
	}
	if dst.SSHClient == nil {
		return fmt.Errorf("remote destination has no SSH client")
	}
	cmd := fmt.Sprintf("rm -rf '%s'", dst.Path)
	_, stderr, exitCode, err := dst.SSHClient.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("remove remote directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("remove remote directory: exit code %d: %s", exitCode, stderr)
	}
	return nil
}
