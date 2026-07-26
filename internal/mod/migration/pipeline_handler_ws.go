package migration

import (
	"context"
	"encoding/json"
	"log"
	"meshium/internal/mod/auth"
	"meshium/internal/shared"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// The pipeline WebSocket: live progress streaming, history replay on connect,
// and the helpers that build the session snapshot the UI renders.
// --- WebSocket: Pipeline Progress ---

func (h *PipelineHandler) handlePipelineWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.pipeline == nil {
		log.Printf("pipeline not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/pipeline/")
	if path == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.Split(path, "/")
	migrationID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid migration ID", http.StatusBadRequest)
		return
	}

	action := "execute"
	if len(parts) > 1 {
		action = parts[1]
	}

	// Upgrade with subprotocol support for secure WS auth
	responseHeader := http.Header{}
	if proto := auth.WebSocketSubprotocolToken(r); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	conn, err := h.upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		shared.LogCtx(r.Context()).Error("websocket upgrade failed", "error", err, "migration_id", migrationID)
		return
	}
	defer conn.Close()

	// All data-frame writes go through safeConn to serialize the three writer
	// goroutines (progress callback, read loop, terminal status). The heartbeat
	// goroutine keeps using conn.WriteControl directly, which gorilla allows.
	safeConn := &wsConn{conn: conn}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Phase2D-2: thread the migration + operator-action identity onto the WS
	// context so commands issued over the socket (pause/cancel/resume and the
	// execute/rollback runs) carry correlation through to logs, events, and
	// audit rows. Reconnects mint a fresh operator-action id so two sessions
	// for the same migration are distinguishable.
	ctx = WithMigrationID(ctx, migrationID)
	ctx = WithOperatorActionID(ctx, NewOperatorActionIDStr())
	ctx = context.WithValue(ctx, keyActorType, "operator")

	// Heartbeat: send ping every 30s, close if no pong within 10s
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		lastPong := time.Now()
		conn.SetPongHandler(func(appData string) error {
			lastPong = time.Now()
			return nil
		})
		for {
			select {
			case <-ticker.C:
				if time.Since(lastPong) > 40*time.Second {
					// No pong received for too long — close connection
					shared.LogCtx(ctx).Warn("websocket heartbeat timeout", "migration_id", migrationID)
					cancel()
					return
				}
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Sequence counter for WS messages
	var wsSeq int64

	// Start a goroutine to read client messages (for pause/cancel/heartbeat)
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(msg, &cmd) != nil {
				continue
			}
			switch cmd.Action {
			case "pause":
				h.pipeline.Pause(ctx, migrationID, nil)
			case "cancel":
				h.pipeline.Cancel(ctx, migrationID, nil)
			case "resume":
				h.pipeline.Resume(ctx, migrationID, nil)
			case "ping":
				// Client-initiated ping — respond with pong
				safeConn.WriteJSON(WSMessageExtended{Step: "heartbeat", Status: "pong", Timestamp: time.Now().Format(time.RFC3339)})
			}
		}
	}()

	var runErr error
	switch action {
	case "execute":
		// The pipeline WS URL carries no explicit action, so this "execute"
		// branch is the default for every connection — including reconnects and
		// streaming-only clients. Running Execute unconditionally would relaunch
		// an already running, finished, or failed migration on every connect
		// (and, after a process restart, re-run one that has no in-process
		// run-lock to stop it). Only start Execute from a startable state; for
		// anything else, stream persisted history and hold the connection open
		// so the client can observe without re-executing.
		state, stateOK := h.currentMigrationState(migrationID)
		startable := stateOK && (state == StateCreated || state == StateResuming)
		if !startable {
			h.streamPipelineHistory(ctx, safeConn, migrationID, &wsSeq)
			// Keep the connection open (read loop handles resume/cancel/pause,
			// heartbeat keeps it alive) until the client disconnects.
			<-ctx.Done()
			return
		}
		runErr = h.pipeline.Execute(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := h.extendWSMessage(migrationID, msg, wsSeq)
			if writeErr := safeConn.WriteJSON(ext); writeErr != nil {
				shared.LogCtx(ctx).Error("websocket write failed", "error", writeErr)
				cancel()
			}
		})
	case "rollback":
		runErr = h.pipeline.Rollback(ctx, migrationID, func(msg WSMessage) {
			wsSeq++
			ext := h.extendWSMessage(migrationID, msg, wsSeq)
			if writeErr := safeConn.WriteJSON(ext); writeErr != nil {
				shared.LogCtx(ctx).Error("websocket write failed", "error", writeErr)
				cancel()
			}
		})
	default:
		safeConn.WriteJSON(WSMessageExtended{Step: action, Status: "error", Error: "unknown action"})
		return
	}

	if runErr != nil {
		safeConn.WriteJSON(h.extendWSMessage(migrationID, WSMessage{Step: action, Status: "error", Error: runErr.Error()}, wsSeq+1))
		return
	}

	safeConn.WriteJSON(h.extendWSMessage(migrationID, WSMessage{Step: action, Status: "complete"}, wsSeq+1))
}

