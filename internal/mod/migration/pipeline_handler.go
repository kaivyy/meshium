package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshium/internal/mod/auth"
	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

// wsConn serializes data-frame writes to a websocket connection.
// gorilla/websocket permits only one concurrent writer for data frames, but
// the pipeline WS handler writes from three goroutines: the progress callback
// driving Execute/Rollback, the client read loop (pong replies), and the
// terminal status/error write. Routing every WriteJSON through this mutex
// prevents interleaved, corrupted frames. WriteControl (the heartbeat ping) is
// exempt from that restriction per gorilla's contract, so it stays on the raw
// conn and is intentionally not routed through here.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsConn) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

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
			CheckOrigin: shared.CheckWebSocketOrigin,
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
	normalizeMigrationConfig(req.Config, req.Categories)

	// Create migration record directly via base repo
	migrationID, err := h.baseRepo.CreateMigration(req.SourceID, req.TargetID, req.Categories)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create migration: %v", err), "INTERNAL")
		return
	}
	if err := h.repo.SetMigrationConfig(migrationID, req.Config); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store migration config: %v", err), "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      migrationID,
		"status":  "created",
		"message": "Migration created. Use /ws/pipeline/{id}/execute to start.",
	})
}

// --- Batch 2: Planner Result Handlers ---

// handleGetWorkloads returns the workload classifications for a migration.
func (h *PipelineHandler) handleGetWorkloads(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	workloads, ok := plan["workloads"]
	if !ok {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	shared.WriteJSON(w, http.StatusOK, workloads)
}

// handleGetDependencyGraph returns the dependency graph for a migration.
func (h *PipelineHandler) handleGetDependencyGraph(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, map[string]any{"nodes": []any{}, "edges": []any{}})
		return
	}
	graph, ok := plan["dependencyGraph"]
	if !ok {
		shared.WriteJSON(w, http.StatusOK, map[string]any{"nodes": []any{}, "edges": []any{}})
		return
	}
	shared.WriteJSON(w, http.StatusOK, graph)
}

// handleGetCompatibility returns the compatibility issues for a migration.
func (h *PipelineHandler) handleGetCompatibility(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	issues, ok := plan["compatibilityIssues"]
	if !ok {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	shared.WriteJSON(w, http.StatusOK, issues)
}

// handleGetStrategy returns the migration strategy for a migration.
func (h *PipelineHandler) handleGetStrategy(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}
	strategy, ok := plan["strategy"]
	if !ok {
		shared.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}
	shared.WriteJSON(w, http.StatusOK, strategy)
}

