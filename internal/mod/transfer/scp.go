package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"meshium/internal/mod/transport"
)

// SCPStrategy transfers files using SFTP via the SSH client's
// Upload/Download methods. It is suitable for small-to-medium files
// (<1GB) and supports resume via temp-file append.
type SCPStrategy struct {
	// ChunkSize is the size of each chunk for resume transfers.
	// Default is 10MB (10 << 20).
	ChunkSize int64
}

// NewSCPStrategy creates an SCPStrategy with default settings.
func NewSCPStrategy() *SCPStrategy {
	return &SCPStrategy{
		ChunkSize: 10 << 20, // 10MB
	}
}

// Name returns "scp".
func (s *SCPStrategy) Name() string { return "scp" }

// CanResume returns true — SCP supports resume via temp-file append.
func (s *SCPStrategy) CanResume() bool { return true }

// EstimateSpeed returns ~10MB/s for SFTP transfers.
func (s *SCPStrategy) EstimateSpeed() int64 { return 10 << 20 }

// Transfer copies a file from src to dst using SFTP.
//
// Supported transfer modes:
//   - Local → Remote: open local file, upload to remote via SFTP
//   - Remote → Local: download from remote via SFTP, write to local file
//   - Remote → Remote: pipe download → upload (no buffering to memory)
//
// If opts.Resume is true and a partial file exists at the destination,
// the transfer continues from the last verified byte offset using
// temp-file append.
func (s *SCPStrategy) Transfer(ctx context.Context, src, dst TransferTarget, opts TransferOptions) (*TransferResult, error) {
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.ProgressInterval == 0 {
		opts.ProgressInterval = 500 * time.Millisecond
	}
	if opts.ChecksumAlgorithm == "" {
		opts.ChecksumAlgorithm = "sha256"
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

		// If context is cancelled, don't retry
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// For the last attempt, don't wait
		if attempt < opts.MaxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}

	return nil, fmt.Errorf("transfer failed after %d attempts: %w", opts.MaxRetries, lastErr)
}

// transferOnce performs a single transfer attempt.
func (s *SCPStrategy) transferOnce(ctx context.Context, src, dst TransferTarget, opts TransferOptions, attempt int) (*TransferResult, error) {
	// Determine transfer mode
	if src.IsLocal && !dst.IsLocal {
		return s.localToRemote(ctx, src, dst, opts, attempt)
	}
	if !src.IsLocal && dst.IsLocal {
		return s.remoteToLocal(ctx, src, dst, opts, attempt)
	}
	if !src.IsLocal && !dst.IsLocal {
		return s.remoteToRemote(ctx, src, dst, opts, attempt)
	}
	return nil, fmt.Errorf("local-to-local transfer not supported")
}

// localToRemote uploads a local file to a remote server via SFTP.
func (s *SCPStrategy) localToRemote(ctx context.Context, src, dst TransferTarget, opts TransferOptions, attempt int) (*TransferResult, error) {
	if dst.SSHClient == nil {
		return nil, fmt.Errorf("remote destination has no SSH client")
	}

	// Get total file size
	totalSize, err := GetFileSize(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("get source file size: %w", err)
	}

	// Check for resume
	offset := int64(0)
	resumed := false
	if opts.Resume && attempt > 0 {
		exists, _ := FileExists(ctx, dst)
		if exists {
			dstSize, err := GetFileSize(ctx, dst)
			if err == nil && dstSize > 0 && dstSize < totalSize {
				offset = dstSize
				resumed = true
			}
		}
	} else if opts.Resume && attempt == 0 {
		// On first attempt, check if there's a partial file from a previous run
		exists, _ := FileExists(ctx, dst)
		if exists {
			dstSize, err := GetFileSize(ctx, dst)
			if err == nil && dstSize > 0 && dstSize < totalSize {
				offset = dstSize
				resumed = true
			}
		}
	}

	// Open local file
	file, err := os.Open(src.Path)
	if err != nil {
		return nil, fmt.Errorf("open local source: %w", err)
	}
	defer file.Close()

	// Seek to offset if resuming
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek source file: %w", err)
		}
	}

	// Create progress tracker
	tracker := NewProgressTracker(totalSize, opts.ProgressCallback, opts.ProgressInterval)
	tracker.SetTransferred(offset)
	tracker.Start()
	defer tracker.Stop()

	// Wrap reader with progress tracking
	reader := tracker.WrapReader(file)

	if offset > 0 {
		// Resume: upload remaining data as temp file, then append
		err = s.uploadAndAppend(ctx, dst.SSHClient, reader, dst.Path, offset)
	} else {
		// Fresh transfer: upload directly
		err = dst.SSHClient.Upload(reader, dst.Path)
	}

	if err != nil {
		return nil, err
	}

	result := &TransferResult{
		BytesTransferred: tracker.BytesTransferred(),
		Resumed:          resumed,
	}
	return result, nil
}

