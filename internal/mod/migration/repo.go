package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrMigrationNotFound is a typed error returned when a migration is not found.
var ErrMigrationNotFound = errors.New("migration not found")

// IsMigrationNotFound returns true if err is or wraps ErrMigrationNotFound.
func IsMigrationNotFound(err error) bool {
	return errors.Is(err, ErrMigrationNotFound)
}

type Repo interface {
	CreateMigration(sourceID, targetID int, categories []string, operationID string) (int, error)
	GetMigration(id int) (*Migration, error)
	GetMigrationByOperationID(operationID string) (*Migration, error)
	ListMigrations() ([]Migration, error)
	UpdateMigrationStatus(id int, status, errMsg string) error
	// TryUpdateMigrationStatus atomically updates the status only if the
	// current status matches expectedStatus. Returns true if the update
	// was applied, false if the current status did not match.
	TryUpdateMigrationStatus(id int, expectedStatus, newStatus, errMsg string) (bool, error)
	SetMigrationPlan(id int, plan MigrationPlan) error
	SetMigrationCompletedAt(id int, ts string) error
	SetMigrationRolledBackAt(id int, ts string) error
	DeleteMigration(id int) error

	CreateStep(migrationID int, category, action, data string) (int, error)
	UpdateStepStatus(id int, status, errMsg string) error
	GetSteps(migrationID int) ([]MigrationStepRecord, error)
	// GetLatestStep returns the most recently completed step for a migration
	// matching the given action, or nil if none exists. Used to restore
	// persisted previews (e.g. dry-run results) across page refreshes.
	GetLatestStep(migrationID int, action string) (*MigrationStepRecord, error)
	GetAppliedCategories(migrationID int) ([]string, error) // categories with StepStatusApplied

	// --- Phase 6B selective-apply selection persistence ---
	// UpsertSelection records (or updates) the operator's decision for one item.
	// reason/riskAck/manualFollowup are persisted when provided (Phase 6C-BE).
	UpsertSelection(ctx context.Context, migrationID int, itemKey, category, action, reason string, riskAck bool, manualFollowup string) error
	// GetSelections returns all recorded decisions for a migration.
	GetSelections(ctx context.Context, migrationID int) ([]SelectionDecision, error)
	// GetSelection returns the decision for a single item, or "" if none.
	GetSelection(ctx context.Context, migrationID int, itemKey string) (string, error)
	// ClearSelections removes all decisions for a migration (used on reset).
	ClearSelections(ctx context.Context, migrationID int) error

	// --- Phase 6C-BE per-item execution + verification evidence ---
	// UpsertItemResult records (or updates) the per-item execution/verification
	// evidence row keyed by (migration_id, item_key).
	UpsertItemResult(ctx context.Context, migrationID int, res ItemResult) error
	// GetItemResults returns all per-item result rows for a migration.
	GetItemResults(ctx context.Context, migrationID int) ([]ItemResult, error)
	// GetItemResult returns the result for a single item key, or false if none.
	GetItemResult(ctx context.Context, migrationID int, itemKey string) (ItemResult, bool, error)
	// AppendSelectionHistory appends one immutable decision-change audit row.
	AppendSelectionHistory(ctx context.Context, migrationID int, h SelectionHistory) error
	// GetSelectionHistory returns the decision-change audit rows for a migration.
	GetSelectionHistory(ctx context.Context, migrationID int) ([]SelectionHistory, error)

	CreateBackup(migrationID, serverID int, category, data string) (int, error)
	GetBackups(migrationID int) ([]MigrationBackup, error)
}

type sqliteRepo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) Repo {
	return &sqliteRepo{db: db}
}

