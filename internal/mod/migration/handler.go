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

// MigrationRunner exposes the planning and execution methods needed by the handler.
type MigrationRunner interface {
	Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error)
	Execute(ctx context.Context, migrationID int, onProgress StepCallback) error
	Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error
	PreFlight(ctx context.Context, migrationID int, onProgress StepCallback) (*PreFlightResult, error)
	DryRun(ctx context.Context, migrationID int, onProgress StepCallback) (*DryRunResult, error)
	Diff(ctx context.Context, sourceID, targetID int, categories []string, onProgress StepCallback) (*DiffResult, error)
	// Parity computes the per-item compare for one migration (Phase 6B2).
	Parity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error)
	// ParitySummary computes the post-apply verification report (spec §H.5, 6B5).
	ParitySummary(ctx context.Context, migrationID int) (*ParitySummary, error)
	// BulkApply runs a 6B6 progressive-automation sweep (apply-safe / accept-risky).
	BulkApply(ctx context.Context, migrationID int, policy BulkPolicy) (*BulkResult, error)
	// RecomputeParity re-runs the live compare (POST parity/recompute).
	RecomputeParity(ctx context.Context, migrationID int, onProgress StepCallback) (*ParityResult, error)
	// InvalidateParity drops the cached compare for a migration after a state
	// change (selection/bulk) that would otherwise serve stale cached parity.
	InvalidateParity(migrationID int)
	// GetFollowUp returns the items still requiring operator follow-up.
	GetFollowUp(ctx context.Context, migrationID int) (*ParityResult, error)
}

// Handler exposes REST and WebSocket routes for migrations.
type Handler struct {
	runner   MigrationRunner
	repo     Repo
	upgrader websocket.Upgrader
}

// NewHandler creates a migration Handler.
func NewHandler(runner MigrationRunner, repo Repo) *Handler {
	return &Handler{
		runner: runner,
		repo:   repo,
		upgrader: websocket.Upgrader{
			CheckOrigin: shared.CheckWebSocketOrigin,
		},
	}
}

// RegisterRoutes registers all migration routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/migrations", h.handleMigrations)
	mux.HandleFunc("/api/migrations/", h.handleMigrationByID)
	mux.HandleFunc("/ws/migrate/", h.handleMigrateWS)
	mux.HandleFunc("/ws/plan", h.handlePlanWS)
	mux.HandleFunc("/ws/dryrun/", h.handleDryRunWS)
	mux.HandleFunc("/ws/diff", h.handleDiffWS)
	mux.HandleFunc("/api/diff", h.handleDiffREST)
}

// --- REST: /api/migrations ---

func (h *Handler) handleMigrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

// --- REST: /api/migrations/{id} ---

