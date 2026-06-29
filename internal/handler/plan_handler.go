package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"meshium/internal/jobengine"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/planner"
	"meshium/internal/shared"
)

// PlanHandler exposes REST routes for the Migration Planner.
type PlanHandler struct {
	planner        planner.Planner
	planStore      planner.PlanStore
	snapshotStore  discovery.SnapshotStore
	engine         *jobengine.Engine
}

// NewPlanHandler creates a PlanHandler.
func NewPlanHandler(
	p planner.Planner,
	planStore planner.PlanStore,
	snapshotStore discovery.SnapshotStore,
	engine *jobengine.Engine,
) *PlanHandler {
	return &PlanHandler{
		planner:       p,
		planStore:     planStore,
		snapshotStore: snapshotStore,
		engine:        engine,
	}
}

// RegisterRoutes registers all plan routes on the mux.
func (h *PlanHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/plans", h.handlePlans)
	mux.HandleFunc("/api/plans/", h.handlePlanByID)
}

// --- REST: /api/plans ---

func (h *PlanHandler) handlePlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

// --- REST: /api/plans/{id} ---

func (h *PlanHandler) handlePlanByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/plans/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	planID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, planID)
		case http.MethodDelete:
			h.handleDelete(w, r, planID)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "execute":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleExecute(w, r, planID)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

// --- REST handlers ---

func (h *PlanHandler) handleList(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.planStore.ListPlans(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list plans", "INTERNAL")
		return
	}

	// Map to FE-expected response shape
	result := make([]map[string]interface{}, len(summaries))
	for i, s := range summaries {
		result[i] = map[string]interface{}{
			"id":            s.ID,
			"createdAt":     s.CreatedAt,
			"sourceHost":    s.Source,
			"targetHost":    s.Target,
			"stepCount":     s.StepCount,
			"riskLevel":     s.RiskLevel,
			"hasBlockers":   s.HasBlockers,
			"totalSizeBytes": 0, // not tracked in summary
		}
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *PlanHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	shared.LimitRequestBody(r)
	var req struct {
		SourceID int `json:"sourceID"`
		TargetID int `json:"targetID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceID == 0 || req.TargetID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceID and targetID are required", "VALIDATION_ERROR")
		return
	}

	// Load snapshots
	sourceSnapshot, err := h.snapshotStore.LoadSnapshot(req.SourceID)
	if err != nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "Server has not been scanned yet. Run a discovery scan first.", "SNAPSHOT_NOT_FOUND")
		return
	}

	targetSnapshot, err := h.snapshotStore.LoadSnapshot(req.TargetID)
	if err != nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "Target server has not been scanned yet. Run a discovery scan first.", "SNAPSHOT_NOT_FOUND")
		return
	}

	// Create plan
	plan, err := h.planner.CreatePlan(r.Context(), sourceSnapshot, targetSnapshot)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to create plan", "INTERNAL")
		return
	}

	// Save plan
	if err := h.planStore.SavePlan(r.Context(), plan); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to save plan", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, plan)
}

func (h *PlanHandler) handleGet(w http.ResponseWriter, r *http.Request, planID string) {
	plan, err := h.planStore.LoadPlan(r.Context(), planID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "plan not found", "PLAN_NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, plan)
}

func (h *PlanHandler) handleDelete(w http.ResponseWriter, r *http.Request, planID string) {
	if err := h.planStore.DeletePlan(r.Context(), planID); err != nil {
		shared.WriteError(w, http.StatusNotFound, "plan not found", "PLAN_NOT_FOUND")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *PlanHandler) handleExecute(w http.ResponseWriter, r *http.Request, planID string) {
	// Load plan
	plan, err := h.planStore.LoadPlan(r.Context(), planID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "plan not found", "PLAN_NOT_FOUND")
		return
	}

	// Check for blockers
	if plan.HasBlockers() {
		shared.WriteJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error":    "plan has blockers — cannot execute",
			"code":     "PLAN_HAS_BLOCKERS",
			"blockers": plan.Blockers,
		})
		return
	}

	// Submit migration job
	jobReq := jobengine.JobRequest{
		Type:   jobengine.JobTypeMigration,
		PlanID: planID,
	}

	job, err := h.engine.Submit(r.Context(), jobReq)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to submit migration job", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]string{
		"jobID": job.ID,
	})
}
