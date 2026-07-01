package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	cronmod "meshium/internal/mod/cron"
	"meshium/internal/shared"
)

// CronHandler exposes REST routes for cron job management.
type CronHandler struct {
	service *cronmod.Service
}

// NewCronHandler creates a CronHandler.
func NewCronHandler(service *cronmod.Service) *CronHandler {
	return &CronHandler{service: service}
}

// RegisterRoutes registers cron routes on the mux.
func (h *CronHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/cron", h.handleList)
	mux.HandleFunc("POST /api/servers/{id}/cron", h.handleCreate)
	mux.HandleFunc("PUT /api/servers/{id}/cron/{jobId}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/servers/{id}/cron/{jobId}", h.handleDelete)
}

func (h *CronHandler) handleList(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	jobs, err := h.service.ListCronJobs(r.Context(), serverID)
	if err != nil {
		writeCronError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, jobs)
}

func (h *CronHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	shared.LimitRequestBody(r)
	var req cronmod.CronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if err := h.service.AddCronJob(r.Context(), serverID, req); err != nil {
		writeCronError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *CronHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	jobID := r.PathValue("jobId")
	if strings.TrimSpace(jobID) == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid cron job id", "BAD_REQUEST")
		return
	}

	shared.LimitRequestBody(r)
	var req cronmod.CronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	if err := h.service.UpdateCronJob(r.Context(), serverID, jobID, req); err != nil {
		writeCronError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *CronHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return
	}

	jobID := r.PathValue("jobId")
	if strings.TrimSpace(jobID) == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid cron job id", "BAD_REQUEST")
		return
	}

	if err := h.service.DeleteCronJob(r.Context(), serverID, jobID); err != nil {
		writeCronError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeCronError(w http.ResponseWriter, err error) {
	if err == nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL_ERROR")
		return
	}

	msg := err.Error()
	switch {
	case strings.Contains(strings.ToLower(msg), "not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(strings.ToLower(msg), "invalid") || strings.Contains(strings.ToLower(msg), "required") || strings.Contains(strings.ToLower(msg), "must"):
		shared.WriteError(w, http.StatusBadRequest, msg, "BAD_REQUEST")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
	}
}
