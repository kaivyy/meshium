package jobengine

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/planner"
	"meshium/internal/mod/transport"
)

// --- Mock Handler for testing ---

// mockHandler is a configurable test handler that can simulate
// success, failure, delay, and progress reporting.
type mockHandler struct {
	delay     time.Duration
	progress  []JobProgress
	failWith  error
	onExecute func(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog))
}

func (h *mockHandler) Execute(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) error {
	if h.onExecute != nil {
		h.onExecute(ctx, job, onProgress, onLog)
		return nil
	}

	// Report progress steps
	for i, p := range h.progress {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if onProgress != nil {
			onProgress(p)
		}
		if onLog != nil {
			onLog(JobLog{
				Timestamp: time.Now().UTC(),
				Level:     LogLevelInfo,
				Step:      fmt.Sprintf("step-%d", i),
				Message:   fmt.Sprintf("Progress %d/%d", p.CurrentStep, p.TotalSteps),
			})
		}
		if h.delay > 0 {
			time.Sleep(h.delay)
		}
	}

	if h.failWith != nil {
		return h.failWith
	}
	return nil
}

// --- Mock SSH Executer ---

type mockSSHExecuter struct{}

func (m *mockSSHExecuter) Exec(cmd string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockSSHExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockSSHExecuter) IsAlive() bool { return true }
func (m *mockSSHExecuter) Upload(src io.Reader, remotePath string) error {
	return nil
}
func (m *mockSSHExecuter) Download(remotePath string, dst io.Writer) error {
	return nil
}

// Ensure mockSSHExecuter satisfies transport.SSHExecuter
var _ transport.SSHExecuter = (*mockSSHExecuter)(nil)

// --- Test Helpers ---

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := fmt.Sprintf(":memory:")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newTestEngine(t *testing.T, handlerFactory HandlerFactory) (*Engine, *sql.DB) {
	t.Helper()
	database := newTestDB(t)

	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure queue table: %v", err)
	}

	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure store table: %v", err)
	}

	broadcaster := NewDefaultProgressBroadcaster()

	engine := NewEngine(EngineConfig{
		Queue:          queue,
		Store:          store,
		Broadcaster:    broadcaster,
		HandlerFactory: handlerFactory,
		MaxWorkers:     1,
	})

	return engine, database
}

// mockHandlerFactory creates mock handlers for testing.
type mockHandlerFactory struct {
	handler JobHandler
}

func (f *mockHandlerFactory) CreateHandler(job *Job) (JobHandler, error) {
	if f.handler == nil {
		return nil, fmt.Errorf("no handler configured")
	}
	return f.handler, nil
}

// --- Tests ---

func TestSubmitJob_QueuedAndRunning(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
		PlanID: "test-plan-1",
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	if job.ID == "" {
		t.Fatal("job ID is empty")
	}
	if job.Status != JobStatusQueued {
		t.Fatalf("expected status queued, got %s", job.Status)
	}
	if job.Type != JobTypeMigration {
		t.Fatalf("expected type migration, got %s", job.Type)
	}
	if job.PlanID != "test-plan-1" {
		t.Fatalf("expected plan ID test-plan-1, got %s", job.PlanID)
	}
}

func TestSubmitJob_NotStarted(t *testing.T) {
	factory := &mockHandlerFactory{handler: &mockHandler{}}
	engine, _ := newTestEngine(t, factory)

	_, err := engine.Submit(context.Background(), JobRequest{
		Type: JobTypeMigration,
	})
	if err == nil {
		t.Fatal("expected error when submitting to unstarted engine")
	}
}

