package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"meshium/internal/mod/server"
	"strings"
	"time"
)

// Pre-apply stages: discovery, analysis, planning, validation and
// preparation. Nothing here writes migratable state to the target — the last
// of them takes the mandatory backups that make the apply reversible.
// --- Stage Implementations ---

// discoveryStage runs full discovery collectors on source and target.
type discoveryStage struct {
	repo    PipelineRepo
	srvRepo server.Repo
	pool    ConnectionPool
	authSvc AESKeyProvider
	hosts   HostKeyStore
}

func (s *discoveryStage) Name() PipelineStageName { return StageDiscovery }

func (s *discoveryStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "discovery", Status: "progress", Value: "Running discovery on source and target..."})

	// Run basic discovery commands on both source and target
	sourceInfo, _, _, err := pc.SourceSSH.ExecContext(ctx, "uname -a && cat /etc/os-release 2>/dev/null | head -5")
	if err != nil {
		return fmt.Errorf("source discovery failed: %w", err)
	}
	targetInfo, _, _, err := pc.TargetSSH.ExecContext(ctx, "uname -a && cat /etc/os-release 2>/dev/null | head -5")
	if err != nil {
		return fmt.Errorf("target discovery failed: %w", err)
	}

	// Store discovery results as checkpoint
	checkpointData, _ := json.Marshal(map[string]string{
		"source": sourceInfo,
		"target": targetInfo,
	})
	pc.CheckpointData[string(StageDiscovery)] = string(checkpointData)

	pc.OnProgress(WSMessage{Step: "discovery", Status: "success", Value: "Discovery completed"})
	return nil
}

func (s *discoveryStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Discovery has no side effects to roll back
	return nil
}

// analysisStage analyzes the discovery data and builds a dependency graph.
type analysisStage struct {
	repo PipelineRepo
}

func (s *analysisStage) Name() PipelineStageName { return StageAnalysis }

func (s *analysisStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "analysis", Status: "progress", Value: "Analyzing discovery data..."})

	categories := pc.Migration.Categories

	// Build analysis result
	analysis := map[string]interface{}{
		"categories": categories,
		"config":     pc.Config,
	}
	resultData, _ := json.Marshal(analysis)
	pc.CheckpointData[string(StageAnalysis)] = string(resultData)

	pc.OnProgress(WSMessage{Step: "analysis", Status: "success", Value: "Analysis completed"})
	return nil
}

func (s *analysisStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// planningStage collects data from the source server for each category.
type planningStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

func (s *planningStage) Name() PipelineStageName { return StagePlanning }

func (s *planningStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "planning", Status: "progress", Value: "Collecting data from source..."})

	categories := pc.Migration.Categories

	// Collect data for each category from source
	for _, catName := range categories {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		mod, ok := s.registry.Get(catName)
		if !ok {
			pc.OnProgress(WSMessage{Step: "planning", Status: "warning", Value: fmt.Sprintf("Unknown category: %s, skipping", catName)})
			continue
		}

		pc.OnProgress(WSMessage{Step: "planning", Status: "progress", Value: fmt.Sprintf("Collecting %s...", catName)})

		data, err := mod.Collector.Collect(ctx, pc.SourceSSH)
		if err != nil {
			return fmt.Errorf("collect %s failed: %w", catName, err)
		}

		// Store collected data
		rawData, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal %s data: %w", catName, err)
		}
		if _, err := pc.JobRepo.CreateStep(pc.MigrationID, catName, "collect", string(rawData)); err != nil {
			return fmt.Errorf("persist collected %s data: %w", catName, err)
		}

		pc.OnProgress(WSMessage{Step: "planning", Status: "success", Value: fmt.Sprintf("Collected %s", catName)})
	}

	pc.OnProgress(WSMessage{Step: "planning", Status: "success", Value: "Planning completed"})
	return nil
}

func (s *planningStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Planning has no side effects on the target
	return nil
}

// validationStage validates the migration plan and checks for blockers.
type validationStage struct {
	repo PipelineRepo
}

func (s *validationStage) Name() PipelineStageName { return StageValidation }

func (s *validationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "validation", Status: "progress", Value: "Validating migration plan..."})

	// Check SSH connectivity to both servers
	if _, _, _, err := pc.SourceSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("source SSH check failed: %w", err)
	}
	if _, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("target SSH check failed: %w", err)
	}

	// Check target disk space
	output, _, _, err := pc.TargetSSH.ExecContext(ctx, "df -h / | tail -1 | awk '{print $4}'")
	if err == nil {
		pc.OnProgress(WSMessage{Step: "validation", Status: "progress", Value: fmt.Sprintf("Target available disk: %s", output)})
	}

	// Plan freshness. The collected payload is a snapshot of the SOURCE at plan
	// time; applying a stale one writes state the source no longer has. This
	// runs BEFORE preparationStage takes backups, so a refusal costs nothing on
	// the target. Beyond the hard window it blocks — re-planning takes seconds.
	if pc.Migration != nil {
		if fresh := planFreshnessOf(pc.Migration.CreatedAt); fresh.Stale {
			status := "warning"
			if fresh.Blocking {
				status = "error"
			}
			pc.OnProgress(WSMessage{Step: "validation", Status: status, Value: fresh.Reason})
			if fresh.Blocking {
				return fmt.Errorf("stale plan: %s", fresh.Reason)
			}
		}
	}

	pc.OnProgress(WSMessage{Step: "validation", Status: "success", Value: "Validation passed"})
	return nil
}

