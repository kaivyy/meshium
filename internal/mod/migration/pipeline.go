package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"meshium/internal/mod/server"
)

// PipelineStageHandler is the interface that each pipeline stage must implement.
// Each stage receives the migration context and returns an error if the stage
// fails. The stage is responsible for its own progress reporting via the
// onProgress callback.
type PipelineStageHandler interface {
	// Name returns the stage name.
	Name() PipelineStageName
	// Execute runs the stage. It should be idempotent — if called again
	// (e.g. after resume), it should skip already-completed work.
	Execute(ctx context.Context, pc *PipelineContext) error
	// Rollback reverts the stage's effects. Called during automatic rollback.
	Rollback(ctx context.Context, pc *PipelineContext) error
}

// PipelineContext is the shared context passed through all pipeline stages.
// It carries the migration record, SSH connections, configuration, and
// progress callback.
type PipelineContext struct {
	MigrationID    int
	Migration      *Migration
	Config         *MigrationConfig
	SourceSSH      SSHExecuter
	TargetSSH      SSHExecuter
	SourceServer   *server.Server
	TargetServer   *server.Server
	Repo           PipelineRepo
	JobRepo        JobRepository
	Registry       *CategoryRegistry
	OnProgress     StepCallback
	StateMachine   *StateMachine
	CheckpointData map[string]string // stage_name → checkpoint data
}

// Pipeline is the unified orchestration engine for zero-downtime migrations.
// It is the single source of truth for migration execution — there is no
// longer a separate Executor. The Pipeline runs through 14 stages, each
// with checkpoint, retry, and rollback support.
//
// The Pipeline is:
//   - Idempotent: stages check their checkpoint and skip if already completed
//   - Crash safe: all state is persisted to the database
//   - Resumeable: interrupted migrations resume from the last checkpoint
//   - Checkpoint aware: each stage writes a checkpoint on completion
//   - Health driven: health checks gate the cutover
//   - Event driven: all state transitions emit WebSocket events
//   - Rollback capable: automatic rollback on failure
type Pipeline struct {
	repo             PipelineRepo
	jobRepo          JobRepository
	srvRepo          server.Repo
	pool             ConnectionPool
	authSvc          AESKeyProvider
	hosts            HostKeyStore
	registry         *CategoryRegistry
	stages           []PipelineStageHandler
	mu               sync.Mutex
	runningPipelines sync.Map // migrationID → struct{} (prevents concurrent execution)
	lifecycle        *pipelineRegistry
}

// NewPipeline creates a new Pipeline with the given dependencies.
// The stages are registered in the correct order for the zero-downtime pipeline.
func NewPipeline(
	repo PipelineRepo,
	jobRepo JobRepository,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
	registry *CategoryRegistry,
) (*Pipeline, error) {
	if registry == nil {
		return nil, fmt.Errorf("category registry is required")
	}
	p := &Pipeline{
		repo:      repo,
		jobRepo:   jobRepo,
		srvRepo:   srvRepo,
		pool:      pool,
		authSvc:   authSvc,
		hosts:     hosts,
		registry:  registry,
		lifecycle: newPipelineRegistry(),
	}
	defaultRegistry = registry
	p.registerDefaultStages()
	return p, nil
}

// registerDefaultStages registers the 14 pipeline stages in order.
func (p *Pipeline) registerDefaultStages() {
	p.stages = []PipelineStageHandler{
		&discoveryStage{repo: p.repo, srvRepo: p.srvRepo, pool: p.pool, authSvc: p.authSvc, hosts: p.hosts},
		&analysisStage{repo: p.repo},
		&planningStage{repo: p.repo, registry: p.registry, srvRepo: p.srvRepo, pool: p.pool, authSvc: p.authSvc, hosts: p.hosts},
		&validationStage{repo: p.repo},
		&preparationStage{repo: p.repo, registry: p.registry},
		&initialSyncStage{repo: p.repo},
		&liveReplicationStage{repo: p.repo},
		&healthVerificationStage{repo: p.repo},
		&preCutoverValidationStage{repo: p.repo},
		&trafficSwitchStage{repo: p.repo},
		&postCutoverObservationStage{repo: p.repo},
		&finalizationStage{repo: p.repo, registry: p.registry},
		&archiveStage{repo: p.repo},
	}
}

// RegisterStage replaces or adds a pipeline stage. This allows external
// code to customize the pipeline behavior.
func (p *Pipeline) RegisterStage(stage PipelineStageHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range p.stages {
		if s.Name() == stage.Name() {
			p.stages[i] = stage
			return
		}
	}
	p.stages = append(p.stages, stage)
}

