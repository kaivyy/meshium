# Phase 4I.X — Pipeline Stepper 0–11 Flow Audit (Map)

> **For agentic workers:** read-only audit. No code changed. All claims cite real
> files & lines. Where the code is ambiguous, it is written as *ambiguity*, not a
> conclusion.

## 1. Files investigated

**Backend (authority)**
- `internal/mod/migration/state.go` — `MigrationState` enum (28 states), `String()`,
  `StateString()` (DB string form), `IsTerminal()`, `IsRunning()`, `IsValidTransition()`,
  `transitionTable`.
- `internal/mod/migration/pipeline_models.go:548-551` — `MigrationSession.State` field type.
- `internal/mod/migration/pipeline_handler.go` — `buildSession` (1720-1798),
  `handleGetPipelineSession` (727-734), `handlePipelineAction` (497-576), cutover/commit
  guards (533-542), `CurrentState` emission (1635-1648).
- `internal/mod/migration/pipeline.go` — `Cutover`/`Commit`/`Rollback`/`Pause`/`Resume`/
  `Cancel`/`Retry` terminal-state setters; `ErrAwaitingCutover` → `StateAwaitingCutover`.
- `internal/mod/migration/recovery.go:277-293` — rollback final states
  (`StateRolledBack` / `StateRollbackDegraded` / `StateFailed`).
- `internal/mod/migration/engine.go:537` — `transition` emits `State: <string>`.
- `internal/shared/types.go:18` — `WriteJSON` uses plain `json.Encode` (no custom marshaling).

**Frontend (presentation)**
- `web/src/routes/migrations/[id]/pipeline/+page.svelte` — the pipeline page (all logic:
  `loadSession`, `recoverStepFromState`, `recoverStepFromRecords`, `wizardStepForStage`,
  `startPipeline` WS handler, action bar, cutover/commit/rollback, `isTerminalState`,
  `canRetryState`, `rollbackAvailable`).
- `web/src/lib/api/pipeline.ts` — `WIZARD_STEPS` (0-10), `MigrationSession` interface,
  `reconcileRollbackState`, `pipelineApi`, `wsPipelineConnect` (reconnect/replay),
  `RollbackState`.
- `web/src/lib/components/PipelineStepper.svelte` — step index display.
- `web/src/lib/components/CutoverChecklist.svelte` — cutover readiness gate.
- `web/src/lib/components/ObservationPanel.svelte` — observation + commit/rollback buttons.
- `web/src/lib/components/SafetyStatePanel.svelte` — awaiting_cutover / observing /
  needs_manual_intervention / rollback_degraded banners.

## 2. Stepper 0–11 — what each step IS

**Indexing:** the stepper is **0-based internally** (`currentStep` 0..10) and **displayed
as `Step {currentStep+1}`** (`PipelineStepper.svelte:60`). There are exactly **11 wizard
steps (indices 0–10)**. The user's "step 11" is therefore index **10 = Finish**; there is
no literal index 11. `WIZARD_STEPS` (`pipeline.ts:859-871`):

| Idx | Label (UI)            | Main component rendered @ currentStep | Authoritative source of truth |
|-----|-----------------------|---------------------------------------|-------------------------------|
| 0 | Discovery             | Discovery block (`+page:1257`)        | `recoverStepFromState` (state) + `recoverStepFromRecords` (stages) |
| 1 | Compatibility         | CompatibilityChecklist (`+page:1309`) | `runCompatibilityCheck` WS + records |
| 2 | Risk                  | Risk report + PlannerView (`+page:1353`) | `runRiskAssessment` REST |
| 3 | Plan                  | category/config form (`+page:1398`)  | **local** `config.categories` + `goNext` only (no state maps here) |
| 4 | Dry Run              | DryRun result (`+page:1468`)          | `runDryRun` WS + persisted `session.dryRun` |
| 5 | Provision             | Provision states (`+page:1534`)       | `runProvision` REST |
| 6 | Execute              | "Start Migration" (`+page:1616`)      | `startPipeline` → WS; `pipelineRunning` flag |
| 7 | Live Monitoring       | replication/health/lag (`+page:1641`) | WS `currentState` + live metrics (`updateLiveMetrics`) |
| 8 | Cutover              | `CutoverChecklist` (`+page:1677`)     | WS `currentState` = pre_cutover/traffic_switch; checklist gate |
| 9 | Observation          | `ObservationPanel` (`+page:1691`)      | WS `currentState` = post_verification/observation; local timer |
| 10 | Finish               | Migration Complete (`+page:1720`)     | terminal state (committed/rolled_back/cancelled/…) |

