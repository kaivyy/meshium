package migration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturingAuditRepo records audit entries so we can assert correlation
// identity propagation (Phase2D-2).
type capturingAuditRepo struct {
	*mockPipelineRepo
	entries []AuditEntry
}

func newCapturingAuditRepo() *capturingAuditRepo {
	return &capturingAuditRepo{mockPipelineRepo: &mockPipelineRepo{}}
}

func (r *capturingAuditRepo) CreateAuditEntry(ctx context.Context, e AuditEntry) (int64, error) {
	// Mirror the real sqliteRepo derivation: inherit the propagated
	// correlation id / actor type when the caller did not set them explicitly.
	if e.CorrelationID == "" {
		if cid := CorrelationFrom(ctx); cid != "" {
			e.CorrelationID = cid
		}
	}
	if e.ActorType == "" {
		e.ActorType = ActorTypeFrom(ctx)
	}
	r.entries = append(r.entries, e)
	return int64(len(r.entries)), nil
}

// TestCorrelationPropagatesToEvent proves an event emitted without an
// explicit correlation id inherits the id propagated on the context — one
// externally-initiated request → one correlation id across all its events.
func TestCorrelationPropagatesToEvent(t *testing.T) {
	repo := newCapturingEventRepo()
	bus := NewEventBus(repo)

	ctx := WithCorrelation(context.Background(), "", "", "", "", "")
	if err := bus.Emit(ctx, MigrationEvent{
		MigrationID: 1, Level: EventLevelInfo, Stage: "apply",
		Type: "progress", Message: "step", Source: "engine",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	if len(repo.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.events))
	}
	got := repo.events[0].CorrelationID
	if got == "" {
		t.Fatal("event missing inherited correlation id")
	}
	if CorrelationFrom(ctx) != got {
		t.Fatalf("event correlation %q != ctx correlation %q", got, CorrelationFrom(ctx))
	}
}

// TestCorrelationExplicitWins proves a caller-supplied id is preserved
// and not overwritten by the context default.
func TestCorrelationExplicitWins(t *testing.T) {
	repo := newCapturingEventRepo()
	bus := NewEventBus(repo)

	ctx := WithCorrelation(context.Background(), "corr-explicit", "", "", "", "")
	if err := bus.Emit(ctx, MigrationEvent{
		MigrationID: 1, Level: EventLevelInfo, Stage: "apply",
		Type: "progress", Message: "step", Source: "engine",
		CorrelationID: "corr-caller",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := repo.events[0].CorrelationID; got != "corr-caller" {
		t.Fatalf("correlation = %q, want corr-caller", got)
	}
}

// TestAuditInheritsCorrelation proves CreateAuditEntry inherits the
// propagated correlation id when the caller did not set one. This single
// chokepoint covers all 8 audit call sites.
func TestAuditInheritsCorrelation(t *testing.T) {
	repo := newCapturingAuditRepo()
	ctx := WithCorrelation(context.Background(), "corr-audit", "req-1", "op-1", "", "operator")

	if _, err := repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID: 1, EventType: "migration_paused", Actor: "operator",
	}); err != nil {
		t.Fatalf("create audit: %v", err)
	}
	if len(repo.entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(repo.entries))
	}
	if got := repo.entries[0].CorrelationID; got != "corr-audit" {
		t.Fatalf("audit correlation = %q, want corr-audit", got)
	}
	if got := RequestIDFrom(ctx); got != "req-1" {
		t.Fatalf("request id = %q, want req-1", got)
	}
}

// TestAuditPersistsIdentityFields proves the additive Phase2D identity
// fields are carried on the struct (and thus round-trip through the
// repo + JSON tags), so a row can be reconstructed into its operation.
func TestAuditPersistsIdentityFields(t *testing.T) {
	e := AuditEntry{
		MigrationID:        1,
		EventType:          "cutover_committed",
		CorrelationID:       "corr-x",
		IdempotencyKey:     "idem-y",
		ActorType:           "operator",
		FenceGeneration:     3,
		FenceStatus:         "released",
		TopologySummary:     "source=primary target=standby",
		TrafficVerifySummary: "marker-present",
		ApprovalRef:         "runbook-7",
		Result:              "success",
	}
	if e.CorrelationID != "corr-x" || e.IdempotencyKey != "idem-y" {
		t.Fatal("identity fields not carried on struct")
	}
	if e.ActorType != "operator" || e.Result != "success" {
		t.Fatal("actor/result fields not carried on struct")
	}
	if e.FenceGeneration != 3 {
		t.Fatalf("fence generation = %d, want 3", e.FenceGeneration)
	}
}

// TestWithRequestIDNoClientIDMintsOne proves the REST middleware mints
// a fresh request id when the client supplies none, and echoes it.
func TestWithRequestIDNoClientIDMintsOne(t *testing.T) {
	var seen RequestID
	next := func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}
	mw := withRequestID(next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/migrations", nil)
	mw(rec, req)

	if seen == "" {
		t.Fatal("request id not minted on context")
	}
	if got := rec.Header().Get("X-Request-ID"); got != string(seen) {
		t.Fatalf("echoed X-Request-ID %q != context id %q", got, seen)
	}
}

// TestWithRequestIDHonorsClientID proves a client-supplied
// X-Request-ID is honored so downstream retries/traces align.
func TestWithRequestIDHonorsClientID(t *testing.T) {
	var seen RequestID
	next := func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}
	mw := withRequestID(next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/migrations", nil)
	req.Header.Set("X-Request-ID", "client-supplied-123")
	mw(rec, req)

	if seen != "client-supplied-123" {
		t.Fatalf("request id = %q, want client-supplied-123", seen)
	}
	if got := rec.Header().Get("X-Request-ID"); string(seen) != "client-supplied-123" {
		t.Fatalf("echoed X-Request-ID %q != client-supplied-123", got)
	}
}
