package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"meshium/internal/mod/process"
	"meshium/internal/shared"
)

// ProcessHandler exposes REST routes for remote process inspection.
type ProcessHandler struct {
	service *process.Service
}

// NewProcessHandler creates a ProcessHandler.
func NewProcessHandler(service *process.Service) *ProcessHandler {
	return &ProcessHandler{service: service}
}

// RegisterRoutes registers all process routes on the mux.
func (h *ProcessHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/processes", h.handleListProcesses)
	mux.HandleFunc("GET /api/servers/{id}/processes/top", h.handleTopProcesses)
	mux.HandleFunc("GET /api/servers/{id}/processes/{pid}", h.handleGetProcess)
	mux.HandleFunc("POST /api/servers/{id}/processes/{pid}/kill", h.handleKillProcess)
}

func (h *ProcessHandler) handleListProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	sortBy := r.URL.Query().Get("sort")
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid limit", "VALIDATION_ERROR")
			return
		}
	}

	processes, err := h.service.ListProcesses(r.Context(), serverID, sortBy)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}
	if limit > 0 && len(processes) > limit {
		processes = processes[:limit]
	}

	shared.WriteJSON(w, http.StatusOK, processes)
}

func (h *ProcessHandler) handleTopProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit < 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid limit", "VALIDATION_ERROR")
			return
		}
	}
	sortBy := r.URL.Query().Get("sortBy")

	processes, err := h.service.GetTopProcesses(r.Context(), serverID, limit, sortBy)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, processes)
}

func (h *ProcessHandler) handleGetProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid process ID", "VALIDATION_ERROR")
		return
	}

	processInfo, err := h.service.GetProcess(r.Context(), serverID, pid)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, processInfo)
}

func (h *ProcessHandler) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid process ID", "VALIDATION_ERROR")
		return
	}

	var req struct {
		Signal string `json:"signal"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
			return
		}
	}

	if err := h.service.KillProcess(r.Context(), serverID, pid, req.Signal); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
