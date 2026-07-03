package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorSafeUsesGenericMessageForServerErrors(t *testing.T) {
	var logs bytes.Buffer
	origLogger := Log
	SetLogger(NewLogger(&logs, LevelDebug))
	defer SetLogger(origLogger)

	rec := httptest.NewRecorder()
	internalErr := errors.New("sql: connection refused")
	WriteErrorSafe(rec, http.StatusInternalServerError, "operation failed", "INTERNAL", internalErr)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("expected status %d, got %d", want, got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "operation failed") || strings.Contains(body, internalErr.Error()) {
		t.Fatalf("expected generic client message, got %q", body)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
	}
	if got := entry["message"]; got != "internal error" {
		t.Fatalf("expected internal error message, got %v", got)
	}
	if got := entry["error"]; got != internalErr.Error() {
		t.Fatalf("expected internal error to be logged, got %v", got)
	}
}

func TestWriteErrorSafeExposesClientFacingErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	internalErr := errors.New("server missing for id 42")
	WriteErrorSafe(rec, http.StatusNotFound, "ignored", "NOT_FOUND", internalErr)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Fatalf("expected status %d, got %d", want, got)
	}
	if body := rec.Body.String(); !strings.Contains(body, internalErr.Error()) {
		t.Fatalf("expected client-facing error message, got %q", body)
	}
}
