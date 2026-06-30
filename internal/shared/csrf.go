package shared

import (
	"net/http"
	"strings"
)

// CSRFMiddleware validates that state-changing requests (POST, PUT, DELETE, PATCH)
// have an acceptable Content-Type. This prevents CSRF attacks because cross-origin
// forms cannot set custom Content-Type headers like application/json.
//
// Allowed Content-Types for state-changing methods:
//   - application/json (standard API requests)
//   - multipart/form-data (file uploads)
//   - empty (action endpoints with no body, e.g., POST /api/auth/lock)
//
// This is safe because the API uses Bearer token authentication, not cookies.
// Cross-origin requests cannot set the Authorization header without a CORS
// preflight, which we do not allow.
//
// For GET/HEAD/OPTIONS requests, the middleware is a no-op.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only validate state-changing methods
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			contentType := r.Header.Get("Content-Type")
			// Allow application/json, multipart/form-data, and empty Content-Type
			if contentType != "" &&
				!strings.HasPrefix(contentType, "application/json") &&
				!strings.HasPrefix(contentType, "multipart/form-data") {
				WriteError(w, http.StatusUnsupportedMediaType, "unsupported content-type", "INVALID_CONTENT_TYPE")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware sets CORS headers for the API. Since the API uses Bearer token
// authentication (not cookies), cross-origin requests are safe as long as the
// Authorization header is required. This middleware restricts origins to
// the same server by default.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "3600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
