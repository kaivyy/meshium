package migration

import (
	"context"
	"log"
	"sync"
	"time"
)

// DefaultPipelineDrainTimeout is the maximum time GracefulDrain waits for
// active pipelines to reach a safe checkpoint before force-cancelling them.
// It mirrors jobengine.DefaultShutdownTimeout so both execution paths honor
// the same drain window.
const DefaultPipelineDrainTimeout = 30 * time.Second

// pipelineRegistry tracks in-flight pipeline runs so the application shutdown
// lifecycle can drain them. It is the pipeline-side analogue of
// jobengine.ShutdownManager: each running Execute registers its cancel func on
// start and unregisters on finish, and shutdown calls drain() to wait for a
// safe checkpoint (or force-cancel on timeout).
//
// All methods are safe to call on a nil receiver — a Pipeline built without a
// registry (e.g. in unit tests via a struct literal) behaves as if lifecycle
// management is disabled: runs are always accepted and never tracked.
type pipelineRegistry struct {
	mu        sync.Mutex
	active    map[int]context.CancelFunc
	accepting bool
	timeout   time.Duration
}

// newPipelineRegistry creates a registry that accepts new pipelines.
func newPipelineRegistry() *pipelineRegistry {
	return &pipelineRegistry{
		active:    make(map[int]context.CancelFunc),
		accepting: true,
		timeout:   DefaultPipelineDrainTimeout,
	}
}

// register records a running pipeline and its cancel func. It returns false if
// the registry has stopped accepting new pipelines (shutdown in progress), in
// which case the caller must not start execution. A nil registry always
// accepts and tracks nothing.
func (r *pipelineRegistry) register(migrationID int, cancel context.CancelFunc) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting {
		return false
	}
	r.active[migrationID] = cancel
	return true
}

// unregister removes a pipeline from the tracker when it finishes.
func (r *pipelineRegistry) unregister(migrationID int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, migrationID)
}

// count returns the number of currently running pipelines.
func (r *pipelineRegistry) count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// stopAccepting refuses further register() calls. Idempotent.
func (r *pipelineRegistry) stopAccepting() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accepting = false
}

// drain performs graceful shutdown of all active pipelines:
//  1. Stop accepting new pipelines.
//  2. Wait for running pipelines to reach a safe checkpoint (up to timeout).
//  3. If the timeout is reached, cancel remaining pipelines — their Execute
//     loop then calls interruptPipeline, which persists the checkpoint via
//     context.Background() so it survives cancellation.
//
// It returns the number of pipelines that were force-cancelled. A nil registry
// returns immediately.
func (r *pipelineRegistry) drain(ctx context.Context) (forceCancelled int, err error) {
	if r == nil {
		return 0, nil
	}

	r.stopAccepting()

	deadline := time.Now().Add(r.timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if r.count() == 0 {
			log.Printf("[pipeline-shutdown] All pipelines finished gracefully")
			return 0, nil
		}

		if time.Now().After(deadline) {
			r.mu.Lock()
			forceCancelled = len(r.active)
			for migrationID, cancel := range r.active {
				log.Printf("[pipeline-shutdown] Force-cancelling pipeline %d (timeout exceeded)", migrationID)
				cancel()
			}
			r.mu.Unlock()

			// Give cancellations time to propagate so interruptPipeline can
			// persist the checkpoint before the process exits.
			time.Sleep(2 * time.Second)

			if remaining := r.count(); remaining > 0 {
				log.Printf("[pipeline-shutdown] %d pipeline(s) still running after force-cancel", remaining)
			}
			log.Printf("[pipeline-shutdown] Force-cancelled %d pipeline(s)", forceCancelled)
			return forceCancelled, nil
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}
