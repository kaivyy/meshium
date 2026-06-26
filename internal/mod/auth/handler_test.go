package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupEndpoint(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	h := NewHandler(svc)

	// GET /api/auth/status — should show setup=false
	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	h.handleStatus(w, req)

	var status AuthStatus
	json.NewDecoder(w.Body).Decode(&status)
	if status.Setup {
		t.Error("should not be setup initially")
	}
	if !status.Locked {
		t.Error("should be locked initially")
	}

	// POST /api/auth/setup
	body, _ := json.Marshal(SetupRequest{Password: "test-password"})
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w = httptest.NewRecorder()
	h.handleSetup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// GET /api/auth/status — should show setup=true, locked=false
	req = httptest.NewRequest("GET", "/api/auth/status", nil)
	w = httptest.NewRecorder()
	h.handleStatus(w, req)

	json.NewDecoder(w.Body).Decode(&status)
	if !status.Setup {
		t.Error("should be setup after setup()")
	}
	if status.Locked {
		t.Error("should be unlocked after setup()")
	}
}

func TestUnlockLockEndpoints(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	h := NewHandler(svc)

	svc.Setup("test-password")
	svc.Lock()

	// POST /api/auth/unlock — wrong password
	body, _ := json.Marshal(UnlockRequest{Password: "wrong"})
	req := httptest.NewRequest("POST", "/api/auth/unlock", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleUnlock(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", w.Code)
	}

	// POST /api/auth/unlock — correct password
	body, _ = json.Marshal(UnlockRequest{Password: "test-password"})
	req = httptest.NewRequest("POST", "/api/auth/unlock", bytes.NewReader(body))
	w = httptest.NewRecorder()
	h.handleUnlock(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// POST /api/auth/lock
	req = httptest.NewRequest("POST", "/api/auth/lock", nil)
	w = httptest.NewRecorder()
	h.handleLock(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
