package discovery

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWSHandlerRejectsInvalidServerID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws/connect/abc", nil)
	w := httptest.NewRecorder()

	h := &Handler{}
	h.handleConnect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid server ID") {
		t.Fatalf("expected invalid server ID error, got %q", w.Body.String())
	}
}
