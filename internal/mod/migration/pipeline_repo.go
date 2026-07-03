package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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

	// --- Migration config ---
	SetMigrationConfig(migrationID int, config *MigrationConfig) error
	GetMigrationConfig(migrationID int) (*MigrationConfig, error)

	// --- Events ---
	CreateEvent(ctx context.Context, event MigrationEvent) error
	GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error)

	// --- Risk ---
	SetMigrationRisk(migrationID int, score float64, class string) error
}

// Ensure sqliteRepo satisfies PipelineRepo at compile time.
var _ PipelineRepo = (*sqliteRepo)(nil)

// --- Migration stages ---

func (r *sqliteRepo) CreateStage(ctx context.Context, migrationID int, stageName string, stageIndex int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_stages (migration_id, stage_name, stage_index, state, started_at)
		 VALUES (?, ?, ?, 'pending', CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, stage_name) DO UPDATE SET state = 'pending'`,
		migrationID, stageName, stageIndex,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateStageState(ctx context.Context, stageID int64, state StageState, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_stages SET state = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(state), errMsg, stageID,
	)
	return err
}

func (r *sqliteRepo) UpdateStageCheckpoint(ctx context.Context, stageID int64, checkpointData string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_stages SET checkpoint_data = ? WHERE id = ?`,
		checkpointData, stageID,
	)
	return err
}

func (r *sqliteRepo) UpdateStageResult(ctx context.Context, stageID int64, resultData string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_stages SET result_data = ? WHERE id = ?`,
		resultData, stageID,
	)
	return err
}

func (r *sqliteRepo) IncrementStageAttempt(ctx context.Context, stageID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_stages SET attempt_count = attempt_count + 1 WHERE id = ?`,
		stageID,
	)
	return err
}

