package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Phase 2A-6 orchestrator tests. These verify the *orchestration* contract —
// the fencing-before-every-step gate, fail-closed on any step failure,
// idempotent re-entry, and the no-dual-writer switch-before-promote ordering.
// The primitives each step calls are faked so the test is about the machine,
// not PG/nginx. The real primitives have their own tests (pg_cutover_test.go,
// nginx_switch_test.go).

// fakePGDriver records calls and returns scripted errors. targetInRecovery
// controls TargetInRecovery so stepVerifyingTarget's standby check can pass or
// fail. If promoteErr is set, CutoverPromote returns it.
type fakePGDriver struct {
	preflights   int
	catchups     int
	promotes     int
	preflightErr error
	catchupErr   error
	freezeErr    error
	promoteErr   error
	// targetInRecovery is returned as CutoverPreflightResult.TargetInRecovery.
	targetInRecovery bool
}

func (f *fakePGDriver) CutoverPreflight(ctx context.Context, config ReplicationConfig, sourceFrozen bool) (CutoverPreflightResult, error) {
	f.preflights++
	if f.preflightErr != nil {
		return CutoverPreflightResult{TargetInRecovery: f.targetInRecovery}, f.preflightErr
	}
	// A healthy pair: same major, source primary, target standby, replicator ok.
	return CutoverPreflightResult{
		SourceVersionNum: 150003,
		TargetVersionNum: 150001,
		SourceInRecovery: false,
		TargetInRecovery: f.targetInRecovery,
		ReplicatorOK:     true,
	}, nil
}

