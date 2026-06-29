package transfer

import (
	"encoding/json"
	"fmt"
	"strings"

	"meshium/internal/mod/migration"
)

// FileTransferStep implements migration.MigrationStep for file transfers.
// It transfers a file (or directory) from a source to a destination,
// with checksum verification and rollback support.
type FileTransferStep struct {
	StepName  string
	Source    TransferTarget
	Dest      TransferTarget
	Selector  *StrategySelector
	Verifier  *ChecksumVerifier
	Options   TransferOptions

	// Internal state set during Prepare
	selectedStrategy TransferStrategy
	fileSize         int64
}

// NewFileTransferStep creates a FileTransferStep.
func NewFileTransferStep(name string, src, dst TransferTarget, opts TransferOptions) *FileTransferStep {
	return &FileTransferStep{
		StepName: name,
		Source:   src,
		Dest:     dst,
		Selector: NewStrategySelector(),
		Verifier: NewChecksumVerifier(),
		Options:  opts,
	}
}

// Name returns the step name.
func (s *FileTransferStep) Name() string { return s.StepName }

// PrepareData is the data returned by Prepare and passed to Apply.
type PrepareData struct {
	Strategy  string `json:"strategy"`
	FileSize  int64  `json:"fileSize"`
	DiskFree  int64  `json:"diskFree"`
	Resumed   bool   `json:"resumed"`
}

// ApplyData is the data returned by Apply and passed to Verify.
type ApplyData struct {
	BytesTransferred int64  `json:"bytesTransferred"`
	Checksum         string `json:"checksum"`
	Strategy         string `json:"strategy"`
	Resumed           bool   `json:"resumed"`
}

// VerifyData is the data returned by Verify and stored as a checkpoint.
type VerifyData struct {
	Checksum string `json:"checksum"`
	Verified bool   `json:"verified"`
}

// Prepare validates that the transfer can proceed:
//   - Checks that the source file exists
//   - Gets the source file size
//   - Checks available disk space at the destination
//   - Selects the appropriate transfer strategy
func (s *FileTransferStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	// Check source file exists
	exists, err := FileExists(ctx, s.Source)
	if err != nil {
		return "", fmt.Errorf("check source exists: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("source file does not exist: %s", s.Source.Path)
	}

	// Get source file size
	fileSize, err := GetFileSize(ctx, s.Source)
	if err != nil {
		return "", fmt.Errorf("get source file size: %w", err)
	}
	s.fileSize = fileSize

	// Check disk space at destination (remote only)
	var diskFree int64
	if !s.Dest.IsLocal && s.Dest.SSHClient != nil {
		diskFree, err = GetRemoteDiskFree(ctx, s.Dest.SSHClient, s.Dest.Path)
		if err != nil {
			// Log warning but don't fail — disk space check is best-effort
			if sctx.Progress != nil {
				sctx.Progress(migration.WSMessage{
					Step:   s.StepName,
					Status: "warning",
					Value:  fmt.Sprintf("Could not check disk space: %v", err),
				})
			}
		} else if diskFree < fileSize {
			return "", fmt.Errorf("insufficient disk space at destination: have %d bytes, need %d bytes", diskFree, fileSize)
		}
	}

	// Select transfer strategy
	strategy := s.Selector.Select(ctx, fileSize, s.Options, s.Source)
	s.selectedStrategy = strategy

	// Check if resuming
	resumed := false
	if s.Options.Resume {
		dstExists, _ := FileExists(ctx, s.Dest)
		if dstExists {
			dstSize, _ := GetFileSize(ctx, s.Dest)
			if dstSize > 0 && dstSize < fileSize {
				resumed = true
			}
		}
	}

	data := PrepareData{
		Strategy: strategy.Name(),
		FileSize: fileSize,
		DiskFree: diskFree,
		Resumed:  resumed,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal prepare data: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Prepared: strategy=%s, size=%d bytes, resumed=%v", strategy.Name(), fileSize, resumed),
		})
	}

	return string(jsonData), nil
}

// Apply performs the file transfer using the selected strategy.
// It wraps the transfer with progress tracking and emits progress
// via the StepContext's progress callback.
func (s *FileTransferStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	if s.selectedStrategy == nil {
		// Select strategy if Prepare wasn't called (shouldn't happen)
		s.selectedStrategy = s.Selector.Select(ctx, s.fileSize, s.Options, s.Source)
	}

	// Wrap progress callback to use migration.WSMessage
	opts := s.Options
	if sctx.Progress != nil {
		originalCallback := opts.ProgressCallback
		opts.ProgressCallback = func(p TransferProgress) {
			sctx.Progress(migration.WSMessage{
				Step:   s.StepName,
				Status: "progress",
				Value:  fmt.Sprintf("%.1f%% — %d/%d bytes — %d B/s", p.Percentage, p.BytesTransferred, p.TotalBytes, p.SpeedBPS),
			})
			if originalCallback != nil {
				originalCallback(p)
			}
		}
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Starting transfer via %s", s.selectedStrategy.Name()),
		})
	}

	result, err := s.selectedStrategy.Transfer(ctx, s.Source, s.Dest, opts)
	if err != nil {
		return "", fmt.Errorf("transfer failed: %w", err)
	}

	data := ApplyData{
		BytesTransferred: result.BytesTransferred,
		Strategy:         result.Strategy,
		Resumed:          result.Resumed,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal apply data: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Transfer complete: %d bytes via %s", result.BytesTransferred, result.Strategy),
		})
	}

	return string(jsonData), nil
}

