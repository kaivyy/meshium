package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"meshium/internal/mod/server"
)

// RecoveryManager handles migration recovery on application restart.
// It loads interrupted migrations and offers resume or cancel operations.
type RecoveryManager struct {
	repo     JobRepository
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
	registry *CategoryRegistry
	engine   *Engine
}

// NewRecoveryManager creates a RecoveryManager.
func NewRecoveryManager(
	repo JobRepository,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
	registry *CategoryRegistry,
) *RecoveryManager {
	engine := NewEngine(repo, srvRepo, pool, authSvc, hosts, registry)
	return &RecoveryManager{
		repo:     repo,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
		registry: registry,
		engine:   engine,
	}
}

// InterruptedMigration holds an interrupted migration and its recovery options.
type InterruptedMigration struct {
	Migration     *Migration     `json:"migration"`
	State         MigrationState `json:"state"`
	AppliedSteps  []string       `json:"appliedSteps"`
	PendingSteps  []string       `json:"pendingSteps"`
	CanResume     bool           `json:"canResume"`
	CanCancel     bool           `json:"canCancel"`
}

// RecoveryResult holds the result of a recovery operation.
type RecoveryResult struct {
	Action       string         `json:"action"` // "resumed" or "cancelled"
	MigrationID  int            `json:"migrationId"`
	FinalState   MigrationState `json:"finalState"`
	Message      string         `json:"message"`
}

// DiscoverInterrupted loads all migrations in an interrupted state.
// This should be called on application startup to identify migrations
// that were interrupted by a crash or restart.
func (rm *RecoveryManager) DiscoverInterrupted() ([]InterruptedMigration, error) {
	migrations, err := rm.repo.GetInterruptedMigrations()
	if err != nil {
		return nil, fmt.Errorf("failed to load interrupted migrations: %w", err)
	}

	results := make([]InterruptedMigration, 0, len(migrations))
	for i := range migrations {
		m := &migrations[i]
		state, err := rm.repo.GetMigrationState(m.ID)
		if err != nil {
			state, _ = StateFromString(m.Status)
		}

		// Load checkpoints to determine applied steps
		checkpoints, err := rm.repo.GetVerifiedCheckpoints(m.ID)
		if err != nil {
			log.Printf("warning: failed to load checkpoints for migration %d: %v", m.ID, err)
		}

		applied := make([]string, 0, len(checkpoints))
		for _, cp := range checkpoints {
			applied = append(applied, cp.StepName)
		}

		// Parse categories to determine pending steps
		var categories []string
		if err := json.Unmarshal([]byte(m.Categories), &categories); err != nil {
			log.Printf("warning: failed to parse categories for migration %d: %v", m.ID, err)
		}

		appliedSet := make(map[string]bool, len(applied))
		for _, name := range applied {
			appliedSet[name] = true
		}

		pending := make([]string, 0, len(categories))
		for _, cat := range categories {
			if !appliedSet[cat] {
				pending = append(pending, cat)
			}
		}

		results = append(results, InterruptedMigration{
			Migration:    m,
			State:        state,
			AppliedSteps: applied,
			PendingSteps: pending,
			CanResume:    state.CanResume(),
			CanCancel:    true,
		})
	}

	return results, nil
}

// ResumeMigration resumes an interrupted migration from the last verified checkpoint.
// It builds steps from the migration's categories and delegates to the Engine.
func (rm *RecoveryManager) ResumeMigration(ctx context.Context, migrationID int, onProgress StepCallback) (*EngineResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	onProgress(WSMessage{Step: "recovery", Status: "progress", Value: "Resuming interrupted migration..."})

	// Load the migration
	migration, err := rm.repo.GetMigration(migrationID)
	if err != nil {
		return nil, fmt.Errorf("migration not found: %w", err)
	}

	// Verify it's in an interruptible state
	state, err := rm.repo.GetMigrationState(migrationID)
	if err != nil {
		state, _ = StateFromString(migration.Status)
	}

	if !state.CanResume() {
		return nil, fmt.Errorf("migration %d is in state %s, cannot resume", migrationID, state)
	}

	// Build steps from categories
	var categories []string
	if err := json.Unmarshal([]byte(migration.Categories), &categories); err != nil {
		return nil, fmt.Errorf("failed to parse migration categories: %w", err)
	}

	steps, err := rm.engine.BuildStepsFromCategories(ctx, migrationID, categories)
	if err != nil {
		return nil, fmt.Errorf("failed to build steps: %w", err)
	}

	// Delegate to Engine.Resume
	result, err := rm.engine.Resume(ctx, migrationID, steps, onProgress)
	if err != nil {
		onProgress(WSMessage{Step: "recovery", Status: "error", Error: fmt.Sprintf("Resume failed: %v", err)})
		return result, err
	}

	onProgress(WSMessage{Step: "recovery", Status: "success", Value: fmt.Sprintf("Migration resumed, final state: %s", result.FinalState.String())})
	return result, nil
}

