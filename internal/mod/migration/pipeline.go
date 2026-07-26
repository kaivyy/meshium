package migration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"meshium/internal/mod/server"
	"meshium/internal/mod/transfer"
	"meshium/internal/shared"
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
	// CheckpointStore persists resumable transfer checkpoints for this run. It is
	// copied from the Pipeline at Execute time so stages (initialSyncStage) can
	// inject it into the DatabaseApplier without reaching back into the Pipeline.
	CheckpointStore transfer.CheckpointStore
	// BackupRefs maps category → restore-point reference (see backup_ref.go).
	// preparationStage fills it when it persists each category's backup;
	// initialSyncStage stamps it onto every item it applies, which is what
	// gives rollback per-item evidence instead of category-wide guesswork.
	BackupRefs map[string]string
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
	// sem caps the number of pipelines executing at once across DISTINCT
	// migrations. runningPipelines only dedupes a single migration; without sem
	// a fleet of migrations would run unbounded in parallel and contend for the
	// same source/target disks, DB, and SSH connections.
	sem           chan struct{}
	maxConcurrent int
	lifecycle     *pipelineRegistry
	// resourceMu guards resourceLocks. runningPipelines stops a SINGLE migration
	// from running twice; sem caps the GLOBAL count; resourceLocks stop two
	// DISTINCT migrations from mutating the same server concurrently (e.g.
	// migration A: host 3→4 and migration B: host 4→5 would otherwise fork
	// both sides of host 4 at once and corrupt its disks/DB/SSH). Refcounted
	// so a migration holding several servers releases each exactly once.
	resourceMu    sync.Mutex
	resourceLocks map[int]int // serverID → holder count
	// checkpointStore persists resumable transfer checkpoints (transfer package).
	// The DB category's file-path engines (PostgreSQL, Redis) use it to resume a
	// killed multi-GB dump/restore from the last byte offset instead of restarting.
	checkpointStore transfer.CheckpointStore
}

