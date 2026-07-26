package migration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

// wsConn serializes data-frame writes to a websocket connection.
// gorilla/websocket permits only one concurrent writer for data frames, but
// the pipeline WS handler writes from three goroutines: the progress callback
// driving Execute/Rollback, the client read loop (pong replies), and the
// terminal status/error write. Routing every WriteJSON through this mutex
// prevents interleaved, corrupted frames. WriteControl (the heartbeat ping) is
// exempt from that restriction per gorilla's contract, so it stays on the raw
// conn and is intentionally not routed through here.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsConn) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// PipelineHandler exposes REST and WebSocket routes for the zero-downtime
// migration pipeline. It extends the existing Handler with new endpoints
// for risk assessment, compatibility checks, provisioning, health checks,
// replication, traffic switching, and real-time pipeline progress.
type PipelineHandler struct {
	pipeline *Pipeline
	repo     PipelineRepo
	baseRepo Repo
	upgrader websocket.Upgrader
}

// NewPipelineHandler creates a new PipelineHandler.
func NewPipelineHandler(pipeline *Pipeline, repo PipelineRepo, baseRepo Repo) *PipelineHandler {
	return &PipelineHandler{
		pipeline: pipeline,
		repo:     repo,
		baseRepo: baseRepo,
		upgrader: websocket.Upgrader{
			CheckOrigin: shared.CheckWebSocketOrigin,
		},
	}
}

// encryptDBConfig encrypts the DatabaseConfig password in place so it is stored
// encrypted at rest (mirrors server.Service.encryptCredential). No-op when no
// password or no database config is set.
func (h *PipelineHandler) encryptDBConfig(cfg *MigrationConfig) error {
	if cfg == nil || cfg.DatabaseConfig == nil || cfg.DatabaseConfig.Password == "" {
		return nil
	}
	if h.pipeline == nil || h.pipeline.authSvc == nil {
		return fmt.Errorf("app is locked")
	}
	key := h.pipeline.authSvc.GetAESKey()
	if key == nil {
		return fmt.Errorf("app is locked")
	}
	enc, err := shared.Encrypt(key, []byte(cfg.DatabaseConfig.Password))
	if err != nil {
		return fmt.Errorf("encrypt db password: %w", err)
	}
	cfg.DatabaseConfig.Password = string(enc)
	return nil
}

// decryptDBConfig decrypts the DatabaseConfig password in place. Used in
// Pipeline.Execute (via the Pipeline's authSvc) and when returning config is
// NOT the path — config returned to the UI is redacted, never decrypted.
func (h *PipelineHandler) decryptDBConfig(cfg *MigrationConfig) error {
	if cfg == nil || cfg.DatabaseConfig == nil || cfg.DatabaseConfig.Password == "" {
		return nil
	}
	if h.pipeline == nil || h.pipeline.authSvc == nil {
		return fmt.Errorf("app is locked")
	}
	key := h.pipeline.authSvc.GetAESKey()
	if key == nil {
		return fmt.Errorf("app is locked")
	}
	dec, err := shared.Decrypt(key, []byte(cfg.DatabaseConfig.Password))
	if err != nil {
		return fmt.Errorf("decrypt db password: %w", err)
	}
	cfg.DatabaseConfig.Password = string(dec)
	return nil
}

// redactDBConfig clears the password before returning config to the UI
// (mirrors server.Service.redactServer). Leaves a "set" indicator.
func (h *PipelineHandler) redactDBConfig(cfg *MigrationConfig) {
	if cfg == nil || cfg.DatabaseConfig == nil {
		return
	}
	if cfg.DatabaseConfig.Password != "" {
		cfg.DatabaseConfig.Password = "set"
	}
}

