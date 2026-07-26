package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Persistence for migration config, events and stored plan artifacts.
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

// --- Plan Storage (Batch 2) ---

// GetSession retrieves the underlying migration record for a pipeline session.
func (r *sqliteRepo) GetSession(ctx context.Context, id int) (*Migration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.GetMigration(id)
}

// GetPlan retrieves the stored plan data for a migration.
func (r *sqliteRepo) GetPlan(ctx context.Context, migrationID int) (map[string]any, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT workloads, dependency_graph, compatibility_issues, strategy, warnings, risk_score, blocking_issues, recommendation_count
		 FROM migration_plans WHERE migration_id = ?`, migrationID)

	var workloadsJSON, graphJSON, compatJSON, strategyJSON, warningsJSON string
	var riskScore float64
	var blockingIssues, recCount int

	if err := row.Scan(&workloadsJSON, &graphJSON, &compatJSON, &strategyJSON, &warningsJSON, &riskScore, &blockingIssues, &recCount); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get plan: %w", err)
	}

	result := make(map[string]any)

	var workloads any
	if err := json.Unmarshal([]byte(workloadsJSON), &workloads); err == nil {
		result["workloads"] = workloads
	}

	var graph any
	if err := json.Unmarshal([]byte(graphJSON), &graph); err == nil {
		result["dependencyGraph"] = graph
	}

	var compat any
	if err := json.Unmarshal([]byte(compatJSON), &compat); err == nil {
		result["compatibilityIssues"] = compat
	}

	var strategy any
	if err := json.Unmarshal([]byte(strategyJSON), &strategy); err == nil {
		result["strategy"] = strategy
	}

	var warnings any
	if err := json.Unmarshal([]byte(warningsJSON), &warnings); err == nil {
		result["warnings"] = warnings
	}

	result["riskScore"] = riskScore
	result["blockingIssues"] = blockingIssues
	result["recommendationCount"] = recCount

	return result, nil
}

// SavePlan stores the plan data for a migration.
func (r *sqliteRepo) SavePlan(ctx context.Context, migrationID int, plan map[string]any) error {
	workloadsJSON := "[]"
	if v, ok := plan["workloads"]; ok {
		if b, err := json.Marshal(v); err == nil {
			workloadsJSON = string(b)
		}
	}

	graphJSON := "{}"
	if v, ok := plan["dependencyGraph"]; ok {
		if b, err := json.Marshal(v); err == nil {
			graphJSON = string(b)
		}
	}

	compatJSON := "[]"
	if v, ok := plan["compatibilityIssues"]; ok {
		if b, err := json.Marshal(v); err == nil {
			compatJSON = string(b)
		}
	}

	strategyJSON := "{}"
	if v, ok := plan["strategy"]; ok {
		if b, err := json.Marshal(v); err == nil {
			strategyJSON = string(b)
		}
	}

	warningsJSON := "[]"
	if v, ok := plan["warnings"]; ok {
		if b, err := json.Marshal(v); err == nil {
			warningsJSON = string(b)
		}
	}

	riskScore := 0.0
	if v, ok := plan["riskScore"]; ok {
		if f, ok := v.(float64); ok {
			riskScore = f
		}
	}

	blockingIssues := 0
	if v, ok := plan["blockingIssues"]; ok {
		if i, ok := v.(int); ok {
			blockingIssues = i
		}
	}

	recCount := 0
	if v, ok := plan["recommendationCount"]; ok {
		if i, ok := v.(int); ok {
			recCount = i
		}
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO migration_plans (migration_id, workloads, dependency_graph, compatibility_issues, strategy, warnings, risk_score, blocking_issues, recommendation_count, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id) DO UPDATE SET
		   workloads = excluded.workloads,
		   dependency_graph = excluded.dependency_graph,
		   compatibility_issues = excluded.compatibility_issues,
		   strategy = excluded.strategy,
		   warnings = excluded.warnings,
		   risk_score = excluded.risk_score,
		   blocking_issues = excluded.blocking_issues,
		   recommendation_count = excluded.recommendation_count,
		   updated_at = CURRENT_TIMESTAMP`,
		migrationID, workloadsJSON, graphJSON, compatJSON, strategyJSON, warningsJSON, riskScore, blockingIssues, recCount)

	return err
}
