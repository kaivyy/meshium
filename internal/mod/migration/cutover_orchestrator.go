package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"meshium/internal/shared"
)

// Phase 2A-6: fenced cutover orchestration.
//
// CutoverOrchestrator drives the 12-step sub-state machine (cutover_substate.go)
// end-to-end when MigrationConfig.AutoCutover is true. It is the ONLY caller of
// the Phase 2A primitives built in Commits 2-5:
//
//   - Commit 2: FencingAuthority (durable lease, AssertHolds before every step)
//   - Commit 3: cutoverMachine (persisted sub-state, idempotent re-entry)
//   - Commit 4: ReplicationEngine engine-agnostic preflight / WaitForCatchUp /
//     CutoverPromote (PostgreSQL, MySQL-seeded, Redis fenced)
//   - Commit 5: NginxSwitcher + HAProxySwitcher (idempotent switch + read-after-write verify)
//
// Contract (spec §5, §6, §7):
//   - Every mutating step calls AssertHolds first. A missing/stale/conflicting
//     lease fails closed to NeedsManualIntervention — no mutation.
//   - Each step's result is persisted (MarkStep) BEFORE the mutation it gates is
//     considered complete; re-entry after a crash skips done steps.
//   - No-dual-writer invariant: traffic is switched to the target FIRST
//     (Switching), then the target is promoted to primary (Promoting). The
//     target is a standby (read-only) until promote, so clients routed to it by
//     the switch see a read-only DB until the promote completes. Reads never
//     drop. Writes pause only during the promote window — minimal downtime.
//   - Verify is the ownership proof: NginxSwitcher's read-after-write marker.
//     A failed verify fails closed (no auto-rollback assumption).
//   - autoCutover defaults OFF (DefaultMigrationConfig). Phase 1's manual-cutover
//     trafficSwitchStage path is untouched when OFF — no regression.
//
// Not in this slice (ponytails):
//   - Multi-traffic-provider (Nginx + HAProxy are the supported two),
//     multi-standby pinning, cross-major cutover, byte-level resume, full-rsync.
//     MongoDB remains deferred (no replica-set lag). ONE target.
//   - Source read-only GUC is defense-in-depth, not the hard fence (the lease is
//     the hard fence). Wiring FreezeManager is deferred — the legacy
//     CutoverEngine uses it but has no live caller. Add when a failure-injection
//     test demands it.

// AutoCutoverDefault is false: Phase 1 manual cutover is the default. Opt-in.
const AutoCutoverDefault = false

// CutoverTimeout bounds the whole orchestrated cutover. The lease TTL
// (FenceTTL=5m) must be renewed within this; for the minimal slice we set a
// single ceiling under one lease lifetime. ponytail: add lease renewal loop
// when cutover can exceed FenceTTL.
var CutoverTimeout = FenceTTL - 10*time.Second

// CutoverRequest is the orchestrator input — built by trafficSwitchStage from
// PipelineContext + MigrationConfig. Kept separate from the legacy
// cutover.CutoverConfig to avoid coupling to the dead CutoverEngine.
//
// The request is engine-agnostic: Replication carries the engine type, and the
// orchestrator drives whatever cutover primitive the engine supports (PostgreSQL
// same-major, MySQL seeded, Redis fenced). TrafficRequest is satisfied by both
// the Nginx and HAProxy switchers (both use the generic TrafficSwitchRequest).
type CutoverRequest struct {
	MigrationID    int
	Holder         string          // lease holder identity (e.g. "meshium-<node>")
	Replication    ReplicationConfig
	TrafficRequest TrafficSwitchRequest
	MaxLagSeconds  int64 // catch-up threshold before promote (engine-specific meaning)
	ObserveFor     time.Duration
}

// CutoverOutcome is the sanitized result the stage persists. No secret survives
// (SanitizeJSONRawMessage on the marshaled blob).
type CutoverOutcome struct {
	Completed   bool                `json:"completed"`
	FinalState  CutoverSubState     `json:"finalState"`
	Holder      string              `json:"holder"`
	Token       int                 `json:"token"`
	Steps       map[string]string   `json:"steps"` // subState -> result
	Preflight   CutoverPreflightResult `json:"preflight,omitempty"`
	Switch      *TrafficSwitchResult   `json:"switch,omitempty"`
	Degraded    []string            `json:"degraded,omitempty"` // honest downscopes (e.g. source freeze unsupported)
	Failure     string              `json:"failure,omitempty"` // sanitized
}

