# Phase 4F.X — Migration Flow Sync Investigation (BE + FE)

**PART 2 — Per-flow BE+FE detail · PART 4 — Gaps/Desync** · 2026-07-14

Map: `migration-flow-map.md`. Report: `migration-flow-sync-final-report.md`.

Evidence basis: direct read of `internal/mod/migration/*.go` (state.go,
pipeline.go, pipeline_handler.go, model.go, handler.go, engine.go, cutover_*.go)
and `web/src/**` (routes/migrations/new, routes/migrations/+page.svelte,
routes/migrations/[id]/pipeline/+page.svelte, lib/api/pipeline.ts,
lib/api/migrations.ts, lib/utils/format.ts). Every claim cites file:line.

---

## A. New Migration wizard (plan)

### Backend
- **Draft/config REST**: `GET/POST /api/migrations` (`handler.go:49-50`);
  `handleMigrations` lists/creates. Plan config persists on the `migrations` row.
- **Plan WS**: `GET /ws/plan` (`handler.go:52`, `handlePlanWS` at `handler.go:230`).
  Auth via subprotocol `meshium-auth.<token>` (`pipeline.ts` `getWsToken` /
  `wsSubprotocols`; minted at the command boundary in the handler).
- **Event types**: progress per category (`packages, configs, services, users,
  docker`, …) with `WSMessage{Step, Status:"progress"|"complete", Value}`; terminal
  events `complete` / `failed` / `interrupted`. The id-less "Migration plan
  created" frame is demoted to `progress` (4G.1) — only an id-bearing frame is
  terminal.
- **Idempotency / `operationId`**: client UUID in `req.OperationID`
  (`handler.go:284`). BE dedups a resubmitted `operationId` to the **same**
  migration for recoverable statuses (`planned/running/interrupted`); a recorded
  `failed` is deliberately **not** deduped so a retry creates a fresh plan
  (`handler.go:284-291`).
- **Completion authority**: `GET /api/migrations?operationId=` →
  `GetMigrationByOperationID` (`handler.go:140-141`). BE is authoritative; the
  terminal WS frame only triggers the FE to re-ask by `operationId`.

### Frontend
- **Wizard**: `web/src/routes/migrations/new/+page.svelte` (Steps 1-4).
- **operationId/draftId/migrationId**: `operationId` generated client-side,
  persisted to URL `?op=` + `sessionStorage` **before** first submit (4G.1).
  `migrationId` learned from the reconcile response.
- **FE state machine**: `idle | connecting | collecting | checking | completed |
  failed | unknown`. The `unknown` state renders a banner + link to Migration
  History (4G.1) — never a fake success or endless spinner.
- **WS handling**: listens to `/ws/plan` progress/terminal; on terminal/close
  calls `GET /api/migrations?operationId=` to reconcile; refresh at step 4
  re-runs the reconcile; retry re-submits the same `operationId` (deduped by BE).

### Sync verdict: **aligned** (reference). No change needed.

---

## B. Migration list / history

### Backend
- **Endpoint**: `GET /api/migrations` (`handler.go:49`). Filter/sort by
  `createdAt`, `status`, `engine` available via query (`handleMigrations`).
- **Metadata sent**: `status` (legacy `Status*` string, model.go:17-25),
  engine/provider/topology, `lastOperationId`, `lastUpdated` (model.go `Migration`).

### Frontend
- **Route**: `web/src/routes/migrations/+page.svelte`.
- **Status rendering**: `statusBadge`/`statusDot` (lines 65-89) handle
  `completed, failed, running, planned, interrupted, awaiting_cutover,
  needs_manual_intervention, rollback_degraded` + a `default` case. Label via
  generic `formatLabel` (`lib/utils/format.ts:81` — splits on `_`, Title-cases).
- **operationId/trace**: list does **not** display `operationId` or a per-run
  trace; shows `status`, health, progress, rollback availability (line 354 copy).

### Sync verdict: **mostly aligned**. The list correctly shows the cross-vocabulary
states `awaiting_cutover`, `needs_manual_intervention`, `rollback_degraded`
despite them originating from the `MigrationState` set. Gap: list never surfaces
`running`'s granular pipeline stage (`live_replication`, `observation`, …) — by
design (those are pipeline-detail), but an operator scanning the list cannot tell
"running in observation" from "running in discovery". See Gaps G3 (operator
confusion).

---

## C. Migration detail / Pipeline (Execute)

### Backend
- **Endpoints** (all under `/api/pipeline/migrations/:id`, dispatched by
  `handlePipelineMigrationByID` `pipeline_handler.go:366`, subroutes
  `stages/cancel/pause/resume/rollback/commit/cutover/risk/compatibility/health/
  replication/sync/metrics/audit/events/config/export` — note: actual prefix is
  `/api/pipeline/migrations/...`, **not** `/api/pipelinemigrations/...` as the
  audit brief assumed).
