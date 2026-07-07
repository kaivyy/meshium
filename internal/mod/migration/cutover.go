package migration

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CutoverEngine orchestrates the final cutover from source to target.
// It coordinates replication catch-up, queue drain, health verification,
// traffic switching, and post-cutover observation with automatic rollback.
type CutoverEngine struct {
	replication  *ReplicationEngine
	queue        *QueueEngine
	health       *HealthEngine
	traffic      *TrafficSwitchEngine
	sync         *SyncEngine
	freezeMgr    *FreezeManager
	freezeResult *FreezeResult
	repo         PipelineRepo
}

// NewCutoverEngine creates a new cutover engine.
func NewCutoverEngine(
	replication *ReplicationEngine,
	queue *QueueEngine,
	health *HealthEngine,
	traffic *TrafficSwitchEngine,
	sync *SyncEngine,
	repo PipelineRepo,
	freezeMgr ...*FreezeManager,
) *CutoverEngine {
	var mgr *FreezeManager
	if len(freezeMgr) > 0 {
		mgr = freezeMgr[0]
	}
	return &CutoverEngine{
		replication: replication,
		queue:       queue,
		health:      health,
		traffic:     traffic,
		sync:        sync,
		freezeMgr:   mgr,
		repo:        repo,
	}
}

// CutoverConfig configures the cutover operation.
type CutoverConfig struct {
	MigrationID         int                 `json:"migrationId"`
	FreezeWrite         bool                `json:"freezeWrite"`
	DrainQueues         bool                `json:"drainQueues"`
	ReplicationConfig   ReplicationConfig   `json:"replicationConfig,omitempty"`
	QueueConfigs        []QueueConfig       `json:"queueConfigs,omitempty"`
	TrafficConfig       TrafficSwitchConfig `json:"trafficConfig,omitempty"`
	HealthChecks        []HealthCheckConfig `json:"healthChecks,omitempty"`
	ObservationDuration time.Duration       `json:"observationDuration,omitempty"`
	AutoRollback        bool                `json:"autoRollback"`
	MaxErrorRate        float64             `json:"maxErrorRate,omitempty"`
	MaxLatencyMs        int64               `json:"maxLatencyMs,omitempty"`
}