// handleGetWarnings returns the planner warnings for a migration.
func (h *PipelineHandler) handleGetWarnings(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	warnings, ok := plan["warnings"]
	if !ok {
		shared.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	shared.WriteJSON(w, http.StatusOK, warnings)
}

// handleGetPlannerResult returns the full planner result for a migration.
func (h *PipelineHandler) handleGetPlannerResult(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}
	_ = migration
	plan, err := h.repo.GetPlan(r.Context(), id)
	if err != nil || plan == nil {
		shared.WriteJSON(w, http.StatusOK, map[string]any{
			"workloads":           []any{},
			"dependencyGraph":     map[string]any{"nodes": []any{}, "edges": []any{}},
			"compatibilityIssues": []any{},
			"strategy":            map[string]any{},
			"warnings":            []any{},
			"riskScore":           0,
			"blockingIssues":      0,
			"recommendationCount": 0,
		})
		return
	}
	shared.WriteJSON(w, http.StatusOK, plan)
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
	case "workloads":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetWorkloads(w, r, id)
	case "dependency-graph":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetDependencyGraph(w, r, id)
	case "compatibility":
		h.handleCompatibilityByID(w, r, id)
	case "config":
		h.handleConfigByID(w, r, id)
	case "risk":
		h.handleRiskByID(w, r, id)
	case "health":
		h.handleHealthByID(w, r, id)
	case "replication":
		h.handleReplicationByID(w, r, id)
	case "traffic":
		h.handleTrafficByID(w, r, id)
	case "metrics":
		h.handleMetricsByID(w, r, id)
	case "audit":
		h.handleAuditByID(w, r, id)
	case "queue":
		h.handleQueueByID(w, r, id)
	case "provision":
		h.handleProvisionByID(w, r, id)
	case "strategy":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetStrategy(w, r, id)
	case "warnings":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetWarnings(w, r, id)
	case "planner-result":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetPlannerResult(w, r, id)
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

func normalizeMigrationConfig(config *MigrationConfig, categories []string) {
	if config == nil {
		return
	}
	if len(categories) > 0 {
		config.Categories = categories
	}
	if config.ObservationDuration > 0 && config.ObservationDuration < time.Second {
		config.ObservationDuration *= time.Second
	}
	if config.MaxErrorRate > 1 {
		config.MaxErrorRate = config.MaxErrorRate / 100
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay > 0 && config.RetryDelay < time.Second {
		config.RetryDelay *= time.Second
	}
	if config.ParallelTransfers == 0 {
		config.ParallelTransfers = 4
	}
}

func (h *PipelineHandler) handleConfigByID(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.repo.GetMigrationConfig(id)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, "migration config not found", "NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		shared.LimitRequestBody(r)
		var cfg MigrationConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
			return
		}
		normalizeMigrationConfig(&cfg, cfg.Categories)
		if len(cfg.Categories) == 0 {
			shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
			return
		}
		if err := h.repo.SetMigrationConfig(id, &cfg); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store migration config: %v", err), "INTERNAL")
			return
		}
		if updater, ok := h.baseRepo.(interface {
			SetMigrationCategories(int, []string) error
		}); ok {
			if err := updater.SetMigrationCategories(id, cfg.Categories); err != nil {
				shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update migration categories: %v", err), "INTERNAL")
				return
			}
		}
		shared.WriteJSON(w, http.StatusOK, cfg)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
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
	h.handleRiskByID(w, r, id)
}

func (h *PipelineHandler) handleRiskByID(w http.ResponseWriter, r *http.Request, id int) {
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
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/compatibility/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleCompatibilityByID(w, r, id)
}