func TestJobExecution_Success(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 0, TotalSteps: 3, CurrentName: "step-0", Percentage: 0},
			{CurrentStep: 1, TotalSteps: 3, CurrentName: "step-1", Percentage: 33.3},
			{CurrentStep: 2, TotalSteps: 3, CurrentName: "step-2", Percentage: 66.6},
			{CurrentStep: 3, TotalSteps: 3, CurrentName: "step-3", Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
		PlanID: "test-plan-success",
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status.IsTerminal() {
			if loaded.Status != JobStatusDone {
				t.Fatalf("expected done, got %s (error: %s)", loaded.Status, loaded.Error)
			}
			if loaded.Progress.Percentage != 100 {
				t.Fatalf("expected 100%% progress, got %.1f%%", loaded.Progress.Percentage)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not complete within timeout")
}

func TestJobExecution_Failure(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 2, Percentage: 50},
		},
		failWith: fmt.Errorf("simulated failure"),
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status.IsTerminal() {
			if loaded.Status != JobStatusFailed {
				t.Fatalf("expected failed, got %s", loaded.Status)
			}
			if loaded.Error == "" {
				t.Fatal("expected non-empty error message")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not complete within timeout")
}

func TestCancelJob_Queued(t *testing.T) {
	// Create a handler that takes a long time so the first job stays running
	// and the second job stays queued
	handler := &mockHandler{
		delay: 2 * time.Second,
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	// Submit first job (will start running)
	_, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job1: %v", err)
	}

	// Submit second job (will be queued since maxWorkers=1)
	job2, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job2: %v", err)
	}

	// Wait for job1 to start running
	time.Sleep(200 * time.Millisecond)

	// Cancel the queued job
	if err := engine.Cancel(ctx, job2.ID); err != nil {
		t.Fatalf("cancel job2: %v", err)
	}

	loaded, err := engine.GetJob(ctx, job2.ID)
	if err != nil {
		t.Fatalf("get job2: %v", err)
	}
	if loaded.Status != JobStatusCancelled {
		t.Fatalf("expected cancelled, got %s", loaded.Status)
	}
	if loaded.FinishedAt == nil {
		t.Fatal("expected finishedAt to be set")
	}
}

func TestCancelJob_Running(t *testing.T) {
	handler := &mockHandler{
		delay: 5 * time.Second,
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 50},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to start running
	time.Sleep(200 * time.Millisecond)

	// Cancel the running job
	if err := engine.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}

	// Wait for cancellation to take effect
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status == JobStatusCancelled {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job was not cancelled within timeout")
}

func TestProgressBroadcast(t *testing.T) {
	handler := &mockHandler{
		delay: 50 * time.Millisecond,
		progress: []JobProgress{
			{CurrentStep: 0, TotalSteps: 3, CurrentName: "step-0", Percentage: 0},
			{CurrentStep: 1, TotalSteps: 3, CurrentName: "step-1", Percentage: 33.3},
			{CurrentStep: 2, TotalSteps: 3, CurrentName: "step-2", Percentage: 66.6},
			{CurrentStep: 3, TotalSteps: 3, CurrentName: "step-3", Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Subscribe to progress before the job starts
	// Wait a moment for the job to start
	time.Sleep(100 * time.Millisecond)

	// Subscribe to progress
	ch := engine.broadcaster.(*DefaultProgressBroadcaster).Subscribe(job.ID)
	defer engine.broadcaster.(*DefaultProgressBroadcaster).Unsubscribe(job.ID, ch)

	// Collect progress updates with a mutex to avoid data race
	var mu sync.Mutex
	received := make([]JobProgress, 0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for p := range ch {
			mu.Lock()
			received = append(received, p)
			mu.Unlock()
		}
	}()

	// Wait for job to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status.IsTerminal() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Give the channel a moment to deliver remaining messages
	time.Sleep(200 * time.Millisecond)

	// Wait for the goroutine to finish
	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected at least one progress update, got none")
	}
}

func TestGracefulShutdown(t *testing.T) {
	handler := &mockHandler{
		delay: 100 * time.Millisecond,
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}

	// Submit a job
	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to start running (poll until status changes from queued)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status == JobStatusRunning || loaded.Status.IsTerminal() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop the engine (should wait for the running job to finish)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := engine.Stop(shutdownCtx); err != nil {
		t.Fatalf("stop engine: %v", err)
	}

	// Verify the job completed
	loaded, err := engine.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !loaded.Status.IsTerminal() {
		t.Fatalf("expected terminal status after shutdown, got %s", loaded.Status)
	}
}

func TestGracefulShutdown_Empty(t *testing.T) {
	factory := &mockHandlerFactory{handler: &mockHandler{}}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}

	// Stop immediately (no running jobs)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Stop(shutdownCtx); err != nil {
		t.Fatalf("stop engine: %v", err)
	}
}

func TestConcurrentJobs_SequentialExecution(t *testing.T) {
	// With maxWorkers=1, jobs should execute sequentially
	var executionOrder []string
	var mu sync.Mutex

	handler := &mockHandler{
		delay: 100 * time.Millisecond,
		onExecute: func(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) {
			mu.Lock()
			executionOrder = append(executionOrder, job.ID)
			mu.Unlock()

			if onProgress != nil {
				onProgress(JobProgress{CurrentStep: 1, TotalSteps: 1, Percentage: 100})
			}
			if onLog != nil {
				onLog(JobLog{Timestamp: time.Now().UTC(), Level: LogLevelInfo, Step: "test", Message: "done"})
			}
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	// Submit 3 jobs
	jobIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		job, err := engine.Submit(ctx, JobRequest{
			Type: JobTypeMigration,
		})
		if err != nil {
			t.Fatalf("submit job %d: %v", i, err)
		}
		jobIDs[i] = job.ID
	}

	// Wait for all jobs to complete
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for _, id := range jobIDs {
			loaded, err := engine.GetJob(ctx, id)
			if err != nil {
				t.Fatalf("get job: %v", err)
			}
			if !loaded.Status.IsTerminal() {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify all jobs completed
	for _, id := range jobIDs {
		loaded, err := engine.GetJob(ctx, id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status != JobStatusDone {
			t.Fatalf("job %s expected done, got %s", id, loaded.Status)
		}
	}

	// With maxWorkers=1, jobs should have executed sequentially
	// (order should match submission order)
	mu.Lock()
	defer mu.Unlock()
	if len(executionOrder) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(executionOrder))
	}
}

func TestJobLogs(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status.IsTerminal() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Get logs
	logs, err := engine.GetLogs(ctx, job.ID)
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}

	if len(logs) == 0 {
		t.Fatal("expected at least one log entry")
	}

	// Verify log entries have expected fields
	for _, log := range logs {
		if log.Timestamp.IsZero() {
			t.Fatal("log timestamp is zero")
		}
		if log.Level == "" {
			t.Fatal("log level is empty")
		}
	}
}

func TestListJobs(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	// Submit 3 jobs
	for i := 0; i < 3; i++ {
		_, err := engine.Submit(ctx, JobRequest{
			Type: JobTypeMigration,
		})
		if err != nil {
			t.Fatalf("submit job %d: %v", i, err)
		}
	}

	// Wait for all to complete
	time.Sleep(1 * time.Second)

	// List all jobs
	jobs, err := engine.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) < 3 {
		t.Fatalf("expected at least 3 jobs, got %d", len(jobs))
	}

	// List by type
	migrationJobs, err := engine.ListJobs(ctx, JobFilter{Type: JobTypeMigration})
	if err != nil {
		t.Fatalf("list migration jobs: %v", err)
	}
	if len(migrationJobs) < 3 {
		t.Fatalf("expected at least 3 migration jobs, got %d", len(migrationJobs))
	}
}

func TestRecoveryAfterRestart(t *testing.T) {
	database := newTestDB(t)

	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure queue table: %v", err)
	}

	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure store table: %v", err)
	}

	broadcaster := NewDefaultProgressBroadcaster()

	// Create a job that is "running" (simulating a crash mid-execution)
	crashedJob := &Job{
		ID:        "job-crashed-1",
		Type:      JobTypeMigration,
		Status:    JobStatusRunning,
		CreatedAt: time.Now().UTC(),
		PlanID:    "test-plan",
	}
	now := time.Now().UTC()
	crashedJob.StartedAt = &now
	if err := store.SaveJob(context.Background(), crashedJob); err != nil {
		t.Fatalf("save crashed job: %v", err)
	}

	// Create a new engine (simulating restart)
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine := NewEngine(EngineConfig{
		Queue:          queue,
		Store:          store,
		Broadcaster:    broadcaster,
		HandlerFactory: factory,
		MaxWorkers:     1,
	})

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	// The crashed job should have been recovered (marked as paused)
	loaded, err := engine.GetJob(ctx, crashedJob.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if loaded.Status != JobStatusPaused {
		t.Fatalf("expected recovered job to be paused, got %s", loaded.Status)
	}
}

