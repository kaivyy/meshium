package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/mod/drift"
	"meshium/internal/shared"
)

// DriftHandler exposes REST routes for configuration drift.
type DriftHandler struct {
	service *drift.Service
}

// NewDriftHandler creates a DriftHandler.
func NewDriftHandler(service *drift.Service) *DriftHandler {
	return &DriftHandler{service: service}
}

// RegisterRoutes registers drift routes on the mux.
func (h *DriftHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/drift", h.handleGetLatest)
	mux.HandleFunc("GET /api/servers/{id}/drift/history", h.handleHistory)
	mux.HandleFunc("POST /api/servers/{id}/drift/check", h.handleCheck)
	mux.HandleFunc("POST /api/servers/{id}/drift/compare", h.handleCompareSnapshots)
	mux.HandleFunc("GET /api/servers/compare/{sourceId}/{targetId}", h.handleCompareServers)
}

// handleGetLatest returns the latest drift report for a server.
func (h *DriftHandler) handleGetLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	report, err := h.service.CheckDrift(r.Context(), serverID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "snapshot") || strings.Contains(strings.ToLower(err.Error()), "not enough") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "SNAPSHOT_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, report)
}

// handleHistory returns drift summaries for consecutive snapshots.
func (h *DriftHandler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, parseErr := strconv.Atoi(limitStr); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}

	history, err := h.service.GetDriftHistory(r.Context(), serverID, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, history)
}

// handleCheck re-runs the latest drift comparison.
func (h *DriftHandler) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	report, err := h.service.CheckDrift(r.Context(), serverID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "snapshot") || strings.Contains(strings.ToLower(err.Error()), "not enough") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "SNAPSHOT_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, report)
}

type compareSnapshotsRequest struct {
	SnapshotA string `json:"snapshotA"`
	SnapshotB string `json:"snapshotB"`
}

// handleCompareSnapshots compares two specific snapshots for a server.
func (h *DriftHandler) handleCompareSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	shared.LimitRequestBody(r)
	var req compareSnapshotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}
	if req.SnapshotA == "" || req.SnapshotB == "" {
		shared.WriteError(w, http.StatusBadRequest, "snapshotA and snapshotB are required", "VALIDATION_ERROR")
		return
	}

	report, err := h.service.GetDriftDetail(r.Context(), serverID, req.SnapshotA, req.SnapshotB)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "snapshot") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "SNAPSHOT_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, report)
}

// handleCompareServers compares the latest snapshots of two servers.
func (h *DriftHandler) handleCompareServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	sourceID, err := strconv.Atoi(r.PathValue("sourceId"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid source id", "VALIDATION_ERROR")
		return
	}
	targetID, err := strconv.Atoi(r.PathValue("targetId"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid target id", "VALIDATION_ERROR")
		return
	}

	report, err := h.service.CompareServers(r.Context(), sourceID, targetID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "snapshot") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "SNAPSHOT_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, report)
}
