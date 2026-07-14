package jobengine

import (
	"context"
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
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