- **WS pipeline**: `GET /ws/pipeline/` (`handlePipelineWS`). Emits
  `WSMessageExtended` (FE interface `pipeline.ts:322-`) with fields: `step, status,
  value, error, stage, stageIndex, stageTotal, progress, bytesDone, bytesTotal,
  speedBytes, eta, replicationLag, healthScore, riskScore, riskClass,
  currentState, cpuUsagePercent, ramUsedBytes, ramTotalBytes, diskUsedPercent,
  networkRxBytesSec, networkTxBytesSec, sequence, timestamp`. `extendWSMessage`
  stamps `CurrentState` from the migration's `MigrationState.StateString()`
  (`pipeline_handler.go:1640-1645,1740`).
- **Event replay**: `GET /api/pipeline/migrations/:id/events?after_seq=&limit=`
  (`pipeline_handler.go:1381-1385`) — used by FE after WS reconnect.
- **BE state machine**: `MigrationState` (state.go), 20 states, transition table
  at `state.go:259-303`.
- **BE invariants**:
  - `AwaitingCutover` appears after the `traffic_switch` stage completes and the
    operator must confirm — set in `pipeline.go:427` (`SetMigrationStateContext`,
    `StateAwaitingCutover`). Not terminal (`state.go:162`); only an explicit commit
    leaves it (`state.go:301`).
  - `NeedsManualIntervention` appears on ambiguous topology / unsafe rollback /
    checkpoint-write failure / commit rejection (`pipeline.go:487,668,908`;
    `state.go:84-88`). Terminal-ish: no automatic transition out
    (`state.go:302`).
  - `RollbackDegraded` appears when a rollback step fails but others succeed
    (`pipeline.go:892-909` — honest terminal, any failed step ⇒ degraded, unsafe
    topology ⇒ needs_manual).
  - Completed vs Failed vs Degraded decided in the run loop
    (`engine.go:643-652` fail-closed to `NeedsManualIntervention`; never force
    `Committed`).

### Frontend
- **Components**: `MigrationHeader, BottomTabs, MigrationEvidencePanel,
  SafetyStatePanel, MigrationLogAudit, CutoverChecklist, ConfirmationModal,
  CompatibilityChecklist, ObservationPanel` (imported in pipeline +page.svelte).
- **FE state machine**: `currentState = session.state` (`+page.svelte:175,205`).
  `recoverStepFromRecords()` maps `currentState` → 11 `stepStatuses`
  (`+page.svelte:283-328`); step 8 = cutover, step 9 = observation, step 10 =
  commit. `metricsStale` (no live frame within 15s) blocks irreversible actions
  (`+page.svelte:1118,1146` `staleData` guard).
- **WS pipeline handling**: `wsPipelineConnect` (`pipeline.ts:665-754`) —
  exponential-backoff reconnect, `lastSequence` tracking, `?after_seq=` replay via
  `replayEvents` (`pipeline.ts:806-808`), `stale` and `replaying` connection
  states (`WSConnectionState`, `pipeline.ts:611`). After reconnect FE replays
  missed events and re-derives `currentState`.

### Sync verdict: **strongly aligned**. The `currentState` field is the single
authority; FE never improvises a state. `needs_manual_intervention`,
`awaiting_cutover`, `rollback_degraded`, `observation` all flow through. Gaps are
narrow (see Gaps G1, G2, G4).

---

## D. Cutover / Commit / Observation

### Backend
- **CutoverEngine / FreezeManager** (`cutover_*.go`): Freeze source read-only +
  fence token; `WaitForCatchUp` (lag 0); verify target health; TrafficSwitchEngine
  flip (DNS/proxy/LB/app config); promote target / demote source; unfreeze target;
  observe window with auto-rollback policy (`engine.go`).
- **States**: `AwaitingCutover` (`pipeline.go:427`), `Observation`
  (`StateObservation`), `Committed` (`StateCommitted`), `NeedsManualIntervention`
  (`pipeline.go:487`).
- **Endpoints**: `POST .../actions/cutover` → `Cutover()` (`pipeline_handler.go:517`);
  `POST .../actions/commit` → `Commit()` (`pipeline_handler.go:523`). Commit while
  cutover unconfirmed → structured **409 `cutover_not_confirmed`** (P0-2,
  `pipeline_handler.go:527-535`).

### Frontend
- **AwaitingCutover**: `CutoverChecklist` component + `SafetyStatePanel` banner
  (`+page.svelte:1186-1196` note: banner renders for `awaiting_cutover / observing
  / needs_manual_intervention / rollback_degraded`).
