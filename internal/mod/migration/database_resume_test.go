package migration

import (
	"context"
	"testing"

	"meshium/internal/mod/transfer"
)

// memCheckpointStore is an in-memory transfer.CheckpointStore for tests.
type memCheckpointStore struct {
	cps map[string]*transfer.TransferCheckpoint
}

func newMemCheckpointStore() *memCheckpointStore {
	return &memCheckpointStore{cps: make(map[string]*transfer.TransferCheckpoint)}
}

func cpKey(tid, file string) string { return tid + "\x00" + file }

func (s *memCheckpointStore) SaveCheckpoint(cp transfer.TransferCheckpoint) error {
	c := cp
	s.cps[cpKey(cp.TransferID, cp.FileName)] = &c
	return nil
}
func (s *memCheckpointStore) GetCheckpoint(tid, file string) (*transfer.TransferCheckpoint, error) {
	if cp, ok := s.cps[cpKey(tid, file)]; ok {
		return cp, nil
	}
	return nil, nil
}
func (s *memCheckpointStore) DeleteCheckpoint(tid, file string) error {
	delete(s.cps, cpKey(tid, file))
	return nil
}
func (s *memCheckpointStore) DeleteCheckpoints(tid string) error {
	for k := range s.cps {
		if len(k) >= len(tid) && k[:len(tid)] == tid {
			delete(s.cps, k)
		}
	}
	return nil
}

// TestDatabaseResumeRefusesWhenSourceMoved proves the fail-closed core of Part 5:
// if a persisted checkpoint exists but the source dump changed after it was
// taken, reconcileOrFail returns fail=true (ManualIntervention) and applyFile
// must NOT blind-resume. We drive reconcileOrFail directly with a mock whose
// stat output differs from the checkpoint's source snapshot.
func TestDatabaseResumeRefusesWhenSourceMoved(t *testing.T) {
	store := newMemCheckpointStore()
	// Persisted checkpoint: source snapshot was 1000:1700000000.
	store.SaveCheckpoint(transfer.TransferCheckpoint{
		TransferID:       "db:7:mydb",
		MigrationID:      7,
		Category:         "database",
		FileName:         "mydb",
		Strategy:         "scp",
		SourceSnapshot:   "1000:1700000000",
		BytesTransferred: 500,
		Resumable:        true,
		LastVerifiedPhase: "transferred",
	})

	ssh := newMockSSH()
	// Source stat now reports a DIFFERENT size+mtime (dump regenerated / moved).
	ssh.addOutput("stat -c", "2000 1700000001")
	// Target partial exists (500 bytes) — a naive resume would append to it.
	ssh.addOutput("test -f", "yes")

	a := &DatabaseApplier{
		migrationID:    7,
		checkpointStore: store,
		sourceSSH:      ssh,
	}
	srcTarget := transfer.TransferTarget{Path: "/tmp/meshium_dump_7_mydb", SSHClient: ssh}
	tgtTarget := transfer.TransferTarget{Path: "/tmp/meshium_dump_7_mydb", SSHClient: ssh}

	verdict, fail := a.reconcileOrFail(context.Background(), "db:7:mydb", "mydb", srcTarget, tgtTarget)
	if !fail {
		t.Fatalf("expected fail-closed (source moved), got verdict=%s fail=%v", verdict, fail)
	}
	if verdict != transfer.VerdictManualIntervention {
		t.Errorf("expected ManualIntervention, got %s", verdict)
	}
}

// TestDatabaseResumeAllowsWhenSourceStable proves the happy path: a persisted
// checkpoint whose source snapshot still matches (and a partial target is
// present) reconciles to Resume with fail=false, so applyFile proceeds.
func TestDatabaseResumeAllowsWhenSourceStable(t *testing.T) {
	store := newMemCheckpointStore()
	store.SaveCheckpoint(transfer.TransferCheckpoint{
		TransferID:       "db:7:mydb",
		MigrationID:      7,
		Category:         "database",
		FileName:         "mydb",
		Strategy:         "scp",
		SourceSnapshot:   "1000:1700000000",
		BytesTransferred: 500,
		Resumable:        true,
		LastVerifiedPhase: "transferred",
	})

	ssh := newMockSSH()
	ssh.addOutput("stat -c", "1000 1700000000") // unchanged
	ssh.addOutput("test -f", "yes")              // partial present

	a := &DatabaseApplier{
		migrationID:     7,
		checkpointStore: store,
		sourceSSH:       ssh,
	}
	srcTarget := transfer.TransferTarget{Path: "/tmp/meshium_dump_7_mydb", SSHClient: ssh}
	tgtTarget := transfer.TransferTarget{Path: "/tmp/meshium_dump_7_mydb", SSHClient: ssh}

	verdict, fail := a.reconcileOrFail(context.Background(), "db:7:mydb", "mydb", srcTarget, tgtTarget)
	if fail {
		t.Fatalf("expected safe-to-resume, got fail=true verdict=%s", verdict)
	}
	if verdict != transfer.VerdictResume {
		t.Errorf("expected Resume, got %s", verdict)
	}
}