// Verify computes the SHA256 checksum at both source and destination
// and compares them. If they match, the transfer is verified.
func (s *FileTransferStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Verifying checksums...",
		})
	}

	checksum, err := s.Verifier.Verify(ctx, s.Source, s.Dest)
	if err != nil {
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}

	data := VerifyData{
		Checksum: checksum,
		Verified: true,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal verify data: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Checksum verified: %s", checksum[:16]+"..."),
		})
	}

	return string(jsonData), nil
}

// Rollback removes the transferred file from the destination.
// This cleans up any partial or complete file that was transferred.
func (s *FileTransferStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back: removing %s", s.Dest.Path),
		})
	}

	err := DeleteFile(ctx, s.Dest)
	if err != nil {
		return fmt.Errorf("rollback failed (could not delete file): %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: file removed",
		})
	}

	return nil
}

// --- DirectoryTransferStep ---

// DirectoryTransferStep implements migration.MigrationStep for directory
// transfers. It transfers all files in a directory, with resume support
// (skipping files that are already transferred and match in size).
type DirectoryTransferStep struct {
	StepName string
	Source   TransferTarget
	Dest     TransferTarget
	Selector *StrategySelector
	Verifier *ChecksumVerifier
	Options  TransferOptions

	// Internal state
	dirTransfer *DirectoryTransfer
}

// NewDirectoryTransferStep creates a DirectoryTransferStep.
func NewDirectoryTransferStep(name string, src, dst TransferTarget, opts TransferOptions) *DirectoryTransferStep {
	return &DirectoryTransferStep{
		StepName: name,
		Source:   src,
		Dest:     dst,
		Selector: NewStrategySelector(),
		Verifier: NewChecksumVerifier(),
		Options:  opts,
	}
}

// Name returns the step name.
func (s *DirectoryTransferStep) Name() string { return s.StepName }

// Prepare validates that the source directory exists and lists its files.
func (s *DirectoryTransferStep) Prepare(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	// Create directory transfer
	s.dirTransfer = NewDirectoryTransfer(s.Selector)

	// List files in source directory
	files, err := s.dirTransfer.listFiles(ctx, s.Source)
	if err != nil {
		return "", fmt.Errorf("list source files: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  fmt.Sprintf("Found %d files to transfer", len(files)),
		})
	}

	data := map[string]interface{}{
		"fileCount": len(files),
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal prepare data: %w", err)
	}

	return string(jsonData), nil
}

// Apply transfers all files in the directory.
func (s *DirectoryTransferStep) Apply(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	if s.dirTransfer == nil {
		s.dirTransfer = NewDirectoryTransfer(s.Selector)
	}

	// Wrap progress callback to use migration.WSMessage
	opts := s.Options
	if sctx.Progress != nil {
		originalCallback := opts.ProgressCallback
		opts.ProgressCallback = func(p TransferProgress) {
			sctx.Progress(migration.WSMessage{
				Step:   s.StepName,
				Status: "progress",
				Value:  fmt.Sprintf("%.1f%% — %d/%d bytes — %d B/s", p.Percentage, p.BytesTransferred, p.TotalBytes, p.SpeedBPS),
			})
			if originalCallback != nil {
				originalCallback(p)
			}
		}
	}

	result, err := s.dirTransfer.Transfer(ctx, s.Source, s.Dest, opts)
	if err != nil {
		return "", fmt.Errorf("directory transfer failed: %w", err)
	}

	data := map[string]interface{}{
		"filesTransferred": result.TransferredFiles,
		"filesSkipped":     result.SkippedFiles,
		"filesFailed":      result.FailedFiles,
		"bytesTransferred": result.TransferredBytes,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal apply data: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  fmt.Sprintf("Directory transfer: %d files, %d bytes", result.TransferredFiles, result.TransferredBytes),
		})
	}

	return string(jsonData), nil
}

// Verify computes checksums for all transferred files.
func (s *DirectoryTransferStep) Verify(sctx migration.StepContext) (string, error) {
	ctx := sctx.Ctx

	if s.dirTransfer == nil {
		s.dirTransfer = NewDirectoryTransfer(s.Selector)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Verifying directory checksums...",
		})
	}

	// Verify all files
	failed, err := s.dirTransfer.Verify(ctx, s.Source, s.Dest)
	if err != nil {
		return "", fmt.Errorf("directory verification failed: %w", err)
	}

	if len(failed) > 0 {
		return "", fmt.Errorf("checksum mismatch for %d files: %s", len(failed), strings.Join(failed, ", "))
	}

	data := map[string]interface{}{
		"verified": true,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal verify data: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "All files verified",
		})
	}

	return string(jsonData), nil
}

// Rollback removes all transferred files from the destination.
func (s *DirectoryTransferStep) Rollback(sctx migration.StepContext) error {
	ctx := sctx.Ctx

	if s.dirTransfer == nil {
		s.dirTransfer = NewDirectoryTransfer(s.Selector)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "progress",
			Value:  "Rolling back: removing transferred files...",
		})
	}

	err := s.dirTransfer.Rollback(ctx, s.Dest)
	if err != nil {
		return fmt.Errorf("directory rollback failed: %w", err)
	}

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   s.StepName,
			Status: "success",
			Value:  "Rollback complete: all files removed",
		})
	}

	return nil
}

// Ensure FileTransferStep and DirectoryTransferStep implement migration.MigrationStep
var _ migration.MigrationStep = (*FileTransferStep)(nil)
var _ migration.MigrationStep = (*DirectoryTransferStep)(nil)
