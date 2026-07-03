package handler

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"meshium/internal/mod/auth"
	monitoringmod "meshium/internal/mod/monitoring"
	"meshium/internal/shared"
)

// MonitoringHandler exposes monitoring REST and WebSocket routes.
type MonitoringHandler struct {
	service  *monitoringmod.Service
	authSvc  *auth.Service
	upgrader websocket.Upgrader
}

// NewMonitoringHandler creates a new monitoring handler.
func NewMonitoringHandler(service *monitoringmod.Service, authSvc *auth.Service) *MonitoringHandler {
	return &MonitoringHandler{
		service: service,
		authSvc: authSvc,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// RegisterRoutes registers monitoring routes on the mux.
func (h *MonitoringHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/metrics", h.handleGetMetrics)
	mux.HandleFunc("GET /api/servers/{id}/metrics/top", h.handleGetTopProcesses)
	mux.HandleFunc("GET /api/servers/{id}/metrics/disk", h.handleGetDiskUsage)
	mux.HandleFunc("GET /api/servers/{id}/metrics/network", h.handleGetNetworkInterfaces)
	mux.HandleFunc("GET /api/servers/{id}/metrics/history", h.handleGetMetricsHistory)
	mux.HandleFunc("/ws/monitoring/", h.handleMonitoringWS)
}

func (h *MonitoringHandler) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseMonitoringServerID(w, r)
	if !ok {
		return
	}

	metrics, err := h.service.GetMetrics(r.Context(), serverID)
	if err != nil {
		writeMonitoringError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, metrics)
}

func (h *MonitoringHandler) handleGetTopProcesses(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseMonitoringServerID(w, r)
	if !ok {
		return
	}

	limit := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}

	processes, err := h.service.GetTopProcesses(r.Context(), serverID, limit)
	if err != nil {
		writeMonitoringError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, processes)
}

func (h *MonitoringHandler) handleGetDiskUsage(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseMonitoringServerID(w, r)
	if !ok {
		return
	}

	disk, err := h.service.GetDiskUsage(r.Context(), serverID)
	if err != nil {
		writeMonitoringError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, disk)
}

func (h *MonitoringHandler) handleGetNetworkInterfaces(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseMonitoringServerID(w, r)
	if !ok {
		return
	}

	interfaces, err := h.service.GetNetworkInterfaces(r.Context(), serverID)
	if err != nil {
		writeMonitoringError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, interfaces)
}

func (h *MonitoringHandler) handleGetMetricsHistory(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseMonitoringServerID(w, r)
	if !ok {
		return
	}

	points := 24
	if raw := strings.TrimSpace(r.URL.Query().Get("points")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			points = parsed
		}
	}

	history, err := h.service.GetMetricsHistory(r.Context(), serverID, points)
	if err != nil {
		writeMonitoringError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, history)
}

func (h *MonitoringHandler) handleMonitoringWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.authSvc == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/monitoring/")
	if path == r.URL.Path || path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	serverID, err := strconv.Atoi(parts[0])
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	// Auth is handled by the middleware (auth.RequireAuth) which validates
	// the session token from the Sec-WebSocket-Protocol header or query param.

	conn, err := upgradeWebSocket(h.upgrader, w, r)
	if err != nil {
		log.Printf("monitoring websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	intervalSeconds := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("interval")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			intervalSeconds = parsed
		}
	}
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}
	if intervalSeconds > 60 {
		intervalSeconds = 60
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	sendMetrics := func() error {
		metrics, err := h.service.GetMetrics(ctx, serverID)
		if err != nil {
			return conn.WriteJSON(monitoringErrorMessage{Type: "error", Message: err.Error()})
		}
		return conn.WriteJSON(metrics)
	}

	if err := sendMetrics(); err != nil {
		if !isBenignWebSocketCloseError(err) {
			log.Printf("monitoring websocket write failed: %v", err)
		}
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendMetrics(); err != nil {
				if !isBenignWebSocketCloseError(err) {
					log.Printf("monitoring websocket write failed: %v", err)
				}
				return
			}
		}
	}
}

type monitoringErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func parseMonitoringServerID(w http.ResponseWriter, r *http.Request) (int, bool) {
	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
		return 0, false
	}
	return serverID, true
}

func writeMonitoringError(w http.ResponseWriter, err error) {
	msg := err.Error()
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	if strings.Contains(strings.ToLower(msg), "not found") {
		status = http.StatusNotFound
		code = "NOT_FOUND"
	}
	shared.WriteError(w, status, msg, code)
}

func isBenignWebSocketCloseError(err error) bool {
	if err == nil {
		return true
	}
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived)
}
