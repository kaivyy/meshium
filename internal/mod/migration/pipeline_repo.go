package migration

import (
	"context"
)

// PipelineRepo extends the existing Repo and JobRepository interfaces with
// methods for the zero-downtime pipeline tables.
type PipelineRepo interface {
	// --- Migration stages ---
	CreateStage(ctx context.Context, migrationID int, stageName string, stageIndex int) (int64, error)
	UpdateStageState(ctx context.Context, stageID int64, state StageState, errMsg string) error
	UpdateStageCheckpoint(ctx context.Context, stageID int64, checkpointData string) error
	UpdateStageResult(ctx context.Context, stageID int64, resultData string) error
	IncrementStageAttempt(ctx context.Context, stageID int64) error
	GetStages(migrationID int) ([]PipelineStage, error)
	GetStageByName(migrationID int, stageName string) (*PipelineStage, error)
	GetCompletedStages(migrationID int) ([]PipelineStage, error)
	// GetLatestStep returns the most recently completed step for a migration
	// matching the given action, or nil if none exists. Used to restore
	// persisted previews (e.g. dry-run results) across page refreshes.
	GetLatestStep(migrationID int, action string) (*MigrationStepRecord, error)

	// --- Replication status ---
	CreateReplicationStatus(ctx context.Context, rs ReplicationStatus) (int64, error)
	UpdateReplicationStatus(ctx context.Context, id int64, status string, lag int64, errMsg string) error
	GetReplicationStatus(migrationID int) ([]ReplicationStatus, error)

	// --- Traffic switch ---
	CreateTrafficSwitchConfig(ctx context.Context, cfg TrafficSwitchConfig) (int64, error)
	UpdateTrafficSwitchState(ctx context.Context, id int64, state string) error
	GetTrafficSwitchConfig(migrationID int) (*TrafficSwitchConfig, error)

	// --- Health history ---
	CreateHealthCheckResult(ctx context.Context, r HealthCheckResult) (int64, error)
	GetHealthHistory(migrationID int, limit int) ([]HealthCheckResult, error)

	// --- Cutover history ---
	CreateCutoverRecord(ctx context.Context, r CutoverRecord) (int64, error)
	UpdateCutoverRecord(ctx context.Context, id int64, completedAt string, errMsg string) error
	GetCutoverHistory(migrationID int) ([]CutoverRecord, error)

	// --- Rollback history ---
	CreateRollbackRecord(ctx context.Context, r RollbackRecord) (int64, error)
	UpdateRollbackRecord(ctx context.Context, id int64, success bool, completedAt string, errMsg string) error
	GetRollbackHistory(migrationID int) ([]RollbackRecord, error)

	// --- Sync session ---
	CreateSyncSession(ctx context.Context, s SyncSession) (int64, error)
	UpdateSyncSession(ctx context.Context, id int64, bytesTransferred int64, filesTransferred int, speed int64, status string) error
	CompleteSyncSession(ctx context.Context, id int64, status string, errMsg string) error
	GetSyncSessions(migrationID int) ([]SyncSession, error)

	// --- Transfer session ---
	CreateTransferSession(ctx context.Context, t TransferSession) (int64, error)
	UpdateTransferSession(ctx context.Context, id int64, bytesTransferred int64, checksum string, status string) error
	GetTransferSessions(syncSessionID int64) ([]TransferSession, error)

	// --- Verification result ---
	CreateVerificationResult(ctx context.Context, r VerificationResult) (int64, error)
	GetVerificationResults(migrationID int) ([]VerificationResult, error)

	// --- Risk report ---
	CreateRiskReport(ctx context.Context, r RiskReport) (int64, error)
	GetRiskReport(migrationID int) (*RiskReport, error)

	// --- Audit trail ---
	CreateAuditEntry(ctx context.Context, e AuditEntry) (int64, error)
	GetAuditTrail(migrationID int, limit int) ([]AuditEntry, error)
	GetAuditEntryByIdempotencyKey(ctx context.Context, migrationID int, key string) (*AuditEntry, error)

	// --- Phase 6B selective-apply selection persistence ---
	UpsertSelection(ctx context.Context, migrationID int, itemKey, category, action, reason string, riskAck bool, manualFollowup string) error
	GetSelections(ctx context.Context, migrationID int) ([]SelectionDecision, error)
	GetSelection(ctx context.Context, migrationID int, itemKey string) (string, error)
	ClearSelections(ctx context.Context, migrationID int) error

	// --- Phase 6C-BE per-item execution + verification evidence ---
	UpsertItemResult(ctx context.Context, migrationID int, res ItemResult) error
	GetItemResults(ctx context.Context, migrationID int) ([]ItemResult, error)
	GetItemResult(ctx context.Context, migrationID int, itemKey string) (ItemResult, bool, error)
	AppendSelectionHistory(ctx context.Context, migrationID int, h SelectionHistory) error
	GetSelectionHistory(ctx context.Context, migrationID int) ([]SelectionHistory, error)

	// --- Diagnostic bundle (Phase2D-4) ---
	BuildDiagnosticBundle(ctx context.Context, migrationID int) (*DiagnosticBundle, error)

	// --- Metrics ---
	CreateMetric(ctx context.Context, m MigrationMetric) (int64, error)
	GetMetrics(migrationID int, limit int) ([]MigrationMetric, error)

	// --- Queue state ---
	CreateQueueState(ctx context.Context, q QueueState) (int64, error)
	UpdateQueueState(ctx context.Context, id int64, paused bool, activeJobs int, drained, synced, verified bool, errMsg string) error
	GetQueueStates(migrationID int) ([]QueueState, error)

	// --- Provision state ---
	CreateProvisionState(ctx context.Context, p ProvisionState) (int64, error)
	UpdateProvisionState(ctx context.Context, id int64, installed, configured, verified bool, version, errMsg string) error
	GetProvisionStates(migrationID int) ([]ProvisionState, error)

	// --- Migration config and planning ---
	SetMigrationConfig(migrationID int, config *MigrationConfig) error
	GetMigrationConfig(migrationID int) (*MigrationConfig, error)
	GetPlan(ctx context.Context, migrationID int) (map[string]any, error)
	SavePlan(ctx context.Context, migrationID int, plan map[string]any) error
	GetSession(ctx context.Context, id int) (*Migration, error)

	// --- Events ---
	CreateEvent(ctx context.Context, event MigrationEvent) error
	GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error)

	// --- Risk ---
	SetMigrationRisk(migrationID int, score float64, class string) error
}

// Ensure sqliteRepo satisfies PipelineRepo at compile time.
var _ PipelineRepo = (*sqliteRepo)(nil)