**Step 3 (Plan) is the only step with no backend state driving it.** `recoverStepFromState`
never maps any `MigrationState` to step 3 (`+page:237-263`). It is reached only by the
operator clicking **Next** and is "completed" in `recoverStepFromRecords` only when a
`planning` *stage* has completed (`+page:365`). So step 3 is effectively **local/config-
driven**, not state-driven.

## 3. MigrationState → UI step table

`recoverStepFromState` (`+page:237-263`) is the REST-load mapping; `startPipeline` WS
handler (`+page:757-772`) is the live mapping. `isTerminalState` (`+page:819-821`) is the
FE terminal test; `state.go:163-165` is the BE terminal test.

| Backend `MigrationState` (state.go) | String form | FE step (REST) | FE step (live WS) | FE terminal? | BE terminal? (`IsTerminal`) | Mismatch / note |
|---|---|---|---|---|---|---|
| `StateCreated` (= `planned`) | `planned` | 0 (`created`) | — | no | no | OK |
| `StatePlanning` | `planning` | 0 | — | no | no | maps to Discovery, not Plan |
| `StateDiscovery` | `discovery` | 0 | — | no | no | OK |
| `StateCompatibilityCheck` | `compatibility_check` | 1 | — | no | no | OK |
| `StateRiskAssessment` | `risk_assessment` | 2 | — | no | no | OK |
| `StateBackup` | `backup` | 5 | — | no | no | OK |
| `StateSnapshot/Transferring/Applying/Verifying` (legacy) | `snapshot/transferring/applying/verifying` | **none → keeps currentStep** | — | no | no | **drift**: not in `recoverStepFromState` → falls to default (stays at 0) |
| `StateProvisionTarget` | `provision_target` | 6 | — | no | no | OK |
| `StateInstallDependencies` | `install_dependencies` | 6 | — | no | no | OK |
| `StateInitialSync` | `initial_sync` | 6 | — | no | no | OK |
| `StateLiveReplication` | `live_replication` | 7 | 7 | no | no | OK |
| `StateVerification` | `verification` | 7 | — | no | no | OK |
| `StatePreCutover` | `pre_cutover` | 8 | 8 | no | no | OK |
| `StateTrafficSwitch` | `traffic_switch` | 8 | 8 | no | no | OK |
| `StatePostVerification` | `post_verification` | 9 (+timer) | 9 | no | no | OK |
| `StateObservation` | `observation` | 9 (+timer) | 9 | no | no | OK |
| `StateAwaitingCutover` | `awaiting_cutover` | **none → keeps currentStep** | none (not in WS switch) | no (FE treats non-terminal) | **no** (explicitly NOT terminal, state.go:162) | **drift**: no dedicated step; piggybacks step 8 + `SafetyStatePanel` banner only |
| `StateCommitted` | `completed` | 10 | 10 | yes | yes | OK |
| `StateFailed` | `failed` | **none → keeps currentStep** (but `recoverStepFromRecords` may fail-step) | none | no (FE) | no (BE: can →rollback) | FE `isTerminalState` excludes `failed` (correct); but no banner |
| `StateRollback` | `rolling_back` | none → stays at current step | none (not in WS switch) | no | no | overlay via `rollbackState='running'` |
| `StateRolledBack` | `rolled_back` | 10 | none (relies on reload) | yes | yes | live: step NOT auto-advanced (WS switch lacks `rolled_back`) |
| `StateRollbackDegraded` | `rollback_degraded` | none → stays | none | yes (FE) | yes | `SafetyStatePanel` banner |
| `StateInterrupted` | `interrupted` | **none → keeps currentStep** | none | no | no | only `Retry` button appears (`canRetryState`) |
| `StatePaused` | `paused` | none → keeps currentStep | none | no | no | only `Resume` button |
| `StateResuming` | `resuming` | none → keeps currentStep | none | no | no | no specific UI |
| `StateCancelled` | `cancelled` | 10 | — | yes | yes | OK — but **no Cancel button exists** in this page |
| `StateNeedsManualIntervention` | `needs_manual_intervention` | none → keeps currentStep | none | yes (FE) | **no** (BE: not terminal, operator may act) | **mismatch**: FE marks terminal → `rollbackAvailable=false`, yet BE allows retry/resume/cancel; no button surfaces it |

