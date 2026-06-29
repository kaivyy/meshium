package transfer

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RsyncStrategy transfers files using rsync over SSH.
// It is suitable for large files and when resume is required,
// as rsync natively supports resume via --partial --append-verify.
type RsyncStrategy struct {
	// available is cached after the first availability check.
	available map[int]bool
	checked   bool
}

// NewRsyncStrategy creates an RsyncStrategy.
func NewRsyncStrategy() *RsyncStrategy {
	return &RsyncStrategy{
		available: make(map[int]bool),
	}
}

// Name returns "rsync".
func (s *RsyncStrategy) Name() string { return "rsync" }

// CanResume returns true — rsync natively supports resume.
func (s *RsyncStrategy) CanResume() bool { return true }

// EstimateSpeed returns ~50MB/s for rsync transfers.
func (s *RsyncStrategy) EstimateSpeed() int64 { return 50 << 20 }

// IsAvailable checks if rsync is installed on the source server.
// Caches the result per server ID (if available).
func (s *RsyncStrategy) IsAvailable(ctx context.Context, src TransferTarget) bool {
	if src.IsLocal {
		// For local sources, check if rsync is installed locally
		// We don't have a local exec interface in SSHExecuter, so we
		// return false to prefer SCP for local sources
		return false
	}
	if src.SSHClient == nil {
		return false
	}

	// Check if rsync is installed on the remote source
	cmd := "which rsync 2>/dev/null && rsync --version 2>/dev/null | head -1"
	stdout, _, exitCode, err := src.SSHClient.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		return false
	}
	return strings.TrimSpace(stdout) != ""
}

// Transfer copies a file from src to dst using rsync over SSH.
//
// Rsync is invoked on the source server to push the file to the destination.
// This requires that the source server can SSH to the destination server.
//
// For local→remote transfers, rsync is run locally.
// For remote→local transfers, rsync is run on the remote source to pull
// from the destination... actually, rsync is run locally to pull from the remote.
// For remote→remote transfers, rsync is run on the source to push to the destination.
func (s *RsyncStrategy) Transfer(ctx context.Context, src, dst TransferTarget, opts TransferOptions) (*TransferResult, error) {
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.ProgressInterval == 0 {
		opts.ProgressInterval = 500 * time.Millisecond
	}

	start := time.Now()
	var lastErr error

	for attempt := 0; attempt < opts.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		result, err := s.transferOnce(ctx, src, dst, opts, attempt)
		if err == nil {
			result.Duration = time.Since(start)
			result.Strategy = s.Name()
			return result, nil
		}

		lastErr = err

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt < opts.MaxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}

	return nil, fmt.Errorf("rsync transfer failed after %d attempts: %w", opts.MaxRetries, lastErr)
}

// transferOnce performs a single rsync transfer attempt.
func (s *RsyncStrategy) transferOnce(ctx context.Context, src, dst TransferTarget, opts TransferOptions, attempt int) (*TransferResult, error) {
	// Build rsync command
	args := []string{
		"-avz",           // archive, verbose, compress
		"--partial",      // keep partially transferred files
		"--append-verify", // resume with checksum verification
	}

	// Add progress if callback is set
	var progressParser *rsyncProgressParser
	if opts.ProgressCallback != nil {
		progressParser = newRsyncProgressParser(opts.ProgressCallback, opts.ProgressInterval)
		args = append(args, "--progress")
	}

	// Build source and destination spec
	var srcSpec, dstSpec string

	if src.IsLocal && !dst.IsLocal {
		// Local → Remote: rsync local_file user@host:remote_path
		srcSpec = src.Path
		dstSpec = fmt.Sprintf("%s@%s:%s", dst.User, dst.Host, dst.Path)
	} else if !src.IsLocal && dst.IsLocal {
		// Remote → Local: rsync user@host:remote_path local_path
		srcSpec = fmt.Sprintf("%s@%s:%s", src.User, src.Host, src.Path)
		dstSpec = dst.Path
	} else if !src.IsLocal && !dst.IsRemote(dst) {
		// Remote → Remote: run rsync on the source to push to destination
		return s.remoteToRemote(ctx, src, dst, opts, args, progressParser)
	} else {
		return nil, fmt.Errorf("unsupported transfer mode for rsync")
	}

	// Run rsync locally (for local→remote and remote→local)
	cmd := fmt.Sprintf("rsync %s '%s' '%s'",
		strings.Join(args, " "), srcSpec, dstSpec)

	// For local execution, we need to run rsync locally
	// But we don't have a local exec interface... we need to use os/exec
	return s.runLocalRsync(ctx, cmd, opts, progressParser)
}

