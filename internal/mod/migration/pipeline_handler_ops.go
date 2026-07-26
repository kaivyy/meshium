package migration

import (
	"encoding/json"
	"fmt"
	"meshium/internal/shared"
	"net/http"
	"strconv"
	"strings"
)

// Operational read endpoints: replication, traffic, metrics, audit,
// diagnostics, recovery, policy, queue, provisioning, events and export.
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

// handleDiagnostics returns the redacted diagnostic bundle for a migration
// (Phase2D-4) — an operator-exportable snapshot for incident recovery.
func (h *PipelineHandler) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/diagnostics/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	bundle, err := h.repo.BuildDiagnosticBundle(r.Context(), id)
	if err != nil {
		shared.LogCtx(r.Context()).Error("build diagnostic bundle failed", "migration_id", id, "error", err)
		shared.WriteError(w, http.StatusInternalServerError, "failed to build diagnostic bundle", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, bundle)
}

// handleRecovery returns operator-facing recovery guidance for a migration's
// current state (Phase2D-6). The guidance is derived from the honest fail-closed
// state machine; it never implies an automatic recovery that does not exist.
func (h *PipelineHandler) handleRecovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/recovery/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	state, ok := h.currentMigrationState(id)
	if !ok {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, recoveryGuidance(state))
}

// handlePolicy exposes the current support/guardrail policy (Phase 4E) as an
// introspectable snapshot. Operators and tooling can audit exactly what the
// server will permit for automatic cutover — the same single source of truth
// enforced at the API boundary (validateConfigSupport) and at execution time
// (runAutoCutover via CheckAutoCutover).
func (h *PipelineHandler) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}
	shared.WriteJSON(w, http.StatusOK, DefaultPolicy.Matrix())
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
