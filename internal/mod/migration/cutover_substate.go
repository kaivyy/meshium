package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// CutoverSubState is a Phase 2A fenced-cutover step. It is NOT a top-level
// MigrationState (those are immutable Phase 1). Sub-states drive the
// trafficSwitchStage's internal step machine and are persisted on the stage
// checkpoint_data + the fence lease.
type CutoverSubState string

const (
	SubPreflight       CutoverSubState = "Preflight"
	SubSeed            CutoverSubState = "Seed"
	SubReplicating     CutoverSubState = "Replicating"
	SubVerifying       CutoverSubState = "Verifying"
	SubAwaitingCutover CutoverSubState = "AwaitingCutover"
	SubFencingSource   CutoverSubState = "FencingSource"
	SubCatchingUp      CutoverSubState = "CatchingUp"
	SubVerifyingTarget CutoverSubState = "VerifyingTarget"
	SubSwitching       CutoverSubState = "SwitchingTraffic"
	SubPromoting       CutoverSubState = "PromotingTarget"
	SubObserving       CutoverSubState = "Observing"
	SubCompleted       CutoverSubState = "Completed"
)

// cutoverTransitions is the legal Phase 2A sub-state edge set (doc §5).
// Re-entry after restart reconciles to the same sub-state (self-loops are
// allowed — idempotent re-run). Anything not listed → illegal →
// NeedsManualIntervention.
var cutoverTransitions = map[CutoverSubState]map[CutoverSubState]bool{
	SubPreflight:       {SubSeed: true, SubPreflight: true},
	SubSeed:            {SubReplicating: true, SubSeed: true},
	SubReplicating:     {SubVerifying: true, SubReplicating: true},
	SubVerifying:       {SubAwaitingCutover: true, SubVerifying: true},
	SubAwaitingCutover: {SubFencingSource: true, SubAwaitingCutover: true},
	SubFencingSource:   {SubCatchingUp: true, SubFencingSource: true},
	SubCatchingUp:      {SubVerifyingTarget: true, SubCatchingUp: true},
	SubVerifyingTarget: {SubSwitching: true, SubVerifyingTarget: true},
	SubSwitching:       {SubPromoting: true, SubSwitching: true},
	SubPromoting:       {SubObserving: true, SubPromoting: true},
	SubObserving:       {SubCompleted: true, SubObserving: true},
	SubCompleted:       {},
}

// validCutoverTransition reports whether from→to is a legal edge. A self-edge
// is legal (idempotent re-entry / restart reconciliation at the same step).
func validCutoverTransition(from, to CutoverSubState) bool {
	if from == to {
		return true
	}
	dests, ok := cutoverTransitions[from]
	if !ok {
		return false
	}
	return dests[to]
}

// cutoverCheckpoint is persisted on the traffic_switch stage's checkpoint_data
// (and mirrored on the fence lease's state). Restart reconciliation loads it
// to re-enter the cutover at the right step. Each step is idempotent: a step
// whose Result is non-empty is considered already-done and skipped.
type cutoverCheckpoint struct {
	SubState CutoverSubState      `json:"subState"`
	Token    int                  `json:"token"`
	Holder   string               `json:"holder"`
	Steps    map[CutoverSubState]string `json:"steps,omitempty"` // subState → result blob (non-empty = done)
}

// cutoverStepResult is the per-step result payload persisted before advancing.
// Step-specific details (LSN, marker, role) live in Data; SubState names the
// completed step.
type cutoverStepResult struct {
	SubState CutoverSubState `json:"subState"`
	Data     string          `json:"data,omitempty"`
}

// ErrCutoverIllegalTransition is returned when a sub-state advance is not a
// legal Phase 2A edge. Callers fail closed to NeedsManualIntervention — an
// out-of-order cutover is unsafe to continue automatically.
var ErrCutoverIllegalTransition = errors.New("illegal cutover sub-state transition")

// ErrCutoverReentryDone is returned by MarkStep when the step already has a
// persisted result — a signal to the orchestrator that re-entry should SKIP
// the step (idempotent), not re-run it. Not a hard error.
var ErrCutoverReentryDone = errors.New("cutover step already completed (skip on re-entry)")

// cutoverMachine is the persisted sub-state machine for one migration's
// fenced cutover. It reads/writes via the stage checkpoint (repo) and checks
// the fence lease for staleness. Fail-closed throughout: a stale lease or an
// illegal transition yields an error the orchestrator maps to
// NeedsManualIntervention.
type cutoverMachine struct {
	repo       PipelineRepo
	authority  *FencingAuthority
	migrationID int
	stageName  PipelineStageName
}

