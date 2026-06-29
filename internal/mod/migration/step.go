package migration

import (
	"context"
	"encoding/json"
	"fmt"
)

// StepContext provides step execution with access to the SSH client,
// the migration job, and a progress callback. It is passed to each
// phase of a MigrationStep.
type StepContext struct {
	Ctx        context.Context
	SSH        SSHExecuter
	Job        *MigrationJob
	Progress   StepCallback
	PrepareData string // data from Prepare phase (for Apply)
	ApplyData   string // data from Apply phase (for Verify)
}

// MigrationStep is the interface that every migration step must implement.
// Each step has four phases:
//
//   - Prepare: validate prerequisites (disk space, connectivity, etc.)
//   - Apply:   make the actual changes on the target server
//   - Verify:  confirm the changes were applied correctly
//   - Rollback: undo the changes made by Apply
//
// The Engine runs Prepare → Apply → Verify for each step in order.
// If any phase fails, Rollback is called for all already-applied steps
// in reverse (LIFO) order.
type MigrationStep interface {
	// Name returns a unique, human-readable name for this step.
	Name() string

	// Prepare validates that the step can be executed. It should check
	// prerequisites (disk space, service availability, etc.) without
	// making any changes. Returns data that will be passed to Apply.
	Prepare(sctx StepContext) (prepareData string, err error)

	// Apply makes the actual changes on the target server. It receives
	// the data returned by Prepare. Returns data that will be passed
	// to Verify.
	Apply(sctx StepContext) (applyData string, err error)

	// Verify confirms that the changes made by Apply are correct.
	// Returns data that will be stored as a checkpoint.
	Verify(sctx StepContext) (verifyData string, err error)

	// Rollback undoes the changes made by Apply. It is called in
	// reverse order (LIFO) when a step fails or when the user cancels.
	Rollback(sctx StepContext) error
}

// BaseStep provides common functionality for MigrationStep implementations.
// Embed this struct to get default no-op implementations of Prepare and
// Verify (which not all steps need), reducing boilerplate.
type BaseStep struct {
	StepName string
}

// Name returns the step name.
func (b *BaseStep) Name() string {
	return b.StepName
}

// Prepare is a no-op by default. Override if the step needs validation.
func (b *BaseStep) Prepare(sctx StepContext) (string, error) {
	return "", nil
}

// Verify is a no-op by default. Override if the step needs verification.
func (b *BaseStep) Verify(sctx StepContext) (string, error) {
	return "", nil
}

// --- CategoryStepAdapter ---

// CategoryStepAdapter bridges the existing Collector/Applier interfaces
// to the new MigrationStep interface. This allows the Engine to use
// existing category modules (packages, configs, services, users, docker)
// without modifying them.
//
// The adapter maps the phases as follows:
//   - Prepare: no-op (the Collector already ran during planning)
//   - Apply:   calls Applier.Backup (mandatory) then Applier.Apply
//   - Verify:  no-op (existing appliers don't have a verify phase)
//   - Rollback: calls Applier.Rollback
type CategoryStepAdapter struct {
	BaseStep
	Category string
	Module   CategoryModule
	Data     CategoryData // collected data from the source
}

// NewCategoryStepAdapter creates a MigrationStep from an existing
// CategoryModule and its collected data.
func NewCategoryStepAdapter(category string, mod CategoryModule, data CategoryData) *CategoryStepAdapter {
	return &CategoryStepAdapter{
		BaseStep: BaseStep{StepName: category},
		Category: category,
		Module:   mod,
		Data:     data,
	}
}

// Apply runs the mandatory backup, then applies the collected data.
// The backup is a hard gate — if it fails, Apply returns an error
// and the Engine will not proceed.
func (a *CategoryStepAdapter) Apply(sctx StepContext) (string, error) {
	if sctx.SSH == nil {
		return "", fmt.Errorf("SSH client is nil for step %s", a.StepName)
	}

	// --- Mandatory backup gate ---
	backup, err := a.Module.Applier.Backup(sctx.Ctx, sctx.SSH)
	if err != nil {
		return "", fmt.Errorf("backup failed for %s (migration aborted — no apply without backup): %w", a.Category, err)
	}

	// Serialize backup data for storage.
	backupJSON, err := json.Marshal(backup)
	if err != nil {
		return "", fmt.Errorf("marshal backup data for %s: %w", a.Category, err)
	}

	// --- Apply the collected data ---
	err = a.Module.Applier.Apply(sctx.Ctx, sctx.SSH, a.Data, func(msg WSMessage) {
		if sctx.Progress != nil {
			msg.Step = a.Category + ":" + msg.Step
			sctx.Progress(msg)
		}
	})
	if err != nil {
		return "", fmt.Errorf("apply failed for %s: %w", a.Category, err)
	}

	// Return the backup data as the apply data — it will be used by
	// Rollback if needed and stored as part of the checkpoint.
	return string(backupJSON), nil
}

// Verify is a no-op for existing category modules. New step
// implementations should override this.
func (a *CategoryStepAdapter) Verify(sctx StepContext) (string, error) {
	return "", nil
}

// Rollback calls the applier's Rollback method using the backup data
// stored during Apply.
func (a *CategoryStepAdapter) Rollback(sctx StepContext) error {
	if sctx.SSH == nil {
		return fmt.Errorf("SSH client is nil for step %s", a.StepName)
	}

	// Parse the backup data from the Apply phase.
	if sctx.ApplyData == "" {
		return fmt.Errorf("no backup data for rollback of %s", a.Category)
	}

	var backup BackupData
	if err := json.Unmarshal([]byte(sctx.ApplyData), &backup); err != nil {
		return fmt.Errorf("unmarshal backup data for %s: %w", a.Category, err)
	}

	return a.Module.Applier.Rollback(sctx.Ctx, sctx.SSH, backup)
}

// --- StepRegistry ---

// StepRegistry holds MigrationStep instances indexed by name.
// The Engine uses this to look up steps when building a job.
type StepRegistry struct {
	steps map[string]MigrationStep
}

// NewStepRegistry creates an empty StepRegistry.
func NewStepRegistry() *StepRegistry {
	return &StepRegistry{steps: make(map[string]MigrationStep)}
}

// Register adds a step to the registry.
func (r *StepRegistry) Register(step MigrationStep) {
	r.steps[step.Name()] = step
}

// Get returns a step by name.
func (r *StepRegistry) Get(name string) (MigrationStep, bool) {
	step, ok := r.steps[name]
	return step, ok
}

// All returns all registered steps.
func (r *StepRegistry) All() []MigrationStep {
	steps := make([]MigrationStep, 0, len(r.steps))
	for _, step := range r.steps {
		steps = append(steps, step)
	}
	return steps
}
