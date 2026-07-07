package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"meshium/internal/mod/auth"
	"meshium/internal/mod/logview"
	"meshium/internal/shared"
)

// LogViewHandler exposes REST and WebSocket routes for remote log viewing.
type LogViewHandler struct {
	service  *logview.Service
	authSvc  *auth.Service
	upgrader websocket.Upgrader
}

type logStreamEvent struct {
	line string
	err  error
}

type logStreamMessage struct {
	Type    string `json:"type"`
	Line    string `json:"line,omitempty"`
	Message string `json:"message,omitempty"`
}

// NewLogViewHandler creates a new log viewer handler.
func NewLogViewHandler(service *logview.Service, authSvc *auth.Service) *LogViewHandler {
	return &LogViewHandler{
		service: service,
		authSvc: authSvc,
		upgrader: websocket.Upgrader{
			CheckOrigin: shared.CheckWebSocketOrigin,
		},
	}
}

// RegisterRoutes registers log viewer routes on the mux.
func (h *LogViewHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/logs", h.handleReadLog)
	mux.HandleFunc("GET /api/servers/{id}/logs/files", h.handleListLogFiles)
	mux.HandleFunc("GET /api/servers/{id}/logs/system", h.handleSystemLogs)
	mux.HandleFunc("GET /api/servers/{id}/logs/service/{name}", h.handleServiceLogs)
	mux.HandleFunc("POST /api/servers/{id}/logs/search", h.handleSearchLogs)
	mux.HandleFunc("GET /ws/logs/{id}", h.handleLogsWS)
}

func (h *LogViewHandler) serverID(r *http.Request) (int, error) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		return 0, errors.New("invalid server id")
	}
	return serverID, nil
}

func (h *LogViewHandler) writeHTTPError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "required"), strings.Contains(lower, "invalid file path"), strings.Contains(lower, "invalid server id"):
		shared.WriteError(w, http.StatusBadRequest, msg, "BAD_REQUEST")
	case strings.Contains(lower, "not found"), strings.Contains(lower, "no such file"), strings.Contains(lower, "directory not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "app is locked"):
		shared.WriteError(w, http.StatusForbidden, msg, "FORBIDDEN")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
	}
}

// handleReadLog handles GET /api/servers/{id}/logs?file=...&lines=100&filter=...
func (h *LogViewHandler) handleReadLog(w http.ResponseWriter, r *http.Request) {
	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		shared.WriteError(w, http.StatusBadRequest, "file is required", "BAD_REQUEST")
		return
	}

	lines := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		lines, err = strconv.Atoi(v)
		if err != nil || lines <= 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid lines value", "BAD_REQUEST")
			return
		}
	}

	filter := r.URL.Query().Get("filter")
	resp, err := h.service.ReadLog(r.Context(), serverID, filePath, lines, filter)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

// handleListLogFiles handles GET /api/servers/{id}/logs/files?dir=/var/log
func (h *LogViewHandler) handleListLogFiles(w http.ResponseWriter, r *http.Request) {
	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "/var/log"
	}

	files, err := h.service.ListLogFiles(r.Context(), serverID, dir)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, files)
}

// handleSystemLogs handles GET /api/servers/{id}/logs/system?lines=100
func (h *LogViewHandler) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	lines := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		lines, err = strconv.Atoi(v)
		if err != nil || lines <= 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid lines value", "BAD_REQUEST")
			return
		}
	}

	resp, err := h.service.GetSystemLogs(r.Context(), serverID, lines)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

// handleServiceLogs handles GET /api/servers/{id}/logs/service/{name}?lines=100
func (h *LogViewHandler) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		shared.WriteError(w, http.StatusBadRequest, "service name is required", "BAD_REQUEST")
		return
	}

	lines := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		lines, err = strconv.Atoi(v)
		if err != nil || lines <= 0 {
			shared.WriteError(w, http.StatusBadRequest, "invalid lines value", "BAD_REQUEST")
			return
		}
	}

	resp, err := h.service.GetServiceLogs(r.Context(), serverID, name, lines)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

// handleSearchLogs handles POST /api/servers/{id}/logs/search
func (h *LogViewHandler) handleSearchLogs(w http.ResponseWriter, r *http.Request) {
	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	var req logview.LogSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	resp, err := h.service.SearchLogs(r.Context(), serverID, req)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

func (h *LogViewHandler) readDisconnect(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			cancel()
			return
		}
	}
}

// handleLogsWS handles GET /ws/logs/{id}?file=...&service=...&system=true&token=...
func (h *LogViewHandler) handleLogsWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.authSvc == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}

	serverID, err := h.serverID(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
		return
	}

	// Auth is handled by the middleware (auth.RequireAuth) which validates
	// the session token from the Sec-WebSocket-Protocol header or query param.

	conn, err := upgradeWebSocket(h.upgrader, w, r)
	if err != nil {
		log.Printf("log websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events := make(chan logStreamEvent, 128)
	go h.readDisconnect(ctx, cancel, conn)
	go func() {
		defer close(events)

		opts := logview.StreamOptions{
			File:        r.URL.Query().Get("file"),
			ServiceName: r.URL.Query().Get("service"),
			System:      strings.EqualFold(r.URL.Query().Get("system"), "true"),
		}

		err := h.service.StreamLogs(ctx, serverID, opts, func(line string) {
			select {
			case events <- logStreamEvent{line: line}:
			case <-ctx.Done():
			}
		})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			select {
			case events <- logStreamEvent{err: err}:
			case <-ctx.Done():
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			if evt.err != nil {
				if err := conn.WriteJSON(logStreamMessage{Type: "error", Message: evt.err.Error()}); err != nil {
					return
				}
				return
			}
			if evt.line == "" {
				continue
			}
			if err := conn.WriteJSON(logStreamMessage{Type: "line", Line: evt.line}); err != nil {
				return
			}
		}
	}
}
