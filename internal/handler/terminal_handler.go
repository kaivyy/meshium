package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"meshium/internal/mod/auth"
	modssh "meshium/internal/mod/ssh"
	servicesvc "meshium/internal/mod/service"
	"meshium/internal/mod/server"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

const terminalInfoTimeout = 5 * time.Second

// TerminalHandler exposes a WebSocket terminal for interactive SSH sessions.
type TerminalHandler struct {
	handlerFactory *HandlerFactoryImpl
	authSvc        *auth.Service
	serverRepo     server.Repo
	upgrader       websocket.Upgrader
}

// Client → Server messages
type terminalClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// Server → Client messages
type terminalConnectedMessage struct {
	Type     string `json:"type"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
}

type terminalOutputMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type terminalErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type terminalClosedMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// NewTerminalHandler creates a TerminalHandler.
func NewTerminalHandler(handlerFactory *HandlerFactoryImpl, authSvc *auth.Service, serverRepo server.Repo) *TerminalHandler {
	return &TerminalHandler{
		handlerFactory: handlerFactory,
		authSvc:        authSvc,
		serverRepo:     serverRepo,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// RegisterRoutes registers terminal and service routes on the mux.
func (h *TerminalHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/terminal/", h.handleTerminalWS)

	if h == nil || h.handlerFactory == nil {
		return
	}

	serviceHandler := NewServiceHandler(servicesvc.NewService(
		h.handlerFactory.serverRepo,
		h.handlerFactory.pool,
		h.handlerFactory.authSvc,
		h.handlerFactory.knownHosts,
	))
	serviceHandler.RegisterRoutes(mux)
}

func (h *TerminalHandler) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.handlerFactory == nil || h.authSvc == nil || h.serverRepo == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/terminal/")
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
	// No need to re-validate the token here.

	// Parse initial terminal size from query params
	cols := 80
	rows := 24
	if c := r.URL.Query().Get("cols"); c != "" {
		if parsed, err := strconv.Atoi(c); err == nil && parsed > 0 {
			cols = parsed
		}
	}
	if r := r.URL.Query().Get("rows"); r != "" {
		if parsed, err := strconv.Atoi(r); err == nil && parsed > 0 {
			rows = parsed
		}
	}

	conn, err := upgradeWebSocket(h.upgrader, w, r)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Get SSH client from pool
	sshClient, err := h.handlerFactory.getSSHExecuter(serverID)
	if err != nil {
		_ = conn.WriteJSON(terminalErrorMessage{
			Type:    "error",
			Message: err.Error(),
		})
		return
	}

	// Type-assert to *modssh.Client to access PTY methods
	sshConn, ok := sshClient.(*modssh.Client)
	if !ok {
		_ = conn.WriteJSON(terminalErrorMessage{
			Type:    "error",
			Message: "SSH client does not support interactive terminal sessions",
		})
		return
	}

	// Open a PTY shell session
	shell, err := sshConn.NewShellSession(cols, rows)
	if err != nil {
		_ = conn.WriteJSON(terminalErrorMessage{
			Type:    "error",
			Message: "Failed to open terminal: " + err.Error(),
		})
		return
	}
	defer shell.Close()

	// Send connected message with hostname info
	hostname, osName := h.loadConnectedInfo(r.Context(), serverID, sshClient)
	if err := conn.WriteJSON(terminalConnectedMessage{
		Type:     "connected",
		Hostname: hostname,
		OS:       osName,
	}); err != nil {
		log.Printf("terminal websocket write failed: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup
	var writeMu sync.Mutex // protects conn.WriteJSON

	// safeWriteJSON writes a JSON message to the WebSocket in a thread-safe manner.
	safeWriteJSON := func(msg interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	}

	// Goroutine 1: Read stdout from SSH shell → send to WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := shell.Stdout().Read(buf)
			if n > 0 {
				if writeErr := safeWriteJSON(terminalOutputMessage{
					Type: "output",
					Data: string(buf[:n]),
				}); writeErr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal stdout read error: %v", err)
				}
				_ = safeWriteJSON(terminalClosedMessage{Type: "closed", Data: "Terminal session ended."})
				cancel()
				return
			}
		}
	}()

	// Goroutine 2: Read stderr from SSH shell → send to WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 8192)
		for {
			n, err := shell.Stderr().Read(buf)
			if n > 0 {
				if writeErr := safeWriteJSON(terminalErrorMessage{
					Type:    "error",
					Message: string(buf[:n]),
				}); writeErr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal stderr read error: %v", err)
				}
				return
			}
		}
	}()

	// Goroutine 3: Read messages from WebSocket → write to SSH shell stdin
	// This runs in the main goroutine context (blocking read loop)
	go func() {
		for {
			var msg terminalClientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) {
					cancel()
					return
				}
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					cancel()
					return
				}
				log.Printf("terminal websocket read error: %v", err)
				cancel()
				return
			}

			switch msg.Type {
			case "input":
				if msg.Data != "" {
					if _, err := shell.Write([]byte(msg.Data)); err != nil {
						log.Printf("terminal write to shell failed: %v", err)
						cancel()
						return
					}
				}
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					if err := shell.Resize(msg.Cols, msg.Rows); err != nil {
						log.Printf("terminal resize failed: %v", err)
					}
				}
			}
		}
	}()

	// Wait for context to be cancelled (client disconnect or shell exit)
	<-ctx.Done()
	shell.Close()
	wg.Wait()
}

func (h *TerminalHandler) loadConnectedInfo(ctx context.Context, serverID int, sshClient transport.SSHExecuter) (string, string) {
	hostname := ""
	osName := ""

	if info, err := h.serverRepo.GetServerInfo(serverID); err == nil && info != nil {
		hostname = strings.TrimSpace(info.Hostname)
		osName = strings.TrimSpace(info.OS)
	}

	if hostname != "" && osName != "" {
		return hostname, osName
	}

	if hostname == "" {
		infoCtx, cancel := context.WithTimeout(ctx, terminalInfoTimeout)
		if out, _, _, err := sshClient.ExecContext(infoCtx, "hostname"); err == nil {
			hostname = strings.TrimSpace(out)
		}
		cancel()
	}

	if osName == "" {
		infoCtx, cancel := context.WithTimeout(ctx, terminalInfoTimeout)
		if out, _, _, err := sshClient.ExecContext(infoCtx, "uname -s"); err == nil {
			osName = strings.TrimSpace(out)
		}
		cancel()
	}

	return hostname, osName
}

// Ensure json import is used (for potential future use)
var _ = json.Marshal
