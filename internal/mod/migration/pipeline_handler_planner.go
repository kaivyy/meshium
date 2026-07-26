package migration

import (
	"meshium/internal/shared"
	"net/http"
)

// Planner read endpoints: the analysis a plan produced (workloads, dependency
// graph, compatibility, strategy, warnings) served back to the wizard.
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