func TestPauseResume(t *testing.T) {
	handler := &mockHandler{
		delay: 5 * time.Second,
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 50},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	job, err := engine.Submit(ctx, JobRequest{
		Type: JobTypeMigration,
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}

	// Wait for job to start running (poll until status changes from queued)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := engine.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if loaded.Status == JobStatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Pause the job
	if err := engine.Pause(ctx, job.ID); err != nil {
		t.Fatalf("pause job: %v", err)
	}

	// Verify it's paused
	loaded, err := engine.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if loaded.Status != JobStatusPaused {
		t.Fatalf("expected paused, got %s", loaded.Status)
	}

	// Resume the job
	if err := engine.Resume(ctx, job.ID); err != nil {
		t.Fatalf("resume job: %v", err)
	}

	// Verify it's queued again
	loaded, err = engine.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if loaded.Status != JobStatusQueued {
		t.Fatalf("expected queued after resume, got %s", loaded.Status)
	}
}

func TestJobStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   JobStatus
		terminal bool
	}{
		{JobStatusQueued, false},
		{JobStatusRunning, false},
		{JobStatusPaused, false},
		{JobStatusDone, true},
		{JobStatusFailed, true},
		{JobStatusCancelled, true},
	}

	for _, tt := range tests {
		if tt.status.IsTerminal() != tt.terminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, tt.status.IsTerminal(), tt.terminal)
		}
	}
}

