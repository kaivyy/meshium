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
