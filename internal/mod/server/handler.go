package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/servers/", h.handleServerByID)
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
		shared.WriteError(w, http.StatusInternalServerError, "failed to update server", "INTERNAL")
		return
	}

	server, err := h.svc.GetByID(id)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
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