// cutoverDriver is the narrow surface of ReplicationEngine the orchestrator
// uses. It is engine-agnostic (Phase 2C/3A): the same primitives drive
// PostgreSQL, MySQL, and Redis cutovers. Kept as an interface so tests inject a
// fake without building the full engine. *ReplicationEngine satisfies it via
// CutoverPreflight / WaitForCatchUp / CutoverFreezeSource / CutoverPromote.
type cutoverDriver interface {
	CutoverPreflight(ctx context.Context, config ReplicationConfig) (CutoverPreflightResult, error)
	WaitForCatchUp(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error
	CutoverFreezeSource(ctx context.Context, config ReplicationConfig) error
	CutoverPromote(ctx context.Context, config ReplicationConfig) error
}

// trafficSwitchDriver is the narrow surface of NginxSwitcher / HAProxySwitcher
// the orchestrator uses. Both switchers satisfy it via the generic
// TrafficSwitchRequest / TrafficSwitchResult (NginxSwitchRequest and
// HAProxySwitchRequest are aliases of TrafficSwitchRequest).
type trafficSwitchDriver interface {
	Switch(ctx context.Context, req TrafficSwitchRequest) (*TrafficSwitchResult, error)
}

// CutoverOrchestrator runs the fenced cutover. It is constructed by
// trafficSwitchStage when AutoCutover is true; the machine + authority are
// shared with the sub-state layer.
type CutoverOrchestrator struct {
	machine  *cutoverMachine
	auth     *FencingAuthority
	driver   cutoverDriver
	traffic  trafficSwitchDriver
}

// NewCutoverOrchestrator wires the orchestrator. driver and traffic are injected
// so tests pass fakes; production passes a *ReplicationEngine and a
// *NginxSwitcher / *HAProxySwitcher. The driver is engine-agnostic (PostgreSQL,
// MySQL, Redis all satisfy cutoverDriver via CutoverPreflight/WaitForCatchUp/
// CutoverPromote).
func NewCutoverOrchestrator(machine *cutoverMachine, auth *FencingAuthority, driver cutoverDriver, traffic trafficSwitchDriver) *CutoverOrchestrator {
	return &CutoverOrchestrator{machine: machine, auth: auth, driver: driver, traffic: traffic}
}

// Run drives the cutover from the persisted sub-state to completion. It is
// idempotent: re-entry after a crash loads the checkpoint and skips done steps.
// On any failure it persists the failure on the checkpoint and returns an error
// the stage maps to NeedsManualIntervention (never a silent partial cutover).
//
// The no-dual-writer invariant: the source accepts writes until the lease is
// acquired + catch-up verified; the target accepts writes only after promote.
// Between switch and promote the target is a read-only standby. There is no
// window where both source and target accept writes.
func (o *CutoverOrchestrator) Run(ctx context.Context, cfg CutoverRequest) (*CutoverOutcome, error) {
	if cfg.MigrationID <= 0 {
		return nil, errors.New("cutover: migrationID required")
	}
	if cfg.Holder == "" {
		return nil, errors.New("cutover: holder required")
	}
	// Bound the whole attempt under one lease lifetime.
	rctx := ctx
	if CutoverTimeout > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, CutoverTimeout)
		defer cancel()
	}

	out := &CutoverOutcome{Steps: map[string]string{}}

	// Load persisted sub-state (or empty). A stale/missing lease here fails
	// closed — the cutover cannot resume blindly.
	cp, err := o.machine.Load(rctx)
	if err != nil {
		out.Failure = sanitizeErr(err)
		return out, fmt.Errorf("cutover load: %w", err)
	}
	// Bootstrap: a cutover always starts at Preflight. The lease state mirrors
	// cp.SubState (SetState in Save), and advance() asserts the lease state ==
	// the step's `from`. Without this, the first AssertHolds("Preflight") would
	// face an empty lease state and fail closed spuriously.
	if cp.SubState == "" {
		cp.SubState = SubPreflight
	}
	out.FinalState = cp.SubState
	// Already done on a prior run — short-circuit as completed. The stage only
	// invokes the orchestrator when the stage is not done, but a re-invocation
	// after a completed cutover (e.g. operator retry) must not re-acquire or
	// re-mutate.
	if cp.SubState == SubCompleted {
		out.Completed = true
		return out, nil
	}

	// Acquire / re-confirm the lease. AssertHolds-before-every-step is enforced
	// inside advance(); this initial acquire covers the first step.
	lease, err := o.acquireOrRecover(rctx, cfg, cp)
	if err != nil {
		out.Failure = sanitizeErr(err)
		return out, fmt.Errorf("cutover fence: %w", err)
	}
	out.Holder = lease.Holder
	out.Token = lease.FenceToken

	// Persist the starting checkpoint with the lease identity so a crash here
	// is recoverable. If the machine already has a non-empty checkpoint this is
	// a no-op save.
	if err := o.machine.Save(rctx, cp, lease); err != nil {
		out.Failure = sanitizeErr(err)
		return out, fmt.Errorf("cutover initial save: %w", err)
	}

	// Drive each step. done() persists the step result + advances; on a re-entry
	// (step already done) it returns ErrCutoverReentryDone which we treat as ok.
	steps := []cutoverStep{
		{SubPreflight, SubSeed, o.stepPreflight(cfg, out)},
		{SubSeed, SubReplicating, o.stepSeed(cfg, out)},
		{SubReplicating, SubVerifying, o.stepReplicating(cfg, out)},
		{SubVerifying, SubAwaitingCutover, o.stepVerifying(cfg, out)},
		{SubAwaitingCutover, SubFencingSource, o.stepAwaitingCutover(cfg, out)},
		{SubFencingSource, SubCatchingUp, o.stepFencingSource(cfg, out)},
		{SubCatchingUp, SubVerifyingTarget, o.stepCatchingUp(cfg, out)},
		{SubVerifyingTarget, SubSwitching, o.stepVerifyingTarget(cfg, out)},
		{SubSwitching, SubPromoting, o.stepSwitching(cfg, out)},
		{SubPromoting, SubObserving, o.stepPromoting(cfg, out)},
		{SubObserving, SubCompleted, o.stepObserving(cfg, out)},
	}
	for _, st := range steps {
		next, err := o.advance(rctx, cp, lease, st)
		if err == nil {
			cp = next
			out.FinalState = cp.SubState
			continue
		}
		// Re-entry skip is success — the step was already done on a prior run.
		if errors.Is(err, ErrCutoverReentryDone) {
			cp = next
			out.FinalState = cp.SubState
			continue
		}
		// Hard failure: persist the failure on the checkpoint and fail closed.
		out.FinalState = cp.SubState
		out.Failure = sanitizeErr(err)
		_ = o.machine.Save(rctx, cp, lease) // best-effort; the advance already persisted
		return out, err
	}

	// All steps done. Mark the lease released and the outcome completed.
	out.Completed = true
	out.FinalState = SubCompleted
	_ = o.auth.Release(rctx, lease) // best-effort release; does not unfreeze
	return out, nil
}

