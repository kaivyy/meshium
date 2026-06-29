// Package transfer provides a file transfer engine for migrations.
//
// The transfer engine supports:
//   - Resume-able transfers (if the connection drops mid-transfer)
//   - Checksum verification (SHA256, computed on both source and destination)
//   - Efficient large-file transfers (streamed via pipe, no buffering to memory)
//   - Integration with the MigrationStep interface from Phase 2
//
// Two transfer strategies are provided:
//   - SCPStrategy: uses SFTP via SSHExecuter.Upload/Download. Suitable for
//     small-to-medium files (<1GB). Resume is supported via temp-file append.
//   - RsyncStrategy: uses rsync over SSH. Suitable for large files or when
//     resume is required. Resume is native via --partial --append-verify.
//
// The strategy selector picks the best strategy based on file size and whether
// resume is required.
package transfer

import (
	"context"
	"fmt"
	"time"

	"meshium/internal/mod/transport"
)

// --- Core types ---

// TransferTarget represents one end of a file transfer.
// A target can be either local (on the machine running the migration) or
// remote (on a server accessible via SSH).
type TransferTarget struct {
	// Host is the hostname or IP of the remote server.
	// Ignored for local targets.
	Host string
	// Port is the SSH port of the remote server.
	// Ignored for local targets.
	Port int
	// User is the SSH username for the remote server.
	// Ignored for local targets.
	User string
	// Path is the file or directory path on the target.
	Path string
	// SSHClient is the SSH client for remote targets.
	// Must be non-nil for remote targets; ignored for local targets.
	SSHClient transport.SSHExecuter
	// IsLocal is true for local targets (on the machine running the migration).
	// When true, SSHClient is ignored and Path is a local filesystem path.
	IsLocal bool
}

// TransferOptions controls how a transfer is performed.
type TransferOptions struct {
	// Resume is true if the transfer should resume from a checkpoint.
	// If true and a partial file exists at the destination, the transfer
	// will continue from the last verified byte offset.
	Resume bool
	// MaxRetries is the maximum number of retry attempts on transfer failure.
	// Default is 3.
	MaxRetries int
	// ProgressCallback is called periodically with transfer progress.
	// May be nil if progress tracking is not needed.
	ProgressCallback func(TransferProgress)
	// ProgressInterval is how often to call ProgressCallback.
	// Default is 500ms.
	ProgressInterval time.Duration
	// Timeout is the maximum duration for the transfer.
	// A zero value means no timeout (only the parent context applies).
	Timeout time.Duration
	// ChecksumAlgorithm is the algorithm to use for checksum verification.
	// Default is "sha256".
	ChecksumAlgorithm string
}

// TransferProgress holds real-time transfer progress information.
type TransferProgress struct {
	// BytesTransferred is the number of bytes transferred so far.
	BytesTransferred int64 `json:"bytesTransferred"`
	// TotalBytes is the total size of the file being transferred.
	// May be 0 if the size is unknown.
	TotalBytes int64 `json:"totalBytes"`
	// SpeedBPS is the current transfer speed in bytes per second.
	SpeedBPS int64 `json:"speedBPS"`
	// ETA is the estimated time remaining.
	ETA time.Duration `json:"eta"`
	// Percentage is the completion percentage (0-100).
	// May be 0 if TotalBytes is unknown.
	Percentage float64 `json:"percentage"`
}

// TransferResult holds the outcome of a transfer.
type TransferResult struct {
	// BytesTransferred is the total number of bytes transferred.
	BytesTransferred int64 `json:"bytesTransferred"`
	// Duration is the total time taken for the transfer.
	Duration time.Duration `json:"duration"`
	// AverageSpeed is the average transfer speed in bytes per second.
	AverageSpeed int64 `json:"averageSpeed"`
	// Checksum is the SHA256 checksum of the transferred file (if verified).
	Checksum string `json:"checksum,omitempty"`
	// Resumed is true if the transfer was resumed from a checkpoint.
	Resumed bool `json:"resumed"`
	// Strategy is the name of the strategy used.
	Strategy string `json:"strategy"`
}