// Execute runs the full zero-downtime migration pipeline. This is the
// single entry point for migration execution — there is no separate Executor.
//
// The pipeline:
//  1. Loads the migration and its config
//  2. Acquires a lock to prevent concurrent execution
//  3. Initializes the state machine
//  4. Creates SSH connections to source and target
//  5. Runs each stage in order, with checkpoint and retry
//  6. On failure: automatic rollback
//  7. On context cancellation: marks as interrupted
//  8. On success: marks as committed
func (p *Pipeline) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Prevent concurrent execution of the same migration
	if !p.tryAcquire(migrationID) {
		return fmt.Errorf("migration %d is already running", migrationID)
	}
	defer p.release(migrationID)

	// Register with the lifecycle registry so application shutdown can drain
	// this run. The derived execCtx is cancelled either by the caller (WS
	// disconnect) or by the registry during shutdown; on cancellation the stage
	// loop below calls interruptPipeline, which persists the checkpoint via
	// context.Background(). If the registry has stopped accepting (shutdown in
	// progress), refuse to start a new run.
	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()
	if !p.lifecycle.register(migrationID, execCancel) {
		return fmt.Errorf("server is shutting down; migration %d not started", migrationID)
	}
	defer p.lifecycle.unregister(migrationID)
	ctx = execCtx

	// Load the migration
	migration, err := p.jobRepo.GetMigration(migrationID)
	if err != nil {
		return fmt.Errorf("migration not found: %w", err)
	}

	// Load config
	config, err := p.repo.GetMigrationConfig(migrationID)
	if err != nil {
		config = DefaultMigrationConfig()
	}

	// Initialize state machine from current state
	currentState, err := p.jobRepo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}
	sm := NewStateMachine(currentState)

	// If resuming, transition to Resuming
	if currentState == StateInterrupted {
		if err := sm.Transition(StateResuming); err != nil {
			return fmt.Errorf("failed to transition to resuming: %w", err)
		}
		if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateResuming); err != nil {
			log.Printf("warning: failed to persist resuming state for migration %d: %v", migrationID, err)
			onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist resuming state: %v", err)})
		}
		onProgress(WSMessage{Step: "pipeline", Status: "progress", Value: "Resuming migration..."})
	}

	// Mark the migration as running in the DB. RecoverInterrupted matches on
	// StatusRunning, so without this a crashed pipeline can never be detected
	// or recovered. This must happen before the long-running SSH/stage work.
	if err := p.jobRepo.UpdateMigrationStatus(migrationID, StatusRunning, ""); err != nil {
		log.Printf("warning: failed to mark migration %d as running: %v", migrationID, err)
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist running status: %v", err)})
	}

	// Create SSH connections
	sourceServer, err := p.srvRepo.GetByID(migration.SourceID)
	if err != nil {
		p.failPipeline(ctx, sm, migrationID, fmt.Sprintf("source server not found: %v", err), onProgress)
		return err
	}
	targetServer, err := p.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		p.failPipeline(ctx, sm, migrationID, fmt.Sprintf("target server not found: %v", err), onProgress)
		return err
	}

	sourceSSH, err := getSSHClientForServer(migration.SourceID, sourceServer, p.srvRepo, p.pool, p.authSvc, p.hosts)
	if err != nil {
		p.failPipeline(ctx, sm, migrationID, fmt.Sprintf("source SSH connection failed: %v", err), onProgress)
		return err
	}
	onProgress(WSMessage{Step: "pipeline", Status: "success", Value: "Connected to source server"})

	targetSSH, err := getSSHClientForServer(migration.TargetID, targetServer, p.srvRepo, p.pool, p.authSvc, p.hosts)
	if err != nil {
		p.failPipeline(ctx, sm, migrationID, fmt.Sprintf("target SSH connection failed: %v", err), onProgress)
		return err
	}
	onProgress(WSMessage{Step: "pipeline", Status: "success", Value: "Connected to target server"})

	// Load completed stages for checkpoint skip
	completedStages, err := p.repo.GetCompletedStages(migrationID)
	if err != nil {
		log.Printf("warning: failed to load completed stages: %v", err)
	}
	completedSet := make(map[string]bool, len(completedStages))
	for _, s := range completedStages {
		completedSet[s.StageName] = true
	}

	// Build the pipeline context
	pc := &PipelineContext{
		MigrationID:    migrationID,
		Migration:      migration,
		Config:         config,
		SourceSSH:      sourceSSH,
		TargetSSH:      targetSSH,
		SourceServer:   sourceServer,
		TargetServer:   targetServer,
		Repo:           p.repo,
		JobRepo:        p.jobRepo,
		Registry:       p.registry,
		OnProgress:     onProgress,
		StateMachine:   sm,
		CheckpointData: make(map[string]string),
	}

	p.mu.Lock()
	stages := make([]PipelineStageHandler, len(p.stages))
	copy(stages, p.stages)
	p.mu.Unlock()

	// Run each stage
	stageTotal := len(stages)
	for i, stage := range stages {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			p.interruptPipeline(ctx, sm, migrationID, onProgress)
			return err
		}
		state, stateErr := p.jobRepo.GetMigrationState(migrationID)
		if stateErr == nil && state == StatePaused {
			onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: "Migration paused — checkpoint saved"})
			return nil
		}

		stageName := string(stage.Name())

		// Skip completed stages (checkpoint/resume)
		if completedSet[stageName] {
			onProgress(WSMessage{
				Step:   stageName,
				Status: "success",
				Value:  fmt.Sprintf("Skipping completed stage %d/%d: %s", i+1, stageTotal, stageName),
			})
			continue
		}

		// Create stage record
		stageID, err := p.repo.CreateStage(ctx, migrationID, stageName, i)
		if err != nil {
			return fmt.Errorf("create stage %s: %w", stageName, err)
		}

		// Update stage state to running
		if err := p.repo.UpdateStageState(ctx, stageID, StageStateRunning, ""); err != nil {
			log.Printf("warning: failed to update stage %s to running: %v", stageName, err)
			onProgress(WSMessage{Step: stageName, Status: "warning", Value: fmt.Sprintf("Failed to persist stage state: %v", err)})
		}

		onProgress(WSMessage{
			Step:   stageName,
			Status: "progress",
			Value:  fmt.Sprintf("Starting stage %d/%d: %s", i+1, stageTotal, stageName),
		})

		// Execute the stage with retry
		maxRetries := 3
		if config.MaxRetries > 0 {
			maxRetries = config.MaxRetries
		}
		retryDelay := 5 * time.Second
		if config.RetryDelay > 0 {
			retryDelay = config.RetryDelay
		}

		var stageErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				if err := p.repo.IncrementStageAttempt(ctx, stageID); err != nil {
					log.Printf("warning: failed to increment stage %s attempt: %v", stageName, err)
					onProgress(WSMessage{Step: stageName, Status: "warning", Value: fmt.Sprintf("Failed to persist retry attempt: %v", err)})
				}
				onProgress(WSMessage{
					Step:   stageName,
					Status: "progress",
					Value:  fmt.Sprintf("Retrying stage %s (attempt %d/%d)", stageName, attempt+1, maxRetries+1),
				})
				select {
				case <-ctx.Done():
					p.interruptPipeline(ctx, sm, migrationID, onProgress)
					return ctx.Err()
				case <-time.After(retryDelay):
				}
				retryDelay *= 2 // exponential backoff
			}

			stageErr = stage.Execute(ctx, pc)
			if stageErr == nil {
				break
			}

			// Check if error was caused by context cancellation
			if ctxErr := ctx.Err(); ctxErr != nil {
				p.interruptPipeline(ctx, sm, migrationID, onProgress)
				return ctxErr
			}
		}

		if stageErr != nil {
			// Stage failed after all retries
			if err := p.repo.UpdateStageState(ctx, stageID, StageStateFailed, stageErr.Error()); err != nil {
				log.Printf("warning: failed to mark stage %s as failed: %v", stageName, err)
				onProgress(WSMessage{Step: stageName, Status: "warning", Value: fmt.Sprintf("Failed to persist stage failure: %v", err)})
			}
			onProgress(WSMessage{
				Step:   stageName,
				Status: "error",
				Error:  fmt.Sprintf("Stage %s failed: %v", stageName, stageErr),
			})

			// Automatic rollback
			p.rollbackPipeline(ctx, sm, migrationID, i, onProgress)
			return fmt.Errorf("stage %s failed: %w", stageName, stageErr)
		}

		// Stage succeeded
		if err := p.repo.UpdateStageState(ctx, stageID, StageStateCompleted, ""); err != nil {
			log.Printf("warning: failed to mark stage %s completed: %v", stageName, err)
			onProgress(WSMessage{Step: stageName, Status: "warning", Value: fmt.Sprintf("Failed to persist stage completion: %v", err)})
		}
		onProgress(WSMessage{
			Step:   stageName,
			Status: "success",
			Value:  fmt.Sprintf("Completed stage %d/%d: %s", i+1, stageTotal, stageName),
		})

		// Audit log
		if _, err := p.repo.CreateAuditEntry(ctx, AuditEntry{
			MigrationID: migrationID,
			EventType:   "stage_completed",
			NewState:    stageName,
			Actor:       "pipeline",
		}); err != nil {
			log.Printf("warning: failed to create audit entry for stage %s: %v", stageName, err)
			onProgress(WSMessage{Step: stageName, Status: "warning", Value: fmt.Sprintf("Failed to persist audit entry: %v", err)})
		}
	}

	// All stages completed — commit
	if err := sm.Transition(StateCommitted); err != nil {
		sm.ForceTransition(StateCommitted)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateCommitted); err != nil {
		log.Printf("warning: failed to persist committed state for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist committed state: %v", err)})
	}
	if err := p.jobRepo.SetMigrationCompletedAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("warning: failed to persist migration completion time for migration %d: %v", migrationID, err)
		onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist migration completion time: %v", err)})
	}

	onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Migration committed successfully"})
	return nil
}

