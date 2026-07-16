package migration

import (
	"context"
	"sync"
	"time"
)

// parityCacheTTL bounds how long a computed ParityResult is served from cache.
// ComputeParity does a live SSH collect of every category on the target, which
// on a remote/WAN target costs several seconds; the compare page also fires the
// parity + parity-summary endpoints back-to-back, each of which recomputes. A
// short TTL makes a reload (or the second endpoint) instant without letting the
// view go stale: 15s is far shorter than any realistic target drift a human
// would act on, and selections/recompute explicitly invalidate it. ponytail:
// raise only if compare is proven to show stale state in practice.
const parityCacheTTL = 15 * time.Second

// parityResultCache caches ComputeParity output per migration. Keyed by
// migrationID; entries expire after parityCacheTTL. Guarded by a mutex so
// concurrent compare requests share one (expensive) live collect.
type parityResultCache struct {
	mu       sync.Mutex
	entries  map[int]*ParityResult
	expires  map[int]time.Time
}

func newParityResultCache() *parityResultCache {
	return &parityResultCache{
		entries: make(map[int]*ParityResult),
		expires: make(map[int]time.Time),
	}
}

func (c *parityResultCache) get(id int) (*ParityResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.expires[id]
	if !ok || time.Now().After(exp) {
		delete(c.entries, id)
		delete(c.expires, id)
		return nil, false
	}
	return c.entries[id], true
}

func (c *parityResultCache) put(id int, r *ParityResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = r
	c.expires[id] = time.Now().Add(parityCacheTTL)
}

func (c *parityResultCache) invalidate(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
	delete(c.expires, id)
}

// CompositeRunner delegates to Planner, Executor, and RollbackManager.
type CompositeRunner struct {
	planner    *Planner
	executor   *Executor
	rollback   *RollbackManager
	parityCache *parityResultCache
}

// NewCompositeRunner creates a CompositeRunner.
func NewCompositeRunner(planner *Planner, executor *Executor, rollback *RollbackManager) *CompositeRunner {
	return &CompositeRunner{
		planner:    planner,
		executor:   executor,
		rollback:   rollback,
		parityCache: newParityResultCache(),
	}
}

// Plan delegates to the Planner.
func (r *CompositeRunner) Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error) {
	return r.planner.Plan(ctx, req, onProgress)
}

// Execute delegates to the Executor.
func (r *CompositeRunner) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return r.executor.Execute(ctx, migrationID, onProgress)
}

// Rollback delegates to the RollbackManager.
func (r *CompositeRunner) Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return r.rollback.Rollback(ctx, migrationID, onProgress)
}

// PreFlight delegates to the Executor.
func (r *CompositeRunner) PreFlight(ctx context.Context, migrationID int, onProgress StepCallback) (*PreFlightResult, error) {
	return r.executor.PreFlight(ctx, migrationID, onProgress)
}

// DryRun delegates to the Executor.
func (r *CompositeRunner) DryRun(ctx context.Context, migrationID int, onProgress StepCallback) (*DryRunResult, error) {
	return r.executor.DryRun(ctx, migrationID, onProgress)
}

// Diff delegates to the DiffService.
func (r *CompositeRunner) Diff(ctx context.Context, sourceID, targetID int, categories []string, onProgress StepCallback) (*DiffResult, error) {
	ds := NewDiffService(r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return ds.Diff(ctx, sourceID, targetID, categories, onProgress)
}

// InvalidateParity drops any cached compare for a migration so the next
// request re-collects live. Called after selections change or an explicit
// recompute — those are the only operations that make a cached result stale.
func (r *CompositeRunner) InvalidateParity(migrationID int) {
	if r.parityCache != nil {
		r.parityCache.invalidate(migrationID)
	}
}

// Parity computes the per-item compare for one migration (Phase 6B2).
// Results are cached for parityCacheTTL so a page reload (or the
// parity-summary endpoint fired alongside this one) reuses the same live
// collect instead of paying the SSH cost twice.
func (r *CompositeRunner) Parity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	if r.parityCache != nil {
		if cached, ok := r.parityCache.get(migrationID); ok {
			if onProgress != nil {
				onProgress(WSMessage{Step: "parity", Status: "complete", Value: "served from cache"})
			}
			return cached, nil
		}
	}
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	result, err := pe.ComputeParity(ctx, migrationID, onProgress)
	if err != nil {
		return nil, err
	}
	if r.parityCache != nil {
		r.parityCache.put(migrationID, result)
	}
	return result, nil
}

// ParitySummary computes the post-apply verification report (6B5).
// Reuses the cached Parity (Parity populates it), so this does not trigger a
// second live target collect when called back-to-back with Parity.
func (r *CompositeRunner) ParitySummary(ctx context.Context, migrationID int) (*ParitySummary, error) {
	parity, err := r.Parity(ctx, migrationID, nil)
	if err != nil {
		return nil, err
	}
	steps, err := r.executor.Repo().GetSteps(migrationID)
	if err != nil {
		return nil, err
	}
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.ComputeParitySummary(ctx, migrationID, parity, steps)
}

// BulkApply runs a 6B6 progressive-automation sweep over the migration's items.
func (r *CompositeRunner) BulkApply(ctx context.Context, migrationID int, policy BulkPolicy) (*BulkResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.BulkApply(ctx, migrationID, policy)
}

// RecomputeParity re-runs the live compare for one migration (POST parity/recompute).
// Refreshes the cache so the next Parity/ParitySummary read reflects the new
// live collect immediately.
func (r *CompositeRunner) RecomputeParity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	result, err := pe.ComputeParity(ctx, migrationID, onProgress)
	if err != nil {
		return nil, err
	}
	if r.parityCache != nil {
		r.parityCache.put(migrationID, result)
	}
	return result, nil
}

// GetFollowUp returns the items still requiring operator follow-up.
func (r *CompositeRunner) GetFollowUp(ctx context.Context, migrationID int) (*ParityResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.GetFollowUp(ctx, migrationID)
}

// Resume delegates to the Executor.
func (r *CompositeRunner) Resume(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return r.executor.Resume(ctx, migrationID, onProgress)
}

// RecoverInterrupted delegates to the Executor.
func (r *CompositeRunner) RecoverInterrupted() ([]int, error) {
	return r.executor.RecoverInterrupted()
}
