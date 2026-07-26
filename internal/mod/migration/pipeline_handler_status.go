package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"meshium/internal/mod/auth"
	"meshium/internal/shared"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Status and preflight endpoints: session, stages, risk, compatibility and
// health — including the preflight runs that populate them.
// --- REST: Get Pipeline Session ---

func (h *PipelineHandler) handleGetPipelineSession(w http.ResponseWriter, r *http.Request, id int) {
	session, err := h.buildSession(r.Context(), id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
		return
	}
	shared.WriteJSON(w, http.StatusOK, session)
}

// --- REST: Get Stages ---

func (h *PipelineHandler) handleGetStages(w http.ResponseWriter, r *http.Request, id int) {
	stages, err := h.repo.GetStages(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get stages", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, stages)
}

// --- REST: Cancel ---

func (h *PipelineHandler) handleCancel(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Cancel(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("cancel failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// --- REST: Pause ---
// Pause is implemented by cancelling the context of the running pipeline.
// The migration will be marked as interrupted and can be resumed later.

func (h *PipelineHandler) handlePause(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Pause(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusConflict, err.Error(), "INVALID_STATE")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// --- REST: Resume ---

func (h *PipelineHandler) handleResume(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Resume(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("resume failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

// --- REST: Rollback ---

func (h *PipelineHandler) handlePipelineRollback(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.pipeline.Rollback(r.Context(), id, nil); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("rollback failed: %v", err), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back"})
}

// --- REST: Risk ---

func (h *PipelineHandler) handleRisk(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/risk/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleRiskByID(w, r, id)
}

func (h *PipelineHandler) handleRiskByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method == http.MethodGet {
		report, err := h.repo.GetRiskReport(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get risk report", "INTERNAL")
			return
		}
		if report == nil {
			shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"riskScore": 0, "riskClass": "not_assessed"})
			return
		}
		shared.WriteJSON(w, http.StatusOK, report)
		return
	}

	if r.Method == http.MethodPost {
		// Run risk assessment
		var input RiskInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
			return
		}

		engine := NewRiskEngine(h.repo)
		report, err := engine.AssessRisk(r.Context(), id, input)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("risk assessment failed: %v", err), "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, report)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

// --- REST: Compatibility ---

func (h *PipelineHandler) handleCompatibility(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/compatibility/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleCompatibilityByID(w, r, id)
}

func (h *PipelineHandler) handleCompatibilityByID(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		results, err := h.getStoredCompatibilityResults(id)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get compatibility results", "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, results)
	case http.MethodPost:
		if r.URL.Query().Get("refresh") != "true" {
			stored, err := h.getStoredCompatibilityResults(id)
			if err != nil {
				shared.WriteError(w, http.StatusInternalServerError, "failed to get compatibility results", "INTERNAL")
				return
			}
			if len(stored) > 0 {
				shared.WriteJSON(w, http.StatusOK, stored)
				return
			}
		}

		results := h.runCompatibilityPreflight(r.Context(), id, nil)
		for _, result := range results {
			_, _ = h.repo.CreateVerificationResult(r.Context(), VerificationResult{
				MigrationID:      id,
				VerificationType: "compatibility",
				Target:           result.CheckName,
				Expected:         string(result.Severity),
				Actual:           result.Message,
				Passed:           result.Passed,
				ErrorMessage:     compatibilityErrorMessage(result),
			})
		}
		shared.WriteJSON(w, http.StatusOK, results)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

func compatibilityErrorMessage(result CompatibilityCheckResult) string {
	if result.Passed {
		return ""
	}
	return result.Message
}

// handleCompatibilityWS streams per-check progress over a WebSocket while the
// compatibility preflight runs, then signals completion. Mirrors handleDryRunWS.
// The result itself is persisted to verification_result and surfaced via
// loadSession (getStoredCompatibilityResults), so the WS carries only progress.
func (h *PipelineHandler) handleCompatibilityWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.pipeline == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/compatibility/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		http.Error(w, "invalid migration ID", http.StatusBadRequest)
		return
	}

	responseHeader := http.Header{}
	if proto := auth.WebSocketSubprotocolToken(r); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	conn, err := h.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		shared.LogCtx(r.Context()).Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Decouple the preflight's lifetime from the WebSocket's: a page refresh
	// mid-check closes the WS and cancels r.Context(), but the work should still
	// complete and persist so the next loadSession shows the results instead of
	// dropping the user back to Discovery. workCtx is bounded by a timeout only;
	// the WS writeMsg no-ops once the connection is gone.
	workCtx, workCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer workCancel()

	writeMsg := func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			shared.LogCtx(r.Context()).Error("websocket write failed", "error", writeErr)
		}
	}

	results := h.runCompatibilityPreflight(workCtx, id, writeMsg)
	for _, result := range results {
		_, _ = h.repo.CreateVerificationResult(workCtx, VerificationResult{
			MigrationID:      id,
			VerificationType: "compatibility",
			Target:           result.CheckName,
			Expected:         string(result.Severity),
			Actual:           result.Message,
			Passed:           result.Passed,
			ErrorMessage:     compatibilityErrorMessage(result),
		})
	}

	writeMsg(WSMessage{Step: "compat", Status: "complete", Value: fmt.Sprintf("Completed %d checks", len(results))})
}

func (h *PipelineHandler) getStoredCompatibilityResults(id int) ([]CompatibilityCheckResult, error) {
	verifications, err := h.repo.GetVerificationResults(id)
	if err != nil {
		return nil, err
	}
	results := make([]CompatibilityCheckResult, 0)
	for _, result := range verifications {
		if result.VerificationType != "compatibility" {
			continue
		}
		severity := Severity(result.Expected)
		if severity != SeverityInfo && severity != SeverityWarning && severity != SeverityHigh && severity != SeverityCritical {
			severity = SeverityInfo
		}
		message := result.Actual
		if message == "" {
			message = result.ErrorMessage
		}
		results = append(results, CompatibilityCheckResult{
			CheckName: result.Target,
			Severity:  severity,
			Passed:    result.Passed,
			Message:   message,
		})
	}
	return results, nil
}

func (h *PipelineHandler) runCompatibilityPreflight(ctx context.Context, id int, onProgress StepCallback) []CompatibilityCheckResult {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}
	onProgress(WSMessage{Step: "compat", Status: "progress", Value: "Loading migration..."})
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		return []CompatibilityCheckResult{{
			CheckName: "migration",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   "migration not found",
		}}
	}

	results := []CompatibilityCheckResult{{
		CheckName: "source_target",
		Severity:  SeverityCritical,
		Passed:    migration.SourceID != migration.TargetID,
		Message:   "source and target are different servers",
	}}
	if migration.SourceID == migration.TargetID {
		results[0].Message = "source and target must be different servers"
	}

	available := map[string]struct{}{}
	for _, category := range NewCategoryRegistry().Available() {
		available[category] = struct{}{}
	}
	for _, category := range migration.Categories {
		_, ok := available[category]
		message := fmt.Sprintf("category %q is supported by the migration executor", category)
		if !ok {
			message = fmt.Sprintf("category %q is not supported by the migration executor", category)
		}
		results = append(results, CompatibilityCheckResult{
			CheckName: "category:" + category,
			Severity:  SeverityCritical,
			Passed:    ok,
			Message:   message,
		})
	}

	if h.pipeline == nil {
		return results
	}

	sourceServer, sourceErr := h.pipeline.srvRepo.GetByID(migration.SourceID)
	targetServer, targetErr := h.pipeline.srvRepo.GetByID(migration.TargetID)
	if sourceErr != nil || targetErr != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "server_records",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   fmt.Sprintf("server lookup failed: source=%v target=%v", sourceErr, targetErr),
		})
		return results
	}

	sourceSSH, sourceErr := getSSHClientForServer(migration.SourceID, sourceServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	targetSSH, targetErr := getSSHClientForServer(migration.TargetID, targetServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	if sourceErr != nil || targetErr != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "ssh_connectivity",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   fmt.Sprintf("ssh connection failed: source=%v target=%v", sourceErr, targetErr),
		})
		return results
	}

	onProgress(WSMessage{Step: "compat", Status: "progress", Value: "Running server compatibility checks..."})
	sshResults, err := NewCompatibilityEngine(sourceSSH, targetSSH, h.repo).CheckCompatibility(ctx, id, onProgress)
	if err != nil {
		results = append(results, CompatibilityCheckResult{
			CheckName: "server_compatibility",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   err.Error(),
		})
		return results
	}
	return append(results, sshResults...)
}

