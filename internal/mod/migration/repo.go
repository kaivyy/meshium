package migration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Repo interface {
	CreateMigration(sourceID, targetID int, categories []string) (int, error)
	GetMigration(id int) (*Migration, error)
	ListMigrations() ([]Migration, error)
	UpdateMigrationStatus(id int, status string) error
	SetMigrationPlan(id int, plan MigrationPlan) error
	SetMigrationError(id int, errMsg string) error
	SetMigrationCompleted(id int) error
	DeleteMigration(id int) error

	CreateStep(migrationID int, category, action string) (int, error)
	UpdateStepStatus(id int, status, output, errMsg string) error
	ListSteps(migrationID int) ([]MigrationStep, error)

	CreateBackup(migrationID, serverID int, category, backupPath, backupType string) error
	ListBackups(migrationID int) ([]MigrationBackup, error)
}

type sqliteRepo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) Repo {
	return &sqliteRepo{db: db}
}

func (r *sqliteRepo) CreateMigration(sourceID, targetID int, categories []string) (int, error) {
	cats, _ := json.Marshal(categories)
	res, err := r.db.Exec(
		`INSERT INTO migrations (source_id, target_id, categories, status) VALUES (?, ?, ?, 'pending')`,
		sourceID, targetID, string(cats),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) GetMigration(id int) (*Migration, error) {
	var m Migration
	var categoriesJSON, planJSON string
	var completedAt sql.NullString
	err := r.db.QueryRow(
		`SELECT id, source_id, target_id, categories, status, plan, error, created_at, completed_at
		 FROM migrations WHERE id = ?`, id,
	).Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &planJSON, &m.Error, &m.CreatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("migration not found")
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(categoriesJSON), &m.Categories)
	_ = planJSON // plan is loaded separately when needed
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

	var migrations []Migration
	for rows.Next() {
		var m Migration
		var categoriesJSON string
		var completedAt sql.NullString
		if err := rows.Scan(&m.ID, &m.SourceID, &m.TargetID, &categoriesJSON, &m.Status, &m.Error, &m.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(categoriesJSON), &m.Categories)
		if completedAt.Valid {
			m.CompletedAt = completedAt.String
		}
		migrations = append(migrations, m)
	}
	return migrations, nil
}

func (r *sqliteRepo) UpdateMigrationStatus(id int, status string) error {
	_, err := r.db.Exec("UPDATE migrations SET status = ? WHERE id = ?", status, id)
	return err
}

func (r *sqliteRepo) SetMigrationPlan(id int, plan MigrationPlan) error {
	planJSON, _ := json.Marshal(plan)
	_, err := r.db.Exec("UPDATE migrations SET plan = ? WHERE id = ?", string(planJSON), id)
	return err
}

func (r *sqliteRepo) SetMigrationError(id int, errMsg string) error {
	_, err := r.db.Exec("UPDATE migrations SET error = ? WHERE id = ?", errMsg, id)
	return err
}

func (r *sqliteRepo) SetMigrationCompleted(id int) error {
	_, err := r.db.Exec(
		"UPDATE migrations SET status = 'success', completed_at = CURRENT_TIMESTAMP WHERE id = ?", id,
	)
	return err
}

func (r *sqliteRepo) DeleteMigration(id int) error {
	res, err := r.db.Exec("DELETE FROM migrations WHERE id = ?", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("migration not found")
	}
	return nil
}

func (r *sqliteRepo) CreateStep(migrationID int, category, action string) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO migration_steps (migration_id, category, action, status, started_at)
		 VALUES (?, ?, ?, 'running', CURRENT_TIMESTAMP)`,
		migrationID, category, action,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) UpdateStepStatus(id int, status, output, errMsg string) error {
	_, err := r.db.Exec(
		`UPDATE migration_steps SET status = ?, output = ?, error = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, output, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) ListSteps(migrationID int) ([]MigrationStep, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, category, action, status, output, error, started_at, completed_at
		 FROM migration_steps WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []MigrationStep
	for rows.Next() {
		var s MigrationStep
		var output, errMsg, startedAt, completedAt sql.NullString
		if err := rows.Scan(&s.ID, &s.MigrationID, &s.Category, &s.Action, &s.Status, &output, &errMsg, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		if output.Valid {
			s.Output = output.String
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

func (r *sqliteRepo) CreateBackup(migrationID, serverID int, category, backupPath, backupType string) error {
	_, err := r.db.Exec(
		`INSERT INTO migration_backups (migration_id, server_id, category, backup_path, backup_type)
		 VALUES (?, ?, ?, ?, ?)`,
		migrationID, serverID, category, backupPath, backupType,
	)
	return err
}

func (r *sqliteRepo) ListBackups(migrationID int) ([]MigrationBackup, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, server_id, category, backup_path, backup_type, created_at
		 FROM migration_backups WHERE migration_id = ?`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backups []MigrationBackup
	for rows.Next() {
		var b MigrationBackup
		if err := rows.Scan(&b.ID, &b.MigrationID, &b.ServerID, &b.Category, &b.BackupPath, &b.BackupType, &b.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, nil
}

// Helper: parse categories from JSON string to slice
func parseCategories(s string) []string {
	var cats []string
	json.Unmarshal([]byte(s), &cats)
	if cats == nil {
		cats = []string{}
	}
	return cats
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
