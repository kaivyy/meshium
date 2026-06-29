package jobengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// JobStore is the interface for persisting and retrieving jobs and their logs.
type JobStore interface {
	// SaveJob creates or updates a job record.
	SaveJob(ctx context.Context, job *Job) error
	// LoadJob retrieves a job by its ID.
	LoadJob(ctx context.Context, jobID string) (*Job, error)
	// ListJobs returns jobs matching the given filter, newest first.
	ListJobs(ctx context.Context, filter JobFilter) ([]*Job, error)
	// AppendLog adds a log entry to a job.
	AppendLog(ctx context.Context, jobID string, log JobLog) error
	// GetLogs returns all logs for a job, ordered by timestamp.
	GetLogs(ctx context.Context, jobID string) ([]JobLog, error)
	// UpdateProgress updates the progress of a running job.
	UpdateProgress(ctx context.Context, jobID string, progress JobProgress) error
}

// SQLiteJobStore stores jobs and logs in SQLite.
type SQLiteJobStore struct {
	db *sql.DB
}

// NewSQLiteJobStore creates a new SQLiteJobStore.
// The caller must call EnsureTable() before using the store.
func NewSQLiteJobStore(db *sql.DB) *SQLiteJobStore {
	return &SQLiteJobStore{db: db}
}

// EnsureTable creates the jobs and job_logs tables if they don't exist.
func (s *SQLiteJobStore) EnsureTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS jobs (
		id          TEXT PRIMARY KEY,
		type        TEXT NOT NULL,
		status      TEXT NOT NULL,
		job         TEXT NOT NULL,
		plan_id     TEXT NOT NULL DEFAULT '',
		migration_id INTEGER NOT NULL DEFAULT 0,
		created_at  DATETIME NOT NULL,
		started_at  DATETIME,
		finished_at DATETIME,
		error       TEXT NOT NULL DEFAULT ''
	);`)
	if err != nil {
		return fmt.Errorf("create jobs table: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_status
		ON jobs(status);`)
	if err != nil {
		return fmt.Errorf("create jobs status index: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_type
		ON jobs(type);`)
	if err != nil {
		return fmt.Errorf("create jobs type index: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_created_at
		ON jobs(created_at DESC);`)
	if err != nil {
		return fmt.Errorf("create jobs created_at index: %w", err)
	}

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS job_logs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id      TEXT NOT NULL,
		timestamp   DATETIME NOT NULL,
		level       TEXT NOT NULL,
		step        TEXT NOT NULL DEFAULT '',
		message     TEXT NOT NULL,
		FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
	);`)
	if err != nil {
		return fmt.Errorf("create job_logs table: %w", err)
	}

	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_job_logs_job_id
		ON job_logs(job_id, timestamp ASC);`)
	if err != nil {
		return fmt.Errorf("create job_logs index: %w", err)
	}

	return nil
}

// SaveJob creates or updates a job record.
func (s *SQLiteJobStore) SaveJob(ctx context.Context, job *Job) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if job.ID == "" {
		return fmt.Errorf("job ID is empty")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, status, job, plan_id, migration_id, created_at, started_at, finished_at, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   type = excluded.type,
		   status = excluded.status,
		   job = excluded.job,
		   plan_id = excluded.plan_id,
		   migration_id = excluded.migration_id,
		   started_at = excluded.started_at,
		   finished_at = excluded.finished_at,
		   error = excluded.error`,
		job.ID, string(job.Type), string(job.Status), string(data),
		job.PlanID, job.MigrationID, job.CreatedAt,
		job.StartedAt, job.FinishedAt, job.Error,
	)
	if err != nil {
		return fmt.Errorf("save job: %w", err)
	}

	return nil
}

// LoadJob retrieves a job by its ID.
func (s *SQLiteJobStore) LoadJob(ctx context.Context, jobID string) (*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT job FROM jobs WHERE id = ?`, jobID,
	).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("job not found: %s", jobID)
		}
		return nil, fmt.Errorf("load job: %w", err)
	}

	var job Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}

	return &job, nil
}