func TestJobStatus_IsActive(t *testing.T) {
	tests := []struct {
		status JobStatus
		active bool
	}{
		{JobStatusQueued, true},
		{JobStatusRunning, true},
		{JobStatusPaused, true},
		{JobStatusDone, false},
		{JobStatusFailed, false},
		{JobStatusCancelled, false},
	}

	for _, tt := range tests {
		if tt.status.IsActive() != tt.active {
			t.Errorf("IsActive(%s) = %v, want %v", tt.status, tt.status.IsActive(), tt.active)
		}
	}
}

func TestSQLiteJobQueue_EnqueueDequeue(t *testing.T) {
	database := newTestDB(t)
	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	// Enqueue 3 jobs
	jobs := []*Job{
		{ID: "job-1", Type: JobTypeMigration, Status: JobStatusQueued, CreatedAt: time.Now().UTC()},
		{ID: "job-2", Type: JobTypeDiscovery, Status: JobStatusQueued, CreatedAt: time.Now().UTC()},
		{ID: "job-3", Type: JobTypeCompatCheck, Status: JobStatusQueued, CreatedAt: time.Now().UTC()},
	}

	for _, job := range jobs {
		if err := queue.Enqueue(ctx, job); err != nil {
			t.Fatalf("enqueue job %s: %v", job.ID, err)
		}
	}

	// Verify size
	size, err := queue.Size(ctx)
	if err != nil {
		t.Fatalf("queue size: %v", err)
	}
	if size != 3 {
		t.Fatalf("expected size 3, got %d", size)
	}

	// Dequeue and verify FIFO order
	for i := 0; i < 3; i++ {
		job, err := queue.Dequeue(ctx)
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		if job == nil {
			t.Fatal("expected non-nil job")
		}
		if job.ID != jobs[i].ID {
			t.Fatalf("expected %s, got %s (FIFO violated)", jobs[i].ID, job.ID)
		}
	}

	// Queue should be empty
	job, err := queue.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue empty: %v", err)
	}
	if job != nil {
		t.Fatal("expected nil job from empty queue")
	}
}

func TestSQLiteJobQueue_Peek(t *testing.T) {
	database := newTestDB(t)
	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	// Peek on empty queue
	job, err := queue.Peek(ctx)
	if err != nil {
		t.Fatalf("peek empty: %v", err)
	}
	if job != nil {
		t.Fatal("expected nil from empty queue")
	}

	// Enqueue a job
	testJob := &Job{ID: "job-peek", Type: JobTypeMigration, Status: JobStatusQueued, CreatedAt: time.Now().UTC()}
	if err := queue.Enqueue(ctx, testJob); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Peek should return the job without removing it
	job, err = queue.Peek(ctx)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if job == nil || job.ID != "job-peek" {
		t.Fatalf("expected job-peek, got %v", job)
	}

	// Size should still be 1
	size, _ := queue.Size(ctx)
	if size != 1 {
		t.Fatalf("expected size 1 after peek, got %d", size)
	}
}