- **Observing**: observation timer (`resolveObservationStart` prefers stage
  `post_cutover_observation.startedAt`, falls back to localStorage keyed by
  migration id — survives reload; `+page.svelte:848-885`).
- **Completed**: step 10 marked complete when `currentState ∈
  {committed,completed,archived}` (`+page.svelte:319`).
- **NeedsManualIntervention**: shown via `SafetyStatePanel` banner + list badge.
- **ConfirmationModal copy**: cutover → "Cutover confirmed"; commit → "Migration
  committed successfully!" (`+page.svelte:843,899`). `openConfirmation` refuses to
  open while `staleData` (`+page.svelte:821-824`) — no action on stale snapshot.
- **Manual vs automatic**: `autoCutoverConfigured` prop (`config.autoCutover`)
  passed to `SafetyStatePanel`; UI consumes, does not decide.

### Sync verdict: **aligned**. The BE 409 guard is the authority; FE gates on
`staleData` and confirms via modal. After commit, no misleading "safe rollback"
implication is shown (terminal `committed`).

---

## E. Rollback

### Backend
- **Flow** (`pipeline.go` `Rollback`): reverse traffic to source, demote target /
  source back to primary, unfreeze source. `StateRollback → StateRolledBack |
  StateRollbackDegraded | StateNeedsManualIntervention | StateFailed`
  (`state.go:281`).
- **Stop condition**: if the observation window saw writes to target →
  `NeedsManualIntervention`, NOT auto (`pipeline.go:892-909`; `force_transition
  _p0_test.go:49` `rollbackTerminalState(ErrUnsafeTopology)=NeedsManualIntervention`).
- **Idempotency**: no-op when already `StateRollback`/`StateRolledBack`
  (state machine forbids re-entering `Rollback` from `RolledBack`, `state.go:282`).
- **Endpoint**: `POST .../actions/rollback` (`pipeline_handler.go:543`).

### Frontend
- **State machine**: `idle | running | completed | degraded | failed | unknown`
  (`reconcileRollbackState` helper, `pipeline.ts:633`).
- **Reconcile model**: mount → `loadSession()`; action → set `'running'` **before**
  the call (`+page.svelte:930`); catch → keep `unknown` (BE may still be rolling,
  `+page.svelte:947-955`).
- **Banner**: `rollbackState` banner renders `running/completed/degraded/failed/
  unknown` with `role=status`/`role=alert` aria-live (`+page.svelte:1157-1179`).

### Sync verdict: **partially aligned — two real gaps** (G1, G2 below). The FE
`reconcileRollbackState` only knows `rolling_back / rolled_back / rollback_degraded
/ rollback_failed` and defaults everything else to `idle` (or keeps `running`).
It has **no branch for `needs_manual_intervention`** — so when BE lands in
`NeedsManualIntervention` during/after a rollback, FE renders `idle` (silent,
no banner). And the legacy `rollback_failed` has no `MigrationState` peer, so the
`failed` branch in the FE helper is effectively unreachable for new-state rollbacks
(BE uses `StateFailed`, whose string is `failed` — actually reachable via the
default only if current≠running). Detailed in Gaps.

---

## Gaps / Desync

> Severity: **blocking correctness** (operator could take a wrong/dangerous
> action or miss a real state) · **operator confusion** (misleading but not
> dangerous) · **cosmetic**.

### G1 — FE rollback never surfaces `needs_manual_intervention`
- **Flow**: rollback (and cutover-stop).
- **BE state/contract**: `Rollback()` can terminate in `StateNeedsManualIntervention`
  (`pipeline.go:908`; `force_transition_p0_test.go:49`). `session.state` will carry
  `"needs_manual_intervention"`.
- **FE behaviour**: `reconcileRollbackState` (`pipeline.ts:633`) has no case for it;
  default → `idle` (or keeps `running` if in-flight). The rollback banner
  (`+page.svelte:1157-1179`) only has `running/completed/degraded/failed/unknown`
  branches, so `needs_manual_intervention` shows `idle` = **no banner, no evidence**.
- **Type**: missing state in FE.
- **Severity**: **blocking correctness** — an operator who triggered a rollback
  that the BE stopped as unsafe gets *no* visible signal; `rollbackAvailable`
  (`+page.svelte:1057`) may still show a live rollback button.

### G2 — FE `isTerminalState` omits `rollback_degraded` & `needs_manual_intervention`
- **Flow**: rollback / pipeline.
- **BE state/contract**: `StateRollbackDegraded` and `StateNeedsManualIntervention`
  are terminal-ish (`state.go:164,283,302`); `IsTerminal()` returns true for
  `rollback_degraded` and `needs_manual_intervention` but **not** for `rolled_back`
  (BE treats `rolled_back` as terminal too via the map, `state.go:164`).
