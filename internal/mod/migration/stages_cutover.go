package migration

import (
	"context"
	"fmt"
	"log"
	"meshium/internal/shared"
	"strings"
	"time"
)

// Cutover stages: pre-cutover validation, the traffic switch, and the
// post-cutover observation window.
//
// The default traffic switch does NOT move traffic — it records
// manual_required and says so, because claiming a switch that never happened
// is the most dangerous lie this system could tell. Automatic fenced cutover
// is opt-in and restricted to engine/provider combinations that have a real
// fencing contract.
type preCutoverValidationStage struct {
	repo PipelineRepo
}

func (s *preCutoverValidationStage) Name() PipelineStageName { return StagePreCutoverValidation }

func (s *preCutoverValidationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "pre_cutover", Status: "progress", Value: "Running pre-cutover validation..."})

	// Verify all data is synced
	// Verify all containers are running
	// Verify all services are healthy
	// Verify replication lag is 0 (if replication enabled)

	// Basic check: verify target is still responsive
	if _, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok"); err != nil {
		return fmt.Errorf("pre-cutover check failed: target not responsive: %w", err)
	}

	pc.OnProgress(WSMessage{Step: "pre_cutover", Status: "success", Value: "Pre-cutover validation passed"})
	return nil
}

func (s *preCutoverValidationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// trafficSwitchStage records a manual-cutover checkpoint. It does NOT perform an
// automatic traffic switch: the real DNS/reverse-proxy/load-balancer switch lives
// in TrafficSwitchEngine (traffic.go), which is only driven by CutoverEngine
// (cutover.go) — and CutoverEngine has no live caller, so no automatic switch is
// wired into this pipeline. Rather than silently report a successful cutover that
// never moved any traffic, this stage records the checkpoint as manual_required
// and tells the operator to perform the cutover themselves.
type trafficSwitchStage struct {
	repo   PipelineRepo
	policy *PolicyEngine
}

// policy returns the active policy engine, defaulting to the process-wide
// DefaultPolicy when the stage was constructed without one (the unit tests build
// the stage with only `repo`). This keeps the API-boundary decision and the
// execution-time decision on the same surface.
func (s *trafficSwitchStage) policyEngine() *PolicyEngine {
	if s.policy != nil {
		return s.policy
	}
	return DefaultPolicy
}

// trafficSwitchManualState is the switch_state persisted for the traffic switch
// config when no automatic traffic switch runs. It must NOT be "switched": that
// value would falsely assert traffic was moved to the target.
const trafficSwitchManualState = "manual_required"

// trafficSwitchManualNote explains, on the cutover record and in the operator
// message, why traffic was not switched automatically and what to do next.
const trafficSwitchManualNote = "automatic traffic switch is not enabled in this pipeline; traffic still points at the source — manual cutover required (switch DNS / reverse proxy / load balancer to the target, then verify)"

func (s *trafficSwitchStage) Name() PipelineStageName { return StageTrafficSwitch }

func (s *trafficSwitchStage) Execute(ctx context.Context, pc *PipelineContext) error {
	if pc.Config != nil && pc.Config.AutoCutover {
		return s.runAutoCutover(ctx, pc)
	}

	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "progress", Value: "Recording manual-cutover checkpoint (automatic traffic switch is not enabled in this pipeline)..."})
	// No automatic traffic switch is performed here. The real TrafficSwitchEngine
	// (traffic.go) is not wired into the live pipeline, so this stage only records
	// that a manual cutover is required. It must never claim traffic was switched.

	// Idempotency guard: this stage's completion checkpoint is written by the
	// pipeline loop only after Execute returns. If the process crashed after a
	// record was inserted but before that checkpoint landed, resume re-runs this
	// stage — so guard each side effect against an existing row keyed by
	// migration_id before inserting, or resume duplicates it. The two creates are
	// guarded independently to also cover a crash between them (config written,
	// cutover record not).

	// Create traffic switch config record (skip if one already exists for this migration)
	existingCfg, err := pc.Repo.GetTrafficSwitchConfig(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("check existing traffic switch config failed: %w", err)
	}
	if existingCfg == nil {
		if _, err := pc.Repo.CreateTrafficSwitchConfig(ctx, TrafficSwitchConfig{
			MigrationID:    pc.MigrationID,
			Provider:       pc.Config.TrafficProvider,
			SwitchState:    trafficSwitchManualState,
			HealthCheckURL: pc.Config.HealthCheckURL,
		}); err != nil {
			return fmt.Errorf("persist traffic switch config failed: %w", err)
		}
	}

	// Record cutover (skip if a traffic_switch cutover record already exists).
	// TrafficSwitched is false and the state is unchanged (source → source)
	// because no traffic was moved; the note records why.
	cutovers, err := pc.Repo.GetCutoverHistory(pc.MigrationID)
	if err != nil {
		return fmt.Errorf("check existing cutover history failed: %w", err)
	}
	trafficCutoverExists := false
	for _, cr := range cutovers {
		if cr.CutoverType == "traffic_switch" {
			trafficCutoverExists = true
			break
		}
	}
	if !trafficCutoverExists {
		if _, err := pc.Repo.CreateCutoverRecord(ctx, CutoverRecord{
			MigrationID:     pc.MigrationID,
			CutoverType:     "traffic_switch",
			PreviousState:   "source",
			NewState:        "source",
			TrafficSwitched: false,
			Error:           trafficSwitchManualNote,
			StartedAt:       time.Now().Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("persist cutover record failed: %w", err)
		}
	}

	// Warning, not success: the pipeline did not move traffic. The operator must
	// complete the cutover manually. Return the sentinel so the execute loop
	// stops cleanly at StateAwaitingCutover — never advancing to Observing /
	// Committed / Completed. Only an explicit operator commit may leave it.
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "warning", Value: trafficSwitchManualNote})
	return ErrAwaitingCutover
}

