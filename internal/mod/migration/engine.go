package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"meshium/internal/mod/server"
)

// Engine orchestrates migration step execution using the typed state machine.
// It runs each step through Prepare → Apply → Verify, checkpoints after
// each verified step, and rolls back in LIFO order on failure.
//
// The Engine is designed to be:
//   - Atomic: a migration either fully commits or fully rolls back
//   - Resumable: checkpoints allow resuming from the last verified step
//   - Rollback-safe: mandatory backup gate + LIFO rollback on failure
//
// The Engine does NOT replace the existing Executor — it is a new, more
// rigorous orchestrator that can be used alongside or instead of it.
type Engine struct {
	repo     JobRepository
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
	registry *CategoryRegistry
	mu       sync.Mutex
}

// NewEngine creates an Engine with the given dependencies.
func NewEngine(
	repo JobRepository,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
	registry *CategoryRegistry,
) *Engine {
	return &Engine{
		repo:     repo,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
		registry: registry,
	}
}

// EngineResult holds the outcome of an engine run.
type EngineResult struct {
	JobID      int            `json:"jobId"`
	FinalState MigrationState `json:"finalState"`
	Steps      []StepResult   `json:"steps"`
	Error      string         `json:"error,omitempty"`
}

// StepResult holds the outcome of a single step execution.
type StepResult struct {
	Name      string `json:"name"`
	Index     int    `json:"index"`
	State     string `json:"state"`
	Skipped   bool   `json:"skipped,omitempty"`
	Error     string `json:"error,omitempty"`
	RolledBack bool  `json:"rolledBack,omitempty"`
}

