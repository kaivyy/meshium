package auth

import (
	"net/http"
	"strings"

	"meshium/internal/shared"
)

// Middleware provides HTTP middleware for authenticating API requests.
// It checks for a valid session token in the Authorization header.
type Middleware struct {
	svc *Service
}

// NewMiddleware creates a new authentication middleware.
func NewMiddleware(svc *Service) *Middleware {
	return &Middleware{svc: svc}
}

// authExemptPaths are paths that don't require authentication.
var authExemptPaths = map[string]bool{
	"/api/auth/setup":  true,
	"/api/auth/unlock": true,
	"/api/auth/status": true,
	"/api/health":      true,
}

// RequireAuth returns middleware that checks for a valid session token.
// If the app is not set up, it allows requests through (setup mode).
// If the app is locked, it returns 403 Forbidden.
// If the app is unlocked, API requests are allowed (unlocked = authenticated).
// Session tokens are validated for WebSocket endpoints to prevent CSRF.
// WebSocket routes (/ws/) require a valid token in the "token" query parameter.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow exempt paths (auth endpoints, health check)
		if authExemptPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		isWS := strings.HasPrefix(r.URL.Path, "/ws/")

		// Allow static file serving (non-API, non-WS routes)
		if !isAPI && !isWS {
			next.ServeHTTP(w, r)
			return
		}

		// If app is not set up yet, allow API access (setup mode)
		setup, err := m.svc.IsSetup()
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
			return
		}
		if !setup {
			// In setup mode, only allow auth-related endpoints
			// (already handled by exempt paths above)
			shared.WriteError(w, http.StatusForbidden, "app not set up", "NOT_SETUP")
			return
		}

		// If app is locked, deny access
		if m.svc.IsLocked() {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}

		// WebSocket routes require a valid session token (from query param).
		// Unlike API routes, WS connections cannot rely on "unlocked = authenticated"
		// because browsers can't set Authorization headers on WebSocket connections.
		if isWS {
			token := extractToken(r)
			if token == "" {
				shared.WriteError(w, http.StatusUnauthorized, "missing session token", "UNAUTHORIZED")
				return
			}
			if !m.svc.ValidateSessionToken(token) {
				shared.WriteError(w, http.StatusUnauthorized, "invalid session token", "UNAUTHORIZED")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// API routes: if a token is provided, validate it (reject invalid tokens).
		// If no token is provided, allow access (unlocked = authenticated).
		// This prevents reload loops where the frontend has no token after
		// a server restart but the app is still unlocked.
		token := extractToken(r)
		if token != "" && !m.svc.ValidateSessionToken(token) {
			shared.WriteError(w, http.StatusUnauthorized, "invalid session token", "UNAUTHORIZED")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractToken extracts the session token from the Authorization header
// or the "token" query parameter (for WebSocket connections that can't set headers).
// Supports both "Bearer <token>" and raw token formats.
func extractToken(r *http.Request) string {
	// Check Authorization header first
	auth := r.Header.Get("Authorization")
	if auth != "" {
		// Check for Bearer token format
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		// Otherwise treat the entire header as the token
		return auth
	}

	// Fall back to query parameter (for WebSocket connections)
	token := r.URL.Query().Get("token")
	if token != "" {
		return token
	}

	return ""
}
