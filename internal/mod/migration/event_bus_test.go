package migration

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestEventBus_EmitAssignsSequence(t *testing.T) {
	bus := NewEventBus(nil)
	ch := bus.Subscribe(1)
	defer bus.Unsubscribe(1, ch)

	if err := bus.Emit(context.Background(), MigrationEvent{MigrationID: 1, Message: "first"}); err != nil {
		t.Fatalf("Emit first failed: %v", err)
	}
	if err := bus.Emit(context.Background(), MigrationEvent{MigrationID: 1, Message: "second"}); err != nil {
		t.Fatalf("Emit second failed: %v", err)
	}

	first := waitForMigrationEvent(t, ch)
	second := waitForMigrationEvent(t, ch)

	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}
	if second.Sequence != 2 {
		t.Fatalf("second sequence = %d, want 2", second.Sequence)
	}
	if first.Timestamp.IsZero() || second.Timestamp.IsZero() {
		t.Fatal("expected timestamps to be assigned")
	}
}

func TestEventBus_SubscribeReceivesEvents(t *testing.T) {
	bus := NewEventBus(nil)
	ch := bus.Subscribe(7)
	defer bus.Unsubscribe(7, ch)

	event := MigrationEvent{
		MigrationID:   7,
		Level:         EventLevelWarning,
		Stage:         "apply",
		Type:          "progress",
		Message:       "migration step started",
		Details:       json.RawMessage(`{"step":"apply"}`),
		Source:        "engine",
		CorrelationID: "corr-123",
	}

	if err := bus.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	received := waitForMigrationEvent(t, ch)
	if received.MigrationID != event.MigrationID {
		t.Fatalf("migration ID = %d, want %d", received.MigrationID, event.MigrationID)
	}
	if received.Level != event.Level {
		t.Fatalf("level = %q, want %q", received.Level, event.Level)
	}
	if received.Stage != event.Stage || received.Type != event.Type || received.Message != event.Message {
		t.Fatalf("received event %+v does not match emitted event %+v", received, event)
	}
	if string(received.Details) != string(event.Details) {
		t.Fatalf("details = %s, want %s", string(received.Details), string(event.Details))
	}
	if received.Source != event.Source {
		t.Fatalf("source = %q, want %q", received.Source, event.Source)
	}
	if received.CorrelationID != event.CorrelationID {
		t.Fatalf("correlation ID = %q, want %q", received.CorrelationID, event.CorrelationID)
	}
}

func TestEventBus_UnsubscribeStopsReceiving(t *testing.T) {
	bus := NewEventBus(nil)
	ch := bus.Subscribe(11)

	if err := bus.Emit(context.Background(), MigrationEvent{MigrationID: 11, Message: "before unsubscribe"}); err != nil {
		t.Fatalf("Emit before unsubscribe failed: %v", err)
	}
	_ = waitForMigrationEvent(t, ch)

	bus.Unsubscribe(11, ch)

	if err := bus.Emit(context.Background(), MigrationEvent{MigrationID: 11, Message: "after unsubscribe"}); err != nil {
		t.Fatalf("Emit after unsubscribe failed: %v", err)
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected unsubscribe to stop receiving events")
	}
}

func TestEventBus_GetEventsAfterSequence(t *testing.T) {
	database, _ := newTestDB(t)
	defer database.Close()

	repo := NewRepo(database).(PipelineRepo)
	bus := NewEventBus(repo)
	migrationID := createTestMigration(t, repo.(JobRepository))

	messages := []string{"one", "two", "three"}
	for _, msg := range messages {
		if err := bus.Emit(context.Background(), MigrationEvent{MigrationID: migrationID, Message: msg}); err != nil {
			t.Fatalf("Emit %q failed: %v", msg, err)
		}
	}

	events, err := bus.GetEvents(context.Background(), migrationID, 1, 10)
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("GetEvents len = %d, want 2", len(events))
	}
	if events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("unexpected sequences: %+v", events)
	}
	if events[0].Message != "two" || events[1].Message != "three" {
		t.Fatalf("unexpected event messages: %+v", events)
	}
}

func waitForMigrationEvent(t *testing.T, ch <-chan MigrationEvent) MigrationEvent {
	t.Helper()
	select {
	case evt, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed unexpectedly")
		}
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for migration event")
		return MigrationEvent{}
	}
}