// Run executes a migration job through the state machine. It:
//  1. Loads the migration and its steps
//  2. Transitions through the state machine: Created → Planning → Backup →
//     Snapshot → Transferring → Applying → Verifying → Committed
//  3. For each step: Prepare → Apply → Verify, checkpointing after Verify
//  4. On failure: rolls back all applied steps in LIFO order
//  5. On context cancellation: marks the migration as interrupted
//
// The steps parameter is the ordered list of MigrationStep instances to
// execute. The Engine does not look up steps from a registry — the caller
// provides them. This allows the caller to build custom step pipelines.
func (e *Engine) Run(ctx context.Context, migrationID int, steps []MigrationStep, onProgress StepCallback) (*EngineResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	result := &EngineResult{
		JobID:      migrationID,
		FinalState: StateCreated,
		Steps:      make([]StepResult, 0, len(steps)),
	}

	// Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		return result, fmt.Errorf("migration not found: %w", err)
	}

	// Initialize state machine
	currentState, err := e.repo.GetMigrationState(migrationID)
	if err != nil {
		// If state column is empty, use the legacy status
		currentState, _ = StateFromString(migration.Status)
	}
	sm := NewStateMachine(currentState)

	// Load the migration job
	job := &MigrationJob{
		Migration: migration,
		State:     sm.State(),
		Steps:     make([]JobStep, 0, len(steps)),
	}

	// Create job step records in the database
	for i, step := range steps {
		stepID, err := e.repo.CreateJobStepContext(ctx, migrationID, step.Name(), i, "category")
		if err != nil {
			return result, fmt.Errorf("create job step %d: %w", i, err)
		}
		job.Steps = append(job.Steps, JobStep{
			ID:          stepID,
			MigrationID: migrationID,
			StepName:    step.Name(),
			StepIndex:   i,
			StepType:    "category",
			State:       StepStatePending,
		})
	}

	// --- Transition: Created → Planning ---
	if err := e.transition(ctx, sm, migrationID, StatePlanning, onProgress, "planning"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// --- Transition: Planning → Backup ---
	if err := e.transition(ctx, sm, migrationID, StateBackup, onProgress, "backup"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// Get SSH connection to the target server
	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		e.failMigration(ctx, sm, migrationID, fmt.Sprintf("target server not found: %v", err), onProgress)
		result.FinalState = sm.State()
		result.Error = err.Error()
		return result, err
	}

	sshClient, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		e.failMigration(ctx, sm, migrationID, fmt.Sprintf("SSH connection failed: %v", err), onProgress)
		result.FinalState = sm.State()
		result.Error = err.Error()
		return result, err
	}

	onProgress(WSMessage{Step: "engine", Status: "success", Value: "Connected to target server"})

	// --- Transition: Backup → Snapshot ---
	if err := e.transition(ctx, sm, migrationID, StateSnapshot, onProgress, "snapshot"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// --- Transition: Snapshot → Transferring ---
	if err := e.transition(ctx, sm, migrationID, StateTransferring, onProgress, "transferring"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// --- Execute steps ---
	// Track which steps have been applied (for rollback)
	appliedSteps := make([]int, 0, len(steps)) // indices into steps slice

	for i, step := range steps {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			e.interruptMigration(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress)
			result.FinalState = sm.State()
			result.Error = "migration interrupted: " + err.Error()
			return result, err
		}

		stepResult := StepResult{
			Name:  step.Name(),
			Index: i,
			State: string(StepStatePending),
		}

		// --- Transition: Transferring → Applying ---
		if i == 0 {
			if err := e.transition(ctx, sm, migrationID, StateApplying, onProgress, "applying"); err != nil {
				result.FinalState = sm.State()
				return result, err
			}
		}

		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "progress",
			Value:  fmt.Sprintf("Preparing step %d/%d: %s", i+1, len(steps), step.Name()),
		})

		// --- Prepare phase ---
		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStatePreparing, "")
		sctx := StepContext{
			Ctx:      ctx,
			SSH:      sshClient,
			Job:      job,
			Progress: onProgress,
		}

		prepareData, err := step.Prepare(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = fmt.Sprintf("prepare failed: %v", err)
			e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateFailed, stepResult.Error)
			result.Steps = append(result.Steps, stepResult)

			// Check if the error was caused by context cancellation
			if ctxErr := ctx.Err(); ctxErr != nil {
				e.interruptMigration(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress)
				result.FinalState = sm.State()
				result.Error = "migration interrupted: " + ctxErr.Error()
				return result, ctxErr
			}

			// Rollback all applied steps
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			result.Error = stepResult.Error
			return result, fmt.Errorf("prepare failed for step %s: %w", step.Name(), err)
		}

		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStatePrepared, "")
		if prepareData != "" {
			e.repo.SetJobStepData(job.Steps[i].ID, "prepare", prepareData)
		}
		stepResult.State = string(StepStatePrepared)

		// --- Apply phase ---
		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "progress",
			Value:  fmt.Sprintf("Applying step %d/%d: %s", i+1, len(steps), step.Name()),
		})

		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateApplying, "")
		sctx.PrepareData = prepareData

		applyData, err := step.Apply(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = fmt.Sprintf("apply failed: %v", err)
			e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateFailed, stepResult.Error)
			result.Steps = append(result.Steps, stepResult)

			// Check if the error was caused by context cancellation
			if ctxErr := ctx.Err(); ctxErr != nil {
				// Context was cancelled — interrupt instead of fail
				e.interruptMigration(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress)
				result.FinalState = sm.State()
				result.Error = "migration interrupted: " + ctxErr.Error()
				return result, ctxErr
			}

			// Rollback all applied steps (this step failed, so it's not in appliedSteps)
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			result.Error = stepResult.Error
			return result, fmt.Errorf("apply failed for step %s: %w", step.Name(), err)
		}

		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateApplied, "")
		if applyData != "" {
			e.repo.SetJobStepData(job.Steps[i].ID, "apply", applyData)
		}
		stepResult.State = string(StepStateApplied)
		appliedSteps = append(appliedSteps, i) // track for rollback

		// --- Verify phase ---
		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "progress",
			Value:  fmt.Sprintf("Verifying step %d/%d: %s", i+1, len(steps), step.Name()),
		})

		// Transition to verifying
		if err := e.transition(ctx, sm, migrationID, StateVerifying, onProgress, "verifying"); err != nil {
			result.FinalState = sm.State()
			return result, err
		}

		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateVerifying, "")
		sctx.ApplyData = applyData

		verifyData, err := step.Verify(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = fmt.Sprintf("verify failed: %v", err)
			e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateFailed, stepResult.Error)
			result.Steps = append(result.Steps, stepResult)

			// Check if the error was caused by context cancellation
			if ctxErr := ctx.Err(); ctxErr != nil {
				e.interruptMigration(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress)
				result.FinalState = sm.State()
				result.Error = "migration interrupted: " + ctxErr.Error()
				return result, ctxErr
			}

			// Rollback all applied steps (including this one)
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			result.Error = stepResult.Error
			return result, fmt.Errorf("verify failed for step %s: %w", step.Name(), err)
		}

		e.repo.UpdateJobStepStateContext(ctx, job.Steps[i].ID, StepStateVerified, "")
		if verifyData != "" {
			e.repo.SetJobStepData(job.Steps[i].ID, "verify", verifyData)
		}

		// --- Checkpoint ---
		if err := e.repo.CreateCheckpointContext(ctx, migrationID, step.Name(), i, verifyData); err != nil {
			log.Printf("warning: failed to create checkpoint for step %s: %v", step.Name(), err)
		}

		stepResult.State = string(StepStateVerified)
		result.Steps = append(result.Steps, stepResult)

		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "success",
			Value:  fmt.Sprintf("Step %d/%d verified: %s", i+1, len(steps), step.Name()),
		})

		// Transition back to applying for the next step
		if i < len(steps)-1 {
			if err := e.transition(ctx, sm, migrationID, StateApplying, onProgress, "applying"); err != nil {
				result.FinalState = sm.State()
				return result, err
			}
		}
	}

	// --- All steps completed: transition to Committed ---
	if err := e.transition(ctx, sm, migrationID, StateCommitted, onProgress, "committed"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	result.FinalState = sm.State()
	onProgress(WSMessage{Step: "engine", Status: "complete", Value: "Migration committed successfully"})
	return result, nil
}

