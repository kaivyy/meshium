package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"meshium/internal/jobengine"
)

// newTestEngine creates a minimal Engine for testing HTTP handlers.
// It uses in-memory queue, noop store, and default broadcaster.
func newTestEngine(t *testing.T) *jobengine.Engine {
	t.Helper()
	broadcaster := jobengine.NewDefaultProgressBroadcaster()
	engine := jobengine.NewEngine(jobengine.EngineConfig{
		Queue:          jobengine.NewInMemoryJobQueue(),
		Store:          jobengine.NewNoopJobStore(),
		Broadcaster:    broadcaster,
		HandlerFactory: jobengine.HandlerFactoryFunc(func(job *jobengine.Job) (jobengine.JobHandler, error) {
			return nil, nil // not needed for HTTP handler tests
		}),
		MaxWorkers: 1,
	})
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("failed to start engine: %v", err)
	}
	t.Cleanup(func() {
		_ = engine.Stop(context.Background())
	})
	return engine
}

func TestJobHandler_ListJobs(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var jobs []*jobengine.Job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	// NoopJobStore returns empty list
	if len(jobs) != 0 {
		t.Errorf("expected empty job list, got %d jobs", len(jobs))
	}
}

func TestJobHandler_GetJob_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestJobHandler_Submit_InvalidBody(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestJobHandler_Submit_MissingType(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"sourceId": 1}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestJobHandler_Submit_InvalidType(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"type": "invalid_type"}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestJobHandler_Submit_Discovery_MissingSourceID(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"type": "discovery"}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestJobHandler_MethodNotAllowed(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPut, "/api/jobs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestJobHandler_GetLogs_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewJobHandler(engine, jobengine.NewNoopJobStore())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/test-id/logs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// NoopJobStore.GetLogs returns empty list, not error
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