func (r *sqliteRepo) GetStages(migrationID int) ([]PipelineStage, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, stage_name, stage_index, state, attempt_count,
		        checkpoint_data, result_data, error, started_at, completed_at
		 FROM migration_stages WHERE migration_id = ? ORDER BY stage_index ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stages := make([]PipelineStage, 0)
	for rows.Next() {
		var s PipelineStage
		var checkpointData, resultData, errMsg, startedAt, completedAt sql.NullString
		if err := rows.Scan(
			&s.ID, &s.MigrationID, &s.StageName, &s.StageIndex, &s.State, &s.AttemptCount,
			&checkpointData, &resultData, &errMsg, &startedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if checkpointData.Valid {
			s.CheckpointData = checkpointData.String
		}
		if resultData.Valid {
			s.ResultData = resultData.String
		}
		if errMsg.Valid {
			s.Error = errMsg.String
		}
		if startedAt.Valid {
			s.StartedAt = startedAt.String
		}
		if completedAt.Valid {
			s.CompletedAt = completedAt.String
		}
		stages = append(stages, s)
	}
	return stages, nil
}

func (r *sqliteRepo) GetStageByName(migrationID int, stageName string) (*PipelineStage, error) {
	var s PipelineStage
	var checkpointData, resultData, errMsg, startedAt, completedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, migration_id, stage_name, stage_index, state, attempt_count,
		        checkpoint_data, result_data, error, started_at, completed_at
		 FROM migration_stages WHERE migration_id = ? AND stage_name = ?`,
		migrationID, stageName,
	).Scan(
		&s.ID, &s.MigrationID, &s.StageName, &s.StageIndex, &s.State, &s.AttemptCount,
		&checkpointData, &resultData, &errMsg, &startedAt, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("stage not found: migration_id=%d stage_name=%s", migrationID, stageName)
	}
	if err != nil {
		return nil, err
	}
	if checkpointData.Valid {
		s.CheckpointData = checkpointData.String
	}
	if resultData.Valid {
		s.ResultData = resultData.String
	}
	if errMsg.Valid {
		s.Error = errMsg.String
	}
	if startedAt.Valid {
		s.StartedAt = startedAt.String
	}
	if completedAt.Valid {
		s.CompletedAt = completedAt.String
	}
	return &s, nil
}

func (r *sqliteRepo) GetCompletedStages(migrationID int) ([]PipelineStage, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, stage_name, stage_index, state, attempt_count,
		        checkpoint_data, result_data, error, started_at, completed_at
		 FROM migration_stages WHERE migration_id = ? AND state = 'completed' ORDER BY stage_index ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stages := make([]PipelineStage, 0)
	for rows.Next() {
		var s PipelineStage
		var checkpointData, resultData, errMsg, startedAt, completedAt sql.NullString
		if err := rows.Scan(
			&s.ID, &s.MigrationID, &s.StageName, &s.StageIndex, &s.State, &s.AttemptCount,
			&checkpointData, &resultData, &errMsg, &startedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if checkpointData.Valid {
			s.CheckpointData = checkpointData.String
		}
		if resultData.Valid {
			s.ResultData = resultData.String
		}
		if errMsg.Valid {
			s.Error = errMsg.String
		}
		if startedAt.Valid {
			s.StartedAt = startedAt.String
		}
		if completedAt.Valid {
			s.CompletedAt = completedAt.String
		}
		stages = append(stages, s)
	}
	return stages, nil
}

// --- Replication status ---

func (r *sqliteRepo) CreateReplicationStatus(ctx context.Context, rs ReplicationStatus) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO replication_status (migration_id, database_type, database_name, source_host, target_host, replication_mode, replication_lag, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rs.MigrationID, rs.DatabaseType, rs.DatabaseName, rs.SourceHost, rs.TargetHost,
		string(rs.ReplicationMode), rs.ReplicationLag, rs.Status,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateReplicationStatus(ctx context.Context, id int64, status string, lag int64, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE replication_status SET status = ?, replication_lag = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, lag, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetReplicationStatus(migrationID int) ([]ReplicationStatus, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, database_type, database_name, source_host, target_host,
		        replication_mode, replication_lag, status, last_error, created_at, updated_at
		 FROM replication_status WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]ReplicationStatus, 0)
	for rows.Next() {
		var rs ReplicationStatus
		var dbName, sourceHost, targetHost, lastErr sql.NullString
		if err := rows.Scan(
			&rs.ID, &rs.MigrationID, &rs.DatabaseType, &dbName, &sourceHost, &targetHost,
			&rs.ReplicationMode, &rs.ReplicationLag, &rs.Status, &lastErr, &rs.CreatedAt, &rs.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if dbName.Valid {
			rs.DatabaseName = dbName.String
		}
		if sourceHost.Valid {
			rs.SourceHost = sourceHost.String
		}
		if targetHost.Valid {
			rs.TargetHost = targetHost.String
		}
		if lastErr.Valid {
			rs.LastError = lastErr.String
		}
		results = append(results, rs)
	}
	return results, nil
}

// --- Traffic switch ---

func (r *sqliteRepo) CreateTrafficSwitchConfig(ctx context.Context, cfg TrafficSwitchConfig) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO traffic_switch_config (migration_id, provider, original_config, new_config, switch_state, health_check_url, rollback_config)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cfg.MigrationID, string(cfg.Provider), cfg.OriginalConfig, cfg.NewConfig, cfg.SwitchState, cfg.HealthCheckURL, cfg.RollbackConfig,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateTrafficSwitchState(ctx context.Context, id int64, state string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE traffic_switch_config SET switch_state = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		state, id,
	)
	return err
}

func (r *sqliteRepo) GetTrafficSwitchConfig(migrationID int) (*TrafficSwitchConfig, error) {
	var cfg TrafficSwitchConfig
	var originalConfig, newConfig, healthCheckURL, rollbackConfig sql.NullString
	err := r.db.QueryRow(
		`SELECT id, migration_id, provider, original_config, new_config, switch_state, health_check_url, rollback_config, created_at, updated_at
		 FROM traffic_switch_config WHERE migration_id = ? ORDER BY id DESC LIMIT 1`,
		migrationID,
	).Scan(
		&cfg.ID, &cfg.MigrationID, &cfg.Provider, &originalConfig, &newConfig, &cfg.SwitchState, &healthCheckURL, &rollbackConfig, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if originalConfig.Valid {
		cfg.OriginalConfig = originalConfig.String
	}
	if newConfig.Valid {
		cfg.NewConfig = newConfig.String
	}
	if healthCheckURL.Valid {
		cfg.HealthCheckURL = healthCheckURL.String
	}
	if rollbackConfig.Valid {
		cfg.RollbackConfig = rollbackConfig.String
	}
	return &cfg, nil
}

// --- Health history ---

func (r *sqliteRepo) CreateHealthCheckResult(ctx context.Context, hr HealthCheckResult) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO health_history (migration_id, server_id, check_type, check_target, status, response_time_ms, status_code, error_message, health_score)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hr.MigrationID, hr.ServerID, string(hr.CheckType), hr.CheckTarget, hr.Status,
		hr.ResponseTimeMs, hr.StatusCode, hr.ErrorMessage, hr.HealthScore,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetHealthHistory(migrationID int, limit int) ([]HealthCheckResult, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, migration_id, server_id, check_type, check_target, status, response_time_ms, status_code, error_message, health_score, created_at
		 FROM health_history WHERE migration_id = ? ORDER BY created_at DESC LIMIT ?`,
		migrationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]HealthCheckResult, 0)
	for rows.Next() {
		var hr HealthCheckResult
		var responseTimeMs, statusCode sql.NullInt64
		var errorMessage sql.NullString
		if err := rows.Scan(
			&hr.ID, &hr.MigrationID, &hr.ServerID, &hr.CheckType, &hr.CheckTarget, &hr.Status,
			&responseTimeMs, &statusCode, &errorMessage, &hr.HealthScore, &hr.CreatedAt,
		); err != nil {
			return nil, err
		}
		if responseTimeMs.Valid {
			hr.ResponseTimeMs = responseTimeMs.Int64
		}
		if statusCode.Valid {
			hr.StatusCode = int(statusCode.Int64)
		}
		if errorMessage.Valid {
			hr.ErrorMessage = errorMessage.String
		}
		results = append(results, hr)
	}
	return results, nil
}