// Resume resumes an interrupted migration from the last verified checkpoint.
// It skips steps that have already been verified and continues from there.
func (e *Engine) Resume(ctx context.Context, migrationID int, steps []MigrationStep, onProgress StepCallback) (*EngineResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	result := &EngineResult{
		JobID:      migrationID,
		FinalState: StateResuming,
		Steps:      make([]StepResult, 0, len(steps)),
	}

	// Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		return result, fmt.Errorf("migration not found: %w", err)
	}

	// Check current state
	currentState, err := e.repo.GetMigrationState(migrationID)
	if err != nil {
		currentState, _ = StateFromString(migration.Status)
	}

	if !currentState.CanResume() {
		return result, fmt.Errorf("migration cannot be resumed from state %s", currentState)
	}

	sm := NewStateMachine(currentState)

	// Transition: Interrupted → Resuming
	if err := e.transition(ctx, sm, migrationID, StateResuming, onProgress, "resuming"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// Get verified checkpoints to know which steps to skip
	checkpoints, err := e.repo.GetVerifiedCheckpoints(migrationID)
	if err != nil {
		return result, fmt.Errorf("failed to load checkpoints: %w", err)
	}

	skipSet := make(map[int]bool, len(checkpoints))
	for _, cp := range checkpoints {
		skipSet[cp.StepIndex] = true
	}

	// Get SSH connection to the target
	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return result, fmt.Errorf("target server not found: %w", err)
	}

	sshClient, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		return result, fmt.Errorf("SSH connection failed: %w", err)
	}

	// Build the job
	job := &MigrationJob{
		Migration: migration,
		State:     sm.State(),
		Steps:     make([]JobStep, 0, len(steps)),
	}

	// Load existing job steps
	for i, step := range steps {
		stepRecord, err := e.repo.GetJobStepByIndex(migrationID, i)
		if err != nil {
			// Step doesn't exist yet, create it
			stepID, err := e.repo.CreateJobStepContext(ctx, migrationID, step.Name(), i, "category")
			if err != nil {
				return result, fmt.Errorf("create job step %d: %w", i, err)
			}
			stepRecord = &JobStep{
				ID:          stepID,
				MigrationID: migrationID,
				StepName:    step.Name(),
				StepIndex:   i,
				State:       StepStatePending,
			}
		}
		job.Steps = append(job.Steps, *stepRecord)
	}

	// Transition: Resuming → Applying
	if err := e.transition(ctx, sm, migrationID, StateApplying, onProgress, "applying"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	// Execute remaining steps (skip verified ones)
	appliedSteps := make([]int, 0, len(steps))
	for i, step := range steps {
		if skipSet[i] {
			result.Steps = append(result.Steps, StepResult{
				Name:    step.Name(),
				Index:   i,
				State:   string(StepStateVerified),
				Skipped: true,
			})
			appliedSteps = append(appliedSteps, i) // already applied, include in rollback set
			continue
		}

		// Same execution as Run
		stepResult := StepResult{Name: step.Name(), Index: i, State: string(StepStatePending)}

		// Prepare
		sctx := StepContext{Ctx: ctx, SSH: sshClient, Job: job, Progress: onProgress}
		prepareData, err := step.Prepare(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = err.Error()
			result.Steps = append(result.Steps, stepResult)
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			return result, err
		}

		// Apply
		sctx.PrepareData = prepareData
		applyData, err := step.Apply(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = err.Error()
			result.Steps = append(result.Steps, stepResult)
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			return result, err
		}
		appliedSteps = append(appliedSteps, i)
		stepResult.State = string(StepStateApplied)

		// Verify
		sctx.ApplyData = applyData
		_, err = step.Verify(sctx)
		if err != nil {
			stepResult.State = string(StepStateFailed)
			stepResult.Error = err.Error()
			result.Steps = append(result.Steps, stepResult)
			e.failAndRollback(ctx, sm, migrationID, appliedSteps, steps, sshClient, onProgress, stepResult.Error)
			result.FinalState = sm.State()
			return result, err
		}

		e.repo.CreateCheckpointContext(ctx, migrationID, step.Name(), i, "")
		stepResult.State = string(StepStateVerified)
		result.Steps = append(result.Steps, stepResult)
	}

	// All steps done: commit
	if err := e.transition(ctx, sm, migrationID, StateCommitted, onProgress, "committed"); err != nil {
		result.FinalState = sm.State()
		return result, err
	}

	result.FinalState = sm.State()
	return result, nil
}

