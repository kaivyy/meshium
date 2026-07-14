package migration

import (
	"context"
	"testing"
)

// TestResourceLockExcludesDistinctMigrations asserts the (source,target)
// resource lock stops two DISTINCT migrations from holding the same server at
// once — the gap runningPipelines (per-migration) and sem (global count) leave
// open. Migration A: 3→4 and B: 4→5 must not both hold server 4.
func TestResourceLockExcludesDistinctMigrations(t *testing.T) {
	p := newCommitPipeline(t)

	// Migration A holds servers 3 and 4.
	a := p.acquireResourceLocks(3, 4)
	defer p.releaseResourceLocks(a)

	if !p.canAcquireResource(1, 2) {
		t.Fatal("distinct servers (1,2) should be acquirable while A holds (3,4)")
	}
	if p.canAcquireResource(4, 5) {
		t.Fatal("server 4 is held by A; migration B (4→5) must NOT acquire it")
	}
	if p.canAcquireResource(3, 9) {
		t.Fatal("server 3 is held by A; a second holder must be rejected")
	}
}

// TestResourceLockRefcountReleases asserts a refcounted release frees a server
// only once every holder has released, and that the same migration holding the
// same server twice does not deadlock itself on release.
func TestResourceLockRefcountReleases(t *testing.T) {
	p := newCommitPipeline(t)

	first := p.acquireResourceLocks(7, 8)
	if p.canAcquireResource(7, 8) {
		t.Fatal("server 7 and 8 should be held")
	}
	// Same migration re-acquires the same servers (e.g. a stage retry path).
	second := p.acquireResourceLocks(7, 8)
	if p.canAcquireResource(7) {
		t.Fatal("server 7 still held (refcount 2) — must stay blocked")
	}
	p.releaseResourceLocks(first)
	if p.canAcquireResource(7) {
		t.Fatal("server 7 must stay blocked until the second holder releases")
	}
	p.releaseResourceLocks(second)
	if !p.canAcquireResource(7, 8) {
		t.Fatal("servers 7 and 8 must be free after both holders release")
	}
}

// TestExecuteRejectsWhenServerBusy asserts Execute fails closed (not blocked,
// not silently racing) when the source or target is held by another migration,
// so two distinct migrations never fork the same host simultaneously.
func TestExecuteRejectsWhenServerBusy(t *testing.T) {
	p := newCommitPipeline(t)
	// Another migration holds server 2 (the target of our migration 1->2).
	other := p.acquireResourceLocks(2)
	defer p.releaseResourceLocks(other)

	migID, err := p.jobRepo.CreateMigration(1, 2, []string{"packages"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	// No onProgress callback / no real server reach: Execute must bail at the
	// resource check before attempting SSH, returning the busy error.
	runErr := p.Execute(t.Context(), migID, func(WSMessage) {})
	if runErr == nil {
		t.Fatal("expected Execute to fail because target server 2 is busy")
	}
}

// TestWarnTransferLimitsHonesty asserts the honesty guard is a safe no-op for
// nil config and does not panic when limits are set. The warning is emitted to
// the log, not returned, so the live applier-based path never silently honors
// limits it cannot apply.
func TestWarnTransferLimitsHonesty(t *testing.T) {
	warnTransferLimitsHonesty(context.Background(), nil)
	warnTransferLimitsHonesty(context.Background(), &MigrationConfig{
		BandwidthLimit:    100,
		ParallelTransfers: 2,
	})
}
