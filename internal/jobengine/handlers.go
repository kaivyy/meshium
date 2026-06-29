package jobengine

import (
	"context"
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/planner"
	"meshium/internal/mod/transport"
)

// JobHandler executes a specific type of job and reports progress.
// Each handler is responsible for the full lifecycle of its job type:
// setting up dependencies, running the work, and reporting progress.
type JobHandler interface {
	// Execute runs the job to completion (or until the context is cancelled).
	// The progress callback is called with real-time updates.
	Execute(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) error
}

// --- MigrationJobHandler ---

// MigrationJobHandler executes a migration job:
// 1. Load plan from PlanStore (Phase 5)
// 2. Build MigrationSteps via bridge (Phase 5)
// 3. Run via migration.Engine (Phase 2)
// 4. Report progress from migration progress callback
type MigrationJobHandler struct {
	planStore  planner.PlanStore
	engine     *migration.Engine
	sourceSSH  transport.SSHExecuter
	targetSSH  transport.SSHExecuter
}

// NewMigrationJobHandler creates a MigrationJobHandler.
func NewMigrationJobHandler(
	planStore planner.PlanStore,
	engine *migration.Engine,
	sourceSSH, targetSSH transport.SSHExecuter,
) *MigrationJobHandler {
	return &MigrationJobHandler{
		planStore: planStore,
		engine:    engine,
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
	}
}

// Execute runs the migration job.
func (h *MigrationJobHandler) Execute(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) error {
	// 1. Load plan from PlanStore
	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "init",
		Message:   fmt.Sprintf("Loading migration plan %s", job.PlanID),
	})

	plan, err := h.planStore.LoadPlan(ctx, job.PlanID)
	if err != nil {
		return fmt.Errorf("load plan: %w", err)
	}

	if plan.HasBlockers() {
		return fmt.Errorf("plan has %d blockers — cannot execute", len(plan.Blockers))
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "init",
		Message:   fmt.Sprintf("Plan loaded: %d steps, risk=%s", plan.StepCount(), plan.RiskLevel),
	})

	// 2. Build MigrationSteps via bridge
	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "bridge",
		Message:   "Building migration steps from plan",
	})

	steps, err := planner.BuildSteps(plan, h.sourceSSH, h.targetSSH)
	if err != nil {
		return fmt.Errorf("build steps: %w", err)
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "bridge",
		Message:   fmt.Sprintf("Built %d migration steps", len(steps)),
	})

	// 3. Report initial progress
	totalSteps := len(steps)
	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 0,
			TotalSteps:  totalSteps,
			CurrentName: "",
			Percentage:  0,
		})
	}

	// 4. Run via migration.Engine
	stepCallback := func(msg migration.WSMessage) {
		// Parse progress from WSMessage
		if onLog != nil {
			level := LogLevelInfo
			if msg.Status == "error" {
				level = LogLevelError
			}
			onLog(JobLog{
				Timestamp: time.Now().UTC(),
				Level:     level,
				Step:      msg.Step,
				Message:   msg.Value,
			})
		}

		if onProgress != nil {
			// Try to extract step number from message
			// The engine sends messages like "Preparing step 1/5: ..."
			// We parse the current step from the message
			currentStep := 0
			currentName := msg.Step
			percentage := 0.0

			if totalSteps > 0 {
				// Approximate progress based on step name
				// The engine sends progress for each step
				for i, step := range steps {
					if step.Name() == msg.Step {
						currentStep = i + 1
						currentName = step.Name()
						break
					}
				}
				percentage = float64(currentStep) / float64(totalSteps) * 100
			}

			onProgress(JobProgress{
				CurrentStep: currentStep,
				TotalSteps:  totalSteps,
				CurrentName: currentName,
				Percentage:  percentage,
			})
		}
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "engine",
		Message:   fmt.Sprintf("Starting migration engine for migration ID %d", job.MigrationID),
	})

	result, err := h.engine.Run(ctx, job.MigrationID, steps, stepCallback)
	if err != nil {
		onLog(JobLog{
			Timestamp: time.Now().UTC(),
			Level:     LogLevelError,
			Step:      "engine",
			Message:   fmt.Sprintf("Migration failed: %v", err),
		})
		return fmt.Errorf("migration engine: %w", err)
	}

	// Report final progress
	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: totalSteps,
			TotalSteps:  totalSteps,
			CurrentName: "",
			Percentage:  100,
		})
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "engine",
		Message:   fmt.Sprintf("Migration completed: final state=%s, %d steps", result.FinalState, len(result.Steps)),
	})

	return nil
}

// --- DiscoveryJobHandler ---

