package migration

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Pipeline lifecycle operations: the operator-facing verbs that move a
// migration between states (pause, cutover, commit, retry, resume, rollback,
// cancel) plus interrupted-run recovery. The stage machinery itself lives in
// pipeline.go and the stages_*.go files.
func (p *Pipeline) Pause(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	if currentState == StatePaused {
		return nil // Already paused
	}

	sm := NewStateMachine(currentState)
	if err := sm.Transition(StatePaused); err != nil {
		return fmt.Errorf("migration cannot be paused from state %s: %w", currentState, err)
	}

	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StatePaused); err != nil {
		return err
	}

	_, _ = p.repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID:   migrationID,
		EventType:     "migration_paused",
		PreviousState: currentState.String(),
		NewState:      StatePaused.String(),
		Actor:         "user",
	})

	onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: "Migration paused — checkpoint saved"})
	return nil
}

// Cutover confirms the traffic switch and advances the migration into observation.
func (p *Pipeline) Cutover(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	switch currentState {
	case StateObservation, StateCommitted:
		return nil
	case StateVerification, StatePreCutover, StateTrafficSwitch, StatePostVerification:
		// valid cutover entry points
	default:
		return fmt.Errorf("migration cannot be cut over from state %s", currentState)
	}

	cutoverRecord := CutoverRecord{
		MigrationID:     migrationID,
		CutoverType:     "manual",
		PreviousState:   currentState.String(),
		TrafficSwitched: true,
		StartedAt:       time.Now().Format(time.RFC3339),
	}
	cutoverID, err := p.repo.CreateCutoverRecord(ctx, cutoverRecord)
	if err != nil {
		return fmt.Errorf("failed to record cutover: %w", err)
	}

	sm := NewStateMachine(currentState)
	transitionTo := func(next MigrationState) error {
		if err := sm.Transition(next); err != nil {
			return fmt.Errorf("failed to transition to %s: %w", next, err)
		}
		if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, next); err != nil {
			return err
		}
		currentState = next
		return nil
	}

	if currentState == StateVerification {
		if err := transitionTo(StatePreCutover); err != nil {
			return err
		}
	}
	if currentState == StatePreCutover {
		if err := transitionTo(StateTrafficSwitch); err != nil {
			return err
		}
	}
	if currentState == StateTrafficSwitch {
		if err := transitionTo(StatePostVerification); err != nil {
			return err
		}
	}
	if currentState == StatePostVerification {
		if err := transitionTo(StateObservation); err != nil {
			return err
		}
	}

	if err := p.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), ""); err != nil {
		log.Printf("warning: failed to update cutover record for migration %d: %v", migrationID, err)
	}

	_, _ = p.repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID:   migrationID,
		EventType:     "migration_cutover_confirmed",
		PreviousState: cutoverRecord.PreviousState,
		NewState:      currentState.String(),
		Actor:         "user",
	})

	onProgress(WSMessage{Step: "cutover", Status: "success", Value: "Cutover confirmed"})
	return nil
}

// Commit finalizes a successful migration.
func (p *Pipeline) Commit(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	if currentState == StateCommitted {
		return nil
	}

	// P0-2 cutover: an AwaitingCutover migration may commit only after the
	// operator has confirmed traffic moved to the target (switch_state flipped
	// off manual_required). While it remains manual_required, reject with a
	// structured error and do NOT transition. Never ForceTransition(Committed).
	if currentState == StateAwaitingCutover {
		cfg, err := p.repo.GetTrafficSwitchConfig(migrationID)
		if err != nil {
			return fmt.Errorf("load traffic switch config: %w", err)
		}
		if cfg == nil || cfg.SwitchState == trafficSwitchManualState {
			return ErrCutoverNotConfirmed
		}
		// switch_state confirmed by the operator — validated transition only.
		sm := NewStateMachine(currentState)
		if err := sm.Transition(StateCommitted); err != nil {
			// Transition failed → fail closed to NeedsManualIntervention, never force.
			if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateNeedsManualIntervention); stateErr != nil {
				return fmt.Errorf("transition to committed failed (%v) and could not fail-closed: %w", err, stateErr)
			}
			return fmt.Errorf("commit transition failed: %w", err)
		}
		if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateCommitted); err != nil {
			return err
		}
		if err := p.jobRepo.SetMigrationCompletedAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
			return err
		}
		_, _ = p.repo.CreateAuditEntry(ctx, AuditEntry{
			MigrationID:   migrationID,
			EventType:     "migration_committed",
			PreviousState: currentState.String(),
			NewState:      StateCommitted.String(),
			Actor:         "user",
		})
		onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Migration committed"})
		return nil
	}

	if currentState != StateObservation {
		return fmt.Errorf("migration cannot be committed from state %s", currentState)
	}

	sm := NewStateMachine(currentState)
	if err := sm.Transition(StateCommitted); err != nil {
		return fmt.Errorf("failed to transition to committed: %w", err)
	}

	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateCommitted); err != nil {
		return err
	}
	if err := p.jobRepo.SetMigrationCompletedAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}

	_, _ = p.repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID:   migrationID,
		EventType:     "migration_committed",
		PreviousState: currentState.String(),
		NewState:      StateCommitted.String(),
		Actor:         "user",
	})

	onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Migration committed"})
	return nil
}

