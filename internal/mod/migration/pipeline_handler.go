package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/auth"
	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

// PipelineHandler exposes REST and WebSocket routes for the zero-downtime
// migration pipeline. It extends the existing Handler with new endpoints
// for risk assessment, compatibility checks, provisioning, health checks,
// replication, traffic switching, and real-time pipeline progress.
type PipelineHandler struct {
	pipeline *Pipeline
	repo     PipelineRepo
	baseRepo Repo
	upgrader websocket.Upgrader
}

// NewPipelineHandler creates a new PipelineHandler.
func NewPipelineHandler(pipeline *Pipeline, repo PipelineRepo, baseRepo Repo) *PipelineHandler {
	return &PipelineHandler{
		pipeline: pipeline,
		repo:     repo,
		baseRepo: baseRepo,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				// Allow same-origin requests
				host := r.Header.Get("Host")
				if strings.Contains(origin, host) {
					return true
				}
				// Allow localhost for development
				if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
					return true
				}
				return false
			},
		},
	}
}

// RegisterRoutes registers all pipeline routes on the mux.
func (h *PipelineHandler) RegisterRoutes(mux *http.ServeMux) {
	// REST endpoints
	mux.HandleFunc("/api/pipeline/migrations", h.handleCreatePipelineMigration)
	mux.HandleFunc("/api/pipeline/migrations/", h.handlePipelineMigrationByID)
	mux.HandleFunc("/api/pipeline/risk/", h.handleRisk)
	mux.HandleFunc("/api/pipeline/compatibility/", h.handleCompatibility)
	mux.HandleFunc("/api/pipeline/health/", h.handleHealth)
	mux.HandleFunc("/api/pipeline/replication/", h.handleReplication)
	mux.HandleFunc("/api/pipeline/traffic/", h.handleTraffic)
	mux.HandleFunc("/api/pipeline/metrics/", h.handleMetrics)
	mux.HandleFunc("/api/pipeline/audit/", h.handleAudit)

	// WebSocket endpoints
	mux.HandleFunc("/ws/pipeline/", h.handlePipelineWS)
}

// --- REST: Create Pipeline Migration ---