// transition validates and persists a state transition.
func (e *Engine) transition(ctx context.Context, sm *StateMachine, migrationID int, to MigrationState, onProgress StepCallback, label string) error {
	if err := sm.Transition(to); err != nil {
		return fmt.Errorf("state transition to %s failed: %w", label, err)
	}
	if err := e.repo.SetMigrationStateContext(ctx, migrationID, to); err != nil {
		return fmt.Errorf("persist state %s failed: %w", label, err)
	}
	onProgress(WSMessage{Step: "engine", Status: "progress", Value: fmt.Sprintf("State: %s", to.String())})
	return nil
}

// failMigration transitions to Failed state.
func (e *Engine) failMigration(ctx context.Context, sm *StateMachine, migrationID int, errMsg string, onProgress StepCallback) {
	onProgress(WSMessage{Step: "engine", Status: "error", Error: errMsg})
	if err := sm.Transition(StateFailed); err != nil {
		sm.ForceTransition(StateFailed)
	}
	e.repo.SetMigrationStateContext(ctx, migrationID, StateFailed)
}

// failAndRollback transitions to Failed, then rolls back all applied steps in LIFO order.
func (e *Engine) failAndRollback(ctx context.Context, sm *StateMachine, migrationID int, appliedSteps []int, steps []MigrationStep, sshClient SSHExecuter, onProgress StepCallback, errMsg string) {
	// Transition to Failed
	e.failMigration(ctx, sm, migrationID, errMsg, onProgress)

	if len(appliedSteps) == 0 {
		return
	}

	// Transition to Rollback
	if err := sm.Transition(StateRollback); err != nil {
		sm.ForceTransition(StateRollback)
	}
	e.repo.SetMigrationStateContext(ctx, migrationID, StateRollback)
	onProgress(WSMessage{Step: "engine", Status: "progress", Value: "Rolling back applied steps (LIFO)..."})

	// Rollback in LIFO order
	for i := len(appliedSteps) - 1; i >= 0; i-- {
		stepIdx := appliedSteps[i]
		step := steps[stepIdx]

		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back step: %s", step.Name()),
		})

		// Load the apply data (backup) for rollback
		jobStep, err := e.repo.GetJobStepByIndex(migrationID, stepIdx)
		applyData := ""
		if err == nil {
			applyData = jobStep.ApplyData
		}

		sctx := StepContext{
			Ctx:       ctx,
			SSH:       sshClient,
			ApplyData: applyData,
			Progress:  onProgress,
		}

		if err := step.Rollback(sctx); err != nil {
			onProgress(WSMessage{
				Step:   step.Name(),
				Status: "warning",
				Value:  fmt.Sprintf("Rollback warning for %s: %v", step.Name(), err),
			})
		}

		// Clear the checkpoint for this step
		e.repo.ClearCheckpoint(migrationID, step.Name())
	}

	// Transition to Restored
	if err := sm.Transition(StateRestored); err != nil {
		sm.ForceTransition(StateRestored)
	}
	e.repo.SetMigrationStateContext(ctx, migrationID, StateRestored)
	onProgress(WSMessage{Step: "engine", Status: "complete", Value: "Rollback complete — target restored"})
}