// CancelMigration cancels an interrupted migration by rolling back all
// already-applied steps in LIFO order. This is a destructive operation —
// it restores the target to its pre-migration state.
func (rm *RecoveryManager) CancelMigration(ctx context.Context, migrationID int, onProgress StepCallback) (*RecoveryResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	onProgress(WSMessage{Step: "recovery", Status: "progress", Value: "Cancelling interrupted migration..."})

	// Load the migration
	migration, err := rm.repo.GetMigration(migrationID)
	if err != nil {
		return nil, fmt.Errorf("migration not found: %w", err)
	}

	// Load checkpoints to find applied steps
	checkpoints, err := rm.repo.GetVerifiedCheckpoints(migrationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		// No applied steps — just mark as failed
		rm.repo.SetMigrationStateContext(ctx, migrationID, StateFailed)
		onProgress(WSMessage{Step: "recovery", Status: "complete", Value: "No applied steps to roll back — migration cancelled"})
		return &RecoveryResult{
			Action:      "cancelled",
			MigrationID: migrationID,
			FinalState:  StateFailed,
			Message:     "No applied steps to roll back",
		}, nil
	}

	// Get SSH connection to the target
	targetServer, err := rm.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return nil, fmt.Errorf("target server not found: %w", err)
	}

	sshClient, err := getSSHClientForServer(migration.TargetID, targetServer, rm.srvRepo, rm.pool, rm.authSvc, rm.hosts)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	// Build steps from categories for rollback
	var categories []string
	if err := json.Unmarshal([]byte(migration.Categories), &categories); err != nil {
		return nil, fmt.Errorf("failed to parse migration categories: %w", err)
	}

	// Build a map of step name → step for rollback
	stepMap := make(map[string]MigrationStep)
	steps, err := rm.engine.BuildStepsFromCategories(ctx, migrationID, categories)
	if err != nil {
		return nil, fmt.Errorf("failed to build steps for rollback: %w", err)
	}
	for _, step := range steps {
		stepMap[step.Name()] = step
	}

	// Transition to Rollback
	rm.repo.SetMigrationStateContext(ctx, migrationID, StateRollback)
	onProgress(WSMessage{Step: "recovery", Status: "progress", Value: fmt.Sprintf("Rolling back %d applied steps (LIFO)...", len(checkpoints))})

	// Rollback in LIFO order (checkpoints are ordered by step_index ASC, so reverse)
	for i := len(checkpoints) - 1; i >= 0; i-- {
		cp := checkpoints[i]
		step, ok := stepMap[cp.StepName]
		if !ok {
			log.Printf("warning: step %s not found for rollback, skipping", cp.StepName)
			continue
		}

		onProgress(WSMessage{
			Step:   step.Name(),
			Status: "progress",
			Value:  fmt.Sprintf("Rolling back step: %s", step.Name()),
		})

		// Load the apply data (backup) for rollback
		jobSteps, _ := rm.repo.GetJobSteps(migrationID)
		applyData := ""
		for _, js := range jobSteps {
			if js.StepName == cp.StepName {
				applyData = js.ApplyData
				break
			}
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

		// Clear the checkpoint
		rm.repo.ClearCheckpoint(migrationID, step.Name())
	}

	// Transition to Restored
	rm.repo.SetMigrationStateContext(ctx, migrationID, StateRestored)
	onProgress(WSMessage{Step: "recovery", Status: "complete", Value: "Migration cancelled — target restored to pre-migration state"})

	return &RecoveryResult{
		Action:      "cancelled",
		MigrationID: migrationID,
		FinalState:  StateRestored,
		Message:     "All applied steps rolled back",
	}, nil
}

// AutoRecover automatically discovers and handles interrupted migrations.
// By default, it resumes interrupted migrations. If resumeOnly is false,
// it cancels them instead.
func (rm *RecoveryManager) AutoRecover(ctx context.Context, resumeOnly bool, onProgress StepCallback) ([]RecoveryResult, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	interrupted, err := rm.DiscoverInterrupted()
	if err != nil {
		return nil, fmt.Errorf("failed to discover interrupted migrations: %w", err)
	}

	if len(interrupted) == 0 {
		return []RecoveryResult{}, nil
	}

	results := make([]RecoveryResult, 0, len(interrupted))
	for _, im := range interrupted {
		var result *RecoveryResult
		var err error

		if resumeOnly && im.CanResume {
			engineResult, err := rm.ResumeMigration(ctx, im.Migration.ID, onProgress)
			if err != nil {
				result = &RecoveryResult{
					Action:      "resume_failed",
					MigrationID: im.Migration.ID,
					FinalState:  StateFailed,
					Message:     err.Error(),
				}
			} else {
				result = &RecoveryResult{
					Action:      "resumed",
					MigrationID: im.Migration.ID,
					FinalState:  engineResult.FinalState,
					Message:     "Migration resumed successfully",
				}
			}
		} else {
			result, err = rm.CancelMigration(ctx, im.Migration.ID, onProgress)
			if err != nil {
				result = &RecoveryResult{
					Action:      "cancel_failed",
					MigrationID: im.Migration.ID,
					FinalState:  StateFailed,
					Message:     err.Error(),
				}
			}
		}

		results = append(results, *result)
	}

	return results, nil
}
