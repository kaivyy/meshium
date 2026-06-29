package jobengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// JobQueue is a persistent FIFO queue for jobs awaiting execution.
// The queue is backed by SQLite so that jobs survive application restarts.
type JobQueue interface {
	// Enqueue adds a job to the back of the queue.
	Enqueue(ctx context.Context, job *Job) error
	// Dequeue removes and returns the job at the front of the queue.
	// Returns nil, nil if the queue is empty.
	Dequeue(ctx context.Context) (*Job, error)
	// Peek returns the job at the front of the queue without removing it.
	// Returns nil, nil if the queue is empty.
	Peek(ctx context.Context) (*Job, error)
	// Size returns the number of jobs currently in the queue.
	Size(ctx context.Context) (int, error)
	// Remove removes a specific job from the queue by ID (used for cancellation).
	Remove(ctx context.Context, jobID string) error
}

// SQLiteJobQueue implements JobQueue using a SQLite table.
// Jobs are stored as JSON with a sequential position for FIFO ordering.
type SQLiteJobQueue struct {
	db *sql.DB
}

// NewSQLiteJobQueue creates a new SQLiteJobQueue.
// The caller must call EnsureTable() before using the queue.
func NewSQLiteJobQueue(db *sql.DB) *SQLiteJobQueue {
	return &SQLiteJobQueue{db: db}
}

// EnsureTable creates the job_queue table if it doesn't exist.
func (q *SQLiteJobQueue) EnsureTable() error {
	_, err := q.db.Exec(`CREATE TABLE IF NOT EXISTS job_queue (
		id          TEXT PRIMARY KEY,
		job         TEXT NOT NULL,
		position    INTEGER NOT NULL,
		queued_at   DATETIME NOT NULL
	);`)
	if err != nil {
		return fmt.Errorf("create job_queue table: %w", err)
	}

	_, err = q.db.Exec(`CREATE INDEX IF NOT EXISTS idx_job_queue_position
		ON job_queue(position ASC);`)
	if err != nil {
		return fmt.Errorf("create job_queue index: %w", err)
	}

	return nil
}

// Enqueue adds a job to the back of the queue.
func (q *SQLiteJobQueue) Enqueue(ctx context.Context, job *Job) error {
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

	// Get the next position
	var maxPos sql.NullInt64
	err = q.db.QueryRowContext(ctx,
		`SELECT MAX(position) FROM job_queue`,
	).Scan(&maxPos)
	if err != nil {
		return fmt.Errorf("get max position: %w", err)
	}

	nextPos := 1
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}

	_, err = q.db.ExecContext(ctx,
		`INSERT INTO job_queue (id, job, position, queued_at) VALUES (?, ?, ?, ?)`,
		job.ID, string(data), nextPos, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}

	return nil
}

// Dequeue removes and returns the job at the front of the queue.
// Returns nil, nil if the queue is empty.
func (q *SQLiteJobQueue) Dequeue(ctx context.Context) (*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var data string
	var id string
	err = tx.QueryRowContext(ctx,
		`SELECT id, job FROM job_queue
		 ORDER BY position ASC LIMIT 1`,
	).Scan(&id, &data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // queue empty
		}
		return nil, fmt.Errorf("dequeue query: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`DELETE FROM job_queue WHERE id = ?`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("dequeue delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dequeue: %w", err)
	}

	var job Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}

	return &job, nil
}

// Peek returns the job at the front of the queue without removing it.
// Returns nil, nil if the queue is empty.
func (q *SQLiteJobQueue) Peek(ctx context.Context) (*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var data string
	err := q.db.QueryRowContext(ctx,
		`SELECT job FROM job_queue ORDER BY position ASC LIMIT 1`,
	).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // queue empty
		}
		return nil, fmt.Errorf("peek query: %w", err)
	}

	var job Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}

	return &job, nil
}

// Size returns the number of jobs currently in the queue.
func (q *SQLiteJobQueue) Size(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	var count int
	err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM job_queue`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("queue size: %w", err)
	}

	return count, nil
}

// Remove removes a specific job from the queue by ID (used for cancellation).
func (q *SQLiteJobQueue) Remove(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	result, err := q.db.ExecContext(ctx,
		`DELETE FROM job_queue WHERE id = ?`, jobID,
	)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job not found in queue: %s", jobID)
	}

	return nil
}

// --- InMemoryJobQueue ---

// InMemoryJobQueue is an in-memory implementation for testing.
type InMemoryJobQueue struct {
	jobs []*Job
}

func NewInMemoryJobQueue() *InMemoryJobQueue {
	return &InMemoryJobQueue{jobs: make([]*Job, 0)}
}

func (q *InMemoryJobQueue) Enqueue(ctx context.Context, job *Job) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	q.jobs = append(q.jobs, job)
	return nil
}

func (q *InMemoryJobQueue) Dequeue(ctx context.Context) (*Job, error) {
	if len(q.jobs) == 0 {
		return nil, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, nil
}

func (q *InMemoryJobQueue) Peek(ctx context.Context) (*Job, error) {
	if len(q.jobs) == 0 {
		return nil, nil
	}
	return q.jobs[0], nil
}

func (q *InMemoryJobQueue) Size(ctx context.Context) (int, error) {
	return len(q.jobs), nil
}

func (q *InMemoryJobQueue) Remove(ctx context.Context, jobID string) error {
	for i, j := range q.jobs {
		if j.ID == jobID {
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("job not found in queue: %s", jobID)
}
