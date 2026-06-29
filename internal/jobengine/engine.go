package jobengine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Engine is the top-level orchestrator for the Job Engine.
// It manages a job queue, worker pool, and real-time progress broadcasting.
//
// The Engine is designed to:
//   - Accept job submissions (migration, discovery, compatibility check)
//   - Queue jobs persistently (SQLite-backed, survives restarts)
//   - Execute jobs via type-specific handlers
//   - Broadcast progress in real-time to subscribers
//   - Support pause/resume/cancel operations
//   - Gracefully shutdown with a configurable timeout
type Engine struct {
	queue    JobQueue
	store    JobStore
	broadcaster ProgressBroadcaster
	shutdown *ShutdownManager

	// handlerFactory creates a handler for a given job.
	// This is set by the caller to wire in the appropriate dependencies
	// (planner, migration engine, SSH clients, etc.)
	handlerFactory HandlerFactory

	// maxWorkers is the maximum number of concurrent job workers.
	// Default: 1 (migrations are heavy and should not run in parallel
	// unless explicitly configured).
	maxWorkers int

	// Internal state
	mu       sync.Mutex
	started  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// HandlerFactory creates a JobHandler for a given job.
// The factory is responsible for wiring in all dependencies (plan store,
// migration engine, SSH clients, snapshot store, etc.) based on the
// job type and request parameters.
type HandlerFactory interface {
	// CreateHandler returns a handler for the given job, or an error
	// if the job type is unknown or dependencies are missing.
	CreateHandler(job *Job) (JobHandler, error)
}

// HandlerFactoryFunc is a function adapter for HandlerFactory.
type HandlerFactoryFunc func(job *Job) (JobHandler, error)

func (f HandlerFactoryFunc) CreateHandler(job *Job) (JobHandler, error) {
	return f(job)
}

// EngineConfig configures the Engine.
type EngineConfig struct {
	Queue          JobQueue
	Store          JobStore
	Broadcaster    ProgressBroadcaster
	HandlerFactory HandlerFactory
	MaxWorkers     int
}

// NewEngine creates a new Engine with the given configuration.
func NewEngine(cfg EngineConfig) *Engine {
	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 1 // default: no parallel migrations
	}

	return &Engine{
		queue:          cfg.Queue,
		store:          cfg.Store,
		broadcaster:    cfg.Broadcaster,
		shutdown:       NewShutdownManager(),
		handlerFactory: cfg.HandlerFactory,
		maxWorkers:     maxWorkers,
		stopCh:         make(chan struct{}),
		cancels:        make(map[string]context.CancelFunc),
	}
}

// Start launches the worker loop. The engine will dequeue jobs from the
// queue and execute them via the appropriate handler.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("engine already started")
	}
	e.started = true
	e.mu.Unlock()

	// Recover any interrupted jobs from a previous run
	if err := e.recoverJobs(ctx); err != nil {
		log.Printf("[engine] Warning: job recovery failed: %v", err)
	}

	for i := 0; i < e.maxWorkers; i++ {
		e.wg.Add(1)
		go e.workerLoop(ctx, i)
	}

	log.Printf("[engine] Started with %d worker(s)", e.maxWorkers)
	return nil
}

// Stop performs graceful shutdown.
// It stops accepting new jobs, waits for running jobs to finish
// (up to 30 seconds), and then force-cancels any remaining jobs.
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	log.Printf("[engine] Stopping — waiting for running jobs (timeout: 30s)")

	// Signal workers to stop
	close(e.stopCh)

	// Wait for running jobs with timeout
	forceCancelled, err := e.shutdown.Shutdown(ctx)
	if err != nil {
		log.Printf("[engine] Shutdown error: %v (force-cancelled %d jobs)", err, forceCancelled)
	} else if forceCancelled > 0 {
		log.Printf("[engine] Force-cancelled %d jobs during shutdown", forceCancelled)
	}

	// Wait for all workers to exit
	e.wg.Wait()

	e.mu.Lock()
	e.started = false
	e.mu.Unlock()

	log.Printf("[engine] Stopped")
	return err
}

// Submit creates a new job from the request and enqueues it.
func (e *Engine) Submit(ctx context.Context, req JobRequest) (*Job, error) {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil, fmt.Errorf("engine not started")
	}
	e.mu.Unlock()

	job := &Job{
		ID:          generateJobID(),
		Type:        req.Type,
		Status:      JobStatusQueued,
		CreatedAt:   time.Now().UTC(),
		PlanID:      req.PlanID,
		MigrationID: req.MigrationID,
		SourceID:    req.SourceID,
		TargetID:    req.TargetID,
	}

	// Save to store first
	if err := e.store.SaveJob(ctx, job); err != nil {
		return nil, fmt.Errorf("save job: %w", err)
	}

	// Enqueue
	if err := e.queue.Enqueue(ctx, job); err != nil {
		// Update status to failed if enqueue fails
		job.Status = JobStatusFailed
		job.Error = fmt.Sprintf("enqueue failed: %v", err)
		_ = e.store.SaveJob(ctx, job)
		return nil, fmt.Errorf("enqueue job: %w", err)
	}

	log.Printf("[engine] Submitted job %s (type=%s)", job.ID, job.Type)
	return job, nil
}