// --- Helpers ---

// streamPipelineHistory writes the persisted event history for a migration to
// the WebSocket connection. It is used when a client connects to a migration
// that is not in a startable state (already running, finished, or failed) so
// the client can observe past progress without re-executing the migration.
// wsSeq is advanced so any subsequent live messages keep monotonically
// increasing sequence numbers.
func (h *PipelineHandler) streamPipelineHistory(ctx context.Context, conn *wsConn, migrationID int, wsSeq *int64) {
	events, err := h.repo.GetEvents(ctx, migrationID, 0, 1000)
	if err != nil {
		shared.LogCtx(ctx).Error("failed to load event history", "migration_id", migrationID, "error", err)
		return
	}
	for _, ev := range events {
		status := "info"
		switch ev.Level {
		case EventLevelError, EventLevelCritical:
			status = "error"
		case EventLevelWarning:
			status = "warning"
		}
		step := ev.Stage
		if step == "" {
			step = ev.Type
		}
		*wsSeq++
		ext := h.extendWSMessage(migrationID, WSMessage{
			Step:   step,
			Status: status,
			Value:  ev.Message,
		}, *wsSeq)
		if writeErr := conn.WriteJSON(ext); writeErr != nil {
			shared.LogCtx(ctx).Error("websocket history write failed", "migration_id", migrationID, "error", writeErr)
			return
		}
	}
}

func (h *PipelineHandler) extendWSMessage(migrationID int, msg WSMessage, sequence int64) WSMessageExtended {
	ext := WSMessageExtended{
		Step:      msg.Step,
		Status:    msg.Status,
		Value:     msg.Value,
		Error:     msg.Error,
		Timestamp: time.Now().Format(time.RFC3339),
		Sequence:  sequence,
	}

	stage, stageIndex := pipelineStageFromStep(msg.Step)
	if stage != "" {
		total := len(AllStages())
		ext.Stage = string(stage)
		ext.StageIndex = stageIndex + 1
		ext.StageTotal = total
		ext.Progress = float64(stageIndex+1) / float64(total) * 100
		ext.CurrentState = stateForPipelineStage(stage).StateString()
		return ext
	}

	if state, ok := h.currentMigrationState(migrationID); ok {
		ext.CurrentState = state.StateString()
	}
	return ext
}

func pipelineStageFromStep(step string) (PipelineStageName, int) {
	base := step
	if idx := strings.Index(base, ":"); idx >= 0 {
		base = base[:idx]
	}
	aliases := map[string]PipelineStageName{
		"pre_cutover": StagePreCutoverValidation,
		"observation": StagePostCutoverObservation,
	}
	if stage, ok := aliases[base]; ok {
		base = string(stage)
	}
	for i, stage := range AllStages() {
		if string(stage) == base {
			return stage, i
		}
	}
	return "", -1
}

func stateForPipelineStage(stage PipelineStageName) MigrationState {
	switch stage {
	case StageDiscovery:
		return StateDiscovery
	case StageAnalysis:
		return StateCompatibilityCheck
	case StagePlanning:
		return StatePlanning
	case StageValidation, StageHealthVerification:
		return StateVerification
	case StagePreparation:
		return StateBackup
	case StageInitialSync:
		return StateInitialSync
	case StageLiveReplication:
		return StateLiveReplication
	case StagePreCutoverValidation:
		return StatePreCutover
	case StageTrafficSwitch:
		return StateTrafficSwitch
	case StagePostCutoverObservation:
		return StateObservation
	case StageFinalization, StageArchive:
		return StateCommitted
	default:
		return StateCreated
	}
}

