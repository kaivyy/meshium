package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"
)

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

// EventBus manages migration events with persistence and pub/sub.
type EventBus struct {
	repo  PipelineRepo
	mu    sync.Mutex
	subs  map[int][]chan MigrationEvent // migrationID → subscribers
	seqMu sync.Mutex
	seqs  map[int]int64 // migrationID → last sequence number
}

// NewEventBus creates a new EventBus.
func NewEventBus(repo PipelineRepo) *EventBus {
	return &EventBus{
		repo: repo,
		subs: make(map[int][]chan MigrationEvent),
		seqs: make(map[int]int64),
	}
}

// Emit persists and publishes a migration event.
func (b *EventBus) Emit(ctx context.Context, event MigrationEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Assign sequence number
	b.seqMu.Lock()
	seq := b.seqs[event.MigrationID] + 1
	b.seqs[event.MigrationID] = seq
	event.Sequence = seq
	b.seqMu.Unlock()

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

// GetEvents retrieves events for a migration after a given sequence number.
func (b *EventBus) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	if b.repo == nil {
		return nil, fmt.Errorf("event bus has no repository")
	}
	return b.repo.GetEvents(ctx, migrationID, afterSequence, limit)
}