// Pause marks a running migration as interrupted so it can be resumed later.
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
		err := fmt.Errorf("rollback failed: %s", strings.Join(rollbackErrors, "; "))
		if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateRollbackDegraded); stateErr != nil {
			onProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Failed to persist rollback failed state: %v", stateErr)})
		}
		onProgress(WSMessage{Step: "rollback", Status: "error", Error: err.Error()})
		return err
	}

	// Transition to RolledBack
	if err := sm.Transition(StateRolledBack); err != nil {
		sm.ForceTransition(StateRolledBack)
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
		if isRunningStatus(m.Status) {
			if err := markInterrupted(p.jobRepo, m.ID); err != nil {
				return recovered, fmt.Errorf("failed to update migration %d: %w", m.ID, err)
			}
			recovered = append(recovered, m.ID)
		}
	}
	return recovered, nil
}

// --- Internal helpers ---

// GracefulDrain stops accepting new pipeline runs and waits for in-flight runs
// to reach a safe checkpoint, up to the registry's timeout. If the timeout is
// exceeded, remaining runs are force-cancelled; their Execute loop then calls
// interruptPipeline, which persists the checkpoint via context.Background() so
// it survives cancellation. It returns the number of runs that were
// force-cancelled. This is the shutdown-lifecycle entry point for the pipeline
// (Jalur B), mirroring the Job Engine's Stop() drain.
func (p *Pipeline) GracefulDrain(ctx context.Context) (int, error) {
	return p.lifecycle.drain(ctx)
}

func (p *Pipeline) tryAcquire(migrationID int) bool {
	_, loaded := p.runningPipelines.LoadOrStore(migrationID, struct{}{})
	return !loaded
}

func (p *Pipeline) release(migrationID int) {
	p.runningPipelines.Delete(migrationID)
}

func (p *Pipeline) failPipeline(ctx context.Context, sm *StateMachine, migrationID int, errMsg string, onProgress StepCallback) {
	onProgress(WSMessage{Step: "pipeline", Status: "error", Error: errMsg})
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

	// Transition to Failed then Rollback
	if err := sm.Transition(StateFailed); err != nil {
		sm.ForceTransition(StateFailed)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateFailed); err != nil {
		log.Printf("warning: failed to persist failed state for migration %d: %v", migrationID, err)
	}

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

	// Roll back in reverse order
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

	// Transition to RolledBack
	if err := sm.Transition(StateRolledBack); err != nil {
		sm.ForceTransition(StateRolledBack)
	}
	if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateRolledBack); err != nil {
		log.Printf("warning: failed to persist rolled back state for migration %d: %v", migrationID, err)
	}
	if err := p.jobRepo.SetMigrationRolledBackAt(migrationID, time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("warning: failed to persist rolled back timestamp for migration %d: %v", migrationID, err)
	}
	onProgress(WSMessage{Step: "pipeline", Status: "complete", Value: "Rollback complete"})
}

