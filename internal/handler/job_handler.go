package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"meshium/internal/jobengine"
	"meshium/internal/shared"
)

// JobHandler exposes REST and WebSocket routes for the Job Engine.
type JobHandler struct {
	engine   *jobengine.Engine
	store    jobengine.JobStore
	upgrader websocket.Upgrader
}

// NewJobHandler creates a JobHandler.
func NewJobHandler(engine *jobengine.Engine, store jobengine.JobStore) *JobHandler {
	return &JobHandler{
		engine: engine,
		store:  store,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// RegisterRoutes registers all job routes on the mux.
func (h *JobHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/jobs", h.handleJobs)
	mux.HandleFunc("/api/jobs/", h.handleJobByID)
	mux.HandleFunc("/ws/jobs/", h.handleJobProgressWS)
}

// --- REST: /api/jobs ---

func (h *JobHandler) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleSubmit(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

// --- REST: /api/jobs/{id} ---

func (h *JobHandler) handleJobByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	jobID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, jobID)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "cancel":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleCancel(w, r, jobID)
	case "pause":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePause(w, r, jobID)
	case "resume":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleResume(w, r, jobID)
	case "logs":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetLogs(w, r, jobID)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

// --- REST handlers ---

func (h *JobHandler) handleList(w http.ResponseWriter, r *http.Request) {
	filter := jobengine.JobFilter{}

	q := r.URL.Query()
	if t := q.Get("type"); t != "" {
		filter.Type = jobengine.JobType(t)
	}
	if s := q.Get("status"); s != "" {
		filter.Status = jobengine.JobStatus(s)
	}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	jobs, err := h.engine.ListJobs(r.Context(), filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list jobs", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, jobs)
}

func (h *JobHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	shared.LimitRequestBody(r)
	var req jobengine.JobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.Type == "" {
		shared.WriteError(w, http.StatusBadRequest, "job type is required", "VALIDATION_ERROR")
		return
	}

	// Validate type
	switch req.Type {
	case jobengine.JobTypeMigration, jobengine.JobTypeDiscovery, jobengine.JobTypeCompatCheck:
		// valid
	default:
		shared.WriteError(w, http.StatusBadRequest, "invalid job type", "VALIDATION_ERROR")
		return
	}

	// Type-specific validation
	switch req.Type {
	case jobengine.JobTypeMigration:
		if req.PlanID == "" {
			shared.WriteError(w, http.StatusBadRequest, "planId is required for migration jobs", "VALIDATION_ERROR")
			return
		}
	case jobengine.JobTypeDiscovery:
		if req.SourceID == 0 {
			shared.WriteError(w, http.StatusBadRequest, "sourceId is required for discovery jobs", "VALIDATION_ERROR")
			return
		}
	case jobengine.JobTypeCompatCheck:
		if req.SourceID == 0 || req.TargetID == 0 {
			shared.WriteError(w, http.StatusBadRequest, "sourceId and targetId are required for compat_check jobs", "VALIDATION_ERROR")
			return
		}
	}

	job, err := h.engine.Submit(r.Context(), req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to submit job", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, job)
}

func (h *JobHandler) handleGet(w http.ResponseWriter, r *http.Request, jobID string) {
	job, err := h.engine.GetJob(r.Context(), jobID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "job not found", "JOB_NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

func (h *JobHandler) handleCancel(w http.ResponseWriter, r *http.Request, jobID string) {
	if err := h.engine.Cancel(r.Context(), jobID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to cancel job", "INTERNAL")
		return
	}

	job, err := h.engine.GetJob(r.Context(), jobID)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

func (h *JobHandler) handlePause(w http.ResponseWriter, r *http.Request, jobID string) {
	if err := h.engine.Pause(r.Context(), jobID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to pause job", "INTERNAL")
		return
	}

	job, err := h.engine.GetJob(r.Context(), jobID)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "paused"})
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

func (h *JobHandler) handleResume(w http.ResponseWriter, r *http.Request, jobID string) {
	if err := h.engine.Resume(r.Context(), jobID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to resume job", "INTERNAL")
		return
	}

	job, err := h.engine.GetJob(r.Context(), jobID)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

func (h *JobHandler) handleGetLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	logs, err := h.engine.GetLogs(r.Context(), jobID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get logs", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, logs)
}

// --- WebSocket: /ws/jobs/{id}/progress ---

func (h *JobHandler) handleJobProgressWS(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ws/jobs/")
	if path == r.URL.Path || path == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.Split(path, "/")
	jobID := parts[0]

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Load job to check if it exists
	job, err := h.engine.GetJob(r.Context(), jobID)
	if err != nil {
		conn.WriteJSON(map[string]string{
			"type":  "error",
			"error": "job not found",
		})
		return
	}

	// If job is already terminal, send final state and close
	if job.Status.IsTerminal() {
		conn.WriteJSON(map[string]interface{}{
			"type":     "progress",
			"progress": job.Progress,
		})
		return
	}

	// Subscribe to progress updates
	ch := h.engine.SubscribeProgress(jobID)
	defer h.engine.UnsubscribeProgress(jobID, ch)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Send initial progress
	conn.WriteJSON(map[string]interface{}{
		"type":     "progress",
		"progress": job.Progress,
	})

	// Read pump: detect client disconnect
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	// Ticker for keepalive ping
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case progress, ok := <-ch:
			if !ok {
				// Channel closed — job finished
				conn.WriteJSON(map[string]interface{}{
					"type":     "progress",
					"progress": progress,
				})
				return
			}
			if err := conn.WriteJSON(map[string]interface{}{
				"type":     "progress",
				"progress": progress,
			}); err != nil {
				log.Printf("websocket write failed: %v", err)
				return
			}

			// Check if job is now terminal
			if progress.Percentage >= 100 {
				return
			}

		case <-ticker.C:
			if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}