// --- Cutover history ---

func (r *sqliteRepo) CreateCutoverRecord(ctx context.Context, cr CutoverRecord) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO cutover_history (migration_id, cutover_type, previous_state, new_state, freeze_write, queue_drained, delta_synced, health_verified, traffic_switched, rollback_triggered, error, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		cr.MigrationID, cr.CutoverType, cr.PreviousState, cr.NewState,
		boolToInt(cr.FreezeWrite), boolToInt(cr.QueueDrained), boolToInt(cr.DeltaSynced),
		boolToInt(cr.HealthVerified), boolToInt(cr.TrafficSwitched), boolToInt(cr.RollbackTriggered),
		cr.Error,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateCutoverRecord(ctx context.Context, id int64, completedAt string, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE cutover_history SET completed_at = ?, error = ? WHERE id = ?`,
		completedAt, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetCutoverHistory(migrationID int) ([]CutoverRecord, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, cutover_type, previous_state, new_state,
		        freeze_write, queue_drained, delta_synced, health_verified, traffic_switched, rollback_triggered,
		        error, started_at, completed_at
		 FROM cutover_history WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]CutoverRecord, 0)
	for rows.Next() {
		var cr CutoverRecord
		var previousState, newState, errMsg, completedAt sql.NullString
		var freezeWrite, queueDrained, deltaSynced, healthVerified, trafficSwitched, rollbackTriggered sql.NullInt64
		if err := rows.Scan(
			&cr.ID, &cr.MigrationID, &cr.CutoverType, &previousState, &newState,
			&freezeWrite, &queueDrained, &deltaSynced, &healthVerified, &trafficSwitched, &rollbackTriggered,
			&errMsg, &cr.StartedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if previousState.Valid {
			cr.PreviousState = previousState.String
		}
		if newState.Valid {
			cr.NewState = newState.String
		}
		if errMsg.Valid {
			cr.Error = errMsg.String
		}
		if completedAt.Valid {
			cr.CompletedAt = completedAt.String
		}
		cr.FreezeWrite = intToBool(freezeWrite)
		cr.QueueDrained = intToBool(queueDrained)
		cr.DeltaSynced = intToBool(deltaSynced)
		cr.HealthVerified = intToBool(healthVerified)
		cr.TrafficSwitched = intToBool(trafficSwitched)
		cr.RollbackTriggered = intToBool(rollbackTriggered)
		results = append(results, cr)
	}
	return results, nil
}

// --- Rollback history ---

func (r *sqliteRepo) CreateRollbackRecord(ctx context.Context, rr RollbackRecord) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO rollback_history (migration_id, rollback_type, stage_name, category,
		        traffic_reverted, dns_reverted, db_role_reverted, redis_role_reverted,
		        queue_resumed, containers_reverted, configs_reverted, success, error, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		rr.MigrationID, rr.RollbackType, rr.StageName, rr.Category,
		boolToInt(rr.TrafficReverted), boolToInt(rr.DNSReverted), boolToInt(rr.DBRoleReverted), boolToInt(rr.RedisRoleReverted),
		boolToInt(rr.QueueResumed), boolToInt(rr.ContainersReverted), boolToInt(rr.ConfigsReverted),
		boolToInt(rr.Success), rr.Error,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateRollbackRecord(ctx context.Context, id int64, success bool, completedAt string, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE rollback_history SET success = ?, completed_at = ?, error = ? WHERE id = ?`,
		boolToInt(success), completedAt, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetRollbackHistory(migrationID int) ([]RollbackRecord, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, rollback_type, stage_name, category,
		        traffic_reverted, dns_reverted, db_role_reverted, redis_role_reverted,
		        queue_resumed, containers_reverted, configs_reverted, success, error, started_at, completed_at
		 FROM rollback_history WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]RollbackRecord, 0)
	for rows.Next() {
		var rr RollbackRecord
		var stageName, category, errMsg, completedAt sql.NullString
		var trafficReverted, dnsReverted, dbRoleReverted, redisRoleReverted, queueResumed, containersReverted, configsReverted, success sql.NullInt64
		if err := rows.Scan(
			&rr.ID, &rr.MigrationID, &rr.RollbackType, &stageName, &category,
			&trafficReverted, &dnsReverted, &dbRoleReverted, &redisRoleReverted,
			&queueResumed, &containersReverted, &configsReverted, &success, &errMsg, &rr.StartedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if stageName.Valid {
			rr.StageName = stageName.String
		}
		if category.Valid {
			rr.Category = category.String
		}
		if errMsg.Valid {
			rr.Error = errMsg.String
		}
		if completedAt.Valid {
			rr.CompletedAt = completedAt.String
		}
		rr.TrafficReverted = intToBool(trafficReverted)
		rr.DNSReverted = intToBool(dnsReverted)
		rr.DBRoleReverted = intToBool(dbRoleReverted)
		rr.RedisRoleReverted = intToBool(redisRoleReverted)
		rr.QueueResumed = intToBool(queueResumed)
		rr.ContainersReverted = intToBool(containersReverted)
		rr.ConfigsReverted = intToBool(configsReverted)
		rr.Success = intToBool(success)
		results = append(results, rr)
	}
	return results, nil
}

// --- Sync session ---

func (r *sqliteRepo) CreateSyncSession(ctx context.Context, s SyncSession) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO sync_session (migration_id, sync_type, source_path, target_path, bytes_total, files_total, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.MigrationID, s.SyncType, s.SourcePath, s.TargetPath, s.BytesTotal, s.FilesTotal, s.Status,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateSyncSession(ctx context.Context, id int64, bytesTransferred int64, filesTransferred int, speed int64, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE sync_session SET bytes_transferred = ?, files_transferred = ?, speed_bytes_sec = ?, status = ? WHERE id = ?`,
		bytesTransferred, filesTransferred, speed, status, id,
	)
	return err
}

func (r *sqliteRepo) CompleteSyncSession(ctx context.Context, id int64, status string, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE sync_session SET status = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetSyncSessions(migrationID int) ([]SyncSession, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, sync_type, source_path, target_path,
		        bytes_transferred, bytes_total, files_transferred, files_total,
		        speed_bytes_sec, checksum_verified, status, error, started_at, completed_at
		 FROM sync_session WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]SyncSession, 0)
	for rows.Next() {
		var s SyncSession
		var sourcePath, targetPath, errMsg, completedAt sql.NullString
		var checksumVerified sql.NullInt64
		if err := rows.Scan(
			&s.ID, &s.MigrationID, &s.SyncType, &sourcePath, &targetPath,
			&s.BytesTransferred, &s.BytesTotal, &s.FilesTransferred, &s.FilesTotal,
			&s.SpeedBytesSec, &checksumVerified, &s.Status, &errMsg, &s.StartedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if sourcePath.Valid {
			s.SourcePath = sourcePath.String
		}
		if targetPath.Valid {
			s.TargetPath = targetPath.String
		}
		if errMsg.Valid {
			s.Error = errMsg.String
		}
		if completedAt.Valid {
			s.CompletedAt = completedAt.String
		}
		s.ChecksumVerified = intToBool(checksumVerified)
		results = append(results, s)
	}
	return results, nil
}

// --- Transfer session ---

func (r *sqliteRepo) CreateTransferSession(ctx context.Context, t TransferSession) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO transfer_session (migration_id, sync_session_id, file_path, file_size, checksum_source, transfer_method, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.MigrationID, t.SyncSessionID, t.FilePath, t.FileSize, t.ChecksumSource, t.TransferMethod, t.Status,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateTransferSession(ctx context.Context, id int64, bytesTransferred int64, checksum string, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE transfer_session SET bytes_transferred = ?, checksum_target = ?, status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		bytesTransferred, checksum, status, id,
	)
	return err
}

func (r *sqliteRepo) GetTransferSessions(syncSessionID int64) ([]TransferSession, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, sync_session_id, file_path, file_size,
		        bytes_transferred, checksum_source, checksum_target, transfer_method, status, error, started_at, completed_at
		 FROM transfer_session WHERE sync_session_id = ? ORDER BY id ASC`,
		syncSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]TransferSession, 0)
	for rows.Next() {
		var t TransferSession
		var checksumSource, checksumTarget, transferMethod, errMsg, completedAt sql.NullString
		if err := rows.Scan(
			&t.ID, &t.MigrationID, &t.SyncSessionID, &t.FilePath, &t.FileSize,
			&t.BytesTransferred, &checksumSource, &checksumTarget, &transferMethod, &t.Status, &errMsg, &t.StartedAt, &completedAt,
		); err != nil {
			return nil, err
		}
		if checksumSource.Valid {
			t.ChecksumSource = checksumSource.String
		}
		if checksumTarget.Valid {
			t.ChecksumTarget = checksumTarget.String
		}
		if transferMethod.Valid {
			t.TransferMethod = transferMethod.String
		}
		if errMsg.Valid {
			t.Error = errMsg.String
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.String
		}
		results = append(results, t)
	}
	return results, nil
}