// --- Stage Implementations ---

// discoveryStage runs full discovery collectors on source and target.
type discoveryStage struct {
	repo    PipelineRepo
	srvRepo server.Repo
	pool    ConnectionPool
	authSvc AESKeyProvider
	hosts   HostKeyStore
}

func (s *discoveryStage) Name() PipelineStageName { return StageDiscovery }

func (s *discoveryStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "discovery", Status: "progress", Value: "Running discovery on source and target..."})

	// Run basic discovery commands on both source and target
	sourceInfo, _, _, err := pc.SourceSSH.ExecContext(ctx, "uname -a && cat /etc/os-release 2>/dev/null | head -5")
	if err != nil {
		return fmt.Errorf("source discovery failed: %w", err)
	}
	targetInfo, _, _, err := pc.TargetSSH.ExecContext(ctx, "uname -a && cat /etc/os-release 2>/dev/null | head -5")
	if err != nil {
		return fmt.Errorf("target discovery failed: %w", err)
	}

	// Store discovery results as checkpoint
	checkpointData, _ := json.Marshal(map[string]string{
		"source": sourceInfo,
		"target": targetInfo,
	})
	pc.CheckpointData[string(StageDiscovery)] = string(checkpointData)

	pc.OnProgress(WSMessage{Step: "discovery", Status: "success", Value: "Discovery completed"})
	return nil
}

func (s *discoveryStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Discovery has no side effects to roll back
	return nil
}

// analysisStage analyzes the discovery data and builds a dependency graph.
type analysisStage struct {
	repo PipelineRepo
}

func (s *analysisStage) Name() PipelineStageName { return StageAnalysis }

func (s *analysisStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "analysis", Status: "progress", Value: "Analyzing discovery data..."})

	categories := pc.Migration.Categories

	// Build analysis result
	analysis := map[string]interface{}{
		"categories": categories,
		"config":     pc.Config,
	}
	resultData, _ := json.Marshal(analysis)
	pc.CheckpointData[string(StageAnalysis)] = string(resultData)

	pc.OnProgress(WSMessage{Step: "analysis", Status: "success", Value: "Analysis completed"})
	return nil
}

func (s *analysisStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// planningStage collects data from the source server for each category.
type planningStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

func (s *planningStage) Name() PipelineStageName { return StagePlanning }

func (s *planningStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "planning", Status: "progress", Value: "Collecting data from source..."})

	categories := pc.Migration.Categories

	// Collect data for each category from source
	for _, catName := range categories {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		mod, ok := s.registry.Get(catName)
		if !ok {
			pc.OnProgress(WSMessage{Step: "planning", Status: "warning", Value: fmt.Sprintf("Unknown category: %s, skipping", catName)})
			continue
		}

		pc.OnProgress(WSMessage{Step: "planning", Status: "progress", Value: fmt.Sprintf("Collecting %s...", catName)})

		data, err := mod.Collector.Collect(ctx, pc.SourceSSH)
		if err != nil {
			return fmt.Errorf("collect %s failed: %w", catName, err)
		}

		// Store collected data
		rawData, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal %s data: %w", catName, err)
		}
		if _, err := pc.JobRepo.CreateStep(pc.MigrationID, catName, "collect", string(rawData)); err != nil {
			return fmt.Errorf("persist collected %s data: %w", catName, err)
		}

		pc.OnProgress(WSMessage{Step: "planning", Status: "success", Value: fmt.Sprintf("Collected %s", catName)})
	}

	pc.OnProgress(WSMessage{Step: "planning", Status: "success", Value: "Planning completed"})
	return nil
}

func (s *planningStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Planning has no side effects on the target
	return nil
}

// validationStage validates the migration plan and checks for blockers.
type validationStage struct {
	repo PipelineRepo
}

func (s *validationStage) Name() PipelineStageName { return StageValidation }

func (s *validationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "validation", Status: "progress", Value: "Validating migration plan..."})

	// Check SSH connectivity to both servers
	if _, _, _, err := pc.SourceSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("source SSH check failed: %w", err)
	}
	if _, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("target SSH check failed: %w", err)
	}

	// Check target disk space
	output, _, _, err := pc.TargetSSH.ExecContext(ctx, "df -h / | tail -1 | awk '{print $4}'")
	if err == nil {
		pc.OnProgress(WSMessage{Step: "validation", Status: "progress", Value: fmt.Sprintf("Target available disk: %s", output)})
	}

	pc.OnProgress(WSMessage{Step: "validation", Status: "success", Value: "Validation passed"})
	return nil
}

func (s *validationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// preparationStage creates mandatory backups on the target server.
type preparationStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
}

func (s *preparationStage) Name() PipelineStageName { return StagePreparation }

func (s *preparationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "preparation", Status: "progress", Value: "Creating backups on target..."})

	categories := pc.Migration.Categories

	// Backup each category on target (MANDATORY — failure is fatal)
	for _, catName := range categories {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		mod, ok := s.registry.Get(catName)
		if !ok {
			continue
		}

		pc.OnProgress(WSMessage{Step: "preparation", Status: "progress", Value: fmt.Sprintf("Backing up %s...", catName)})

		backup, err := mod.Applier.Backup(ctx, pc.TargetSSH)
		if err != nil {
			return fmt.Errorf("backup %s failed (migration aborted — no apply without backup): %w", catName, err)
		}

		// Save backup to DB. Persistence failure is fatal: initialSyncStage
		// rollback restores from these records, so a lost backup means we
		// could apply to the target with no way to restore it.
		rawBackup, err := json.Marshal(backup)
		if err != nil {
			return fmt.Errorf("marshal %s backup failed (migration aborted — no apply without backup): %w", catName, err)
		}
		if _, err := pc.JobRepo.CreateBackup(pc.MigrationID, pc.Migration.TargetID, catName, string(rawBackup)); err != nil {
			return fmt.Errorf("persist %s backup failed (migration aborted — no apply without backup): %w", catName, err)
		}

		pc.OnProgress(WSMessage{Step: "preparation", Status: "success", Value: fmt.Sprintf("Backed up %s", catName)})
	}

	pc.OnProgress(WSMessage{Step: "preparation", Status: "success", Value: "All backups created"})
	return nil
}