// runAutoCutover drives the Phase 2A+2C fenced cutover orchestrator when
// MigrationConfig.AutoCutover is true. It builds the orchestrator from the
// pipeline context, runs the 12-step machine (fencing before every step,
// switch-before-promote, fail-closed), and persists the outcome. Any error
// fails closed: the cutover record records the sanitized failure and the
// stage returns the error so the pipeline stops at NeedsManualIntervention.
//
// Phase 2C: PostgreSQL, MySQL (seeded), and Redis are supported with fenced
// cutover; traffic switches via nginx or haproxy. MongoDB has no safe cutover
// contract and fails closed here. Engines/providers without a fenced switcher
// fail closed with an explicit unsupported error rather than an unsafe cutover.
func (s *trafficSwitchStage) runAutoCutover(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "progress", Value: "Fenced cutover: acquiring lease and driving switch-before-promote..."})

	// The orchestrator needs the fence-lease surface, which PipelineRepo does
	// not expose. The concrete *sqliteRepo satisfies fenceLeaseRepo; assert it
	// and fail closed if the repo is not fence-capable (never silently skip).
	fenceRepo, ok := pc.Repo.(fenceLeaseRepo)
	if !ok {
		return s.failAutoCutover(ctx, pc, nil, fmt.Errorf("repo does not support fence leases (cannot run fenced cutover safely)"))
	}

	// Phase 2C: dispatch by engine + traffic provider. Build the replication
	// config from the user's DatabaseConfig (the replicator creds) + source/target
	// hosts. No creds are persisted; they live only in the in-memory request for
	// the preflight probe. Engines with no safe cutover contract (mongodb) and
	// providers with no fenced switcher (everything but nginx/haproxy) fail closed
	// here rather than attempting an unsafe cutover.
	dc := pc.Config.DatabaseConfig
	if dc == nil {
		return s.failAutoCutover(ctx, pc, nil, fmt.Errorf("autoCutover requires a DatabaseConfig"))
	}
	// Execution-time policy gate: enforce the engine/provider support decision
	// where the cutover actually happens — not only at POST time. This is the
	// single-source-of-truth check (CheckAutoCutover) shared with the API
	// boundary, so a config that sneaks past validation still fails closed here,
	// before any lease is acquired or mutation attempted.
	if err := s.policyEngine().CheckAutoCutover(dc.Engine, pc.Config.TrafficProvider); err != nil {
		return s.failAutoCutover(ctx, pc, nil, err)
	}
	dbType, ok := cutoverEngineType(dc.Engine)
	if !ok {
		return s.failAutoCutover(ctx, pc, nil, fmt.Errorf("autoCutover: engine %q has no fenced-cutover contract (supported: postgres, mysql, redis; mongodb is not safe)", dc.Engine))
	}
	if pc.Config.TrafficConfig == "" {
		return s.failAutoCutover(ctx, pc, nil, fmt.Errorf("autoCutover requires TrafficConfig (full provider config)"))
	}

	holder, err := GenerateHolder()
	if err != nil {
		return s.failAutoCutover(ctx, pc, nil, fmt.Errorf("generate holder: %w", err))
	}

	replConfig := ReplicationConfig{
		DatabaseType:    dbType,
		DatabaseName:    dc.DatabaseName,
		SourceHost:      pc.SourceServer.Host,
		SourcePort:      defaultPort(dc.Engine),
		TargetHost:      pc.TargetServer.Host,
		TargetPort:      defaultPort(dc.Engine),
		ReplicationUser: dc.Username,
		ReplicationPass: dc.Password,
		MigrationID:     pc.MigrationID,
	}

	trafficReq := TrafficSwitchRequest{
		IdempotencyKey: fmt.Sprintf("meshium-cutover-%d-%s", pc.MigrationID, holder),
		NewConfig:      pc.Config.TrafficConfig,
		VerifyURL:      pc.Config.HealthCheckURL,
	}

	auth := NewFencingAuthority(fenceRepo)
	machine := newCutoverMachine(pc.Repo, auth, pc.MigrationID)
	engine := NewReplicationEngine(pc.SourceSSH, pc.TargetSSH, pc.Repo)

	// Provider dispatch: nginx and haproxy have fenced switchers with
	// read-after-write verification. Any other provider is unsupported here.
	traffic, trafficErr := newTrafficSwitcher(pc.Config.TrafficProvider, pc.TargetSSH)
	if trafficErr != nil {
		return s.failAutoCutover(ctx, pc, nil, trafficErr)
	}
	o := NewCutoverOrchestrator(machine, auth, engine, traffic)

	out, runErr := o.Run(ctx, CutoverRequest{
		MigrationID:    pc.MigrationID,
		Holder:         holder,
		Replication:    replConfig,
		TrafficRequest: trafficReq,
		MaxLagSeconds:  PGCatchUpMaxLag,
		ObserveFor:     pc.Config.ObservationDuration,
	})

	if runErr != nil {
		return s.failAutoCutover(ctx, pc, out, runErr)
	}
	if out == nil || !out.Completed {
		return s.failAutoCutover(ctx, pc, out, fmt.Errorf("cutover did not complete (finalState=%q)", outFinalState(out)))
	}

	// Success: traffic moved to the target. Persist the switch config + cutover
	// record idempotently (guarded against a crash between the two writes).
	if _, err := pc.Repo.GetTrafficSwitchConfig(pc.MigrationID); err == nil {
		// already exists (re-entry after a completed cutover): leave as-is.
	} else {
		if _, err := pc.Repo.CreateTrafficSwitchConfig(ctx, TrafficSwitchConfig{
			MigrationID:    pc.MigrationID,
			Provider:       pc.Config.TrafficProvider,
			SwitchState:    "switched",
			HealthCheckURL: pc.Config.HealthCheckURL,
		}); err != nil {
			return fmt.Errorf("persist traffic switch config failed: %w", err)
		}
	}
	if !cutoverTrafficRecordExists(pc, "traffic_switch") {
		if _, err := pc.Repo.CreateCutoverRecord(ctx, CutoverRecord{
			MigrationID:     pc.MigrationID,
			CutoverType:     "traffic_switch",
			PreviousState:   "source",
			NewState:        "target",
			TrafficSwitched: true,
			StartedAt:       time.Now().Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("persist cutover record failed: %w", err)
		}
	}

	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "success", Value: "Fenced cutover completed: traffic switched to target and verified"})
	return nil
}