func (h *Handler) handleMigrationByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/migrations/"), "/")
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
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, id)
		case http.MethodDelete:
			h.handleDelete(w, r, id)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleRollback(w, r, id)
	case "steps":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetSteps(w, r, id)
	case "preflight":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePreFlight(w, r, id)
	case "dryrun":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleDryRunREST(w, r, id)
	case "export":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleExport(w, r, id)
	case "parity":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleParity(w, r, id)
	case "selection":
		switch r.Method {
		case http.MethodGet:
			h.handleGetSelection(w, r, id)
		case http.MethodPut:
			h.handlePutSelection(w, r, id)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
	case "parity-summary":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleParitySummary(w, r, id)
	case "selection/bulk":
		if r.Method != http.MethodPut {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleBulkSelection(w, r, id)
	case "parity/recompute":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleParityRecompute(w, r, id)
	case "follow-up":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleFollowUp(w, r, id)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

// --- REST handlers ---

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	// Reconciliation for an in-flight/completed create-plan: a client that lost
	// the terminal WS frame re-asks by operationId. Backend is authoritative.
	if op := r.URL.Query().Get("operationId"); op != "" {
		m, err := h.repo.GetMigrationByOperationID(op)
		if err != nil {
			shared.WriteJSON(w, http.StatusOK, []interface{}{})
			return
		}
		shared.WriteJSON(w, http.StatusOK, []Migration{*m})
		return
	}
	migrations, err := h.repo.ListMigrations()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list migrations", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, migrations)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	shared.LimitRequestBody(r)
	var req PlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceServerID == 0 || req.TargetServerID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceServerId and targetServerId are required", "VALIDATION_ERROR")
		return
	}

	if len(req.Categories) == 0 {
		shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
		return
	}

	// Plan synchronously (for REST, without WebSocket progress)
	plan, err := h.runner.Plan(r.Context(), req, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to create plan", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, plan)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.repo.GetMigration(id)
	if err != nil {
		if IsMigrationNotFound(err) {
			shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get migration", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, migration)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.repo.DeleteMigration(id); err != nil {
		if IsMigrationNotFound(err) {
			shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to delete migration", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request, id int) {
	err := h.runner.Rollback(r.Context(), id, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "rollback failed", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back"})
}

func (h *Handler) handleGetSteps(w http.ResponseWriter, r *http.Request, id int) {
	steps, err := h.repo.GetSteps(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get steps", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, steps)
}

// --- WebSocket handlers ---

func (h *Handler) handlePlanWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		log.Printf("migration runner not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Try query params first (for clients that pass params in URL)
	var req PlanRequest
	sourceID, _ := strconv.Atoi(r.URL.Query().Get("source"))
	targetID, _ := strconv.Atoi(r.URL.Query().Get("target"))
	categories := strings.Split(r.URL.Query().Get("categories"), ",")

	// Upgrade to WebSocket first
	conn, err := h.upgradeWebSocket(w, r)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// If source/target not in query params, read from first WebSocket message
	if sourceID == 0 || targetID == 0 {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: "failed to read plan request: " + err.Error()})
			return
		}
		if err := json.Unmarshal(msgBytes, &req); err != nil {
			conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: "invalid plan request: " + err.Error()})
			return
		}
		conn.SetReadDeadline(time.Time{}) // reset deadline
	} else {
		req = PlanRequest{
			SourceServerID: sourceID,
			TargetServerID: targetID,
			Categories:     categories,
		}
	}

	if req.SourceServerID == 0 || req.TargetServerID == 0 {
		conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: "sourceServerId and targetServerId are required"})
		return
	}

	// Idempotency: if a client resubmits the same operationId (refresh/retry/
	// reconnect), return the existing recoverable migration instead of creating a
	// second one. Only recoverable statuses are deduped; a terminal failure is
	// left to the caller (the FE reuses the same op for a safe retry).
	if req.OperationID != "" {
		if existing, derr := h.repo.GetMigrationByOperationID(req.OperationID); derr == nil {
			// Return the existing in-flight/valid plan for refresh/reconnect/retry
			// idempotency — never insert a duplicate. StatusFailed is deliberately
			// excluded: a retry after failure must create a FRESH plan, so it is
			// allowed to fall through to CreateMigration again (the op string is
			// not unique-constrained; reconcile reads ORDER BY id DESC so it sees
			// the newest row). GetMigrationByOperationID on a refresh of a failed
			// op still returns it, so the FE shows the failed banner (rule 2: no
			// discarded finished state).
			switch existing.Status {
			case StatusPlanned, StatusRunning, StatusInterrupted:
				conn.WriteJSON(WSMessage{
					Step:   "plan",
					Status: "complete",
					Value:  fmt.Sprintf("migration_id:%d", existing.ID),
				})
				return
			}
		}
	}

	plan, err := h.runner.Plan(ctx, req, func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("websocket write failed: %v", writeErr)
			cancel()
		}
	})

	if err != nil {
		conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: err.Error()})
		return
	}

	migrationID := 0
	if plan != nil {
		migrationID = plan.ID
	}
	// Terminal frame MUST carry the migration id (and the operation echo) so the
	// FE can reconcile without regex fragility. The migration is already
	// persisted (CreateMigration runs early in Plan), so this is the authoritative
	// completion signal — but the FE treats completion as REST-confirmed, never
	// trusting the frame alone.
	conn.WriteJSON(WSMessage{
		Step:   "plan",
		Status: "complete",
		Value:  fmt.Sprintf("migration_id:%d", migrationID),
	})
}

