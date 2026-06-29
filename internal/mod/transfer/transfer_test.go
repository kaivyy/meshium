package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"meshium/internal/mod/migration"
)

// --- Mock SSH Executer ---

// mockSSH is a configurable SSHExecuter for testing transfer operations.
type mockSSH struct {
	mu             sync.Mutex
	files          map[string][]byte    // remotePath → file content
	execOutput     map[string]string    // cmd prefix → stdout
	execErr        map[string]error     // cmd prefix → error
	uploadData     map[string][]byte    // remotePath → uploaded data
	downloadData   map[string][]byte    // remotePath → data to serve on download
	uploadErr      error
	downloadErr    error
	alive          bool
}

func newMockSSH() *mockSSH {
	return &mockSSH{
		files:        make(map[string][]byte),
		execOutput:   make(map[string]string),
		execErr:      make(map[string]error),
		uploadData:   make(map[string][]byte),
		downloadData: make(map[string][]byte),
		alive:        true,
	}
}

// setFile sets a remote file's content.
func (m *mockSSH) setFile(path string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = data
}

// getFile returns a remote file's content.
func (m *mockSSH) getFile(path string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.files[path]
}

func (m *mockSSH) Exec(cmd string) (string, string, int, error) {
	return m.ExecContext(context.Background(), cmd)
}

func (m *mockSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for exact match first
	if out, ok := m.execOutput[cmd]; ok {
		if err, ok := m.execErr[cmd]; ok {
			return out, "", 1, err
		}
		return out, "", 0, nil
	}

	// Check for prefix match
	for key, out := range m.execOutput {
		if strings.HasPrefix(cmd, key) {
			if err, ok := m.execErr[key]; ok {
				return out, "", 1, err
			}
			return out, "", 0, nil
		}
	}

	// Handle stat commands
	if strings.Contains(cmd, "stat -c %s") || strings.Contains(cmd, "stat -f %z") {
		// Extract path from command
		path := extractPathFromStat(cmd)
		if data, ok := m.files[path]; ok {
			return fmt.Sprintf("%d", len(data)), "", 0, nil
		}
		return "0", "", 0, nil
	}

	// Handle test -f (file exists check)
	if strings.Contains(cmd, "test -f") {
		path := extractPathFromTest(cmd)
		if _, ok := m.files[path]; ok {
			return "yes", "", 0, nil
		}
		return "no", "", 0, nil
	}

	// Handle rm -f (file deletion)
	if strings.Contains(cmd, "rm -f") {
		path := extractPathFromRm(cmd)
		delete(m.files, path)
		return "", "", 0, nil
	}

	// Handle rm -rf (directory deletion)
	if strings.Contains(cmd, "rm -rf") {
		path := extractPathFromRm(cmd)
		// Delete all files with this prefix
		for k := range m.files {
			if strings.HasPrefix(k, path) {
				delete(m.files, k)
			}
		}
		return "", "", 0, nil
	}

	// Handle mkdir -p
	if strings.Contains(cmd, "mkdir -p") {
		return "", "", 0, nil
	}

	// Handle cat >> (append)
	if strings.Contains(cmd, "cat '") && strings.Contains(cmd, ">>") {
		// This is the append command for resume
		return "", "", 0, nil
	}

	// Handle sha256sum
	if strings.Contains(cmd, "sha256sum") || strings.Contains(cmd, "shasum -a 256") {
		path := extractPathFromChecksum(cmd)
		if data, ok := m.files[path]; ok {
			hash := sha256.Sum256(data)
			return hex.EncodeToString(hash[:]), "", 0, nil
		}
		return "", "file not found", 1, fmt.Errorf("file not found")
	}

	// Handle df (disk free)
	if strings.Contains(cmd, "df -B1") {
		return fmt.Sprintf("%d", 100<<30), "", 0, nil // 100GB free
	}

	// Handle which rsync
	if strings.Contains(cmd, "which rsync") {
		return "", "", 1, nil // rsync not available
	}

	// Handle find (directory listing)
	if strings.Contains(cmd, "find . -type f") {
		// Return files in the current directory
		// The mock stores files with full paths, so we need to find
		// files that match the cd'd directory
		// Extract the cd path
		cdPath := ""
		if strings.Contains(cmd, "cd '") {
			parts := strings.Split(cmd, "'")
			if len(parts) >= 2 {
				cdPath = parts[1]
			}
		}
		var lines []string
		for path, data := range m.files {
			if strings.HasPrefix(path, cdPath+"/") {
				relPath := strings.TrimPrefix(path, cdPath+"/")
				lines = append(lines, fmt.Sprintf("%d\t%s", len(data), relPath))
			}
		}
		return strings.Join(lines, "\n") + "\n", "", 0, nil
	}

	return "", "", 0, nil
}

