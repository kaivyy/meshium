package migration

import (
	"testing"
)

func TestMigrationStateString(t *testing.T) {
	tests := []struct {
		state MigrationState
		want  string
	}{
		{StateCreated, "created"},
		{StatePlanning, "planning"},
		{StateBackup, "backup"},
		{StateSnapshot, "snapshot"},
		{StateTransferring, "transferring"},
		{StateApplying, "applying"},
		{StateVerifying, "verifying"},
		{StateCommitted, "committed"},
		{StateFailed, "failed"},
		{StateRollback, "rollback"},
		{StateRestored, "restored"},
		{StateInterrupted, "interrupted"},
		{StateResuming, "resuming"},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateStringRoundTrip(t *testing.T) {
	for state, str := range stateString {
		t.Run(str, func(t *testing.T) {
			got, err := StateFromString(str)
			if err != nil {
				t.Fatalf("StateFromString(%q): %v", str, err)
			}
			if got != state {
				t.Errorf("StateFromString(%q) = %v, want %v", str, got, state)
			}
			if got.StateString() != str {
				t.Errorf("StateString() = %q, want %q", got.StateString(), str)
			}
		})
	}
}

func TestStateFromStringUnknown(t *testing.T) {
	_, err := StateFromString("bogus")
	if err == nil {
		t.Fatal("expected error for unknown state string")
	}
}

func TestStateIsTerminal(t *testing.T) {
	terminalStates := []MigrationState{StateCommitted, StateRestored}
	nonTerminalStates := []MigrationState{
		StateCreated, StatePlanning, StateBackup, StateSnapshot,
		StateTransferring, StateApplying, StateVerifying,
		StateFailed, StateRollback, StateInterrupted, StateResuming,
	}

	for _, s := range terminalStates {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range nonTerminalStates {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestStateIsRunning(t *testing.T) {
	runningStates := []MigrationState{
		StateCreated, StatePlanning, StateBackup, StateSnapshot,
		StateTransferring, StateApplying, StateVerifying,
		StateRollback, StateResuming,
	}
	notRunningStates := []MigrationState{
		StateCommitted, StateFailed, StateRestored, StateInterrupted,
	}

	for _, s := range runningStates {
		if !s.IsRunning() {
			t.Errorf("%s should be running", s)
		}
	}
	for _, s := range notRunningStates {
		if s.IsRunning() {
			t.Errorf("%s should not be running", s)
		}
	}
}

func TestStateCanResume(t *testing.T) {
	if !StateInterrupted.CanResume() {
		t.Error("StateInterrupted should be resumable")
	}
	if StateCommitted.CanResume() {
		t.Error("StateCommitted should not be resumable")
	}
	if StateFailed.CanResume() {
		t.Error("StateFailed should not be resumable")
	}
}

func TestIsValidTransition(t *testing.T) {
	valid := []struct {
		from, to MigrationState
	}{
		{StateCreated, StatePlanning},
		{StatePlanning, StateBackup},
		{StatePlanning, StateFailed},
		{StateBackup, StateSnapshot},
		{StateBackup, StateFailed},
		{StateSnapshot, StateTransferring},
		{StateTransferring, StateApplying},
		{StateApplying, StateVerifying},
		{StateApplying, StateRollback},
		{StateVerifying, StateCommitted},
		{StateVerifying, StateRollback},
		{StateFailed, StateRollback},
		{StateRollback, StateRestored},
		{StateInterrupted, StateResuming},
		{StateResuming, StateTransferring},
		{StateResuming, StateApplying},
		{StateResuming, StateVerifying},
	}

	for _, tt := range valid {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			if !IsValidTransition(tt.from, tt.to) {
				t.Errorf("transition %s → %s should be valid", tt.from, tt.to)
			}
		})
	}
}

func TestIsInvalidTransition(t *testing.T) {
	invalid := []struct {
		from, to MigrationState
	}{
		{StateCreated, StateApplying},      // skip phases
		{StateCreated, StateCommitted},     // skip everything
		{StateCommitted, StatePlanning},     // terminal
		{StateRestored, StateApplying},     // terminal
		{StateRollback, StateApplying},     // can't go back to applying during rollback
		{StateBackup, StateApplying},        // skip snapshot
		{StatePlanning, StateApplying},       // skip backup
	}

	for _, tt := range invalid {
		t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
			if IsValidTransition(tt.from, tt.to) {
				t.Errorf("transition %s → %s should be invalid", tt.from, tt.to)
			}
		})
	}
}

func TestStateMachineTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)

	if err := sm.Transition(StatePlanning); err != nil {
		t.Fatalf("Created→Planning: %v", err)
	}
	if sm.State() != StatePlanning {
		t.Fatalf("state = %s, want planning", sm.State())
	}

	if err := sm.Transition(StateBackup); err != nil {
		t.Fatalf("Planning→Backup: %v", err)
	}

	// Invalid transition
	if err := sm.Transition(StateCommitted); err == nil {
		t.Fatal("Backup→Committed should be invalid")
	}

	// State should not have changed after invalid transition
	if sm.State() != StateBackup {
		t.Fatalf("state = %s, want backup after invalid transition", sm.State())
	}
}

func TestStateMachineConcurrent(t *testing.T) {
	sm := NewStateMachine(StateCreated)

	// Multiple goroutines trying to transition — only one should succeed
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			errs <- sm.Transition(StatePlanning)
		}()
	}

	successCount := 0
	for i := 0; i < 10; i++ {
		err := <-errs
		if err == nil {
			successCount++
		}
	}

	if successCount != 1 {
		t.Errorf("expected 1 successful transition, got %d", successCount)
	}
	if sm.State() != StatePlanning {
		t.Errorf("state = %s, want planning", sm.State())
	}
}

func TestStateMachineForceTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.ForceTransition(StateRollback)
	if sm.State() != StateRollback {
		t.Fatalf("ForceTransition failed: state = %s", sm.State())
	}
}

func TestStateMachineIsTerminal(t *testing.T) {
	sm := NewStateMachine(StateCommitted)
	if !sm.IsTerminal() {
		t.Error("Committed should be terminal")
	}

	sm = NewStateMachine(StateApplying)
	if sm.IsTerminal() {
		t.Error("Applying should not be terminal")
	}
}