- **FE behaviour**: `isTerminalState` (`+page.svelte:815-817`) = `{completed,
  committed, archived, rolled_back, cancelled}`. `rollback_degraded` and
  `needs_manual_intervention` are **not** terminal → `rollbackAvailable`
  (`!isTerminalState(currentState) && currentStep >= 6`, `+page.svelte:1057`) can
  wrongly offer rollback on an already-degraded or manual-intervention migration.
- **Type**: overclaimed capability in FE (offers action BE will reject) +
  missing state.
- **Severity**: **blocking correctness** — button offers a rollback the BE will
  refuse (or is unsafe to offer) on a terminal-ish state.

### G3 — List page hides granular running stage
- **Flow**: list.
- **BE state/contract**: legacy `Status*` list API sends `"running"` for all
  in-flight `MigrationState`s (created→observation). The granular stage is only in
  `session.state` on the detail page.
- **FE behaviour**: list badge shows `running` pulse for any running state
  (`+page.svelte:84`); no drill-down of `live_replication` vs `observation`.
- **Type**: operator confusion (no operator-evidence gap, just coarse).
- **Severity**: **operator confusion**.

### G4 — Route-path mismatch (audit brief vs reality)
- **Flow**: pipeline (all sub-endpoints).
- **BE contract**: actual prefix `/api/pipeline/migrations/:id/...`
  (`pipeline_handler.go:126-135,366`).
- **FE behaviour**: FE calls `/pipeline/migrations/${id}/...` (`pipeline.ts:543+`)
  — **matches BE**. The brief's `/api/pipelinemigrations/:id/` is stale.
- **Type**: documentation/assumption drift (not a code defect).
- **Severity**: cosmetic (this doc corrects the brief).

### G5 — Two parallel state vocabularies (legacy `Status*` vs `MigrationState`)
- **Flow**: plan/list vs pipeline/rollback.
- **BE contract**: `model.go` `Status*` (legacy) and `state.go` `MigrationState`
  coexist; rollback sub-status `rollback_failed` exists only in legacy
  (`model.go:23`), while `MigrationState` uses `rollback_degraded` and collapses a
  fully-failed rollback to `StateFailed` (string `"failed"`, `state.go:210`) — no
  `rollback_failed` peer.
- **FE behaviour**: list uses legacy strings; pipeline uses `MigrationState`
  strings; rollback helper mixes both (`rollback_failed` branch, `pipeline.ts:633`).
- **Type**: misinterpreted/overlapping contract.
- **Severity**: **operator confusion** (low) — values happen to agree on the
  shared names, but the `rollback_failed` branch in the FE is effectively dead for
  new-state rollbacks, and an operator reading `failed` for a rolled-back-failed
  migration cannot distinguish "migration failed" from "rollback failed".

### G6 — No live e2e evidence path captured
- **Flow**: all.
- **BE/FE behaviour**: investigation is static (code-level). No server was stood
  up; no screenshots/logs persisted.
- **Type**: missing recoverability evidence.
- **Severity**: operator confusion (audit completeness) — PART 5 walkthrough was
  code-level, not live.

---

## Checklist answers (PART 3 consolidated)

| Category | BE has state? | FE reflects? | Naming same? | BE state missing in FE? | FE state w/o BE? | FE fakes state? |
|---|---|---|---|---|---|---|
| Plan & wizard | yes (`operationId`, `Status*`) | yes | yes | no | no | no (unknown used) |
| List & summary | yes (legacy `Status*`) | yes (8+ cases + default) | yes | granular stage hidden (G3) | no | no |
| Pipeline & progress | yes (`MigrationState`) | yes (`currentState`) | yes | no | no | no |
| Cutover/Commit/Obs | yes | yes (banner, gated) | yes | no | no | no |
| Rollback & recovery | yes (`StateRollback*`, `NeedsManual`) | partial | partial | **`needs_manual` (G1)** | `unknown` is FE-only (acceptable) | no |
| NeedsManual/Degraded | yes | **partial (G1,G2)** | yes | `needs_manual` in rollback FE | no | no |
| Unknown/ambiguity | n/a (BE authoritative) | yes (`unknown`) | n/a | — | `unknown` | no (honest) |
| Dup-prevention/idempotency | yes (`operationId`, idempotency-key, run-lock, no-op rollback) | yes (guards + reconcile) | yes | no | no | no |
| Refresh/reconnect | yes (`events?after_seq=`, `?operationId=`) | yes (replay + reconcile) | yes | no | no | no |
