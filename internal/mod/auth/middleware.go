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
// If the app is not set up, it returns 403 (only exempt setup endpoints work).
// If the app is locked, it returns 403 Forbidden.
// If the app is unlocked, API and WebSocket requests require a valid session
// token — a missing or invalid token is rejected with 401. The global unlocked
// state is NOT sufficient on its own: each client must present the token it
// received from /auth/unlock, so an unauthenticated browser cannot ride on
// another session's unlock.
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

		// If app is not set up yet, deny API/WS access. Only the exempt
		// setup/status endpoints (handled above) are reachable.
		setup, err := m.svc.IsSetup()
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
			return
		}
		if !setup {
			shared.WriteError(w, http.StatusForbidden, "app not set up", "NOT_SETUP")
			return
		}

		// If app is locked, deny access
		if m.svc.IsLocked() {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}

		// API and WebSocket routes require a valid session token. A missing
		// or invalid token is rejected — being globally unlocked is not enough,
		// the caller must prove it holds this session's token.
		token := extractToken(r)
		if !m.svc.ValidateSessionToken(token) {
			shared.WriteError(w, http.StatusUnauthorized, "invalid session token", "UNAUTHORIZED")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// extractToken extracts the session token from one of the following sources,
// in order of preference:
//  1. Authorization header (for REST API requests)
//  2. Sec-WebSocket-Protocol header (for WebSocket connections using subprotocol auth)
//  3. "token" query parameter (legacy fallback for WebSocket connections)
//
// The subprotocol mechanism is preferred for WebSocket connections because it
// does not expose the token in the URL (which would appear in server logs,
// browser history, and analytics tools). The subprotocol format is:
//   Sec-WebSocket-Protocol: meshium-auth.<token>
//
// The query parameter fallback is kept for backward compatibility but should
// be removed once all clients use subprotocol auth.
func extractToken(r *http.Request) string {
	// 1. Check Authorization header (REST API)
	auth := r.Header.Get("Authorization")
	if auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		return auth
	}

	// 2. Check Sec-WebSocket-Protocol header (modern WebSocket auth)
	// Client sends: Sec-WebSocket-Protocol: meshium-auth.<token>
	// We extract the token and must echo back the subprotocol in the response.
	protocols := websocketSubprotocols(r)
	for _, p := range protocols {
		if strings.HasPrefix(p, "meshium-auth.") {
			token := strings.TrimPrefix(p, "meshium-auth.")
			if token != "" {
				return token
			}
		}
	}

	// 3. Fall back to query parameter (legacy WebSocket auth)
	token := r.URL.Query().Get("token")
	if token != "" {
		return token
	}

	return ""
}

// websocketSubprotocols extracts the subprotocols from the Sec-WebSocket-Protocol
// header. The header value is a comma-separated list of protocol names.
func websocketSubprotocols(r *http.Request) []string {
	header := r.Header.Get("Sec-WebSocket-Protocol")
	if header == "" {
		return nil
	}
	var protocols []string
	for _, p := range strings.Split(header, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			protocols = append(protocols, p)
		}
	}
	return protocols
}

// WebSocketSubprotocolToken returns the subprotocol token string to echo back
// to the client during WebSocket upgrade. If the client requested a
// "meshium-auth.*" subprotocol, this returns that protocol string so the
// upgrader can include it in the response. Otherwise it returns empty string.
func WebSocketSubprotocolToken(r *http.Request) string {
	protocols := websocketSubprotocols(r)
	for _, p := range protocols {
		if strings.HasPrefix(p, "meshium-auth.") {
			return p
		}
	}
	return ""
}