// --- REST: Health ---

func (h *PipelineHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/pipeline/health/")
	id, err := strconv.Atoi(strings.TrimSpace(path))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}
	h.handleHealthByID(w, r, id)
}

func (h *PipelineHandler) handleHealthByID(w http.ResponseWriter, r *http.Request, id int) {
	if r.Method == http.MethodGet {
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if v, err := strconv.Atoi(l); err == nil {
				limit = v
			}
		}
		history, err := h.repo.GetHealthHistory(id, limit)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, "failed to get health history", "INTERNAL")
			return
		}
		shared.WriteJSON(w, http.StatusOK, history)
		return
	}

	if r.Method == http.MethodPost {
		results := h.runHealthPreflight(r.Context(), id)
		for _, result := range results {
			_, _ = h.repo.CreateHealthCheckResult(r.Context(), result)
		}
		shared.WriteJSON(w, http.StatusOK, results)
		return
	}

	shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
}

func (h *PipelineHandler) runHealthPreflight(ctx context.Context, id int) []HealthCheckResult {
	migration, err := h.baseRepo.GetMigration(id)
	if err != nil || migration == nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			CheckType:    HealthCheckTCP,
			CheckTarget:  "migration",
			Status:       "error",
			ErrorMessage: "migration not found",
			HealthScore:  0,
		}}
	}
	if h.pipeline == nil {
		return []HealthCheckResult{}
	}

	targetServer, err := h.pipeline.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			ServerID:     migration.TargetID,
			CheckType:    HealthCheckTCP,
			CheckTarget:  "target",
			Status:       "error",
			ErrorMessage: err.Error(),
			HealthScore:  0,
		}}
	}
	targetSSH, err := getSSHClientForServer(migration.TargetID, targetServer, h.pipeline.srvRepo, h.pipeline.pool, h.pipeline.authSvc, h.pipeline.hosts)
	if err != nil {
		return []HealthCheckResult{{
			MigrationID:  id,
			ServerID:     migration.TargetID,
			CheckType:    HealthCheckTCP,
			CheckTarget:  targetServer.Host,
			Status:       "error",
			ErrorMessage: err.Error(),
			HealthScore:  0,
		}}
	}

	start := time.Now()
	_, _, _, err = targetSSH.ExecContext(ctx, "echo ok")
	result := HealthCheckResult{
		MigrationID:    id,
		ServerID:       migration.TargetID,
		CheckType:      HealthCheckTCP,
		CheckTarget:    targetServer.Host,
		ResponseTimeMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		result.Status = "error"
		result.ErrorMessage = err.Error()
		result.HealthScore = 0
		return []HealthCheckResult{result}
	}
	result.Status = "healthy"
	result.HealthScore = 100
	return []HealthCheckResult{result}
}