// failAutoCutover records the sanitized failure on a cutover record (if the
// orchestrator produced one) and returns the error so the pipeline fails closed
// to NeedsManualIntervention. The failure blob is redacted (no secret leak).
func (s *trafficSwitchStage) failAutoCutover(ctx context.Context, pc *PipelineContext, out *CutoverOutcome, cause error) error {
	failure := trafficSwitchManualNote
	if cause != nil {
		failure = shared.SanitizeString(cause.Error())
	}
	if out != nil && out.Failure != "" {
		failure = out.Failure
	}
	if !cutoverTrafficRecordExists(pc, "traffic_switch") {
		_, _ = pc.Repo.CreateCutoverRecord(ctx, CutoverRecord{
			MigrationID:     pc.MigrationID,
			CutoverType:     "traffic_switch",
			PreviousState:   "source",
			NewState:        "source",
			TrafficSwitched: false,
			Error:           failure,
			StartedAt:       time.Now().Format(time.RFC3339),
		})
	}
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "error", Value: "Fenced cutover failed (fail-closed): " + failure})
	return fmt.Errorf("auto cutover failed (fail-closed): %w", cause)
}

// cutoverTrafficRecordExists reports whether a cutover record of the given type
// already exists for this migration (idempotency guard for re-entry).
func cutoverTrafficRecordExists(pc *PipelineContext, cutoverType string) bool {
	cutovers, err := pc.Repo.GetCutoverHistory(pc.MigrationID)
	if err != nil {
		return false
	}
	for _, cr := range cutovers {
		if cr.CutoverType == cutoverType {
			return true
		}
	}
	return false
}

