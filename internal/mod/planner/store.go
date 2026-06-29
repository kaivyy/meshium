package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PlanStore is the interface for persisting and retrieving migration plans.
type PlanStore interface {
	// SavePlan stores a migration plan.
	SavePlan(ctx context.Context, plan *MigrationPlan) error
	// LoadPlan retrieves a migration plan by its ID.
	LoadPlan(ctx context.Context, planID string) (*MigrationPlan, error)
	// ListPlans returns a summary of all stored plans, newest first.
	ListPlans(ctx context.Context) ([]*MigrationPlanSummary, error)
	// DeletePlan removes a migration plan by its ID.
	DeletePlan(ctx context.Context, planID string) error
}

// SQLitePlanStore stores migration plans in SQLite as JSON.
type SQLitePlanStore struct {
	db *sql.DB
}

// NewSQLitePlanStore creates a new SQLitePlanStore.
// The caller must call EnsureTable() before using the store.
func NewSQLitePlanStore(db *sql.DB) *SQLitePlanStore {
	return &SQLitePlanStore{db: db}
}

// EnsureTable creates the migration_plans table if it doesn't exist.
// It also enables WAL mode for better concurrent read/write performance.
func (s *SQLitePlanStore) EnsureTable() error {
	// Enable WAL mode
	_, err := s.db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("enable WAL mode: %w", err)
	}

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS migration_plans (
		id          TEXT PRIMARY KEY,
		plan        TEXT NOT NULL,
		source_host TEXT NOT NULL,
		target_host TEXT NOT NULL,
		step_count  INTEGER NOT NULL,
		risk_level  TEXT NOT NULL,
		has_blockers INTEGER NOT NULL DEFAULT 0,
		created_at  DATETIME NOT NULL
	);`)
	if err != nil {
		return fmt.Errorf("create migration_plans table: %w", err)
	}

	// Create index for listing by creation date
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_migration_plans_created_at
		ON migration_plans(created_at DESC);`)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	return nil
}

// SavePlan stores a migration plan.
func (s *SQLitePlanStore) SavePlan(ctx context.Context, plan *MigrationPlan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if plan.ID == "" {
		return fmt.Errorf("plan ID is empty")
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}

	hasBlockers := 0
	if plan.HasBlockers() {
		hasBlockers = 1
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO migration_plans (id, plan, source_host, target_host, step_count, risk_level, has_blockers, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   plan = excluded.plan,
		   source_host = excluded.source_host,
		   target_host = excluded.target_host,
		   step_count = excluded.step_count,
		   risk_level = excluded.risk_level,
		   has_blockers = excluded.has_blockers,
		   created_at = excluded.created_at`,
		plan.ID, string(data), plan.Source.Hostname, plan.Target.Hostname,
		plan.StepCount(), string(plan.RiskLevel), hasBlockers, plan.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("save plan: %w", err)
	}

	return nil
}

// LoadPlan retrieves a migration plan by its ID.
func (s *SQLitePlanStore) LoadPlan(ctx context.Context, planID string) (*MigrationPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT plan FROM migration_plans WHERE id = ?`, planID,
	).Scan(&data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("plan not found: %s", planID)
		}
		return nil, fmt.Errorf("load plan: %w", err)
	}

	var plan MigrationPlan
	if err := json.Unmarshal([]byte(data), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	return &plan, nil
}

// ListPlans returns a summary of all stored plans, newest first.
func (s *SQLitePlanStore) ListPlans(ctx context.Context) ([]*MigrationPlanSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source_host, target_host, step_count, risk_level, has_blockers, created_at
		 FROM migration_plans ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	summaries := make([]*MigrationPlanSummary, 0)
	for rows.Next() {
		var s MigrationPlanSummary
		var hasBlockers int
		var createdAtStr string
		if err := rows.Scan(&s.ID, &s.Source, &s.Target, &s.StepCount, &s.RiskLevel, &hasBlockers, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan plan summary: %w", err)
		}
		s.HasBlockers = hasBlockers != 0
		// Parse the created_at timestamp
		if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
			s.CreatedAt = t
		} else if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			s.CreatedAt = t
		} else {
			s.CreatedAt = time.Now() // fallback
		}
		summaries = append(summaries, &s)
	}

	return summaries, nil
}

// DeletePlan removes a migration plan by its ID.
func (s *SQLitePlanStore) DeletePlan(ctx context.Context, planID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM migration_plans WHERE id = ?`, planID)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("plan not found: %s", planID)
	}

	return nil
}

// --- NoopPlanStore ---

// NoopPlanStore is a no-op implementation for testing.
type NoopPlanStore struct{}

func NewNoopPlanStore() *NoopPlanStore { return &NoopPlanStore{} }

func (s *NoopPlanStore) SavePlan(ctx context.Context, plan *MigrationPlan) error { return nil }
func (s *NoopPlanStore) LoadPlan(ctx context.Context, planID string) (*MigrationPlan, error) {
	return nil, fmt.Errorf("plan not found (noop store)")
}
func (s *NoopPlanStore) ListPlans(ctx context.Context) ([]*MigrationPlanSummary, error) {
	return []*MigrationPlanSummary{}, nil
}
func (s *NoopPlanStore) DeletePlan(ctx context.Context, planID string) error { return nil }