// Execute runs the cutover pipeline.
// Steps: Freeze → Delta Sync → Replication Catch-Up → Queue Drain →
//
//	Health Verify → Traffic Switch → Observe → Success
//
// On failure: Traffic Back → Replication Rollback → Queue Resume → Rollback
func (e *CutoverEngine) Execute(ctx context.Context, config CutoverConfig) error {
	migrationID := config.MigrationID

	// Record cutover start
	record := CutoverRecord{
		MigrationID: migrationID,
		CutoverType: "full",
	}
	cutoverID, _ := e.repo.CreateCutoverRecord(ctx, record)
	e.freezeResult = nil

	// Step 1: Freeze writes (if configured)
	if config.FreezeWrite {
		if err := e.freezeWrites(ctx, config); err != nil {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("freeze writes failed: %v", err))
			e.rollback(ctx, config)
			return fmt.Errorf("freeze writes: %w", err)
		}
	}

	// Step 2: Final delta sync
	if e.replication != nil {
		if err := e.replication.FinalSync(ctx, config.ReplicationConfig); err != nil {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("final sync failed: %v", err))
			e.rollback(ctx, config)
			return fmt.Errorf("final sync: %w", err)
		}
	}

	// Step 3: Wait for replication catch-up (lag = 0)
	if e.replication != nil {
		if err := e.replication.WaitForCatchUp(ctx, config.ReplicationConfig, 0); err != nil {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("replication catch-up failed: %v", err))
			e.rollback(ctx, config)
			return fmt.Errorf("replication catch-up: %w", err)
		}
	}

	// Step 4: Drain queues (if configured)
	if config.DrainQueues && e.queue != nil {
		for _, qcfg := range config.QueueConfigs {
			if err := e.queue.PauseQueue(ctx, migrationID, qcfg); err != nil {
				e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("pause queue %s failed: %v", qcfg.QueueName, err))
				e.rollback(ctx, config)
				return fmt.Errorf("pause queue %s: %w", qcfg.QueueName, err)
			}
			if err := e.queue.WaitForActiveJobs(ctx, qcfg, 5*time.Minute); err != nil {
				e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("drain queue %s failed: %v", qcfg.QueueName, err))
				e.rollback(ctx, config)
				return fmt.Errorf("drain queue %s: %w", qcfg.QueueName, err)
			}
		}
	}

	// Step 5: Health verify on target
	if e.health != nil && len(config.HealthChecks) > 0 {
		results, score := e.health.CheckAll(ctx, config.HealthChecks)
		if score.ChecksFailed > 0 && score.Score < 80 {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339),
				fmt.Sprintf("health check failed: %d/%d checks failed, score=%.1f", score.ChecksFailed, score.ChecksTotal, score.Score))
			e.rollback(ctx, config)
			return fmt.Errorf("health check failed: %d/%d checks failed, score=%.1f", score.ChecksFailed, score.ChecksTotal, score.Score)
		}
		// Record health check results
		for _, r := range results {
			r.MigrationID = migrationID
			e.repo.CreateHealthCheckResult(ctx, r)
		}
	}

	// Step 6: Traffic switch
	if e.traffic != nil {
		if err := e.traffic.Switch(ctx, migrationID, config.TrafficConfig); err != nil {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("traffic switch failed: %v", err))
			e.rollback(ctx, config)
			return fmt.Errorf("traffic switch: %w", err)
		}
	}

	// Step 7: Promote target database
	if e.replication != nil {
		if err := e.replication.Promote(ctx, migrationID, config.ReplicationConfig); err != nil {
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("promote failed: %v", err))
			e.rollback(ctx, config)
			return fmt.Errorf("promote: %w", err)
		}
	}

	// Step 8: Resume queues on target
	if config.DrainQueues && e.queue != nil {
		for _, qcfg := range config.QueueConfigs {
			e.queue.ResumeQueue(ctx, migrationID, qcfg)
		}
	}

	// Step 9: Verify traffic is flowing
	if e.traffic != nil {
		if err := e.traffic.Verify(ctx, config.TrafficConfig); err != nil {
			if config.AutoRollback {
				e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), fmt.Sprintf("traffic verify failed: %v", err))
				e.rollback(ctx, config)
				return fmt.Errorf("traffic verify: %w", err)
			}
			// Without auto-rollback, traffic is switched and the target is
			// promoted but verification failed — the migration is in an
			// inconsistent, half-cut-over state. Surface a clear error and
			// mark the cutover record as failed so this is not treated as a
			// success; a human must intervene.
			e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339),
				fmt.Sprintf("traffic verify failed, manual intervention required (auto-rollback disabled): %v", err))
			return fmt.Errorf("traffic verify failed, manual intervention required (traffic switched and target promoted, auto-rollback disabled): %w", err)
		}
	}

	// Update cutover record
	e.repo.UpdateCutoverRecord(ctx, cutoverID, time.Now().Format(time.RFC3339), "")
	return nil
}

// Rollback reverts the cutover by switching traffic back, reverting replication,
// and resuming queues on the source.
func (e *CutoverEngine) Rollback(ctx context.Context, migrationID int) error {
	// Get cutover history to understand what needs rollback
	history, _ := e.repo.GetCutoverHistory(migrationID)
	_ = history // used for context

	// Reconstruct the cutover config from persisted state so replication and
	// queue rollback act on the real databases/queues instead of an empty
	// config (which would fail with "unsupported database type" and skip
	// queue resume entirely).
	return e.rollback(ctx, e.loadRollbackConfig(migrationID))
}