// ListJobs returns jobs matching the given filter, newest first.
func (s *SQLiteJobStore) ListJobs(ctx context.Context, filter JobFilter) ([]*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	query := `SELECT job FROM jobs`
	args := make([]interface{}, 0)
	conditions := make([]string, 0)

	if filter.Type != "" {
		conditions = append(conditions, `type = ?`)
		args = append(args, string(filter.Type))
	}
	if filter.Status != "" {
		conditions = append(conditions, `status = ?`)
		args = append(args, string(filter.Status))
	}

	if len(conditions) > 0 {
		query += " WHERE " + joinConditions(conditions, " AND ")
	}

	query += ` ORDER BY created_at DESC`

	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]*Job, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		var job Job
		if err := json.Unmarshal([]byte(data), &job); err != nil {
			return nil, fmt.Errorf("unmarshal job: %w", err)
		}
		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// AppendLog adds a log entry to a job.
func (s *SQLiteJobStore) AppendLog(ctx context.Context, jobID string, log JobLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ts := log.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_logs (job_id, timestamp, level, step, message)
		 VALUES (?, ?, ?, ?, ?)`,
		jobID, ts, string(log.Level), log.Step, log.Message,
	)
	if err != nil {
		return fmt.Errorf("append log: %w", err)
	}

	return nil
}

// GetLogs returns all logs for a job, ordered by timestamp.
func (s *SQLiteJobStore) GetLogs(ctx context.Context, jobID string) ([]JobLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT timestamp, level, step, message FROM job_logs
		 WHERE job_id = ? ORDER BY timestamp ASC`, jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}
	defer rows.Close()

	logs := make([]JobLog, 0)
	for rows.Next() {
		var log JobLog
		var tsStr string
		var levelStr string
		if err := rows.Scan(&tsStr, &levelStr, &log.Step, &log.Message); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		log.Level = LogLevel(levelStr)
		if t, err := time.Parse("2006-01-02 15:04:05", tsStr); err == nil {
			log.Timestamp = t
		} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			log.Timestamp = t
		} else {
			log.Timestamp = time.Now()
		}
		logs = append(logs, log)
	}

	return logs, nil
}

// UpdateProgress updates the progress of a running job.
func (s *SQLiteJobStore) UpdateProgress(ctx context.Context, jobID string, progress JobProgress) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Load the job, update progress, and save
	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT job FROM jobs WHERE id = ?`, jobID,
	).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job not found: %s", jobID)
		}
		return fmt.Errorf("load job for progress update: %w", err)
	}

	var job Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return fmt.Errorf("unmarshal job: %w", err)
	}

	job.Progress = progress

	updatedData, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE jobs SET job = ? WHERE id = ?`, string(updatedData), jobID,
	)
	if err != nil {
		return fmt.Errorf("update progress: %w", err)
	}

	return nil
}

// joinConditions joins conditions with a separator.
func joinConditions(conditions []string, sep string) string {
	result := ""
	for i, c := range conditions {
		if i > 0 {
			result += sep
		}
		result += c
	}
	return result
}

// --- NoopJobStore ---

// NoopJobStore is a no-op implementation for testing.
type NoopJobStore struct{}

func NewNoopJobStore() *NoopJobStore { return &NoopJobStore{} }

func (s *NoopJobStore) SaveJob(ctx context.Context, job *Job) error { return nil }
func (s *NoopJobStore) LoadJob(ctx context.Context, jobID string) (*Job, error) {
	return nil, fmt.Errorf("job not found (noop store)")
}
func (s *NoopJobStore) ListJobs(ctx context.Context, filter JobFilter) ([]*Job, error) {
	return []*Job{}, nil
}
func (s *NoopJobStore) AppendLog(ctx context.Context, jobID string, log JobLog) error { return nil }
func (s *NoopJobStore) GetLogs(ctx context.Context, jobID string) ([]JobLog, error) {
	return []JobLog{}, nil
}
func (s *NoopJobStore) UpdateProgress(ctx context.Context, jobID string, progress JobProgress) error {
	return nil
}