// DefaultMaxConcurrentMigrations bounds how many migrations may run at once.
// It is a global safety ceiling, not a perf tuning knob: one host's SSH pool
// and disks saturate well before the CPU does, so this prevents one operator
// from starving others. Overridable via SetMaxConcurrent.
const DefaultMaxConcurrentMigrations = 4

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
	checkpointStore transfer.CheckpointStore,
) (*Pipeline, error) {
	if registry == nil {
		return nil, fmt.Errorf("category registry is required")
	}
	p := &Pipeline{
		repo:            repo,
		jobRepo:         jobRepo,
		srvRepo:         srvRepo,
		pool:            pool,
		authSvc:         authSvc,
		hosts:           hosts,
		registry:        registry,
		lifecycle:       newPipelineRegistry(),
		checkpointStore: checkpointStore,
	}
	p.maxConcurrent = DefaultMaxConcurrentMigrations
	p.sem = make(chan struct{}, p.maxConcurrent)
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
		&trafficSwitchStage{repo: p.repo, policy: DefaultPolicy},
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

	// Bound global parallelism across distinct migrations. If shutdown is in
	// progress the lifecycle register check below will reject; here we just
	// wait for a free slot (honoring ctx cancellation).
	if !p.acquireSlot(ctx) {
		return fmt.Errorf("migration %d not started: %w", migrationID, ctx.Err())
	}
	defer p.releaseSlot()

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

	// Claim exclusive ownership of the source AND target servers before any
	// mutating work begins. runningPipelines blocks a re-entrant same-migration
	// run; this blocks a DISTINCT migration from forking the same host while we
	// hold it (e.g. migration A: 3→4 and B: 4→5 must not run concurrently, or
	// host 4 is corrupted on both ends). The check-and-claim is atomic so two
	// goroutines cannot both win the race. Fail closed (no wait) if either
	// server is already held — a stuck wait risks a wedged run.
	heldServers := p.tryAcquireResource(migration.SourceID, migration.TargetID)
	if heldServers == nil {
		return fmt.Errorf("migration %d cannot start: source or target server is busy with another migration", migrationID)
	}
	defer p.releaseResourceLocks(heldServers)

	// Load config
	config, err := p.repo.GetMigrationConfig(migrationID)
	if err != nil {
		config = DefaultMigrationConfig()
	}
	// Decrypt the DB credentials for the database category's Apply step.
	// Stored encrypted (pipeline_handler encryptDBConfig); plaintext only in
	// memory for the lifetime of this execution.
	if config.DatabaseConfig != nil && config.DatabaseConfig.Password != "" && config.DatabaseConfig.Password != "set" {
		if p.authSvc != nil {
			if key := p.authSvc.GetAESKey(); key != nil {
				if dec, derr := shared.Decrypt(key, []byte(config.DatabaseConfig.Password)); derr == nil {
					config.DatabaseConfig.Password = string(dec)
				}
			}
		}
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
		MigrationID:     migrationID,
		Migration:       migration,
		Config:          config,
		SourceSSH:       sourceSSH,
		TargetSSH:       targetSSH,
		SourceServer:    sourceServer,
		TargetServer:    targetServer,
		Repo:            p.repo,
		JobRepo:         p.jobRepo,
		Registry:        p.registry,
		OnProgress:      onProgress,
		StateMachine:    sm,
		CheckpointData:  make(map[string]string),
		CheckpointStore: p.checkpointStore,
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
		var attemptsUsed int
		for attempt := 0; attempt <= maxRetries; attempt++ {
			attemptsUsed = attempt + 1
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
			// P0-2 cutover: ErrAwaitingCutover is a clean stop, not a failure.
			// The trafficSwitchStage recorded manual_required; persist
			// StateAwaitingCutover and return — do NOT advance to Observing /
			// Committed / Completed, do NOT roll back. Survives restart.
			if errors.Is(stageErr, ErrAwaitingCutover) {
				if err := p.repo.UpdateStageState(ctx, stageID, StageStateCompleted, "awaiting_cutover"); err != nil {
					log.Printf("warning: failed to mark stage %s awaiting_cutover: %v", stageName, err)
				}
				if err := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateAwaitingCutover); err != nil {
					onProgress(WSMessage{Step: "pipeline", Status: "warning", Value: fmt.Sprintf("Failed to persist awaiting_cutover: %v", err)})
				}
				onProgress(WSMessage{Step: "traffic_switch", Status: "warning", Value: "Pipeline stopped: manual cutover required. Confirm traffic moved to target, then commit via the cutover endpoint."})
				return nil
			}

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

		// Stage succeeded. P0-3: persist a stage checkpoint BEFORE marking the
		// stage completed / emitting success. A checkpoint write failure fails
		// closed — do not advance the stage to completed (which would let a
		// resume skip work whose progress was never durably recorded).
		checkpointJSON := stageCheckpointJSON(stageName, attemptsUsed, stageTotal)
		if err := p.repo.UpdateStageCheckpoint(ctx, stageID, checkpointJSON); err != nil {
			return fmt.Errorf("persist checkpoint for stage %s: %w", stageName, err)
		}
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

	// All stages completed — commit. P0-2: never ForceTransition(Committed).
	// The trafficSwitchStage returns ErrAwaitingCutover for a manual cutover,
	// stopping the loop at StateAwaitingCutover before reaching here, so this
	// path is only reached when cutover was confirmed. If the validated
	// Transition fails, the from-state was unexpected — fail closed to
	// NeedsManualIntervention rather than forcing Committed past the rules.
	if err := sm.Transition(StateCommitted); err != nil {
		if stateErr := p.jobRepo.SetMigrationStateContext(ctx, migrationID, StateNeedsManualIntervention); stateErr != nil {
			log.Printf("error: commit transition failed (%v) and could not fail-closed: %v", err, stateErr)
		}
		return fmt.Errorf("commit transition failed: %w", err)
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