// Cancel cancels a running or queued job.
// If the job is queued, it is removed from the queue.
// If the job is running, its context is cancelled.
func (e *Engine) Cancel(ctx context.Context, jobID string) error {
	job, err := e.store.LoadJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}

	if job.Status.IsTerminal() {
		return fmt.Errorf("job %s is already in terminal state: %s", jobID, job.Status)
	}

	// If queued, remove from queue
	if job.Status == JobStatusQueued {
		if err := e.queue.Remove(ctx, jobID); err != nil {
			log.Printf("[engine] Warning: could not remove job %s from queue: %v", jobID, err)
		}
	}

	// If running, cancel its context
	if job.Status == JobStatusRunning {
		e.cancelMu.Lock()
		cancel, ok := e.cancels[jobID]
		e.cancelMu.Unlock()
		if ok {
			cancel()
		}
	}

	// Update status
	job.Status = JobStatusCancelled
	now := time.Now().UTC()
	job.FinishedAt = &now
	if err := e.store.SaveJob(ctx, job); err != nil {
		return fmt.Errorf("save cancelled job: %w", err)
	}

	// Broadcast final status
	e.broadcaster.Broadcast(jobID, JobProgress{
		CurrentStep: job.Progress.CurrentStep,
		TotalSteps:  job.Progress.TotalSteps,
		Percentage:  job.Progress.Percentage,
	})

	log.Printf("[engine] Cancelled job %s", jobID)
	return nil
}

// Pause pauses a running job. The job's context is cancelled, but
// the job status is set to Paused (not Cancelled). The job can be
// resumed later via Resume().
func (e *Engine) Pause(ctx context.Context, jobID string) error {
	job, err := e.store.LoadJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}

	if job.Status != JobStatusRunning {
		return fmt.Errorf("job %s is not running (status: %s)", jobID, job.Status)
	}

	// Cancel the job's context (this will cause the handler to return)
	e.cancelMu.Lock()
	cancel, ok := e.cancels[jobID]
	e.cancelMu.Unlock()
	if ok {
		cancel()
	}

	// Update status to paused
	job.Status = JobStatusPaused
	if err := e.store.SaveJob(ctx, job); err != nil {
		return fmt.Errorf("save paused job: %w", err)
	}

	log.Printf("[engine] Paused job %s", jobID)
	return nil
}

// Resume resumes a paused job by re-enqueuing it.
func (e *Engine) Resume(ctx context.Context, jobID string) error {
	job, err := e.store.LoadJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}

	if job.Status != JobStatusPaused {
		return fmt.Errorf("job %s is not paused (status: %s)", jobID, job.Status)
	}

	// Re-enqueue the job
	job.Status = JobStatusQueued
	if err := e.store.SaveJob(ctx, job); err != nil {
		return fmt.Errorf("save resumed job: %w", err)
	}

	if err := e.queue.Enqueue(ctx, job); err != nil {
		return fmt.Errorf("enqueue resumed job: %w", err)
	}

	log.Printf("[engine] Resumed job %s", jobID)
	return nil
}

// GetJob retrieves a job by its ID.
func (e *Engine) GetJob(ctx context.Context, jobID string) (*Job, error) {
	return e.store.LoadJob(ctx, jobID)
}

// ListJobs returns jobs matching the given filter.
func (e *Engine) ListJobs(ctx context.Context, filter JobFilter) ([]*Job, error) {
	return e.store.ListJobs(ctx, filter)
}

// SubscribeProgress returns a channel that receives progress updates
// for the given job. The channel is buffered and non-blocking — if the
// subscriber is slow, updates are dropped (not buffered indefinitely).
func (e *Engine) SubscribeProgress(jobID string) chan JobProgress {
	return e.broadcaster.Subscribe(jobID)
}

// UnsubscribeProgress removes a progress subscription.
func (e *Engine) UnsubscribeProgress(jobID string, ch chan JobProgress) {
	e.broadcaster.Unsubscribe(jobID, ch)
}

// GetLogs returns all logs for a job.
func (e *Engine) GetLogs(ctx context.Context, jobID string) ([]JobLog, error) {
	return e.store.GetLogs(ctx, jobID)
}

// --- Internal worker loop ---

// workerLoop continuously dequeues and executes jobs.
func (e *Engine) workerLoop(ctx context.Context, workerID int) {
	defer e.wg.Done()

	for {
		select {
		case <-e.stopCh:
			log.Printf("[worker-%d] Stopping", workerID)
			return
		case <-ctx.Done():
			log.Printf("[worker-%d] Context cancelled", workerID)
			return
		default:
			// Dequeue with a short timeout to avoid busy-waiting
			dequeueCtx, dequeueCancel := context.WithTimeout(ctx, 5*time.Second)
			job, err := e.queue.Dequeue(dequeueCtx)
			dequeueCancel()

			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[worker-%d] Dequeue error: %v", workerID, err)
				time.Sleep(1 * time.Second)
				continue
			}

			if job == nil {
				// Queue empty — wait a bit
				time.Sleep(500 * time.Millisecond)
				continue
			}

			log.Printf("[worker-%d] Executing job %s (type=%s)", workerID, job.ID, job.Type)
			e.executeJob(ctx, job)
		}
	}
}