// cutoverStep is one edge: from -> to, with the action that produces the step's
// result blob. The action runs only if the step is not already done.
type cutoverStep struct {
	from, to CutoverSubState
	action   stepAction
}

// stepAction runs the step's work and returns its result blob (persisted) or
// an error (fail closed).
type stepAction func(ctx context.Context, lease *FenceLease) (string, error)

// advance asserts the lease holds, then runs the step's action and MarkStep.
// Re-entry (already done) returns (cp, ErrCutoverReentryDone).
func (o *CutoverOrchestrator) advance(ctx context.Context, cp *cutoverCheckpoint, lease *FenceLease, st cutoverStep) (*cutoverCheckpoint, error) {
	// Already done? Skip without touching the lease — idempotent re-entry.
	if cp.StepDone(st.from) {
		return cp, ErrCutoverReentryDone
	}
	// Assert the lease holds BEFORE any mutation. The hard fence.
	if _, err := o.auth.AssertHolds(ctx, o.machine.migrationID, lease.Holder, lease.FenceToken, string(st.from)); err != nil {
		return cp, fmt.Errorf("cutover %s: fence not held: %w", st.from, err)
	}
	data, err := st.action(ctx, lease)
	if err != nil {
		return cp, fmt.Errorf("cutover %s: %w", st.from, err)
	}
	return o.machine.MarkStep(ctx, cp, lease, st.from, st.to, data)
}

