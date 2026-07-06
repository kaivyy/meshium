package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestPipelineHandlerCreateStoresConfig(t *testing.T) {
	repo := newTestRepo(t)
	_ = createTestMigration(t, repo)
	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{
		"sourceId": 1,
		"targetId": 2,
		"categories": ["packages"],
		"config": {
			"categories": ["docker"],
			"replicationEnabled": false,
			"freezeWriteOnCutover": false,
			"drainQueuesOnCutover": false,
			"autoRollbackOnError": false,
			"trafficProvider": "nginx",
			"trafficConfig": "{\"configPath\":\"/etc/nginx/conf.d/app.conf\",\"newServer\":\"10.0.0.2:80\"}"
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/pipeline/migrations", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}

	var created struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	cfg, err := repo.(PipelineRepo).GetMigrationConfig(created.ID)
	if err != nil {
		t.Fatalf("GetMigrationConfig(): %v", err)
	}
	if cfg == nil {
		t.Fatal("config was not stored")
	}
	if len(cfg.Categories) != 1 || cfg.Categories[0] != "packages" {
		t.Fatalf("stored categories = %#v, want request categories", cfg.Categories)
	}
	if cfg.ReplicationEnabled {
		t.Fatal("replicationEnabled = true, want false from request config")
	}
	if cfg.TrafficConfig == "" {
		t.Fatal("trafficConfig was not stored")
	}
}

func TestPipelineHandlerConfigEndpointUpdatesConfig(t *testing.T) {
	repo := newTestRepo(t)
	migrationID := createTestMigration(t, repo)
	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{
		"categories": ["docker"],
		"replicationEnabled": false,
		"freezeWriteOnCutover": false,
		"drainQueuesOnCutover": false,
		"autoRollbackOnError": true,
		"observationDuration": 120000000000
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/pipeline/migrations/1/config", body)
	w := httptest.NewRecorder()
	req.URL.Path = "/api/pipeline/migrations/" + strconv.Itoa(migrationID) + "/config"
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	cfg, err := repo.(PipelineRepo).GetMigrationConfig(migrationID)
	if err != nil {
		t.Fatalf("GetMigrationConfig(): %v", err)
	}
	if len(cfg.Categories) != 1 || cfg.Categories[0] != "docker" {
		t.Fatalf("stored categories = %#v, want [docker]", cfg.Categories)
	}
}

func TestPipelineHandlerAcceptsFrontendPostPreflightMethods(t *testing.T) {
	repo := newTestRepo(t)
	migrationID := createTestMigration(t, repo)
	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, path := range []string{"compatibility", "health"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/pipeline/migrations/"+strconv.Itoa(migrationID)+"/"+path, bytes.NewBufferString(`{}`))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want %d; body=%s", path, w.Code, http.StatusOK, w.Body.String())
			}
		})
	}
}

func TestPipelineHandlerCompatibilityPostUsesStoredResults(t *testing.T) {
	repo := newTestRepo(t)
	migrationID := createTestMigration(t, repo)
	pipelineRepo := repo.(PipelineRepo)

	if _, err := pipelineRepo.CreateVerificationResult(context.Background(), VerificationResult{
		MigrationID:      migrationID,
		VerificationType: "compatibility",
		Target:           "cached_check",
		Expected:         string(SeverityInfo),
		Actual:           "cached compatibility result",
		Passed:           true,
	}); err != nil {
		t.Fatalf("CreateVerificationResult(): %v", err)
	}

	handler := NewPipelineHandler(nil, pipelineRepo, repo.(Repo))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/pipeline/migrations/"+strconv.Itoa(migrationID)+"/compatibility", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var results []CompatibilityCheckResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(results) != 1 || results[0].CheckName != "cached_check" {
		t.Fatalf("results = %#v, want cached result only", results)
	}

	verifications, err := pipelineRepo.GetVerificationResults(migrationID)
	if err != nil {
		t.Fatalf("GetVerificationResults(): %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("verification count = %d, want no duplicate records", len(verifications))
	}
}