func (h *PipelineHandler) handleCompatibilityByID(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		results, err := h.getStoredCompatibilityResults(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get compatibility results", "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, results)
	case http.MethodPost:
		if r.URL.Query().Get("refresh") != "true" {
			stored, err := h.getStoredCompatibilityResults(id)
			if err != nil {
				shared.WriteError(w, http.StatusInternalServerError, "failed to get compatibility results", "INTERNAL")
				return
			}
			if len(stored) > 0 {
				shared.WriteJSON(w, http.StatusOK, stored)
				return
			}
		}

		results := h.runCompatibilityPreflight(r.Context(), id)
		for _, result := range results {
			_, _ = h.repo.CreateVerificationResult(r.Context(), VerificationResult{
				MigrationID:      id,
				VerificationType: "compatibility",
				Target:           result.CheckName,
				Expected:         string(result.Severity),
				Actual:           result.Message,
				Passed:           result.Passed,
				ErrorMessage:     compatibilityErrorMessage(result),
			})
		}
		shared.WriteJSON(w, http.StatusOK, results)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

func compatibilityErrorMessage(result CompatibilityCheckResult) string {
	if result.Passed {
		return ""
	}
	return result.Message
}

func (h *PipelineHandler) getStoredCompatibilityResults(id int) ([]CompatibilityCheckResult, error) {
	verifications, err := h.repo.GetVerificationResults(id)
	if err != nil {
		return nil, err
	}
	results := make([]CompatibilityCheckResult, 0)
	for _, result := range verifications {
		if result.VerificationType != "compatibility" {
			continue
		}
		severity := Severity(result.Expected)
		if severity != SeverityInfo && severity != SeverityWarning && severity != SeverityHigh && severity != SeverityCritical {
			severity = SeverityInfo
		}
		message := result.Actual
		if message == "" {
			message = result.ErrorMessage
		}
		results = append(results, CompatibilityCheckResult{
			CheckName: result.Target,
			Severity:  severity,
			Passed:    result.Passed,
			Message:   message,
		})
	}
	return results, nil
}

func (h *PipelineHandler) runCompatibilityPreflight(ctx context.Context, id int) []CompatibilityCheckResult {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		return []CompatibilityCheckResult{{
			CheckName: "migration",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   "migration not found",
		}}
	}

	results := []CompatibilityCheckResult{{
		CheckName: "source_target",
		Severity:  SeverityCritical,
		Passed:    migration.SourceID != migration.TargetID,
		Message:   "source and target are different servers",
	}}
	if migration.SourceID == migration.TargetID {
		results[0].Message = "source and target must be different servers"
	}

	available := map[string]struct{}{}
	for _, category := range NewCategoryRegistry().Available() {
		available[category] = struct{}{}
	}
	for _, category := range migration.Categories {
		_, ok := available[category]
		message := fmt.Sprintf("category %q is supported by the migration executor", category)
		if !ok {
			message = fmt.Sprintf("category %q is not supported by the migration executor", category)
		}
		results = append(results, CompatibilityCheckResult{
			CheckName: "category:" + category,
			Severity:  SeverityCritical,
			Passed:    ok,
			Message:   message,
		})
	}

	if h.pipeline == nil {
		return results
	}

	sourceServer, sourceErr := h.pipeline.srvRepo.GetByID(migration.SourceID)
	targetServer, targetErr := h.pipeline.srvRepo.GetByID(migration.TargetID)
	if sourceErr != nil || targetErr != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "server_records",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   fmt.Sprintf("server lookup failed: source=%v target=%v", sourceErr, targetErr),
		})
		return results
	}

	sourceSSH, sourceErr := getSSHClientForServer(migration.SourceID, sourceServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	targetSSH, targetErr := getSSHClientForServer(migration.TargetID, targetServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	if sourceErr != nil || targetErr != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "ssh_connectivity",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   fmt.Sprintf("ssh connection failed: source=%v target=%v", sourceErr, targetErr),
		})
		return results
	}

	sshResults, err := NewCompatibilityEngine(sourceSSH, targetSSH, h.repo).CheckCompatibility(ctx, id)
	if err != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "server_compatibility",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   err.Error(),
		})
		return results
	}
	return append(results, sshResults...)
}

// --- REST: Health ---

func (h *PipelineHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/health/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleHealthByID(w, r, id)
}

func (h *PipelineHandler) handleHealthByID(w http.ResponseWriter, r *http.Request, id int) {
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

	if r.Method == http.MethodPost {
		results := h.runHealthPreflight(r.Context(), id)
		for _, result := range results {
			_, _ = h.repo.CreateHealthCheckResult(r.Context(), result)
		}
		shared.WriteJSON(w, http.StatusOK, results)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

func (h *PipelineHandler) runHealthPreflight(ctx context.Context, id int) []HealthCheckResult {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			CheckType:    HealthCheckTCP,
			CheckTarget:  "migration",
			Status:       "error",
			ErrorMessage: "migration not found",
			HealthScore:  0,
		}}
	}
	if h.pipeline == nil {
		return []HealthCheckResult{}
	}

	targetServer, err := h.pipeline.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			ServerID:     migration.TargetID,
			CheckType:    HealthCheckTCP,
			CheckTarget:  "target",
			Status:       "error",
			ErrorMessage: err.Error(),
			HealthScore:  0,
		}}
	}
	targetSSH, err := getSSHClientForServer(migration.TargetID, targetServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	if err != nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			ServerID:     migration.TargetID,
			CheckType:    HealthCheckTCP,
			CheckTarget:  targetServer.Host,
			Status:       "error",
			ErrorMessage: err.Error(),
			HealthScore:  0,
		}}
	}

	start := time.Now()
	_, _, _, err = targetSSH.ExecContext(ctx, "echo ok")
	result := HealthCheckResult{
		MigrationID:    id,
		ServerID:       migration.TargetID,
		CheckType:      HealthCheckTCP,
		CheckTarget:    targetServer.Host,
		ResponseTimeMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return []HealthCheckResult{result}
	}
	result.Status = "healthy"
	result.HealthScore = 100
	return []HealthCheckResult{result}
}

// --- REST: Replication ---

func (h *PipelineHandler) handleReplication(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/replication/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleReplicationByID(w, r, id)
}

