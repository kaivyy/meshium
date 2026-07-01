package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	aimod "meshium/internal/mod/ai"
	"meshium/internal/shared"
)

// AIHandler exposes rule-based assistant routes.
type AIHandler struct {
	service *aimod.Service
}

// NewAIHandler creates a new AIHandler.
func NewAIHandler(service *aimod.Service) *AIHandler {
	return &AIHandler{service: service}
}

// RegisterRoutes registers assistant routes on the mux.
func (h *AIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/assistant/chat", h.handleChat)
	mux.HandleFunc("GET /api/assistant/suggestions", h.handleSuggestions)
	mux.HandleFunc("POST /api/assistant/analyze-log", h.handleAnalyzeLog)
	mux.HandleFunc("POST /api/assistant/command-help", h.handleCommandHelp)
}

func (h *AIHandler) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req aimod.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	resp, err := h.service.Chat(r.Context(), req)
	if err != nil {
		writeAIError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

func (h *AIHandler) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("serverId")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, "invalid server id", "BAD_REQUEST")
			return
		}
		serverID = parsed
	}

	suggestions, err := h.service.GetSuggestions(r.Context(), serverID)
	if err != nil {
		writeAIError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"suggestions": suggestions,
	})
}

func (h *AIHandler) handleAnalyzeLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req struct {
		ServerID   int    `json:"serverId"`
		LogContent string `json:"logContent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	analysis, err := h.service.AnalyzeLog(r.Context(), req.ServerID, req.LogContent)
	if err != nil {
		writeAIError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, analysis)
}

func (h *AIHandler) handleCommandHelp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "BAD_REQUEST")
		return
	}

	help, err := h.service.GetCommandHelp(r.Context(), req.Command)
	if err != nil {
		writeAIError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, help)
}

func writeAIError(w http.ResponseWriter, err error) {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "required") || strings.Contains(lower, "invalid"):
		shared.WriteError(w, http.StatusBadRequest, msg, "BAD_REQUEST")
	case strings.Contains(lower, "not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(lower, "locked"):
		shared.WriteError(w, http.StatusForbidden, msg, "LOCKED")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
	}
}