**Three states are terminal in FE but not BE, or vice-versa:** `needs_manual_intervention`
(FE terminal / BE non-terminal), `awaiting_cutover` (FE non-terminal / BE non-terminal
but no step). `failed` correctly non-terminal in both.

## 4. Step → actions available

`rollbackAvailable = !isTerminalState(currentState) && currentStep >= 6` (`+page:1061`).
`staleData = wsConnectionState === 'stale'` blocks cutover/commit/rollback
(`+page:119`, `828`, `1148-1151`, `ObservationPanel`).

| Step | Execute | Retry | Pause | Resume | Cancel | Cutover | Commit | Rollback |
|------|---------|-------|-------|--------|--------|---------|--------|----------|
| 0 Discovery | — | if `interrupted`/`failed` | — | if `paused` | — (no button) | — | — | if avail |
| 1 Compat | — | ↑ | — | ↑ | — | — | — | if avail |
| 2 Risk | — | ↑ | — | ↑ | — | — | — | if avail |
| 3 Plan | — | ↑ | — | ↑ | — | — | — | if avail |
| 4 Dry Run | — | ↑ | — | ↑ | — | — | — | if avail |
| 5 Provision | — | ↑ | — | ↑ | — | — | — | if avail |
| 6 Execute | **Start Migration** (`!pipelineRunning`) | ↑ | if running | ↑ | — | — | — | if avail |
| 7 Live Mon | — | ↑ | if running | ↑ | — | Next→"Ready for Cutover" (`replicationLag<=5`) | — | if avail |
| 8 Cutover | — | ↑ | if running | ↑ | — | **Start Cutover** (checklist gate) | — | if avail |
| 9 Obs | — | ↑ | if running | ↑ | — | — | **Commit** (only when stepStatus==='completed') | **Rollback** |
| 10 Finish | — | ↑ | — | — | — | — | — | — |

- **Cancel**: exists in `pipelineApi.cancel` (`pipeline.ts:599`) and BE `handleCancel`, but
  **no button renders it** in this page. `cancelled` terminal state is reachable only
  elsewhere.
- **Cutover** gated by `allChecksPassed` (`CutoverChecklist.svelte:23`): target containers
  healthy, MySQL synced, Redis/BullMQ synced, Worker A paused, Worker B ready, lag ≤ 5s,
  health ≥ 80, `rollbackAvailable`.
- **Commit** gated by `stepStatus[9] === 'completed'` (`ObservationPanel.svelte:82`), which
  only flips when the local observation timer elapses `observationDuration` seconds
  (`+page:890`). If WS never emits `committed`/`completed`, the timer is the sole path to
  enabling Commit.

## 5. Page-load / reconnect / replay flow

1. `onMount` → `loadSession()` (`+page:129-214`).
2. **REST first**: `session = await pipelineApi.getSession(id)` → `GET
   /api/pipeline/migrations/:id` (`buildSession`). Sets `currentState = session.state`.
3. `recoverStepFromState()` then `recoverStepFromRecords()` position the stepper.
4. `restoreMetrics()` flags `metricsStale=true` until first live frame.
5. WS **not** connected until `startPipeline()` (step 6) calls `wsPipelineConnect`
   (`pipeline.ts:676`). So on a cold load of an idle migration, **no WS is open** — the
   stepper is positioned purely from REST + localStorage (`restoreStep`).
