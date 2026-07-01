package handler

import (
	"net/http"
	"strconv"
	"strings"

	servicesvc "meshium/internal/mod/service"
	"meshium/internal/shared"
)

// ServiceHandler handles HTTP requests for systemd service operations.
type ServiceHandler struct {
	service *servicesvc.Service
}

// NewServiceHandler creates a new service handler.
func NewServiceHandler(service *servicesvc.Service) *ServiceHandler {
	return &ServiceHandler{service: service}
}

// RegisterRoutes registers service routes on the mux.
func (h *ServiceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/services", h.handleList)
	mux.HandleFunc("GET /api/servers/{id}/services/{name}", h.handleStatus)
	mux.HandleFunc("POST /api/servers/{id}/services/{name}/start", h.handleStart)
	mux.HandleFunc("POST /api/servers/{id}/services/{name}/stop", h.handleStop)
	mux.HandleFunc("POST /api/servers/{id}/services/{name}/restart", h.handleRestart)
	mux.HandleFunc("POST /api/servers/{id}/services/{name}/enable", h.handleEnable)
	mux.HandleFunc("POST /api/servers/{id}/services/{name}/disable", h.handleDisable)
}

func (h *ServiceHandler) handleList(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseServiceServerID(w, r)
	if !ok {
		return
	}

	services, err := h.service.ListServices(r.Context(), serverID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, services)
}

func (h *ServiceHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServiceRequest(w, r)
	if !ok {
		return
	}

	status, err := h.service.GetServiceStatus(r.Context(), serverID, name)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, status)
}

func (h *ServiceHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	h.handleAction(w, r, func(serverID int, name string) error {
		return h.service.StartService(r.Context(), serverID, name)
	})
}

func (h *ServiceHandler) handleStop(w http.ResponseWriter, r *http.Request) {
	h.handleAction(w, r, func(serverID int, name string) error {
		return h.service.StopService(r.Context(), serverID, name)
	})
}

func (h *ServiceHandler) handleRestart(w http.ResponseWriter, r *http.Request) {
	h.handleAction(w, r, func(serverID int, name string) error {
		return h.service.RestartService(r.Context(), serverID, name)
	})
}

func (h *ServiceHandler) handleEnable(w http.ResponseWriter, r *http.Request) {
	h.handleAction(w, r, func(serverID int, name string) error {
		return h.service.EnableService(r.Context(), serverID, name)
	})
}

func (h *ServiceHandler) handleDisable(w http.ResponseWriter, r *http.Request) {
	h.handleAction(w, r, func(serverID int, name string) error {
		return h.service.DisableService(r.Context(), serverID, name)
	})
}

func (h *ServiceHandler) handleAction(w http.ResponseWriter, r *http.Request, fn func(serverID int, name string) error) {
	serverID, name, ok := parseServiceRequest(w, r)
	if !ok {
		return
	}

	if err := fn(serverID, name); err != nil {
		writeServiceError(w, err)
		return
	}

	status, err := h.service.GetServiceStatus(r.Context(), serverID, name)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, status)
}

func parseServiceServerID(w http.ResponseWriter, r *http.Request) (int, bool) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return 0, false
	}
	return serverID, true
}

func parseServiceRequest(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	serverID, ok := parseServiceServerID(w, r)
	if !ok {
		return 0, "", false
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		shared.WriteError(w, http.StatusBadRequest, "service name is required", "BAD_REQUEST")
		return 0, "", false
	}

	return serverID, name, true
}

func writeServiceError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"), strings.Contains(lower, "could not be found"), strings.Contains(lower, "no such"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
	}
}
