package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Persistence for stage execution: stage rows, replication status, traffic
// switch config and the durable fence leases that make cutover safe.
// --- Migration stages ---

func (r *sqliteRepo) CreateStage(ctx context.Context, migrationID int, stageName string, stageIndex int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_stages (migration_id, stage_name, stage_index, state, started_at)
		 VALUES (?, ?, ?, 'pending', CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, stage_name) DO UPDATE SET
			stage_index = excluded.stage_index,
			state = 'pending',
			error = '',
			started_at = CURRENT_TIMESTAMP,
			completed_at = NULL`,
		migrationID, stageName, stageIndex,
	)
	if err != nil {
		return 0, err
	}
	_ = res
	var id int64
	err = r.db.QueryRowContext(ctx,
		`SELECT id FROM migration_stages WHERE migration_id = ? AND stage_name = ?`,
		migrationID, stageName,
	).Scan(&id)
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

// stageCheckpointJSON builds the per-stage checkpoint blob persisted after a
// stage completes. Stage-boundary resume (P0-3) reads this to know how far the
// pipeline got; byte-level / mid-transfer resume is Phase 2 (documented).
type stageCheckpoint struct {
	Stage      string `json:"stage"`
	State      string `json:"state"` // "completed"
	Attempt    int    `json:"attempt"`
	StageTotal int    `json:"stageTotal,omitempty"`
}

func stageCheckpointJSON(stageName string, attempt, stageTotal int) string {
	b, err := json.Marshal(stageCheckpoint{
		Stage:      stageName,
		State:      "completed",
		Attempt:    attempt,
		StageTotal: stageTotal,
	})
	if err != nil {
		return fmt.Sprintf(`{"stage":%q,"state":"completed","attempt":%d}`, stageName, attempt)
	}
	return string(b)
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

func (r *sqliteRepo) UpdateTrafficSwitchConfig(ctx context.Context, cfg TrafficSwitchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE traffic_switch_config SET
			original_config = ?,
			new_config = ?,
			switch_state = ?,
			health_check_url = ?,
			rollback_config = ?,
			updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		cfg.OriginalConfig, cfg.NewConfig, cfg.SwitchState, cfg.HealthCheckURL, cfg.RollbackConfig, cfg.ID,
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

// --- Fence lease repo methods (Phase 2A durable fencing authority) ---

func (r *sqliteRepo) CreateFenceLease(ctx context.Context, l FenceLease) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := r.db.Exec(
		`INSERT INTO migration_fence_leases
		   (migration_id, holder, fence_token, state, acquired_at, expires_at, renewed_at, released_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.MigrationID, l.Holder, l.FenceToken, l.State, l.AcquiredAt, l.ExpiresAt,
		nullableTime(l.RenewedAt), nullableTime(l.ReleasedAt),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *sqliteRepo) UpdateFenceLeaseState(ctx context.Context, id int64, state string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE migration_fence_leases SET state = ? WHERE id = ?`,
		state, id,
	)
	return err
}

// RenewFenceLease extends the lease expiry only if the holder + token match.
// Returns ErrFenceLeaseNotHeld on mismatch (fail-closed). The caller MUST
// treat any renew error as NeedsManualIntervention.
func (r *sqliteRepo) RenewFenceLease(ctx context.Context, id int64, holder string, token int, expiresAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	res, err := r.db.Exec(
		`UPDATE migration_fence_leases
		   SET expires_at = ?, renewed_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND holder = ? AND fence_token = ? AND released_at IS NULL`,
		expiresAt.Format(time.RFC3339), id, holder, token,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFenceLeaseNotHeld
	}
	return nil
}

// ReleaseFenceLease marks the lease released (best-effort cleanup). Does NOT
// auto-unfreeze the source; unfreeze is an explicit fenced step. Mismatched
// holder/token → ErrFenceLeaseNotHeld (fail-closed).
func (r *sqliteRepo) ReleaseFenceLease(ctx context.Context, id int64, holder string, token int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	res, err := r.db.Exec(
		`UPDATE migration_fence_leases SET released_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND holder = ? AND fence_token = ? AND released_at IS NULL`,
		id, holder, token,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFenceLeaseNotHeld
	}
	return nil
}

func (r *sqliteRepo) GetFenceLease(migrationID int) (*FenceLease, error) {
	var l FenceLease
	var renewedAt, releasedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, migration_id, holder, fence_token, state, acquired_at, expires_at, renewed_at, released_at
		 FROM migration_fence_leases WHERE migration_id = ? ORDER BY id DESC LIMIT 1`,
		migrationID,
	).Scan(
		&l.ID, &l.MigrationID, &l.Holder, &l.FenceToken, &l.State, &l.AcquiredAt, &l.ExpiresAt, &renewedAt, &releasedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if renewedAt.Valid {
		l.RenewedAt = renewedAt.String
	}
	if releasedAt.Valid {
		l.ReleasedAt = releasedAt.String
	}
	return &l, nil
}

// nullableTime returns NULL for empty time strings (DATETIME columns).
func nullableTime(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// reactivateFenceLease recycles the single existing lease row (UNIQUE on
// migration_id) for a new acquisition: overwrites holder/token/state and
// timestamps, clears released_at. Used after a prior lease was released
// cleanly. A stale (expired, unreleased) lease must NOT reach here — the
// authority refuses to silently overwrite it.
func (r *sqliteRepo) reactivateFenceLease(ctx context.Context, migrationID int, holder string, token int, state, acquiredAt, expiresAt string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	res, err := r.db.Exec(
		`UPDATE migration_fence_leases
		   SET holder = ?, fence_token = ?, state = ?, acquired_at = ?, expires_at = ?, renewed_at = NULL, released_at = NULL
		 WHERE migration_id = ?`,
		holder, token, state, acquiredAt, expiresAt, migrationID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFenceLeaseConflict
	}
	return nil
}