// newCutoverMachine builds a machine for the migration's traffic_switch stage.
func newCutoverMachine(repo PipelineRepo, authority *FencingAuthority, migrationID int) *cutoverMachine {
	return &cutoverMachine{repo: repo, authority: authority, migrationID: migrationID, stageName: StageTrafficSwitch}
}

// Load reads the persisted sub-state. On restart:
//   - no checkpoint → the cutover has not begun (returns an empty checkpoint).
//   - checkpoint present, lease stale → ErrFenceLeaseStale (→ NeedsManualIntervention).
//   - checkpoint present, lease missing → ErrFenceLeaseNotHeld (→ NeedsManualIntervention).
func (m *cutoverMachine) Load(ctx context.Context) (*cutoverCheckpoint, error) {
	stage, err := m.repo.GetStageByName(m.migrationID, string(m.stageName))
	if err != nil {
		return nil, err
	}
	if stage == nil {
		return &cutoverCheckpoint{}, nil
	}
	if stage.CheckpointData == "" {
		return &cutoverCheckpoint{}, nil
	}
	var cp cutoverCheckpoint
	if err := json.Unmarshal([]byte(stage.CheckpointData), &cp); err != nil {
		return nil, fmt.Errorf("cutover checkpoint unmarshal: %w", err)
	}
	// Verify the lease is live and matches the checkpoint's holder/token.
	// This is the restart-reconciliation safety check: a stale lease means the
	// cutover was interrupted and must not resume blindly.
	if cp.Holder == "" || cp.Token == 0 {
		// Pre-cutover (no lease yet). Legal — AwaitingCutover may have no lease.
		return &cp, nil
	}
	if _, err := m.authority.AssertHolds(ctx, m.migrationID, cp.Holder, cp.Token, ""); err != nil {
		return nil, err
	}
	return &cp, nil
}

// Save persists the sub-state checkpoint + mirrors state onto the fence lease
// (persist-before-advance). Both writes must succeed before returning.
func (m *cutoverMachine) Save(ctx context.Context, cp *cutoverCheckpoint, lease *FenceLease) error {
	if cp == nil {
		return errors.New("nil checkpoint")
	}
	b, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	stage, err := m.repo.GetStageByName(m.migrationID, string(m.stageName))
	if err != nil {
		return err
	}
	if stage == nil {
		return errors.New("cutover stage not found")
	}
	// Mirror the lease identity onto the checkpoint so a restart can validate
	// the lease even at the very first persisted sub-state (before any MarkStep).
	if lease != nil {
		cp.Token = lease.FenceToken
		cp.Holder = lease.Holder
		b, _ = json.Marshal(cp)
	}
	if err := m.repo.UpdateStageCheckpoint(ctx, stage.ID, string(b)); err != nil {
		return err
	}
	if lease != nil {
		if err := m.authority.SetState(ctx, lease, string(cp.SubState)); err != nil {
			return err
		}
	}
	return nil
}

// MarkStep records a step's result in the checkpoint and advances the sub-state
// to `to`, persisting before return. If the step already has a result,
// it returns ErrCutoverReentryDone (caller skips, does NOT fail). The
// `from`→`to` edge is validated; illegal → ErrCutoverIllegalTransition.
func (m *cutoverMachine) MarkStep(ctx context.Context, cp *cutoverCheckpoint, lease *FenceLease, from, to CutoverSubState, resultData string) (*cutoverCheckpoint, error) {
	if cp == nil {
		return nil, errors.New("nil checkpoint")
	}
	// Validate the edge using the CURRENT persisted sub-state, which must equal
	// `from` (the orchestrator's view). A mismatch means state diverged under us.
	if cp.SubState != from {
		return nil, fmt.Errorf("%w: cp=%q from=%q", ErrCutoverIllegalTransition, cp.SubState, from)
	}
	if !validCutoverTransition(from, to) {
		return nil, fmt.Errorf("%w: %s→%s", ErrCutoverIllegalTransition, from, to)
	}
	if cp.Steps == nil {
		cp.Steps = make(map[CutoverSubState]string)
	}
	if existing := cp.Steps[from]; existing != "" {
		// Already done on a prior run — idempotent skip signal.
		return cp, ErrCutoverReentryDone
	}
	cp.Steps[from] = resultData
	cp.SubState = to
	if lease != nil {
		cp.Token = lease.FenceToken
		cp.Holder = lease.Holder
	}
	if err := m.Save(ctx, cp, lease); err != nil {
		return nil, err
	}
	return cp, nil
}

// StepDone reports whether `step` already has a persisted result (idempotent
// skip on re-entry). Used by the orchestrator before running a step.
func (cp *cutoverCheckpoint) StepDone(step CutoverSubState) bool {
	if cp == nil || cp.Steps == nil {
		return false
	}
	return cp.Steps[step] != ""
}
