// Package discoverystep implements the DiscoveryStep, which bridges the
// discovery engine (Phase 4) with the migration step interface (Phase 2).
//
// This package exists separately from the discovery package to avoid a
// circular dependency: the migration package imports discovery for
// SystemInfo, and the DiscoveryStep needs migration.MigrationStep.
package discoverystep

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
)

// DiscoveryStep implements migration.MigrationStep for server discovery.
//
// It runs all discovery collectors in parallel, builds a dependency graph,
// and stores the snapshot for use by the migration planner (Phase 5).
//
// Lifecycle:
//   - Prepare: verifies the SSH connection is alive
//   - Apply:   runs all collectors in parallel, builds dependency graph
//   - Verify:  confirms the snapshot is not empty (at least OS info)
//   - Rollback: no-op (discovery does not modify server state)
type DiscoveryStep struct {
	// ServerID is the ID of the server to discover.
	ServerID int
	// Store is the snapshot store for persistence.
	Store discovery.SnapshotStore
	// Collectors is the list of collectors to run.
	// If nil, DefaultCollectors() is used.
	Collectors []discovery.SnapshotCollector
	// runner is the collector runner (set in Prepare).
	runner *discovery.CollectorRunner
	// snapshot is the result of Apply.
	snapshot *discovery.ServerSnapshot
	// graph is the dependency graph built from the snapshot.
	graph *discovery.DependencyGraph
}

// NewDiscoveryStep creates a DiscoveryStep for the given server.
func NewDiscoveryStep(serverID int, store discovery.SnapshotStore) *DiscoveryStep {
	return &DiscoveryStep{
		ServerID: serverID,
		Store:    store,
	}
}

// Name returns the step name.
func (s *DiscoveryStep) Name() string {
	return "discovery"
}

// Prepare verifies that the SSH connection is alive.
func (s *DiscoveryStep) Prepare(sctx migration.StepContext) (string, error) {
	if sctx.SSH == nil {
		return "", fmt.Errorf("SSH executer is nil")
	}
	if !sctx.SSH.IsAlive() {
		return "", fmt.Errorf("SSH connection is not alive")
	}

	// Set up collectors
	collectors := s.Collectors
	if collectors == nil {
		collectors = discovery.DefaultCollectors()
	}
	s.runner = discovery.NewCollectorRunner(collectors...)

	data, _ := json.Marshal(map[string]interface{}{
		"serverId":   s.ServerID,
		"collectors": collectorNames(collectors),
	})
	return string(data), nil
}

// Apply runs all collectors in parallel and builds the dependency graph.
func (s *DiscoveryStep) Apply(sctx migration.StepContext) (string, error) {
	if s.runner == nil {
		return "", fmt.Errorf("Prepare was not called")
	}

	// Run all collectors
	snapshot, err := s.runner.Run(sctx.Ctx, sctx.SSH)
	if err != nil {
		return "", fmt.Errorf("collector run failed: %w", err)
	}
	s.snapshot = snapshot

	// Build dependency graph
	s.graph = discovery.BuildDependencyGraph(snapshot)

	// Send progress if callback is set
	if sctx.Progress != nil {
		containerCount := 0
		if snapshot.Docker != nil {
			containerCount = len(snapshot.Docker.Containers)
		}
		sctx.Progress(migration.WSMessage{
			Step:   "discovery",
			Status: "success",
			Value:  fmt.Sprintf("snapshot: %d containers, %d services, %d databases, %d ports, graph: %d nodes/%d edges, %d errors", containerCount, len(snapshot.Services), len(snapshot.Databases), len(snapshot.NetworkPorts), len(s.graph.Nodes), len(s.graph.Edges), len(snapshot.CollectionErrors)),
		})
	}

	// Store snapshot if store is configured
	if s.Store != nil {
		if err := s.Store.SaveSnapshot(s.ServerID, snapshot); err != nil {
			// Non-fatal: snapshot was collected but couldn't be persisted
			if sctx.Progress != nil {
				sctx.Progress(migration.WSMessage{
					Step:   "discovery",
					Status: "warning",
					Error:  fmt.Sprintf("failed to save snapshot: %v", err),
				})
			}
		}
	}

	data, _ := json.Marshal(map[string]interface{}{
		"capturedAt":   snapshot.CapturedAt,
		"os":           snapshot.OS.Distro,
		"hostname":     snapshot.OS.Hostname,
		"containers":   len(snapshot.Docker.Containers),
		"services":     len(snapshot.Services),
		"databases":    len(snapshot.Databases),
		"graphNodes":   len(s.graph.Nodes),
		"graphEdges":   len(s.graph.Edges),
		"collectionErrors": len(snapshot.CollectionErrors),
	})
	return string(data), nil
}

