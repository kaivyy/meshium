package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"meshium/internal/shared"
	"time"
)

// Persistence for the audit trail, metrics, queue and provisioning state —
// the evidence a migration leaves behind.
// --- Audit trail ---

func (r *sqliteRepo) CreateAuditEntry(ctx context.Context, e AuditEntry) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// Phase2D-2: inherit the propagated correlation id when the caller did
	// not set one. One externally-initiated operation → one correlation id
	// across all its audit rows. Covers every CreateAuditEntry call site.
	if e.CorrelationID == "" {
		if rid := CorrelationFrom(ctx); rid != "" {
			e.CorrelationID = rid
		}
	}
	if e.IdempotencyKey == "" {
		if key := IdempotencyKeyFrom(ctx); key != "" {
			e.IdempotencyKey = key
		}
	}
	// Phase2D-9 (release gate): sanitize every free-text audit field at the
	// persistence boundary. Callers pass raw error/detail strings that can embed
	// a secret (e.g. "mysql -uroot -pS3cret" in EventData). Persisting them
	// would leak into an operator-readable column. SanitizeString redacts
	// connection strings, bearer tokens, private keys, and inline passwords.
	e.EventData = shared.SanitizeString(e.EventData)
	e.PreviousState = shared.SanitizeString(e.PreviousState)
	e.NewState = shared.SanitizeString(e.NewState)
	e.Actor = shared.SanitizeString(e.Actor)
	e.FenceStatus = shared.SanitizeString(e.FenceStatus)
	e.TopologySummary = shared.SanitizeString(e.TopologySummary)
	e.TrafficVerifySummary = shared.SanitizeString(e.TrafficVerifySummary)
	e.ApprovalRef = shared.SanitizeString(e.ApprovalRef)
	res, err := r.db.Exec(
		`INSERT INTO audit_trail (migration_id, event_type, event_data, previous_state, new_state, actor,
			correlation_id, idempotency_key, actor_type, fence_generation, fence_status,
			topology_summary, traffic_verify_summary, approval_ref, result)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.MigrationID, e.EventType, e.EventData, e.PreviousState, e.NewState, e.Actor,
		e.CorrelationID, e.IdempotencyKey, e.ActorType, e.FenceGeneration, e.FenceStatus,
		e.TopologySummary, e.TrafficVerifySummary, e.ApprovalRef, e.Result,
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
		`SELECT id, migration_id, event_type, event_data, previous_state, new_state, actor, created_at,
			correlation_id, idempotency_key, actor_type, fence_generation, fence_status,
			topology_summary, traffic_verify_summary, approval_ref, result
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
		var correlationID, idempotencyKey, actorType, fenceStatus, topologySummary, trafficVerifySummary, approvalRef, result sql.NullString
		var fenceGeneration sql.NullInt64
		if err := rows.Scan(
			&e.ID, &e.MigrationID, &e.EventType, &eventData, &previousState, &newState, &actor, &e.CreatedAt,
			&correlationID, &idempotencyKey, &actorType, &fenceGeneration, &fenceStatus,
			&topologySummary, &trafficVerifySummary, &approvalRef, &result,
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
		if correlationID.Valid {
			e.CorrelationID = correlationID.String
		}
		if idempotencyKey.Valid {
			e.IdempotencyKey = idempotencyKey.String
		}
		if actorType.Valid {
			e.ActorType = actorType.String
		}
		if fenceGeneration.Valid {
			e.FenceGeneration = int(fenceGeneration.Int64)
		}
		if fenceStatus.Valid {
			e.FenceStatus = fenceStatus.String
		}
		if topologySummary.Valid {
			e.TopologySummary = topologySummary.String
		}
		if trafficVerifySummary.Valid {
			e.TrafficVerifySummary = trafficVerifySummary.String
		}
		if approvalRef.Valid {
			e.ApprovalRef = approvalRef.String
		}
		if result.Valid {
			e.Result = result.String
		}
		results = append(results, e)
	}
	return results, nil
}