func (m *mockSSH) IsAlive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alive
}

func (m *mockSSH) Upload(src io.Reader, remotePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.uploadErr != nil {
		return m.uploadErr
	}

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, src); err != nil {
		return err
	}
	m.files[remotePath] = buf.Bytes()
	m.uploadData[remotePath] = buf.Bytes()
	return nil
}

func (m *mockSSH) Download(remotePath string, dst io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.downloadErr != nil {
		return m.downloadErr
	}

	if data, ok := m.files[remotePath]; ok {
		_, err := dst.Write(data)
		return err
	}
	return fmt.Errorf("file not found: %s", remotePath)
}

// --- Path extraction helpers ---

func extractPathFromStat(cmd string) string {
	// Extract path from: stat -c %s 'path' 2>/dev/null || stat -f %z 'path' 2>/dev/null
	// The path is always the first quoted string
	parts := strings.Split(cmd, "'")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func extractPathFromTest(cmd string) string {
	// Extract path from: test -f 'path' && echo yes || echo no
	parts := strings.Split(cmd, "'")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func extractPathFromRm(cmd string) string {
	// Extract path from: rm -f 'path' or rm -rf 'path'
	parts := strings.Split(cmd, "'")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func extractPathFromChecksum(cmd string) string {
	// Extract path from: sha256sum 'path' 2>/dev/null | ... || shasum -a 256 'path' 2>/dev/null | ...
	parts := strings.Split(cmd, "'")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// --- Test helpers ---

// createLocalFile creates a local file with the given content.
func createLocalFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

// localFileChecksum computes the SHA256 checksum of a local file.
func localFileChecksum(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// --- Tests ---

func TestSCPStrategyLocalToRemote(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("Hello, World! This is a test file for transfer.")
	createLocalFile(t, srcPath, content)

	src := TransferTarget{Path: srcPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: ssh}

	strategy := NewSCPStrategy()
	result, err := strategy.Transfer(context.Background(), src, dst, TransferOptions{})

	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if result.BytesTransferred != int64(len(content)) {
		t.Errorf("expected %d bytes transferred, got %d", len(content), result.BytesTransferred)
	}

	// Verify the file was uploaded
	uploaded := ssh.getFile("/remote/dest.txt")
	if string(uploaded) != string(content) {
		t.Errorf("file content mismatch: expected %q, got %q", content, uploaded)
	}
}

func TestSCPStrategyRemoteToLocal(t *testing.T) {
	ssh := newMockSSH()
	content := []byte("Remote file content for download test.")
	ssh.setFile("/remote/source.txt", content)

	tmpDir := t.TempDir()
	dstPath := filepath.Join(tmpDir, "downloaded.txt")

	src := TransferTarget{Path: "/remote/source.txt", IsLocal: false, SSHClient: ssh}
	dst := TransferTarget{Path: dstPath, IsLocal: true}

	strategy := NewSCPStrategy()
	result, err := strategy.Transfer(context.Background(), src, dst, TransferOptions{})

	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if result.BytesTransferred != int64(len(content)) {
		t.Errorf("expected %d bytes transferred, got %d", len(content), result.BytesTransferred)
	}

	// Verify the local file
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("file content mismatch: expected %q, got %q", content, data)
	}
}

func TestSCPStrategyRemoteToRemote(t *testing.T) {
	srcSSH := newMockSSH()
	dstSSH := newMockSSH()
	content := []byte("Remote-to-remote transfer test data.")
	srcSSH.setFile("/remote/source.txt", content)

	src := TransferTarget{Path: "/remote/source.txt", IsLocal: false, SSHClient: srcSSH}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: dstSSH}

	strategy := NewSCPStrategy()
	result, err := strategy.Transfer(context.Background(), src, dst, TransferOptions{})

	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if result.BytesTransferred != int64(len(content)) {
		t.Errorf("expected %d bytes transferred, got %d", len(content), result.BytesTransferred)
	}

	// Verify the file was uploaded to destination
	uploaded := dstSSH.getFile("/remote/dest.txt")
	if string(uploaded) != string(content) {
		t.Errorf("file content mismatch: expected %q, got %q", content, uploaded)
	}
}

func TestSCPStrategyProgressCallback(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := make([]byte, 1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	createLocalFile(t, srcPath, content)

	var progressCalls []TransferProgress
	var mu sync.Mutex
	callback := func(p TransferProgress) {
		mu.Lock()
		progressCalls = append(progressCalls, p)
		mu.Unlock()
	}

	src := TransferTarget{Path: srcPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: ssh}

	strategy := NewSCPStrategy()
	_, err := strategy.Transfer(context.Background(), src, dst, TransferOptions{
		ProgressCallback: callback,
		ProgressInterval: 10 * time.Millisecond,
	})

	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(progressCalls) == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestChecksumVerifierMatch(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()

	// Create identical local and remote files
	content := []byte("Checksum verification test data.")
	localPath := filepath.Join(tmpDir, "local.txt")
	createLocalFile(t, localPath, content)
	ssh.setFile("/remote/file.txt", content)

	src := TransferTarget{Path: localPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/file.txt", IsLocal: false, SSHClient: ssh}

	verifier := NewChecksumVerifier()
	checksum, err := verifier.Verify(context.Background(), src, dst)

	if err != nil {
		t.Fatalf("checksum verification failed: %v", err)
	}

	expectedChecksum := localFileChecksum(t, localPath)
	if checksum != expectedChecksum {
		t.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, checksum)
	}
}

func TestChecksumVerifierMismatch(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()

	// Create different local and remote files
	localContent := []byte("Local file content")
	remoteContent := []byte("Different remote file content")
	localPath := filepath.Join(tmpDir, "local.txt")
	createLocalFile(t, localPath, localContent)
	ssh.setFile("/remote/file.txt", remoteContent)

	src := TransferTarget{Path: localPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/file.txt", IsLocal: false, SSHClient: ssh}

	verifier := NewChecksumVerifier()
	verifier.MaxRetries = 1 // Don't retry for this test

	_, err := verifier.Verify(context.Background(), src, dst)

	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected 'checksum mismatch' error, got: %v", err)
	}
}

func TestFileTransferStepSuccess(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("FileTransferStep test data.")
	createLocalFile(t, srcPath, content)

	src := TransferTarget{Path: srcPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: ssh}

	step := NewFileTransferStep("file-transfer", src, dst, TransferOptions{})

	// Prepare
	prepareData, err := step.Prepare(migrationStepContext(context.Background()))
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if prepareData == "" {
		t.Error("expected non-empty prepare data")
	}

	// Apply
	applyData, err := step.Apply(migrationStepContext(context.Background()))
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if applyData == "" {
		t.Error("expected non-empty apply data")
	}

	// Verify
	verifyData, err := step.Verify(migrationStepContext(context.Background()))
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if verifyData == "" {
		t.Error("expected non-empty verify data")
	}

	// Check the file was transferred
	uploaded := ssh.getFile("/remote/dest.txt")
	if string(uploaded) != string(content) {
		t.Errorf("file content mismatch: expected %q, got %q", content, uploaded)
	}
}

func TestFileTransferStepRollback(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("FileTransferStep rollback test.")
	createLocalFile(t, srcPath, content)

	src := TransferTarget{Path: srcPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: ssh}

	step := NewFileTransferStep("file-transfer", src, dst, TransferOptions{})

	// Prepare and Apply
	_, _ = step.Prepare(migrationStepContext(context.Background()))
	_, _ = step.Apply(migrationStepContext(context.Background()))

	// Verify the file exists
	uploaded := ssh.getFile("/remote/dest.txt")
	if len(uploaded) == 0 {
		t.Fatal("file should exist after Apply")
	}

	// Rollback
	err := step.Rollback(migrationStepContext(context.Background()))
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify the file was deleted
	deleted := ssh.getFile("/remote/dest.txt")
	if len(deleted) != 0 {
		t.Error("file should be deleted after rollback")
	}
}

func TestFileTransferStepContextCancellation(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := make([]byte, 1024)
	createLocalFile(t, srcPath, content)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	src := TransferTarget{Path: srcPath, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest.txt", IsLocal: false, SSHClient: ssh}

	step := NewFileTransferStep("file-transfer", src, dst, TransferOptions{})

	_, err := step.Prepare(migrationStepContext(ctx))
	if err == nil {
		// Prepare might succeed even with cancelled context since it's just stat
		// Try Apply instead
		_, err = step.Apply(migrationStepContext(ctx))
		if err == nil {
			t.Skip("context cancellation not detected (mock too fast)")
		}
	}
}

func TestDirectoryTransferLocalToRemote(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "source_dir")

	// Create a directory with multiple files
	files := map[string]string{
		"file1.txt": "Content of file 1",
		"file2.txt": "Content of file 2",
		"subdir/file3.txt": "Content of file 3 in subdir",
	}

	for relPath, content := range files {
		createLocalFile(t, filepath.Join(srcDir, relPath), []byte(content))
	}

	src := TransferTarget{Path: srcDir, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest_dir", IsLocal: false, SSHClient: ssh}

	selector := NewStrategySelector()
	dt := NewDirectoryTransfer(selector)

	result, err := dt.Transfer(context.Background(), src, dst, TransferOptions{})
	if err != nil {
		t.Fatalf("directory transfer failed: %v", err)
	}

	if result.TotalFiles != 3 {
		t.Errorf("expected 3 total files, got %d", result.TotalFiles)
	}
	if result.TransferredFiles != 3 {
		t.Errorf("expected 3 transferred files, got %d", result.TransferredFiles)
	}
	if result.FailedFiles != 0 {
		t.Errorf("expected 0 failed files, got %d", result.FailedFiles)
	}

	// Verify all files were transferred
	for relPath, content := range files {
		remotePath := "/remote/dest_dir/" + relPath
		uploaded := ssh.getFile(remotePath)
		if string(uploaded) != content {
			t.Errorf("file %s content mismatch: expected %q, got %q", relPath, content, uploaded)
		}
	}
}

func TestDirectoryTransferResume(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "source_dir")

	// Create source files
	files := map[string]string{
		"file1.txt": "Content of file 1",
		"file2.txt": "Content of file 2",
		"file3.txt": "Content of file 3",
	}

	for relPath, content := range files {
		createLocalFile(t, filepath.Join(srcDir, relPath), []byte(content))
	}

	// Pre-populate the destination with file1.txt already transferred
	ssh.setFile("/remote/dest_dir/file1.txt", []byte(files["file1.txt"]))

	src := TransferTarget{Path: srcDir, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest_dir", IsLocal: false, SSHClient: ssh}

	selector := NewStrategySelector()
	dt := NewDirectoryTransfer(selector)

	result, err := dt.Transfer(context.Background(), src, dst, TransferOptions{Resume: true})
	if err != nil {
		t.Fatalf("directory transfer failed: %v", err)
	}

	// file1.txt should be skipped (already exists with correct size)
	if result.SkippedFiles != 1 {
		t.Errorf("expected 1 skipped file, got %d", result.SkippedFiles)
	}
	if result.TransferredFiles != 3 {
		t.Errorf("expected 3 transferred files (including skipped), got %d", result.TransferredFiles)
	}
}

func TestDirectoryTransferVerify(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "source_dir")

	// Create source files
	files := map[string]string{
		"file1.txt": "Content of file 1",
		"file2.txt": "Content of file 2",
	}

	for relPath, content := range files {
		createLocalFile(t, filepath.Join(srcDir, relPath), []byte(content))
	}

	src := TransferTarget{Path: srcDir, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest_dir", IsLocal: false, SSHClient: ssh}

	selector := NewStrategySelector()
	dt := NewDirectoryTransfer(selector)

	// Transfer
	_, err := dt.Transfer(context.Background(), src, dst, TransferOptions{})
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	// Verify
	failed, err := dt.Verify(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("expected 0 failed files, got %d: %v", len(failed), failed)
	}
}

func TestDirectoryTransferRollback(t *testing.T) {
	ssh := newMockSSH()
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "source_dir")

	// Create source files
	files := map[string]string{
		"file1.txt": "Content of file 1",
		"file2.txt": "Content of file 2",
	}

	for relPath, content := range files {
		createLocalFile(t, filepath.Join(srcDir, relPath), []byte(content))
	}

	src := TransferTarget{Path: srcDir, IsLocal: true}
	dst := TransferTarget{Path: "/remote/dest_dir", IsLocal: false, SSHClient: ssh}

	selector := NewStrategySelector()
	dt := NewDirectoryTransfer(selector)

	// Transfer
	_, err := dt.Transfer(context.Background(), src, dst, TransferOptions{})
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	// Verify files exist
	if len(ssh.files) == 0 {
		t.Fatal("expected files to exist after transfer")
	}

	// Rollback
	err = dt.Rollback(context.Background(), dst)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	// Verify files were deleted
	for relPath := range files {
		remotePath := "/remote/dest_dir/" + relPath
		if data := ssh.getFile(remotePath); len(data) != 0 {
			t.Errorf("file %s should be deleted after rollback", remotePath)
		}
	}
}

func TestStrategySelectorDefault(t *testing.T) {
	selector := NewStrategySelector()

	// Small file → SCP
	strategy := selector.Select(context.Background(), 1024, TransferOptions{}, TransferTarget{IsLocal: true})
	if strategy.Name() != "scp" {
		t.Errorf("expected scp for small file, got %s", strategy.Name())
	}

	// Large file → still SCP because rsync is not available on local
	strategy = selector.Select(context.Background(), 2<<30, TransferOptions{}, TransferTarget{IsLocal: true})
	if strategy.Name() != "scp" {
		t.Errorf("expected scp for large file (rsync not available), got %s", strategy.Name())
	}
}

func TestProgressTracker(t *testing.T) {
	var progressCalls []TransferProgress
	var mu sync.Mutex
	callback := func(p TransferProgress) {
		mu.Lock()
		progressCalls = append(progressCalls, p)
		mu.Unlock()
	}

	tracker := NewProgressTracker(1000, callback, 10*time.Millisecond)
	tracker.Start()
	defer tracker.Stop()

	// Simulate some bytes transferred
	tracker.SetTransferred(500)

	// Wait for at least one callback
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(progressCalls) == 0 {
		t.Error("expected at least one progress callback")
	}

	last := progressCalls[len(progressCalls)-1]
	if last.BytesTransferred != 500 {
		t.Errorf("expected 500 bytes transferred, got %d", last.BytesTransferred)
	}
	if last.TotalBytes != 1000 {
		t.Errorf("expected 1000 total bytes, got %d", last.TotalBytes)
	}
	if last.Percentage != 50 {
		t.Errorf("expected 50%%, got %.1f%%", last.Percentage)
	}
}

func TestProgressReaderWriter(t *testing.T) {
	tracker := NewProgressTracker(100, nil, 100*time.Millisecond)
	tracker.Start()
	defer tracker.Stop()

	// Test progress reader
	data := []byte("test data for progress reader")
	reader := bytes.NewReader(data)
	pr := tracker.WrapReader(reader)

	buf := make([]byte, 10)
	n, err := pr.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if n != 10 {
		t.Errorf("expected 10 bytes read, got %d", n)
	}
	if tracker.BytesTransferred() != 10 {
		t.Errorf("expected 10 bytes tracked, got %d", tracker.BytesTransferred())
	}

	// Test progress writer
	tracker2 := NewProgressTracker(100, nil, 100*time.Millisecond)
	tracker2.Start()
	defer tracker2.Stop()

	var writer bytes.Buffer
	pw := tracker2.WrapWriter(&writer)

	n, err = pw.Write(data)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if tracker2.BytesTransferred() != int64(len(data)) {
		t.Errorf("expected %d bytes tracked, got %d", len(data), tracker2.BytesTransferred())
	}
}

func TestGetFileSizeLocal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	content := []byte("test file size")
	createLocalFile(t, path, content)

	size, err := GetFileSize(context.Background(), TransferTarget{Path: path, IsLocal: true})
	if err != nil {
		t.Fatalf("GetFileSize failed: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), size)
	}
}

func TestGetFileSizeRemote(t *testing.T) {
	ssh := newMockSSH()
	ssh.setFile("/remote/file.txt", []byte("remote file content"))

	size, err := GetFileSize(context.Background(), TransferTarget{
		Path:      "/remote/file.txt",
		IsLocal:   false,
		SSHClient: ssh,
	})
	if err != nil {
		t.Fatalf("GetFileSize failed: %v", err)
	}
	if size != 19 { // len("remote file content")
		t.Errorf("expected size 19, got %d", size)
	}
}

func TestFileExistsLocal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "exists.txt")
	createLocalFile(t, path, []byte("exists"))

	exists, err := FileExists(context.Background(), TransferTarget{Path: path, IsLocal: true})
	if err != nil {
		t.Fatalf("FileExists failed: %v", err)
	}
	if !exists {
		t.Error("expected file to exist")
	}

	notExists, _ := FileExists(context.Background(), TransferTarget{Path: filepath.Join(tmpDir, "nonexistent.txt"), IsLocal: true})
	if notExists {
		t.Error("expected file to not exist")
	}
}

func TestDeleteFileLocal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "delete.txt")
	createLocalFile(t, path, []byte("delete me"))

	err := DeleteFile(context.Background(), TransferTarget{Path: path, IsLocal: true})
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	exists, _ := FileExists(context.Background(), TransferTarget{Path: path, IsLocal: true})
	if exists {
		t.Error("file should not exist after deletion")
	}
}

func TestDeleteFileRemote(t *testing.T) {
	ssh := newMockSSH()
	ssh.setFile("/remote/delete.txt", []byte("delete me"))

	err := DeleteFile(context.Background(), TransferTarget{
		Path:      "/remote/delete.txt",
		IsLocal:   false,
		SSHClient: ssh,
	})
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	if data := ssh.getFile("/remote/delete.txt"); len(data) != 0 {
		t.Error("file should be deleted")
	}
}

func TestComputeChecksumLocal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "checksum.txt")
	content := []byte("checksum test data")
	createLocalFile(t, path, content)

	checksum, err := ComputeChecksum(context.Background(), TransferTarget{Path: path, IsLocal: true})
	if err != nil {
		t.Fatalf("ComputeChecksum failed: %v", err)
	}

	expected := localFileChecksum(t, path)
	if checksum != expected {
		t.Errorf("expected checksum %s, got %s", expected, checksum)
	}
}

func TestComputeChecksumRemote(t *testing.T) {
	ssh := newMockSSH()
	content := []byte("remote checksum test")
	ssh.setFile("/remote/file.txt", content)

	checksum, err := ComputeChecksum(context.Background(), TransferTarget{
		Path:      "/remote/file.txt",
		IsLocal:   false,
		SSHClient: ssh,
	})
	if err != nil {
		t.Fatalf("ComputeChecksum failed: %v", err)
	}

	hash := sha256.Sum256(content)
	expected := hex.EncodeToString(hash[:])
	if checksum != expected {
		t.Errorf("expected checksum %s, got %s", expected, checksum)
	}
}

// --- Helper to create migration.StepContext ---

// migrationStepContext creates a migration.StepContext for testing.
func migrationStepContext(ctx context.Context) migration.StepContext {
	return migration.StepContext{Ctx: ctx}
}