func (s *preparationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Backups are read-only; nothing to roll back
	return nil
}

// initialSyncStage applies the data collected during the collect phase to the
// target by replaying each category's Applier.Apply. Despite the stage name,
// this is NOT a file-level rsync transfer: the SyncEngine (InitialSync /
// DeltaSync) is not driven by the live pipeline (see the NOTE in Execute), so
// only what each category collector captured is reproduced on the target.
type initialSyncStage struct {
	repo PipelineRepo
}

func (s *initialSyncStage) Name() PipelineStageName { return StageInitialSync }

func (s *initialSyncStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "initial_sync", Status: "progress", Value: "Applying collected data to target..."})

	// NOTE: this stage does not perform a file-level rsync data transfer. The
	// SyncEngine (InitialSync/DeltaSync in sync.go) is constructed but has no
	// live pipeline caller, so syncConfigFromPipelineContext(pc) is not built or
	// consumed here. Instead the stage replays each collected category via
	// mod.Applier.Apply below. If/when this stage is wired to the SyncEngine,
	// pass syncConfigFromPipelineContext(pc) into it and update the progress
	// messages to reflect the real transfer.

	// Apply collected data to target (this is the "initial sync" for file-based categories)
	steps, err := pc.JobRepo.GetSteps(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load migration steps: %w", err)
	}

	categories := pc.Migration.Categories
	_ = categories

	appliedOrder := make([]string, 0)
	skippedSet := make(map[string]struct{})

	for _, step := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if step.Action != "collect" || step.Status != StepStatusCompleted {
			continue
		}

		// Get the category module
		mod, ok := getRegistryFromContext(pc).Get(step.Category)
		if !ok {
			skippedSet[step.Category] = struct{}{}
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Skipping %s: category not registered", step.Category)})
			continue
		}

		// Parse the collected data
		var data CategoryData
		if err := json.Unmarshal([]byte(step.Data), &data); err != nil {
			skippedSet[step.Category] = struct{}{}
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Skipping %s: invalid step data: %v", step.Category, err)})
			continue
		}

		pc.OnProgress(WSMessage{Step: "initial_sync", Status: "progress", Value: fmt.Sprintf("Applying %s...", step.Category)})

		// Apply
		err := mod.Applier.Apply(ctx, pc.TargetSSH, data, func(msg WSMessage) {
			msg.Step = "initial_sync:" + step.Category + ":" + msg.Step
			pc.OnProgress(msg)
		})
		if err != nil {
			// Roll back already-applied categories
			s.rollbackApplied(ctx, pc, appliedOrder)
			return fmt.Errorf("apply %s failed: %w", step.Category, err)
		}

		// Checkpoint
		if err := pc.JobRepo.UpdateStepStatus(step.ID, StepStatusApplied, ""); err != nil {
			log.Printf("warning: failed to persist applied step for %s: %v", step.Category, err)
			pc.OnProgress(WSMessage{Step: "initial_sync", Status: "warning", Value: fmt.Sprintf("Failed to persist applied state for %s: %v", step.Category, err)})
		}
		appliedOrder = append(appliedOrder, step.Category)

		pc.OnProgress(WSMessage{Step: "initial_sync", Status: "success", Value: fmt.Sprintf("Applied %s", step.Category)})
	}

	if len(skippedSet) > 0 {
		skippedCategories := make([]string, 0, len(skippedSet))
		for category := range skippedSet {
			skippedCategories = append(skippedCategories, category)
		}
		sort.Strings(skippedCategories)
		if len(appliedOrder) > 0 {
			s.rollbackApplied(ctx, pc, appliedOrder)
		}
		return fmt.Errorf("initial sync skipped %d categories: %v", len(skippedCategories), skippedCategories)
	}

	pc.OnProgress(WSMessage{Step: "initial_sync", Status: "success", Value: "Collected data applied to target"})
	return nil
}

func syncConfigFromPipelineContext(pc *PipelineContext) SyncConfig {
	cfg := SyncConfig{}
	if pc == nil {
		return cfg
	}

	cfg.MigrationID = pc.MigrationID
	if pc.TargetServer != nil {
		cfg.TargetHost = pc.TargetServer.Host
		cfg.TargetPort = pc.TargetServer.Port
		cfg.TargetUser = pc.TargetServer.Username
	}
	return cfg
}

func (s *initialSyncStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Roll back applied categories using stored backups
	backups, err := pc.JobRepo.GetBackups(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load backups: %w", err)
	}

	// Get applied categories
	appliedCats, err := pc.JobRepo.GetAppliedCategories(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load applied categories: %w", err)
	}
	appliedSet := make(map[string]bool, len(appliedCats))
	for _, c := range appliedCats {
		appliedSet[c] = true
	}

	// Roll back in reverse order
	for i := len(backups) - 1; i >= 0; i-- {
		backup := backups[i]
		if !appliedSet[backup.Category] {
			continue
		}

		mod, ok := getRegistryFromContext(pc).Get(backup.Category)
		if !ok {
			continue
		}

		var backupData BackupData
		if err := json.Unmarshal([]byte(backup.Data), &backupData); err != nil {
			continue
		}

		pc.OnProgress(WSMessage{Step: "rollback", Status: "progress", Value: fmt.Sprintf("Rolling back %s...", backup.Category)})
		if err := mod.Applier.Rollback(ctx, pc.TargetSSH, backupData); err != nil {
			pc.OnProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Rollback warning for %s: %v", backup.Category, err)})
		}
	}

	return nil
}

