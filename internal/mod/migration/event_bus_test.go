package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func newTestMigrationEvent(migrationID int, message string) MigrationEvent {
	return MigrationEvent{
		MigrationID:   migrationID,
		Level:         EventLevelInfo,
		Stage:         "apply",
		Type:          "progress",
		Message:       message,
		Details:       json.RawMessage(`{"step":"apply"}`),
		Source:        "engine",
		CorrelationID: "corr-123",
	}
}

type capturingEventRepo struct {
	*mockPipelineRepo
	mu     sync.Mutex
	events []MigrationEvent
}

func newCapturingEventRepo() *capturingEventRepo {
	return &capturingEventRepo{mockPipelineRepo: &mockPipelineRepo{}}
}

func (r *capturingEventRepo) CreateEvent(ctx context.Context, event MigrationEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *capturingEventRepo) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	filtered := make([]MigrationEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.MigrationID == migrationID && event.Sequence > afterSequence {
			filtered = append(filtered, event)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Sequence < filtered[j].Sequence
	})

	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	result := make([]MigrationEvent, limit)
	copy(result, filtered[:limit])
	return result, nil
}

func TestEventBus_EmitRejectsInvalidEvent(t *testing.T) {
	bus := NewEventBus(nil)

	err := bus.Emit(context.Background(), MigrationEvent{Message: "missing required fields"})
	if err == nil {
		t.Fatal("expected invalid event to be rejected")
	}
}

func TestEventBus_EmitAssignsSequence(t *testing.T) {
	bus := NewEventBus(nil)
	ch := bus.Subscribe(1)
	defer bus.Unsubscribe(1, ch)

	if err := bus.Emit(context.Background(), newTestMigrationEvent(1, "first")); err != nil {
		t.Fatalf("Emit first failed: %v", err)
	}
	if err := bus.Emit(context.Background(), newTestMigrationEvent(1, "second")); err != nil {
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

func TestEventBus_GetLastSequenceReturnsCurrentSequence(t *testing.T) {
	repo := newCapturingEventRepo()
	bus := NewEventBus(repo)

	if err := bus.Emit(context.Background(), newTestMigrationEvent(42, "first")); err != nil {
		t.Fatalf("Emit first failed: %v", err)
	}
	if err := bus.Emit(context.Background(), newTestMigrationEvent(42, "second")); err != nil {
		t.Fatalf("Emit second failed: %v", err)
	}

	if got := bus.GetLastSequence(42); got != 2 {
		t.Fatalf("GetLastSequence() = %d, want 2", got)
	}
}

func TestEventBus_SubscribeReceivesEvents(t *testing.T) {
	bus := NewEventBus(nil)
	ch := bus.Subscribe(7)
	defer bus.Unsubscribe(7, ch)

	event := newTestMigrationEvent(7, "migration step started")
	event.Level = EventLevelWarning

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

	if err := bus.Emit(context.Background(), newTestMigrationEvent(11, "before unsubscribe")); err != nil {
		t.Fatalf("Emit before unsubscribe failed: %v", err)
	}
	_ = waitForMigrationEvent(t, ch)

	bus.Unsubscribe(11, ch)

	if err := bus.Emit(context.Background(), newTestMigrationEvent(11, "after unsubscribe")); err != nil {
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
		if err := bus.Emit(context.Background(), newTestMigrationEvent(migrationID, msg)); err != nil {
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

func TestEventBus_ReplayEventsDeliversEventsInOrder(t *testing.T) {
	repo := newCapturingEventRepo()
	bus := NewEventBus(repo)

	for _, msg := range []string{"one", "two", "three"} {
		if err := bus.Emit(context.Background(), newTestMigrationEvent(33, msg)); err != nil {
			t.Fatalf("Emit %q failed: %v", msg, err)
		}
	}

	var replayed []MigrationEvent
	if err := bus.ReplayEvents(context.Background(), 33, 1, func(event MigrationEvent) {
		replayed = append(replayed, event)
	}); err != nil {
		t.Fatalf("ReplayEvents failed: %v", err)
	}

	if len(replayed) != 2 {
		t.Fatalf("ReplayEvents len = %d, want 2", len(replayed))
	}
	if replayed[0].Sequence != 2 || replayed[1].Sequence != 3 {
		t.Fatalf("ReplayEvents returned out of order events: %+v", replayed)
	}
	if replayed[0].Message != "two" || replayed[1].Message != "three" {
		t.Fatalf("ReplayEvents returned unexpected messages: %+v", replayed)
	}
}

func TestEventBus_ConcurrentEmitUsesUniqueSequences(t *testing.T) {
	repo := newCapturingEventRepo()
	bus := NewEventBus(repo)

	const total = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(total)

	for i := 0; i < total; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			if err := bus.Emit(context.Background(), newTestMigrationEvent(91, fmt.Sprintf("event-%d", i))); err != nil {
				t.Errorf("Emit %d failed: %v", i, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	repo.mu.Lock()
	events := append([]MigrationEvent(nil), repo.events...)
	repo.mu.Unlock()

	if len(events) != total {
		t.Fatalf("captured %d events, want %d", len(events), total)
	}

	seen := make(map[int64]struct{}, total)
	for _, event := range events {
		if _, ok := seen[event.Sequence]; ok {
			t.Fatalf("duplicate sequence detected: %d", event.Sequence)
		}
		seen[event.Sequence] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("unique sequence count = %d, want %d", len(seen), total)
	}
	if got := bus.GetLastSequence(91); got != total {
		t.Fatalf("GetLastSequence() = %d, want %d", got, total)
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
