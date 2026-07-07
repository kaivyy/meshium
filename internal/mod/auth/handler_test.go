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
	sm := NewSessionManager()
	h := NewHandler(svc, sm)

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

	var setupResp AuthResponse
	json.NewDecoder(w.Body).Decode(&setupResp)
	if setupResp.SessionToken == "" {
		t.Fatal("expected session token in setup response")
	}

	// GET /api/auth/status WITHOUT the token — a tokenless caller must see
	// locked=true even though the app is globally unlocked, so an
	// unauthenticated browser is routed to the login screen.
	req = httptest.NewRequest("GET", "/api/auth/status", nil)
	w = httptest.NewRecorder()
	h.handleStatus(w, req)

	json.NewDecoder(w.Body).Decode(&status)
	if !status.Setup {
		t.Error("should be setup after setup()")
	}
	if !status.Locked {
		t.Error("tokenless status check should report locked, even when unlocked")
	}

	// GET /api/auth/status WITH the setup token — this client holds the
	// session token, so it sees the true unlocked state.
	req = httptest.NewRequest("GET", "/api/auth/status", nil)
	req.Header.Set("Authorization", "Bearer "+setupResp.SessionToken)
	w = httptest.NewRecorder()
	h.handleStatus(w, req)

	json.NewDecoder(w.Body).Decode(&status)
	if status.Locked {
		t.Error("should be unlocked after setup() when the token is presented")
	}
}

func TestUnlockLockEndpoints(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	sm := NewSessionManager()
	h := NewHandler(svc, sm)

	if err := svc.Setup("test-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
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

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode unlock response: %v", err)
	}
	if resp["sessionToken"] == "" {
		t.Fatal("expected session token in unlock response")
	}
	if !sm.ValidateSession(resp["sessionToken"]) {
		t.Fatal("expected unlock token to be tracked by session manager")
	}

	// POST /api/auth/lock
	req = httptest.NewRequest("POST", "/api/auth/lock", nil)
	req.Header.Set("X-Session-Token", resp["sessionToken"])
	w = httptest.NewRecorder()
	h.handleLock(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if sm.ValidateSession(resp["sessionToken"]) {
		t.Fatal("expected session to be removed after lock")
	}
}

func TestSSHKeyEndpoints(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	sm := NewSessionManager()
	h := NewHandler(svc, sm)

	if err := svc.Setup("test-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/ssh-key/public", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var publicResp SSHKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&publicResp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	storedPublic, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if publicResp.PublicKey != storedPublic {
		t.Fatalf("expected public key %q, got %q", storedPublic, publicResp.PublicKey)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/ssh-key/regenerate", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var regenerated SSHKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&regenerated); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if regenerated.PublicKey == publicResp.PublicKey {
		t.Fatal("expected regenerated SSH key to change")
	}

	storedPublic, err = repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if storedPublic != regenerated.PublicKey {
		t.Fatalf("expected stored public key to match regenerated key")
	}
}
