package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"meshium/internal/shared"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Migration-by-id endpoints and the operator action verbs (start, pause,
// resume, cancel, rollback, cutover, commit) plus migration config CRUD.
// --- REST: Pipeline Migration by ID ---

func (h *PipelineHandler) handlePipelineMigrationByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/pipeline/migrations/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	// Phase2D-2: thread the migration identity onto the context so every
	// sub-handler (and the audit rows it writes) carries the migration id.
	r = r.WithContext(WithMigrationID(r.Context(), id))

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetPipelineSession(w, r, id)
		return
	}

	switch parts[1] {
	case "stages":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetStages(w, r, id)
	case "cancel":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleCancel(w, r, id)
	case "pause":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePause(w, r, id)
	case "resume":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleResume(w, r, id)
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePipelineRollback(w, r, id)
	case "export":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handlePipelineExport(w, r, id)
	case "actions":
		if len(parts) < 3 {
			shared.WriteError(w, http.StatusBadRequest, "action is required", "VALIDATION_ERROR")
			return
		}
		h.handlePipelineAction(w, r, id, parts[2])
	case "events":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetEvents(w, r, id)
	case "workloads":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetWorkloads(w, r, id)
	case "dependency-graph":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetDependencyGraph(w, r, id)
	case "compatibility":
		h.handleCompatibilityByID(w, r, id)
	case "config":
		h.handleConfigByID(w, r, id)
	case "risk":
		h.handleRiskByID(w, r, id)
	case "health":
		h.handleHealthByID(w, r, id)
	case "replication":
		h.handleReplicationByID(w, r, id)
	case "traffic":
		h.handleTrafficByID(w, r, id)
	case "metrics":
		h.handleMetricsByID(w, r, id)
	case "audit":
		h.handleAuditByID(w, r, id)
	case "queue":
		h.handleQueueByID(w, r, id)
	case "provision":
		h.handleProvisionByID(w, r, id)
	case "strategy":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetStrategy(w, r, id)
	case "warnings":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetWarnings(w, r, id)
	case "planner-result":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetPlannerResult(w, r, id)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func (h *PipelineHandler) handlePipelineAction(w http.ResponseWriter, r *http.Request, id int, action string) {
	// Phase2D-5: honor a client-supplied Idempotency-Key. A retried mutation
	// (network blip, client timeout) carrying the same key is answered from the
	// original audit outcome instead of re-executing — never masking a real
	// failure. The key is also threaded into the action's audit rows for
	// traceability. A key with no prior entry proceeds normally.
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		if prev, err := h.repo.GetAuditEntryByIdempotencyKey(r.Context(), id, key); err == nil && prev != nil && prev.Result != "" {
			shared.WriteJSON(w, http.StatusOK, map[string]string{
				"status":            prev.NewState,
				"idempotencyKey":    key,
				"replayedFromAudit": "true",
			})
			return
		}
		ctx := WithIdempotencyKey(r.Context(), key)
		r = r.WithContext(ctx)
	}

	switch action {
	case "cutover":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Cutover(r.Context(), id, nil) }, "cutover")
	case "commit":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		err := h.pipeline.Commit(r.Context(), id, nil)
		if err == nil {
			shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "committed"})
			return
		}
		// P0-2: a commit while cutover is unconfirmed is a structured 409 — not a
		// generic INVALID_STATE. The operator must confirm traffic moved first.
		if errors.Is(err, ErrCutoverNotConfirmed) {
			shared.WriteJSON(w, http.StatusConflict, map[string]string{
				"error":  "cutover_not_confirmed",
				"detail": "operator must confirm traffic moved to target before committing",
			})
			return
		}
		shared.WriteError(w, http.StatusConflict, err.Error(), "INVALID_STATE")
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Rollback(r.Context(), id, nil) }, "rolled_back")
	case "pause":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Pause(r.Context(), id, nil) }, "paused")
	case "resume":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Resume(r.Context(), id, nil) }, "resumed")
	case "cancel":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Cancel(r.Context(), id, nil) }, "cancelled")
	case "retry":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		handlePipelineActionResult(w, func() error { return h.pipeline.Retry(r.Context(), id, nil) }, "retried")
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func handlePipelineActionResult(w http.ResponseWriter, fn func() error, status string) {
	if err := fn(); err != nil {
		shared.WriteError(w, http.StatusConflict, err.Error(), "INVALID_STATE")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": status})
}

func normalizeMigrationConfig(config *MigrationConfig, categories []string) {
	if config == nil {
		return
	}
	if len(categories) > 0 {
		config.Categories = categories
	}
	if config.ObservationDuration > 0 && config.ObservationDuration < time.Second {
		config.ObservationDuration *= time.Second
	}
	if config.MaxErrorRate > 1 {
		config.MaxErrorRate = config.MaxErrorRate / 100
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay > 0 && config.RetryDelay < time.Second {
		config.RetryDelay *= time.Second
	}
	if config.ParallelTransfers == 0 {
		config.ParallelTransfers = 4
	}
}

// warnTransferLimitsHonesty emits a single operator-visible warning when
// per-migration transfer limits (BandwidthLimit / ParallelTransfers) are set
// but the live pipeline cannot honor them. The active path replays collected
// category data through appliers and never invokes the rsync-backed
// SyncEngine, so --bwlimit / --parallel are inert unless that engine is
// driven. We surface this instead of silently swallowing the config so an
// operator is never misled into expecting throttled transfers.
func warnTransferLimitsHonesty(ctx context.Context, config *MigrationConfig) {
	if config == nil {
		return
	}
	if config.BandwidthLimit > 0 || config.ParallelTransfers > 0 {
		shared.LogCtx(ctx).Warn(
			"transfer limits set but not honored on the active pipeline path",
			"bandwidthLimit", config.BandwidthLimit,
			"parallelTransfers", config.ParallelTransfers,
			"detail", "active path replays collected data via appliers; limits apply only when the rsync SyncedTransfer engine is driven",
		)
	}
}

// supportedTrafficProviders is the runtime guardrail consulted at API
// validation. It lists ONLY providers that have a real fenced switcher with
// read-after-write ownership verification (newTrafficSwitcher in pipeline.go)
// — nginx, haproxy, and (from Phase 4B) caddy. Every other provider once
// selectable here had NO switcher and failed closed only at cutover runtime
// (finding A.2-1); that is a documentation/UX honesty gap. We now block them at
// the API boundary so a client is told up front, not mid-migration.
//
// The legacy TrafficSwitchEngine.Switch in traffic.go still references the
// other provider constants for the MANUAL switch path; they remain defined as
// enum values but are not selectable as automatic switches.
var supportedTrafficProviders = map[TrafficProvider]bool{
	TrafficProviderNginx:   true,
	TrafficProviderHAProxy: true,
	TrafficProviderCaddy:   true,
}

// supportedReplicationModes is the runtime guardrail for the replication
// strategy. Anything outside this set is not a supported engine within scope.
var supportedReplicationModes = map[ReplicationMode]bool{
	ReplicationModeNone:      true,
	ReplicationModeStreaming: true,
	ReplicationModeLogical:   true,
	ReplicationModeReplica:   true,
	ReplicationModeDump:      true,
}

// validateConfigSupport rejects config that selects an unsupported
// provider/replication engine at the API boundary, so a client is told up
// front instead of discovering it via a fail-closed cutover mid-migration. It
// delegates to the single PolicyEngine surface (DefaultPolicy) so the API
// boundary and execution time cannot drift (Phase 4E).
func validateConfigSupport(config *MigrationConfig) error {
	return DefaultPolicy.CheckConfigSupport(config)
}

func (h *PipelineHandler) handleConfigByID(w http.ResponseWriter, r *http.Request, id int) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.repo.GetMigrationConfig(id)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, "migration config not found", "NOT_FOUND")
			return
		}
		h.redactDBConfig(cfg)
		shared.WriteJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		shared.LimitRequestBody(r)
		var cfg MigrationConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
			return
		}
		normalizeMigrationConfig(&cfg, cfg.Categories)
		warnTransferLimitsHonesty(r.Context(), &cfg)
		if err := validateConfigSupport(&cfg); err != nil {
			shared.WriteError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
			return
		}
		if len(cfg.Categories) == 0 {
			shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
			return
		}
		// If the UI sent the redacted "set" placeholder (or no password), keep
		// the existing encrypted password rather than overwriting it with empty.
		if cfg.DatabaseConfig != nil && (cfg.DatabaseConfig.Password == "" || cfg.DatabaseConfig.Password == "set") {
			if existing, err := h.repo.GetMigrationConfig(id); err == nil && existing.DatabaseConfig != nil {
				cfg.DatabaseConfig.Password = existing.DatabaseConfig.Password
			} else {
				cfg.DatabaseConfig.Password = ""
			}
		}
		if err := h.encryptDBConfig(&cfg); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to secure db credentials: %v", err), "INTERNAL")
			return
		}
		if err := h.repo.SetMigrationConfig(id, &cfg); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store migration config: %v", err), "INTERNAL")
			return
		}
		if updater, ok := h.baseRepo.(interface {
			SetMigrationCategories(int, []string) error
		}); ok {
			if err := updater.SetMigrationCategories(id, cfg.Categories); err != nil {
				shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update migration categories: %v", err), "INTERNAL")
				return
			}
		}
		shared.WriteJSON(w, http.StatusOK, cfg)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}