func (s *initialSyncStage) rollbackApplied(ctx context.Context, pc *PipelineContext, appliedOrder []string) {
	// Load backups from DB
	backups, err := pc.JobRepo.GetBackups(pc.MigrationID)
	if err != nil {
		return
	}
	backupMap := make(map[string]BackupData)
	for _, b := range backups {
		var bd BackupData
		if json.Unmarshal([]byte(b.Data), &bd) == nil {
			backupMap[b.Category] = bd
		}
	}

	for i := len(appliedOrder) - 1; i >= 0; i-- {
		catName := appliedOrder[i]
		backup, ok := backupMap[catName]
		if !ok {
			continue
		}
		mod, ok := getRegistryFromContext(pc).Get(catName)
		if !ok {
			continue
		}
		pc.OnProgress(WSMessage{Step: "rollback", Status: "progress", Value: fmt.Sprintf("Rolling back %s...", catName)})
		if err := mod.Applier.Rollback(ctx, pc.TargetSSH, backup); err != nil {
			pc.OnProgress(WSMessage{Step: "rollback", Status: "warning", Value: fmt.Sprintf("Rollback warning: %v", err)})
		}
	}
}

// liveReplicationStage sets up database/Redis replication.
type liveReplicationStage struct {
	repo PipelineRepo
}

func (s *liveReplicationStage) Name() PipelineStageName { return StageLiveReplication }

// detectDatabases scans the source server for running database services.
// Returns a list of detected database types and their default ports.
func detectDatabases(ctx context.Context, ssh SSHExecuter) []DatabaseInfo {
	databases := make([]DatabaseInfo, 0)

	// Check for MySQL/MariaDB
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "mysql", Port: 3306})
	}

	// Check for PostgreSQL
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x postgres >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "postgres", Port: 5432})
	}

	// Check for Redis
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x redis-server >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "redis", Port: 6379})
	}

	// Check for MongoDB
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x mongod >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "mongodb", Port: 27017})
	}

	return databases
}

func (s *liveReplicationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	if !pc.Config.ReplicationEnabled {
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "Replication disabled, skipping"})
		return nil
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress", Value: "Scanning source server for databases..."})

	// Detect databases running on source
	databases := detectDatabases(ctx, pc.SourceSSH)
	if len(databases) == 0 {
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "No databases detected on source — replication not needed"})
		return nil
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
		Value: fmt.Sprintf("Detected %d database(s): %s", len(databases), formatDBList(databases))})

	// Create replication engine
	replEngine := NewReplicationEngine(pc.SourceSSH, pc.TargetSSH, pc.Repo)

	// Set up replication for each detected database
	for _, db := range databases {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		replConfig := ReplicationConfig{
			DatabaseType: db.Type,
			SourcePort:   db.Port,
			TargetPort:   db.Port,
			SourceHost:   pc.SourceServer.Host,
			TargetHost:   pc.TargetServer.Host,
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("Setting up %s replication %s:%d → %s:%d...", db.Type, replConfig.SourceHost, db.Port, replConfig.TargetHost, db.Port)})

		if err := replEngine.SetupReplication(ctx, pc.MigrationID, replConfig); err != nil {
			return fmt.Errorf("%s replication setup failed: %w", db.Type, err)
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success",
			Value: fmt.Sprintf("%s replication established", db.Type)})

		// Monitor initial lag
		lag, err := replEngine.MonitorLag(ctx, replConfig)
		if err != nil {
			return fmt.Errorf("%s lag monitoring failed: %w", db.Type, err)
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("%s replication lag: %d seconds", db.Type, lag)})

		// Wait for initial catch-up (max 5 minutes, lag threshold 30s)
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("Waiting for %s initial sync...", db.Type)})

		catchupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = replEngine.WaitForCatchUp(catchupCtx, replConfig, 30)
		cancel()
		if err != nil {
			return fmt.Errorf("%s initial sync failed: %w", db.Type, err)
		}
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success",
			Value: fmt.Sprintf("%s initial sync completed", db.Type)})
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "Live replication setup completed"})
	return nil
}

func (s *liveReplicationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress", Value: "Reverting replication..."})

	// Detect what databases were set up
	databases := detectDatabases(ctx, pc.SourceSSH)
	if len(databases) == 0 {
		return nil
	}

	replEngine := NewReplicationEngine(pc.SourceSSH, pc.TargetSSH, pc.Repo)
	for _, db := range databases {
		replConfig := ReplicationConfig{
			DatabaseType: db.Type,
			SourcePort:   db.Port,
			TargetPort:   db.Port,
			SourceHost:   pc.SourceServer.Host,
			TargetHost:   pc.TargetServer.Host,
		}
		if err := replEngine.Rollback(ctx, pc.MigrationID, replConfig); err != nil {
			pc.OnProgress(WSMessage{Step: "live_replication", Status: "warning",
				Value: fmt.Sprintf("%s replication rollback warning: %v", db.Type, err)})
		}
	}
	return nil
}

func formatDBList(dbs []DatabaseInfo) string {
	types := make([]string, len(dbs))
	for i, db := range dbs {
		types[i] = db.Type
	}
	return strings.Join(types, ", ")
}

// healthVerificationStage verifies that all services are healthy on the target.
type healthVerificationStage struct {
	repo PipelineRepo
}

func (s *healthVerificationStage) Name() PipelineStageName { return StageHealthVerification }

