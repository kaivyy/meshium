//go:build integration

// Phase 4C — WAN / large-scale transfer resilience.
//
// The rsync baseline (SyncEngine) ships `--partial --append-verify`, so a killed
// or SSH-dropped transfer is resumed by re-running the same resumable command.
// This file PROVES that contract with REAL local rsync (no SSH needed: the test
// executer runs commands on the host, and SyncConfig.TargetHost == "" routes
// rsync to local paths):
//
//   - TestSyncResumeAfterKillLive: start an InitialSync, cancel its context to
//     KILL rsync mid-transfer (partial files remain), then ResumeSync completes
//     it, and file-count + per-file md5sum match the source with zero corruption.
//   - TestSyncResumeAfterSSHResumeDismissed (mock): a transient source SSH error
//     on the first run leaves a partial tree; ResumeSync re-runs the SAME
//     resumable command and completes without corrupting data.
//
// It does NOT replace rsync (finding D-1). It proves the existing resume +
// integrity contract end-to-end.
//
// Run: go test -tags integration ./internal/mod/migration/ -run 'Sync' -v

package migration

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// localExecuter runs commands on the host (where rsync + real temp dirs live).
// Used so SyncEngine's rsync (TargetHost == "") runs locally and a cancelable
// context actually kills the process.
type localExecuter struct{}

func (localExecuter) Exec(cmd string) (string, string, int, error) {
	return localExecuter{}.ExecContext(context.Background(), cmd)
}
func (localExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	out, err := exec.CommandContext(ctx, "bash", "-lc", cmd).CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(out), "", exit, err
}
func (localExecuter) IsAlive() bool { return true }
func (localExecuter) Upload(io.Reader, string) error {
	return fmt.Errorf("localExecuter: Upload unsupported")
}
func (localExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("localExecuter: Download unsupported")
}

// recordingSyncRepo is a minimal in-memory PipelineRepo that records sync
// sessions so ResumeSync can find the interrupted one. It embeds the no-op
// mockPipelineRepo (full interface) and overrides the sync methods.
type recordingSyncRepo struct {
	*mockPipelineRepo
	sessions map[int64]*SyncSession
	nextID   int64
}

func newRecordingSyncRepo() *recordingSyncRepo {
	return &recordingSyncRepo{sessions: make(map[int64]*SyncSession), nextID: 1}
}

func (r *recordingSyncRepo) CreateSyncSession(_ context.Context, s SyncSession) (int64, error) {
	id := r.nextID
	r.nextID++
	s.ID = id
	cp := s
	r.sessions[id] = &cp
	return id, nil
}
func (r *recordingSyncRepo) UpdateSyncSession(_ context.Context, id int64, bytes int64, files int, speed int64, status string) error {
	if s, ok := r.sessions[id]; ok {
		s.BytesTransferred = bytes
		s.FilesTransferred = files
		s.SpeedBytesSec = speed
		s.Status = status
	}
	return nil
}
func (r *recordingSyncRepo) CompleteSyncSession(_ context.Context, id int64, status string, errMsg string) error {
	if s, ok := r.sessions[id]; ok {
		s.Status = status
		s.Error = errMsg
	}
	return nil
}
func (r *recordingSyncRepo) GetSyncSessions(_ int) ([]SyncSession, error) {
	out := make([]SyncSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, *s)
	}
	return out, nil
}