6. Reconnect/replay: `wsPipelineConnect` tracks `lastSequence`; on reopen it sets status
   `replaying`, replays `events?after_seq=` (`pipeline.ts:733`), then `connected`. `stale`
   fires after `staleTimeout=15000ms` with no frame (`pipeline.ts:702-709`). `stale` blocks
   irreversible actions; `replaying` is presentation-only.
7. **Authoritative vs presentational:** REST `session.state` + `session.stages` are
   authoritative for initial position; live WS `currentState` is authoritative during a run;
   `localStorage` `stepKey`/`metricsKey` are **presentational** overrides (per-browser,
   not multi-device).

## 6. Phase grouping

| Phase | Backend states | UI step | Primary endpoint/action | WS events | Auto/Manual |
|-------|---------------|---------|------------------------|-----------|------------|
| pre-exec / planning / discovery | planned/planning/discovery | 0 | `getSession` (REST) + `runDiscovery` | — | Manual (Next) |
| compat + risk + health evidence | compatibility_check, risk_assessment | 1,2 | `checkCompatibility` (WS), `assessRisk` (REST) | `compat:*`, `dryrun:*` | Manual |
| execute / data movement | initial_sync, install_dependencies, provision_target, backup | 5,6 | `startPipeline` → WS | `engine`/`State:*` | Manual start, then auto-advance |
| replication / verification | live_replication, verification | 7 | live metrics | `currentState` | Auto-advance at lag≤5 (FE gate) |
| cutover | pre_cutover, traffic_switch, awaiting_cutover | 8 | `actions/cutover` (POST) | `currentState` | Manual (checklist) |
| observation | post_verification, observation | 9 | local timer | `currentState` | Auto-advance on timer; Commit manual |
| commit | (committed) | 10 | `actions/commit` (POST) | `currentState=committed` | Manual |
| rollback | rolling_back, rolled_back, rollback_degraded, (failed), needs_manual_intervention | stays at current step (overlay) | `actions/rollback` (POST) | `currentState` | Manual |

## 7. Edge cases / ambiguities

- **A. `session.state` type mismatch (see Final Report, BLOCKING).** REST returns
  `MigrationState` as a **JSON integer** (no custom marshaling; empirically `{"state":1}`),
  but FE types it `string` and calls `.toLowerCase()` (`+page:175,239`) → **throws** on a
  number. The outer `try/catch` then falls back to `migrationApi.get` (legacy
  `/migrations/:id`, string `status`). So cold load works *only via the fragile fallback*.
- **B. `needs_manual_intervention` dead-end.** FE marks it terminal (no rollback/cancel) but
  BE allows retry/resume/cancel; no button surfaces it.
- **C. `409 cutover_not_confirmed` not surfaced.** `confirmCutover` (`+page:841-853`) does
  not special-case the 409; shows generic toast. Operator gets no distinct guidance.
- **D. `awaiting_cutover` has no step.** Relies on step 8 + `SafetyStatePanel` banner.
- **E. `'archived'` is referenced** in `recoverStepFromState`/`isTerminalState` but is
  **not a `MigrationState`** (absent from `state.go`). Dead branch.
- **F. Rollback during live run does not move the stepper** — it overlays via
  `rollbackState` + `SafetyStatePanel`. Intended, but means the stepper can show e.g. step 8
  while a rollback is in progress (confusing if operator expects a "rollback" step).
- **G. Observation panel shows hardcoded fake metrics** (`0.1%` error rate, `120ms` P95,
  simulated `trafficA/B` split, `ObservationPanel.svelte:14-16,71-78`) — purely visual.
- **H. localStorage step persistence can override the BE-derived step** within
  `lastCompleted+1` (`+page:346-358`) — presentational drift on refresh.

## 8. Verification method

- `MigrationState` JSON shape proven by isolated `go run` snippet: `{"state":1}`.
- `(1).toLowerCase()` proven to throw via `node` snippet.
- `IsTerminal`/`StateString`/`transitionTable` read directly from `state.go`.
- Step logic read line-by-line from `+page.svelte` and `pipeline.ts`.
- No server was running; live-wire behavior inferred from `CurrentState =
  state.StateString()` (`pipeline_handler.go:1645`) which is a **string** — so the type
  mismatch is specific to the **REST session loader**, not the live WS path.
