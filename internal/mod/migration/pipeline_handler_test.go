package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type eventReplayRepo struct {
	*mockPipelineRepo
	events          []MigrationEvent
	lastMigrationID int
	lastAfterSeq    int64
	lastLimit       int
}

func newEventReplayRepo(events []MigrationEvent) *eventReplayRepo {
	return &eventReplayRepo{
		mockPipelineRepo: &mockPipelineRepo{},
		events:           events,
	}
}

func (r *eventReplayRepo) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	r.lastMigrationID = migrationID
	r.lastAfterSeq = afterSequence
	r.lastLimit = limit

	filtered := make([]MigrationEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.MigrationID == migrationID && event.Sequence > afterSequence {
			filtered = append(filtered, event)
		}
	}
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	result := make([]MigrationEvent, limit)
	copy(result, filtered[:limit])
	return result, nil
}

func makeTestReplayEvents(migrationID int) []MigrationEvent {
	return []MigrationEvent{
		{MigrationID: migrationID, Sequence: 1, Level: EventLevelInfo, Stage: "planning", Type: "stage_started", Message: "event-1", Source: "pipeline"},
		{MigrationID: migrationID, Sequence: 2, Level: EventLevelInfo, Stage: "backup", Type: "stage_started", Message: "event-2", Source: "pipeline"},
		{MigrationID: migrationID, Sequence: 3, Level: EventLevelInfo, Stage: "snapshot", Type: "stage_started", Message: "event-3", Source: "pipeline"},
		{MigrationID: migrationID, Sequence: 4, Level: EventLevelInfo, Stage: "transferring", Type: "stage_started", Message: "event-4", Source: "pipeline"},
	}
}

func TestPipelineHandlerGetEventsEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantAfterSeq int64
		wantLimit    int
		wantSeqs     []int64
	}{
		{
			name:         "after_seq filters returned events",
			query:        "?after_seq=2",
			wantAfterSeq: 2,
			wantLimit:    100,
			wantSeqs:     []int64{3, 4},
		},
		{
			name:         "limit truncates returned events",
			query:        "?limit=2",
			wantAfterSeq: 0,
			wantLimit:    2,
			wantSeqs:     []int64{1, 2},
		},
		{
			name:         "after_seq and limit work together",
			query:        "?after_seq=1&limit=1",
			wantAfterSeq: 1,
			wantLimit:    1,
			wantSeqs:     []int64{2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const migrationID = 77
			repo := newEventReplayRepo(makeTestReplayEvents(migrationID))
			handler := NewPipelineHandler(nil, repo, nil)
			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			req := httptest.NewRequest(http.MethodGet, "/api/pipeline/migrations/77/events"+tt.query, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			if repo.lastMigrationID != migrationID {
				t.Fatalf("migration ID = %d, want %d", repo.lastMigrationID, migrationID)
			}
			if repo.lastAfterSeq != tt.wantAfterSeq {
				t.Fatalf("afterSeq = %d, want %d", repo.lastAfterSeq, tt.wantAfterSeq)
			}
			if repo.lastLimit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", repo.lastLimit, tt.wantLimit)
			}

			var got []MigrationEvent
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got) != len(tt.wantSeqs) {
				t.Fatalf("len(events) = %d, want %d", len(got), len(tt.wantSeqs))
			}
			for i, wantSeq := range tt.wantSeqs {
				if got[i].Sequence != wantSeq {
					t.Fatalf("events[%d].Sequence = %d, want %d", i, got[i].Sequence, wantSeq)
				}
			}
		})
	}
}
