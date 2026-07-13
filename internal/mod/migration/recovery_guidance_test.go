package migration

import (
	"testing"
)

// TestRecoveryGuidanceFailClosed asserts the recovery guidance never promises
// an automatic forward when the state is fail-closed (needs_manual_intervention,
// awaiting_cutover), and that it does for resumable states.
func TestRecoveryGuidanceFailClosed(t *testing.T) {
	if g := recoveryGuidance(StateNeedsManualIntervention); g.SafeToResume {
		t.Fatal("needs_manual_intervention must not be safe-to-resume")
	}
	if g := recoveryGuidance(StateAwaitingCutover); g.SafeToResume {
		t.Fatal("awaiting_cutover must not be safe-to-resume")
	}
	if g := recoveryGuidance(StateFailed); !g.SafeToResume {
		t.Fatal("failed should be safe-to-resume (retry)")
	}
	if g := recoveryGuidance(StateInterrupted); !g.SafeToResume {
		t.Fatal("interrupted should be safe-to-resume")
	}
	if g := recoveryGuidance(StatePaused); !g.SafeToResume {
		t.Fatal("paused should be safe-to-resume")
	}
}

// TestRecoveryGuidanceAlwaysAnswers proves every defined state yields guidance
// (the API must never 500 for an unknown-in-practice state).
func TestRecoveryGuidanceAlwaysAnswers(t *testing.T) {
	for s := StateCreated; s <= StateNeedsManualIntervention; s++ {
		g := recoveryGuidance(s)
		if g.State == "" || g.Summary == "" {
			t.Fatalf("state %d yielded empty guidance", s)
		}
		if len(g.OperatorActions) == 0 {
			t.Fatalf("state %d yielded no operator actions", s)
		}
	}
}