// Retry restarts a failed or interrupted migration from the last checkpoint.
func (p *Pipeline) Retry(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	switch currentState {
	case StateInterrupted:
		return p.Resume(ctx, migrationID, onProgress)
	case StateFailed:
		sm := NewStateMachine(currentState)
		if err := sm.Transition(StateInterrupted); err != nil {
			return fmt.Errorf("failed to prepare retry from state %s: %w", currentState, err)
		}
		if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateInterrupted); err != nil {
			return err
		}
		_, _ = p.repo.CreateAuditEntry(ctx, AuditEntry{
			MigrationID:   migrationID,
			EventType:     "migration_retried",
			PreviousState: currentState.String(),
			NewState:      StateInterrupted.String(),
			Actor:         "user",
		})
		return p.Resume(ctx, migrationID, onProgress)
	case StateResuming:
		return p.Resume(ctx, migrationID, onProgress)
	default:
		return fmt.Errorf("migration cannot be retried from state %s", currentState)
	}
}

// Resume resumes an interrupted migration from the last checkpoint.
func (p *Pipeline) Resume(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Load the migration
	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	// Check current state
	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	if !currentState.CanResume() {
		return fmt.Errorf("migration cannot be resumed from state %s", currentState)
	}

	// Transition to Resuming
	sm := NewStateMachine(currentState)
	if err := sm.Transition(StateResuming); err != nil {
		return fmt.Errorf("failed to transition to resuming: %w", err)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateResuming); err != nil {
		log.Printf("warning: failed to persist resuming state for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist resuming state: %v", err)})
	}
	onProgress(WSMessage{Step: "pipeline", Status: "progress", Value: "Resuming migration..."})

	// Delegate to Execute which will skip completed stages
	return p.Execute(ctx, migrationID, onProgress)
}

