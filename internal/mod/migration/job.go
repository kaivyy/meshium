package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StepState represents the lifecycle state of a single migration step.
type StepState string

const (
	StepStatePending     StepState = "pending"
	StepStatePreparing   StepState = "preparing"
	StepStatePrepared    StepState = "prepared"
	StepStateApplying    StepState = "applying"
	StepStateApplied     StepState = "applied"
	StepStateVerifying   StepState = "verifying"
	StepStateVerified    StepState = "verified"
	StepStateRollingBack StepState = "rolling_back"
	StepStateRolledBack  StepState = "rolled_back"
	StepStateFailed      StepState = "failed"
)

// JobStep is a persisted step in a migration job. Each step tracks its
// own state independently of the overall migration state, enabling
// fine-grained checkpoint/resume.
type JobStep struct {
	ID          int64     `json:"id"`
	MigrationID int       `json:"migrationId"`
	StepName    string    `json:"stepName"`
	StepIndex   int       `json:"stepIndex"`
	StepType    string    `json:"stepType"` // "category", "docker", "volume", "database", "nginx"
	State       StepState `json:"state"`
	PrepareData string    `json:"prepareData,omitempty"` // JSON data from Prepare
	ApplyData   string    `json:"applyData,omitempty"`   // JSON data from Apply
	VerifyData  string    `json:"verifyData,omitempty"`  // JSON data from Verify
	Error       string    `json:"error,omitempty"`
	StartedAt   string    `json:"startedAt,omitempty"`
	CompletedAt string    `json:"completedAt,omitempty"`
}

