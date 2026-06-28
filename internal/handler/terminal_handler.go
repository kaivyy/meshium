package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"meshium/internal/mod/auth"
	"meshium/internal/mod/server"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

const terminalCommandTimeout = 30 * time.Second
const terminalInfoTimeout = 5 * time.Second

// TerminalHandler exposes a WebSocket terminal for executing commands on a remote server.
type TerminalHandler struct {
	handlerFactory *HandlerFactoryImpl
	authSvc        *auth.Service
	serverRepo     server.Repo
	upgrader       websocket.Upgrader
}

type terminalClientMessage struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
}

type terminalConnectedMessage struct {
	Type     string `json:"type"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
}

type terminalOutputMessage struct {
	Type     string `json:"type"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type terminalErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type terminalEvent struct {
	message *terminalClientMessage
	err     error
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

// RegisterRoutes registers terminal routes on the mux.
func (h *TerminalHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/terminal/", h.handleTerminalWS)
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

	token := r.URL.Query().Get("token")
	if token == "" {
		shared.WriteError(w, http.StatusUnauthorized, "missing session token", "UNAUTHORIZED")
		return
	}

	if h.authSvc.IsLocked() {
		shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
		return
	}

	if !h.authSvc.ValidateSessionToken(token) {
		shared.WriteError(w, http.StatusUnauthorized, "invalid session token", "UNAUTHORIZED")
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	sshClient, err := h.handlerFactory.getSSHExecuter(serverID)
	if err != nil {
		_ = conn.WriteJSON(terminalErrorMessage{
			Type:    "error",
			Message: err.Error(),
		})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	events := make(chan terminalEvent, 32)
	go h.readTerminalMessages(ctx, cancel, conn, events)

	hostname, osName := h.loadConnectedInfo(ctx, serverID, sshClient)
	if err := conn.WriteJSON(terminalConnectedMessage{
		Type:     "connected",
		Hostname: hostname,
		OS:       osName,
	}); err != nil {
		log.Printf("terminal websocket write failed: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			if evt.err != nil {
				if websocket.IsCloseError(evt.err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					return
				}

				if err := conn.WriteJSON(terminalErrorMessage{
					Type:    "error",
					Message: evt.err.Error(),
				}); err != nil {
					log.Printf("terminal websocket write failed: %v", err)
					return
				}
				continue
			}

			if evt.message == nil {
				continue
			}

			if err := h.handleTerminalMessage(ctx, conn, sshClient, evt.message); err != nil {
				if writeErr := conn.WriteJSON(terminalErrorMessage{
					Type:    "error",
					Message: err.Error(),
				}); writeErr != nil {
					log.Printf("terminal websocket write failed: %v", writeErr)
					return
				}
			}
		}
	}
}

func (h *TerminalHandler) readTerminalMessages(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, events chan<- terminalEvent) {
	defer close(events)

	for {
		var msg terminalClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				cancel()
				return
			}

			select {
			case events <- terminalEvent{err: err}:
			case <-ctx.Done():
				return
			}
			continue
		}

		select {
		case events <- terminalEvent{message: &msg}:
		case <-ctx.Done():
			return
		}
	}
}

func (h *TerminalHandler) handleTerminalMessage(ctx context.Context, conn *websocket.Conn, sshClient transport.SSHExecuter, msg *terminalClientMessage) error {
	if msg.Type != "command" {
		return errors.New("unsupported message type")
	}

	command := strings.TrimSpace(msg.Command)
	if command == "" {
		return errors.New("command is required")
	}

	cmdCtx, cancel := context.WithTimeout(ctx, terminalCommandTimeout)
	defer cancel()

	stdout, stderr, exitCode, err := sshClient.ExecContext(cmdCtx, command)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return errors.New("command timed out")
		}
		return err
	}

	return conn.WriteJSON(terminalOutputMessage{
		Type:     "output",
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	})
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
