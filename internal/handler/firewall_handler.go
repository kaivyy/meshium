package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/mod/firewall"
	"meshium/internal/shared"
)

// FirewallHandler exposes REST routes for firewall management.
type FirewallHandler struct {
	service *firewall.Service
}

// NewFirewallHandler creates a FirewallHandler.
func NewFirewallHandler(service *firewall.Service) *FirewallHandler {
	return &FirewallHandler{service: service}
}

// RegisterRoutes registers firewall routes on the mux.
func (h *FirewallHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers/{id}/firewall", h.handleFirewall)
	mux.HandleFunc("/api/servers/{id}/firewall/rules", h.handleFirewallRules)
	mux.HandleFunc("/api/servers/{id}/firewall/rules/{ruleId}", h.handleFirewallRuleByID)
	mux.HandleFunc("/api/servers/{id}/firewall/enable", h.handleFirewallEnable)
	mux.HandleFunc("/api/servers/{id}/firewall/disable", h.handleFirewallDisable)
}

func (h *FirewallHandler) handleFirewall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	result, err := h.service.ListRules(r.Context(), serverID)
	if err != nil {
		writeFirewallError(w, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

func (h *FirewallHandler) handleFirewallRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	shared.LimitRequestBody(r)
	var req firewall.FirewallRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := h.service.AddRule(r.Context(), serverID, req); err != nil {
		writeFirewallError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *FirewallHandler) handleFirewallRuleByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	ruleID := r.PathValue("ruleId")
	if ruleID == "" {
		shared.WriteError(w, http.StatusBadRequest, "rule id is required", "VALIDATION_ERROR")
		return
	}

	if err := h.service.DeleteRule(r.Context(), serverID, ruleID); err != nil {
		writeFirewallError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *FirewallHandler) handleFirewallEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	if err := h.service.EnableFirewall(r.Context(), serverID); err != nil {
		writeFirewallError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *FirewallHandler) handleFirewallDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server id", "VALIDATION_ERROR")
		return
	}

	if err := h.service.DisableFirewall(r.Context(), serverID); err != nil {
		writeFirewallError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeFirewallError(w http.ResponseWriter, err error) {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		shared.WriteError(w, http.StatusNotFound, msg, "NOT_FOUND")
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "required"):
		shared.WriteError(w, http.StatusBadRequest, msg, "VALIDATION_ERROR")
	case strings.Contains(lower, "unsupported") || strings.Contains(lower, "not supported"):
		shared.WriteError(w, http.StatusUnprocessableEntity, msg, "UNSUPPORTED")
	default:
		shared.WriteError(w, http.StatusInternalServerError, msg, "INTERNAL")
	}
}
