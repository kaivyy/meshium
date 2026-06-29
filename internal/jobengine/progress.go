package jobengine

import (
	"sync"
)

// ProgressBroadcaster is a channel-based pub/sub system for job progress.
// Subscribers receive progress updates on a channel. The broadcast is
// non-blocking: if a subscriber's channel is full, the update is dropped
// rather than blocking the broadcaster.
type ProgressBroadcaster interface {
	// Subscribe returns a channel that receives progress updates for the
	// given job ID. The channel has a buffer of 16 entries.
	Subscribe(jobID string) chan JobProgress
	// Unsubscribe removes a subscriber from the given job ID.
	Unsubscribe(jobID string, ch chan JobProgress)
	// Broadcast sends a progress update to all subscribers of the given job.
	// This is non-blocking: if a subscriber's channel is full, the update
	// is dropped.
	Broadcast(jobID string, progress JobProgress)
	// Cleanup removes all subscribers for the given job ID.
	Cleanup(jobID string)
}

// DefaultProgressBroadcaster implements ProgressBroadcaster using
// per-job subscriber maps protected by a mutex.
type DefaultProgressBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan JobProgress]struct{}
}

// NewDefaultProgressBroadcaster creates a new DefaultProgressBroadcaster.
func NewDefaultProgressBroadcaster() *DefaultProgressBroadcaster {
	return &DefaultProgressBroadcaster{
		subscribers: make(map[string]map[chan JobProgress]struct{}),
	}
}

// Subscribe returns a channel that receives progress updates for the
// given job ID. The channel is buffered with 16 entries.
func (b *DefaultProgressBroadcaster) Subscribe(jobID string) chan JobProgress {
	ch := make(chan JobProgress, 16)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subscribers[jobID] == nil {
		b.subscribers[jobID] = make(map[chan JobProgress]struct{})
	}
	b.subscribers[jobID][ch] = struct{}{}

	return ch
}

// Unsubscribe removes a subscriber from the given job ID.
func (b *DefaultProgressBroadcaster) Unsubscribe(jobID string, ch chan JobProgress) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[jobID]; ok {
		if _, exists := subs[ch]; exists {
			close(ch)
			delete(subs, ch)
		}
		if len(subs) == 0 {
			delete(b.subscribers, jobID)
		}
	}
}

// Broadcast sends a progress update to all subscribers of the given job.
// This is non-blocking: if a subscriber's channel is full, the update
// is dropped (not blocked).
func (b *DefaultProgressBroadcaster) Broadcast(jobID string, progress JobProgress) {
	b.mu.RLock()
	subs, ok := b.subscribers[jobID]
	b.mu.RUnlock()

	if !ok {
		return
	}

	// Copy the subscriber channels under the read lock to avoid
	// holding the lock during the send.
	channels := make([]chan JobProgress, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}

	for _, ch := range channels {
		select {
		case ch <- progress:
		default:
			// Channel full — drop the update (non-blocking)
		}
	}
}

// Cleanup removes all subscribers for the given job ID and closes
// their channels. Called when a job is finished.
func (b *DefaultProgressBroadcaster) Cleanup(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[jobID]; ok {
		for ch := range subs {
			close(ch)
		}
		delete(b.subscribers, jobID)
	}
}
