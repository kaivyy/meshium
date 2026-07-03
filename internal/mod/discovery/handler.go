package discovery

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: shared.CheckWebSocketOrigin,
}

// ConnectionRunner runs a connection test and streams step results.
type ConnectionRunner interface {
	RunConnectionTest(ctx context.Context, serverID int, onStep StepCallback) error
}

// Handler exposes WebSocket routes for discovery.
type Handler struct {
	svc ConnectionRunner
}

func NewHandler(svc ConnectionRunner) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes registers the WebSocket endpoint.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/connect/", h.handleConnect)
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ws/connect/")
	if path == r.URL.Path || path == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	serverID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid server ID", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		shared.Log.Error("websocket upgrade failed", "error", err, "path", r.URL.Path)
		return
	}
	defer conn.Close()

	if h == nil || h.svc == nil {
		shared.Log.Error("discovery service not configured")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := h.svc.RunConnectionTest(ctx, serverID, func(msg WSMessage) {
		if err := writeJSONWithDeadline(conn, msg); err != nil {
			shared.Log.Error("websocket write failed", "error", err, "serverID", serverID)
			_ = conn.Close()
			cancel()
		}
	}); err != nil {
		shared.Log.Error("connection test failed", "error", err, "serverID", serverID)
	}
}

func writeJSONWithDeadline(conn *websocket.Conn, msg interface{}) error {
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return conn.WriteJSON(msg)
}
