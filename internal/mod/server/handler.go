package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

type Handler struct {
	svc        *Service
	keyHandler *KeyHandler
}

func NewHandler(svc *Service) *Handler {
	return &Handler{
		svc:        svc,
		keyHandler: NewKeyHandler(svc),
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/servers/", h.handleServerByID)
	mux.HandleFunc("/api/auth-priority", h.keyHandler.handleAuthPriority)
	mux.HandleFunc("/api/connection-profiles", h.keyHandler.handleConnectionProfiles)
	mux.HandleFunc("/api/connection-profiles/", h.keyHandler.handleConnectionProfileByID)
	mux.HandleFunc("/api/known-hosts", h.keyHandler.handleKnownHosts)
	mux.HandleFunc("/api/known-hosts/", h.keyHandler.handleKnownHostByID)
	mux.HandleFunc("/api/host-key-changes", h.keyHandler.handleHostKeyChanges)
	mux.HandleFunc("/api/dashboard", h.keyHandler.handleDashboard)
	mux.HandleFunc("/api/ssh-agent/status", h.keyHandler.handleSSHAgentStatus)
	mux.HandleFunc("/api/export", h.keyHandler.handleExport)
	mux.HandleFunc("/api/import", h.keyHandler.handleImport)
}

func (h *Handler) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

func (h *Handler) handleServerByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, id)
		case http.MethodPut:
			h.handleUpdate(w, r, id)
		case http.MethodDelete:
			h.handleDelete(w, r, id)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "favorite":
		if r.Method != http.MethodPatch {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleToggleFavorite(w, r, id)
	case "info":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetInfo(w, r, id)
	case "auth-status":
		h.keyHandler.handleAuthStatus(w, r, id)
	case "credential-health":
		h.keyHandler.handleCredentialHealth(w, r, id)
	case "connection-history":
		h.keyHandler.handleConnectionHistory(w, r, id)
	case "connection-metrics":
		h.keyHandler.handleConnectionMetrics(w, r, id)
	case "retry-config":
		h.keyHandler.handleRetryConfig(w, r, id)
	case "keys":
		h.keyHandler.handleServerKeys(w, r, id)
	case "install-key":
		h.keyHandler.handleInstallKey(w, r, id)
	case "verify-key":
		h.keyHandler.handleVerifyKey(w, r, id)
	case "rotate-key":
		h.keyHandler.handleRotateKey(w, r, id)
	case "fingerprint":
		h.keyHandler.handleFingerprint(w, r, id)
	case "test-auth":
		h.keyHandler.handleTestAuth(w, r, id)
	case "remove-password":
		h.keyHandler.handleRemovePassword(w, r, id)
	case "clear-key":
		h.keyHandler.handleClearKey(w, r, id)
	case "agent-config":
		h.keyHandler.handleAgentConfig(w, r, id)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	filter := ListFilter{
		Environment: r.URL.Query().Get("environment"),
		Region:      r.URL.Query().Get("region"),
		Tag:         r.URL.Query().Get("tag"),
		Query:       r.URL.Query().Get("q"),
	}

	servers, err := h.svc.List(filter)
	if err != nil {
		shared.Log.Error("failed to list servers", "error", err)
		shared.WriteError(w, http.StatusInternalServerError, "failed to list servers", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, servers)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.Name == "" || req.Host == "" || req.Username == "" {
		shared.WriteError(w, http.StatusBadRequest, "name, host, and username are required", "VALIDATION_ERROR")
		return
	}

	server, err := h.svc.Create(req)
	if err != nil {
		shared.Log.Error("failed to create server", "error", err)
		shared.WriteError(w, http.StatusInternalServerError, "failed to create server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server.Response())
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id int) {
	server, err := h.svc.GetByID(id)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.Log.Error("failed to get server", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id int) {
	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Update(id, req); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.Log.Error("failed to update server", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to update server", "INTERNAL")
		return
	}

	server, err := h.svc.GetByID(id)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.Log.Error("failed to get server", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.svc.Delete(id); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "NOT_FOUND")
			return
		}
		shared.Log.Error("failed to delete server", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to delete server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleToggleFavorite(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.svc.ToggleFavorite(id); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "NOT_FOUND")
			return
		}
		shared.Log.Error("failed to toggle favorite", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to toggle favorite", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleGetInfo(w http.ResponseWriter, r *http.Request, id int) {
	info, err := h.svc.GetServerInfo(id)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server info not found", "SERVER_NOT_FOUND")
			return
		}
		shared.Log.Error("failed to get server info", "error", err, "serverID", id)
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server info", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, info)
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}
