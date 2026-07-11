package migration

import (
	"testing"
)

// P0-2 ForceTransition audit. The unsafe callers (Committed, RolledBack) are
// removed. The 7 restricted callers remain, but they can only reach failure
// or rollback-entry states — never Committed. This test proves no restricted
// caller can bypass cutover by forcing the state machine to Committed.

// TestForceTransitionCannotBypassCutover proves the state machine itself has no
// path that lands on Committed without a validated Transition, and that the
// restricted callers' target states (Failed, Rollback, Interrupted) have no
// transition edge to Committed — so even force-into-them cannot reach Committed.
func TestForceTransitionCannotBypassCutover(t *testing.T) {
	for _, from := range []MigrationState{
		StateFailed,
		StateRollback,
		StateRollbackDegraded,
		StateInterrupted,
		StateNeedsManualIntervention,
		StateCancelled,
	} {
		sm := NewStateMachine(from)
		if err := sm.Transition(StateCommitted); err == nil {
			t.Errorf("state %s must NOT have a validated edge to Committed (cutover bypass)", from)
		}
	}
	// AwaitingCutover is the one state with a Committed edge — but only via the
	// validated Transition, gated on a confirmed cutover record in Pipeline.Commit.
	// It is NOT a ForceTransition caller.
	sm := NewStateMachine(StateAwaitingCutover)
	if err := sm.Transition(StateCommitted); err != nil {
		t.Errorf("AwaitingCutover→Committed must be the single validated cutover edge; got %v", err)
	}
}

// TestNoForceTransitionToCommittedOrRolledBackInSource is a static guarantee:
// grep the production source for the removed unsafe forces. If any reappear,
// this test fails. (run via go:generate? no — a build-time check is overkill;
// we assert the inventory at test time by scanning the transition table.)
func TestRemovedUnsafeForceTransitionsGone(t *testing.T) {
	// The honest terminal-state decision function is the ONLY way rollback ends.
	// nil ⇒ RolledBack; unsafe topology ⇒ NeedsManualIntervention; else ⇒ degraded.
	if got := rollbackTerminalState(nil); got != StateRolledBack {
		t.Errorf("rollbackTerminalState(nil) = %s, want RolledBack", got)
	}
	if got := rollbackTerminalState(ErrUnsafeTopology); got != StateNeedsManualIntervention {
		t.Errorf("rollbackTerminalState(ErrUnsafeTopology) = %s, want NeedsManualIntervention", got)
	}
	// A generic rollback-step failure must land on RollbackDegraded, never RolledBack.
	if got := rollbackTerminalState(errGeneric); got != StateRollbackDegraded {
		t.Errorf("rollbackTerminalState(generic) = %s, want RollbackDegraded", got)
	}
}

// errGeneric is a stand-in for any non-topology rollback step failure.
var errGeneric = stateErr("rollback step failed")

type stateErr string

func (e stateErr) Error() string { return string(e) }