func TestSQLiteJobQueue_Remove(t *testing.T) {
	database := newTestDB(t)
	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	// Enqueue 2 jobs
	queue.Enqueue(ctx, &Job{ID: "job-1", Type: JobTypeMigration, Status: JobStatusQueued, CreatedAt: time.Now().UTC()})
	queue.Enqueue(ctx, &Job{ID: "job-2", Type: JobTypeMigration, Status: JobStatusQueued, CreatedAt: time.Now().UTC()})

	// Remove job-1
	if err := queue.Remove(ctx, "job-1"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Size should be 1
	size, _ := queue.Size(ctx)
	if size != 1 {
		t.Fatalf("expected size 1 after remove, got %d", size)
	}

	// Dequeue should return job-2
	job, err := queue.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if job.ID != "job-2" {
		t.Fatalf("expected job-2, got %s", job.ID)
	}
}

func TestSQLiteJobStore_SaveAndLoad(t *testing.T) {
	database := newTestDB(t)
	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	now := time.Now().UTC()
	startedAt := now.Add(1 * time.Minute)
	job := &Job{
		ID:        "job-store-1",
		Type:      JobTypeMigration,
		Status:    JobStatusRunning,
		CreatedAt: now,
		StartedAt: &startedAt,
		PlanID:    "plan-123",
		Progress: JobProgress{
			CurrentStep: 2,
			TotalSteps:  5,
			Percentage:  40,
		},
	}

	if err := store.SaveJob(ctx, job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	loaded, err := store.LoadJob(ctx, "job-store-1")
	if err != nil {
		t.Fatalf("load job: %v", err)
	}

	if loaded.ID != job.ID {
		t.Fatalf("expected ID %s, got %s", job.ID, loaded.ID)
	}
	if loaded.Type != job.Type {
		t.Fatalf("expected type %s, got %s", job.Type, loaded.Type)
	}
	if loaded.Status != job.Status {
		t.Fatalf("expected status %s, got %s", job.Status, loaded.Status)
	}
	if loaded.PlanID != job.PlanID {
		t.Fatalf("expected plan ID %s, got %s", job.PlanID, loaded.PlanID)
	}
	if loaded.Progress.CurrentStep != job.Progress.CurrentStep {
		t.Fatalf("expected current step %d, got %d", job.Progress.CurrentStep, loaded.Progress.CurrentStep)
	}
}

func TestSQLiteJobStore_AppendAndGetLogs(t *testing.T) {
	database := newTestDB(t)
	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	// Create a job first
	job := &Job{
		ID:        "job-logs-1",
		Type:      JobTypeMigration,
		Status:    JobStatusRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveJob(ctx, job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	// Append logs
	logs := []JobLog{
		{Timestamp: time.Now().UTC(), Level: LogLevelInfo, Step: "init", Message: "Starting job"},
		{Timestamp: time.Now().UTC(), Level: LogLevelWarn, Step: "config", Message: "Config warning"},
		{Timestamp: time.Now().UTC(), Level: LogLevelError, Step: "db", Message: "DB error"},
	}

	for _, log := range logs {
		if err := store.AppendLog(ctx, job.ID, log); err != nil {
			t.Fatalf("append log: %v", err)
		}
	}

	// Get logs
	retrieved, err := store.GetLogs(ctx, job.ID)
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}

	if len(retrieved) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(retrieved))
	}

	// Verify order (should be by timestamp ascending)
	if retrieved[0].Step != "init" {
		t.Fatalf("expected first log step 'init', got %s", retrieved[0].Step)
	}
	if retrieved[2].Step != "db" {
		t.Fatalf("expected last log step 'db', got %s", retrieved[2].Step)
	}
}

func TestSQLiteJobStore_UpdateProgress(t *testing.T) {
	database := newTestDB(t)
	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	ctx := context.Background()

	// Create a job
	job := &Job{
		ID:        "job-progress-1",
		Type:      JobTypeMigration,
		Status:    JobStatusRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveJob(ctx, job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	// Update progress
	progress := JobProgress{
		CurrentStep: 3,
		TotalSteps:  10,
		Percentage:  30,
		BytesDone:   1024,
		BytesTotal:  10240,
	}
	if err := store.UpdateProgress(ctx, job.ID, progress); err != nil {
		t.Fatalf("update progress: %v", err)
	}

	// Load and verify
	loaded, err := store.LoadJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if loaded.Progress.CurrentStep != 3 {
		t.Fatalf("expected current step 3, got %d", loaded.Progress.CurrentStep)
	}
	if loaded.Progress.BytesDone != 1024 {
		t.Fatalf("expected bytes done 1024, got %d", loaded.Progress.BytesDone)
	}
}

func TestProgressBroadcaster_NonBlocking(t *testing.T) {
	b := NewDefaultProgressBroadcaster()

	// Subscribe with a buffered channel of 16
	ch := b.Subscribe("job-1")

	// Send more than 16 updates — should not block
	for i := 0; i < 100; i++ {
		b.Broadcast("job-1", JobProgress{
			CurrentStep: i,
			TotalSteps:  100,
			Percentage:  float64(i),
		})
	}

	// The channel should have at most 16 messages (buffer size)
	// The rest should have been dropped
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			// No more messages
		}
		if count >= 16 {
			break
		}
	}

	if count > 16 {
		t.Fatalf("expected at most 16 buffered messages, got %d", count)
	}

	b.Cleanup("job-1")
}

func TestProgressBroadcaster_MultipleSubscribers(t *testing.T) {
	b := NewDefaultProgressBroadcaster()

	ch1 := b.Subscribe("job-1")
	ch2 := b.Subscribe("job-1")

	// Broadcast should reach both subscribers
	b.Broadcast("job-1", JobProgress{CurrentStep: 1, TotalSteps: 1, Percentage: 100})

	select {
	case p := <-ch1:
		if p.CurrentStep != 1 {
			t.Fatalf("ch1: expected step 1, got %d", p.CurrentStep)
		}
	default:
		t.Fatal("ch1 did not receive progress")
	}

	select {
	case p := <-ch2:
		if p.CurrentStep != 1 {
			t.Fatalf("ch2: expected step 1, got %d", p.CurrentStep)
		}
	default:
		t.Fatal("ch2 did not receive progress")
	}

	b.Cleanup("job-1")
}

func TestProgressBroadcaster_Cleanup(t *testing.T) {
	b := NewDefaultProgressBroadcaster()

	ch := b.Subscribe("job-1")
	b.Cleanup("job-1")

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after cleanup")
	}
}

