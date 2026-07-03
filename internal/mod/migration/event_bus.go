package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"meshium/internal/shared"
)

const eventReplayBatchSize = 100

// EventLevel represents the severity of a migration event.
type EventLevel string

const (
	EventLevelInfo     EventLevel = "info"
	EventLevelWarning  EventLevel = "warning"
	EventLevelError    EventLevel = "error"
	EventLevelCritical EventLevel = "critical"
)

// MigrationEvent represents a single event in the migration lifecycle.
type MigrationEvent struct {
	ID            int64           `json:"id"`
	MigrationID   int             `json:"migrationId"`
	Sequence      int64           `json:"sequence"`
	Timestamp     time.Time       `json:"timestamp"`
	Level         EventLevel      `json:"level"`
	Stage         string          `json:"stage"`
	Type          string          `json:"type"`
	Message       string          `json:"message"`
	Details       json.RawMessage `json:"details,omitempty"`
	Source        string          `json:"source"`
	CorrelationID string          `json:"correlationId,omitempty"`
}

// Validate ensures the event contains the required fields and uses a known level.
func (e MigrationEvent) Validate() error {
	if e.MigrationID <= 0 {
		return fmt.Errorf("migrationId is required")
	}
	if e.Level == "" {
		return fmt.Errorf("level is required")
	}
	if e.Type == "" {
		return fmt.Errorf("type is required")
	}
	if e.Stage == "" {
		return fmt.Errorf("stage is required")
	}
	if e.Source == "" {
		return fmt.Errorf("source is required")
	}
	switch e.Level {
	case EventLevelInfo, EventLevelWarning, EventLevelError, EventLevelCritical:
	default:
		return fmt.Errorf("invalid level: %s", e.Level)
	}
	return nil
}

// EventBus manages migration events with persistence and pub/sub.
type EventBus struct {
	repo  PipelineRepo
	mu    sync.Mutex
	subs  map[int][]chan MigrationEvent // migrationID → subscribers
	seqMu sync.Mutex
	seqs  map[int]*int64 // migrationID → atomic counter
}

// NewEventBus creates a new EventBus.
func NewEventBus(repo PipelineRepo) *EventBus {
	return &EventBus{
		repo: repo,
		subs: make(map[int][]chan MigrationEvent),
		seqs: make(map[int]*int64),
	}
}

func (b *EventBus) sequenceCounter(migrationID int) *int64 {
	b.seqMu.Lock()
	defer b.seqMu.Unlock()

	if counter, ok := b.seqs[migrationID]; ok {
		return counter
	}

	counter := new(int64)
	b.seqs[migrationID] = counter
	return counter
}

// Emit persists and publishes a migration event.
func (b *EventBus) Emit(ctx context.Context, event MigrationEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Message = shared.SanitizeString(event.Message)
	event.Source = shared.SanitizeString(event.Source)
	event.Details = shared.SanitizeJSONRawMessage(event.Details)

	seq := atomic.AddInt64(b.sequenceCounter(event.MigrationID), 1)
	event.Sequence = seq

	// Persist to database
	if b.repo != nil {
		if err := b.repo.CreateEvent(ctx, event); err != nil {
			// Don't fail the operation, but log it
			// Persistence failure should not block the migration
		}
	}

	// Publish to subscribers
	b.mu.Lock()
	subs := b.subs[event.MigrationID]
	channels := make([]chan MigrationEvent, len(subs))
	copy(channels, subs)
	b.mu.Unlock()

	for _, ch := range channels {
		func(ch chan MigrationEvent) {
			defer func() {
				_ = recover()
			}()
			select {
			case ch <- event:
			default:
				// Drop event if subscriber is too slow
			}
		}(ch)
	}

	return nil
}

// Subscribe returns a channel that receives events for a migration.
func (b *EventBus) Subscribe(migrationID int) <-chan MigrationEvent {
	ch := make(chan MigrationEvent, 100)
	b.mu.Lock()
	b.subs[migrationID] = append(b.subs[migrationID], ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (b *EventBus) Unsubscribe(migrationID int, ch <-chan MigrationEvent) {
	if ch == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[migrationID]
	target := reflect.ValueOf(ch).Pointer()
	for i, sub := range subs {
		if reflect.ValueOf(sub).Pointer() == target {
			b.subs[migrationID] = append(subs[:i], subs[i+1:]...)
			close(sub)
			return
		}
	}
}

// GetLastSequence returns the latest known sequence number for a migration.
func (b *EventBus) GetLastSequence(migrationID int) int64 {
	b.seqMu.Lock()
	counter := b.seqs[migrationID]
	b.seqMu.Unlock()
	if counter != nil {
		return atomic.LoadInt64(counter)
	}
	if b.repo == nil {
		return 0
	}

	var afterSequence int64
	var lastSequence int64
	for {
		events, err := b.repo.GetEvents(context.Background(), migrationID, afterSequence, eventReplayBatchSize)
		if err != nil || len(events) == 0 {
			return lastSequence
		}
		lastSequence = events[len(events)-1].Sequence
		afterSequence = lastSequence
		if len(events) < eventReplayBatchSize {
			return lastSequence
		}
	}
}

// ReplayEvents retrieves stored events after the given sequence and publishes them to the callback.
func (b *EventBus) ReplayEvents(ctx context.Context, migrationID int, afterSequence int64, publish func(MigrationEvent)) error {
	if b.repo == nil {
		return fmt.Errorf("event bus has no repository")
	}
	if publish == nil {
		publish = func(MigrationEvent) {}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		events, err := b.repo.GetEvents(ctx, migrationID, afterSequence, eventReplayBatchSize)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		for _, event := range events {
			if err := ctx.Err(); err != nil {
				return err
			}
			publish(event)
			afterSequence = event.Sequence
		}
		if len(events) < eventReplayBatchSize {
			return nil
		}
	}
}

// GetEvents retrieves events for a migration after a given sequence number.
func (b *EventBus) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	if b.repo == nil {
		return nil, fmt.Errorf("event bus has no repository")
	}
	return b.repo.GetEvents(ctx, migrationID, afterSequence, limit)
}
