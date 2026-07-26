package migration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// Pipeline failure handling: marking a run failed or interrupted, and the
// staged rollback that unwinds completed stages in reverse order.
func (p *Pipeline) failPipeline(ctx context.Context, sm *StateMachine, migrationID int, errMsg string, onProgress StepCallback) {
	onProgress(WSMessage{Step: "pipeline", Status: "error", Error: errMsg})
	// P0-2 ForceTransition audit: StateFailed is failure-marking only — it can
	// never reach Committed, so it cannot bypass cutover rules. Force is the
	// acceptable fallback when the from-state was unexpected.
	if err := sm.Transition(StateFailed); err != nil {
		sm.ForceTransition(StateFailed)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateFailed); err != nil {
		log.Printf("warning: failed to persist failed state for migration %d: %v", migrationID, err)
	}
	if err := p.jobRepo.UpdateMigrationStatus(migrationID, StatusFailed, errMsg); err != nil {
		log.Printf("warning: failed to update failed migration status for migration %d: %v", migrationID, err)
	}
}

func (p *Pipeline) interruptPipeline(ctx context.Context, sm *StateMachine, migrationID int, onProgress StepCallback) {
	onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: "Migration interrupted"})
	// P0-2 ForceTransition audit: recovery-only (context cancellation); never
	// reaches Committed, cannot bypass cutover rules.
	if err := sm.Transition(StateInterrupted); err != nil {
		sm.ForceTransition(StateInterrupted)
	}
	if err := p.jobRepo.SetMigrationStateContext(context.Background(), migrationID, StateInterrupted); err != nil {
		log.Printf("warning: failed to persist interrupted state for migration %d: %v", migrationID, err)
	}
	if err := p.jobRepo.UpdateMigrationStatus(migrationID, StatusInterrupted, "interrupted by context cancellation"); err != nil {
		log.Printf("warning: failed to update interrupted migration status for migration %d: %v", migrationID, err)
	}
}

func (p *Pipeline) rollbackPipeline(ctx context.Context, sm *StateMachine, migrationID int, failedStageIndex int, onProgress StepCallback) {
	onProgress(WSMessage{Step: "pipeline", Status: "progress", Value: "Starting automatic rollback..."})

	// Transition to Failed then Rollback. P0-2 ForceTransition audit: entering
	// Failed/Rollback only — never reaches Committed, cannot bypass cutover.
	if err := sm.Transition(StateFailed); err != nil {
		sm.ForceTransition(StateFailed)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateFailed); err != nil {
		log.Printf("warning: failed to persist failed state for migration %d: %v", migrationID, err)
	}

	// P0-2 ForceTransition audit: this only ENTERS rollback — it never reaches
	// Committed, so it cannot bypass cutover rules. The terminal state is
	// decided honestly by rollbackTerminalState below, never forced to RolledBack.
	if err := sm.Transition(StateRollback); err != nil {
		sm.ForceTransition(StateRollback)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateRollback); err != nil {
		log.Printf("warning: failed to persist rollback state for migration %d: %v", migrationID, err)
	}

	// Roll back completed stages in reverse order
	completedStages, err := p.repo.GetCompletedStages(migrationID)
	if err != nil {
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: fmt.Sprintf("failed to load completed stages: %v", err)})
		return
	}

	// Create SSH connection to target if needed
	migration, _ := p.jobRepo.GetMigration(migrationID)
	if migration == nil {
		return
	}
	targetServer, err := p.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: fmt.Sprintf("target server not found: %v", err)})
		return
	}
	targetSSH, err := getSSHClientForServer(migration.TargetID, targetServer, p.srvRepo, p.pool, p.authSvc, p.hosts)
	if err != nil {
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: fmt.Sprintf("SSH connection failed: %v", err)})
		return
	}

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

	// Roll back in reverse order. P0-2: aggregate any step failure so the
	// terminal state is honest — any failed step ⇒ RollbackDegraded (or
	// NeedsManualIntervention for an unsafe topology), never a clean RolledBack.
	var rollbackErr error
	for i := len(completedStages) - 1; i >= 0; i-- {
		stage := completedStages[i]
		for _, handler := range stages {
			if string(handler.Name()) == stage.StageName {
				onProgress(WSMessage{
					Step:   stage.StageName,
					Status: "progress",
					Value:  fmt.Sprintf("Rolling back: %s", stage.StageName),
				})
				if err := handler.Rollback(ctx, pc); err != nil {
					rollbackErr = errors.Join(rollbackErr, err)
					onProgress(WSMessage{
						Step:   stage.StageName,
						Status: "warning",
						Value:  fmt.Sprintf("Rollback warning: %v", err),
					})
				}
				break
			}
		}
	}

	// P0-2: decide the terminal state from the aggregate rollback error — never
	// ForceTransition(RolledBack). nil ⇒ RolledBack; ErrUnsafeTopology ⇒
	// NeedsManualIntervention; anything else ⇒ RollbackDegraded. If the validated
	// Transition to the chosen state fails, fail closed to NeedsManualIntervention.
	terminal := rollbackTerminalState(rollbackErr)
	if err := sm.Transition(terminal); err != nil {
		terminal = StateNeedsManualIntervention
		if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateNeedsManualIntervention); stateErr != nil {
			log.Printf("error: rollback terminal transition failed (%v) and could not fail-closed: %v", err, stateErr)
		}
	} else {
		if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, terminal); err != nil {
			log.Printf("warning: failed to persist %s state for migration %d: %v", terminal, migrationID, err)
		}
	}
	if terminal == StateRolledBack {
		if err := p.jobRepo.SetMigrationRolledBackAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
			log.Printf("warning: failed to persist rolled back timestamp for migration %d: %v", migrationID, err)
		}
	}
	if terminal == StateRollbackDegraded || terminal == StateNeedsManualIntervention {
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Rollback finished in degraded state: %s", terminal)})
	} else {
		onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Rollback complete"})
	}
}
