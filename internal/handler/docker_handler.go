package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	dockermod "meshium/internal/mod/docker"
	"meshium/internal/shared"
)

// DockerHandler handles Docker management API requests.
type DockerHandler struct {
	service *dockermod.Service
}

var defaultDockerHandler *DockerHandler

// NewDockerHandler creates a new DockerHandler.
func NewDockerHandler(service *dockermod.Service) *DockerHandler {
	return &DockerHandler{service: service}
}

func setDockerRoutesHandler(handler *DockerHandler) {
	defaultDockerHandler = handler
}

func registerDockerRoutes(mux *http.ServeMux) {
	if defaultDockerHandler != nil {
		defaultDockerHandler.RegisterRoutes(mux)
	}
}

// RegisterRoutes registers Docker routes on the given mux.
func (h *DockerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/docker/containers", h.handleListContainers)
	mux.HandleFunc("GET /api/servers/{id}/docker/images", h.handleListImages)
	mux.HandleFunc("POST /api/servers/{id}/docker/containers/{name}/start", h.handleStartContainer)
	mux.HandleFunc("POST /api/servers/{id}/docker/containers/{name}/stop", h.handleStopContainer)
	mux.HandleFunc("POST /api/servers/{id}/docker/containers/{name}/restart", h.handleRestartContainer)
	mux.HandleFunc("DELETE /api/servers/{id}/docker/containers/{name}", h.handleRemoveContainer)
	mux.HandleFunc("POST /api/servers/{id}/docker/pull", h.handlePullImage)
	mux.HandleFunc("GET /api/servers/{id}/docker/containers/{name}/logs", h.handleGetContainerLogs)
}

func (h *DockerHandler) handleListContainers(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseServerID(w, r)
	if !ok {
		return
	}

	containers, err := h.service.ListContainers(r.Context(), serverID)
	if err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.ContainerListResponse{
		Containers: containers,
		Total:      len(containers),
	})
}

func (h *DockerHandler) handleListImages(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseServerID(w, r)
	if !ok {
		return
	}

	images, err := h.service.ListImages(r.Context(), serverID)
	if err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.ImageListResponse{
		Images: images,
		Total:  len(images),
	})
}

func (h *DockerHandler) handleStartContainer(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServerIDAndName(w, r)
	if !ok {
		return
	}

	if err := h.service.StartContainer(r.Context(), serverID, name); err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.ActionResponse{
		Name:    name,
		Action:  "start",
		Message: fmt.Sprintf("Started container %s", name),
	})
}

func (h *DockerHandler) handleStopContainer(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServerIDAndName(w, r)
	if !ok {
		return
	}

	if err := h.service.StopContainer(r.Context(), serverID, name); err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.ActionResponse{
		Name:    name,
		Action:  "stop",
		Message: fmt.Sprintf("Stopped container %s", name),
	})
}

func (h *DockerHandler) handleRestartContainer(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServerIDAndName(w, r)
	if !ok {
		return
	}

	if err := h.service.RestartContainer(r.Context(), serverID, name); err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.ActionResponse{
		Name:    name,
		Action:  "restart",
		Message: fmt.Sprintf("Restarted container %s", name),
	})
}

func (h *DockerHandler) handleRemoveContainer(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServerIDAndName(w, r)
	if !ok {
		return
	}

	force := r.URL.Query().Get("force") == "true"
	if err := h.service.RemoveContainer(r.Context(), serverID, name, force); err != nil {
		writeDockerError(w, err)
		return
	}

	message := fmt.Sprintf("Removed container %s", name)
	if force {
		message = fmt.Sprintf("Force removed container %s", name)
	}
	shared.WriteJSON(w, http.StatusOK, dockermod.ActionResponse{
		Name:    name,
		Action:  "remove",
		Message: message,
	})
}

func (h *DockerHandler) handlePullImage(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseServerID(w, r)
	if !ok {
		return
	}

	var req struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if strings.TrimSpace(req.Image) == "" {
		shared.WriteError(w, http.StatusBadRequest, "image is required", "BAD_REQUEST")
		return
	}

	output, err := h.service.PullImage(r.Context(), serverID, req.Image)
	if err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.PullResponse{
		Image:  req.Image,
		Output: output,
	})
}

func (h *DockerHandler) handleGetContainerLogs(w http.ResponseWriter, r *http.Request) {
	serverID, name, ok := parseServerIDAndName(w, r)
	if !ok {
		return
	}

	lines := 100
	if raw := r.URL.Query().Get("lines"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid lines value", "BAD_REQUEST")
			return
		}
		lines = parsed
	}

	logs, err := h.service.GetContainerLogs(r.Context(), serverID, name, lines)
	if err != nil {
		writeDockerError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, dockermod.LogsResponse{
		Name:  name,
		Lines: lines,
		Logs:  logs,
	})
}

func parseServerID(w http.ResponseWriter, r *http.Request) (int, bool) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return 0, false
	}
	return serverID, true
}

func parseServerIDAndName(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	serverID, ok := parseServerID(w, r)
	if !ok {
		return 0, "", false
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		shared.WriteError(w, http.StatusBadRequest, "container name is required", "BAD_REQUEST")
		return 0, "", false
	}

	return serverID, name, true
}

func writeDockerError(w http.ResponseWriter, err error) {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "app is locked"):
		shared.WriteError(w, http.StatusForbidden, msg, "LOCKED")
	case strings.Contains(lower, "server not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(lower, "not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(lower, "docker is not installed"):
		shared.WriteError(w, http.StatusBadRequest, msg, "DOCKER_NOT_INSTALLED")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
	}
}
