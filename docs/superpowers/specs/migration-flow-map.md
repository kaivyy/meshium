# Phase 4F.X — End-to-End Migration Flow Map

**PART 1 — Inventory & High-Level Map** · 2026-07-14

Scope: the full migration journey — New Migration → Plan → Execute/Pipeline →
Cutover/Commit/Observation → Rollback — mapped BE↔FE so we know where the two
must align. Investigation detail: `migration-flow-sync-investigation.md`.

---

## 0. Two BE state vocabularies (read this first)

The backend has **two parallel status systems**, and this is the single most
important fact for the whole sync audit:

1. **Legacy `Status*` strings** (`internal/mod/migration/model.go:17-25`):
   `planned, running, completed, failed, rolling_back, rolled_back,
   rollback_failed, interrupted, resuming`. Used by the *plan/list* surface
   (`/api/migrations`) and the *rollback* sub-status.

2. **`MigrationState` enum** (`internal/mod/migration/state.go:23-89`, string
   form in `stateString` map `state.go:189-221`): the 20-state zero-downtime
   pipeline vocabulary — `planned, planning, discovery, compatibility_check,
   risk_assessment, backup, provision_target, install_dependencies,
   initial_sync, live_replication, verification, pre_cutover, traffic_switch,
   post_verification, observation, completed, failed, rolling_back, rolled_back,
   rollback_degraded, interrupted, paused, resuming, cancelled, awaiting_cutover,
   needs_manual_intervention`. Persisted as its `StateString()` and surfaced via
   `GET /api/pipeline/migrations/:id` (the pipeline `session.state` field).

The FE pipeline page consumes the **`MigrationState` string** (`session.state` →
`currentState`). The FE list page consumes the **legacy `Status*` string**
(`m.status`). They are mostly consistent (both use `planned/running/completed/
failed/...`, and both agree on `rolling_back/rolled_back/rollback_degraded`),
**except rollback_failed**: the legacy list vocabulary has `rollback_failed`
(model.go:23) but the `MigrationState` vocabulary does **not** — it only has
`rollback_degraded` (state.go:71,213) and jumps `StateRollback` →
`StateRolledBack`/`StateRollbackDegraded`/`StateNeedsManualIntervention`/
`StateFailed` (state.go:281). So a *failed* rollback in the new state machine is
`StateFailed`, not `rollback_failed`. See Gaps section.

---

## Alur 1 — New Migration → Plan

```
Entry:  GET/POST  /api/migrations            (handler.go:49-50)
        WS         /ws/plan                   (handler.go:52, handlePlanWS:230)
        WS auth:   subprotocol meshium-auth.<token>   (pipeline.ts getWsToken/wsSubprotocols)

  [Draft] --create--> [planned / planning] --WS progress (per category)-->
        packages, configs, services, users, docker ...
                 |
                 |-- WS progress frames (status=progress) -----------+
                 |                                                    |
                 +-- WS terminal (status=complete|failed|interrupted) |
                                                              |
        FE reconcile authority: GET /api/migrations?operationId=   (handler.go:140)
                 |
  Operator decision: SUBMIT PLAN  (wizard step 4)
```

- `operationId`: client UUID, persisted to URL `?op=` + `sessionStorage` before
  submit (4G.1). BE dedups resubmitted `operationId` → same migration for
  recoverable statuses; a recorded `failed` is NOT deduped (handler.go:284-291).
- FE state machine: `idle | connecting | collecting | checking | completed |
  failed | unknown` (web/src/routes/migrations/new/+page.svelte).
- Refresh at step 4 reconciles via `?operationId=`; never re-creates a duplicate.

## Alur 2 — Migration List / History

```
Entry:  GET /api/migrations   (handler.go:49, handleMigrations)
        metadata per row: status (legacy Status*), engine/provider/topology,
        lastOperationId, lastUpdated  (model.go Migration struct)

  FE route: /migrations  (+page.svelte)
  status badges: completed/failed/running/planned/awaiting_cutover/
                 needs_manual_intervention/rollback_degraded  (lines 65-89)
                 + default case for any other string
```

Note: list uses **legacy `Status*`** strings, so it shows `running`/`completed`/
`failed` etc. It does NOT show the granular `MigrationState` pipeline stages
(`discovery`, `live_replication`, `observation`…) — those are a pipeline-detail
concern. `needs_manual_intervention` and `rollback_degraded` DO appear (list has
explicit cases) even though they originate from the newer `MigrationState` set.

## Alur 3 — Migration Detail / Pipeline (Execute)

