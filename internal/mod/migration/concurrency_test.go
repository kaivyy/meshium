package migration

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPipelineConcurrencyCap asserts the global semaphore never admits more
// than maxConcurrent simultaneous holders. This is the ceiling that prevents a
// fleet of distinct migrations from saturating source/target disks and the SSH
// pool, even though runningPipelines only dedupes a single migration.
func TestPipelineConcurrencyCap(t *testing.T) {
	p := newCommitPipeline(t)
	if p.maxConcurrent != DefaultMaxConcurrentMigrations {
		t.Fatalf("expected default cap %d, got %d", DefaultMaxConcurrentMigrations, p.maxConcurrent)
	}

	// Fill every slot.
	held := 0
	for i := 0; i < p.maxConcurrent; i++ {
		if !p.acquireSlot(context.Background()) {
			t.Fatalf("slot %d should have been acquired", i)
		}
		held++
	}
	if len(p.sem) != held {
		t.Fatalf("expected %d slots held, channel len is %d", held, len(p.sem))
	}

	// One more must block (and not acquire) once the cap is reached. Use a
	// timed context — a forever-blocking acquire would hang the test.
	blockedCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if p.acquireSlot(blockedCtx) {
		t.Fatal("expected acquisition to block once the cap is reached")
	}

	// Releasing a slot must let the next acquisition succeed.
	p.releaseSlot()
	if !p.acquireSlot(context.Background()) {
		t.Fatal("expected acquisition to succeed after a release")
	}
	p.releaseSlot()
}

// TestSetMaxConcurrentChangesCap asserts an operator can lower/raise the global
// ceiling and that <=0 is rejected (keeps the existing cap).
func TestSetMaxConcurrentChangesCap(t *testing.T) {
	p := newCommitPipeline(t)

	p.SetMaxConcurrent(2)
	if p.maxConcurrent != 2 {
		t.Fatalf("expected cap 2, got %d", p.maxConcurrent)
	}
	if cap(p.sem) != 2 {
		t.Fatalf("expected sem capacity 2, got %d", cap(p.sem))
	}
	// Fill the new, smaller cap.
	for i := 0; i < 2; i++ {
		if !p.acquireSlot(context.Background()) {
			t.Fatalf("slot %d should acquire under cap 2", i)
		}
	}
	blockedCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if p.acquireSlot(blockedCtx) {
		t.Fatal("cap 2 should block the third acquisition")
	}
	p.releaseSlot()
	p.releaseSlot()

	// Invalid value is ignored.
	p.SetMaxConcurrent(0)
	if p.maxConcurrent != 2 {
		t.Fatalf("expected cap unchanged at 2, got %d", p.maxConcurrent)
	}
}

// TestSyncConfigWiresTransferControls asserts the per-migration bandwidth and
// parallel-transfer limits declared on MigrationConfig actually reach the
// SyncConfig that drives rsync. This is the bug: the builder ignored pc.Config.
func TestSyncConfigWiresTransferControls(t *testing.T) {
	pc := &PipelineContext{
		MigrationID: 7,
		Config: &MigrationConfig{
			BandwidthLimit:    512,
			ParallelTransfers: 8,
		},
	}
	cfg := syncConfigFromPipelineContext(pc)
	if cfg.BandwidthLimit != 512 {
		t.Fatalf("expected bandwidth 512, got %d", cfg.BandwidthLimit)
	}
	if cfg.ParallelTransfers != 8 {
		t.Fatalf("expected parallel 8, got %d", cfg.ParallelTransfers)
	}
}

// TestBuildRsyncCommandParallel asserts --parallel is emitted only when
// ParallelTransfers > 1 (rsync rejects --parallel=1), mirroring the
// BandwidthLimit > 0 guard.
func TestBuildRsyncCommandParallel(t *testing.T) {
	engine := &SyncEngine{}

	single := engine.buildRsyncCommand(SyncConfig{SourcePath: "/s", TargetPath: "/d", ParallelTransfers: 1}, false)
	if strings.Contains(single, "--parallel=") {
		t.Fatalf("expected no --parallel at value 1, got %q", single)
	}

	multi := engine.buildRsyncCommand(SyncConfig{SourcePath: "/s", TargetPath: "/d", ParallelTransfers: 6}, false)
	if !strings.Contains(multi, "--parallel=6") {
		t.Fatalf("expected --parallel=6, got %q", multi)
	}
}