func (h *PipelineHandler) handleReplicationByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
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
	h.handleTrafficByID(w, r, id)
}

func (h *PipelineHandler) handleTrafficByID(w http.ResponseWriter, r *http.Request, id int) {
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
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/metrics/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleMetricsByID(w, r, id)
}

func (h *PipelineHandler) handleMetricsByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
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
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/audit/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleAuditByID(w, r, id)
}

func (h *PipelineHandler) handleAuditByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
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

func (h *PipelineHandler) handleQueueByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}
	states, err := h.repo.GetQueueStates(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get queue states", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, states)
}

func (h *PipelineHandler) handleProvisionByID(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
		states, err := h.repo.GetProvisionStates(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get provision states", "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, states)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
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

	// All data-frame writes go through safeConn to serialize the three writer
	// goroutines (progress callback, read loop, terminal status). The heartbeat
	// goroutine keeps using conn.WriteControl directly, which gorilla allows.
	safeConn := &wsConn{conn: conn}

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
				safeConn.WriteJSON(WSMessageExtended{Step: "heartbeat", Status: "pong", Timestamp: time.Now().Format(time.RFC3339)})
			}
		}
	}()

	var runErr error
	switch action {
	case "execute":
		// The pipeline WS URL carries no explicit action, so this "execute"
		// branch is the default for every connection — including reconnects and
		// streaming-only clients. Running Execute unconditionally would relaunch
		// an already running, finished, or failed migration on every connect
		// (and, after a process restart, re-run one that has no in-process
		// run-lock to stop it). Only start Execute from a startable state; for
		// anything else, stream persisted history and hold the connection open
		// so the client can observe without re-executing.
		state, stateOK := h.currentMigrationState(migrationID)
		startable := stateOK && (state == StateCreated || state == StateResuming)
		if !startable {
			h.streamPipelineHistory(ctx, safeConn, migrationID, &wsSeq)
			// Keep the connection open (read loop handles resume/cancel/pause,
			// heartbeat keeps it alive) until the client disconnects.
			<-ctx.Done()
			return
		}
		runErr = h.pipeline.Execute(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := h.extendWSMessage(migrationID, msg, wsSeq)
			if writeErr := safeConn.WriteJSON(ext); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	case "rollback":
		runErr = h.pipeline.Rollback(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := h.extendWSMessage(migrationID, msg, wsSeq)
			if writeErr := safeConn.WriteJSON(ext); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	default:
		safeConn.WriteJSON(WSMessageExtended{Step: action, Status: "error", Error: "unknown action"})
		return
	}

	if runErr != nil {
		safeConn.WriteJSON(h.extendWSMessage(migrationID, WSMessage{Step: action, Status: "error", Error: runErr.Error()}, wsSeq+1))
		return
	}

	safeConn.WriteJSON(h.extendWSMessage(migrationID, WSMessage{Step: action, Status: "complete"}, wsSeq+1))
}

// --- Helpers ---

// streamPipelineHistory writes the persisted event history for a migration to
// the WebSocket connection. It is used when a client connects to a migration
// that is not in a startable state (already running, finished, or failed) so
// the client can observe past progress without re-executing the migration.
// wsSeq is advanced so any subsequent live messages keep monotonically
// increasing sequence numbers.
func (h *PipelineHandler) streamPipelineHistory(ctx context.Context, conn *wsConn, migrationID int, wsSeq *int64) {
	events, err := h.repo.GetEvents(ctx, migrationID, 0, 1000)
	if err != nil {
		log.Printf("failed to load event history for migration %d: %v", migrationID, err)
		return
	}
	for _, ev := range events {
		status := "info"
		switch ev.Level {
		case EventLevelError, EventLevelCritical:
			status = "error"
		case EventLevelWarning:
			status = "warning"
		}
		step := ev.Stage
		if step == "" {
			step = ev.Type
		}
		*wsSeq++
		ext := h.extendWSMessage(migrationID, WSMessage{
			Step:   step,
			Status: status,
			Value:  ev.Message,
		}, *wsSeq)
		if writeErr := conn.WriteJSON(ext); writeErr != nil {
			log.Printf("websocket history write failed for migration %d: %v", migrationID, writeErr)
			return
		}
	}
}

func (h *PipelineHandler) extendWSMessage(migrationID int, msg WSMessage, sequence int64) WSMessageExtended {
	ext := WSMessageExtended{
		Step:      msg.Step,
		Status:    msg.Status,
		Value:     msg.Value,
		Error:     msg.Error,
		Timestamp: time.Now().Format(time.RFC3339),
		Sequence:  sequence,
	}

	stage, stageIndex := pipelineStageFromStep(msg.Step)
	if stage != "" {
		total := len(AllStages())
		ext.Stage = string(stage)
		ext.StageIndex = stageIndex + 1
		ext.StageTotal = total
		ext.Progress = float64(stageIndex+1) / float64(total) * 100
		ext.CurrentState = stateForPipelineStage(stage).StateString()
		return ext
	}

	if state, ok := h.currentMigrationState(migrationID); ok {
		ext.CurrentState = state.StateString()
	}
	return ext
}

func pipelineStageFromStep(step string) (PipelineStageName, int) {
	base := step
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	aliases := map[string]PipelineStageName{
		"pre_cutover": StagePreCutoverValidation,
		"observation": StagePostCutoverObservation,
	}
	if stage, ok := aliases[base]; ok {
		base = string(stage)
	}
	for i, stage := range AllStages() {
		if string(stage) == base {
			return stage, i
		}
	}
	return "", -1
}

func stateForPipelineStage(stage PipelineStageName) MigrationState {
	switch stage {
	case StageDiscovery:
		return StateDiscovery
	case StageAnalysis:
		return StateCompatibilityCheck
	case StagePlanning:
		return StatePlanning
	case StageValidation, StageHealthVerification:
		return StateVerification
	case StagePreparation:
		return StateBackup
	case StageInitialSync:
		return StateInitialSync
	case StageLiveReplication:
		return StateLiveReplication
	case StagePreCutoverValidation:
		return StatePreCutover
	case StageTrafficSwitch:
		return StateTrafficSwitch
	case StagePostCutoverObservation:
		return StateObservation
	case StageFinalization, StageArchive:
		return StateCommitted
	default:
		return StateCreated
	}
}

func (h *PipelineHandler) currentMigrationState(migrationID int) (MigrationState, bool) {
	if stateRepo, ok := h.baseRepo.(interface {
		GetMigrationState(int) (MigrationState, error)
	}); ok {
		if state, err := stateRepo.GetMigrationState(migrationID); err == nil {
			return state, true
		}
	}
	if h.baseRepo == nil {
		return StateCreated, false
	}
	migration, err := h.baseRepo.GetMigration(migrationID)
	if err != nil || migration == nil {
		return StateCreated, false
	}
	state, err := StateFromString(migration.Status)
	return state, err == nil
}

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
	session.VerificationResults, _ = h.repo.GetVerificationResults(migrationID)
	session.CompatibilityResults, _ = h.getStoredCompatibilityResults(migrationID)
	session.HealthHistory, _ = h.repo.GetHealthHistory(migrationID, 50)
	session.CutoverHistory, _ = h.repo.GetCutoverHistory(migrationID)
	session.RollbackHistory, _ = h.repo.GetRollbackHistory(migrationID)
	session.SyncSessions, _ = h.repo.GetSyncSessions(migrationID)
	session.QueueStates, _ = h.repo.GetQueueStates(migrationID)
	session.ProvisionStates, _ = h.repo.GetProvisionStates(migrationID)
	session.AuditTrail, _ = h.repo.GetAuditTrail(migrationID, 200)
	session.Events, _ = h.repo.GetEvents(ctx, migrationID, 0, 200)

	// Restore the latest dry-run preview so step 4 keeps its change list
	// across a page refresh (the value is only held in frontend memory
	// otherwise).
	if step, _ := h.repo.GetLatestStep(migrationID, "dryrun"); step != nil && step.Data != "" {
		var dr DryRunResult
		if json.Unmarshal([]byte(step.Data), &dr) == nil {
			session.DryRun = &dr
		}
	}

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