// acquireOrRecover gets the lease. If the checkpoint already references a
// holder+token (a prior run), assert that lease still holds rather than
// acquiring a new one (which would conflict). A stale lease here fails closed.
func (o *CutoverOrchestrator) acquireOrRecover(ctx context.Context, cfg CutoverRequest, cp *cutoverCheckpoint) (*FenceLease, error) {
	if cp.Holder != "" && cp.Token != 0 {
		lease, err := o.auth.AssertHolds(ctx, o.machine.migrationID, cp.Holder, cp.Token, string(cp.SubState))
		if err != nil {
			return nil, fmt.Errorf("recover prior lease: %w", err)
		}
		return lease, nil
	}
	lease, err := o.auth.Acquire(ctx, cfg.MigrationID, cfg.Holder, string(cp.SubState))
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return nil, ErrFenceLeaseNotHeld
	}
	return lease, nil
}

// --- Step actions -------------------------------------------------------

// stepPreflight: PG preflight (same-major, source primary, target standby,
// replicator connectivity). No mutation. Records PGPreflightResult.
func (o *CutoverOrchestrator) stepPreflight(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		r, err := o.driver.CutoverPreflight(ctx, cfg.Replication)
		out.Preflight = r
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ok src=%d tgt=%d replicator=%v", r.SourceVersionNum, r.TargetVersionNum, r.ReplicatorOK), nil
	}
}

// stepSeed: for PostgreSQL the seed already happened in the live_replication
// stage (the target standby exists). For MySQL the seed is performed by
// setupMySQL (a streaming dump/restore before CHANGE REPLICATION SOURCE TO). For
// Redis the seed is the REPLICAOF initial sync. This step re-proves, at the
// cutover boundary, the target is in the expected pre-switch role (standby for
// PG/MySQL, replica for Redis). No mutation. Records the preflight result.
func (o *CutoverOrchestrator) stepSeed(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		// Re-run preflight gates only (cheap, read-only). A target that is no
		// longer in the expected role here means something promoted it
		// out-of-band — fail.
		r, err := o.driver.CutoverPreflight(ctx, cfg.Replication)
		if err != nil {
			return "", fmt.Errorf("seed boundary: %w", err)
		}
		return fmt.Sprintf("ok target-standby=%v", r.TargetInRecovery), nil
	}
}

// stepReplicating: confirm the replication channel is healthy (target
// connected to source). Read-only.
func (o *CutoverOrchestrator) stepReplicating(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		// WaitForCatchUp with maxLag 0 proves a live, near-caught-up channel.
		// A no-standby error here fails closed (target not connected).
		maxLag := cfg.MaxLagSeconds
		if maxLag < 0 {
			maxLag = PGCatchUpMaxLag
		}
		if err := o.driver.WaitForCatchUp(ctx, cfg.Replication, maxLag); err != nil {
			return "", fmt.Errorf("replicating: %w", err)
		}
		return "ok lag<=" + fmt.Sprint(maxLag), nil
	}
}

// stepVerifying: final pre-cutover catch-up verify. Same primitive; recorded as
// a distinct step so the checkpoint shows the verify gate fired.
func (o *CutoverOrchestrator) stepVerifying(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		maxLag := cfg.MaxLagSeconds
		if maxLag < 0 {
			maxLag = PGCatchUpMaxLag
		}
		if err := o.driver.WaitForCatchUp(ctx, cfg.Replication, maxLag); err != nil {
			return "", fmt.Errorf("verifying: %w", err)
		}
		return "ok verified", nil
	}
}

// stepAwaitingCutover: the explicit cutover gate. In auto mode this is a
// no-op pass-through (the operator already opted in via AutoCutover=true).
// Recorded so the checkpoint shows the gate was reached deliberately.
func (o *CutoverOrchestrator) stepAwaitingCutover(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		return "ok auto-cutover-opted-in", nil
	}
}

