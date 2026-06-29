package jobengine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// DefaultShutdownTimeout is the maximum time to wait for running jobs
// to finish their current step before force-cancelling.
const DefaultShutdownTimeout = 30 * time.Second

// ShutdownManager handles graceful shutdown of the Job Engine.
//
// When Stop() is called:
//  1. Stop accepting new jobs (close the submit channel)
//  2. Let running jobs finish their current step
//  3. After timeout (30s): cancel via context
//  4. State is saved to the JobStore — can be resumed on restart
//     via Phase 2 recovery
type ShutdownManager struct {
	// runningJobs tracks all currently running jobs by ID.
	// Each entry holds the cancel function for that job's context.
	mu          sync.Mutex
	runningJobs map[string]context.CancelFunc
	// timeout is the maximum time to wait for running jobs.
	timeout time.Duration
}

// NewShutdownManager creates a ShutdownManager with the default timeout.
func NewShutdownManager() *ShutdownManager {
	return &ShutdownManager{
		runningJobs: make(map[string]context.CancelFunc),
		timeout:     DefaultShutdownTimeout,
	}
}

// NewShutdownManagerWithTimeout creates a ShutdownManager with a custom timeout.
func NewShutdownManagerWithTimeout(timeout time.Duration) *ShutdownManager {
	return &ShutdownManager{
		runningJobs: make(map[string]context.CancelFunc),
		timeout:     timeout,
	}
}

// Register adds a running job to the shutdown tracker.
// The cancel function will be called if the shutdown timeout is exceeded.
func (sm *ShutdownManager) Register(jobID string, cancel context.CancelFunc) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.runningJobs[jobID] = cancel
}

// Unregister removes a job from the shutdown tracker (called when a job finishes).
func (sm *ShutdownManager) Unregister(jobID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.runningJobs, jobID)
}

// RunningCount returns the number of currently running jobs.
func (sm *ShutdownManager) RunningCount() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.runningJobs)
}

// Shutdown performs graceful shutdown:
//  1. Wait for running jobs to finish (up to timeout)
//  2. If timeout is reached, cancel all remaining jobs
//  3. Return the number of jobs that were force-cancelled
func (sm *ShutdownManager) Shutdown(ctx context.Context) (forceCancelled int, err error) {
	// Give running jobs time to finish
	deadline := time.Now().Add(sm.timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		sm.mu.Lock()
		running := len(sm.runningJobs)
		sm.mu.Unlock()

		if running == 0 {
			log.Printf("[shutdown] All jobs finished gracefully")
			return 0, nil
		}

		if time.Now().After(deadline) {
			// Timeout reached — force cancel all remaining jobs
			sm.mu.Lock()
			forceCancelled = len(sm.runningJobs)
			for jobID, cancel := range sm.runningJobs {
				log.Printf("[shutdown] Force-cancelling job %s (timeout exceeded)", jobID)
				cancel()
			}
			sm.mu.Unlock()

			// Wait a brief moment for cancellations to propagate
			time.Sleep(2 * time.Second)

			sm.mu.Lock()
			remaining := len(sm.runningJobs)
			sm.mu.Unlock()

			if remaining > 0 {
				return forceCancelled, fmt.Errorf("shutdown timeout: %d jobs still running after force-cancel", remaining)
			}

			log.Printf("[shutdown] Force-cancelled %d jobs", forceCancelled)
			return forceCancelled, nil
		}

		// Wait for the next tick or context cancellation
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			// Continue waiting
		}
	}
}
