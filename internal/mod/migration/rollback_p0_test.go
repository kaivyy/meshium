package migration

import (
	"errors"
	"fmt"
	"testing"
)

// P0-2 honest terminal state: RolledBack is reachable from Rollback only when
// every step succeeded. The state machine enforces the transition; the pipeline
// no longer ForceTransition(StateRolledBack) when Transition fails.
func TestRolledBackOnlyFromRollback(t *testing.T) {
	sm := NewStateMachine(StateRollback)
	if err := sm.Transition(StateRolledBack); err != nil {
		t.Fatalf("Rollback→RolledBack must be valid: %v", err)
	}
	if sm.State() != StateRolledBack {
		t.Fatalf("expected RolledBack, got %s", sm.State())
	}
}

// A failed rollback step must never end as RolledBack. The terminal-state
// decision lives in rollbackTerminalState (consulted by Pipeline.Rollback):
// ErrUnsafeTopology wrapping ⇒ NeedsManualIntervention, else RollbackDegraded.
func TestPartialRollbackTerminalDecision(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want MigrationState
	}{
		{"partial-step-failure", errors.New("volumes: boom"), StateRollbackDegraded},
		{"topology-ambiguous", fmt.Errorf("%w: both writable", ErrUnsafeTopology), StateNeedsManualIntervention},
		{"mixed-wrapped", fmt.Errorf("rollback failed: db: %w", ErrUnsafeTopology), StateNeedsManualIntervention},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rollbackTerminalState(tc.err)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
			if got == StateRolledBack {
				t.Fatal("partial rollback must never resolve to RolledBack")
			}
		})
	}
}

func TestFullRollbackTerminalDecision(t *testing.T) {
	if rollbackTerminalState(nil) != StateRolledBack {
		t.Fatal("nil error ⇒ RolledBack")
	}
}

// Topology-ambiguous rollback resolves to NeedsManualIntervention, never
// RolledBack — the regression the guardrail requires proving.
func TestTopologyAmbiguousRollbackEndsManualIntervention(t *testing.T) {
	got := rollbackTerminalState(fmt.Errorf("%w: unknown role", ErrUnsafeTopology))
	if got != StateNeedsManualIntervention {
		t.Fatalf("ambiguous topology must end NeedsManualIntervention, got %s", got)
	}
}