// remoteToLocal downloads a file from a remote server to local via SFTP.
func (s *SCPStrategy) remoteToLocal(ctx context.Context, src, dst TransferTarget, opts TransferOptions, attempt int) (*TransferResult, error) {
	if src.SSHClient == nil {
		return nil, fmt.Errorf("remote source has no SSH client")
	}

	// Get total file size
	totalSize, err := GetFileSize(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("get source file size: %w", err)
	}

	// Check for resume
	offset := int64(0)
	resumed := false
	flags := os.O_CREATE | os.O_WRONLY
	if opts.Resume {
		exists, _ := FileExists(ctx, dst)
		if exists {
			dstSize, err := GetFileSize(ctx, dst)
			if err == nil && dstSize > 0 && dstSize < totalSize {
				offset = dstSize
				resumed = true
				flags |= os.O_APPEND // append to existing file
			}
		}
	}
	if offset == 0 {
		flags |= os.O_TRUNC // overwrite existing file
	}

	// Open local file
	file, err := os.OpenFile(dst.Path, flags, 0644)
	if err != nil {
		return nil, fmt.Errorf("open local destination: %w", err)
	}
	defer file.Close()

	// Create progress tracker
	tracker := NewProgressTracker(totalSize, opts.ProgressCallback, opts.ProgressInterval)
	tracker.SetTransferred(offset)
	tracker.Start()
	defer tracker.Stop()

	// Wrap writer with progress tracking
	writer := tracker.WrapWriter(file)

	if offset > 0 {
		// Resume: download from offset using a pipe
		// SFTP Download reads the whole file, so we need to skip the first `offset` bytes
		err = s.downloadWithOffset(ctx, src.SSHClient, writer, src.Path, offset, totalSize)
	} else {
		// Fresh download
		err = src.SSHClient.Download(src.Path, writer)
	}

	if err != nil {
		return nil, err
	}

	result := &TransferResult{
		BytesTransferred: tracker.BytesTransferred(),
		Resumed:          resumed,
	}
	return result, nil
}

// remoteToRemote pipes a file from one remote server to another via SFTP.
// This uses io.Pipe to stream data without buffering to memory.
func (s *SCPStrategy) remoteToRemote(ctx context.Context, src, dst TransferTarget, opts TransferOptions, attempt int) (*TransferResult, error) {
	if src.SSHClient == nil {
		return nil, fmt.Errorf("remote source has no SSH client")
	}
	if dst.SSHClient == nil {
		return nil, fmt.Errorf("remote destination has no SSH client")
	}

	// Get total file size
	totalSize, err := GetFileSize(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("get source file size: %w", err)
	}

	// Create progress tracker
	tracker := NewProgressTracker(totalSize, opts.ProgressCallback, opts.ProgressInterval)
	tracker.Start()
	defer tracker.Stop()

	// Create a pipe to stream data from download to upload
	pr, pw := io.Pipe()

	// Start download in a goroutine (writes to pipe)
	// We track progress on the write side only to avoid double-counting
	downloadErr := make(chan error, 1)
	go func() {
		err := src.SSHClient.Download(src.Path, pw)
		pw.CloseWithError(err)
		downloadErr <- err
	}()

	// Track bytes read from the pipe for progress (only one side tracks)
	trackedReader := tracker.WrapReader(pr)

	// Upload from pipe (reads from pipe)
	uploadErr := make(chan error, 1)
	go func() {
		err := dst.SSHClient.Upload(trackedReader, dst.Path)
		uploadErr <- err
	}()

	// Wait for both to complete
	dlErr := <-downloadErr
	ulErr := <-uploadErr

	if dlErr != nil {
		return nil, fmt.Errorf("download failed: %w", dlErr)
	}
	if ulErr != nil {
		return nil, fmt.Errorf("upload failed: %w", ulErr)
	}

	result := &TransferResult{
		BytesTransferred: tracker.BytesTransferred(),
	}
	return result, nil
}

// uploadAndAppend uploads data to a temp file and appends it to the destination.
// This is used for resume: the remaining data is uploaded as a temp file,
// then appended to the existing partial file.
func (s *SCPStrategy) uploadAndAppend(ctx context.Context, ssh transport.SSHExecuter, reader io.Reader, dstPath string, offset int64) error {
	tempPath := dstPath + ".resume_tmp"

	// Upload remaining data to temp file
	err := ssh.Upload(reader, tempPath)
	if err != nil {
		// Clean up temp file on failure
		ssh.ExecContext(ctx, fmt.Sprintf("rm -f '%s'", tempPath))
		return fmt.Errorf("upload resume chunk: %w", err)
	}

	// Append temp file to destination
	cmd := fmt.Sprintf("cat '%s' >> '%s' && rm -f '%s'", tempPath, dstPath, tempPath)
	_, stderr, exitCode, err := ssh.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("append resume chunk: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("append resume chunk: exit code %d: %s", exitCode, stderr)
	}

	return nil
}

// downloadWithOffset downloads a file from a remote server starting at the
// given offset. Since SFTP Download reads the whole file, we skip the first
// `offset` bytes by writing them to io.Discard.
func (s *SCPStrategy) downloadWithOffset(ctx context.Context, ssh transport.SSHExecuter, writer io.Writer, remotePath string, offset, totalSize int64) error {
	// We need to skip `offset` bytes. Since Download writes to a Writer,
	// we create a writer that discards the first `offset` bytes and then
	// passes the rest to the real writer.
	skipWriter := &offsetSkipWriter{
		offset:    offset,
		skipped:   0,
		delegate:  writer,
	}

	return ssh.Download(remotePath, skipWriter)
}

// offsetSkipWriter discards the first `offset` bytes, then passes the rest
// to the delegate writer.
type offsetSkipWriter struct {
	offset   int64
	skipped  int64
	delegate io.Writer
}

func (w *offsetSkipWriter) Write(p []byte) (int, error) {
	if w.skipped < w.offset {
		// Still skipping
		remaining := w.offset - w.skipped
		if int64(len(p)) <= remaining {
			w.skipped += int64(len(p))
			return len(p), nil
		}
		// Skip part of the buffer, write the rest
		skip := int(remaining)
		w.skipped = w.offset
		n, err := w.delegate.Write(p[skip:])
		return skip + n, err
	}
	// Past the offset, write everything
	return w.delegate.Write(p)
}