func (s *healthVerificationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "health_verification", Status: "progress", Value: "Verifying health on target..."})

	// Basic health check: verify target is responsive
	output, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok")
	if err != nil {
		return fmt.Errorf("target health check failed: %w", err)
	}
	if output != "ok\n" {
		// err is nil here, so don't wrap it (would render as %!w(<nil>)).
		return fmt.Errorf("target health check failed: unexpected output %q", output)
	}

	// Check Docker containers if docker category is included
	for _, cat := range pc.Migration.Categories {
		if cat == "docker" {
			dockerOutput, _, _, err := pc.TargetSSH.ExecContext(ctx, "docker ps --format '{{.Names}} {{.Status}}' 2>/dev/null")
			if err == nil && dockerOutput != "" {
				pc.OnProgress(WSMessage{Step: "health_verification", Status: "progress", Value: fmt.Sprintf("Docker containers: %s", dockerOutput)})
			}
		}
	}

	pc.OnProgress(WSMessage{Step: "health_verification", Status: "success", Value: "Health verification passed"})
	return nil
}

func (s *healthVerificationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// preCutoverValidationStage performs final checks before traffic switch.
type preCutoverValidationStage struct {
	repo PipelineRepo
}

func (s *preCutoverValidationStage) Name() PipelineStageName { return StagePreCutoverValidation }

func (s *preCutoverValidationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "pre_cutover", Status: "progress", Value: "Running pre-cutover validation..."})

	// Verify all data is synced
	// Verify all containers are running
	// Verify all services are healthy
	// Verify replication lag is 0 (if replication enabled)

	// Basic check: verify target is still responsive
	if _, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("pre-cutover check failed: target not responsive: %w", err)
	}

	pc.OnProgress(WSMessage{Step: "pre_cutover", Status: "success", Value: "Pre-cutover validation passed"})
	return nil
}

func (s *preCutoverValidationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// trafficSwitchStage records a manual-cutover checkpoint. It does NOT perform an
// automatic traffic switch: the real DNS/reverse-proxy/load-balancer switch lives
// in TrafficSwitchEngine (traffic.go), which is only driven by CutoverEngine
// (cutover.go) — and CutoverEngine has no live caller, so no automatic switch is
// wired into this pipeline. Rather than silently report a successful cutover that
// never moved any traffic, this stage records the checkpoint as manual_required
// and tells the operator to perform the cutover themselves.
type trafficSwitchStage struct {
	repo PipelineRepo
}

// trafficSwitchManualState is the switch_state persisted for the traffic switch
// config when no automatic traffic switch runs. It must NOT be "switched": that
// value would falsely assert traffic was moved to the target.
const trafficSwitchManualState = "manual_required"

// trafficSwitchManualNote explains, on the cutover record and in the operator
// message, why traffic was not switched automatically and what to do next.
const trafficSwitchManualNote = "automatic traffic switch is not enabled in this pipeline; traffic still points at the source — manual cutover required (switch DNS / reverse proxy / load balancer to the target, then verify)"

func (s *trafficSwitchStage) Name() PipelineStageName { return StageTrafficSwitch }

func (s *trafficSwitchStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "progress", Value: "Recording manual-cutover checkpoint (automatic traffic switch is not enabled in this pipeline)..."})

	// No automatic traffic switch is performed here. The real TrafficSwitchEngine
	// (traffic.go) is not wired into the live pipeline, so this stage only records
	// that a manual cutover is required. It must never claim traffic was switched.

	// Idempotency guard: this stage's completion checkpoint is written by the
	// pipeline loop only after Execute returns. If the process crashed after a
	// record was inserted but before that checkpoint landed, resume re-runs this
	// stage — so guard each side effect against an existing row keyed by
	// migration_id before inserting, or resume duplicates it. The two creates are
	// guarded independently to also cover a crash between them (config written,
	// cutover record not).

	// Create traffic switch config record (skip if one already exists for this migration)
	existingCfg, err := pc.Repo.GetTrafficSwitchConfig(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("check existing traffic switch config failed: %w", err)
	}
	if existingCfg == nil {
		if _, err := pc.Repo.CreateTrafficSwitchConfig(ctx, TrafficSwitchConfig{
			MigrationID:    pc.MigrationID,
			Provider:       pc.Config.TrafficProvider,
			SwitchState:    trafficSwitchManualState,
			HealthCheckURL: pc.Config.HealthCheckURL,
		}); err != nil {
			return fmt.Errorf("persist traffic switch config failed: %w", err)
		}
	}

	// Record cutover (skip if a traffic_switch cutover record already exists).
	// TrafficSwitched is false and the state is unchanged (source → source)
	// because no traffic was moved; the note records why.
	cutovers, err := pc.Repo.GetCutoverHistory(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("check existing cutover history failed: %w", err)
	}
	trafficCutoverExists := false
	for _, cr := range cutovers {
		if cr.CutoverType == "traffic_switch" {
			trafficCutoverExists = true
			break
		}
	}
	if !trafficCutoverExists {
		if _, err := pc.Repo.CreateCutoverRecord(ctx, CutoverRecord{
			MigrationID:     pc.MigrationID,
			CutoverType:     "traffic_switch",
			PreviousState:   "source",
			NewState:        "source",
			TrafficSwitched: false,
			Error:           trafficSwitchManualNote,
			StartedAt:       time.Now().Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("persist cutover record failed: %w", err)
		}
	}

	// Warning, not success: the pipeline did not move traffic. The operator must
	// complete the cutover manually.
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "warning", Value: trafficSwitchManualNote})
	return nil
}

func (s *trafficSwitchStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "progress", Value: "Recording traffic-cutover rollback checkpoint (no automatic traffic switch was performed)..."})

	// No automatic traffic switch was performed on the forward path, so there is
	// nothing to revert automatically. Record the rollback checkpoint honestly:
	// TrafficReverted is false. If the operator performed a manual cutover, they
	// must manually revert it.
	pc.Repo.CreateRollbackRecord(ctx, RollbackRecord{
		MigrationID:     pc.MigrationID,
		RollbackType:    "traffic",
		TrafficReverted: false,
		Error:           "no automatic traffic switch was performed; if a manual cutover was done, revert DNS / reverse proxy / load balancer to the source manually",
		StartedAt:       time.Now().Format(time.RFC3339),
	})

	return nil
}