func TestPipelineHandlerSessionIncludesReplayRecords(t *testing.T) {
	repo := newTestRepo(t)
	migrationID := createTestMigration(t, repo)
	pipelineRepo := repo.(PipelineRepo)

	if _, err := pipelineRepo.CreateVerificationResult(context.Background(), VerificationResult{
		MigrationID:      migrationID,
		VerificationType: "compatibility",
		Target:           "source_target",
		Expected:         string(SeverityInfo),
		Actual:           "source and target are different servers",
		Passed:           true,
	}); err != nil {
		t.Fatalf("CreateVerificationResult(): %v", err)
	}
	if _, err := pipelineRepo.CreateAuditEntry(context.Background(), AuditEntry{
		MigrationID: migrationID,
		EventType:   "compatibility_completed",
		NewState:    StateRiskAssessment.String(),
	}); err != nil {
		t.Fatalf("CreateAuditEntry(): %v", err)
	}
	if err := pipelineRepo.CreateEvent(context.Background(), MigrationEvent{
		MigrationID: migrationID,
		Sequence:    1,
		Level:       EventLevelInfo,
		Stage:       StateCompatibilityCheck.StateString(),
		Type:        EventTypeCompatibilityCompleted,
		Message:     "compatibility check completed",
		Source:      "test",
	}); err != nil {
		t.Fatalf("CreateEvent(): %v", err)
	}

	handler := NewPipelineHandler(nil, pipelineRepo, repo.(Repo))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/migrations/"+strconv.Itoa(migrationID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var compatibility []CompatibilityCheckResult
	if err := json.Unmarshal(body["compatibilityResults"], &compatibility); err != nil {
		t.Fatalf("compatibilityResults missing or invalid: %v; body=%s", err, w.Body.String())
	}
	if len(compatibility) != 1 || compatibility[0].CheckName != "source_target" || !compatibility[0].Passed {
		t.Fatalf("compatibilityResults = %#v, want stored result", compatibility)
	}

	var audit []AuditEntry
	if err := json.Unmarshal(body["auditTrail"], &audit); err != nil {
		t.Fatalf("auditTrail missing or invalid: %v; body=%s", err, w.Body.String())
	}
	if len(audit) != 1 || audit[0].EventType != "compatibility_completed" {
		t.Fatalf("auditTrail = %#v, want stored audit entry", audit)
	}

	var events []MigrationEvent
	if err := json.Unmarshal(body["events"], &events); err != nil {
		t.Fatalf("events missing or invalid: %v; body=%s", err, w.Body.String())
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events = %#v, want stored event", events)
	}
}

func TestPipelineHandlerExtendsWSMessageWithStageState(t *testing.T) {
	repo := newTestRepo(t)
	migrationID := createTestMigration(t, repo)
	handler := NewPipelineHandler(nil, repo.(PipelineRepo), repo.(Repo))

	msg := handler.extendWSMessage(migrationID, WSMessage{
		Step:   "live_replication",
		Status: "progress",
		Value:  "replication running",
	}, 9)

	if msg.Sequence != 9 {
		t.Fatalf("Sequence = %d, want 9", msg.Sequence)
	}
	if msg.CurrentState != StateLiveReplication.StateString() {
		t.Fatalf("CurrentState = %q, want %q", msg.CurrentState, StateLiveReplication.StateString())
	}
	if msg.Stage != string(StageLiveReplication) {
		t.Fatalf("Stage = %q, want %q", msg.Stage, StageLiveReplication)
	}
	if msg.StageIndex == 0 || msg.StageTotal != len(AllStages()) {
		t.Fatalf("stage progress = %d/%d, want non-zero index and total %d", msg.StageIndex, msg.StageTotal, len(AllStages()))
	}
	if msg.Progress <= 0 {
		t.Fatalf("Progress = %.1f, want > 0", msg.Progress)
	}
}