func (r *sqliteRepo) CreateMigration(sourceID, targetID int, categories []string, operationID string) (int, error) {
	cats, err := json.Marshal(categories)
	if err != nil {
		return 0, fmt.Errorf("marshal categories: %w", err)
	}
	res, err := r.db.Exec(
		`INSERT INTO migrations (source_id, target_id, categories, status, operation_id) VALUES (?, ?, ?, 'planned', ?)`,
		sourceID, targetID, string(cats), operationID,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// GetMigrationByOperationID returns the migration carrying the given client
// idempotency key, or ErrMigrationNotFound. Used to dedup a retried/resumed
// create-plan so a refresh never inserts a second migration.
func (r *sqliteRepo) GetMigrationByOperationID(operationID string) (*Migration, error) {
	var m Migration
	var categoriesJSON string
	var planJSON, errStr sql.NullString
	var completedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, source_id, target_id, categories, status, plan, error, created_at, completed_at, operation_id
		 FROM migrations WHERE operation_id = ? ORDER BY id DESC LIMIT 1`,
		operationID,
	).Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &planJSON, &errStr, &m.CreatedAt, &completedAt, &m.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMigrationNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(categoriesJSON), &m.Categories); err != nil {
		return nil, fmt.Errorf("unmarshal migration categories: %w", err)
	}
	if planJSON.Valid {
		var plan MigrationPlan
		if err := json.Unmarshal([]byte(planJSON.String), &plan); err != nil {
			return nil, fmt.Errorf("unmarshal migration plan: %w", err)
		}
		m.Plan = &plan
	}
	if errStr.Valid {
		m.Error = errStr.String
	}
	if completedAt.Valid {
		m.CompletedAt = completedAt.String
	}
	return &m, nil
}

func (r *sqliteRepo) SetMigrationCategories(id int, categories []string) error {
	cats, err := json.Marshal(categories)
	if err != nil {
		return fmt.Errorf("marshal categories: %w", err)
	}
	_, err = r.db.Exec(`UPDATE migrations SET categories = ? WHERE id = ?`, string(cats), id)
	return err
}

func (r *sqliteRepo) GetMigration(id int) (*Migration, error) {
	var m Migration
	var categoriesJSON string
	var planJSON, errStr sql.NullString
	var completedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, source_id, target_id, categories, status, plan, error, created_at, completed_at, operation_id
		 FROM migrations WHERE id = ?`, id,
	).Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &planJSON, &errStr, &m.CreatedAt, &completedAt, &m.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMigrationNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(categoriesJSON), &m.Categories); err != nil {
		return nil, fmt.Errorf("unmarshal migration categories: %w", err)
	}
	if planJSON.Valid {
		var plan MigrationPlan
		if err := json.Unmarshal([]byte(planJSON.String), &plan); err != nil {
			return nil, fmt.Errorf("unmarshal migration plan: %w", err)
		}
		m.Plan = &plan
	}
	if errStr.Valid {
		m.Error = errStr.String
	}
	if completedAt.Valid {
		m.CompletedAt = completedAt.String
	}
	return &m, nil
}

