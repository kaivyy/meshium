package auth

import (
	"encoding/json"
	"net/http"

	"meshium/internal/shared"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/setup", h.handleSetup)
	mux.HandleFunc("/api/auth/unlock", h.handleUnlock)
	mux.HandleFunc("/api/auth/lock", h.handleLock)
	mux.HandleFunc("/api/auth/status", h.handleStatus)
}

func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	setup, err := h.svc.IsSetup()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	if setup {
		shared.WriteError(w, http.StatusBadRequest, "master password already set", "ALREADY_SETUP")
		return
	}

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}
	if len(req.Password) < 8 {
		shared.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Setup(req.Password); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "setup failed", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req UnlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Unlock(req.Password); err != nil {
		shared.WriteError(w, http.StatusUnauthorized, "invalid password", "AUTH_FAILED")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	h.svc.Lock()
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	setup, err := h.svc.IsSetup()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, AuthStatus{
		Setup:  setup,
		Locked: h.svc.IsLocked(),
	})
}
