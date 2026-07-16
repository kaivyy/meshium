package migration

import "context"

// CompositeRunner delegates to Planner, Executor, and RollbackManager.
type CompositeRunner struct {
	planner  *Planner
	executor *Executor
	rollback *RollbackManager
}

// NewCompositeRunner creates a CompositeRunner.
func NewCompositeRunner(planner *Planner, executor *Executor, rollback *RollbackManager) *CompositeRunner {
	return &CompositeRunner{planner: planner, executor: executor, rollback: rollback}
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

// Parity computes the per-item compare for one migration (Phase 6B2).
func (r *CompositeRunner) Parity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.ComputeParity(ctx, migrationID, onProgress)
}

// ParitySummary computes the post-apply verification report (6B5).
func (r *CompositeRunner) ParitySummary(ctx context.Context, migrationID int) (*ParitySummary, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	parity, err := pe.ComputeParity(ctx, migrationID, nil)
	if err != nil {
		return nil, err
	}
	steps, err := r.executor.Repo().GetSteps(migrationID)
	if err != nil {
		return nil, err
	}
	return pe.ComputeParitySummary(ctx, migrationID, parity, steps)
}

// BulkApply runs a 6B6 progressive-automation sweep over the migration's items.
func (r *CompositeRunner) BulkApply(ctx context.Context, migrationID int, policy BulkPolicy) (*BulkResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.BulkApply(ctx, migrationID, policy)
}

// RecomputeParity re-runs the live compare for one migration (POST parity/recompute).
func (r *CompositeRunner) RecomputeParity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	pe := NewParityEngine(r.executor.Repo(), r.executor.registry, r.executor.srvRepo, r.executor.pool, r.executor.authSvc, r.executor.hosts)
	return pe.ComputeParity(ctx, migrationID, onProgress)
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
