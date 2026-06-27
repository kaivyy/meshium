package discovery

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
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
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	if h == nil || h.svc == nil {
		log.Printf("discovery service not configured")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := h.svc.RunConnectionTest(ctx, serverID, func(msg WSMessage) {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("websocket write failed: %v", err)
			_ = conn.Close()
			cancel()
		}
	}); err != nil {
		log.Printf("connection test failed for server %d: %v", serverID, err)
	}
}