// planFreshnessOf parses a stored RFC3339 plan timestamp and evaluates it. An
// unparsable value is treated as unknown (warn, never silently fresh).
func planFreshnessOf(createdAt string) PlanFreshness {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		// The repo also stores "2006-01-02 15:04:05" for older rows.
		if t2, err2 := time.Parse("2006-01-02 15:04:05", createdAt); err2 == nil {
			t = t2
		} else {
			return evaluatePlanFreshness(time.Time{}, time.Now())
		}
	}
	return evaluatePlanFreshness(t, time.Now())
}

func (s *validationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// preparationStage creates mandatory backups on the target server.
type preparationStage struct {
	repo     PipelineRepo
	registry *CategoryRegistry
}

func (s *preparationStage) Name() PipelineStageName { return StagePreparation }

func (s *preparationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "preparation", Status: "progress", Value: "Creating backups on target..."})

	categories := pc.Migration.Categories

	// Backup each category on target (MANDATORY — failure is fatal)
	for _, catName := range categories {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		mod, ok := s.registry.Get(catName)
		if !ok {
			continue
		}

		pc.OnProgress(WSMessage{Step: "preparation", Status: "progress", Value: fmt.Sprintf("Backing up %s...", catName)})

		backup, err := mod.Applier.Backup(ctx, pc.TargetSSH)
		if err != nil {
			return fmt.Errorf("backup %s failed (migration aborted — no apply without backup): %w", catName, err)
		}

		// Save backup to DB. Persistence failure is fatal: initialSyncStage
		// rollback restores from these records, so a lost backup means we
		// could apply to the target with no way to restore it.
		rawBackup, err := json.Marshal(backup)
		if err != nil {
			return fmt.Errorf("marshal %s backup failed (migration aborted — no apply without backup): %w", catName, err)
		}
		backupID, err := pc.JobRepo.CreateBackup(pc.MigrationID, pc.Migration.TargetID, catName, string(rawBackup))
		if err != nil {
			return fmt.Errorf("persist %s backup failed (migration aborted — no apply without backup): %w", catName, err)
		}
		// Remember the restore point so initialSyncStage can stamp it onto each
		// item it applies (per-item rollback evidence).
		if pc.BackupRefs == nil {
			pc.BackupRefs = map[string]string{}
		}
		pc.BackupRefs[catName] = backupRefFor(backupID)

		pc.OnProgress(WSMessage{Step: "preparation", Status: "success", Value: fmt.Sprintf("Backed up %s", catName)})
	}

	pc.OnProgress(WSMessage{Step: "preparation", Status: "success", Value: "All backups created"})

	// Auto-provision Docker when the docker category is selected and the target
	// lacks it. The compat check flags "source has Docker, target does not" as a
	// blocker, but the whole point of that check is to know what to install — so
	// we provision it here (before initialSyncStage applies docker state). The
	// installer is idempotent (no-op if already present) and we only attempt it
	// when docker is an explicit migration category, so we never surprise a
	// target that was deliberately chosen without it.
	if containsCategory(categories, "docker") {
		if out, _, _, dErr := pc.TargetSSH.ExecContext(ctx, "which docker 2>/dev/null"); dErr != nil || strings.TrimSpace(out) == "" {
			pc.OnProgress(WSMessage{Step: "preparation", Status: "progress", Value: "Docker not found on target — provisioning..."})
			engine := NewProvisionEngine(pc.TargetSSH, pc.Repo)
			if pErr := engine.Provision(ctx, pc.MigrationID, ProvisionConfig{Components: []string{"docker"}}); pErr != nil {
				return fmt.Errorf("auto-provision docker failed: %w", pErr)
			}
			pc.OnProgress(WSMessage{Step: "preparation", Status: "success", Value: "Docker provisioned on target"})
		}
	}

	return nil
}

// containsCategory reports whether the category list includes the given name.
func containsCategory(categories []string, name string) bool {
	for _, c := range categories {
		if c == name {
			return true
		}
	}
	return false
}

func (s *preparationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	// Backups are read-only; nothing to roll back
	return nil
}

// Phase 6C-BE: filterCategoryToApplySet returns a CategoryData containing
// ONLY the items the operator chose to apply (apply_from_source + not blocked),
// so a coarse category Applier (which installs the whole list) never applies a
// keep_target/skip item. Non-selectable sub-fields (user groups/cron/firewall,
// config metadata) are preserved as-is — only the itemized, parity-addressable
// arrays are filtered. Returns the original data unchanged when applySet is empty
// (meaning "apply everything", the backward-compatible default).
//
// Key prefixes mirror parity_engine.go compareCategory exactly so an item's
// ItemKey matches its position in the collected data.