func (f *fakePGDriver) WaitForCatchUp(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error {
	f.catchups++
	return f.catchupErr
}

func (f *fakePGDriver) CutoverFreezeSource(ctx context.Context, config ReplicationConfig) error {
	if f.freezeErr != nil {
		return f.freezeErr
	}
	return nil // PG supports source freeze
}

func (f *fakePGDriver) CutoverPromote(ctx context.Context, config ReplicationConfig) error {
	f.promotes++
	return f.promoteErr
}

// fakeTrafficSwitch records the switch call and scripts its result/error.
type fakeTrafficSwitch struct {
	switches  int
	result   *NginxSwitchResult
	switchErr error
}

func (f *fakeTrafficSwitch) Switch(ctx context.Context, req NginxSwitchRequest) (*NginxSwitchResult, error) {
	f.switches++
	if f.switchErr != nil {
		return nil, f.switchErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return &NginxSwitchResult{Switched: true, Verified: true, ConfigTestOK: true, ReloadOK: true}, nil
}

// orchestratorHarness builds a fully-wired orchestrator against an in-memory
// sqlite repo: a migrated migration + a traffic_switch stage + a fencing
// authority with a fake clock. Returns the orchestrator, the fakes, and the
// clock so tests can expire the lease mid-run.
func orchestratorHarness(t *testing.T) (*CutoverOrchestrator, *fakePGDriver, *fakeTrafficSwitch, *fakeClock, *FencingAuthority, *sqliteRepo) {
	t.Helper()
	database, _ := newTestDB(t)
	repo := NewRepo(database).(*sqliteRepo) // sqliteRepo satisfies PipelineRepo
	for _, sid := range []int{1, 2} {
		if _, err := database.Exec(
			`INSERT INTO servers (id, name, host, port, username) VALUES (?, ?, ?, ?, ?)`,
			sid, "srv", "127.0.0.1", 22, "u",
		); err != nil {
			t.Fatalf("seed server %d: %v", sid, err)
		}
	}
	if _, err := database.Exec(
		`INSERT INTO migrations (id, source_id, target_id, categories, status)
		 VALUES (1, 1, 2, ?, ?)`,
		"postgres", "planned",
	); err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	if _, err := repo.CreateStage(context.Background(), 1, string(StageTrafficSwitch), 0); err != nil {
		t.Fatalf("create stage: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
	auth := &FencingAuthority{repo: repo, clock: clock, ttl: FenceTTL}
	machine := newCutoverMachine(repo, auth, 1)
	pg := &fakePGDriver{targetInRecovery: true}
	traffic := &fakeTrafficSwitch{}
	o := NewCutoverOrchestrator(machine, auth, pg, traffic)
	return o, pg, traffic, clock, auth, repo
}

// cutoverReq returns a canonical request. ObserveFor is 0 so the observation
// step does not sleep; MaxLagSeconds 1 so catch-up uses a small threshold.
func cutoverReq() CutoverRequest {
	return CutoverRequest{
		MigrationID:   1,
		Holder:        "meshium-test",
		Replication:    pgPreflightConfig(),
		TrafficRequest: nginxSwitchReq("http://verify/health"),
		MaxLagSeconds:  1,
		ObserveFor:     0,
	}
}

// TestCutoverOrchestratorHappyPath: a clean run reaches SubCompleted, calls
// preflight + catch-up + switch + promote exactly the expected number of times,
// and marks the outcome completed. The lease is released at the end.
func TestCutoverOrchestratorHappyPath(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	out, err := o.Run(context.Background(), cutoverReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !out.Completed || out.FinalState != SubCompleted {
		t.Fatalf("not completed: %+v", out)
	}
	// Preflight runs at Preflight, Seed, VerifyingTarget = 3 calls.
	if pg.preflights != 3 {
		t.Fatalf("preflights=%d want 3", pg.preflights)
	}
	// Catch-up at Replicating, Verifying, CatchingUp = 3 calls.
	if pg.catchups != 3 {
		t.Fatalf("catchups=%d want 3", pg.catchups)
	}
	// Switch and promote each exactly once.
	if traffic.switches != 1 {
		t.Fatalf("switches=%d want 1", traffic.switches)
	}
	if pg.promotes != 1 {
		t.Fatalf("promotes=%d want 1", pg.promotes)
	}
	if out.Holder != "meshium-test" || out.Token == 0 {
		t.Fatalf("outcome missing lease identity: holder=%q token=%d", out.Holder, out.Token)
	}
}

// TestCutoverOrchestratorDegradesWhenSourceFreezeUnsupported: when the engine
// cannot physically freeze the source (MySQL/Redis today, ErrNoSourceFreeze),
// the cutover must still complete but record the degraded condition rather than
// silently claiming a closed dual-writer window. Any other freeze error still
// fails closed.
func TestCutoverOrchestratorDegradesWhenSourceFreezeUnsupported(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	pg.freezeErr = ErrNoSourceFreeze
	out, err := o.Run(context.Background(), cutoverReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !out.Completed {
		t.Fatalf("should complete (degraded is continuable): %+v", out)
	}
	found := false
	for _, d := range out.Degraded {
		if strings.HasPrefix(d, "source_freeze_not_supported:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("degraded condition not recorded: %+v", out.Degraded)
	}
	if traffic.switches != 1 || pg.promotes != 1 {
		t.Fatalf("switch/promote ran: switch=%d promote=%d", traffic.switches, pg.promotes)
	}
}

// TestCutoverOrchestratorFailClosedOnFreezeError: a real freeze error (not the
// "unsupported" class) fails closed — no switch, no promote.
func TestCutoverOrchestratorFailClosedOnFreezeError(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	pg.freezeErr = errors.New("ALTER SYSTEM rejected")
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("freeze error returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on freeze error")
	}
	if traffic.switches != 0 || pg.promotes != 0 {
		t.Fatalf("switch/promote ran after freeze error: switch=%d promote=%d", traffic.switches, pg.promotes)
	}
}

// TestCutoverOrchestratorNoDualWriterOrdering: switch (Switching) happens BEFORE
// promote (Promoting). The sub-state order is the invariant: traffic moves to
// the read-only standby first, then the standby is promoted. We assert this by
// recording call order via the fakes' counters during a real run.
func TestCutoverOrchestratorNoDualWriterOrdering(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	// Script: switch succeeds only after we flip a gate so PromotePG can observe
	// that traffic.switches already advanced. Use channels? Simpler: make
	// PromotePG assert switches==1 when it runs (proves switch ran first).
	traffic.result = &NginxSwitchResult{Switched: true, Verified: true, ConfigTestOK: true, ReloadOK: true}
	originalPromote := pg.promoteErr
	_ = originalPromote
	// Wrap promote to assert switch already ran: the wrapper observes the
	// shared traffic fake's switch counter when PromotePG fires.
	pg2 := &orderPGDriver{inner: pg, traffic: traffic}
	o.driver = pg2
	if _, err := o.Run(context.Background(), cutoverReq()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !pg2.promoteSawSwitch {
		t.Fatal("promote ran before switch (no-dual-writer invariant violated)")
	}
}

// orderPGDriver wraps fakePGDriver to assert switch-before-promote: its
// PromotePG reads the traffic fake's switch counter (the same pointer the
// orchestrator holds), proving traffic moved before the target was promoted.
type orderPGDriver struct {
	inner             *fakePGDriver
	traffic           *fakeTrafficSwitch
	promoteSawSwitch  bool
	switchCountAtPromote int
}

func (o *orderPGDriver) CutoverPreflight(ctx context.Context, config ReplicationConfig, sourceFrozen bool) (CutoverPreflightResult, error) {
	return o.inner.CutoverPreflight(ctx, config, sourceFrozen)
}
func (o *orderPGDriver) WaitForCatchUp(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error {
	return o.inner.WaitForCatchUp(ctx, config, maxLagSeconds)
}
func (o *orderPGDriver) CutoverFreezeSource(ctx context.Context, config ReplicationConfig) error {
	return o.inner.CutoverFreezeSource(ctx, config)
}
func (o *orderPGDriver) CutoverPromote(ctx context.Context, config ReplicationConfig) error {
	o.switchCountAtPromote = o.traffic.switches
	if o.switchCountAtPromote >= 1 {
		o.promoteSawSwitch = true
	}
	return o.inner.CutoverPromote(ctx, config)
}

// TestCutoverOrchestratorFailClosedOnPreflightFail: a preflight failure fails
// closed — outcome NOT completed, error returned, no switch/promote ran.
func TestCutoverOrchestratorFailClosedOnPreflightFail(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	pg.preflightErr = errors.New("source unreachable")
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("preflight failure returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on preflight failure")
	}
	if out.FinalState == SubCompleted {
		t.Fatal("advanced to completed on preflight failure")
	}
	if traffic.switches != 0 || pg.promotes != 0 {
		t.Fatal("switch/promote ran after preflight failure")
	}
	if out.Failure == "" {
		t.Fatal("failure blob empty")
	}
}

// TestCutoverOrchestratorFailClosedOnSwitchFail: a switch failure fails closed —
// no promote ran (the target was never promoted; traffic did not move).
func TestCutoverOrchestratorFailClosedOnSwitchFail(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	traffic.switchErr = ErrNginxVerifyFailed
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("switch failure returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on switch failure")
	}
	if pg.promotes != 0 {
		t.Fatal("promote ran after switch failure (would create a dual-writer)")
	}
}

// TestCutoverOrchestratorFailClosedOnPromoteFail: a promote failure fails closed.
// The switch already moved traffic to the (read-only) standby; the target is
// NOT promoted. Operator gets NeedsManualIntervention.
func TestCutoverOrchestratorFailClosedOnPromoteFail(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	pg.promoteErr = errors.New("pg_promote returned f")
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("promote failure returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on promote failure")
	}
	if traffic.switches != 1 {
		t.Fatalf("switch did not run before promote fail: %d", traffic.switches)
	}
}

// TestCutoverOrchestratorFailClosedOnStaleLeaseMidRun: the lease expires between
// steps. AssertHolds (inside advance) returns ErrFenceLeaseStale and the cutover
// fails closed at that step — no further mutation. We force this by advancing the
// fake clock past TTL before the run reaches the switch step.
func TestCutoverOrchestratorFailClosedOnStaleLeaseMidRun(t *testing.T) {
	o, pg, traffic, clock, _, _ := orchestratorHarness(t)
	// Script the switch to expire the clock before it returns, proving the
	// NEXT step's AssertHolds sees a stale lease. Simpler: advance the clock
	// past TTL inside a wrapped promote so the observation step's AssertHolds
	// fails. But observation is the last step; we want a mid-run failure.
	// Instead: wrap catch-up to advance the clock past TTL on the SECOND call
	// (Verifying → AwaitingCutover boundary), so FencingSource's AssertHolds
	// fails.
	pg.catchupErr = nil
	pg2 := &stalePGDriver{inner: pg, clock: clock}
	o.driver = pg2
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("stale lease returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on stale lease")
	}
	// Switch must NOT have run — the lease went stale before FencingSource,
	// which is before Switching.
	if traffic.switches != 0 {
		t.Fatalf("switch ran after stale lease: %d", traffic.switches)
	}
}

// stalePGDriver advances the fake clock past TTL on the second catch-up call,
// simulating a cutover that took longer than the lease lifetime.
type stalePGDriver struct {
	inner *fakePGDriver
	clock *fakeClock
	calls int
}

func (s *stalePGDriver) CutoverPreflight(ctx context.Context, config ReplicationConfig, sourceFrozen bool) (CutoverPreflightResult, error) {
	return s.inner.CutoverPreflight(ctx, config, sourceFrozen)
}
func (s *stalePGDriver) WaitForCatchUp(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error {
	s.calls++
	if s.calls >= 2 {
		// Advance past TTL so the next AssertHolds sees expiry.
		s.clock.t = s.clock.t.Add(FenceTTL + time.Second)
	}
	return s.inner.WaitForCatchUp(ctx, config, maxLagSeconds)
}
func (s *stalePGDriver) CutoverFreezeSource(ctx context.Context, config ReplicationConfig) error {
	return s.inner.CutoverFreezeSource(ctx, config)
}
func (s *stalePGDriver) CutoverPromote(ctx context.Context, config ReplicationConfig) error {
	return s.inner.CutoverPromote(ctx, config)
}

// TestCutoverOrchestratorIdempotentReentry: after a completed run, a second run
// short-circuits as already-completed (no new preflight/switch/promote). Also
// covers re-entry after a partial run: a re-run skips done steps. We test the
// completed short-circuit first (the common operator-retry case).
func TestCutoverOrchestratorIdempotentReentry(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	req := cutoverReq()
	if _, err := o.Run(context.Background(), req); err != nil {
		t.Fatalf("first run: %v", err)
	}
	preflightsBefore := pg.preflights
	switchesBefore := traffic.switches
	promotesBefore := pg.promotes

	out, err := o.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !out.Completed || out.FinalState != SubCompleted {
		t.Fatalf("re-entry not completed: %+v", out)
	}
	if pg.preflights != preflightsBefore || traffic.switches != switchesBefore || pg.promotes != promotesBefore {
		t.Fatalf("re-entry re-ran steps: pre=%d/%d switch=%d/%d promote=%d/%d",
			pg.preflights, preflightsBefore, traffic.switches, switchesBefore, pg.promotes, promotesBefore)
	}
}

// TestCutoverOrchestratorResumesAfterPartialFail: a run that fails at the switch
// leaves the checkpoint mid-machine; a second run (after the failure is cleared)
// resumes and completes, re-running only the not-yet-done step. The already-done
// steps (preflight, seed, catch-up, verify) are NOT re-run.
func TestCutoverOrchestratorResumesAfterPartialFail(t *testing.T) {
	o, pg, traffic, clock, _, _ := orchestratorHarness(t)
	// First run: switch fails. Lease is released on failure path (best-effort).
	// Re-acquire needs the prior lease released or expired; the failure path
	// does NOT release (it persists the checkpoint). So advance the clock past
	// TTL to let the second Acquire treat the prior lease as stale... but Acquire
	// refuses stale (returns ErrFenceLeaseStale) rather than silently replacing.
	// That is the CORRECT fail-closed behavior: a stale lease means manual
	// intervention. So a resume requires the lease to still be live.
	//
	// For this test we keep the clock frozen (lease live) and clear the switch
	// error on the second run. The orchestrator's acquireOrRecover asserts the
	// prior lease still holds (same holder/token) and resumes. Already-done
	// steps skip via ErrCutoverReentryDone.
	traffic.switchErr = ErrNginxVerifyFailed
	if _, err := o.Run(context.Background(), cutoverReq()); err == nil {
		t.Fatal("first run should have failed at switch")
	}
	preflightsAfterFail := pg.preflights
	catchupsAfterFail := pg.catchups

	// Clear the failure and resume. Clock frozen → lease still live.
	traffic.switchErr = nil
	out, err := o.Run(context.Background(), cutoverReq())
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if !out.Completed {
		t.Fatalf("resume not completed: %+v", out)
	}
	// Already-done steps did NOT re-run: preflights and catch-ups should be
	// the same as after the failed run. Only the switch + promote ran fresh.
	if pg.preflights != preflightsAfterFail {
		t.Fatalf("resume re-ran preflight: before=%d after=%d", preflightsAfterFail, pg.preflights)
	}
	if pg.catchups != catchupsAfterFail {
		t.Fatalf("resume re-ran catch-up: before=%d after=%d", catchupsAfterFail, pg.catchups)
	}
	if traffic.switches != 2 {
		t.Fatalf("switch should have run once more on resume: %d", traffic.switches)
	}
	if pg.promotes != 1 {
		t.Fatalf("promote should have run once on resume: %d", pg.promotes)
	}
	_ = clock
}

// TestCutoverOrchestratorFailsClosedOnTargetNotStandbyBeforeSwitch: if the
// target got promoted out-of-band between catch-up and switch, the
// verifying-target step refuses to switch — promoting an already-primary would
// be a no-op cutover with a dual-writer window.
func TestCutoverOrchestratorFailsClosedOnTargetNotStandbyBeforeSwitch(t *testing.T) {
	o, pg, traffic, _, _, _ := orchestratorHarness(t)
	// Flip target to a primary (not in recovery) so verifying-target fails.
	pg.targetInRecovery = false
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("target-not-standby returned no error; must fail closed")
	}
	if out.Completed {
		t.Fatal("marked completed on target-not-standby")
	}
	if traffic.switches != 0 {
		t.Fatalf("switch ran despite target not being a standby: %d", traffic.switches)
	}
}

// TestCutoverOrchestratorSanitizesFailure: the failure blob in the outcome is
// sanitized — no secret leaks. We inject a failure whose message contains a
// password-like token and assert it is redacted.
func TestCutoverOrchestratorSanitizesFailure(t *testing.T) {
	o, pg, _, _, _, _ := orchestratorHarness(t)
	pg.preflightErr = errors.New("source probe failed password=Hunter2 conn refused")
	out, err := o.Run(context.Background(), cutoverReq())
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(out.Failure, "Hunter2") {
		t.Fatalf("secret leaked into failure blob: %q", out.Failure)
	}
	if !strings.Contains(out.Failure, "[REDACTED]") {
		t.Fatalf("failure blob not redacted: %q", out.Failure)
	}
}

// TestCutoverOrchestratorRequiresMigrationID: a zero/missing migrationID fails
// fast without touching the repo or the lease.
func TestCutoverOrchestratorRequiresMigrationID(t *testing.T) {
	o, _, _, _, _, _ := orchestratorHarness(t)
	req := cutoverReq()
	req.MigrationID = 0
	if _, err := o.Run(context.Background(), req); err == nil {
		t.Fatal("zero migrationID returned no error")
	}
}

// TestCutoverOrchestratorRequiresHolder: an empty holder fails fast.
func TestCutoverOrchestratorRequiresHolder(t *testing.T) {
	o, _, _, _, _, _ := orchestratorHarness(t)
	req := cutoverReq()
	req.Holder = ""
	if _, err := o.Run(context.Background(), req); err == nil {
		t.Fatal("empty holder returned no error")
	}
}