func (r *sqliteRepo) ListMigrations() ([]Migration, error) {
	rows, err := r.db.Query(
		`SELECT id, source_id, target_id, categories, status, error, created_at, completed_at
		 FROM migrations ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	migrations := make([]Migration, 0)
	for rows.Next() {
		var m Migration
		var categoriesJSON string
		var completedAt sql.NullString
		var errorStr sql.NullString
		// The error column is NULL for migrations that never failed; scanning it
		// directly into a string fails with "converting NULL to string". Scan
		// into sql.NullString and let NullString.String yield "" when invalid —
		// matching GetMigration above.
		if err := rows.Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &errorStr, &m.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		m.Error = errorStr.String
		if err := json.Unmarshal([]byte(categoriesJSON), &m.Categories); err != nil {
			return nil, fmt.Errorf("unmarshal migration categories: %w", err)
		}
		if completedAt.Valid {
			m.CompletedAt = completedAt.String
		}
		migrations = append(migrations, m)
	}
	return migrations, nil
}

func (r *sqliteRepo) UpdateMigrationStatus(id int, status, errMsg string) error {
	_, err := r.db.Exec(
		"UPDATE migrations SET status = ?, error = ? WHERE id = ?",
		status, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) TryUpdateMigrationStatus(id int, expectedStatus, newStatus, errMsg string) (bool, error) {
	res, err := r.db.Exec(
		"UPDATE migrations SET status = ?, error = ? WHERE id = ? AND status = ?",
		newStatus, errMsg, id, expectedStatus,
	)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (r *sqliteRepo) SetMigrationPlan(id int, plan MigrationPlan) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal migration plan: %w", err)
	}
	_, err = r.db.Exec("UPDATE migrations SET plan = ? WHERE id = ?", string(planJSON), id)
	return err
}

func (r *sqliteRepo) SetMigrationCompletedAt(id int, ts string) error {
	_, err := r.db.Exec("UPDATE migrations SET completed_at = ? WHERE id = ?", ts, id)
	return err
}

func (r *sqliteRepo) SetMigrationRolledBackAt(id int, ts string) error {
	_, err := r.db.Exec("UPDATE migrations SET completed_at = ? WHERE id = ?", ts, id)
	return err
}

func (r *sqliteRepo) DeleteMigration(id int) error {
	res, err := r.db.Exec("DELETE FROM migrations WHERE id = ?", id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrMigrationNotFound
	}
	// When the last migration row is deleted, reset the AUTOINCREMENT counter
	// so the next plan starts from 1 again instead of monotonically climbing
	// (e.g. deleting the only plan #24 then creating anew yields #1, not #25).
	// sqlite_sequence only exists for AUTOINCREMENT tables; ignore "no such
	// table" defensively. Resetting only when empty avoids stealing IDs mid-set.
	var remaining int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&remaining); err != nil {
		return nil // count failure is non-fatal; the delete already succeeded
	}
	if remaining == 0 {
		_, _ = r.db.Exec("DELETE FROM sqlite_sequence WHERE name = 'migrations'")
	}
	return nil
}

func (r *sqliteRepo) CreateStep(migrationID int, category, action, data string) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO migration_steps (migration_id, category, action, status, data, started_at)
		 VALUES (?, ?, ?, 'completed', ?, CURRENT_TIMESTAMP)`,
		migrationID, category, action, data,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) UpdateStepStatus(id int, status, errMsg string) error {
	_, err := r.db.Exec(
		`UPDATE migration_steps SET status = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) GetSteps(migrationID int) ([]MigrationStepRecord, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, category, action, status, data, error, started_at, completed_at
		 FROM migration_steps WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps := make([]MigrationStepRecord, 0)
	for rows.Next() {
		var s MigrationStepRecord
		var data, errMsg, startedAt, completedAt sql.NullString
		if err := rows.Scan(&s.ID, &s.MigrationID, &s.Category, &s.Action, &s.Status, &data, &errMsg, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		if data.Valid {
			s.Data = data.String
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
		steps = append(steps, s)
	}
	return steps, nil
}

// GetLatestStep returns the most recently completed step for a migration
// matching the given action, or nil if none exists. Used to restore
// persisted previews (e.g. dry-run results) across page refreshes.
func (r *sqliteRepo) GetLatestStep(migrationID int, action string) (*MigrationStepRecord, error) {
	var s MigrationStepRecord
	var data, errMsg, startedAt, completedAt sql.NullString
	row := r.db.QueryRow(
		`SELECT id, migration_id, category, action, status, data, error, started_at, completed_at
		 FROM migration_steps
		 WHERE migration_id = ? AND action = ? AND status = 'completed'
		 ORDER BY id DESC LIMIT 1`,
		migrationID, action,
	)
	if err := row.Scan(&s.ID, &s.MigrationID, &s.Category, &s.Action, &s.Status, &data, &errMsg, &startedAt, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if data.Valid {
		s.Data = data.String
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

func (r *sqliteRepo) CreateBackup(migrationID, serverID int, category, data string) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO migration_backups (migration_id, server_id, category, backup_path, backup_type)
		 VALUES (?, ?, ?, ?, 'data')`,
		migrationID, serverID, category, data,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) GetBackups(migrationID int) ([]MigrationBackup, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, server_id, category, backup_path, created_at
		 FROM migration_backups WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backups := make([]MigrationBackup, 0)
	for rows.Next() {
		var b MigrationBackup
		if err := rows.Scan(&b.ID, &b.MigrationID, &b.ServerID, &b.Category, &b.Data, &b.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, nil
}

// GetAppliedCategories returns the list of categories that have been
// successfully applied (StepStatusApplied) for a migration. Used for
// checkpoint/resume: these categories can be skipped on resume.
func (r *sqliteRepo) GetAppliedCategories(migrationID int) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT DISTINCT category FROM migration_steps
		 WHERE migration_id = ? AND action = 'collect' AND status = ?`,
		migrationID, StepStatusApplied,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var cat string
		if err := rows.Scan(&cat); err != nil {
			return nil, err
		}
		categories = append(categories, cat)
	}
	return categories, nil
}

// --- Phase 6B selective-apply selection persistence ---

// UpsertSelection records (or updates) the operator's decision for one item.
// reason/riskAck/manualFollowup are persisted when provided (Phase 6C-BE).
// On conflict the metadata columns are also refreshed so a re-decision carries
// its new rationale; legacy rows with empty metadata stay valid (nullable).
func (r *sqliteRepo) UpsertSelection(ctx context.Context, migrationID int, itemKey, category, action, reason string, riskAck bool, manualFollowup string) error {
	_, err := r.db.Exec(
		`INSERT INTO migration_selections
		   (migration_id, item_key, category, action, decision_reason, risk_acknowledged, manual_followup, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, item_key)
		 DO UPDATE SET
		   category = excluded.category,
		   action = excluded.action,
		   decision_reason = excluded.decision_reason,
		   risk_acknowledged = excluded.risk_acknowledged,
		   manual_followup = excluded.manual_followup,
		   updated_at = CURRENT_TIMESTAMP`,
		migrationID, itemKey, category, action, reason, boolToInt(riskAck), manualFollowup,
	)
	return err
}

// GetSelections returns all recorded decisions for a migration.
func (r *sqliteRepo) GetSelections(ctx context.Context, migrationID int) ([]SelectionDecision, error) {
	rows, err := r.db.Query(
		`SELECT migration_id, item_key, category, action, decision_reason, risk_acknowledged, manual_followup, updated_at
		 FROM migration_selections WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sel := make([]SelectionDecision, 0)
	for rows.Next() {
		var d SelectionDecision
		var updatedAt string
		var riskAck int
		if err := rows.Scan(&d.MigrationID, &d.ItemKey, &d.Category, &d.Action, &d.DecisionReason, &riskAck, &d.ManualFollowup, &updatedAt); err != nil {
			return nil, err
		}
		d.RiskAcknowledged = riskAck != 0
		d.UpdatedAt = updatedAt
		sel = append(sel, d)
	}
	return sel, nil
}

// GetItemResults returns all per-item result rows for a migration.
func (r *sqliteRepo) GetItemResults(ctx context.Context, migrationID int) ([]ItemResult, error) {
	rows, err := r.db.Query(
		`SELECT migration_id, item_key, category, execution_state, last_execution_at, step_refs,
		        execution_notes, verification_state, verification_level, verify_evidence, verify_notes, backup_ref, created_at, updated_at
		 FROM migration_item_results WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ItemResult, 0)
	for rows.Next() {
		var ir ItemResult
		var lastExec, created, updated sql.NullString
		var stepRefs string
		if err := rows.Scan(&ir.MigrationID, &ir.ItemKey, &ir.Category, &ir.ExecutionState, &lastExec, &stepRefs,
			&ir.ExecutionNotes, &ir.VerificationState, &ir.VerificationLevel, &ir.VerifyEvidence, &ir.VerifyNotes, &ir.BackupRef, &created, &updated); err != nil {
			return nil, err
		}
		ir.LastExecutionAt = lastExec.String
		ir.CreatedAt = created.String
		ir.UpdatedAt = updated.String
		ir.StepRefs = parseStepRefs(stepRefs)
		out = append(out, ir)
	}
	return out, nil
}

// GetItemResult returns the result for a single item key.
func (r *sqliteRepo) GetItemResult(ctx context.Context, migrationID int, itemKey string) (ItemResult, bool, error) {
	var ir ItemResult
	var lastExec, created, updated sql.NullString
	var stepRefs string
	err := r.db.QueryRow(
		`SELECT migration_id, item_key, category, execution_state, last_execution_at, step_refs,
		        execution_notes, verification_state, verification_level, verify_evidence, verify_notes, backup_ref, created_at, updated_at
		 FROM migration_item_results WHERE migration_id = ? AND item_key = ?`,
		migrationID, itemKey,
	).Scan(&ir.MigrationID, &ir.ItemKey, &ir.Category, &ir.ExecutionState, &lastExec, &stepRefs,
		&ir.ExecutionNotes, &ir.VerificationState, &ir.VerificationLevel, &ir.VerifyEvidence, &ir.VerifyNotes, &ir.BackupRef, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemResult{}, false, nil
	}
	if err != nil {
		return ItemResult{}, false, err
	}
	ir.LastExecutionAt = lastExec.String
	ir.CreatedAt = created.String
	ir.UpdatedAt = updated.String
	ir.StepRefs = parseStepRefs(stepRefs)
	return ir, true, nil
}

// UpsertItemResult records (or updates) a per-item execution/verification row.
func (r *sqliteRepo) UpsertItemResult(ctx context.Context, migrationID int, res ItemResult) error {
	stepRefs := encodeStepRefs(res.StepRefs)
	_, err := r.db.Exec(
		`INSERT INTO migration_item_results
		   (migration_id, item_key, category, execution_state, last_execution_at, step_refs,
		    execution_notes, verification_state, verification_level, verify_evidence, verify_notes, backup_ref, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(migration_id, item_key)
		 DO UPDATE SET
		   category = excluded.category,
		   execution_state = excluded.execution_state,
		   last_execution_at = excluded.last_execution_at,
		   step_refs = excluded.step_refs,
		   execution_notes = excluded.execution_notes,
		   verification_state = excluded.verification_state,
		   verification_level = excluded.verification_level,
		   verify_evidence = excluded.verify_evidence,
		   verify_notes = excluded.verify_notes,
		   backup_ref = excluded.backup_ref,
		   updated_at = CURRENT_TIMESTAMP`,
		migrationID, res.ItemKey, res.Category, res.ExecutionState, nullIfEmpty(res.LastExecutionAt), stepRefs,
		res.ExecutionNotes, res.VerificationState, res.VerificationLevel, res.VerifyEvidence, res.VerifyNotes, res.BackupRef,
	)
	return err
}

// AppendSelectionHistory appends one immutable decision-change audit row.
func (r *sqliteRepo) AppendSelectionHistory(ctx context.Context, migrationID int, h SelectionHistory) error {
	_, err := r.db.Exec(
		`INSERT INTO migration_selection_history
		   (migration_id, item_key, from_action, to_action, reason, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		migrationID, h.ItemKey, h.FromAction, h.ToAction, h.Reason, h.Actor,
	)
	return err
}

// GetSelectionHistory returns the decision-change audit rows for a migration.
func (r *sqliteRepo) GetSelectionHistory(ctx context.Context, migrationID int) ([]SelectionHistory, error) {
	rows, err := r.db.Query(
		`SELECT migration_id, item_key, from_action, to_action, reason, actor, created_at
		 FROM migration_selection_history WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SelectionHistory, 0)
	for rows.Next() {
		var h SelectionHistory
		var created string
		if err := rows.Scan(&h.MigrationID, &h.ItemKey, &h.FromAction, &h.ToAction, &h.Reason, &h.Actor, &created); err != nil {
			return nil, err
		}
		h.CreatedAt = created
		out = append(out, h)
	}
	return out, nil
}

// GetSelection returns the decision for a single item, or "" if none.
func (r *sqliteRepo) GetSelection(ctx context.Context, migrationID int, itemKey string) (string, error) {
	var action string
	err := r.db.QueryRow(
		`SELECT action FROM migration_selections WHERE migration_id = ? AND item_key = ?`,
		migrationID, itemKey,
	).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return action, nil
}

// ClearSelections removes all decisions for a migration (used on reset).
func (r *sqliteRepo) ClearSelections(ctx context.Context, migrationID int) error {
	_, err := r.db.Exec(`DELETE FROM migration_selections WHERE migration_id = ?`, migrationID)
	return err
}

// Helper: check if a string is in a slice
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// --- Phase 6C-BE small repo helpers ---
// boolToInt already exists in pipeline_repo.go; reused.

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// parseStepRefs decodes the JSON int-array stored in step_refs. Best-effort:
// an empty/garbled value yields an empty slice rather than an error so a legacy
// or hand-edited row never breaks the scan.
func parseStepRefs(s string) []int {
	if s == "" {
		return []int{}
	}
	var refs []int
	if err := json.Unmarshal([]byte(s), &refs); err != nil {
		return []int{}
	}
	return refs
}

func encodeStepRefs(refs []int) string {
	if len(refs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(refs)
	if err != nil {
		return "[]"
	}
	return string(b)
}
