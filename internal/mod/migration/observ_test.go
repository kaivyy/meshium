package migration

import (
	"context"
	"testing"

	"meshium/internal/mod/observ"
)

// panicSink is a Sink whose HandleEvent always panics. It models a misbehaving
// observability backend: the Recorder must isolate the panic so the migration
// completes regardless. Used by TestEngineRunSurvivesFailingSink.
type panicSink struct{ observ.NopSink }

func (panicSink) HandleEvent(observ.Event) { panic("sink boom") }

// eventTypes returns the set of event Type strings present in evs, for order-
// independent assertions about which lifecycle events were emitted.
func eventTypes(evs []observ.Event) map[string]bool {
	set := make(map[string]bool, len(evs))
	for _, e := range evs {
		set[e.Type] = true
	}
	return set
}

// TestEngineRunEmitsLifecycleEvents wires a MemorySink recorder into the real
// job-engine migration path (Engine.Run over a committed migration) and asserts
// the lifecycle events actually reach the sink, carrying migration_id, stage,
// timestamp, status, and a message. This proves the observability seam is wired
// into a real path, not just defined.
func TestEngineRunEmitsLifecycleEvents(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	sink := observ.NewMemorySink()
	engine.SetRecorder(observ.NewRecorder(sink))

	steps := []MigrationStep{newMockStep("packages"), newMockStep("configs")}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.FinalState != StateCommitted {
		t.Fatalf("expected final state %s, got %s", StateCommitted, result.FinalState)
	}

	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("no events reached the sink; observability is not wired into the real path")
	}

	types := eventTypes(events)
	// A committed run must produce the start, per-stage lifecycle, and completion
	// events. Failure/interrupt events are covered by the state-machine tests that
	// drive those paths.
	for _, want := range []string{
		EventMigrationStarted,
		EventStageStarted,
		EventStageCompleted,
		EventMigrationCompleted,
	} {
		if !types[want] {
			t.Errorf("expected a %q event on the committed path; got types %v", want, types)
		}
	}

	// Every event must carry the contract fields: migration_id, stage, timestamp,
	// status, and a message. These are what a timeline/audit store indexes on.
	for _, e := range events {
		if e.MigrationID != migrationID {
			t.Errorf("event %q: migrationId = %d, want %d", e.Type, e.MigrationID, migrationID)
		}
		if e.Phase == "" {
			t.Errorf("event %q: empty stage/phase", e.Type)
		}
		if e.Time.IsZero() {
			t.Errorf("event %q: zero timestamp", e.Type)
		}
		if e.Message == "" {
			t.Errorf("event %q: empty message", e.Type)
		}
		if e.Fields["status"] == "" {
			t.Errorf("event %q: missing status field", e.Type)
		}
	}
}

// TestEngineRunSurvivesFailingSink installs a Sink that panics on every event
// and asserts the migration still commits. Observability must never be a fatal
// dependency: a broken backend degrades to no telemetry, never a failed
// migration.
func TestEngineRunSurvivesFailingSink(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	engine.SetRecorder(observ.NewRecorder(panicSink{}))

	steps := []MigrationStep{newMockStep("packages"), newMockStep("configs")}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err != nil {
		t.Fatalf("a failing event sink must not fail the migration; got error: %v", err)
	}
	if result.FinalState != StateCommitted {
		t.Fatalf("expected final state %s despite failing sink, got %s", StateCommitted, result.FinalState)
	}
}