// IsRemote is a helper to check if dst is remote.
func (TransferTarget) IsRemote(dst TransferTarget) bool {
	return !dst.IsLocal
}

// remoteToRemote runs rsync on the source server to push to the destination.
// This requires the source to be able to SSH to the destination.
func (s *RsyncStrategy) remoteToRemote(ctx context.Context, src, dst TransferTarget, opts TransferOptions, args []string, progressParser *rsyncProgressParser) (*TransferResult, error) {
	if src.SSHClient == nil {
		return nil, fmt.Errorf("remote source has no SSH client")
	}

	// Build rsync command to run on the source
	dstSpec := fmt.Sprintf("%s@%s:%s", dst.User, dst.Host, dst.Path)
	cmd := fmt.Sprintf("rsync %s '%s' '%s'",
		strings.Join(args, " "), src.Path, dstSpec)

	// Execute via SSH on the source
	stdout, stderr, exitCode, err := src.SSHClient.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("rsync remote-to-remote failed: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("rsync remote-to-remote failed: exit code %d: %s", exitCode, stderr)
	}

	// Parse progress from stdout
	if progressParser != nil {
		for _, line := range strings.Split(stdout, "\n") {
			progressParser.parse(line)
		}
	}

	result := &TransferResult{
		BytesTransferred: 0, // rsync doesn't report exact bytes easily
	}
	return result, nil
}

// runLocalRsync runs rsync locally using os/exec.
// This is used for local→remote and remote→local transfers.
func (s *RsyncStrategy) runLocalRsync(ctx context.Context, cmd string, opts TransferOptions, progressParser *rsyncProgressParser) (*TransferResult, error) {
	// We need to use os/exec to run rsync locally
	// But we need to import os/exec... let's use a simpler approach
	// Actually, for the transfer engine, we should use the SSH client
	// to run rsync. But for local→remote, we need to run rsync locally.

	// Let's use exec.Command via a helper
	return execLocalRsync(ctx, cmd, opts, progressParser)
}

// rsyncProgressParser parses rsync --progress output.
// rsync progress lines look like:
//
//	1,234,567  1%  10.00MB/s  0:00:01
//
// or for the final line:
//
//	1,234,567 100%  10.00MB/s  0:00:00 (1xfrs, 1xdirs, 0xother)
type rsyncProgressParser struct {
	callback  func(TransferProgress)
	interval  time.Duration
	lastEmit  time.Time
	totalSize int64
}

func newRsyncProgressParser(callback func(TransferProgress), interval time.Duration) *rsyncProgressParser {
	return &rsyncProgressParser{
		callback: callback,
		interval: interval,
		lastEmit: time.Now(),
	}
}

// parse attempts to parse a rsync progress line and emit a progress callback.
func (p *rsyncProgressParser) parse(line string) {
	if p.callback == nil {
		return
	}

	// Throttle callbacks
	now := time.Now()
	if now.Sub(p.lastEmit) < p.interval {
		return
	}
	p.lastEmit = now

	// Try to parse "bytes  percentage  speed  eta" format
	var bytes int64
	var pct float64
	var speedStr string
	var etaStr string
	n, err := fmt.Sscanf(line, "%d  %f%%  %s  %s", &bytes, &pct, &speedStr, &etaStr)
	if err != nil || n < 2 {
		return
	}

	// Parse speed (e.g., "10.00MB/s")
	speedBPS := parseRsyncSpeed(speedStr)

	// Parse ETA (e.g., "0:00:01")
	eta := parseRsyncETA(etaStr)

	p.callback(TransferProgress{
		BytesTransferred: bytes,
		TotalBytes:       p.totalSize,
		SpeedBPS:         speedBPS,
		ETA:              eta,
		Percentage:       pct,
	})
}

// parseRsyncSpeed parses a speed string like "10.00MB/s" into bytes per second.
func parseRsyncSpeed(s string) int64 {
	var value float64
	var unit string
	n, err := fmt.Sscanf(s, "%f%s", &value, &unit)
	if err != nil || n < 2 {
		return 0
	}

	switch strings.ToUpper(unit) {
	case "B/S":
		return int64(value)
	case "KB/S":
		return int64(value * 1024)
	case "MB/S":
		return int64(value * 1024 * 1024)
	case "GB/S":
		return int64(value * 1024 * 1024 * 1024)
	default:
		return 0
	}
}

// parseRsyncETA parses an ETA string like "0:00:01" into a duration.
func parseRsyncETA(s string) time.Duration {
	var h, m, sec int
	n, err := fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec)
	if err != nil || n < 3 {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
}
