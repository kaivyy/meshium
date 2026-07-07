package shared

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// APIError is the standard error response format.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes a standard API error response.
func WriteError(w http.ResponseWriter, status int, message, code string) {
	WriteJSON(w, status, APIError{Error: message, Code: code})
}

// WriteErrorSafe logs the internal error and returns either a generic error
// message or the underlying client-facing message, depending on status.
func WriteErrorSafe(w http.ResponseWriter, status int, safeMessage, code string, internalErr error) {
	if internalErr != nil {
		Log.Error("internal error", "error", internalErr, "status", status, "code", code)
	}

	message := safeMessage
	if internalErr != nil && status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		message = internalErr.Error()
	}
	if message == "" {
		message = "internal error"
	}

	WriteError(w, status, message, code)
}

// CheckWebSocketOrigin validates the WebSocket Origin header for same-origin
// requests. It is proxy-aware: when Meshium runs behind a reverse proxy (e.g.
// Tailscale serve, nginx), the browser's Origin carries the public hostname
// while the request Host may be the internal address (localhost:9527). To allow
// legitimate same-site access without opening up genuine cross-origin requests,
// the Origin hostname is compared against both the request Host and any
// X-Forwarded-Host the proxy sets. Hostnames are compared without port, since a
// proxy commonly terminates on 443 while forwarding to a different backend port.
func CheckWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := hostname(u.Host)
	if originHost == "" {
		return false
	}

	if originHost == hostname(r.Host) {
		return true
	}

	// Honor forwarded host(s) set by a reverse proxy. X-Forwarded-Host may be a
	// comma-separated list when multiple proxies are chained.
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		for _, h := range strings.Split(fwd, ",") {
			if originHost == hostname(strings.TrimSpace(h)) {
				return true
			}
		}
	}

	return false
}

// hostname returns the lowercased host portion of a "host" or "host:port"
// string, stripping any port. Returns "" if the input is empty.
func hostname(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}