// executeJob runs a single job to completion.
func (e *Engine) executeJob(parentCtx context.Context, job *Job) {
	// Create a cancellable context for this job
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Register with shutdown manager
	e.shutdown.Register(job.ID, cancel)
	defer e.shutdown.Unregister(job.ID)

	// Register cancel function
	e.cancelMu.Lock()
	e.cancels[job.ID] = cancel
	e.cancelMu.Unlock()
	defer func() {
		e.cancelMu.Lock()
		delete(e.cancels, job.ID)
		e.cancelMu.Unlock()
	}()

	// Update job status to running
	now := time.Now().UTC()
	job.Status = JobStatusRunning
	job.StartedAt = &now
	if err := e.store.SaveJob(ctx, job); err != nil {
		log.Printf("[engine] Error saving job %s as running: %v", job.ID, err)
	}

	// Create handler
	handler, err := e.handlerFactory.CreateHandler(job)
	if err != nil {
		e.failJob(ctx, job, fmt.Sprintf("create handler: %v", err))
		return
	}

	// Progress and log callbacks
	onProgress := func(progress JobProgress) {
		job.Progress = progress
		_ = e.store.UpdateProgress(ctx, job.ID, progress)
		e.broadcaster.Broadcast(job.ID, progress)
	}

	onLog := func(logEntry JobLog) {
		_ = e.store.AppendLog(ctx, job.ID, logEntry)
	}

	// Execute the job
	err = handler.Execute(ctx, job, onProgress, onLog)

	// Update job status based on result
	finishedAt := time.Now().UTC()
	job.FinishedAt = &finishedAt

	if err != nil {
		// Check if the error was caused by context cancellation
		if ctx.Err() != nil {
			// Job was cancelled or paused
			if job.Status == JobStatusPaused {
				// Already set to Paused by Pause()
				_ = e.store.SaveJob(ctx, job)
				return
			}
			job.Status = JobStatusCancelled
			job.Error = "cancelled: " + ctx.Err().Error()
		} else {
			job.Status = JobStatusFailed
			job.Error = err.Error()
		}
	} else {
		job.Status = JobStatusDone
		job.Progress.Percentage = 100
	}

	if err := e.store.SaveJob(ctx, job); err != nil {
		log.Printf("[engine] Error saving final job state for %s: %v", job.ID, err)
	}

	// Broadcast final progress
	e.broadcaster.Broadcast(job.ID, job.Progress)

	// Clean up broadcaster subscribers for this job
	if bc, ok := e.broadcaster.(*DefaultProgressBroadcaster); ok {
		bc.Cleanup(job.ID)
	}

	log.Printf("[engine] Job %s finished: %s", job.ID, job.Status)
}

// failJob marks a job as failed and saves it.
func (e *Engine) failJob(ctx context.Context, job *Job, errMsg string) {
	now := time.Now().UTC()
	job.Status = JobStatusFailed
	job.Error = errMsg
	job.FinishedAt = &now
	_ = e.store.SaveJob(ctx, job)
	log.Printf("[engine] Job %s failed: %s", job.ID, errMsg)
}

// recoverJobs recovers jobs that were interrupted by a previous crash.
// Jobs in "running" or "queued" state from a previous run are marked
// as "paused" so they can be resumed manually.
func (e *Engine) recoverJobs(ctx context.Context) error {
	runningJobs, err := e.store.ListJobs(ctx, JobFilter{Status: JobStatusRunning})
	if err != nil {
		return fmt.Errorf("list running jobs: %w", err)
	}

	for _, job := range runningJobs {
		log.Printf("[engine] Recovering job %s (was running, marking as paused)", job.ID)
		job.Status = JobStatusPaused
		if err := e.store.SaveJob(ctx, job); err != nil {
			log.Printf("[engine] Error recovering job %s: %v", job.ID, err)
		}
	}

	queuedJobs, err := e.store.ListJobs(ctx, JobFilter{Status: JobStatusQueued})
	if err != nil {
		return fmt.Errorf("list queued jobs: %w", err)
	}

	for _, job := range queuedJobs {
		log.Printf("[engine] Recovering job %s (was queued, re-enqueuing)", job.ID)
		// Queued jobs are still in the queue (if SQLite-backed) or
		// can be re-enqueued
	}

	if len(runningJobs) > 0 || len(queuedJobs) > 0 {
		log.Printf("[engine] Recovery: %d running → paused, %d queued", len(runningJobs), len(queuedJobs))
	}

	return nil
}
