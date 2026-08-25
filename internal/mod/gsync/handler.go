package gsync

import (
	"encoding/json"
	"errors"
	"net/http"

	"meshium/internal/shared"
)

// Handler exposes the gsync REST API.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes registers gsync routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gsync/status", h.handleStatus)
	mux.HandleFunc("POST /api/gsync/config", h.handleSaveToken)
	mux.HandleFunc("DELETE /api/gsync/config", h.handleDeleteToken)
	mux.HandleFunc("POST /api/gsync/pairs", h.handleUpsertPair)
	mux.HandleFunc("DELETE /api/gsync/pairs/{id}", h.handleDeletePair)
	mux.HandleFunc("POST /api/gsync/run/{id}", h.handleRun)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Status(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "GSYNC_STATUS_FAILED")
		return
	}
	shared.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) handleSaveToken(w http.ResponseWriter, r *http.Request) {
	shared.LimitRequestBody(r)
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	if err := h.svc.SaveToken(r.Context(), req.Token); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_TOKEN")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteToken(r.Context()); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "GSYNC_DELETE_FAILED")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) handleUpsertPair(w http.ResponseWriter, r *http.Request) {
	shared.LimitRequestBody(r)
	var p Pair
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}
	saved, err := h.svc.UpsertPair(r.Context(), p)
	if err != nil {
		if errors.Is(err, ErrBadPair) {
			shared.WriteError(w, http.StatusBadRequest, err.Error(), "BAD_PAIR")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "GSYNC_SAVE_FAILED")
		return
	}
	shared.WriteJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleDeletePair(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeletePair(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, ErrBadPair) {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err.Error(), "GSYNC_DELETE_FAILED")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRun is synchronous: a manual sync is a user watching a button; the
// scheduler path goes through the job engine instead.
func (h *Handler) handleRun(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.RunNow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, ErrBadPair) {
			shared.WriteError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "result": res})
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}
