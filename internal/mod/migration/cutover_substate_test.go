package migration

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newCutoverHarness builds a migrated sqlite DB, seeds servers + a migration,
// creates the traffic_switch stage, and returns a cutoverMachine + its
// FencingAuthority (fake clock) + the migration id. This is the shared harness
// for the sub-state machine tests.
func newCutoverHarness(t *testing.T) (*cutoverMachine, *FencingAuthority, *fakeClock, int, PipelineRepo) {
	t.Helper()
	repo := newTestRepo(t)
	prepo := repo.(PipelineRepo)
	sqlite := repo.(*sqliteRepo)
	seedServers(t, repo)
	ctx := context.Background()
	migID, err := repo.CreateMigration(1, 2, []string{"postgres"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	if _, err := prepo.CreateStage(ctx, migID, string(StageTrafficSwitch), 0); err != nil {
		t.Fatalf("create stage: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
	auth := &FencingAuthority{repo: sqlite, clock: clock, ttl: FenceTTL}
	m := newCutoverMachine(prepo, auth, migID)
	return m, auth, clock, migID, prepo
}

// freshCheckpoint returns a checkpoint initialized at SubPreflight with an
// empty Steps map — the starting point the orchestrator saves before the
// first MarkStep.
func freshCheckpoint() *cutoverCheckpoint {
	return &cutoverCheckpoint{SubState: SubPreflight, Steps: map[CutoverSubState]string{}}
}

// TestCutoverLegalForwardWalk drives every legal edge Preflight→Completed via
// MarkStep and verifies the persisted sub-state after each advance. Restart
// reconciliation (Load) must return the same sub-state.
func TestCutoverLegalForwardWalk(t *testing.T) {
	m, auth, _, migID, _ := newCutoverHarness(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, migID, "meshium-A", string(SubPreflight))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cp := freshCheckpoint()
	if err := m.Save(ctx, cp, lease); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	steps := []struct {
		from, to CutoverSubState
		data     string
	}{
		{SubPreflight, SubSeed, "preflight-ok"},
		{SubSeed, SubReplicating, "seed-ok"},
		{SubReplicating, SubVerifying, "repl-ok"},
		{SubVerifying, SubAwaitingCutover, "verify-ok"},
		{SubAwaitingCutover, SubFencingSource, "await-ok"},
		{SubFencingSource, SubCatchingUp, "fence-ok"},
		{SubCatchingUp, SubVerifyingTarget, "catchup-ok"},
		{SubVerifyingTarget, SubSwitching, "verifytarget-ok"},
		{SubSwitching, SubPromoting, "switch-ok"},
		{SubPromoting, SubObserving, "promote-ok"},
		{SubObserving, SubCompleted, "observe-ok"},
	}
	for i, s := range steps {
		next, err := m.MarkStep(ctx, cp, lease, s.from, s.to, s.data)
		if err != nil {
			t.Fatalf("step %d %s→%s: %v", i, s.from, s.to, err)
		}
		cp = next
		if cp.SubState != s.to {
			t.Fatalf("step %d substate=%q want %q", i, cp.SubState, s.to)
		}
		if !cp.StepDone(s.from) {
			t.Fatalf("step %d %s not marked done", i, s.from)
		}
		// Restart reconciliation: a fresh machine loads the same sub-state.
		again := newCutoverMachine(m.repo, m.authority, m.migrationID)
		loaded, err := again.Load(ctx)
		if err != nil {
			t.Fatalf("step %d reload: %v", i, err)
		}
		if loaded.SubState != s.to {
			t.Fatalf("step %d reload substate=%q want %q", i, loaded.SubState, s.to)
		}
	}
	if cp.SubState != SubCompleted {
		t.Fatalf("final substate=%q want Completed", cp.SubState)
	}
}

// TestCutoverIllegalTransition: an out-of-order advance fails closed with
// ErrCutoverIllegalTransition and does NOT persist.
func TestCutoverIllegalTransition(t *testing.T) {
	m, auth, _, migID, _ := newCutoverHarness(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, migID, "meshium-A", string(SubPreflight))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cp := freshCheckpoint()
	if err := m.Save(ctx, cp, lease); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Seed→Promoting is not a legal edge.
	if _, err := m.MarkStep(ctx, cp, lease, SubSeed, SubPromoting, "bad"); !errors.Is(err, ErrCutoverIllegalTransition) {
		t.Fatalf("illegal edge: err=%v want ErrCutoverIllegalTransition", err)
	}
	// cp.SubState was Preflight, not Seed → also illegal via the divergence check.
	if _, err := m.MarkStep(ctx, cp, lease, SubPreflight, SubPromoting, "bad"); !errors.Is(err, ErrCutoverIllegalTransition) {
		t.Fatalf("illegal forward: err=%v want ErrCutoverIllegalTransition", err)
	}
}

// TestCutoverIdempotentReentry: re-running an already-done step returns
// ErrCutoverReentryDone (a skip signal, not a hard error) and leaves the
// persisted state unchanged.
func TestCutoverIdempotentReentry(t *testing.T) {
	m, auth, _, migID, _ := newCutoverHarness(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, migID, "meshium-A", string(SubPreflight))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cp := freshCheckpoint()
	if err := m.Save(ctx, cp, lease); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := m.MarkStep(ctx, cp, lease, SubPreflight, SubSeed, "preflight-ok"); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if !cp.StepDone(SubPreflight) {
		t.Fatal("preflight should be done")
	}
	// Re-enter: re-run Preflight→Seed. The step already has a result.
	cp.SubState = SubPreflight // orchestrator rewinds its view to re-run the step
	next, err := m.MarkStep(ctx, cp, lease, SubPreflight, SubSeed, "preflight-ok-again")
	if !errors.Is(err, ErrCutoverReentryDone) {
		t.Fatalf("reentry: err=%v want ErrCutoverReentryDone", err)
	}
	if next.Steps[SubPreflight] != "preflight-ok" {
		t.Fatalf("reentry overwrote result: got %q", next.Steps[SubPreflight])
	}
}

// TestCutoverLoadEmptyWhenNoCheckpoint: a migration with no traffic_switch
// checkpoint loads an empty checkpoint (cutover not started).
func TestCutoverLoadEmptyWhenNoCheckpoint(t *testing.T) {
	m, _, _, _, _ := newCutoverHarness(t)
	cp, err := m.Load(context.Background())
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if cp.SubState != "" {
		t.Fatalf("empty load substate=%q want empty", cp.SubState)
	}
}

// TestCutoverLoadStaleLeaseFailsClosed: a checkpoint whose lease has expired
// without release loads as ErrFenceLeaseStale → orchestrator maps to
// NeedsManualIntervention.
func TestCutoverLoadStaleLeaseFailsClosed(t *testing.T) {
	m, auth, clock, migID, _ := newCutoverHarness(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, migID, "meshium-A", string(SubPreflight))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cp := freshCheckpoint()
	if err := m.Save(ctx, cp, lease); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Expire the lease without releasing (stale).
	clock.t = clock.t.Add(FenceTTL + time.Second)
	if _, err := m.Load(ctx); !errors.Is(err, ErrFenceLeaseStale) {
		t.Fatalf("stale load: err=%v want ErrFenceLeaseStale", err)
	}
}

// TestCutoverLoadMissingLeaseFailsClosed: a checkpoint that references a
// holder/token but no lease row exists (e.g. lease row deleted) loads as
// ErrFenceLeaseNotHeld → NeedsManualIntervention.
func TestCutoverLoadMissingLeaseFailsClosed(t *testing.T) {
	m, _, _, _, _ := newCutoverHarness(t)
	ctx := context.Background()
	// Persist a checkpoint that claims a holder/token without ever acquiring.
	cp := &cutoverCheckpoint{SubState: SubFencingSource, Token: 7, Holder: "meshium-ghost", Steps: map[CutoverSubState]string{}}
	if err := m.Save(ctx, cp, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := m.Load(ctx); !errors.Is(err, ErrFenceLeaseNotHeld) {
		t.Fatalf("missing lease load: err=%v want ErrFenceLeaseNotHeld", err)
	}
}

// TestCutoverSelfLoopReentry: a self-edge (from==to) is legal and used for
// idempotent re-entry at the same step. On a done step it returns
// ErrCutoverReentryDone; on a fresh step it records and stays.
func TestCutoverSelfLoopReentry(t *testing.T) {
	m, auth, _, migID, _ := newCutoverHarness(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, migID, "meshium-A", string(SubReplicating))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cp := &cutoverCheckpoint{SubState: SubReplicating, Steps: map[CutoverSubState]string{}}
	if err := m.Save(ctx, cp, lease); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Fresh self-loop: records and stays at SubReplicating.
	next, err := m.MarkStep(ctx, cp, lease, SubReplicating, SubReplicating, "repl-retry-ok")
	if err != nil {
		t.Fatalf("fresh self-loop: %v", err)
	}
	if next.SubState != SubReplicating {
		t.Fatalf("self-loop advanced: %q", next.SubState)
	}
	// Done self-loop: skip signal.
	if _, err := m.MarkStep(ctx, next, lease, SubReplicating, SubReplicating, "again"); !errors.Is(err, ErrCutoverReentryDone) {
		t.Fatalf("done self-loop: err=%v want ErrCutoverReentryDone", err)
	}
}

// TestCutoverCompletedIsTerminal: SubCompleted has no legal forward edge;
// any advance from it is illegal.
func TestCutoverCompletedIsTerminal(t *testing.T) {
	if validCutoverTransition(SubCompleted, SubPreflight) {
		t.Fatal("Completed must not transition back to Preflight")
	}
	if validCutoverTransition(SubCompleted, SubObserving) {
		t.Fatal("Completed must not transition to Observing")
	}
}
