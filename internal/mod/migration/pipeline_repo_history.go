package migration

import (
	"context"
	"database/sql"
	"errors"
)

// Persistence for the operational record: health, cutover and rollback
// history, sync/transfer sessions, verification results and risk reports.
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