// --- TransferStrategy interface ---

// TransferStrategy defines how files are transferred between targets.
// Implementations include SCPStrategy (SFTP) and RsyncStrategy (rsync over SSH).
type TransferStrategy interface {
	// Name returns the strategy name (e.g., "scp", "rsync").
	Name() string

	// Transfer copies a file from src to dst.
	// The context is used for cancellation; opts controls transfer behavior.
	// Returns a TransferResult with transfer statistics.
	Transfer(ctx context.Context, src, dst TransferTarget, opts TransferOptions) (*TransferResult, error)

	// CanResume returns true if this strategy supports resuming
	// a partially-completed transfer.
	CanResume() bool

	// EstimateSpeed returns the estimated transfer speed in bytes per second.
	// This is a rough estimate used by the strategy selector.
	EstimateSpeed() int64
}

// --- Strategy selector ---

// StrategySelector picks the best transfer strategy based on file size
// and whether resume is required.
type StrategySelector struct {
	scp   *SCPStrategy
	rsync *RsyncStrategy
	// RsyncThreshold is the file size above which rsync is preferred.
	// Default is 1GB (1 << 30 bytes).
	RsyncThreshold int64
}

// NewStrategySelector creates a StrategySelector with default settings.
func NewStrategySelector() *StrategySelector {
	return &StrategySelector{
		scp:           NewSCPStrategy(),
		rsync:         NewRsyncStrategy(),
		RsyncThreshold: 1 << 30, // 1GB
	}
}

// Select picks the best strategy for the given file size and options.
//
// Selection rules:
//   - If resume is required and rsync is available, use rsync.
//   - If file size > RsyncThreshold and rsync is available, use rsync.
//   - Otherwise, use SCP.
func (s *StrategySelector) Select(ctx context.Context, fileSize int64, opts TransferOptions, src TransferTarget) TransferStrategy {
	// If resume is required, prefer rsync (it has native resume support)
	if opts.Resume && s.rsync.IsAvailable(ctx, src) {
		return s.rsync
	}

	// For large files, prefer rsync if available
	if fileSize > s.RsyncThreshold && s.rsync.IsAvailable(ctx, src) {
		return s.rsync
	}

	// Default to SCP
	return s.scp
}

// SCP returns the SCP strategy.
func (s *StrategySelector) SCP() *SCPStrategy { return s.scp }

// Rsync returns the rsync strategy.
func (s *StrategySelector) Rsync() *RsyncStrategy { return s.rsync }

// --- File size helpers ---

// GetFileSize returns the size of a file at the given target.
// For local targets, it uses os.Stat. For remote targets, it uses
// `stat -c %s /path` via SSH.
func GetFileSize(ctx context.Context, target TransferTarget) (int64, error) {
	if target.IsLocal {
		return getLocalFileSize(target.Path)
	}
	if target.SSHClient == nil {
		return 0, fmt.Errorf("remote target has no SSH client")
	}
	return getRemoteFileSize(ctx, target.SSHClient, target.Path)
}

// FileExists checks if a file exists at the given target.
func FileExists(ctx context.Context, target TransferTarget) (bool, error) {
	if target.IsLocal {
		return localFileExists(target.Path)
	}
	if target.SSHClient == nil {
		return false, fmt.Errorf("remote target has no SSH client")
	}
	return remoteFileExists(ctx, target.SSHClient, target.Path)
}

// DeleteFile removes a file at the given target.
// Used for rollback (cleaning up partial files).
func DeleteFile(ctx context.Context, target TransferTarget) error {
	if target.IsLocal {
		return deleteLocalFile(target.Path)
	}
	if target.SSHClient == nil {
		return fmt.Errorf("remote target has no SSH client")
	}
	return deleteRemoteFile(ctx, target.SSHClient, target.Path)
}