// Verify confirms the snapshot is not empty (at least OS info).
func (s *DiscoveryStep) Verify(sctx migration.StepContext) (string, error) {
	if s.snapshot == nil {
		return "", fmt.Errorf("snapshot is nil — Apply was not called")
	}

	// At minimum, we should have OS info
	if s.snapshot.OS.Distro == "" && s.snapshot.OS.Hostname == "" {
		return "", fmt.Errorf("snapshot is empty — OS info not collected")
	}

	data, _ := json.Marshal(map[string]interface{}{
		"verified":     true,
		"os":           s.snapshot.OS.Distro,
		"hostname":     s.snapshot.OS.Hostname,
		"graphNodes":   len(s.graph.Nodes),
		"graphEdges":   len(s.graph.Edges),
		"collectionErrors": len(s.snapshot.CollectionErrors),
	})
	return string(data), nil
}

// Rollback is a no-op — discovery does not modify server state.
func (s *DiscoveryStep) Rollback(sctx migration.StepContext) error {
	return nil
}

// --- Accessors ---

// Snapshot returns the snapshot collected during Apply.
func (s *DiscoveryStep) Snapshot() *discovery.ServerSnapshot {
	return s.snapshot
}

// Graph returns the dependency graph built during Apply.
func (s *DiscoveryStep) Graph() *discovery.DependencyGraph {
	return s.graph
}

// --- Helpers ---

func collectorNames(collectors []discovery.SnapshotCollector) []string {
	names := make([]string, len(collectors))
	for i, c := range collectors {
		names[i] = c.Name()
	}
	return names
}

// --- Compile-time interface check ---

var _ migration.MigrationStep = (*DiscoveryStep)(nil)

// --- CompatibilityStep ---

// CompatibilityStep implements migration.MigrationStep for compatibility
// checking between source and target servers.
//
// It requires that DiscoveryStep has already been run for both source and
// target servers. The snapshots are loaded from the snapshot store.
type CompatibilityStep struct {
	SourceID int
	TargetID int
	Store    discovery.SnapshotStore
	// report is the result of Apply.
	report *discovery.CompatibilityReport
}

// NewCompatibilityStep creates a CompatibilityStep.
func NewCompatibilityStep(sourceID, targetID int, store discovery.SnapshotStore) *CompatibilityStep {
	return &CompatibilityStep{
		SourceID: sourceID,
		TargetID: targetID,
		Store:    store,
	}
}

func (s *CompatibilityStep) Name() string { return "compatibility-check" }

func (s *CompatibilityStep) Prepare(sctx migration.StepContext) (string, error) {
	if s.Store == nil {
		return "", fmt.Errorf("snapshot store is nil")
	}
	data, _ := json.Marshal(map[string]interface{}{
		"sourceId": s.SourceID,
		"targetId": s.TargetID,
	})
	return string(data), nil
}

func (s *CompatibilityStep) Apply(sctx migration.StepContext) (string, error) {
	source, err := s.Store.LoadSnapshot(s.SourceID)
	if err != nil {
		return "", fmt.Errorf("load source snapshot: %w", err)
	}
	target, err := s.Store.LoadSnapshot(s.TargetID)
	if err != nil {
		return "", fmt.Errorf("load target snapshot: %w", err)
	}

	s.report = discovery.CheckCompatibility(source, target)

	if sctx.Progress != nil {
		sctx.Progress(migration.WSMessage{
			Step:   "compatibility",
			Status: "success",
			Value:  fmt.Sprintf("compatible=%v warnings=%d blockers=%d", s.report.Compatible, len(s.report.Warnings), len(s.report.Blockers)),
		})
	}

	data, _ := json.Marshal(s.report)
	return string(data), nil
}

func (s *CompatibilityStep) Verify(sctx migration.StepContext) (string, error) {
	if s.report == nil {
		return "", fmt.Errorf("compatibility report is nil — Apply was not called")
	}
	if s.report.HasBlockers() {
		var msgs []string
		for _, b := range s.report.Blockers {
			msgs = append(msgs, b.Message)
		}
		return "", fmt.Errorf("compatibility blockers: %v", msgs)
	}
	data, _ := json.Marshal(map[string]interface{}{
		"verified":  true,
		"warnings":  len(s.report.Warnings),
		"blockers":  len(s.report.Blockers),
	})
	return string(data), nil
}

func (s *CompatibilityStep) Rollback(sctx migration.StepContext) error {
	return nil // No-op
}

var _ migration.MigrationStep = (*CompatibilityStep)(nil)

// --- Timeout helper for context ---

// withTimeout returns a context with the given timeout, or the parent
// context if it already has a deadline.
func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