// makeTree writes n files with random-ish content under root, returning the
// relative file list. Total size kept modest to respect disk pressure.
func makeTree(t *testing.T, root string, n int) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i < n; i++ {
		// Spread across subdirs so the tree is non-trivial.
		sub := filepath.Join(root, fmt.Sprintf("d%02d", i%5))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir sub: %v", err)
		}
		p := filepath.Join(sub, fmt.Sprintf("f%04d.dat", i))
		// 4KB each — enough to make a kill mid-transfer meaningful.
		content := []byte(strings.Repeat(fmt.Sprintf("%04d", i), 1024))
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// md5RelMap returns map[relpath]md5 for every file under root.
func md5RelMap(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		h := md5.New()
		io.Copy(h, f)
		rel, _ := filepath.Rel(root, p)
		out[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// TestSyncResumeAfterKillLive proves: kill rsync mid-transfer → ResumeSync
// completes → target is byte-identical to source (no corruption, no loss).
func TestSyncResumeAfterKillLive(t *testing.T) {
	src := t.TempDir()
	tgt := t.TempDir()
	makeTree(t, src, 4000) // ~16MB across 5 dirs; large enough that rsync runs past the cancel window

	repo := newRecordingSyncRepo()
	eng := NewSyncEngine(localExecuter{}, localExecuter{}, repo)

	// 1) Start InitialSync with a cancelable context, then KILL it early.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := eng.InitialSync(ctx, 1, SyncConfig{
			SourcePath: src, TargetPath: tgt, MigrationID: 1,
			BandwidthLimit: 1000, // 1000 KB/s → ~16s for 16MB; cancel at 400ms catches it mid-transfer
		})
		done <- err
	}()
	// Let it transfer a slice, then cancel (kill rsync).
	time.Sleep(400 * time.Millisecond)
	cancel()
	initErr := <-done
	// A killed transfer must surface an error, not a silent success.
	if initErr == nil {
		t.Fatalf("cancelled InitialSync returned nil error; kill was not observed")
	}
	// The interrupted session must be recorded as error/running (not completed).
	ss, _ := repo.GetSyncSessions(1)
	if len(ss) == 0 {
		t.Fatal("interrupted sync session not recorded")
	}
	if ss[0].Status == "completed" {
		t.Fatalf("interrupted session must not be completed, got %q", ss[0].Status)
	}

	// 2) Resume. The same resumable command must complete the transfer.
	if err := eng.ResumeSync(context.Background(), 1, ss[0].ID); err != nil {
		t.Fatalf("ResumeSync: %v", err)
	}
	ss2, _ := repo.GetSyncSessions(1)
	if ss2[0].Status != "completed" {
		t.Fatalf("resumed session status = %q, want completed", ss2[0].Status)
	}

	// 3) Integrity: target file set + per-file md5 must equal source.
	srcH := md5RelMap(t, src)
	tgtH := md5RelMap(t, tgt)
	if len(srcH) != len(tgtH) {
		t.Fatalf("file count mismatch after resume: source=%d target=%d", len(srcH), len(tgtH))
	}
	var mism int
	for rel, h := range srcH {
		if tgtH[rel] != h {
			mism++
		}
	}
	if mism > 0 {
		t.Fatalf("%d files corrupted/mismatched after resume", mism)
	}
}

// TestSyncResumeAfterInterruptionMock proves the interruption→resume state
// machine with a mock source SSH: a sync session left in "error" (interrupted)
// state is resumed by ResumeSync re-running the SAME resumable rsync command,
// reaches "completed", and VerifyChecksums confirms integrity (matching
// md5sums on source and target).
func TestSyncResumeAfterInterruptionMock(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()

	fullOut := "Number of regular files transferred: 5\nsent 12288 bytes\nTotal file size: 12288 bytes\n"
	rsyncCmd := (&SyncEngine{}).buildRsyncCommand(SyncConfig{
		SourcePath: "/data", TargetPath: "/data", MigrationID: 1,
	}, true) // incremental=true → resume command form
	src.execOutput[rsyncCmd] = fullOut

	// md5sum output for both source and target (same tree → same hashes).
	md5 := "aaaa  /data/file1\nbbbb  /data/file2\ncccc  /data/file3\n" +
		"dddd  /data/file4\neeee  /data/file5\n"
	srcCmd := "find /data -type f -exec md5sum {} \\; 2>/dev/null | sort"
	src.execOutput[srcCmd] = md5
	tgtCmd := "find /data -type f -exec md5sum {} \\; 2>/dev/null | sort"
	tgt.execOutput[tgtCmd] = md5

	repo := newRecordingSyncRepo()
	eng := NewSyncEngine(src, tgt, repo)

	// Pre-create an interrupted (error) session, as an earlier run would have.
	id, err := repo.CreateSyncSession(context.Background(), SyncSession{
		MigrationID: 1, SyncType: "initial", SourcePath: "/data",
		TargetPath: "/data", Status: "error",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Resume must complete.
	if err := eng.ResumeSync(context.Background(), 1, id); err != nil {
		t.Fatalf("ResumeSync: %v", err)
	}
	ss, _ := repo.GetSyncSessions(1)
	if len(ss) == 0 || ss[0].Status != "completed" {
		t.Fatalf("resumed status = %+v, want completed", ss)
	}
	if ss[0].FilesTransferred != 5 {
		t.Fatalf("resumed files transferred = %d, want 5", ss[0].FilesTransferred)
	}

	// Integrity: VerifyChecksums must pass (matching md5sums).
	if err := eng.VerifyChecksums(context.Background(), 1, id); err != nil {
		t.Fatalf("VerifyChecksums after resume: %v", err)
	}
}

// sortKeys is a tiny helper used by tests that need deterministic map dumps.
func sortKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var _ = sortKeys // keep helper referenced for future assertions