// loadRollbackConfig reconstructs a CutoverConfig from persisted replication
// and queue state so that a manually-triggered rollback has the information it
// needs to actually revert replication and resume queues.
func (e *CutoverEngine) loadRollbackConfig(migrationID int) CutoverConfig {
	config := CutoverConfig{MigrationID: migrationID}
	if e == nil || e.repo == nil {
		return config
	}

	if statuses, err := e.repo.GetReplicationStatus(migrationID); err == nil {
		for _, s := range statuses {
			config.ReplicationConfig = ReplicationConfig{
				DatabaseType: s.DatabaseType,
				DatabaseName: s.DatabaseName,
				SourceHost:   s.SourceHost,
				TargetHost:   s.TargetHost,
				MigrationID:  migrationID,
			}
			break
		}
	}

	if states, err := e.repo.GetQueueStates(migrationID); err == nil {
		for _, s := range states {
			config.QueueConfigs = append(config.QueueConfigs, QueueConfig{
				QueueType:   s.QueueType,
				QueueName:   s.QueueName,
				MigrationID: migrationID,
			})
		}
	}

	return config
}

// rollback performs the actual rollback operations.
func (e *CutoverEngine) rollback(ctx context.Context, config CutoverConfig) error {
	var rollbackErr error

	// Record rollback
	rollbackRecord := RollbackRecord{
		MigrationID:  config.MigrationID,
		RollbackType: "cutover",
	}
	rollbackID, _ := e.repo.CreateRollbackRecord(ctx, rollbackRecord)

	// 1. Revert traffic
	if e.traffic != nil {
		if err := e.traffic.Rollback(ctx, config.MigrationID); err != nil && rollbackErr == nil {
			rollbackErr = fmt.Errorf("traffic rollback: %w", err)
		}
	}

	// 2. Revert replication
	if e.replication != nil {
		if err := e.replication.Rollback(ctx, config.MigrationID, config.ReplicationConfig); err != nil && rollbackErr == nil {
			rollbackErr = fmt.Errorf("replication rollback: %w", err)
		}
	}

	// 3. Resume queues on source
	if e.queue != nil {
		for _, qcfg := range config.QueueConfigs {
			e.queue.ResumeQueue(ctx, config.MigrationID, qcfg)
		}
	}

	if err := e.unfreezeWrites(ctx); err != nil && rollbackErr == nil {
		rollbackErr = fmt.Errorf("unfreeze writes: %w", err)
	}

	// Update rollback record
	success := rollbackErr == nil
	e.repo.UpdateRollbackRecord(ctx, rollbackID, success, time.Now().Format(time.RFC3339), "")
	if rollbackErr != nil {
		return rollbackErr
	}
	return nil
}

// unfreezeWrites reverses any freeze operations that were applied during cutover.
func (e *CutoverEngine) unfreezeWrites(ctx context.Context) error {
	if e == nil || e.freezeMgr == nil || e.freezeResult == nil {
		return nil
	}
	return e.freezeMgr.UnfreezeWrites(ctx, e.freezeResult)
}

// freezeWrites freezes write operations on the source server.
func (e *CutoverEngine) freezeWrites(ctx context.Context, config CutoverConfig) error {
	if e == nil {
		return fmt.Errorf("cutover engine is nil")
	}

	if e.freezeMgr == nil {
		return fmt.Errorf("freeze manager not configured")
	}

	// Freeze ALL databases involved in the migration, not just the single
	// ReplicationConfig.DatabaseType. The live-replication stage sets up
	// replication for every database detected on the source, so freezing only
	// one would leave the others writable during cutover and cause divergence.
	var databases []DatabaseInfo
	if e.freezeMgr.sourceSSH != nil {
		databases = detectDatabases(ctx, e.freezeMgr.sourceSSH)
	}

	// Ensure the primary replication database is included even if detection
	// missed it (e.g. non-standard process name) or SSH was unavailable.
	if config.ReplicationConfig.DatabaseType != "" {
		found := false
		for _, db := range databases {
			if strings.EqualFold(db.Type, config.ReplicationConfig.DatabaseType) {
				found = true
				break
			}
		}
		if !found {
			databases = append(databases, DatabaseInfo{
				Type: config.ReplicationConfig.DatabaseType,
				Port: config.ReplicationConfig.SourcePort,
			})
		}
	}

	if len(databases) == 0 {
		return nil
	}

	result, err := e.freezeMgr.FreezeWrites(ctx, databases)
	if err != nil {
		return err
	}
	e.freezeResult = result
	return nil
}