func TestShutdownManager_RegisterUnregister(t *testing.T) {
	sm := NewShutdownManager()

	_, cancel := context.WithCancel(context.Background())

	sm.Register("job-1", cancel)
	if sm.RunningCount() != 1 {
		t.Fatalf("expected 1 running, got %d", sm.RunningCount())
	}

	sm.Unregister("job-1")
	if sm.RunningCount() != 0 {
		t.Fatalf("expected 0 running, got %d", sm.RunningCount())
	}
}

func TestShutdownManager_EmptyShutdown(t *testing.T) {
	sm := NewShutdownManagerWithTimeout(1 * time.Second)

	ctx := context.Background()
	force, err := sm.Shutdown(ctx)
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if force != 0 {
		t.Fatalf("expected 0 force-cancelled, got %d", force)
	}
}

func TestSubmitJob_AllTypes(t *testing.T) {
	handler := &mockHandler{
		progress: []JobProgress{
			{CurrentStep: 1, TotalSteps: 1, Percentage: 100},
		},
	}
	factory := &mockHandlerFactory{handler: handler}
	engine, _ := newTestEngine(t, factory)

	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	defer engine.Stop(ctx)

	types := []JobType{JobTypeMigration, JobTypeDiscovery, JobTypeCompatCheck}
	for _, jt := range types {
		job, err := engine.Submit(ctx, JobRequest{Type: jt})
		if err != nil {
			t.Fatalf("submit %s: %v", jt, err)
		}
		if job.Type != jt {
			t.Fatalf("expected type %s, got %s", jt, job.Type)
		}
	}

	// Wait for all to complete
	time.Sleep(2 * time.Second)

	jobs, err := engine.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) < 3 {
		t.Fatalf("expected at least 3 jobs, got %d", len(jobs))
	}
}