// --- Verification result ---

func (r *sqliteRepo) CreateVerificationResult(ctx context.Context, vr VerificationResult) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO verification_result (migration_id, verification_type, target, expected, actual, passed, error_message)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		vr.MigrationID, vr.VerificationType, vr.Target, vr.Expected, vr.Actual, boolToInt(vr.Passed), vr.ErrorMessage,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetVerificationResults(migrationID int) ([]VerificationResult, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, verification_type, target, expected, actual, passed, error_message, created_at
		 FROM verification_result WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]VerificationResult, 0)
	for rows.Next() {
		var vr VerificationResult
		var target, expected, actual, errMsg sql.NullString
		var passed sql.NullInt64
		if err := rows.Scan(
			&vr.ID, &vr.MigrationID, &vr.VerificationType, &target, &expected, &actual, &passed, &errMsg, &vr.CreatedAt,
		); err != nil {
			return nil, err
		}
		if target.Valid {
			vr.Target = target.String
		}
		if expected.Valid {
			vr.Expected = expected.String
		}
		if actual.Valid {
			vr.Actual = actual.String
		}
		if errMsg.Valid {
			vr.ErrorMessage = errMsg.String
		}
		vr.Passed = intToBool(passed)
		results = append(results, vr)
	}
	return results, nil
}