// Checkpoint is a persisted checkpoint for a single step. It records
// that a step has been verified and can be skipped on resume.
type Checkpoint struct {
	ID             int64  `json:"id"`
	MigrationID    int    `json:"migrationId"`
	StepName       string `json:"stepName"`
	StepIndex      int    `json:"stepIndex"`
	State          string `json:"state"` // "verified" or "rolled_back"
	CheckpointData string `json:"checkpointData,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// MigrationJob wraps the existing Migration model with typed state
// and step-level checkpoint tracking. It is the in-memory representation
// used by the Engine.
type MigrationJob struct {
	Migration *Migration     `json:"migration"`
	State     MigrationState `json:"state"`
	Steps     []JobStep      `json:"steps"`
}

// JobRepository extends the existing Repo interface with checkpoint
// and step-state management methods needed by the Engine.
type JobRepository interface {
	Repo

	// --- Typed state ---

	// GetMigrationState returns the typed state of a migration.
	GetMigrationState(migrationID int) (MigrationState, error)
	// SetMigrationState updates the typed state of a migration.
	SetMigrationState(migrationID int, state MigrationState) error

	// --- Job steps ---

	// CreateJobStep creates a new step record for a migration job.
	CreateJobStep(migrationID int, stepName string, stepIndex int, stepType string) (int64, error)
	// GetJobSteps returns all steps for a migration, ordered by step index.
	GetJobSteps(migrationID int) ([]JobStep, error)
	// UpdateJobStepState updates the state and error of a job step.
	UpdateJobStepState(stepID int64, state StepState, errMsg string) error
	// SetJobStepData stores the data produced by a step phase (prepare/apply/verify).
	SetJobStepData(stepID int64, phase string, data string) error
	// GetJobStepByIndex returns the step at the given index for a migration.
	GetJobStepByIndex(migrationID int, stepIndex int) (*JobStep, error)

	// --- Checkpoints ---

	// CreateCheckpoint records that a step has been verified.
	CreateCheckpoint(migrationID int, stepName string, stepIndex int, data string) error
	// GetCheckpoints returns all checkpoints for a migration.
	GetCheckpoints(migrationID int) ([]Checkpoint, error)
	// GetVerifiedCheckpoints returns checkpoints with state "verified".
	GetVerifiedCheckpoints(migrationID int) ([]Checkpoint, error)
	// ClearCheckpoint removes a checkpoint for a step (used during rollback).
	ClearCheckpoint(migrationID int, stepName string) error

	// --- Recovery ---

	// GetInterruptedMigrations returns all migrations in an interrupted state.
	GetInterruptedMigrations() ([]Migration, error)

	// --- Context-aware variants (used by Engine) ---

	CreateJobStepContext(ctx context.Context, migrationID int, stepName string, stepIndex int, stepType string) (int64, error)
	UpdateJobStepStateContext(ctx context.Context, stepID int64, state StepState, errMsg string) error
	SetMigrationStateContext(ctx context.Context, migrationID int, state MigrationState) error
	CreateCheckpointContext(ctx context.Context, migrationID int, stepName string, stepIndex int, data string) error
}

// --- sqliteRepo extensions for JobRepository ---

// Ensure sqliteRepo satisfies JobRepository at compile time.
var _ JobRepository = (*sqliteRepo)(nil)

func (r *sqliteRepo) GetMigrationState(migrationID int) (MigrationState, error) {
	var stateStr string
	err := r.db.QueryRow(
		`SELECT COALESCE(state, '') FROM migrations WHERE id = ?`, migrationID,
	).Scan(&stateStr)
	if errors.Is(err, sql.ErrNoRows) {
		return StateCreated, ErrMigrationNotFound
	}
	if err != nil {
		return StateCreated, err
	}
	if stateStr == "" {
		// Fall back to the legacy status column if state is not set.
		var legacyStatus string
		err := r.db.QueryRow(
			`SELECT status FROM migrations WHERE id = ?`, migrationID,
		).Scan(&legacyStatus)
		if err != nil {
			return StateCreated, err
		}
		return StateFromString(legacyStatus)
	}
	return StateFromString(stateStr)
}

func (r *sqliteRepo) SetMigrationState(migrationID int, state MigrationState) error {
	stateStr := state.StateString()
	// Also update the legacy status column for backward compatibility.
	_, err := r.db.Exec(
		`UPDATE migrations SET state = ?, status = ? WHERE id = ?`,
		stateStr, stateStr, migrationID,
	)
	return err
}

func (r *sqliteRepo) CreateJobStep(migrationID int, stepName string, stepIndex int, stepType string) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO migration_job_steps (migration_id, step_name, step_index, step_type, state, started_at)
		 VALUES (?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, step_name) DO UPDATE SET state = 'pending'`,
		migrationID, stepName, stepIndex, stepType,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetJobSteps(migrationID int) ([]JobStep, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, step_name, step_index, step_type, state,
		        prepare_data, apply_data, verify_data, error, started_at, completed_at
		 FROM migration_job_steps WHERE migration_id = ? ORDER BY step_index ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps := make([]JobStep, 0)
	for rows.Next() {
		var s JobStep
		var prepareData, applyData, verifyData, errMsg, startedAt, completedAt sql.NullString
		if err := rows.Scan(
			&s.ID, &s.MigrationID, &s.StepName, &s.StepIndex, &s.StepType, &s.State,
			&prepareData, &applyData, &verifyData, &errMsg, &startedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if prepareData.Valid {
			s.PrepareData = prepareData.String
		}
		if applyData.Valid {
			s.ApplyData = applyData.String
		}
		if verifyData.Valid {
			s.VerifyData = verifyData.String
		}
		if errMsg.Valid {
			s.Error = errMsg.String
		}
		if startedAt.Valid {
			s.StartedAt = startedAt.String
		}
		if completedAt.Valid {
			s.CompletedAt = completedAt.String
		}
		steps = append(steps, s)
	}
	return steps, nil
}

func (r *sqliteRepo) UpdateJobStepState(stepID int64, state StepState, errMsg string) error {
	_, err := r.db.Exec(
		`UPDATE migration_job_steps SET state = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(state), errMsg, stepID,
	)
	return err
}

func (r *sqliteRepo) SetJobStepData(stepID int64, phase string, data string) error {
	var query string
	switch phase {
	case "prepare":
		query = `UPDATE migration_job_steps SET prepare_data = ? WHERE id = ?`
	case "apply":
		query = `UPDATE migration_job_steps SET apply_data = ? WHERE id = ?`
	case "verify":
		query = `UPDATE migration_job_steps SET verify_data = ? WHERE id = ?`
	default:
		return fmt.Errorf("unknown phase: %s", phase)
	}
	_, err := r.db.Exec(query, data, stepID)
	return err
}

func (r *sqliteRepo) GetJobStepByIndex(migrationID int, stepIndex int) (*JobStep, error) {
	var s JobStep
	var prepareData, applyData, verifyData, errMsg, startedAt, completedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, migration_id, step_name, step_index, step_type, state,
		        prepare_data, apply_data, verify_data, error, started_at, completed_at
		 FROM migration_job_steps WHERE migration_id = ? AND step_index = ?`,
		migrationID, stepIndex,
	).Scan(
		&s.ID, &s.MigrationID, &s.StepName, &s.StepIndex, &s.StepType, &s.State,
		&prepareData, &applyData, &verifyData, &errMsg, &startedAt, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job step not found: migration_id=%d step_index=%d", migrationID, stepIndex)
	}
	if err != nil {
		return nil, err
	}
	if prepareData.Valid {
		s.PrepareData = prepareData.String
	}
	if applyData.Valid {
		s.ApplyData = applyData.String
	}
	if verifyData.Valid {
		s.VerifyData = verifyData.String
	}
	if errMsg.Valid {
		s.Error = errMsg.String
	}
	if startedAt.Valid {
		s.StartedAt = startedAt.String
	}
	if completedAt.Valid {
		s.CompletedAt = completedAt.String
	}
	return &s, nil
}

func (r *sqliteRepo) CreateCheckpoint(migrationID int, stepName string, stepIndex int, data string) error {
	_, err := r.db.Exec(
		`INSERT INTO migration_checkpoints (migration_id, step_name, step_index, state, checkpoint_data, updated_at)
		 VALUES (?, ?, ?, 'verified', ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, step_name) DO UPDATE SET state = 'verified', checkpoint_data = ?, updated_at = CURRENT_TIMESTAMP`,
		migrationID, stepName, stepIndex, data, data,
	)
	return err
}

func (r *sqliteRepo) GetCheckpoints(migrationID int) ([]Checkpoint, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, step_name, step_index, state, checkpoint_data, created_at, updated_at
		 FROM migration_checkpoints WHERE migration_id = ? ORDER BY step_index ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	checkpoints := make([]Checkpoint, 0)
	for rows.Next() {
		var c Checkpoint
		var data, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&c.ID, &c.MigrationID, &c.StepName, &c.StepIndex, &c.State, &data, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if data.Valid {
			c.CheckpointData = data.String
		}
		if createdAt.Valid {
			c.CreatedAt = createdAt.String
		}
		if updatedAt.Valid {
			c.UpdatedAt = updatedAt.String
		}
		checkpoints = append(checkpoints, c)
	}
	return checkpoints, nil
}

func (r *sqliteRepo) GetVerifiedCheckpoints(migrationID int) ([]Checkpoint, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, step_name, step_index, state, checkpoint_data, created_at, updated_at
		 FROM migration_checkpoints WHERE migration_id = ? AND state = 'verified' ORDER BY step_index ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	checkpoints := make([]Checkpoint, 0)
	for rows.Next() {
		var c Checkpoint
		var data, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&c.ID, &c.MigrationID, &c.StepName, &c.StepIndex, &c.State, &data, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if data.Valid {
			c.CheckpointData = data.String
		}
		if createdAt.Valid {
			c.CreatedAt = createdAt.String
		}
		if updatedAt.Valid {
			c.UpdatedAt = updatedAt.String
		}
		checkpoints = append(checkpoints, c)
	}
	return checkpoints, nil
}

func (r *sqliteRepo) ClearCheckpoint(migrationID int, stepName string) error {
	_, err := r.db.Exec(
		`UPDATE migration_checkpoints SET state = 'rolled_back', updated_at = CURRENT_TIMESTAMP
		 WHERE migration_id = ? AND step_name = ?`,
		migrationID, stepName,
	)
	return err
}

func (r *sqliteRepo) GetInterruptedMigrations() ([]Migration, error) {
	rows, err := r.db.Query(
		`SELECT id, source_id, target_id, categories, status, error, created_at, completed_at
		 FROM migrations WHERE status = ? OR state = ? ORDER BY created_at DESC`,
		StatusInterrupted, StateInterrupted.StateString(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	migrations := make([]Migration, 0)
	for rows.Next() {
		var m Migration
		var categoriesJSON string
		var errStr, completedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &errStr, &m.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		m.Categories = categoriesJSON
		if errStr.Valid {
			m.Error = errStr.String
		}
		if completedAt.Valid {
			m.CompletedAt = completedAt.String
		}
		migrations = append(migrations, m)
	}
	return migrations, nil
}

// nowRFC3339 returns the current time in RFC3339 format.
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

// --- Context-aware helpers (used by Engine) ---

// CreateJobStepContext is a context-aware variant of CreateJobStep.
func (r *sqliteRepo) CreateJobStepContext(ctx context.Context, migrationID int, stepName string, stepIndex int, stepType string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.CreateJobStep(migrationID, stepName, stepIndex, stepType)
}

// UpdateJobStepStateContext is a context-aware variant of UpdateJobStepState.
func (r *sqliteRepo) UpdateJobStepStateContext(ctx context.Context, stepID int64, state StepState, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.UpdateJobStepState(stepID, state, errMsg)
}

// SetMigrationStateContext is a context-aware variant of SetMigrationState.
func (r *sqliteRepo) SetMigrationStateContext(ctx context.Context, migrationID int, state MigrationState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.SetMigrationState(migrationID, state)
}

// CreateCheckpointContext is a context-aware variant of CreateCheckpoint.
func (r *sqliteRepo) CreateCheckpointContext(ctx context.Context, migrationID int, stepName string, stepIndex int, data string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.CreateCheckpoint(migrationID, stepName, stepIndex, data)
}
