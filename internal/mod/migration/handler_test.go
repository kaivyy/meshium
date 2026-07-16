package migration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockRepo implements Repo for testing.
type mockRepo struct {
	migrations []Migration
	steps      []MigrationStepRecord
	backups    []MigrationBackup
	selections []SelectionDecision
}

func (m *mockRepo) CreateMigration(sourceID, targetID int, categories []string, operationID string) (int, error) {
	id := len(m.migrations) + 1
	m.migrations = append(m.migrations, Migration{
		ID:         id,
		SourceID:   sourceID,
		TargetID:   targetID,
		Categories:  categories,
		Status:      StatusPlanned,
		OperationID: operationID,
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

func (m *mockRepo) GetMigrationByOperationID(operationID string) (*Migration, error) {
	// Mirror the SQL `ORDER BY id DESC LIMIT 1`: a retried op that created a
	// fresh (failed→new) row must reconcile to the NEWEST, not the dead one.
	var found *Migration
	for i := range m.migrations {
		if m.migrations[i].OperationID == operationID {
			cp := m.migrations[i]
			found = &cp
		}
	}
	if found == nil {
		return nil, ErrMigrationNotFound
	}
	return found, nil
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

func (m *mockRepo) TryUpdateMigrationStatus(id int, expectedStatus, newStatus, errMsg string) (bool, error) {
	for i := range m.migrations {
		if m.migrations[i].ID == id && m.migrations[i].Status == expectedStatus {
			m.migrations[i].Status = newStatus
			m.migrations[i].Error = errMsg
			return true, nil
		}
	}
	return false, nil
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

func (m *mockRepo) GetLatestStep(migrationID int, action string) (*MigrationStepRecord, error) {
	var latest *MigrationStepRecord
	for i := range m.steps {
		s := &m.steps[i]
		if s.MigrationID == migrationID && s.Action == action && s.Status == StepStatusCompleted {
			latest = s
		}
	}
	return latest, nil
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

// --- Phase 6B selection persistence mocks (in-memory) ---

func (m *mockRepo) UpsertSelection(ctx context.Context, migrationID int, itemKey, category, action string) error {
	for i := range m.selections {
		if m.selections[i].MigrationID == migrationID && m.selections[i].ItemKey == itemKey {
			m.selections[i].Category = category
			m.selections[i].Action = action
			return nil
		}
	}
	m.selections = append(m.selections, SelectionDecision{MigrationID: migrationID, ItemKey: itemKey, Category: category, Action: action})
	return nil
}

func (m *mockRepo) GetSelections(ctx context.Context, migrationID int) ([]SelectionDecision, error) {
	var out []SelectionDecision
	for _, s := range m.selections {
		if s.MigrationID == migrationID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *mockRepo) GetSelection(ctx context.Context, migrationID int, itemKey string) (string, error) {
	for _, s := range m.selections {
		if s.MigrationID == migrationID && s.ItemKey == itemKey {
			return s.Action, nil
		}
	}
	return "", nil
}

func (m *mockRepo) ClearSelections(ctx context.Context, migrationID int) error {
	var kept []SelectionDecision
	for _, s := range m.selections {
		if s.MigrationID != migrationID {
			kept = append(kept, s)
		}
	}
	m.selections = kept
	return nil
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