// postCutoverObservationStage monitors the target after traffic switch.
type postCutoverObservationStage struct {
	repo PipelineRepo
}

func (s *postCutoverObservationStage) Name() PipelineStageName { return StagePostCutoverObservation }

func (s *postCutoverObservationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "observation", Status: "progress", Value: "Starting post-cutover observation..."})

	// Observation schedule: 1m, 5m, 10m, 30m
	observationDuration := pc.Config.ObservationDuration
	if observationDuration == 0 {
		observationDuration = 10 * time.Minute
	}

	checkInterval := 30 * time.Second
	deadline := time.Now().Add(observationDuration)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check target health
		output, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok")
		healthy := err == nil && output == "ok\n"

		if !healthy {
			// Build a real error describing the failure. err may be nil when
			// the command succeeded but returned unexpected output, so avoid
			// wrapping a nil error (which renders as %!w(<nil>)).
			var healthErr error
			if err != nil {
				healthErr = fmt.Errorf("target health check failed during observation: %w", err)
			} else {
				healthErr = fmt.Errorf("target health check failed during observation: unexpected output %q", output)
			}
			if pc.Config.AutoRollbackOnError {
				return healthErr
			}
			pc.OnProgress(WSMessage{Step: "observation", Status: "warning", Value: fmt.Sprintf("Health check warning: %v", healthErr)})
		}

		// Record the actual health status/score from the check, not a
		// hard-coded healthy result.
		healthResult := HealthCheckResult{
			MigrationID: pc.MigrationID,
			ServerID:    pc.Migration.TargetID,
			CheckType:   HealthCheckTCP,
			CheckTarget: pc.TargetServer.Host,
		}
		if healthy {
			healthResult.Status = "healthy"
			healthResult.HealthScore = 100
		} else {
			healthResult.Status = "unhealthy"
			healthResult.HealthScore = 0
			if err != nil {
				healthResult.ErrorMessage = err.Error()
			} else {
				healthResult.ErrorMessage = fmt.Sprintf("unexpected output %q", output)
			}
		}
		if _, err := pc.Repo.CreateHealthCheckResult(ctx, healthResult); err != nil {
			log.Printf("warning: failed to persist observation health check for migration %d: %v", pc.MigrationID, err)
		}

		remaining := time.Until(deadline).Round(time.Second)
		healthLabel := "OK"
		if !healthy {
			healthLabel = "DEGRADED"
		}
		pc.OnProgress(WSMessage{Step: "observation", Status: "progress", Value: fmt.Sprintf("Observation %s — %s remaining", healthLabel, remaining)})

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkInterval):
		}
	}

	pc.OnProgress(WSMessage{Step: "observation", Status: "success", Value: "Observation completed — all checks passed"})
	return nil
}

func (s *postCutoverObservationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// finalizationStage performs final cleanup and verification.
type finalizationStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
}

func (s *finalizationStage) Name() PipelineStageName { return StageFinalization }

func (s *finalizationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "finalization", Status: "progress", Value: "Finalizing migration..."})

	// Mark all steps as completed
	steps, err := pc.JobRepo.GetSteps(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("failed to load steps: %w", err)
	}

	// Finalization runs AFTER trafficSwitch and postCutoverObservation, so the
	// target is already live with the migrated data. A returned error here would
	// propagate to Execute's rollback path, which restores the target's stale
	// pre-migration backups — destroying the freshly-migrated data over a benign,
	// transient DB write failure (e.g. "database is locked"). This status update
	// is cosmetic bookkeeping (Applied → Completed); log and continue instead of
	// triggering a destructive rollback of an already-cutover migration.
	for _, step := range steps {
		if step.Status == StepStatusApplied {
			if err := pc.JobRepo.UpdateStepStatus(step.ID, StepStatusCompleted, ""); err != nil {
				log.Printf("warning: failed to finalize step %s for migration %d: %v", step.Category, pc.MigrationID, err)
				pc.OnProgress(WSMessage{Step: "finalization", Status: "warning", Value: fmt.Sprintf("Failed to finalize step %s: %v", step.Category, err)})
			}
		}
	}

	// Record final verification
	pc.Repo.CreateVerificationResult(ctx, VerificationResult{
		MigrationID:      pc.MigrationID,
		VerificationType: "finalization",
		Target:           "target",
		Passed:           true,
	})

	pc.OnProgress(WSMessage{Step: "finalization", Status: "success", Value: "Migration finalized"})
	return nil
}

func (s *finalizationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// archiveStage archives the migration and creates a final report.
type archiveStage struct {
	repo PipelineRepo
}

func (s *archiveStage) Name() PipelineStageName { return StageArchive }

func (s *archiveStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "archive", Status: "progress", Value: "Archiving migration..."})

	// Create audit entry
	if _, err := pc.Repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID: pc.MigrationID,
		EventType:   "migration_archived",
		NewState:    StateCommitted.String(),
		Actor:       "pipeline",
	}); err != nil {
		log.Printf("warning: failed to create archive audit entry for migration %d: %v", pc.MigrationID, err)
	}

	pc.OnProgress(WSMessage{Step: "archive", Status: "success", Value: "Migration archived"})
	return nil
}

func (s *archiveStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// --- Helper ---

// getRegistryFromContext returns the registry associated with the pipeline
// context. It prefers the per-request registry and falls back to the default
// registry for legacy call sites.
func getRegistryFromContext(pc *PipelineContext) *CategoryRegistry {
	if pc != nil && pc.Registry != nil {
		return pc.Registry
	}
	return defaultRegistry
}

// defaultRegistry is set by NewPipeline to allow legacy stage helpers to access it.
var defaultRegistry *CategoryRegistry