// --- Risk report ---

func (r *sqliteRepo) CreateRiskReport(ctx context.Context, rr RiskReport) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO risk_report (migration_id, risk_score, risk_class, downtime_estimate, data_size_bytes, database_size_bytes, container_count, volume_count, rollback_complexity, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rr.MigrationID, rr.RiskScore, string(rr.RiskClass), rr.DowntimeEstimate, rr.DataSizeBytes, rr.DatabaseSizeBytes,
		rr.ContainerCount, rr.VolumeCount, rr.RollbackComplexity, rr.Details,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetRiskReport(migrationID int) (*RiskReport, error) {
	var rr RiskReport
	var downtimeEstimate, rollbackComplexity, details sql.NullString
	err := r.db.QueryRow(
		`SELECT id, migration_id, risk_score, risk_class, downtime_estimate, data_size_bytes, database_size_bytes, container_count, volume_count, rollback_complexity, details, created_at
		 FROM risk_report WHERE migration_id = ? ORDER BY id DESC LIMIT 1`,
		migrationID,
	).Scan(
		&rr.ID, &rr.MigrationID, &rr.RiskScore, &rr.RiskClass, &downtimeEstimate, &rr.DataSizeBytes, &rr.DatabaseSizeBytes,
		&rr.ContainerCount, &rr.VolumeCount, &rollbackComplexity, &details, &rr.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if downtimeEstimate.Valid {
		rr.DowntimeEstimate = downtimeEstimate.String
	}
	if rollbackComplexity.Valid {
		rr.RollbackComplexity = rollbackComplexity.String
	}
	if details.Valid {
		rr.Details = details.String
	}
	return &rr, nil
}

// --- Audit trail ---

func (r *sqliteRepo) CreateAuditEntry(ctx context.Context, e AuditEntry) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO audit_trail (migration_id, event_type, event_data, previous_state, new_state, actor)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.MigrationID, e.EventType, e.EventData, e.PreviousState, e.NewState, e.Actor,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetAuditTrail(migrationID int, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, migration_id, event_type, event_data, previous_state, new_state, actor, created_at
		 FROM audit_trail WHERE migration_id = ? ORDER BY created_at DESC LIMIT ?`,
		migrationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]AuditEntry, 0)
	for rows.Next() {
		var e AuditEntry
		var eventData, previousState, newState, actor sql.NullString
		if err := rows.Scan(
			&e.ID, &e.MigrationID, &e.EventType, &eventData, &previousState, &newState, &actor, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		if eventData.Valid {
			e.EventData = eventData.String
		}
		if previousState.Valid {
			e.PreviousState = previousState.String
		}
		if newState.Valid {
			e.NewState = newState.String
		}
		if actor.Valid {
			e.Actor = actor.String
		}
		results = append(results, e)
	}
	return results, nil
}

// --- Metrics ---

func (r *sqliteRepo) CreateMetric(ctx context.Context, m MigrationMetric) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_metrics (migration_id, metric_name, metric_value, metric_unit, stage_name)
		 VALUES (?, ?, ?, ?, ?)`,
		m.MigrationID, m.MetricName, m.MetricValue, m.MetricUnit, m.StageName,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) GetMetrics(migrationID int, limit int) ([]MigrationMetric, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, migration_id, metric_name, metric_value, metric_unit, stage_name, created_at
		 FROM migration_metrics WHERE migration_id = ? ORDER BY created_at DESC LIMIT ?`,
		migrationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]MigrationMetric, 0)
	for rows.Next() {
		var m MigrationMetric
		var metricUnit, stageName sql.NullString
		if err := rows.Scan(
			&m.ID, &m.MigrationID, &m.MetricName, &m.MetricValue, &metricUnit, &stageName, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		if metricUnit.Valid {
			m.MetricUnit = metricUnit.String
		}
		if stageName.Valid {
			m.StageName = stageName.String
		}
		results = append(results, m)
	}
	return results, nil
}

// --- Queue state ---

func (r *sqliteRepo) CreateQueueState(ctx context.Context, q QueueState) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_queue_state (migration_id, queue_type, queue_name, paused, active_jobs, drained, synced, verified, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.MigrationID, q.QueueType, q.QueueName, boolToInt(q.Paused), q.ActiveJobs,
		boolToInt(q.Drained), boolToInt(q.Synced), boolToInt(q.Verified), q.Error,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateQueueState(ctx context.Context, id int64, paused bool, activeJobs int, drained, synced, verified bool, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_queue_state SET paused = ?, active_jobs = ?, drained = ?, synced = ?, verified = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		boolToInt(paused), activeJobs, boolToInt(drained), boolToInt(synced), boolToInt(verified), errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetQueueStates(migrationID int) ([]QueueState, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, queue_type, queue_name, paused, active_jobs, drained, synced, verified, error, created_at, updated_at
		 FROM migration_queue_state WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]QueueState, 0)
	for rows.Next() {
		var q QueueState
		var queueName, errMsg sql.NullString
		var paused, drained, synced, verified sql.NullInt64
		if err := rows.Scan(
			&q.ID, &q.MigrationID, &q.QueueType, &queueName, &paused, &q.ActiveJobs, &drained, &synced, &verified, &errMsg, &q.CreatedAt, &q.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if queueName.Valid {
			q.QueueName = queueName.String
		}
		if errMsg.Valid {
			q.Error = errMsg.String
		}
		q.Paused = intToBool(paused)
		q.Drained = intToBool(drained)
		q.Synced = intToBool(synced)
		q.Verified = intToBool(verified)
		results = append(results, q)
	}
	return results, nil
}

// --- Provision state ---

func (r *sqliteRepo) CreateProvisionState(ctx context.Context, p ProvisionState) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_provision_state (migration_id, component, installed, configured, verified, version, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.MigrationID, p.Component, boolToInt(p.Installed), boolToInt(p.Configured), boolToInt(p.Verified), p.Version, p.Error,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

func (r *sqliteRepo) UpdateProvisionState(ctx context.Context, id int64, installed, configured, verified bool, version, errMsg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_provision_state SET installed = ?, configured = ?, verified = ?, version = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		boolToInt(installed), boolToInt(configured), boolToInt(verified), version, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetProvisionStates(migrationID int) ([]ProvisionState, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, component, installed, configured, verified, version, error, created_at, updated_at
		 FROM migration_provision_state WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]ProvisionState, 0)
	for rows.Next() {
		var p ProvisionState
		var version, errMsg sql.NullString
		var installed, configured, verified sql.NullInt64
		if err := rows.Scan(
			&p.ID, &p.MigrationID, &p.Component, &installed, &configured, &verified, &version, &errMsg, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if version.Valid {
			p.Version = version.String
		}
		if errMsg.Valid {
			p.Error = errMsg.String
		}
		p.Installed = intToBool(installed)
		p.Configured = intToBool(configured)
		p.Verified = intToBool(verified)
		results = append(results, p)
	}
	return results, nil
}

// --- Migration config ---

func (r *sqliteRepo) SetMigrationConfig(migrationID int, config *MigrationConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal migration config: %w", err)
	}
	_, err = r.db.Exec("UPDATE migrations SET config = ? WHERE id = ?", string(configJSON), migrationID)
	return err
}

func (r *sqliteRepo) GetMigrationConfig(migrationID int) (*MigrationConfig, error) {
	var configJSON string
	err := r.db.QueryRow("SELECT config FROM migrations WHERE id = ?", migrationID).Scan(&configJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMigrationNotFound
	}
	if err != nil {
		return nil, err
	}
	if configJSON == "" || configJSON == "{}" {
		return DefaultMigrationConfig(), nil
	}
	var cfg MigrationConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return DefaultMigrationConfig(), nil
	}
	return &cfg, nil
}

// --- Risk ---

func (r *sqliteRepo) SetMigrationRisk(migrationID int, score float64, class string) error {
	_, err := r.db.Exec("UPDATE migrations SET risk_score = ?, risk_class = ? WHERE id = ?", score, class, migrationID)
	return err
}

// --- Events ---

func (r *sqliteRepo) CreateEvent(ctx context.Context, event MigrationEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	if event.Level == "" {
		event.Level = EventLevelInfo
	}
	details := string(event.Details)
	if len(event.Details) == 0 {
		details = "{}"
	}
	_, err := r.db.Exec(
		`INSERT INTO migration_events (
			migration_id, sequence, timestamp, level, stage, type, message, details, source, correlation_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.MigrationID, event.Sequence, event.Timestamp.Format(time.RFC3339Nano), string(event.Level),
		event.Stage, event.Type, event.Message, details, event.Source, event.CorrelationID,
	)
	return err
}

func (r *sqliteRepo) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, migration_id, sequence, timestamp, level, stage, type, message, details, source, correlation_id
		 FROM migration_events
		 WHERE migration_id = ? AND sequence > ?
		 ORDER BY sequence ASC
		 LIMIT ?`,
		migrationID, afterSequence, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]MigrationEvent, 0)
	for rows.Next() {
		var e MigrationEvent
		var ts, details, correlationID sql.NullString
		if err := rows.Scan(
			&e.ID, &e.MigrationID, &e.Sequence, &ts, &e.Level, &e.Stage, &e.Type, &e.Message, &details, &e.Source, &correlationID,
		); err != nil {
			return nil, err
		}
		if ts.Valid && ts.String != "" {
			parsed, err := parseMigrationEventTimestamp(ts.String)
			if err != nil {
				return nil, err
			}
			e.Timestamp = parsed
		}
		if details.Valid && details.String != "" {
			e.Details = json.RawMessage(details.String)
		} else {
			e.Details = json.RawMessage(`{}`)
		}
		if correlationID.Valid {
			e.CorrelationID = correlationID.String
		}
		events = append(events, e)
	}
	return events, nil
}

func parseMigrationEventTimestamp(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	var parseErr error
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts.UTC(), nil
		} else {
			parseErr = err
		}
	}
	return time.Time{}, fmt.Errorf("parse migration event timestamp %q: %w", value, parseErr)
}

// --- Helpers ---

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(v sql.NullInt64) bool {
	return v.Valid && v.Int64 != 0
}