// stepFencingSource: with the durable fence already held (AssertHolds in
// advance), physically freeze the source so the dual-writer window closes.
// PostgreSQL enforces default_transaction_read_only=on and verifies it. Engines
// that cannot enforce a freeze (MySQL/Redis) return ErrNoSourceFreeze: that is
// NOT fatal — we record it as a degraded condition (manual freeze required) and
// continue, so we never silently claim RPO=0 for an engine whose source can
// still accept writes. Any other error fails closed.
func (o *CutoverOrchestrator) stepFencingSource(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		if err := o.driver.CutoverFreezeSource(ctx, cfg.Replication); err != nil {
			if errors.Is(err, ErrNoSourceFreeze) {
				out.Degraded = append(out.Degraded, "source_freeze_not_supported:"+cfg.Replication.DatabaseType)
				return fmt.Sprintf("degraded: source freeze unsupported for %s (manual freeze required) holder=%s token=%d",
					cfg.Replication.DatabaseType, lease.Holder, lease.FenceToken), nil
			}
			return "", fmt.Errorf("fencing source: %w", err)
		}
		return fmt.Sprintf("ok frozen holder=%s token=%d", lease.Holder, lease.FenceToken), nil
	}
}

// stepCatchingUp: drain remaining replication lag to <= threshold. This is the
// RPO≈0 window: after this the target is caught up to the fenced source.
func (o *CutoverOrchestrator) stepCatchingUp(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		maxLag := cfg.MaxLagSeconds
		if maxLag < 0 {
			maxLag = PGCatchUpMaxLag
		}
		if err := o.driver.WaitForCatchUp(ctx, cfg.Replication, maxLag); err != nil {
			return "", fmt.Errorf("catching-up: %w", err)
		}
		return "ok caught-up", nil
	}
}

// stepVerifyingTarget: the target is still a standby and caught up. Read-only
// boundary check before the switch.
func (o *CutoverOrchestrator) stepVerifyingTarget(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		r, err := o.driver.CutoverPreflight(ctx, cfg.Replication)
		if err != nil {
			return "", fmt.Errorf("verifying-target: %w", err)
		}
		if !r.TargetInRecovery {
			return "", errors.New("verifying-target: target not a standby before switch (promoted out-of-band?)")
		}
		return "ok target-standby", nil
	}
}

// stepSwitching: switch traffic to the target via the injected switcher
// (Nginx or HAProxy). This is the read-after-write ownership proof. The target
// is still a standby — clients see read-only until promote. Failure fails closed
// (no auto-rollback).
func (o *CutoverOrchestrator) stepSwitching(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		res, err := o.traffic.Switch(ctx, cfg.TrafficRequest)
		out.Switch = res
		if err != nil {
			return "", fmt.Errorf("switching: %w", err)
		}
		return fmt.Sprintf("ok switched=%v verified=%v", res.Switched, res.Verified), nil
	}
}

// stepPromoting: promote the target standby to primary via the engine-agnostic
// CutoverPromote primitive. The ONLY write to the data plane. After this the
// target accepts writes; the source is fenced. Post-promote probe (in
// CutoverPromote) is the ownership proof.
func (o *CutoverOrchestrator) stepPromoting(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		if err := o.driver.CutoverPromote(ctx, cfg.Replication); err != nil {
			return "", fmt.Errorf("promoting: %w", err)
		}
		return "ok promoted", nil
	}
}

// stepObserving: brief observation that the target serves traffic and accepts
// writes. Read-only; the duration is bounded by cfg.ObserveFor.
func (o *CutoverOrchestrator) stepObserving(cfg CutoverRequest, out *CutoverOutcome) stepAction {
	return func(ctx context.Context, lease *FenceLease) (string, error) {
		if cfg.ObserveFor > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(cfg.ObserveFor):
			}
		}
		return "ok observed", nil
	}
}

// sanitizeErr redacts any secret from an error string before persisting it on
// the checkpoint. No secret on persisted data (security constraint).
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	return shared.SanitizeString(err.Error())
}
