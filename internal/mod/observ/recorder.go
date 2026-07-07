package observ

import (
	"sync"
	"sync/atomic"
	"time"
)

// clock is the time source, indirected so tests can make timestamps
// deterministic. Production uses time.Now.
type clock func() time.Time

// Recorder is the entry point call sites use to emit observability data. It
// assigns each event a per-migration monotonic sequence, stamps a timestamp,
// validates, and forwards to the configured Sink. It is safe for concurrent use.
//
// A Recorder never returns an error from the emit path: observability must not
// break a migration. Invalid events/metrics are dropped (counted, for tests),
// and a panicking sink is recovered. The one place errors surface is the
// explicit Emit/EmitMetric methods, for callers that want to know.
type Recorder struct {
	sink  Sink
	now   clock
	seqMu sync.Mutex
	seqs  map[int]*int64

	dropped int64 // atomic: count of invalid events/metrics dropped
}

// NewRecorder returns a Recorder writing to sink. A nil sink becomes a NopSink
// so the Recorder is always safe to call.
func NewRecorder(sink Sink) *Recorder {
	if sink == nil {
		sink = NopSink{}
	}
	return &Recorder{
		sink: sink,
		now:  func() time.Time { return time.Now().UTC() },
		seqs: make(map[int]*int64),
	}
}

// withClock overrides the time source. Test-only helper (unexported).
func (r *Recorder) withClock(c clock) *Recorder {
	r.now = c
	return r
}

func (r *Recorder) sequenceCounter(migrationID int) *int64 {
	r.seqMu.Lock()
	defer r.seqMu.Unlock()
	if c, ok := r.seqs[migrationID]; ok {
		return c
	}
	c := new(int64)
	r.seqs[migrationID] = c
	return c
}

// Dropped returns the number of invalid events/metrics dropped so far. Used by
// tests and health surfaces to detect a miswired call site.
func (r *Recorder) Dropped() int64 { return atomic.LoadInt64(&r.dropped) }

// Emit validates, sequences, timestamps, and forwards an event. It returns the
// validation error (if any) for callers that care; the sequence is only
// consumed when the event is valid. A panicking sink is recovered.
func (r *Recorder) Emit(e Event) error {
	if e.Kind == "" {
		e.Kind = KindLog
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}
	if err := e.Validate(); err != nil {
		atomic.AddInt64(&r.dropped, 1)
		return err
	}
	if e.Time.IsZero() {
		e.Time = r.now()
	}
	e.Sequence = atomic.AddInt64(r.sequenceCounter(e.MigrationID), 1)
	r.deliverEvent(e)
	return nil
}

// EmitMetric validates, timestamps, and forwards a metric. Metrics are not
// sequenced (they are a separate signal). A panicking sink is recovered.
func (r *Recorder) EmitMetric(m Metric) error {
	if err := m.Validate(); err != nil {
		atomic.AddInt64(&r.dropped, 1)
		return err
	}
	if m.Time.IsZero() {
		m.Time = r.now()
	}
	r.deliverMetric(m)
	return nil
}

func (r *Recorder) deliverEvent(e Event) {
	defer func() { _ = recover() }()
	r.sink.HandleEvent(e)
}

func (r *Recorder) deliverMetric(m Metric) {
	defer func() { _ = recover() }()
	r.sink.HandleMetric(m)
}

// Log emits an ordinary log event with the given severity. Convenience over
// Emit for the common case; the error is discarded because a log call site
// should not have to branch on observability failure.
func (r *Recorder) Log(migrationID int, phase Phase, sev Severity, msg string, fields map[string]string) {
	_ = r.Emit(Event{
		MigrationID: migrationID,
		Phase:       phase,
		Kind:        KindLog,
		Severity:    sev,
		Message:     msg,
		Fields:      fields,
	})
}

// PhaseStarted emits a KindAudit checkpoint that a phase has begun.
func (r *Recorder) PhaseStarted(migrationID int, phase Phase, msg string) {
	_ = r.Emit(Event{
		MigrationID: migrationID,
		Phase:       phase,
		Kind:        KindAudit,
		Severity:    SeverityInfo,
		Type:        AuditPhaseStarted,
		Message:     msg,
	})
}

// PhaseCompleted emits a KindAudit checkpoint that a phase finished successfully.
func (r *Recorder) PhaseCompleted(migrationID int, phase Phase, msg string) {
	_ = r.Emit(Event{
		MigrationID: migrationID,
		Phase:       phase,
		Kind:        KindAudit,
		Severity:    SeverityInfo,
		Type:        AuditPhaseCompleted,
		Message:     msg,
	})
}

// PhaseFailed emits a KindAudit checkpoint that a phase failed, recording the
// error message in a field. err must be non-nil.
func (r *Recorder) PhaseFailed(migrationID int, phase Phase, err error) {
	msg := "phase failed"
	fields := map[string]string{}
	if err != nil {
		fields["error"] = err.Error()
	}
	_ = r.Emit(Event{
		MigrationID: migrationID,
		Phase:       phase,
		Kind:        KindAudit,
		Severity:    SeverityError,
		Type:        AuditPhaseFailed,
		Message:     msg,
		Fields:      fields,
	})
}
