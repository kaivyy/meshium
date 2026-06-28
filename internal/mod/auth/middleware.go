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
	"/api/auth/setup":   true,
	"/api/auth/unlock":  true,
	"/api/auth/status":  true,
	"/api/health":       true,
}

// RequireAuth returns middleware that checks for a valid session token.
// If the app is not set up, it allows requests through (setup mode).
// If the app is locked, it returns 403 Forbidden.
// If no valid session token is provided, it returns 401 Unauthorized.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow exempt paths (auth endpoints, health check)
		if authExemptPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Allow static file serving (non-API routes)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
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

		// Check for session token in Authorization header
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
	})
}

// extractToken extracts the session token from the Authorization header.
// Supports both "Bearer <token>" and raw token formats.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	// Check for Bearer token format
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	// Otherwise treat the entire header as the token
	return auth
}