```
Entry:  GET  /api/pipeline/migrations/:id              (session)   pipeline_handler.go:366,727
        GET  /api/pipeline/migrations/:id/stages
        GET/POST /api/pipeline/migrations/:id/risk
        GET/POST /api/pipeline/migrations/:id/compatibility
        GET/POST /api/pipeline/migrations/:id/health
        GET  /api/pipeline/migrations/:id/replication
        GET  /api/pipeline/migrations/:id/sync
        GET  /api/pipeline/migrations/:id/metrics
        GET  /api/pipeline/migrations/:id/audit
        GET  /api/pipeline/migrations/:id/events?after_seq=&limit=   (replay)
        PUT  /api/pipeline/migrations/:id/config
        WS   /ws/pipeline/   (handlePipelineWS)  — emits WSMessageExtended
        POST /api/pipeline/migrations/:id/actions/{execute|pause|resume|cancel|retry}

  BE state machine (MigrationState, state.go:259-303):
   created → planning → discovery → compatibility_check → risk_assessment →
   backup → provision_target → install_dependencies → initial_sync →
   live_replication → verification → pre_cutover → traffic_switch →
   post_verification → observation → committed
        | (any stage)            → failed → rollback → rolled_back | rollback_degraded
        | (crash/disconnect)     → interrupted → resuming → (resume)
        | (user)                 → paused → resuming
        | (traffic switch done)  → awaiting_cutover  (only operator commit leaves it)
        | (ambiguous topology / unsafe rollback / checkpoint write fail)
                              → needs_manual_intervention  (no auto exit)

  FE route: /migrations/:id/pipeline  (+page.svelte)
  currentState = session.state  (MigrationState string)
  Stepper: 11 wizard steps mapped from currentState (recoverStepFromRecords)
  WS: wsPipelineConnect — exponential backoff reconnect, lastSequence,
      ?after_seq= replay, stale timer  (pipeline.ts:665-754, 806-808)
```

Operator decision: **EXECUTE** (actions/execute). Side-effectful; BE refuses
re-execute from non-startable state + run-lock + state machine. Refresh/reconnect
replays missed events; `metricsStale` blocks irreversible actions.

## Alur 4 — Cutover / Commit / Observation

```
  BE: after traffic_switch stage → StateAwaitingCutover (pipeline.go:427)
      operator cutover  POST .../actions/cutover  → Cutover() (pipeline_handler.go:517)
      post_verification → observation (StateObservation)
      operator commit   POST .../actions/commit   → Commit() (pipeline_handler.go:523)
            - commit while cutover unconfirmed → 409 cutover_not_confirmed (P0-2)
      observation: auto-rollback policy if target writes detected (engine.go)

  FE: CutoverChecklist component (awaiting_cutover evidence)
      Observation timer (resolveObservationStart from stage.startedAt / localStorage)
      SafetyStatePanel: honest banner for awaiting_cutover / observation /
                        needs_manual_intervention / rollback_degraded
      ConfirmationModal copy: "Cutover confirmed" / "Migration committed successfully!"
```

Operator decisions: **CUTOVER** then **COMMIT**. These are distinct; commit is
refused until cutover is confirmed (BE 409). After commit there is no safe
rollback implication surfaced — terminal `committed`.

## Alur 5 — Rollback

```
  BE: POST .../actions/rollback → Rollback() (pipeline.go)
      StateRollback → StateRolledBack | StateRollbackDegraded |
                      StateNeedsManualIntervention | StateFailed
      idempotency: no-op if already StateRollback/StateRolledBack
      stop condition: target had writes during observation window →
                      needs_manual_intervention (NOT auto)

  FE: rollbackPipeline()  (pipeline page)
      rollbackState machine: idle | running | completed | degraded |
                             failed | unknown
      reconcile: mount → loadSession(); action → set 'running' before call;
                catch → stays 'unknown' if BE may still be rolling
      helper: reconcileRollbackState(backendState, current)
              (pipeline.ts:633) — maps rolling_back/rolled_back/rollback_degraded/
              rollback_failed; default → idle (or keeps 'running' if in-flight)
      banner: SafetyStatePanel + rollbackState banner (running/completed/
              degraded/failed/unknown), role=status/alert aria-live
```

Operator decision: **ROLLBACK** (destructive; confirmation modal, `staleData`
guard, actionLoading guard).

---

## Where BE & FE must align (summary)

| Concern | BE authority | FE consumer |
|---|---|---|
| Plan completion | `GET /api/migrations?operationId=` | `?op=` reconcile |
| List status | legacy `Status*` | `m.status` |
| Pipeline state | `MigrationState` string (`session.state`) | `currentState` |
| Live progress | `/ws/pipeline/` `WSMessageExtended` | `wsPipelineConnect` |
| Reconnect recovery | `/events?after_seq=` | `replayEvents` |
| Cutover/commit | state guard + 409 | gated buttons |
| Rollback terminal | `StateRollback*` | `reconcileRollbackState` |

**Key desyncs preview** (full list in investigation doc §Gaps): (a) FE list + the
rollback helper never represent `needs_manual_intervention` (a real terminal-ish
rollback outcome); (b) legacy `rollback_failed` has no `MigrationState` peer;
(c) FE `isTerminalState` omits `rollback_degraded` and `needs_manual_intervention`,
so the rollback button can wrongly show on those states.
