package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"meshium/internal/mod/transport"
)

// ChecksumVerifier verifies file integrity by comparing SHA256 checksums
// computed on the source and destination.
type ChecksumVerifier struct {
	// MaxRetries is the maximum number of retry attempts on checksum mismatch.
	// Default is 3.
	MaxRetries int
}

// NewChecksumVerifier creates a ChecksumVerifier with default settings.
func NewChecksumVerifier() *ChecksumVerifier {
	return &ChecksumVerifier{MaxRetries: 3}
}

// Verify computes the SHA256 checksum of a file at both the source and
// destination, and compares them. If they don't match, it retries up to
// MaxRetries times.
//
// Returns the matching checksum, or an error if verification fails after
// all retries.
func (v *ChecksumVerifier) Verify(ctx context.Context, src, dst TransferTarget) (string, error) {
	if v.MaxRetries == 0 {
		v.MaxRetries = 3
	}

	var lastErr error
	for attempt := 0; attempt < v.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		srcChecksum, err := v.computeChecksum(ctx, src)
		if err != nil {
			lastErr = fmt.Errorf("compute source checksum: %w", err)
			continue
		}

		dstChecksum, err := v.computeChecksum(ctx, dst)
		if err != nil {
			lastErr = fmt.Errorf("compute destination checksum: %w", err)
			continue
		}

		if srcChecksum == dstChecksum {
			return srcChecksum, nil
		}

		lastErr = fmt.Errorf("checksum mismatch: source=%s, destination=%s", srcChecksum, dstChecksum)
	}

	return "", fmt.Errorf("checksum verification failed after %d attempts: %w", v.MaxRetries, lastErr)
}

// VerifyFile verifies a single file by computing its checksum at both ends.
// This is a convenience method that wraps Verify.
func (v *ChecksumVerifier) VerifyFile(ctx context.Context, src, dst TransferTarget) error {
	_, err := v.Verify(ctx, src, dst)
	return err
}

// computeChecksum computes the SHA256 checksum of a file at the given target.
// For local targets, it reads the file directly. For remote targets, it
// uses `sha256sum` via SSH.
func (v *ChecksumVerifier) computeChecksum(ctx context.Context, target TransferTarget) (string, error) {
	if target.IsLocal {
		return computeLocalChecksum(target.Path)
	}
	if target.SSHClient == nil {
		return "", fmt.Errorf("remote target has no SSH client")
	}
	return computeRemoteChecksum(ctx, target.SSHClient, target.Path)
}

// computeLocalChecksum computes the SHA256 checksum of a local file.
func computeLocalChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open local file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash local file: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// computeRemoteChecksum computes the SHA256 checksum of a remote file via SSH.
// Uses `sha256sum` command, with fallback to `shasum -a 256` for macOS.
func computeRemoteChecksum(ctx context.Context, ssh transport.SSHExecuter, path string) (string, error) {
	// Try sha256sum first (Linux), fall back to shasum (macOS)
	cmd := fmt.Sprintf("sha256sum '%s' 2>/dev/null | awk '{print $1}' || shasum -a 256 '%s' 2>/dev/null | awk '{print $1}'", path, path)
	stdout, stderr, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("compute remote checksum: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("compute remote checksum: exit code %d: %s", exitCode, stderr)
	}

	checksum := strings.TrimSpace(stdout)
	if len(checksum) != 64 {
		return "", fmt.Errorf("invalid checksum length: %d (expected 64): %q", len(checksum), checksum)
	}

	return checksum, nil
}

// ComputeChecksum computes the SHA256 checksum of a file at the given target.
// This is a standalone function that can be used without a ChecksumVerifier.
func ComputeChecksum(ctx context.Context, target TransferTarget) (string, error) {
	if target.IsLocal {
		return computeLocalChecksum(target.Path)
	}
	if target.SSHClient == nil {
		return "", fmt.Errorf("remote target has no SSH client")
	}
	return computeRemoteChecksum(ctx, target.SSHClient, target.Path)
}