func TestFileBasedDB(t *testing.T) {
	// Test with a file-based SQLite database
	dbPath := fmt.Sprintf("/tmp/meshium-test-%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure queue table: %v", err)
	}

	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure store table: %v", err)
	}

	ctx := context.Background()

	// Enqueue a job
	job := &Job{
		ID:        "job-file-1",
		Type:      JobTypeMigration,
		Status:    JobStatusQueued,
		CreatedAt: time.Now().UTC(),
	}
	if err := queue.Enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Verify size
	size, err := queue.Size(ctx)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size != 1 {
		t.Fatalf("expected size 1, got %d", size)
	}

	// Dequeue
	dequeued, err := queue.Dequeue(ctx)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if dequeued.ID != "job-file-1" {
		t.Fatalf("expected job-file-1, got %s", dequeued.ID)
	}
}

// --- Integration tests with real Phase 2-5 types ---

func TestIntegration_PlannerPlanStoreJobEngine(t *testing.T) {
	// This test verifies that the Job Engine can work with the planner
	// package's PlanStore interface
	database := newTestDB(t)

	sqlitePlanStore := planner.NewSQLitePlanStore(database)
	if err := sqlitePlanStore.EnsureTable(); err != nil {
		t.Fatalf("ensure plan store table: %v", err)
	}
	var planStore planner.PlanStore = sqlitePlanStore

	// Create a plan and save it
	plan := &planner.MigrationPlan{
		ID:        "plan-integration-1",
		CreatedAt: time.Now().UTC(),
		Source: planner.ServerSummary{
			Hostname: "source-server",
			OS:       "Ubuntu 22.04",
		},
		Target: planner.ServerSummary{
			Hostname: "target-server",
			OS:       "Ubuntu 24.04",
		},
		Steps:      []planner.PlannedStep{},
		RiskLevel:  planner.RiskLow,
	}
	if err := planStore.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	// Load the plan
	loaded, err := planStore.LoadPlan(context.Background(), "plan-integration-1")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if loaded.ID != "plan-integration-1" {
		t.Fatalf("expected plan-integration-1, got %s", loaded.ID)
	}

	// Verify the plan can be used by a job
	queue := NewSQLiteJobQueue(database)
	if err := queue.EnsureTable(); err != nil {
		t.Fatalf("ensure queue table: %v", err)
	}

	store := NewSQLiteJobStore(database)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("ensure store table: %v", err)
	}

	// Create a job referencing the plan
	job := &Job{
		ID:        "job-integration-1",
		Type:      JobTypeMigration,
		Status:    JobStatusQueued,
		CreatedAt: time.Now().UTC(),
		PlanID:    "plan-integration-1",
	}
	if err := store.SaveJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	loadedJob, err := store.LoadJob(context.Background(), "job-integration-1")
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if loadedJob.PlanID != "plan-integration-1" {
		t.Fatalf("expected plan ID plan-integration-1, got %s", loadedJob.PlanID)
	}
}

func TestIntegration_DiscoverySnapshotStore(t *testing.T) {
	// Test that the Job Engine can work with the discovery package's
	// SnapshotStore interface
	database := newTestDB(t)

	snapshotStore := discovery.NewSQLiteSnapshotStore(database)
	if err := snapshotStore.EnsureTable(); err != nil {
		t.Fatalf("ensure snapshot store table: %v", err)
	}

	// Save a snapshot
	snapshot := &discovery.ServerSnapshot{
		OS: discovery.OSInfo{
			Hostname: "test-server",
			Distro:   "Ubuntu 22.04",
		},
	}
	if err := snapshotStore.SaveSnapshot(1, snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// Load it
	loaded, err := snapshotStore.LoadSnapshot(1)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if loaded.OS.Hostname != "test-server" {
		t.Fatalf("expected hostname test-server, got %s", loaded.OS.Hostname)
	}
}

// --- Compile-time interface checks ---

var _ JobQueue = (*SQLiteJobQueue)(nil)
var _ JobQueue = (*InMemoryJobQueue)(nil)
var _ JobStore = (*SQLiteJobStore)(nil)
var _ JobStore = (*NoopJobStore)(nil)
var _ ProgressBroadcaster = (*DefaultProgressBroadcaster)(nil)
var _ JobHandler = (*MigrationJobHandler)(nil)
var _ JobHandler = (*DiscoveryJobHandler)(nil)
var _ JobHandler = (*CompatCheckJobHandler)(nil)