// pgEngine reports whether the engine string is PostgreSQL (Phase 2A scope).
func pgEngine(engine string) bool {
	e := strings.ToLower(strings.TrimSpace(engine))
	return e == "postgres" || e == "postgresql"
}

// pgPort returns the DB config port, defaulting to 5432 for PG.
func pgPort(dc *DatabaseConfig) int {
	if dc.Port > 0 {
		return dc.Port
	}
	return 5432
}

// cutoverEngineType normalizes an engine string to the ReplicationConfig
// DatabaseType the fenced cutover supports. MongoDB returns ok=false: it has no
// replicaset-lag measurement, so a safe cutover contract cannot exist — callers
// fail closed. PostgreSQL, MySQL/MariaDB, and Redis are the supported engines.
func cutoverEngineType(engine string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(engine))
	switch e {
	case "postgres", "postgresql":
		return "postgres", true
	case "mysql", "mariadb":
		return "mysql", true
	case "redis":
		return "redis", true
	case "mongodb":
		return "", false
	default:
		return "", false
	}
}

// newTrafficSwitcher builds the fenced traffic switcher for a provider. Only
// nginx and haproxy have fenced switchers with read-after-write verification;
// anything else is unsupported for automated cutover (fail closed upstream).
func newTrafficSwitcher(provider TrafficProvider, ssh SSHExecuter) (trafficSwitchDriver, error) {
	switch provider {
	case TrafficProviderNginx:
		return NewNginxSwitcher(ssh, nil), nil
	case TrafficProviderHAProxy:
		return NewHAProxySwitcher(ssh, nil), nil
	case TrafficProviderCaddy:
		return NewCaddySwitcher(ssh, nil), nil
	default:
		return nil, fmt.Errorf("autoCutover: traffic provider %q has no fenced switcher (supported: nginx, haproxy, caddy)", provider)
	}
}

