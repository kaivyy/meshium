package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"meshium/internal/shared"
)

// SessionManager manages session tokens for authenticated clients.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]struct{}
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]struct{})}
}

// CreateSession generates a new session token and stores it.
func (sm *SessionManager) CreateSession() string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	sm.mu.Lock()
	sm.sessions[token] = struct{}{}
	sm.mu.Unlock()
	return token
}

// ValidateSession checks if a session token is valid.
func (sm *SessionManager) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.sessions[token]
	return ok
}

// RemoveSession removes a session token.
func (sm *SessionManager) RemoveSession(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// AuthMiddleware wraps an http.Handler with authentication checking.
// It allows access to /api/auth/setup, /api/auth/unlock, /api/auth/status, and /api/health without auth.
// All other routes require a valid session token.
// WebSocket endpoints (/ws/) accept the token as a query parameter since browsers
// cannot set custom headers on WebSocket connections.
func AuthMiddleware(svc *Service, sm *SessionManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow health checks
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow auth endpoints without session
		if r.URL.Path == "/api/auth/setup" || r.URL.Path == "/api/auth/unlock" || r.URL.Path == "/api/auth/status" {
			next.ServeHTTP(w, r)
			return
		}

		// Check if app is set up
		setup, err := svc.IsSetup()
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
			return
		}
		if !setup {
			// Allow only auth endpoints if not set up (already handled above)
			shared.WriteError(w, http.StatusServiceUnavailable, "setup required", "SETUP_REQUIRED")
			return
		}

		// Check if app is locked
		if svc.IsLocked() {
			shared.WriteError(w, http.StatusUnauthorized, "app is locked", "APP_LOCKED")
			return
		}

		// Extract session token: header, cookie, or query param (for WebSocket)
		var token string
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			// WebSocket clients pass token as query parameter
			token = r.URL.Query().Get("token")
		} else {
			token = r.Header.Get("X-Session-Token")
			if token == "" {
				if cookie, err := r.Cookie("session"); err == nil {
					token = cookie.Value
				}
			}
		}

		if !sm.ValidateSession(token) {
			if strings.HasPrefix(r.URL.Path, "/ws/") {
				http.Error(w, "authentication required", http.StatusUnauthorized)
			} else {
				shared.WriteError(w, http.StatusUnauthorized, "invalid or missing session", "NO_SESSION")
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}
