package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubRunner is a no-op MigrationRunner — sufficient to exercise the handler's
// reconcile/dedup paths without a real planner.
type stubRunner struct{}

func (stubRunner) Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error) {
	return nil, nil
}
func (stubRunner) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error { return nil }
func (stubRunner) Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error { return nil }
func (stubRunner) PreFlight(ctx context.Context, migrationID int, onProgress StepCallback) (*PreFlightResult, error) {
	return nil, nil
}
func (stubRunner) DryRun(ctx context.Context, migrationID int, onProgress StepCallback) (*DryRunResult, error) {
	return nil, nil
}
func (stubRunner) Diff(ctx context.Context, sourceID, targetID int, categories []string, onProgress StepCallback) (*DiffResult, error) {
	return nil, nil
}
func (stubRunner) Parity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error) {
	return nil, nil
}
func (stubRunner) ParitySummary(ctx context.Context, migrationID int) (*ParitySummary, error) {
	return nil, nil
}
func (stubRunner) BulkApply(ctx context.Context, migrationID int, policy BulkPolicy) (*BulkResult, error) {
	return nil, nil
}

func newTestHandler() (*Handler, *mockRepo) {
	repo := &mockRepo{}
	h := NewHandler(stubRunner{}, repo)
	return h, repo
}

// TestReconcileByOperationID returns the existing migration (rule 4: refresh /
// reconnect / retry that lost the terminal frame reconciles to the SAME plan,
// never a blank list). The backend is authoritative for completion.
func TestReconcileByOperationID(t *testing.T) {
	h, repo := newTestHandler()
	id, err := repo.CreateMigration(1, 2, []string{"packages"}, "op-reconcile-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/migrations?operationId=op-reconcile-1", nil)
	w := httptest.NewRecorder()
	h.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var list []Migration
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected exactly 1 reconciliation, got %d", len(list))
	}
	if list[0].ID != id {
		t.Fatalf("expected id %d, got %d", id, list[0].ID)
	}
	if list[0].OperationID != "op-reconcile-1" {
		t.Fatalf("operation id not echoed: %q", list[0].OperationID)
	}
}

// TestReconcileByOperationIDMissing returns an empty list (not an error), so the
// FE can render UNKNOWN honestly.
func TestReconcileByOperationIDMissing(t *testing.T) {
	h, _ := newTestHandler()
	req := httptest.NewRequest("GET", "/api/migrations?operationId=op-does-not-exist", nil)
	w := httptest.NewRecorder()
	h.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" && !strings.Contains(w.Body.String(), "[]") {
		t.Fatalf("expected empty array, got %q", w.Body.String())
	}
}

// TestDedupExcludesFailed ensures a retried op that FAILED creates a fresh plan
// rather than returning the dead one: handlePlanWS only dedups planned/running/
// interrupted. (Here we assert the repo-level contract it relies on: distinct
// creates with the same op id are both kept, newest-first.)
func TestDedupExcludesFailed(t *testing.T) {
	repo := &mockRepo{}
	a, _ := repo.CreateMigration(1, 2, []string{"packages"}, "op-retry")
	repo.UpdateMigrationStatus(a, StatusFailed, "boom")
	b, _ := repo.CreateMigration(1, 2, []string{"packages"}, "op-retry") // retry → fresh plan
	if a == b {
		t.Fatalf("retry after failure must create a new migration id")
	}
	// Reconcile sees the NEWEST (the fresh retry), not the failed one.
	got, err := repo.GetMigrationByOperationID("op-retry")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got.ID != b {
		t.Fatalf("expected reconcile to newest id %d, got %d", b, got.ID)
	}
	if got.Status == StatusFailed {
		t.Fatalf("reconcile must not resolve to the dead failed plan")
	}
}

// TestDedupReturnsExistingForInFlight asserts the contract handlePlanWS depends
// on: an op whose migration is still planned/running/interrupted reconciles to
// that SAME row, so a reconnect never duplicates.
func TestDedupReturnsExistingForInFlight(t *testing.T) {
	repo := &mockRepo{}
	id, _ := repo.CreateMigration(1, 2, []string{"packages"}, "op-inflight")
	repo.UpdateMigrationStatus(id, StatusRunning, "")
	got, err := repo.GetMigrationByOperationID("op-inflight")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got.ID != id {
		t.Fatalf("expected same id %d, got %d", id, got.ID)
	}
}
