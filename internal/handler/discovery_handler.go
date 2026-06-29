package handler

import (
	"net/http"
	"strconv"

	"meshium/internal/jobengine"
	"meshium/internal/mod/discovery"
	"meshium/internal/shared"
)

// DiscoveryHandler exposes REST routes for discovery snapshots and compatibility checks.
type DiscoveryHandler struct {
	snapshotStore discovery.SnapshotStore
	engine        *jobengine.Engine
}

// NewDiscoveryHandler creates a DiscoveryHandler.
func NewDiscoveryHandler(
	snapshotStore discovery.SnapshotStore,
	engine *jobengine.Engine,
) *DiscoveryHandler {
	return &DiscoveryHandler{
		snapshotStore: snapshotStore,
		engine:        engine,
	}
}

// RegisterRoutes registers all discovery REST routes on the mux.
// Uses Go 1.22+ ServeMux pattern matching for /api/servers/{id}/snapshot
// and /api/servers/{id}/discover which are more specific than the
// server handler's /api/servers/ prefix pattern.
func (h *DiscoveryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers/{id}/snapshot", h.handleGetSnapshot)
	mux.HandleFunc("/api/servers/{id}/discover", h.handleTriggerDiscovery)
	mux.HandleFunc("/api/compat", h.handleCompat)
}

// handleGetSnapshot returns the latest snapshot for a server.
// GET /api/servers/{id}/snapshot
func (h *DiscoveryHandler) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	snapshot, err := h.snapshotStore.LoadSnapshot(serverID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "No scan data yet. Run a discovery scan first.", "SNAPSHOT_NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, snapshot)
}

// handleTriggerDiscovery submits a discovery job for a server.
// POST /api/servers/{id}/discover
func (h *DiscoveryHandler) handleTriggerDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	jobReq := jobengine.JobRequest{
		Type:     jobengine.JobTypeDiscovery,
		SourceID: serverID,
	}

	job, err := h.engine.Submit(r.Context(), jobReq)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to submit discovery job", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]string{
		"jobID": job.ID,
	})
}

// handleCompat checks compatibility between two servers.
// GET /api/compat?source={serverID}&target={serverID}
func (h *DiscoveryHandler) handleCompat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	q := r.URL.Query()
	sourceIDStr := q.Get("source")
	targetIDStr := q.Get("target")

	if sourceIDStr == "" || targetIDStr == "" {
		shared.WriteError(w, http.StatusBadRequest, "source and target query parameters are required", "VALIDATION_ERROR")
		return
	}

	sourceID, err := strconv.Atoi(sourceIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid source ID", "VALIDATION_ERROR")
		return
	}

	targetID, err := strconv.Atoi(targetIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid target ID", "VALIDATION_ERROR")
		return
	}

	// Load snapshots
	sourceSnapshot, err := h.snapshotStore.LoadSnapshot(sourceID)
	if err != nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "Source server has not been scanned yet. Run a discovery scan first.", "SNAPSHOT_NOT_FOUND")
		return
	}

	targetSnapshot, err := h.snapshotStore.LoadSnapshot(targetID)
	if err != nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "Target server has not been scanned yet. Run a discovery scan first.", "SNAPSHOT_NOT_FOUND")
		return
	}

	// Run compatibility check
	report := discovery.CheckCompatibility(sourceSnapshot, targetSnapshot)

	// Build response with source/target IDs
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"sourceID":   sourceID,
		"targetID":   targetID,
		"compatible": report.Compatible,
		"blockers":   report.Blockers,
		"warnings":   report.Warnings,
	})
}