func (h *PipelineHandler) handleCreatePipelineMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	shared.LimitRequestBody(r)
	var req struct {
		SourceID   int              `json:"sourceId"`
		TargetID   int              `json:"targetId"`
		Categories []string         `json:"categories"`
		Config     *MigrationConfig `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceID == 0 || req.TargetID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceId and targetId are required", "VALIDATION_ERROR")
		return
	}
	if len(req.Categories) == 0 {
		shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
		return
	}

	if req.Config == nil {
		req.Config = DefaultMigrationConfig()
	}
	req.Config.Categories = req.Categories

	// Create migration record directly via base repo
	migrationID, err := h.baseRepo.CreateMigration(req.SourceID, req.TargetID, req.Categories)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create migration: %v", err), "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      migrationID,
		"status":  "created",
		"message": "Migration created. Use /ws/pipeline/{id}/execute to start.",
	})
}

// --- REST: Pipeline Migration by ID ---

func (h *PipelineHandler) handlePipelineMigrationByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/pipeline/migrations/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetPipelineSession(w, r, id)
		return
	}

	switch parts[1] {
	case "stages":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetStages(w, r, id)
	case "cancel":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleCancel(w, r, id)
	case "pause":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePause(w, r, id)
	case "resume":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleResume(w, r, id)
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePipelineRollback(w, r, id)
	case "export":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePipelineExport(w, r, id)
	case "actions":
		if len(parts) < 3 {
			shared.WriteError(w, http.StatusBadRequest, "action is required", "VALIDATION_ERROR")
			return
		}
		h.handlePipelineAction(w, r, id, parts[2])
	case "events":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetEvents(w, r, id)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func (h *PipelineHandler) handlePipelineAction(w http.ResponseWriter, r *http.Request, id int, action string) {
	switch action {
	case "cutover":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Cutover(r.Context(), id, nil) }, "cutover")
	case "commit":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Commit(r.Context(), id, nil) }, "committed")
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Rollback(r.Context(), id, nil) }, "rolled_back")
	case "pause":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Pause(r.Context(), id, nil) }, "paused")
	case "resume":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Resume(r.Context(), id, nil) }, "resumed")
	case "cancel":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Cancel(r.Context(), id, nil) }, "cancelled")
	case "retry":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Retry(r.Context(), id, nil) }, "retried")
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func handlePipelineActionResult(w http.ResponseWriter, fn func() error, status string) {
	if err := fn(); err != nil {
		shared.WriteError(w, http.StatusConflict, err.Error(), "INVALID_STATE")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": status})
}

// --- REST: Get Pipeline Session ---

func (h *PipelineHandler) handleGetPipelineSession(w http.ResponseWriter, r *http.Request, id int) {
	session, err := h.buildSession(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
		return
	}
	shared.WriteJSON(w, http.StatusOK, session)
}

// --- REST: Get Stages ---

func (h *PipelineHandler) handleGetStages(w http.ResponseWriter, r *http.Request, id int) {
	stages, err := h.repo.GetStages(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get stages", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, stages)
}

// --- REST: Cancel ---

func (h *PipelineHandler) handleCancel(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Cancel(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("cancel failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// --- REST: Pause ---
// Pause is implemented by cancelling the context of the running pipeline.
// The migration will be marked as interrupted and can be resumed later.

func (h *PipelineHandler) handlePause(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Pause(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusConflict, err.Error(), "INVALID_STATE")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// --- REST: Resume ---

func (h *PipelineHandler) handleResume(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Resume(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("resume failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

// --- REST: Rollback ---

func (h *PipelineHandler) handlePipelineRollback(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Rollback(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("rollback failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back"})
}

// --- REST: Risk ---

func (h *PipelineHandler) handleRisk(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/risk/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	if r.Method == http.MethodGet {
		report, err := h.repo.GetRiskReport(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get risk report", "INTERNAL")
			return
		}
		if report == nil {
			shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"riskScore": 0, "riskClass": "not_assessed"})
			return
		}
		shared.WriteJSON(w, http.StatusOK, report)
		return
	}

	if r.Method == http.MethodPost {
		// Run risk assessment
		var input RiskInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
			return
		}

		engine := NewRiskEngine(h.repo)
		report, err := engine.AssessRisk(r.Context(), id, input)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("risk assessment failed: %v", err), "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, report)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

// --- REST: Compatibility ---

func (h *PipelineHandler) handleCompatibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/compatibility/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	// Get existing results
	results, err := h.repo.GetVerificationResults(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get compatibility results", "INTERNAL")
		return
	}

	// Filter to compatibility results only
	var compatResults []VerificationResult
	for _, r := range results {
		if r.VerificationType == "compatibility" {
			compatResults = append(compatResults, r)
		}
	}

	shared.WriteJSON(w, http.StatusOK, compatResults)
}

// --- REST: Health ---

func (h *PipelineHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/health/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	if r.Method == http.MethodGet {
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil {
				limit = v
			}
		}
		history, err := h.repo.GetHealthHistory(id, limit)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get health history", "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, history)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

// --- REST: Replication ---

func (h *PipelineHandler) handleReplication(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/replication/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	status, err := h.repo.GetReplicationStatus(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get replication status", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, status)
}

// --- REST: Traffic ---

func (h *PipelineHandler) handleTraffic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/traffic/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	if r.Method == http.MethodGet {
		cfg, err := h.repo.GetTrafficSwitchConfig(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get traffic config", "INTERNAL")
			return
		}
		if cfg == nil {
			shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"switchState": "not_configured"})
			return
		}
		shared.WriteJSON(w, http.StatusOK, cfg)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

// --- REST: Metrics ---

func (h *PipelineHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/metrics/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}

	metrics, err := h.repo.GetMetrics(id, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get metrics", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, metrics)
}

// --- REST: Audit ---

func (h *PipelineHandler) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/audit/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}

	trail, err := h.repo.GetAuditTrail(id, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get audit trail", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, trail)
}

// --- REST: Event Replay ---

// handleGetEvents returns migration events after a given sequence number.
// This is used by the frontend to replay missed events after a WebSocket reconnect.
// Query params: after_seq (int64, default 0), limit (int, default 100)
func (h *PipelineHandler) handleGetEvents(w http.ResponseWriter, r *http.Request, id int) {
	afterSeq := int64(0)
	if s := r.URL.Query().Get("after_seq"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			afterSeq = v
		}
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 1000 {
			limit = v
		}
	}
	events, err := h.repo.GetEvents(r.Context(), id, afterSeq, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get events", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, events)
}

// --- REST: Export ---

func (h *PipelineHandler) handlePipelineExport(w http.ResponseWriter, r *http.Request, id int) {
	session, err := h.buildSession(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"migration-%d-report.json\"", id))
	json.NewEncoder(w).Encode(session)
}

// --- WebSocket: Pipeline Progress ---

func (h *PipelineHandler) handlePipelineWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.pipeline == nil {
		log.Printf("pipeline not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/pipeline/")
	if path == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.Split(path, "/")
	migrationID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid migration ID", http.StatusBadRequest)
		return
	}

	action := "execute"
	if len(parts) > 1 {
		action = parts[1]
	}

	// Upgrade with subprotocol support for secure WS auth
	responseHeader := http.Header{}
	if proto := auth.WebSocketSubprotocolToken(r); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	conn, err := h.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Heartbeat: send ping every 30s, close if no pong within 10s
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		lastPong := time.Now()
		conn.SetPongHandler(func(appData string) error {
			lastPong = time.Now()
			return nil
		})
		for {
			select {
			case <-ticker.C:
				if time.Since(lastPong) > 40*time.Second {
					// No pong received for too long — close connection
					log.Printf("websocket heartbeat timeout for migration %d", migrationID)
					cancel()
					return
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Sequence counter for WS messages
	var wsSeq int64

	// Start a goroutine to read client messages (for pause/cancel/heartbeat)
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(msg, &cmd) != nil {
				continue
			}
			switch cmd.Action {
			case "pause":
				h.pipeline.Pause(ctx, migrationID, nil)
			case "cancel":
				h.pipeline.Cancel(ctx, migrationID, nil)
			case "resume":
				h.pipeline.Resume(ctx, migrationID, nil)
			case "ping":
				// Client-initiated ping — respond with pong
				conn.WriteJSON(WSMessageExtended{Step: "heartbeat", Status: "pong", Timestamp: time.Now().Format(time.RFC3339)})
			}
		}
	}()

	var runErr error
	switch action {
	case "execute":
		runErr = h.pipeline.Execute(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := WSMessageExtended{
				Step:      msg.Step,
				Status:    msg.Status,
				Value:     msg.Value,
				Error:     msg.Error,
				Timestamp: time.Now().Format(time.RFC3339),
				Sequence:  wsSeq,
			}
			if writeErr := conn.WriteJSON(ext); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	case "rollback":
		runErr = h.pipeline.Rollback(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := WSMessageExtended{
				Step:      msg.Step,
				Status:    msg.Status,
				Value:     msg.Value,
				Error:     msg.Error,
				Timestamp: time.Now().Format(time.RFC3339),
				Sequence:  wsSeq,
			}
			if writeErr := conn.WriteJSON(ext); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	default:
		conn.WriteJSON(WSMessageExtended{Step: action, Status: "error", Error: "unknown action"})
		return
	}

	if runErr != nil {
		conn.WriteJSON(WSMessageExtended{
			Step:   action,
			Status: "error",
			Error:  runErr.Error(),
		})
		return
	}

	conn.WriteJSON(WSMessageExtended{Step: action, Status: "complete"})
}

// --- Helpers ---

// buildSession constructs a full MigrationSession from the database.
func (h *PipelineHandler) buildSession(ctx context.Context, migrationID int) (*MigrationSession, error) {
	// Get base migration
	migration, err := h.baseRepo.GetMigration(migrationID)
	if err != nil {
		return nil, err
	}

	// Build session
	session := &MigrationSession{
		Migration: migration,
	}

	// Get config
	config, _ := h.repo.GetMigrationConfig(migrationID)
	if config != nil {
		session.Config = config
	}

	// Get state from status string
	if migration.Status != "" {
		state, _ := StateFromString(migration.Status)
		session.State = state
	}

	// Load related data
	session.Stages, _ = h.repo.GetStages(migrationID)
	session.ReplicationStatus, _ = h.repo.GetReplicationStatus(migrationID)
	session.TrafficSwitch, _ = h.repo.GetTrafficSwitchConfig(migrationID)
	session.RiskReport, _ = h.repo.GetRiskReport(migrationID)
	session.HealthHistory, _ = h.repo.GetHealthHistory(migrationID, 50)
	session.CutoverHistory, _ = h.repo.GetCutoverHistory(migrationID)
	session.RollbackHistory, _ = h.repo.GetRollbackHistory(migrationID)
	session.SyncSessions, _ = h.repo.GetSyncSessions(migrationID)
	session.QueueStates, _ = h.repo.GetQueueStates(migrationID)
	session.ProvisionStates, _ = h.repo.GetProvisionStates(migrationID)

	return session, nil
}

// wsProgressAdapter converts a WSMessage callback to WSMessageExtended.
func wsProgressAdapter(fn func(WSMessageExtended)) func(WSMessage) {
	return func(msg WSMessage) {
		fn(WSMessageExtended{
			Step:      msg.Step,
			Status:    msg.Status,
			Value:     msg.Value,
			Error:     msg.Error,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}
