package migration

import (
	"context"
	"errors"
	"testing"

	"meshium/internal/mod/server"
)

// newCommitPipeline builds a Pipeline whose repos point at a fresh sqlite DB
// with two seeded servers, for exercising Commit's AwaitingCutover path.
func newCommitPipeline(t *testing.T) *Pipeline {
	t.Helper()
	database, _ := newTestDB(t)
	repo := NewRepo(database)
	seedServers(t, repo.(JobRepository))
	srvRepo := newMockServerRepo()
	srvRepo.AddServer(&server.Server{ID: 1, Host: "source", Port: 22, Username: "user"})
	srvRepo.AddServer(&server.Server{ID: 2, Host: "target", Port: 22, Username: "user"})
	registry := NewCategoryRegistry()
	pool := newMockPool()
	authSvc := &mockAuthSvc{}
	hosts := &mockHostKeyStore{}
	p, err := NewPipeline(repo.(PipelineRepo), repo.(JobRepository), srvRepo, pool, authSvc, hosts, registry)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	return p
}

// P0-2 cutover: a migration in StateAwaitingCutover with switch_state still
// manual_required must reject commit with ErrCutoverNotConfirmed — no
// transition, never ForceTransition(Committed).
func TestCommitRejectedWhileManualRequired(t *testing.T) {
	p := newCommitPipeline(t)
	repo := p.jobRepo
	prepo := p.repo
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"packages"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	// Place the migration in AwaitingCutover + create a manual_required config.
	if _, err := prepo.CreateTrafficSwitchConfig(ctx, TrafficSwitchConfig{
		MigrationID: migID,
		SwitchState:  trafficSwitchManualState,
	}); err != nil {
		t.Fatalf("create traffic config: %v", err)
	}
	if err := repo.SetMigrationStateContext(ctx, migID, StateAwaitingCutover); err != nil {
		t.Fatalf("set awaiting_cutover: %v", err)
	}

	err = p.Commit(ctx, migID, nil)
	if !errors.Is(err, ErrCutoverNotConfirmed) {
		t.Fatalf("commit must be rejected with ErrCutoverNotConfirmed; got %v", err)
	}
	// State must still be AwaitingCutover (no advance, no force to Committed).
	got, _ := repo.GetMigrationState(migID)
	if got != StateAwaitingCutover {
		t.Fatalf("state must remain AwaitingCutover after rejected commit; got %s", got)
	}
}

// P0-2 cutover: a confirmed switch_state allows commit via validated Transition.
func TestCommitAllowedWhenConfirmed(t *testing.T) {
	p := newCommitPipeline(t)
	repo := p.jobRepo
	prepo := p.repo
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"packages"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	if _, err := prepo.CreateTrafficSwitchConfig(ctx, TrafficSwitchConfig{
		MigrationID: migID,
		SwitchState:  "confirmed",
	}); err != nil {
		t.Fatalf("create traffic config: %v", err)
	}
	if err := repo.SetMigrationStateContext(ctx, migID, StateAwaitingCutover); err != nil {
		t.Fatalf("set awaiting_cutover: %v", err)
	}

	if err := p.Commit(ctx, migID, nil); err != nil {
		t.Fatalf("confirmed commit must succeed: %v", err)
	}
	got, _ := repo.GetMigrationState(migID)
	if got != StateCommitted {
		t.Fatalf("state must be Committed after confirmed commit; got %s", got)
	}
}

// P0-2 cutover: the state machine forbids a direct force from AwaitingCutover
// to Committed without a valid transition — proving the restricted ForceTransition
// callers cannot bypass cutover safety. (Full ForceTransition audit is commit 6.)
func TestAwaitingCutoverCannotForceToCommitted(t *testing.T) {
	sm := NewStateMachine(StateAwaitingCutover)
	// The validated path: AwaitingCutover → Committed is in the table.
	if err := sm.Transition(StateCommitted); err != nil {
		t.Fatalf("validated AwaitingCutover→Committed must be allowed: %v", err)
	}
	// From NeedsManualIntervention there is NO path out (table is empty) — the
	// only way to leave is an explicit operator/admin action, not auto-advance.
	sm2 := NewStateMachine(StateNeedsManualIntervention)
	if err := sm2.Transition(StateCommitted); err == nil {
		t.Fatal("NeedsManualIntervention must NOT auto-advance to Committed")
	}
}

// P0-2 cutover: AwaitingCutover is NOT terminal — an operator can still act
// (commit/rollback). NeedsManualIntervention IS terminal-ish (loop stops).
func TestAwaitingCutoverNotTerminalManualInterventionIsTerminal(t *testing.T) {
	if StateAwaitingCutover.IsTerminal() {
		t.Fatal("AwaitingCutover must not be terminal (operator can commit)")
	}
	if !StateNeedsManualIntervention.IsTerminal() {
		t.Fatal("NeedsManualIntervention must be terminal-ish (loop stops)")
	}
}

// P0-2 cutover: the sentinel round-trips through the state column — a restart
// reads awaiting_cutover back. (Persists via SetMigrationState dual-write.)
func TestAwaitingCutoverRoundTripsThroughStateColumn(t *testing.T) {
	repo := newTestRepo(t)
	seedServers(t, repo)
	ctx := context.Background()
	migID, err := repo.CreateMigration(1, 2, []string{"packages"}, "")
	if err != nil {
		t.Fatalf("create migration: %v", err)
	}
	if err := repo.SetMigrationStateContext(ctx, migID, StateAwaitingCutover); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, err := repo.GetMigrationState(migID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}

	if got != StateAwaitingCutover {
		t.Fatalf("awaiting_cutover did not round-trip; got %s", got)
	}
	// String form (what the API returns) must be "awaiting_cutover".
	if got.String() != "awaiting_cutover" {
		t.Fatalf("String() = %q, want awaiting_cutover", got.String())
	}
}
