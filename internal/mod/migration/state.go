package migration

import (
	"fmt"
	"sync"
)

// MigrationState is a typed enum representing the lifecycle state of a migration.
// It replaces string-based status constants with explicit, validated transitions.
type MigrationState int

const (
	// StateCreated is the initial state when a migration job is created but not yet started.
	StateCreated MigrationState = iota
	// StatePlanning: collecting data from the source server and building the plan.
	StatePlanning
	// StateBackup: creating mandatory backups on the target server before any changes.
	StateBackup
	// StateSnapshot: recording pre-migration snapshot for verification.
	StateSnapshot
	// StateTransferring: transferring data from source to target.
	StateTransferring
	// StateApplying: applying changes to the target server.
	StateApplying
	// StateVerifying: verifying that applied changes are correct.
	StateVerifying
	// StateCommitted: all steps completed and verified — migration is done.
	StateCommitted
	// StateFailed: a step failed and the migration cannot proceed.
	StateFailed
	// StateRollback: rolling back already-applied steps in LIFO order.
	StateRollback
	// StateRestored: rollback completed, target is restored to pre-migration state.
	StateRestored
	// StateInterrupted: migration was interrupted (crash, disconnect) and can be resumed.
	StateInterrupted
	// StateResuming: an interrupted migration is being resumed.
	StateResuming
)

// String returns the human-readable name of the state.
func (s MigrationState) String() string {
	switch s {
	case StateCreated:
		return "created"
	case StatePlanning:
		return "planning"
	case StateBackup:
		return "backup"
	case StateSnapshot:
		return "snapshot"
	case StateTransferring:
		return "transferring"
	case StateApplying:
		return "applying"
	case StateVerifying:
		return "verifying"
	case StateCommitted:
		return "committed"
	case StateFailed:
		return "failed"
	case StateRollback:
		return "rollback"
	case StateRestored:
		return "restored"
	case StateInterrupted:
		return "interrupted"
	case StateResuming:
		return "resuming"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// IsTerminal returns true if the state is a terminal state (no further transitions).
func (s MigrationState) IsTerminal() bool {
	return s == StateCommitted || s == StateRestored
}

// IsRunning returns true if the migration is actively processing (not terminal, not failed).
func (s MigrationState) IsRunning() bool {
	switch s {
	case StateCreated, StatePlanning, StateBackup, StateSnapshot,
		StateTransferring, StateApplying, StateVerifying,
		StateRollback, StateResuming:
		return true
	default:
		return false
	}
}

// CanResume returns true if the migration can be resumed from this state.
func (s MigrationState) CanResume() bool {
	return s == StateInterrupted
}

// stateString maps MigrationState to the existing string status constants
// stored in the database. This maintains backward compatibility with the
// existing migrations table and all code that reads/writes string statuses.
var stateString = map[MigrationState]string{
	StateCreated:     "planned",    // maps to existing StatusPlanned
	StatePlanning:    "planning",   // new
	StateBackup:      "backup",     // new
	StateSnapshot:    "snapshot",   // new
	StateTransferring: "transferring", // new
	StateApplying:    "applying",   // new
	StateVerifying:   "verifying",  // new
	StateCommitted:   "completed",  // maps to existing StatusCompleted
	StateFailed:      "failed",     // maps to existing StatusFailed
	StateRollback:    "rolling_back", // maps to existing StatusRollingBack
	StateRestored:    "rolled_back",  // maps to existing StatusRolledBack
	StateInterrupted: "interrupted",   // maps to existing StatusInterrupted
	StateResuming:    "resuming",      // maps to existing StatusResuming
}

// stringState is the reverse mapping, populated in init().
var stringState map[string]MigrationState

func init() {
	stringState = make(map[string]MigrationState, len(stateString))
	for state, str := range stateString {
		stringState[str] = state
	}
}

// StateFromString converts a database string status to a MigrationState.
// Returns an error if the string is not a recognized state.
func StateFromString(s string) (MigrationState, error) {
	state, ok := stringState[s]
	if !ok {
		return StateCreated, fmt.Errorf("unknown migration state: %q", s)
	}
	return state, nil
}

// StateString returns the database string representation of the state.
func (s MigrationState) StateString() string {
	if str, ok := stateString[s]; ok {
		return str
	}
	return "unknown"
}

// transitionTable defines the valid state transitions.
// A transition from → to is valid only if to is in the set of allowed
// successors for from.
var transitionTable = map[MigrationState][]MigrationState{
	StateCreated:      {StatePlanning},
	StatePlanning:     {StateBackup, StateFailed, StateInterrupted},
	StateBackup:       {StateSnapshot, StateFailed, StateInterrupted},
	StateSnapshot:     {StateTransferring, StateFailed, StateInterrupted},
	StateTransferring: {StateApplying, StateCommitted, StateFailed, StateInterrupted},
	StateApplying:     {StateVerifying, StateCommitted, StateFailed, StateInterrupted, StateRollback},
	StateVerifying:    {StateCommitted, StateApplying, StateFailed, StateInterrupted, StateRollback},
	StateCommitted:    {}, // terminal
	StateFailed:       {StateRollback, StateInterrupted},
	StateRollback:     {StateRestored, StateFailed},
	StateRestored:     {}, // terminal
	StateInterrupted:  {StateResuming, StateFailed},
	StateResuming:     {StateBackup, StateTransferring, StateApplying, StateVerifying, StateFailed, StateInterrupted},
}

// IsValidTransition returns true if transitioning from → to is allowed.
func IsValidTransition(from, to MigrationState) bool {
	allowed, ok := transitionTable[from]
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if candidate == to {
			return true
		}
	}
	return false
}

// StateMachine provides thread-safe state transitions with validation.
// It is used by the Engine to ensure that migration state changes are
// explicit, validated, and never skip required phases.
type StateMachine struct {
	mu    sync.Mutex
	state MigrationState
}

// NewStateMachine creates a StateMachine starting at the given state.
func NewStateMachine(initial MigrationState) *StateMachine {
	return &StateMachine{state: initial}
}

// State returns the current state. Thread-safe.
func (sm *StateMachine) State() MigrationState {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state
}

// Transition attempts to move from the current state to the target state.
// Returns an error if the transition is not valid. Thread-safe.
func (sm *StateMachine) Transition(to MigrationState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	from := sm.state
	if !IsValidTransition(from, to) {
		return fmt.Errorf("invalid state transition: %s → %s", from, to)
	}
	sm.state = to
	return nil
}

// MustTransition panics if the transition is invalid. For use in tests.
func (sm *StateMachine) MustTransition(to MigrationState) {
	if err := sm.Transition(to); err != nil {
		panic(err)
	}
}

// ForceTransition sets the state without validation. Use with caution —
// only for recovery scenarios where the state must be corrected.
func (sm *StateMachine) ForceTransition(to MigrationState) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state = to
}

// IsTerminal returns true if the current state is terminal.
func (sm *StateMachine) IsTerminal() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state.IsTerminal()
}

// IsRunning returns true if the current state indicates active processing.
func (sm *StateMachine) IsRunning() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state.IsRunning()
}

// CanResume returns true if the current state allows resumption.
func (sm *StateMachine) CanResume() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state.CanResume()
}
