package migration

import "meshium/internal/mod/observ"

// Migration lifecycle observability event types emitted by the Engine on the
// job-engine migration path. They are stable identifiers a downstream sink can
// index on, deliberately distinct from observ's built-in phase_* audit
// checkpoints: these describe the migration lifecycle as the Engine's state
// machine drives it, not per-phase spans a future integration might derive.
const (
	EventMigrationStarted     = "migration_started"
	EventStageStarted         = "stage_started"
	EventStageCompleted       = "stage_completed"
	EventStageFailed          = "stage_failed"
	EventMigrationInterrupted = "migration_interrupted"
	EventMigrationCompleted   = "migration_completed"
)

// SetRecorder installs an observability recorder. It is optional: an Engine
// constructed by NewEngine already has a NopSink-backed recorder, so emit call
// sites never nil-check and a caller that never sets a recorder pays nothing.
// Observability must never affect migration behavior, so this only swaps the
// sink the Engine emits to; the emit path itself validates, isolates sink
// panics, and never propagates errors back into Run.
//
// It takes the same lock Run holds so a recorder swap cannot race a running
// migration's emits under the race detector.
func (e *Engine) SetRecorder(r *observ.Recorder) {
	if r == nil {
		r = observ.NewRecorder(nil)
	}
	e.mu.Lock()
	e.observer = r
	e.mu.Unlock()
}

// stageOf maps a migration state to the observ stage (Phase) an event carries.
// The migration state machine is richer than observ's canonical phase set, so
// the honest stage value is the state's own name; BuildTimeline tolerates
// non-canonical phases (they sort last, deterministically).
func stageOf(s MigrationState) observ.Phase {
	return observ.Phase(s.String())
}

// emit forwards one lifecycle event to the recorder. Callers must hold e.mu
// (every call site is inside Run or a helper Run invokes). The error is
// discarded: a migration must never fail because of observability.
func (e *Engine) emit(migrationID int, stage observ.Phase, eventType string, sev observ.Severity, msg string, fields map[string]string) {
	_ = e.observer.Emit(observ.Event{
		MigrationID: migrationID,
		Phase:       stage,
		Kind:        observ.KindAudit,
		Severity:    sev,
		Type:        eventType,
		Message:     msg,
		Fields:      fields,
	})
}

func (e *Engine) obsMigrationStarted(migrationID int, stage observ.Phase) {
	e.emit(migrationID, stage, EventMigrationStarted, observ.SeverityInfo,
		"migration started", map[string]string{"status": "started"})
}

func (e *Engine) obsStageStarted(migrationID int, stage observ.Phase) {
	e.emit(migrationID, stage, EventStageStarted, observ.SeverityInfo,
		"stage started: "+string(stage), map[string]string{"status": "started"})
}

func (e *Engine) obsStageCompleted(migrationID int, stage observ.Phase) {
	e.emit(migrationID, stage, EventStageCompleted, observ.SeverityInfo,
		"stage completed: "+string(stage), map[string]string{"status": "completed"})
}

func (e *Engine) obsStageFailed(migrationID int, stage observ.Phase, reason string) {
	e.emit(migrationID, stage, EventStageFailed, observ.SeverityError,
		"stage failed: "+string(stage), map[string]string{"status": "failed", "reason": reason})
}

func (e *Engine) obsMigrationInterrupted(migrationID int, stage observ.Phase, reason string) {
	e.emit(migrationID, stage, EventMigrationInterrupted, observ.SeverityWarn,
		"migration interrupted", map[string]string{"status": "interrupted", "reason": reason})
}

func (e *Engine) obsMigrationCompleted(migrationID int, stage observ.Phase) {
	e.emit(migrationID, stage, EventMigrationCompleted, observ.SeverityInfo,
		"migration completed", map[string]string{"status": "completed"})
}
