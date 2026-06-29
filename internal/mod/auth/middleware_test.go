package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionTokenGeneratedOnSetup(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if err := svc.Setup("test-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	token := svc.GetSessionToken()
	if token == "" {
		t.Error("session token should be non-empty after setup")
	}
}

func TestSessionTokenGeneratedOnUnlock(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("test-password")
	svc.Lock()

	if svc.GetSessionToken() != "" {
		t.Error("session token should be empty when locked")
	}

	token, err := svc.Unlock("test-password")
	if err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
	if token == "" {
		t.Error("Unlock should return a non-empty session token")
	}
	if svc.GetSessionToken() != token {
		t.Error("GetSessionToken should return the same token as Unlock")
	}
}

func TestSessionTokenClearedOnLock(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("test-password")
	if svc.GetSessionToken() == "" {
		t.Fatal("session token should be non-empty after setup")
	}

	svc.Lock()
	if svc.GetSessionToken() != "" {
		t.Error("session token should be cleared after Lock()")
	}
}

func TestValidateSessionToken(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("test-password")
	token := svc.GetSessionToken()

	// Valid token
	if !svc.ValidateSessionToken(token) {
		t.Error("ValidateSessionToken should return true for valid token")
	}

	// Invalid token
	if svc.ValidateSessionToken("invalid-token") {
		t.Error("ValidateSessionToken should return false for invalid token")
	}

	// Empty token
	if svc.ValidateSessionToken("") {
		t.Error("ValidateSessionToken should return false for empty token")
	}

	// After lock, no token should be valid
	svc.Lock()
	if svc.ValidateSessionToken(token) {
		t.Error("ValidateSessionToken should return false after Lock()")
	}
}

func TestSessionTokenChangesOnReunlock(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("test-password")
	token1 := svc.GetSessionToken()

	svc.Lock()
	token2, _ := svc.Unlock("test-password")

	if token1 == token2 {
		t.Error("session token should change on re-unlock")
	}
}

func TestMiddlewareAllowsExemptPaths(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// Auth endpoints should be allowed even when locked
	for _, path := range []string{"/api/auth/setup", "/api/auth/unlock", "/api/auth/status", "/api/health"} {
		handlerCalled = false
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if !handlerCalled {
			t.Errorf("handler should be called for exempt path %s", path)
		}
	}
}

func TestMiddlewareBlocksAPIWhenLocked(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	svc.Setup("test-password")
	svc.Lock()

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("handler should NOT be called when app is locked")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when locked, got %d", w.Code)
	}
}

func TestMiddlewareAllowsAPIWithoutTokenWhenUnlocked(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	svc.Setup("test-password")

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("handler should be called without token when unlocked")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK without token when unlocked, got %d", w.Code)
	}
}

func TestMiddlewareAllowsAPIWithValidToken(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	svc.Setup("test-password")
	token := svc.GetSessionToken()

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("handler should be called with valid session token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK with valid token, got %d", w.Code)
	}
}

func TestMiddlewareBlocksAPIWithInvalidToken(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	svc.Setup("test-password")

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	req := httptest.NewRequest("GET", "/api/servers", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("handler should NOT be called with invalid token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized with invalid token, got %d", w.Code)
	}
}

func TestMiddlewareAllowsStaticFiles(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	svc.Lock()

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// Non-API paths should be allowed (static files)
	req := httptest.NewRequest("GET", "/index.html", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("handler should be called for non-API paths (static files)")
	}
}

func TestMiddlewareBlocksAPIWhenNotSetup(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	// Don't call Setup — app is not set up

	mw := NewMiddleware(svc)

	handlerCalled := false
	handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if handlerCalled {
		t.Error("handler should NOT be called when app is not set up")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when not setup, got %d", w.Code)
	}
}
