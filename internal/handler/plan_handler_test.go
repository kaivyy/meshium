package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/planner"
)

// --- Mock implementations ---

type mockPlanStore struct {
	plans map[string]*planner.MigrationPlan
}

func newMockPlanStore() *mockPlanStore {
	return &mockPlanStore{plans: make(map[string]*planner.MigrationPlan)}
}

func (m *mockPlanStore) SavePlan(_ context.Context, plan *planner.MigrationPlan) error {
	m.plans[plan.ID] = plan
	return nil
}
func (m *mockPlanStore) LoadPlan(_ context.Context, planID string) (*planner.MigrationPlan, error) {
	p, ok := m.plans[planID]
	if !ok {
		return nil, errPlanNotFound
	}
	return p, nil
}
func (m *mockPlanStore) ListPlans(_ context.Context) ([]*planner.MigrationPlanSummary, error) {
	summaries := make([]*planner.MigrationPlanSummary, 0, len(m.plans))
	for _, p := range m.plans {
		summaries = append(summaries, &planner.MigrationPlanSummary{
			ID:          p.ID,
			CreatedAt:   p.CreatedAt,
			Source:      p.Source.Hostname,
			Target:      p.Target.Hostname,
			StepCount:   len(p.Steps),
			RiskLevel:   p.RiskLevel,
			HasBlockers: p.HasBlockers(),
		})
	}
	return summaries, nil
}
func (m *mockPlanStore) DeletePlan(_ context.Context, planID string) error {
	if _, ok := m.plans[planID]; !ok {
		return errPlanNotFound
	}
	delete(m.plans, planID)
	return nil
}

var errPlanNotFound = fmt.Errorf("plan not found")

type mockSnapshotStore struct {
	snapshots map[int]*discovery.ServerSnapshot
}

func newMockSnapshotStore() *mockSnapshotStore {
	return &mockSnapshotStore{snapshots: make(map[int]*discovery.ServerSnapshot)}
}

func (m *mockSnapshotStore) SaveSnapshot(serverID int, snapshot *discovery.ServerSnapshot) error {
	m.snapshots[serverID] = snapshot
	return nil
}
func (m *mockSnapshotStore) LoadSnapshot(serverID int) (*discovery.ServerSnapshot, error) {
	s, ok := m.snapshots[serverID]
	if !ok {
		return nil, errSnapshotNotFound
	}
	return s, nil
}
func (m *mockSnapshotStore) LoadSnapshotAt(serverID int, _ time.Time) (*discovery.ServerSnapshot, error) {
	return m.LoadSnapshot(serverID)
}
func (m *mockSnapshotStore) DeleteSnapshot(serverID int) error {
	delete(m.snapshots, serverID)
	return nil
}

var errSnapshotNotFound = fmt.Errorf("snapshot not found")

// --- Tests ---

func TestPlanHandler_ListPlans(t *testing.T) {
	engine := newTestEngine(t)
	planStore := newMockPlanStore()
	planStore.plans["plan-1"] = &planner.MigrationPlan{
		ID:        "plan-1",
		CreatedAt: time.Now(),
		Source:    planner.ServerSummary{Hostname: "source-host"},
		Target:    planner.ServerSummary{Hostname: "target-host"},
		Steps:     []planner.PlannedStep{{Order: 0}, {Order: 1}},
		RiskLevel: planner.RiskLow,
	}

	h := NewPlanHandler(nil, planStore, newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/plans", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 plan, got %d", len(result))
	}
}

func TestPlanHandler_GetPlan_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewPlanHandler(nil, newMockPlanStore(), newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/plans/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestPlanHandler_CreatePlan_MissingIDs(t *testing.T) {
	engine := newTestEngine(t)
	h := NewPlanHandler(nil, newMockPlanStore(), newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/plans", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestPlanHandler_CreatePlan_SnapshotNotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewPlanHandler(nil, newMockPlanStore(), newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"sourceID": 1, "targetID": 2}`
	req := httptest.NewRequest(http.MethodPost, "/api/plans", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", w.Code)
	}
}

func TestPlanHandler_DeletePlan(t *testing.T) {
	engine := newTestEngine(t)
	planStore := newMockPlanStore()
	planStore.plans["plan-1"] = &planner.MigrationPlan{ID: "plan-1"}

	h := NewPlanHandler(nil, planStore, newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/plans/plan-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

func TestPlanHandler_DeletePlan_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewPlanHandler(nil, newMockPlanStore(), newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/plans/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestPlanHandler_ExecutePlan_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewPlanHandler(nil, newMockPlanStore(), newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/plans/nonexistent/execute", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}