func (h *PipelineHandler) currentMigrationState(migrationID int) (MigrationState, bool) {
	if stateRepo, ok := h.baseRepo.(interface {
		GetMigrationState(int) (MigrationState, error)
	}); ok {
		if state, err := stateRepo.GetMigrationState(migrationID); err == nil {
			return state, true
		}
	}
	if h.baseRepo == nil {
		return StateCreated, false
	}
	migration, err := h.baseRepo.GetMigration(migrationID)
	if err != nil || migration == nil {
		return StateCreated, false
	}
	state, err := StateFromString(migration.Status)
	return state, err == nil
}

// buildSession constructs a full MigrationSession from the database.
func (h *PipelineHandler) buildSession(ctx context.Context, migrationID int) (*MigrationSession, error) {
	// Get base migration
	migration, err := h.baseRepo.GetMigration(migrationID)
	if err != nil {
		return nil, err
	}

	// Build session
	session := &MigrationSession{
		Migration: migration,
	}

	// Get config
	config, _ := h.repo.GetMigrationConfig(migrationID)
	if config != nil {
		session.Config = config
	}

	// Get state from status string. State is the string form of the
	// MigrationState (StateString), e.g. "live_replication", matching what the
	// WS currentState carries and what the frontend expects (Phase 4I.X fix 1).
	// migration.Status is itself the StateString form, so use it directly.
	if migration.Status != "" {
		_, _ = StateFromString(migration.Status) // validates the stored status
		session.State = migration.Status
	}

	// Load related data
	session.Stages, _ = h.repo.GetStages(migrationID)
	session.ReplicationStatus, _ = h.repo.GetReplicationStatus(migrationID)
	session.TrafficSwitch, _ = h.repo.GetTrafficSwitchConfig(migrationID)
	session.RiskReport, _ = h.repo.GetRiskReport(migrationID)
	session.VerificationResults, _ = h.repo.GetVerificationResults(migrationID)
	session.CompatibilityResults, _ = h.getStoredCompatibilityResults(migrationID)
	session.HealthHistory, _ = h.repo.GetHealthHistory(migrationID, 50)
	session.CutoverHistory, _ = h.repo.GetCutoverHistory(migrationID)
	session.RollbackHistory, _ = h.repo.GetRollbackHistory(migrationID)
	session.SyncSessions, _ = h.repo.GetSyncSessions(migrationID)
	session.QueueStates, _ = h.repo.GetQueueStates(migrationID)
	session.ProvisionStates, _ = h.repo.GetProvisionStates(migrationID)
	session.AuditTrail, _ = h.repo.GetAuditTrail(migrationID, 200)
	session.Events, _ = h.repo.GetEvents(ctx, migrationID, 0, 200)

	// Restore the latest dry-run preview so step 4 keeps its change list
	// across a page refresh (the value is only held in frontend memory
	// otherwise).
	if step, _ := h.repo.GetLatestStep(migrationID, "dryrun"); step != nil && step.Data != "" {
		var dr DryRunResult
		if json.Unmarshal([]byte(step.Data), &dr) == nil {
			session.DryRun = &dr
		}
	}

	// Surface an unusable plan: a migration in StatusPlanned whose collected
	// categories don't cover every requested category has nothing complete to
	// migrate. This happens when the plan WebSocket disconnected mid-collection
	// before the interrupted-marking fix existed, leaving a stale row with zero
	// or partial collect steps. Set an in-memory error (not persisted) so the
	// frontend's incompletePlan guard shows "Recreate Migration Plan" instead of
	// dead-ending the wizard on empty Dry Run / Provision / Execute steps.
	if migration.Status == StatusPlanned && migration.Error == "" {
		if steps, err := h.baseRepo.GetSteps(migrationID); err == nil {
			collected := make(map[string]bool, len(steps))
			for _, s := range steps {
				if s.Action == "collect" && s.Status == StepStatusCompleted {
					collected[s.Category] = true
				}
			}
			missing := false
			for _, c := range migration.Categories {
				if !collected[c] {
					missing = true
					break
				}
			}
			if missing {
				migration.Error = "category data collection was incomplete for this plan"
			}
		}
	}

	return session, nil
}

// wsProgressAdapter converts a WSMessage callback to WSMessageExtended.
func wsProgressAdapter(fn func(WSMessageExtended)) func(WSMessage) {
	return func(msg WSMessage) {
		fn(WSMessageExtended{
			Step:      msg.Step,
			Status:    msg.Status,
			Value:     msg.Value,
			Error:     msg.Error,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}
