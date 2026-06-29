package migration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockRepo implements Repo for testing.
type mockRepo struct {
	migrations []Migration
	steps       []MigrationStepRecord
	backups     []MigrationBackup
}

func (m *mockRepo) CreateMigration(sourceID, targetID int, categories []string) (int, error) {
	id := len(m.migrations) + 1
	m.migrations = append(m.migrations, Migration{
		ID:         id,
		SourceID:   sourceID,
		TargetID:   targetID,
		Categories: `[` + strings.Join(categories, `,`) + `]`,
		Status:     StatusPlanned,
	})
	return id, nil
}

func (m *mockRepo) GetMigration(id int) (*Migration, error) {
	for _, p := range m.migrations {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, ErrMigrationNotFound
}

func (m *mockRepo) ListMigrations() ([]Migration, error) {
	return m.migrations, nil
}

func (m *mockRepo) UpdateMigrationStatus(id int, status, errMsg string) error {
	for i := range m.migrations {
		if m.migrations[i].ID == id {
			m.migrations[i].Status = status
			m.migrations[i].Error = errMsg
			return nil
		}
	}
	return ErrMigrationNotFound
}

func (m *mockRepo) SetMigrationPlan(id int, plan MigrationPlan) error { return nil }
func (m *mockRepo) SetMigrationCompletedAt(id int, ts string) error   { return nil }
func (m *mockRepo) SetMigrationRolledBackAt(id int, ts string) error  { return nil }

func (m *mockRepo) DeleteMigration(id int) error {
	for i, p := range m.migrations {
		if p.ID == id {
			m.migrations = append(m.migrations[:i], m.migrations[i+1:]...)
			return nil
		}
	}
	return ErrMigrationNotFound
}

func (m *mockRepo) CreateStep(migrationID int, category, action, data string) (int, error) {
	id := len(m.steps) + 1
	m.steps = append(m.steps, MigrationStepRecord{
		ID:          id,
		MigrationID: migrationID,
		Category:    category,
		Action:      action,
		Status:      StepStatusCompleted,
		Data:        data,
	})
	return id, nil
}

func (m *mockRepo) UpdateStepStatus(stepID int, status, errMsg string) error { return nil }

func (m *mockRepo) GetSteps(migrationID int) ([]MigrationStepRecord, error) {
	var result []MigrationStepRecord
	for _, s := range m.steps {
		if s.MigrationID == migrationID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockRepo) CreateBackup(migrationID, serverID int, category, data string) (int, error) {
	id := len(m.backups) + 1
	m.backups = append(m.backups, MigrationBackup{
		ID:          id,
		MigrationID: migrationID,
		ServerID:    serverID,
		Category:    category,
		Data:        data,
	})
	return id, nil
}

func (m *mockRepo) GetBackups(migrationID int) ([]MigrationBackup, error) {
	var result []MigrationBackup
	for _, b := range m.backups {
		if b.MigrationID == migrationID {
			result = append(result, b)
		}
	}
	return result, nil
}

func (m *mockRepo) GetAppliedCategories(migrationID int) ([]string, error) {
	var result []string
	for _, s := range m.steps {
		if s.MigrationID == migrationID && s.Action == "collect" && s.Status == StepStatusApplied {
			result = append(result, s.Category)
		}
	}
	return result, nil
}

func TestHandleList(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, SourceID: 1, TargetID: 2, Status: StatusPlanned},
		},
	}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("GET", "/api/migrations", nil)
	w := httptest.NewRecorder()
	handler.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGetNotFound(t *testing.T) {
	repo := &mockRepo{}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("GET", "/api/migrations/999", nil)
	w := httptest.NewRecorder()
	handler.handleGet(w, req, 999)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleDelete(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StatusPlanned},
		},
	}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("DELETE", "/api/migrations/1", nil)
	w := httptest.NewRecorder()
	handler.handleDelete(w, req, 1)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCreateValidation(t *testing.T) {
	repo := &mockRepo{}
	handler := NewHandler(nil, repo)

	// Missing sourceServerId
	body := `{"targetServerId":2,"categories":["packages"]}`
	req := httptest.NewRequest("POST", "/api/migrations", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.handleCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGetSteps(t *testing.T) {
	repo := &mockRepo{
		steps: []MigrationStepRecord{
			{ID: 1, MigrationID: 1, Category: "packages", Action: "collect", Status: StepStatusCompleted},
		},
	}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("GET", "/api/migrations/1/steps", nil)
	w := httptest.NewRecorder()
	handler.handleGetSteps(w, req, 1)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