func (h *Handler) handleMigrateWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		log.Printf("migration runner not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/migrate/")
	if path == r.URL.Path || path == "" {
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
	if len(parts) > 1 && parts[1] == "rollback" {
		action = "rollback"
	}

	conn, err := h.upgradeWebSocket(w, r)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var runErr error
	if action == "rollback" {
		runErr = h.runner.Rollback(ctx, migrationID, func(msg WSMessage) {
			if writeErr := conn.WriteJSON(msg); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	} else {
		runErr = h.runner.Execute(ctx, migrationID, func(msg WSMessage) {
			if writeErr := conn.WriteJSON(msg); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	}

	if runErr != nil {
		conn.WriteJSON(WSMessage{
			Step:   action,
			Status: "error",
			Error:  runErr.Error(),
		})
		return
	}

	conn.WriteJSON(WSMessage{Step: action, Status: "complete"})
}

// --- Pre-Flight REST handler ---

func (h *Handler) handlePreFlight(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}

	result, err := h.runner.PreFlight(r.Context(), id, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "pre-flight failed", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) handleDryRunREST(w http.ResponseWriter, r *http.Request, id int) {
	result, err := h.runner.DryRun(r.Context(), id, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "dry run failed", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}

// --- Dry Run WebSocket handler ---

func (h *Handler) handleDryRunWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/dryrun/")
	migrationID, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		http.Error(w, "invalid migration ID", http.StatusBadRequest)
		return
	}

	conn, err := h.upgradeWebSocket(w, r)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	_, err = h.runner.DryRun(ctx, migrationID, func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("websocket write failed: %v", writeErr)
			cancel()
		}
	})

	if err != nil {
		conn.WriteJSON(WSMessage{Step: "dryrun", Status: "error", Error: err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{Step: "dryrun", Status: "complete"})
}

// --- Diff REST handler ---

func (h *Handler) handleDiffREST(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req struct {
		SourceID   int      `json:"sourceId"`
		TargetID   int      `json:"targetId"`
		Categories []string `json:"categories"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceID == 0 || req.TargetID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceId and targetId are required", "VALIDATION_ERROR")
		return
	}

	result, err := h.runner.Diff(r.Context(), req.SourceID, req.TargetID, req.Categories, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "diff failed", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

// --- Diff WebSocket handler ---

func (h *Handler) handleDiffWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Try query params first
	var req struct {
		SourceID   int      `json:"sourceId"`
		TargetID   int      `json:"targetId"`
		Categories []string `json:"categories"`
	}
	sourceID, _ := strconv.Atoi(r.URL.Query().Get("source"))
	targetID, _ := strconv.Atoi(r.URL.Query().Get("target"))
	categories := strings.Split(r.URL.Query().Get("categories"), ",")

	// Upgrade to WebSocket first
	conn, err := h.upgradeWebSocket(w, r)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// If source/target not in query params, read from first WebSocket message
	if sourceID == 0 || targetID == 0 {
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			conn.WriteJSON(WSMessage{Step: "diff", Status: "error", Error: "failed to read diff request: " + err.Error()})
			return
		}
		if err := json.Unmarshal(msgBytes, &req); err != nil {
			conn.WriteJSON(WSMessage{Step: "diff", Status: "error", Error: "invalid diff request: " + err.Error()})
			return
		}
		conn.SetReadDeadline(time.Time{})
	} else {
		req = struct {
			SourceID   int      `json:"sourceId"`
			TargetID   int      `json:"targetId"`
			Categories []string `json:"categories"`
		}{SourceID: sourceID, TargetID: targetID, Categories: categories}
	}

	if req.SourceID == 0 || req.TargetID == 0 {
		conn.WriteJSON(WSMessage{Step: "diff", Status: "error", Error: "sourceId and targetId are required"})
		return
	}

	_, err = h.runner.Diff(ctx, req.SourceID, req.TargetID, req.Categories, func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("websocket write failed: %v", writeErr)
			cancel()
		}
	})

	if err != nil {
		conn.WriteJSON(WSMessage{Step: "diff", Status: "error", Error: err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{Step: "diff", Status: "complete"})
}

// --- Export handler ---

func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.repo.GetMigration(id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
		return
	}

	steps, err := h.repo.GetSteps(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get steps", "INTERNAL")
		return
	}

	export := map[string]interface{}{
		"migration": migration,
		"steps":     steps,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"migration-%d.json\"", id))
	json.NewEncoder(w).Encode(export)
}

// --- Parity + Selection handlers (Phase 6B2) ---

func (h *Handler) handleParity(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}
	result, err := h.runner.Parity(r.Context(), id, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "parity failed: "+err.Error(), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}

// handleGetSelection returns all recorded selection decisions for a migration.
func (h *Handler) handleGetSelection(w http.ResponseWriter, r *http.Request, id int) {
	decisions, err := h.repo.GetSelections(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to load selections", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, decisions)
}

// selectionRequest is the PUT body for one item decision (§C.2 actions).
type selectionRequest struct {
	ItemKey           string `json:"itemKey"`
	Category          string `json:"category"`
	Action            string `json:"action"` // apply_from_source|keep_target|skip|review_manual
	DecisionReason    string `json:"decisionReason,omitempty"`     // Phase 6C-BE
	RiskAcknowledged  bool   `json:"riskAcknowledged"`            // Phase 6C-BE
	ManualFollowup    string `json:"manualFollowup,omitempty"`    // Phase 6C-BE
}

func (h *Handler) handlePutSelection(w http.ResponseWriter, r *http.Request, id int) {
	shared.LimitRequestBody(r)
	var req selectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}
	if req.ItemKey == "" || req.Action == "" {
		shared.WriteError(w, http.StatusBadRequest, "itemKey and action are required", "VALIDATION_ERROR")
		return
	}
	switch ParityAction(req.Action) {
	case ActionApplyFromSource, ActionKeepTarget, ActionSkip, ActionReviewManual:
	default:
		shared.WriteError(w, http.StatusBadRequest, "invalid action", "VALIDATION_ERROR")
		return
	}
	// Append-only audit: record the prior action so the decision change is
	// tamper-evident (Phase 6C-BE §F.2). A new item has no prior action.
	prevAction, _ := h.repo.GetSelection(r.Context(), id, req.ItemKey)
	if err := h.repo.AppendSelectionHistory(r.Context(), id, SelectionHistory{
		ItemKey:    req.ItemKey,
		FromAction: prevAction,
		ToAction:   req.Action,
		Reason:     req.DecisionReason,
		Actor:      "operator",
	}); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to record selection history", "INTERNAL")
		return
	}
	if err := h.repo.UpsertSelection(r.Context(), id, req.ItemKey, req.Category, req.Action, req.DecisionReason, req.RiskAcknowledged, req.ManualFollowup); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to persist selection", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "itemKey": req.ItemKey, "action": req.Action})
	// A selection changes the derived parity/summary, so drop the cached compare.
	h.runner.InvalidateParity(id)
}

// handleParitySummary returns the post-apply verification report (spec §H.5):
// 3 independent scores (infra / runtime / app health) + manual gaps + unresolved
// drift. Derived from the compare items + step outcomes + selections.
func (h *Handler) handleParitySummary(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}
	summary, err := h.runner.ParitySummary(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "parity summary failed: "+err.Error(), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, summary)
}

// handleBulkSelection runs a 6B6 progressive-automation sweep (apply-safe /
// accept-risky) and persists the resulting selection decisions. Explicit
// operator selections are never overwritten.
func (h *Handler) handleBulkSelection(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}
	shared.LimitRequestBody(r)
	var req struct {
		Policy string `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}
	var policy BulkPolicy
	switch req.Policy {
	case string(BulkApplySafe), string(BulkAcceptRiskyUnchanged),
		string(BulkSkipSelected), string(BulkReviewManualSelected), string(BulkClear):
		policy = BulkPolicy(req.Policy)
	default:
		shared.WriteError(w, http.StatusBadRequest, "invalid policy", "VALIDATION_ERROR")
		return
	}
	res, err := h.runner.BulkApply(r.Context(), id, policy)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "bulk apply failed: "+err.Error(), "INTERNAL")
		return
	}
	h.runner.InvalidateParity(id)
	shared.WriteJSON(w, http.StatusOK, res)
}

// handleParityRecompute re-runs the live compare for a migration (POST
// parity/recompute). Useful after a target change or a manual fix so the
// per-item state refreshes without a full re-plan.
func (h *Handler) handleParityRecompute(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}
	result, err := h.runner.RecomputeParity(r.Context(), id, func(WSMessage) {})
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "parity recompute failed: "+err.Error(), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}

// handleFollowUp returns the items still requiring operator follow-up
// (skip / review_manual / unknown / verify_failed) for a migration.
func (h *Handler) handleFollowUp(w http.ResponseWriter, r *http.Request, id int) {
	if h == nil || h.runner == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}
	result, err := h.runner.GetFollowUp(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "follow-up failed: "+err.Error(), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	responseHeader := http.Header{}
	if proto := auth.WebSocketSubprotocolToken(r); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	return h.upgrader.Upgrade(w, r, responseHeader)
}