// GetAuditEntryByIdempotencyKey returns the most recent audit entry recorded
// under a client-supplied idempotency key for a migration, or nil if none.
// Used to de-duplicate retried REST mutations: a repeat request carrying the
// same key can be answered from the original outcome instead of re-executing.
func (r *sqliteRepo) GetAuditEntryByIdempotencyKey(ctx context.Context, migrationID int, key string) (*AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, migration_id, event_type, event_data, previous_state, new_state, actor, created_at,
			correlation_id, idempotency_key, actor_type, fence_generation, fence_status,
			topology_summary, traffic_verify_summary, approval_ref, result
		 FROM audit_trail WHERE migration_id = ? AND idempotency_key = ?
		 ORDER BY created_at DESC LIMIT 1`,
		migrationID, key)

	var e AuditEntry
	var eventData, previousState, newState, actor sql.NullString
	var correlationID, idempotencyKey, actorType, fenceStatus, topologySummary, trafficVerifySummary, approvalRef, result sql.NullString
	var fenceGeneration sql.NullInt64
	if err := row.Scan(
		&e.ID, &e.MigrationID, &e.EventType, &eventData, &previousState, &newState, &actor, &e.CreatedAt,
		&correlationID, &idempotencyKey, &actorType, &fenceGeneration, &fenceStatus,
		&topologySummary, &trafficVerifySummary, &approvalRef, &result,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
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
	if correlationID.Valid {
		e.CorrelationID = correlationID.String
	}
	if idempotencyKey.Valid {
		e.IdempotencyKey = idempotencyKey.String
	}
	if actorType.Valid {
		e.ActorType = actorType.String
	}
	if fenceGeneration.Valid {
		e.FenceGeneration = int(fenceGeneration.Int64)
	}
	if fenceStatus.Valid {
		e.FenceStatus = fenceStatus.String
	}
	if topologySummary.Valid {
		e.TopologySummary = topologySummary.String
	}
	if trafficVerifySummary.Valid {
		e.TrafficVerifySummary = trafficVerifySummary.String
	}
	if approvalRef.Valid {
		e.ApprovalRef = approvalRef.String
	}
	if result.Valid {
		e.Result = result.String
	}
	return &e, nil
}

// BuildDiagnosticBundle aggregates a migration's durable state into one
// operator-exportable snapshot for incident recovery (Phase2D-4). It never
// masks a failure: any sub-query error is propagated and the caller surfaces
// it rather than returning a partial/fabricated bundle. The config is redacted
// so the bundle is safe to share; secrets never leave the boundary.
func (r *sqliteRepo) BuildDiagnosticBundle(ctx context.Context, migrationID int) (*DiagnosticBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b := &DiagnosticBundle{GeneratedAt: time.Now().UTC().Format(time.RFC3339), MigrationID: migrationID}

	if m, err := r.GetMigration(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load migration: %w", err)
	} else {
		b.Migration = m
	}

	if cfg, err := r.GetMigrationConfig(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load config: %w", err)
	} else if cfg != nil {
		raw, _ := json.Marshal(cfg)
		redacted := shared.SanitizeJSONRawMessage(raw)
		var cfgMap map[string]interface{}
		if err := json.Unmarshal(redacted, &cfgMap); err == nil {
			b.Config = cfgMap
		}
	}

	if stages, err := r.GetStages(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load stages: %w", err)
	} else {
		b.Stages = stages
	}
	if trail, err := r.GetAuditTrail(migrationID, 200); err != nil {
		return nil, fmt.Errorf("diagnostic: load audit: %w", err)
	} else {
		b.AuditTrail = trail
	}
	if events, err := r.GetEvents(ctx, migrationID, 0, 500); err != nil {
		return nil, fmt.Errorf("diagnostic: load events: %w", err)
	} else {
		b.Events = events
	}
	if fl, err := r.GetFenceLease(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load fence: %w", err)
	} else {
		b.Fence = fl
	}
	if tc, err := r.GetTrafficSwitchConfig(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load traffic: %w", err)
	} else {
		b.Traffic = tc
	}
	if co, err := r.GetCutoverHistory(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load cutover: %w", err)
	} else {
		b.Cutover = co
	}
	if rb, err := r.GetRollbackHistory(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load rollback: %w", err)
	} else {
		b.Rollback = rb
	}
	if ss, err := r.GetSyncSessions(migrationID); err != nil {
		return nil, fmt.Errorf("diagnostic: load sync: %w", err)
	} else {
		b.SyncSessions = ss
	}

	return b, nil
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
