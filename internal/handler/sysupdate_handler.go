package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/mod/sysupdate"
	"meshium/internal/shared"
)

// SysUpdateHandler handles system package update and package management requests.
type SysUpdateHandler struct {
	service *sysupdate.Service
}

// NewSysUpdateHandler creates a new system update handler.
func NewSysUpdateHandler(service *sysupdate.Service) *SysUpdateHandler {
	return &SysUpdateHandler{service: service}
}

// RegisterRoutes registers system update routes on the given mux.
func (h *SysUpdateHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/updates", h.handleCheckUpdates)
	mux.HandleFunc("POST /api/servers/{id}/updates/install", h.handleInstallUpdates)
	mux.HandleFunc("GET /api/servers/{id}/updates/packages", h.handleListPackages)
	mux.HandleFunc("GET /api/servers/{id}/updates/packages/{package}", h.handleGetPackage)
	mux.HandleFunc("POST /api/servers/{id}/updates/packages/{package}", h.handleInstallPackage)
	mux.HandleFunc("DELETE /api/servers/{id}/updates/packages/{package}", h.handleRemovePackage)
}

func (h *SysUpdateHandler) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	status, err := h.service.CheckUpdates(r.Context(), serverID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, status)
}

func (h *SysUpdateHandler) handleInstallUpdates(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	shared.LimitRequestBody(r)
	var req sysupdate.InstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	result, err := h.service.InstallUpdates(r.Context(), serverID, req.SecurityOnly)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *SysUpdateHandler) handleListPackages(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = r.URL.Query().Get("q")
	}

	packages, err := h.service.ListInstalledPackages(r.Context(), serverID, filter)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, packages)
}

func (h *SysUpdateHandler) handleGetPackage(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	packageName := strings.TrimSpace(r.PathValue("package"))
	pkg, err := h.service.GetPackageInfo(r.Context(), serverID, packageName)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, pkg)
}

func (h *SysUpdateHandler) handleInstallPackage(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	packageName := strings.TrimSpace(r.PathValue("package"))
	result, err := h.service.InstallPackage(r.Context(), serverID, packageName)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *SysUpdateHandler) handleRemovePackage(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	packageName := strings.TrimSpace(r.PathValue("package"))
	result, err := h.service.RemovePackage(r.Context(), serverID, packageName)
	if err != nil {
		h.writeError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *SysUpdateHandler) writeError(w http.ResponseWriter, err error) {
	msg := err.Error()
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"

	switch {
	case strings.Contains(strings.ToLower(msg), "invalid package name"):
		status = http.StatusBadRequest
		code = "VALIDATION_ERROR"
	case strings.Contains(strings.ToLower(msg), "unsupported package manager"):
		status = http.StatusNotImplemented
		code = "UNSUPPORTED"
	case strings.Contains(strings.ToLower(msg), "not found"):
		status = http.StatusNotFound
		code = "NOT_FOUND"
	case strings.Contains(strings.ToLower(msg), "locked"):
		status = http.StatusForbidden
		code = "LOCKED"
	}

	shared.WriteError(w, status, msg, code)
}
