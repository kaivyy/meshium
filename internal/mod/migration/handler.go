package migration

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

// MigrationRunner exposes the planning and execution methods needed by the handler.
type MigrationRunner interface {
	Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error)
	Execute(ctx context.Context, migrationID int, onProgress StepCallback) error
	Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error
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
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// RegisterRoutes registers all migration routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/migrations", h.handleMigrations)
	mux.HandleFunc("/api/migrations/", h.handleMigrationByID)
	mux.HandleFunc("/ws/migrate/", h.handleMigrateWS)
	mux.HandleFunc("/ws/plan", h.handlePlanWS)
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
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

// --- REST handlers ---

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	migrations, err := h.repo.ListMigrations()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list migrations", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, migrations)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
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
		shared.WriteError(w, http.StatusInternalServerError, "failed to create plan: "+err.Error(), "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, plan)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id int) {
	migration, err := h.repo.GetMigration(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
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
		if strings.Contains(err.Error(), "not found") {
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
		shared.WriteError(w, http.StatusInternalServerError, "rollback failed: "+err.Error(), "INTERNAL")
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

	var req PlanRequest
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	} else {
		sourceID, _ := strconv.Atoi(r.URL.Query().Get("source"))
		targetID, _ := strconv.Atoi(r.URL.Query().Get("target"))
		categories := strings.Split(r.URL.Query().Get("categories"), ",")
		req = PlanRequest{
			SourceServerID: sourceID,
			TargetServerID: targetID,
			Categories:     categories,
		}
	}

	if req.SourceServerID == 0 || req.TargetServerID == 0 {
		http.Error(w, "sourceServerId and targetServerId are required", http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	_, err = h.runner.Plan(ctx, req, func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("websocket write failed: %v", writeErr)
			cancel()
		}
	})

	if err != nil {
		conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{Step: "plan", Status: "complete"})
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

	conn, err := h.upgrader.Upgrade(w, r, nil)
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
