package migration

import (
	"fmt"
	"sync"
)

// MigrationState is a typed enum representing the lifecycle state of a migration.
// It replaces string-based status constants with explicit, validated transitions.
//
// The full zero-downtime pipeline has 20 states:
//
//   Created → Planning → Discovery → CompatibilityCheck → RiskAssessment →
//   Backup → ProvisionTarget → InstallDependencies → InitialSync →
//   LiveReplication → Verification → PreCutover → TrafficSwitch →
//   PostVerification → Observation → Committed
//
// Failure at any stage: → Failed → Rollback → RolledBack
// Interruption: → Interrupted → Resuming → (resume from last checkpoint)
// User cancel: → Cancelled
type MigrationState int

const (
	// StateCreated is the initial state when a migration is created but not yet started.
	StateCreated MigrationState = iota
	// StatePlanning: collecting data from the source server and building the plan.
	StatePlanning
	// StateDiscovery: running full discovery collectors on source and target.
	StateDiscovery
	// StateCompatibilityCheck: comparing source/target for compatibility blockers.
	StateCompatibilityCheck
	// StateRiskAssessment: evaluating risk score and downtime estimation.
	StateRiskAssessment
	// StateBackup: creating mandatory backups on the target server before any changes.
	StateBackup
	// StateSnapshot: creating a snapshot of the source server state (legacy Engine).
	StateSnapshot
	// StateTransferring: transferring data from source to target (legacy Engine).
	StateTransferring
	// StateApplying: applying collected data to the target server (legacy Engine).
	StateApplying
	// StateVerifying: verifying applied changes on the target server (legacy Engine).
	StateVerifying
	// StateProvisionTarget: installing Docker, compose, nginx, databases, etc. on target.
	StateProvisionTarget
	// StateInstallDependencies: installing packages and configuring target.
	StateInstallDependencies
	// StateInitialSync: initial rsync/data transfer from source to target.
	StateInitialSync
	// StateLiveReplication: setting up DB/Redis replication and monitoring lag.
	StateLiveReplication
	// StateVerification: verifying that all data and services are correctly replicated.
	StateVerification
	// StatePreCutover: final delta sync, queue drain, health verify before traffic switch.
	StatePreCutover
	// StateTrafficSwitch: switching DNS/proxy/CDN traffic from source to target.
	StateTrafficSwitch
	// StatePostVerification: verifying that traffic is flowing to the new target.
	StatePostVerification
	// StateObservation: monitoring the new target for errors after traffic switch.
	StateObservation
	// StateCommitted: all stages completed — migration is done and traffic is on target.
	StateCommitted
	// StateFailed: a stage failed and the migration cannot proceed.
	StateFailed
	// StateRollback: rolling back already-applied changes in LIFO order.
	StateRollback
	// StateRolledBack: rollback completed, target/source restored to pre-migration state.
	StateRolledBack
	// StateRollbackDegraded: rollback completed with some stage failures.
	StateRollbackDegraded
	// StateInterrupted: migration was interrupted (crash, disconnect) and can be resumed.
	StateInterrupted
	// StatePaused: migration was paused by the user (not cancelled). Can be resumed.
	StatePaused
	// StateResuming: an interrupted migration is being resumed.
	StateResuming
	// StateCancelled: migration was cancelled by the user.
	StateCancelled
)

