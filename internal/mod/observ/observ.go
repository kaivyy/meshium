// Package observ defines Meshium's observability abstraction: a structured
// event model, an execution timeline per migration, and a pluggable sink
// interface so metrics/tracing backends (Prometheus, OpenTelemetry, ...) can be
// added later without changing call sites.
//
// Why a new package rather than extending migration.EventBus: EventBus already
// persists and fans out migration-internal lifecycle events tied to the
// pipeline's PipelineRepo. This package is deliberately decoupled from that
// storage and from the migration package entirely — it is stdlib-only and
// nothing imports it yet, so it cannot affect the running pipeline. It provides
// the vendor-neutral seam a future integration can wire EventBus (and the
// observation metrics) into, and the timeline view that neither currently
// produces.
//
// Design constraints:
//   - Stdlib-only. No Prometheus/OTel dependency is mandated; a Sink is a small
//     interface an adapter implements. Meshium ships a NopSink and an in-memory
//     MemorySink; a real backend is an out-of-tree (or later in-tree) Sink.
//   - Honest, minimal model. An Event carries a lifecycle Phase, a Kind
//     distinguishing audit checkpoints from ordinary logs, a Severity, and
//     free-form string Fields. Metrics are a separate, explicitly-typed value.
package observ

import (
	"fmt"
	"time"
)

// Phase names a lifecycle phase of a migration. The set mirrors the audit
// checkpoints Tahap 8 requires; a Recorder emits an audit Event at the start,
// completion, and failure of each phase, and the Timeline groups events by it.
type Phase string

const (
	PhaseDiscovery   Phase = "discovery"
	PhaseValidation  Phase = "validation"
	PhasePlanning    Phase = "planning"
	PhaseBackup      Phase = "backup"
	PhaseTransfer    Phase = "transfer"
	PhaseReplication Phase = "replication"
	PhaseCutover     Phase = "cutover"
	PhaseRollback    Phase = "rollback"
	PhaseRecovery    Phase = "recovery"
)

// orderedPhases is the canonical lifecycle order. The Timeline sorts phase
// spans by first-event time, but this order is the deterministic tie-break and
// documents the intended progression. Rollback and recovery are terminal/repair
// phases and sort after the forward path.
var orderedPhases = []Phase{
	PhaseDiscovery, PhaseValidation, PhasePlanning, PhaseBackup,
	PhaseTransfer, PhaseReplication, PhaseCutover, PhaseRollback, PhaseRecovery,
}

// phaseRank returns a phase's position in the canonical order, or a large value
// for an unknown phase so custom phases sort last but deterministically.
func phaseRank(p Phase) int {
	for i, known := range orderedPhases {
		if known == p {
			return i
		}
	}
	return len(orderedPhases)
}

// Severity is the level of an event. It is intentionally small and independent
// of any logging library.
type Severity string

const (
	SeverityDebug Severity = "debug"
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// validSeverity reports whether s is one of the known severities.
func validSeverity(s Severity) bool {
	switch s {
	case SeverityDebug, SeverityInfo, SeverityWarn, SeverityError:
		return true
	default:
		return false
	}
}

// Kind distinguishes an audit checkpoint (a durable statement that a phase
// started, completed, or failed) from an ordinary log event. A backend may
// route the two differently (audit → durable store, log → sampled stream).
type Kind string

const (
	// KindLog is an ordinary, possibly-sampled observability event.
	KindLog Kind = "log"
	// KindAudit is a lifecycle checkpoint that should not be dropped.
	KindAudit Kind = "audit"
)

// Audit event types emitted by the Recorder phase helpers. They are stable
// strings so a downstream store can index on them.
const (
	AuditPhaseStarted   = "phase_started"
	AuditPhaseCompleted = "phase_completed"
	AuditPhaseFailed    = "phase_failed"
)

// Event is a single structured observability event. It is self-contained: it
// carries its own migration id, monotonically-increasing sequence (assigned by
// the Recorder), timestamp, phase, kind, severity, message, and free-form
// string fields. Fields are strings so an event serializes trivially to any
// backend without type negotiation.
type Event struct {
	MigrationID int               `json:"migrationId"`
	Sequence    int64             `json:"sequence"`
	Time        time.Time         `json:"time"`
	Phase       Phase             `json:"phase"`
	Kind        Kind              `json:"kind"`
	Severity    Severity          `json:"severity"`
	Type        string            `json:"type,omitempty"`
	Message     string            `json:"message"`
	Fields      map[string]string `json:"fields,omitempty"`
}

// Validate checks the event has the fields every sink can rely on. The Recorder
// validates before assigning a sequence, so an invalid event never reaches a
// sink with a wasted sequence number.
func (e Event) Validate() error {
	if e.MigrationID <= 0 {
		return fmt.Errorf("observ: migrationId is required")
	}
	if e.Phase == "" {
		return fmt.Errorf("observ: phase is required")
	}
	if e.Message == "" {
		return fmt.Errorf("observ: message is required")
	}
	if e.Kind != KindLog && e.Kind != KindAudit {
		return fmt.Errorf("observ: invalid kind %q", e.Kind)
	}
	if !validSeverity(e.Severity) {
		return fmt.Errorf("observ: invalid severity %q", e.Severity)
	}
	return nil
}

// Metric is a single numeric measurement. It is deliberately separate from
// Event: a metrics backend consumes these, a log/trace backend consumes events,
// and a Sink may implement either or both. Kept minimal (name/value/unit plus
// phase and labels) so it maps onto Prometheus, OTel, or a plain time series.
type Metric struct {
	MigrationID int               `json:"migrationId"`
	Time        time.Time         `json:"time"`
	Phase       Phase             `json:"phase"`
	Name        string            `json:"name"`
	Value       float64           `json:"value"`
	Unit        string            `json:"unit,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Validate checks the metric has a migration id and a name.
func (m Metric) Validate() error {
	if m.MigrationID <= 0 {
		return fmt.Errorf("observ: metric migrationId is required")
	}
	if m.Name == "" {
		return fmt.Errorf("observ: metric name is required")
	}
	return nil
}