// DiscoveryJobHandler executes a discovery job:
// 1. SSH to target server
// 2. Run CollectorRunner (Phase 4)
// 3. Save snapshot via SnapshotStore
// 4. Return snapshot ID
type DiscoveryJobHandler struct {
	runner         *discovery.CollectorRunner
	snapshotStore  discovery.SnapshotStore
	sshExecuter    transport.SSHExecuter
	serverID       int
}

// NewDiscoveryJobHandler creates a DiscoveryJobHandler.
func NewDiscoveryJobHandler(
	runner *discovery.CollectorRunner,
	snapshotStore discovery.SnapshotStore,
	sshExecuter transport.SSHExecuter,
	serverID int,
) *DiscoveryJobHandler {
	return &DiscoveryJobHandler{
		runner:        runner,
		snapshotStore: snapshotStore,
		sshExecuter:   sshExecuter,
		serverID:      serverID,
	}
}

// Execute runs the discovery job.
func (h *DiscoveryJobHandler) Execute(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) error {
	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "discovery",
		Message:   fmt.Sprintf("Starting discovery on server %d", h.serverID),
	})

	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 0,
			TotalSteps:  1,
			CurrentName: "discovery",
			Percentage:  0,
		})
	}

	// 1. Run CollectorRunner
	snapshot, err := h.runner.Run(ctx, h.sshExecuter)
	if err != nil {
		return fmt.Errorf("collector runner: %w", err)
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "discovery",
		Message:   "Discovery completed, saving snapshot",
	})

	// 2. Save snapshot
	if h.snapshotStore != nil {
		if err := h.snapshotStore.SaveSnapshot(h.serverID, snapshot); err != nil {
			return fmt.Errorf("save snapshot: %w", err)
		}
	}

	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 1,
			TotalSteps:  1,
			CurrentName: "discovery",
			Percentage:  100,
		})
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "discovery",
		Message:   "Snapshot saved successfully",
	})

	return nil
}

// --- CompatCheckJobHandler ---

// CompatCheckJobHandler executes a compatibility check job:
// 1. Load source + target snapshot from SnapshotStore
// 2. Run CheckCompatibility (Phase 4)
// 3. Return CompatibilityReport
type CompatCheckJobHandler struct {
	snapshotStore discovery.SnapshotStore
	sourceID      int
	targetID      int
}

// NewCompatCheckJobHandler creates a CompatCheckJobHandler.
func NewCompatCheckJobHandler(
	snapshotStore discovery.SnapshotStore,
	sourceID, targetID int,
) *CompatCheckJobHandler {
	return &CompatCheckJobHandler{
		snapshotStore: snapshotStore,
		sourceID:      sourceID,
		targetID:      targetID,
	}
}

// Execute runs the compatibility check job.
func (h *CompatCheckJobHandler) Execute(ctx context.Context, job *Job, onProgress func(JobProgress), onLog func(JobLog)) error {
	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "compat",
		Message:   fmt.Sprintf("Loading snapshots: source=%d, target=%d", h.sourceID, h.targetID),
	})

	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 0,
			TotalSteps:  3,
			CurrentName: "load-snapshots",
			Percentage:  0,
		})
	}

	// 1. Load source snapshot
	sourceSnapshot, err := h.snapshotStore.LoadSnapshot(h.sourceID)
	if err != nil {
		return fmt.Errorf("load source snapshot: %w", err)
	}

	// 2. Load target snapshot
	targetSnapshot, err := h.snapshotStore.LoadSnapshot(h.targetID)
	if err != nil {
		return fmt.Errorf("load target snapshot: %w", err)
	}

	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 2,
			TotalSteps:  3,
			CurrentName: "check-compatibility",
			Percentage:  66.7,
		})
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "compat",
		Message:   "Running compatibility check",
	})

	// 3. Run compatibility check
	report := discovery.CheckCompatibility(sourceSnapshot, targetSnapshot)

	if onProgress != nil {
		onProgress(JobProgress{
			CurrentStep: 3,
			TotalSteps:  3,
			CurrentName: "compat",
			Percentage:  100,
		})
	}

	if report.HasBlockers() {
		for _, b := range report.Blockers {
			onLog(JobLog{
				Timestamp: time.Now().UTC(),
				Level:     LogLevelError,
				Step:      "compat",
				Message:   fmt.Sprintf("Blocker: %s — %s", b.Category, b.Message),
			})
		}
		return fmt.Errorf("compatibility check found %d blockers", len(report.Blockers))
	}

	for _, w := range report.Warnings {
		onLog(JobLog{
			Timestamp: time.Now().UTC(),
			Level:     LogLevelWarn,
			Step:      "compat",
			Message:   fmt.Sprintf("Warning: %s — %s", w.Category, w.Message),
		})
	}

	onLog(JobLog{
		Timestamp: time.Now().UTC(),
		Level:     LogLevelInfo,
		Step:      "compat",
		Message:   "Compatibility check passed (no blockers)",
	})

	return nil
}