// String returns the human-readable name of the state.
func (s MigrationState) String() string {
	switch s {
	case StateCreated:
		return "created"
	case StatePlanning:
		return "planning"
	case StateDiscovery:
		return "discovery"
	case StateCompatibilityCheck:
		return "compatibility_check"
	case StateRiskAssessment:
		return "risk_assessment"
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
	case StateProvisionTarget:
		return "provision_target"
	case StateInstallDependencies:
		return "install_dependencies"
	case StateInitialSync:
		return "initial_sync"
	case StateLiveReplication:
		return "live_replication"
	case StateVerification:
		return "verification"
	case StatePreCutover:
		return "pre_cutover"
	case StateTrafficSwitch:
		return "traffic_switch"
	case StatePostVerification:
		return "post_verification"
	case StateObservation:
		return "observation"
	case StateCommitted:
		return "committed"
	case StateFailed:
		return "failed"
	case StateRollback:
		return "rollback"
	case StateRolledBack:
		return "rolled_back"
	case StateRollbackDegraded:
		return "rollback_degraded"
	case StateInterrupted:
		return "interrupted"
	case StatePaused:
		return "paused"
	case StateResuming:
		return "resuming"
	case StateCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// IsTerminal returns true if the state is a terminal state (no further transitions).
func (s MigrationState) IsTerminal() bool {
	return s == StateCommitted || s == StateRolledBack || s == StateRollbackDegraded || s == StateCancelled
}

// IsRunning returns true if the migration is actively processing (not terminal, not failed).
func (s MigrationState) IsRunning() bool {
	switch s {
	case StateCreated, StatePlanning, StateDiscovery, StateCompatibilityCheck,
		StateRiskAssessment, StateBackup, StateSnapshot, StateTransferring,
		StateApplying, StateVerifying, StateProvisionTarget, StateInstallDependencies,
		StateInitialSync, StateLiveReplication, StateVerification,
		StatePreCutover, StateTrafficSwitch, StatePostVerification,
		StateObservation, StateRollback, StateResuming:
		return true
	default:
		return false
	}
}

// CanResume returns true if the migration can be resumed from this state.
func (s MigrationState) CanResume() bool {
	return s == StateInterrupted || s == StatePaused
}

// stateString maps MigrationState to the string status stored in the database.
// New states that don't have a legacy equivalent use their own string name.
var stateString = map[MigrationState]string{
	StateCreated:            "planned",              // maps to existing StatusPlanned
	StatePlanning:           "planning",             // new
	StateDiscovery:          "discovery",            // new
	StateCompatibilityCheck: "compatibility_check",  // new
	StateRiskAssessment:     "risk_assessment",      // new
	StateBackup:             "backup",               // new
	StateSnapshot:           "snapshot",             // legacy Engine
	StateTransferring:       "transferring",         // legacy Engine
	StateApplying:           "applying",             // legacy Engine
	StateVerifying:          "verifying",            // legacy Engine
	StateProvisionTarget:   "provision_target",     // new
	StateInstallDependencies: "install_dependencies", // new
	StateInitialSync:        "initial_sync",         // new
	StateLiveReplication:    "live_replication",     // new
	StateVerification:       "verification",         // new
	StatePreCutover:         "pre_cutover",          // new
	StateTrafficSwitch:      "traffic_switch",       // new
	StatePostVerification:   "post_verification",    // new
	StateObservation:        "observation",           // new
	StateCommitted:          "completed",            // maps to existing StatusCompleted
	StateFailed:             "failed",               // maps to existing StatusFailed
	StateRollback:           "rolling_back",         // maps to existing StatusRollingBack
	StateRolledBack:         "rolled_back",          // maps to existing StatusRolledBack
	StateRollbackDegraded:   "rollback_degraded",    // partial rollback failure
	// StateRestored is an alias — same iota value, already covered by StateRolledBack
	StateInterrupted:        "interrupted",          // maps to existing StatusInterrupted
	StatePaused:             "paused",               // real pause (not cancel)
	StateResuming:           "resuming",             // maps to existing StatusResuming
	StateCancelled:          "cancelled",            // new
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
//
// The pipeline flows linearly through the 14 stages. Each stage can
// transition to Failed or Interrupted. Failed can transition to Rollback.
// Rollback transitions to RolledBack. Interrupted can transition to Resuming.
// Resuming can transition to any stage that was in progress when interrupted.
var transitionTable = map[MigrationState][]MigrationState{
	StateCreated:             {StatePlanning},
	StatePlanning:            {StateDiscovery, StateBackup, StateFailed, StateInterrupted, StatePaused},
	StateDiscovery:           {StateCompatibilityCheck, StateFailed, StateInterrupted, StatePaused},
	StateCompatibilityCheck:  {StateRiskAssessment, StateFailed, StateInterrupted, StatePaused},
	StateRiskAssessment:      {StateBackup, StateFailed, StateInterrupted, StatePaused},
	StateBackup:              {StateProvisionTarget, StateSnapshot, StateFailed, StateInterrupted, StatePaused},
	StateSnapshot:            {StateTransferring, StateFailed, StateInterrupted, StatePaused},
	StateTransferring:        {StateApplying, StateCommitted, StateFailed, StateInterrupted, StatePaused},
	StateApplying:            {StateVerifying, StateCommitted, StateFailed, StateInterrupted, StatePaused, StateRollback},
	StateVerifying:           {StatePreCutover, StateCommitted, StateApplying, StateFailed, StateInterrupted, StatePaused, StateRollback},
	StateProvisionTarget:     {StateInstallDependencies, StateFailed, StateInterrupted, StatePaused},
	StateInstallDependencies: {StateInitialSync, StateFailed, StateInterrupted, StatePaused},
	StateInitialSync:         {StateLiveReplication, StateFailed, StateInterrupted, StatePaused},
	StateLiveReplication:     {StateVerification, StateFailed, StateInterrupted, StatePaused},
	StateVerification:        {StatePreCutover, StateFailed, StateInterrupted, StatePaused},
	StatePreCutover:          {StateTrafficSwitch, StateFailed, StateInterrupted, StatePaused},
	StateTrafficSwitch:       {StatePostVerification, StateFailed, StateInterrupted, StatePaused, StateRollback},
	StatePostVerification:    {StateObservation, StateFailed, StateInterrupted, StatePaused, StateRollback},
	StateObservation:         {StateCommitted, StateFailed, StateInterrupted, StatePaused, StateRollback},
	StateCommitted:           {}, // terminal
	StateFailed:              {StateRollback, StateInterrupted},
	StateRollback:            {StateRolledBack, StateRollbackDegraded, StateFailed},
	StateRolledBack:          {}, // terminal
	StateRollbackDegraded:    {}, // terminal
	StateInterrupted:         {StateResuming, StateFailed, StateCancelled},
	StatePaused:              {StateResuming, StateFailed, StateCancelled},
	StateResuming: {
		StatePlanning, StateDiscovery, StateCompatibilityCheck, StateRiskAssessment,
		StateBackup, StateSnapshot, StateTransferring, StateApplying, StateVerifying,
		StateProvisionTarget, StateInstallDependencies,
		StateInitialSync, StateLiveReplication, StateVerification,
		StatePreCutover, StateTrafficSwitch, StatePostVerification,
		StateObservation, StateFailed, StateInterrupted,
	},
	StateCancelled: {}, // terminal
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
