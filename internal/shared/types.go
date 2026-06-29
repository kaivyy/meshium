package shared

import (
	"encoding/json"
	"net/http"
	"net/url"
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

// CheckWebSocketOrigin validates the WebSocket Origin header for same-origin requests.
func CheckWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
