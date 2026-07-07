package observ

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fixedClock returns a deterministic, monotonically advancing clock so timeline
// timestamps are predictable in tests.
func fixedClock(start time.Time, step time.Duration) clock {
	var mu sync.Mutex
	cur := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := cur
		cur = cur.Add(step)
		return t
	}
}

// TestRecorderAssignsSequencePerMigration verifies each migration gets its own
// monotonic sequence starting at 1.
func TestRecorderAssignsSequencePerMigration(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)

	r.Log(1, PhaseDiscovery, SeverityInfo, "a", nil)
	r.Log(1, PhaseDiscovery, SeverityInfo, "b", nil)
	r.Log(2, PhaseDiscovery, SeverityInfo, "c", nil)

	events := sink.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Migration 1: sequences 1,2. Migration 2: sequence 1.
	seqByMig := map[int][]int64{}
	for _, e := range events {
		seqByMig[e.MigrationID] = append(seqByMig[e.MigrationID], e.Sequence)
	}
	if got := seqByMig[1]; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("migration 1 sequences = %v, want [1 2]", got)
	}
	if got := seqByMig[2]; len(got) != 1 || got[0] != 1 {
		t.Errorf("migration 2 sequences = %v, want [1]", got)
	}
}

// TestRecorderDropsInvalid verifies invalid events are dropped and counted, not
// forwarded, and do not consume a sequence number.
func TestRecorderDropsInvalid(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)

	// Missing message → invalid.
	if err := r.Emit(Event{MigrationID: 1, Phase: PhaseDiscovery, Kind: KindLog, Severity: SeverityInfo}); err == nil {
		t.Fatal("expected validation error for missing message")
	}
	// Missing migrationID → invalid.
	if err := r.Emit(Event{Phase: PhaseDiscovery, Message: "x"}); err == nil {
		t.Fatal("expected validation error for missing migrationId")
	}
	if r.Dropped() != 2 {
		t.Errorf("expected 2 dropped, got %d", r.Dropped())
	}
	if len(sink.Events()) != 0 {
		t.Errorf("expected no events forwarded, got %d", len(sink.Events()))
	}

	// A valid event afterwards should still get sequence 1 (drops did not consume).
	r.Log(1, PhaseDiscovery, SeverityInfo, "valid", nil)
	events := sink.Events()
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Errorf("expected first valid event at sequence 1, got %+v", events)
	}
}

// TestRecorderDefaultsKindAndSeverity verifies Emit fills in KindLog/SeverityInfo
// when unset.
func TestRecorderDefaultsKindAndSeverity(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)
	if err := r.Emit(Event{MigrationID: 1, Phase: PhasePlanning, Message: "m"}); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	e := sink.Events()[0]
	if e.Kind != KindLog {
		t.Errorf("expected default kind log, got %q", e.Kind)
	}
	if e.Severity != SeverityInfo {
		t.Errorf("expected default severity info, got %q", e.Severity)
	}
}

// TestRecorderNilSinkSafe verifies a Recorder with a nil sink never panics.
func TestRecorderNilSinkSafe(t *testing.T) {
	r := NewRecorder(nil)
	r.Log(1, PhaseDiscovery, SeverityInfo, "safe", nil)
	r.PhaseStarted(1, PhaseTransfer, "go")
	// No panic == pass.
}

// panicSink panics on every call, to exercise the Recorder's panic isolation.
type panicSink struct{}

func (panicSink) HandleEvent(Event)   { panic("boom") }
func (panicSink) HandleMetric(Metric) { panic("boom") }

// TestRecorderRecoversPanickingSink verifies a panicking sink does not crash the
// emit path.
func TestRecorderRecoversPanickingSink(t *testing.T) {
	r := NewRecorder(panicSink{})
	// Must not panic.
	if err := r.Emit(Event{MigrationID: 1, Phase: PhaseCutover, Message: "m"}); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if err := r.EmitMetric(Metric{MigrationID: 1, Name: "x", Value: 1}); err != nil {
		t.Fatalf("EmitMetric returned error: %v", err)
	}
}

// TestPhaseAuditHelpers verifies the phase audit helpers emit KindAudit events
// with the right type and severity.
func TestPhaseAuditHelpers(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)

	r.PhaseStarted(7, PhaseBackup, "starting backup")
	r.PhaseCompleted(7, PhaseBackup, "backup done")
	r.PhaseFailed(7, PhaseCutover, errors.New("dns not switched"))

	events := sink.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events, got %d", len(events))
	}
	for _, e := range events {
		if e.Kind != KindAudit {
			t.Errorf("event %q kind = %q, want audit", e.Type, e.Kind)
		}
	}
	if events[0].Type != AuditPhaseStarted || events[1].Type != AuditPhaseCompleted || events[2].Type != AuditPhaseFailed {
		t.Errorf("unexpected audit types: %q %q %q", events[0].Type, events[1].Type, events[2].Type)
	}
	if events[2].Severity != SeverityError {
		t.Errorf("failed audit severity = %q, want error", events[2].Severity)
	}
	if events[2].Fields["error"] != "dns not switched" {
		t.Errorf("failed audit missing error field: %v", events[2].Fields)
	}
}