// RegisterRoutes registers all pipeline routes on the mux.
//
// Phase2D-2: every REST handler is wrapped with withRequestID so a single
// externally-initiated operation carries one correlation id through the whole
// call chain (handler → pipeline → stage → executor → events → audit → logs).
// WebSocket endpoints mint the id at the command boundary inside the handler.
func (h *PipelineHandler) RegisterRoutes(mux *http.ServeMux) {
	// REST endpoints
	mux.HandleFunc("/api/pipeline/migrations", withRequestID(h.handleCreatePipelineMigration))
	mux.HandleFunc("/api/pipeline/migrations/", withRequestID(h.handlePipelineMigrationByID))
	mux.HandleFunc("/api/pipeline/risk/", withRequestID(h.handleRisk))
	mux.HandleFunc("/api/pipeline/compatibility/", withRequestID(h.handleCompatibility))
	mux.HandleFunc("/api/pipeline/health/", withRequestID(h.handleHealth))
	mux.HandleFunc("/api/pipeline/replication/", withRequestID(h.handleReplication))
	mux.HandleFunc("/api/pipeline/traffic/", withRequestID(h.handleTraffic))
	mux.HandleFunc("/api/pipeline/metrics/", withRequestID(h.handleMetrics))
	mux.HandleFunc("/api/pipeline/audit/", withRequestID(h.handleAudit))
	mux.HandleFunc("/api/pipeline/diagnostics/", withRequestID(h.handleDiagnostics))
	mux.HandleFunc("/api/pipeline/recovery/", withRequestID(h.handleRecovery))
	mux.HandleFunc("/api/pipeline/policy", withRequestID(h.handlePolicy))

	// WebSocket endpoints (id minted at the command boundary, not here)
	mux.HandleFunc("/ws/pipeline/", h.handlePipelineWS)
	mux.HandleFunc("/ws/compatibility/", h.handleCompatibilityWS)
}

// withRequestID ensures an incoming request has a correlation id: honor a
// client-supplied X-Request-ID (so downstream retries/traces align), else
// mint a fresh one. The value is treated as an opaque label only.
// withRequestID ensures an incoming request has correlation identity: honor a
// client-supplied X-Request-ID (so downstream retries/traces align), else
// mint a fresh per-request id. WithCorrelation also derives a stable
// boundary CorrelationID when none is present. The values are opaque labels.
func withRequestID(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		ctx := WithCorrelation(r.Context(), "", reqID, "", "", "")
		if got := RequestIDFrom(ctx); got != "" {
			w.Header().Set("X-Request-ID", string(got)) // echo so callers can correlate
		}
		next(w, r.WithContext(ctx))
	}
}

// --- REST: Create Pipeline Migration ---

func (h *PipelineHandler) handleCreatePipelineMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	shared.LimitRequestBody(r)
	var req struct {
		SourceID    int              `json:"sourceId"`
		TargetID    int              `json:"targetId"`
		Categories  []string         `json:"categories"`
		OperationID string           `json:"operationId,omitempty"`
		Config      *MigrationConfig `json:"config,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceID == 0 || req.TargetID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceId and targetId are required", "VALIDATION_ERROR")
		return
	}
	if len(req.Categories) == 0 {
		shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
		return
	}

	// Guardrail: reject config that selects an unsupported provider/replication
	// engine at the API boundary, before any state is persisted or a pipeline
	// is started. Unsupported selections fail closed mid-migration otherwise.
	if err := validateConfigSupport(req.Config); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err.Error(), "VALIDATION_ERROR")
		return
	}

	if req.Config == nil {
		req.Config = DefaultMigrationConfig()
	}
	normalizeMigrationConfig(req.Config, req.Categories)
	// Honesty guard: the live pipeline replays collected category data through
	// appliers, it never drives rsync, so BandwidthLimit/ParallelTransfers are
	// inert on the active path. Warn before persisting so an operator expecting
	// throttled transfers is not silently misconfigured.
	warnTransferLimitsHonesty(r.Context(), req.Config)
	if err := h.encryptDBConfig(req.Config); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to secure db credentials: %v", err), "INTERNAL")
		return
	}

	// Create migration record directly via base repo
	migrationID, err := h.baseRepo.CreateMigration(req.SourceID, req.TargetID, req.Categories, req.OperationID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create migration: %v", err), "INTERNAL")
		return
	}
	if err := h.repo.SetMigrationConfig(migrationID, req.Config); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store migration config: %v", err), "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      migrationID,
		"status":  "created",
		"message": "Migration created. Use /ws/pipeline/{id}/execute to start.",
	})
}