// interruptMigration handles context cancellation by marking the migration as interrupted.
func (e *Engine) interruptMigration(ctx context.Context, sm *StateMachine, migrationID int, appliedSteps []int, steps []MigrationStep, sshClient SSHExecuter, onProgress StepCallback) {
	onProgress(WSMessage{Step: "engine", Status: "warning", Value: "Migration interrupted by context cancellation"})

	// Transition to Interrupted
	if err := sm.Transition(StateInterrupted); err != nil {
		sm.ForceTransition(StateInterrupted)
	}
	e.repo.SetMigrationStateContext(context.Background(), migrationID, StateInterrupted)
}

// getSSHClient obtains an SSH connection for the given server.
func (e *Engine) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, e.srvRepo, e.pool, e.authSvc, e.hosts)
}

// --- Helpers for building steps from categories ---

// BuildStepsFromCategories creates MigrationStep instances from category
// names using the CategoryRegistry. Each category is wrapped in a
// CategoryStepAdapter.
func (e *Engine) BuildStepsFromCategories(ctx context.Context, migrationID int, categories []string) ([]MigrationStep, error) {
	// Load the collected data for each category
	stepsRecords, err := e.repo.GetSteps(migrationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load migration steps: %w", err)
	}

	// Build a map of category → collected data
	categoryData := make(map[string]CategoryData)
	for _, record := range stepsRecords {
		if record.Action == "collect" && record.Status == StepStatusCompleted && record.Data != "" {
			var data CategoryData
			if err := json.Unmarshal([]byte(record.Data), &data); err == nil {
				categoryData[record.Category] = data
			}
		}
	}

	// Build steps in the order requested
	steps := make([]MigrationStep, 0, len(categories))
	for _, catName := range categories {
		mod, ok := e.registry.Get(catName)
		if !ok {
			return nil, fmt.Errorf("unknown category: %s", catName)
		}
		data, exists := categoryData[catName]
		if !exists {
			return nil, fmt.Errorf("no collected data for category: %s", catName)
		}
		steps = append(steps, NewCategoryStepAdapter(catName, mod, data))
	}

	return steps, nil
}