// Rollback triggers a manual rollback of a migration.
func (p *Pipeline) Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Load the migration
	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	// Get current state
	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}
	if currentState == StateRollback || currentState == StateRolledBack {
		return nil
	}

	sm := NewStateMachine(currentState)
	if err := sm.Transition(StateRollback); err != nil {
		return fmt.Errorf("migration cannot be rolled back from state %s: %w", currentState, err)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateRollback); err != nil {
		return err
	}
	onProgress(WSMessage{Step: "rollback", Status: "progress", Value: "Starting rollback..."})

	// Get completed stages to know what to roll back
	completedStages, err := p.repo.GetCompletedStages(migrationID)
	if err != nil {
		return fmt.Errorf("failed to load completed stages: %w", err)
	}

	// Create SSH connection to target
	targetServer, err := p.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return fmt.Errorf("target server not found: %w", err)
	}
	targetSSH, err := getSSHClientForServer(migration.TargetID, targetServer, p.srvRepo, p.pool, p.authSvc, p.hosts)
	if err != nil {
		return fmt.Errorf("target SSH connection failed: %w", err)
	}

	// Build pipeline context
	config, _ := p.repo.GetMigrationConfig(migrationID)
	if config == nil {
		config = DefaultMigrationConfig()
	}
	pc := &PipelineContext{
		MigrationID:  migrationID,
		Migration:    migration,
		Config:       config,
		TargetSSH:    targetSSH,
		Repo:         p.repo,
		JobRepo:      p.jobRepo,
		Registry:     p.registry,
		OnProgress:   onProgress,
		StateMachine: sm,
	}

	p.mu.Lock()
	stages := make([]PipelineStageHandler, len(p.stages))
	copy(stages, p.stages)
	p.mu.Unlock()

	rollbackErrors := make([]string, 0)

	// Roll back stages in reverse order
	for i := len(completedStages) - 1; i >= 0; i-- {
		stage := completedStages[i]
		// Find the stage handler
		for _, handler := range stages {
			if string(handler.Name()) == stage.StageName {
				onProgress(WSMessage{
					Step:   stage.StageName,
					Status: "progress",
					Value:  fmt.Sprintf("Rolling back stage: %s", stage.StageName),
				})
				if err := handler.Rollback(ctx, pc); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", stage.StageName, err))
					onProgress(WSMessage{
						Step:   stage.StageName,
						Status: "error",
						Error:  fmt.Sprintf("Rollback failed for %s: %v", stage.StageName, err),
					})
				}
				break
			}
		}
	}

	if len(rollbackErrors) > 0 {
		// P0-2 honest terminal state: any failed rollback step → RollbackDegraded
		// (never RolledBack). If a rollback step reported ErrUnsafeTopology, the
		// topology was ambiguous → NeedsManualIntervention instead.
		err := fmt.Errorf("rollback failed: %s", strings.Join(rollbackErrors, "; "))
		targetState := rollbackTerminalState(err)
		if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, targetState); stateErr != nil {
			onProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Failed to persist rollback failed state: %v", stateErr)})
		}
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: err.Error()})
		return err
	}

	// P0-2: honest terminal state. RolledBack is reached ONLY when every
	// rollback step succeeded. If the validated transition fails (unexpected
	// from-state), fail closed to NeedsManualIntervention — never ForceTransition.
	if err := sm.Transition(StateRolledBack); err != nil {
		if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateNeedsManualIntervention); stateErr != nil {
			log.Printf("warning: failed to persist needs_manual_intervention for migration %d: %v", migrationID, stateErr)
		}
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: fmt.Sprintf("rollback could not be finalized safely: %v", err)})
		return fmt.Errorf("rollback could not be finalized safely: %w", err)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateRolledBack); err != nil {
		log.Printf("warning: failed to persist rolled back state for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Failed to persist rolled back state: %v", err)})
	}
	if err := p.jobRepo.SetMigrationRolledBackAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("warning: failed to persist rolled back timestamp for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Failed to persist rollback timestamp: %v", err)})
	}

	onProgress(WSMessage{Step: "rollback", Status: "complete", Value: "Rollback complete"})
	return nil
}

// Cancel cancels a migration.
func (p *Pipeline) Cancel(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Get current state
	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		return fmt.Errorf("failed to get migration state: %w", err)
	}

	if currentState == StateCancelled {
		return nil
	}

	sm := NewStateMachine(currentState)
	if err := sm.Transition(StateCancelled); err != nil {
		return fmt.Errorf("migration cannot be cancelled from state %s: %w", currentState, err)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateCancelled); err != nil {
		return err
	}

	// Audit log
	if _, err := p.repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID:   migrationID,
		EventType:     "migration_cancelled",
		PreviousState: currentState.String(),
		NewState:      StateCancelled.String(),
		Actor:         "user",
	}); err != nil {
		log.Printf("warning: failed to create cancel audit entry for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist cancel audit entry: %v", err)})
	}

	onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Migration cancelled"})
	return nil
}

// RecoverInterrupted detects migrations left in an in-progress state by a
// crashed process and marks them as interrupted so they can be resumed.
// Detection uses isRunningStatus so it catches both the legacy literal
// "running" and any state-machine string (e.g. "initial_sync") persisted into
// the status column by SetMigrationState; markInterrupted resets the typed
// state column too so the migration stays resumable.
func (p *Pipeline) RecoverInterrupted() ([]int, error) {
	migrations, err := p.jobRepo.ListMigrations()
	if err != nil {
		return nil, fmt.Errorf("failed to list migrations: %w", err)
	}

	var recovered []int
	for _, m := range migrations {
		// A literal "planned" / StateCreated row is the initial pre-collection
		// state, not an in-flight pipeline run, so it is never a crashed run.
		// (isRunningStatus reports it as running because StateCreated.IsRunning()
		// is true; that would wrongly flip every freshly created migration to
		// interrupted at startup, and would also clobber the clearer
		// collection-interrupted message the executor writes for such rows.)
		if m.Status != StatusPlanned && isRunningStatus(m.Status) {
			if err := markInterrupted(p.jobRepo, m.ID, "process may have crashed"); err != nil {
				return recovered, fmt.Errorf("failed to update migration %d: %w", m.ID, err)
			}
			recovered = append(recovered, m.ID)
		}
	}
	return recovered, nil
}
