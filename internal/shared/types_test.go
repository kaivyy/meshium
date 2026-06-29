package shared

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorSafeUsesGenericMessageForServerErrors(t *testing.T) {
	var logs bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer log.SetOutput(origOutput)
	defer log.SetFlags(origFlags)

	rec := httptest.NewRecorder()
	internalErr := errors.New("sql: connection refused")
	WriteErrorSafe(rec, http.StatusInternalServerError, "operation failed", "INTERNAL", internalErr)

	if got, want := rec.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("expected status %d, got %d", want, got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "operation failed") || strings.Contains(body, internalErr.Error()) {
		t.Fatalf("expected generic client message, got %q", body)
	}
	if !strings.Contains(logs.String(), internalErr.Error()) {
		t.Fatalf("expected internal error to be logged, got %q", logs.String())
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