// TestEmitMetric verifies metric validation and forwarding.
func TestEmitMetric(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)

	if err := r.EmitMetric(Metric{MigrationID: 1, Name: "lag_seconds", Value: 0.5, Unit: "s", Phase: PhaseReplication}); err != nil {
		t.Fatalf("EmitMetric returned error: %v", err)
	}
	if err := r.EmitMetric(Metric{Name: "no_migration"}); err == nil {
		t.Fatal("expected error for metric without migrationId")
	}
	metrics := sink.Metrics()
	if len(metrics) != 1 || metrics[0].Name != "lag_seconds" {
		t.Fatalf("expected 1 metric lag_seconds, got %+v", metrics)
	}
}

// TestMultiSinkFanout verifies MultiSink forwards to all sinks and skips nils.
func TestMultiSinkFanout(t *testing.T) {
	a := NewMemorySink()
	b := NewMemorySink()
	r := NewRecorder(NewMultiSink(a, nil, b))

	r.Log(1, PhaseDiscovery, SeverityInfo, "fan", nil)
	if len(a.Events()) != 1 || len(b.Events()) != 1 {
		t.Errorf("expected both sinks to receive the event, got a=%d b=%d", len(a.Events()), len(b.Events()))
	}
}

// TestBuildTimelineOrdersByLifecycle verifies spans come back in canonical
// lifecycle order regardless of emission order, filtered to the migration.
func TestBuildTimelineOrdersByLifecycle(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink).withClock(fixedClock(time.Unix(1000, 0).UTC(), time.Second))

	// Emit out of lifecycle order and interleave a second migration.
	r.PhaseStarted(1, PhaseCutover, "cutover")
	r.PhaseStarted(2, PhaseDiscovery, "other migration")
	r.PhaseStarted(1, PhaseDiscovery, "discovery")
	r.PhaseStarted(1, PhaseTransfer, "transfer")

	tl := BuildTimeline(1, sink.Events())
	if tl.MigrationID != 1 {
		t.Fatalf("timeline migrationID = %d", tl.MigrationID)
	}
	wantOrder := []Phase{PhaseDiscovery, PhaseTransfer, PhaseCutover}
	if len(tl.Phases) != len(wantOrder) {
		t.Fatalf("expected %d phases, got %d: %+v", len(wantOrder), len(tl.Phases), tl.Phases)
	}
	for i, p := range wantOrder {
		if tl.Phases[i].Phase != p {
			t.Errorf("phase[%d] = %q, want %q", i, tl.Phases[i].Phase, p)
		}
	}
}

// TestBuildTimelineStatusDerivation verifies failed takes precedence over
// completed, and an in-flight phase reads as started.
func TestBuildTimelineStatusDerivation(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink).withClock(fixedClock(time.Unix(2000, 0).UTC(), time.Second))

	// Backup: started + completed → completed.
	r.PhaseStarted(1, PhaseBackup, "b")
	r.PhaseCompleted(1, PhaseBackup, "b done")
	// Transfer: started only → started.
	r.PhaseStarted(1, PhaseTransfer, "t")
	// Cutover: completed then failed → failed wins even though completion came first.
	r.PhaseCompleted(1, PhaseCutover, "c done")
	r.PhaseFailed(1, PhaseCutover, errors.New("rolled back"))

	tl := BuildTimeline(1, sink.Events())
	status := map[Phase]string{}
	for _, s := range tl.Phases {
		status[s.Phase] = s.Status
	}
	if status[PhaseBackup] != StatusCompleted {
		t.Errorf("backup status = %q, want completed", status[PhaseBackup])
	}
	if status[PhaseTransfer] != StatusStarted {
		t.Errorf("transfer status = %q, want started", status[PhaseTransfer])
	}
	if status[PhaseCutover] != StatusFailed {
		t.Errorf("cutover status = %q, want failed (failure must not be masked by completion)", status[PhaseCutover])
	}
}

// TestBuildTimelineSpanBounds verifies a span's Start/End bracket its events and
// Duration is non-negative.
func TestBuildTimelineSpanBounds(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink).withClock(fixedClock(time.Unix(3000, 0).UTC(), time.Second))

	r.PhaseStarted(1, PhaseTransfer, "t start") // t=3000
	r.Log(1, PhaseTransfer, SeverityInfo, "progress", nil) // t=3001
	r.PhaseCompleted(1, PhaseTransfer, "t done")           // t=3002

	tl := BuildTimeline(1, sink.Events())
	if len(tl.Phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(tl.Phases))
	}
	span := tl.Phases[0]
	if span.EventCount != 3 {
		t.Errorf("expected 3 events in span, got %d", span.EventCount)
	}
	if !span.Start.Equal(time.Unix(3000, 0).UTC()) {
		t.Errorf("span start = %v, want 3000", span.Start)
	}
	if !span.End.Equal(time.Unix(3002, 0).UTC()) {
		t.Errorf("span end = %v, want 3002", span.End)
	}
	if span.Duration() != 2*time.Second {
		t.Errorf("span duration = %v, want 2s", span.Duration())
	}
}

// TestConcurrentEmitIsRaceFree exercises concurrent emission for the race
// detector and verifies all events land.
func TestConcurrentEmitIsRaceFree(t *testing.T) {
	sink := NewMemorySink()
	r := NewRecorder(sink)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(mig int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				r.Log(mig+1, PhaseTransfer, SeverityInfo, "x", nil)
			}
		}(g)
	}
	wg.Wait()

	if got := len(sink.Events()); got != 8*50 {
		t.Fatalf("expected %d events, got %d", 8*50, got)
	}
}
