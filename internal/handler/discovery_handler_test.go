package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"meshium/internal/mod/discovery"
)

func TestDiscoveryHandler_GetSnapshot_NotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/servers/999/snapshot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestDiscoveryHandler_GetSnapshot_Success(t *testing.T) {
	engine := newTestEngine(t)
	snapshotStore := newMockSnapshotStore()
	snapshotStore.snapshots[1] = &discovery.ServerSnapshot{
		OS: discovery.OSInfo{
			Hostname: "test-server",
			Distro:   "Ubuntu 22.04",
		},
	}

	h := NewDiscoveryHandler(snapshotStore, engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/servers/1/snapshot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestDiscoveryHandler_TriggerDiscovery(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/servers/1/discover", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}
}

func TestDiscoveryHandler_Compat_MissingParams(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/compat", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestDiscoveryHandler_Compat_InvalidIDs(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/compat?source=abc&target=def", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestDiscoveryHandler_Compat_SnapshotNotFound(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/compat?source=1&target=2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", w.Code)
	}
}

func TestDiscoveryHandler_Compat_Success(t *testing.T) {
	engine := newTestEngine(t)
	snapshotStore := newMockSnapshotStore()
	snapshotStore.snapshots[1] = &discovery.ServerSnapshot{
		OS:       discovery.OSInfo{Hostname: "source"},
		Hardware: discovery.HardwareInfo{RAMTotalMB: 4096, DiskTotalGB: 100},
	}
	snapshotStore.snapshots[2] = &discovery.ServerSnapshot{
		OS:       discovery.OSInfo{Hostname: "target"},
		Hardware: discovery.HardwareInfo{RAMTotalMB: 8192, DiskTotalGB: 200},
	}

	h := NewDiscoveryHandler(snapshotStore, engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/compat?source=1&target=2", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestDiscoveryHandler_MethodNotAllowed_Snapshot(t *testing.T) {
	engine := newTestEngine(t)
	h := NewDiscoveryHandler(newMockSnapshotStore(), engine)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/servers/1/snapshot", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}