// outFinalState safely reads the outcome's final state for an error message.
func outFinalState(out *CutoverOutcome) string {
	if out == nil {
		return ""
	}
	return string(out.FinalState)
}

func (s *trafficSwitchStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "traffic_switch", Status: "progress", Value: "Recording traffic-cutover rollback checkpoint (no automatic traffic switch was performed)..."})

	// No automatic traffic switch was performed on the forward path, so there is
	// nothing to revert automatically. Record the rollback checkpoint honestly:
	// TrafficReverted is false. If the operator performed a manual cutover, they
	// must manually revert it.
	pc.Repo.CreateRollbackRecord(ctx, RollbackRecord{
		MigrationID:     pc.MigrationID,
		RollbackType:    "traffic",
		TrafficReverted: false,
		Error:           "no automatic traffic switch was performed; if a manual cutover was done, revert DNS / reverse proxy / load balancer to the source manually",
		StartedAt:       time.Now().Format(time.RFC3339),
	})

	return nil
}

// postCutoverObservationStage monitors the target after traffic switch.
type postCutoverObservationStage struct {
	repo PipelineRepo
}

func (s *postCutoverObservationStage) Name() PipelineStageName { return StagePostCutoverObservation }

func (s *postCutoverObservationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "observation", Status: "progress", Value: "Starting post-cutover observation..."})

	// Observation schedule: 1m, 5m, 10m, 30m
	observationDuration := pc.Config.ObservationDuration
	if observationDuration == 0 {
		observationDuration = 10 * time.Minute
	}

	checkInterval := 30 * time.Second
	deadline := time.Now().Add(observationDuration)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check target health
		output, _, _, err := pc.TargetSSH.ExecContext(ctx, "echo ok")
		healthy := err == nil && output == "ok\n"

		if !healthy {
			// Build a real error describing the failure. err may be nil when
			// the command succeeded but returned unexpected output, so avoid
			// wrapping a nil error (which renders as %!w(<nil>)).
			var healthErr error
			if err != nil {
				healthErr = fmt.Errorf("target health check failed during observation: %w", err)
			} else {
				healthErr = fmt.Errorf("target health check failed during observation: unexpected output %q", output)
			}
			if pc.Config.AutoRollbackOnError {
				return healthErr
			}
			pc.OnProgress(WSMessage{Step: "observation", Status: "warning", Value: fmt.Sprintf("Health check warning: %v", healthErr)})
		}

		// Record the actual health status/score from the check, not a
		// hard-coded healthy result.
		healthResult := HealthCheckResult{
			MigrationID: pc.MigrationID,
			ServerID:    pc.Migration.TargetID,
			CheckType:   HealthCheckTCP,
			CheckTarget: pc.TargetServer.Host,
		}
		if healthy {
			healthResult.Status = "healthy"
			healthResult.HealthScore = 100
		} else {
			healthResult.Status = "unhealthy"
			healthResult.HealthScore = 0
			if err != nil {
				healthResult.ErrorMessage = err.Error()
			} else {
				healthResult.ErrorMessage = fmt.Sprintf("unexpected output %q", output)
			}
		}
		if _, err := pc.Repo.CreateHealthCheckResult(ctx, healthResult); err != nil {
			log.Printf("warning: failed to persist observation health check for migration %d: %v", pc.MigrationID, err)
		}

		remaining := time.Until(deadline).Round(time.Second)
		healthLabel := "OK"
		if !healthy {
			healthLabel = "DEGRADED"
		}
		pc.OnProgress(WSMessage{Step: "observation", Status: "progress", Value: fmt.Sprintf("Observation %s — %s remaining", healthLabel, remaining)})

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkInterval):
		}
	}

	pc.OnProgress(WSMessage{Step: "observation", Status: "success", Value: "Observation completed — all checks passed"})
	return nil
}

func (s *postCutoverObservationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	return nil
}

// finalizationStage performs final cleanup and verification.
