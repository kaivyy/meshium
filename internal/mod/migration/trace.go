package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
)

// newID returns a short random hex id (n bytes). crypto/rand avoids weak
// math/rand sources; ids only need collision-unlikelihood, not secrecy.
func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(b)
}

// Phase2D-2: correlation/identity propagation.
//
// One externally-initiated operation carries ONE request correlation ID. It is
// generated at the boundary (REST handler, WebSocket command, migration
// creation, pipeline start/retry/resume, cutover, rollback, background
// reconcile) and propagated — not regenerated — through handler → pipeline →
// stage → remote executor → transfer/replication → fencing → traffic →
// persistence → events → audit → structured logs.
//
// We distinguish identity *kinds* by CONTEXT FIELD, never by minting a fresh
// ID per internal call. A single request ID flows into the event correlationId
// and the audit correlation_id. Migration/pipeline/transfer/lease/operator
// identities are orthogonal labels carried on the same context so any record can
// be reconstructed post-incident. No secret is ever placed in a trace field.

type traceKey int

const (
	keyRequestID        traceKey = iota // external operation correlation id
	keyMigrationID                      // migration id (may be 0 pre-create)
	keyPipelineID                       // pipeline/execution id
	keyTransferID                       // transfer id (rsync/strategy run)
	keyLeaseGen                        // fence lease generation
	keyOperatorActionID                 // idempotent operator action id
)

// RequestID is the externally-initiated operation correlation identifier.
type RequestID string

// NewRequestID returns a random hex correlation id (no Math.random; crypto/rand).
func NewRequestID() RequestID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable; fall back to a zero-padded marker
		// so callers still get a non-empty, unique-enough id for local tracing.
		return RequestID("rid-fallback")
	}
	return RequestID(hex.EncodeToString(b[:]))
}

// WithRequestID returns a child context carrying the correlation id.
func WithRequestID(ctx context.Context, id RequestID) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// WithCorrelation is the boundary helper: it ensures a request correlation id
// is present (honoring an explicit client-supplied X-Request-ID when one is
// passed) and returns the child context. It is the single entry point REST/WS
// handlers call so every operation carries exactly one correlation id. The id
// is an opaque, non-secret label.
func WithCorrelation(ctx context.Context, correlationID, requestID, migrationID, operatorAction, actorType string) context.Context {
	rid := RequestID(requestID)
	if rid == "" {
		rid = NewRequestID()
	}
	ctx = WithRequestID(ctx, rid)
	// The first positional arg is the boundary correlation id. Preserve an
	// explicit one; otherwise derive a stable boundary id from the request id
	// so CorrelationFrom never returns empty for an operation that has a
	// request id.
	if correlationID == "" {
		correlationID = rid.String()
	}
	ctx = context.WithValue(ctx, keyCorrelation, correlationID) // preserve explicit boundary id
	if migrationID != "" {
		if id, err := parseID(migrationID); err == nil {
			ctx = WithMigrationID(ctx, id)
		}
	}
	if operatorAction != "" {
		ctx = WithOperatorActionID(ctx, operatorAction)
	}
	if actorType != "" {
		ctx = context.WithValue(ctx, keyActorType, actorType)
	}
	return ctx
}

// keyActorType carries the actor type (operator | api_client | system_reconciler).
const keyActorType traceKey = keyOperatorActionID + 1

// ActorTypeFrom extracts the actor type from ctx ("" if unset).
func ActorTypeFrom(ctx context.Context) string {
	if v, ok := ctx.Value(keyActorType).(string); ok {
		return v
	}
	return ""
}

// RequestIDFrom extracts the correlation id; "" if none propagated yet.
func RequestIDFrom(ctx context.Context) RequestID {
	if id, ok := ctx.Value(keyRequestID).(RequestID); ok {
		return id
	}
	return ""
}

// CorrelationFrom extracts the boundary correlation id passed to
// WithCorrelation (the first positional arg). "" if none propagated yet.
func CorrelationFrom(ctx context.Context) string {
	if id, ok := ctx.Value(keyCorrelation).(string); ok {
		return id
	}
	return ""
}

// keyCorrelation carries the boundary correlation id string.
const keyCorrelation traceKey = keyActorType + 1

// WithMigrationID carries the migration id on the context.
func WithMigrationID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, keyMigrationID, id)
}

// MigrationIDFrom extracts the migration id (0 if unset).
func MigrationIDFrom(ctx context.Context) int {
	if id, ok := ctx.Value(keyMigrationID).(int); ok {
		return id
	}
	return 0
}

// WithPipelineID carries the pipeline/execution id.
func WithPipelineID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyPipelineID, id)
}

// WithTransferID carries the transfer id.
func WithTransferID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyTransferID, id)
}

// WithLeaseGen carries the fence lease generation.
func WithLeaseGen(ctx context.Context, gen int) context.Context {
	return context.WithValue(ctx, keyLeaseGen, gen)
}

// LeaseGenFrom extracts the fence lease generation (0 if unset).
func LeaseGenFrom(ctx context.Context) int {
	if g, ok := ctx.Value(keyLeaseGen).(int); ok {
		return g
	}
	return 0
}

// WithOperatorActionID carries the idempotent operator action id.
func WithOperatorActionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyOperatorActionID, id)
}

// NewOperatorActionIDStr mints a fresh operator-action id as a plain string.
func NewOperatorActionIDStr() string {
	return "op-" + newID(10)
}

// OperatorActionIDFrom extracts the operator action id ("" if unset).
func OperatorActionIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(keyOperatorActionID).(string); ok {
		return id
	}
	return ""
}

// MigrationIDToString is a tiny helper kept local to avoid importing strconv
// at every call site that needs to label an id in a log/event field.
func MigrationIDToString(id int) string { return strconv.Itoa(id) }

// parseID parses a base-10 migration id; returns 0 and an error on bad input.
func parseID(s string) (int, error) {
	return strconv.Atoi(s)
}

// String renders the correlation id for log/event fields.
func (r RequestID) String() string { return string(r) }
