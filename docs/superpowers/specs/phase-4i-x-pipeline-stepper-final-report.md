# Phase 4I.X — Pipeline Stepper 0–11 Flow Audit (Final Report)

> **For agentic workers:** read-only audit. No code was changed. All findings cite
> real files/lines. Severity tags: **[BLOCKING]** breach of correctness / data-safety,
> **[CONFUSION]** operator can misread state, **[COSMETIC]** presentational noise.
> Ambiguity is written as ambiguity — not resolved into a conclusion.

## 1. How the stepper actually works

`/migrations/:id/pipeline` is a **11-step wizard** (`WIZARD_STEPS`, `pipeline.ts:859-871`),
**0-based internally** (`currentStep` 0..10), displayed as `Step {currentStep+1}`
(`PipelineStepper.svelte:60`). User's "step 11" = index 10 = **Finish**. There is no
literal index 11.

Three sources position the stepper, in order of authority:

1. **REST session load** (`loadSession`, `+page:129-214` → `GET /api/pipeline/migrations/:id`,
   `buildSession` at `pipeline_handler.go:1720`). Sets `currentState = session.state` and
   runs `recoverStepFromState()` (`+page:237-263`) then `recoverStepFromRecords()`
   (`+page:265-410`). This is the **cold-load** path.
2. **Live WebSocket** (`startPipeline` at step 6 → `wsPipelineConnect`, `pipeline.ts:676`).
   The WS handler (`+page:757-772`) advances `currentStep` on each `msg.currentState`
   string. This is the **run-time** path. `CurrentState` is emitted as a **string**
   (`state.StateString()`, `pipeline_handler.go:1645`).
3. **localStorage** (`restoreStep`/`restoreMetrics`, `+page:346-358`). A per-browser
   override that can nudge the step forward within `lastCompleted+1`. Presentational only.

**Step 3 (Plan) is the only step with no backend state.** `recoverStepFromState` never
maps a `MigrationState` to step 3; it is entered only by clicking **Next** and "completed"
only when a `planning` *stage* finished. So step 3 is **local/config-driven**, not
state-driven.

**Transitions are not all automatic.** Pre-exec steps (0–5) advance on operator Next or
after a manual action (discovery/compat/risk/dry-run/provision calls). Step 6→7→8→9 advance
on WS `currentState` once the pipeline is running. Cutover (8→9) is gated by
`CutoverChecklist.allChecksPassed` (`CutoverChecklist.svelte:23`). Observation→Finish (9→10)
is gated by a **local observation timer** (`+page:890`) that must elapse
`observationDuration` seconds; if the WS never emits `committed`/`completed`, the timer is
the *sole* path to enabling **Commit**.

## 2. Is the 11-step UI valid/honest vs the backend?

**Mostly honest, with one BLOCKING defect on the cold-load path and several CONFUSION gaps.**

- The 11 steps map to a real backend progression that the canonical pipeline does walk
  (discovery → compat → risk → plan → dry-run → provision → execute → live-replication →
  cutover → observation → commit). The UI does **not** invent fake steps beyond what the
  backend can reach.
- **Authoritative truth is the backend.** The UI only reflects `session.state` /
  `currentState`. It never improvises a state the backend didn't emit (per CLAUDE.md:
  "FE tidak boleh meng-improvise state yang tidak didukung BE"). That rule is honored.
- **The defect (below) breaks the cold-load path but not the run-time path**, because the
  WS path carries `CurrentState` as a string while the REST path carries `session.state`
  as an integer.

## 3. Mismatch / gap list (with severity)

### [BLOCKING] 3.1 — `session.state` integer-vs-string type mismatch
- **Where:** Backend `MigrationSession.State` is `MigrationState` (a Go `int`)
  (`pipeline_models.go:548-551`); no `MarshalJSON` overrides it (`grep` confirmed none), so
  it serializes as `{"state":1}`. The FE declares `state: string` (`pipeline.ts:302`) and
  calls `currentState.toLowerCase()` in `recoverStepFromState` (`+page:239`) and in
  `loadSession` (`+page:175`). `(1).toLowerCase()` **throws** (proven via `node`).
- **Effect:** the primary `try` block in `loadSession` throws, and execution falls through
  to the `catch`, which calls the **legacy** `migrationApi.get('/migrations/:id')` (string
  `status`). So a cold load of an idle migration is positioned only via the fragile legacy
  fallback — the canonical `pipelineApi.getSession` result is effectively discarded when
  `state` is an integer.
- **Live path unaffected:** `CurrentState` is a string (`pipeline_handler.go:1645`), so
  running migrations position correctly. The breakage is specific to the REST loader.
- **Why it matters:** `MigrationState` has 28 values (`state.go`); the legacy
  `migrationApi` endpoint may not return the same field shape/coverage, so the fallback can
  silently mis-position the stepper or lose records. This is a latent production bug on the
  most common path (open the page → see where it is).

### [CONFUSION] 3.2 — `needs_manual_intervention` is FE-terminal but BE-non-terminal
- FE `isTerminalState` (`+page:819-821`) includes `needs_manual_intervention`, so
  `rollbackAvailable` becomes `false` (step≥6). But backend (`state.go`) treats
  `StateNeedsManualIntervention` as **non-terminal** — operator may still retry/resume/
  cancel. No button surfaces those actions. **Operator sees a dead-end where the backend
  allows recovery.** (Confirmed: `transitionTable` allows `StateNeedsManualIntervention`←
  nothing automatic, but `Retry`/`Resume`/`Cancel` handlers accept it.)

### [CONFUSION] 3.3 — `awaiting_cutover` has no step
- `StateAwaitingCutover` (`awaiting_cutover`) is emitted when `trafficSwitchStage` returns
  `ErrAwaitingCutover` (`pipeline.go:427`). The FE has no dedicated step for it; it relies
  on step 8 + a `SafetyStatePanel` banner. If the operator is not on step 8, the banner may
  not be visible next to the active step. Not terminal in either layer, so it's not a
  dead-end — but the stepper position is undefined (falls to `default` → stays at current
  step, `+page:258`).

### [CONFUSION] 3.4 — `failed` has no dedicated UI / banner
- `StateFailed` is non-terminal in both layers (can → rollback). But `recoverStepFromState`
  has no `failed` branch; `recoverStepFromRecords` may mark the step `failed`
  (`+page:376-381`) only if `session.stages` carries the failure. No safety-state banner
  exists for `failed`. On a cold load where stages are absent, a failed run looks like a
  paused step.

### [CONFUSION] 3.5 — `paused` / `interrupted` / `resuming` have minimal UI
- Only `Retry` (if `canRetryState`) / `Resume` buttons appear (`+page:1061-1075`). The
  stepper stays at the current step with no distinct visual treatment of "paused" vs
  "running vs unknown". An operator glancing at the stepper cannot tell a paused migration
  from an in-progress one.

### [CONFUSION] 3.6 — `rolled_back` does not auto-advance the stepper live
- On rollback, `StateRolledBack` is terminal (`state.go`), and `recoverStepFromRecords`
  maps it to step 10 (`+page:393-396`). But the **live WS switch lacks `rolled_back`**
  (`+page:757-772`), so during a live run the stepper does **not** jump to Finish when the
  backend rolls back — it relies on the next refresh / replay. The `rollbackState='completed'`
  overlay + `SafetyStatePanel` keep the operator informed, but the *step index* lags until
  reload.

### [CONFUSION] 3.7 — Commit gated by a local timer, not by backend confirmation
- `ObservationPanel` enables **Commit** only after the local `observationDuration` timer
  elapses and sets `stepStatus[9]='completed'` (`ObservationPanel.svelte:82`). If the WS
  never emits `committed`/`completed`, the timer alone unlocks Commit. This means the
  operator can commit even when the backend has not independently confirmed a healthy
  observation window. (Fail-closed exists on the *backend* commit path — `pipeline.go`
  commit → `StateNeedsManualIntervention` on failure — but the FE unlock is timer-based.)

### [CONFUSION] 3.8 — `409 cutover_not_confirmed` not surfaced distinctly
- `handlePipelineAction` returns `409 {error:"cutover_not_confirmed"}`
  (`pipeline_handler.go:533-536`). `confirmCutover` (`+page:841-853`) does not special-case
  this; the operator gets a generic toast. No guidance to fix the checklist.

### [COSMETIC] 3.9 — `'archived'` is not a real state
- `recoverStepFromState` / `isTerminalState` reference `'archived'`, but `StateArchived`
  does not exist in `state.go` (enum has 28 states, none archived). Dead branch — never
  matches. Harmless but misleading to a reader.

### [COSMETIC] 3.10 — Observation panel shows hardcoded fake metrics
- `ObservationPanel.svelte:14-16,71-78` hardcodes `Error Rate 0.1%`, `P95 Latency 120ms`,
  and a simulated `trafficA/B` split. Purely visual; not from any API. Misleading if an
  operator reads them as live values (they are not).

### [COSMETIC] 3.11 — No Cancel button in this page
- `pipelineApi.cancel` and `handleCancel` exist, but **no button renders Cancel**
  (`+page` action bar). `StateCancelled` is reachable only elsewhere. Not a mismatch, but an
  unreachable action the UI implies exists.

### [COSMETIC] 3.12 — Dead `rollback_failed` reconcile branch
- `reconcileRollbackState` (`pipeline.ts:625`) maps `rollback_failed → 'failed'`, but the
  backend **never emits** `rollback_failed` (recovery.go final states are `StateRolledBack`
  / `StateRollbackDegraded` / `StateFailed`). Dead branch.

### [AMBIGUITY] 3.13 — Snapshot/Transferring/Applying/Verifying legacy states
- `state.go` defines `StateSnapshot` / `StateTransferring` / `StateApplying` / `StateVerifying`
  but `recoverStepFromState` has no branch for them — they fall to `default` (stay at step 0
  on cold load). **Ambiguity:** it is unclear whether the canonical pipeline can actually
  emit these states today (the `SyncEngine` is noted as "not wired" in `pipeline.go`). If it
  cannot, these are dead states and the gap is cosmetic; if it can, a cold load mid-sync
  would mis-position. Could not confirm reachability without running a live sync.

### [AMBIGUITY] 3.14 — `restoreStep` localStorage can override backend-derived step
- `+page:346-358` nudges `currentStep` forward if localStorage records a higher completed
  step within `lastCompleted+1`. **Ambiguity:** on a multi-device / shared-migration
  scenario this could show a step ahead of what the backend reports for a *different*
  operator. Scoped to per-browser; severity unclear without a multi-operator test.

## 4. Can the stepper be trusted as the canonical representation?

- **During a live run:** **yes, with caveats.** The WS `CurrentState` is a backend string;
  the stepper tracks it faithfully except for `rolled_back` (3.6) and the overlay-only
  rollback rendering (3.5/3.6). The caveat is that rollback is shown as an *overlay*, not as
  a stepper step — so a migrating operator can see step 8 (Cutover) while a rollback is in
  flight. That is intentional but can read as "stuck on cutover."
- **On cold load / refresh:** **no, not reliably.** The `session.state` integer-vs-string
  mismatch (3.1) makes the canonical loader throw, and the stepper is positioned via the
  legacy `migrationApi` fallback. Until 3.1 is fixed, a freshly opened pipeline page may be
  mis-positioned or lose record-derived detail.
- **On non-happy-path states:** **partially.** `needs_manual_intervention` (3.2),
  `awaiting_cutover` (3.3), `failed` (3.4), `paused`/`interrupted` (3.5) lack dedicated,
  unambiguous stepper treatment; the operator must rely on banners or absence of buttons to
  infer them.

**Headline:** The stepper is an honest *reflection* of the backend for the happy path and
for live runs, but the cold-load type mismatch (3.1) is a correctness bug that breaks the
most common entry path, and three non-terminal/anomaly states (3.2–3.5) are under-
rendered — so the stepper should **not** yet be treated as the authoritative source of
truth by an operator. The backend `MigrationState` remains the source of truth (per
CLAUDE.md), and the FE correctly does not improvise states; the gap is in *rendering
coverage*, not in *state invention*.

## 5. Recommendations (do NOT implement — audit only)

1. **Fix the `session.state` type on the wire.** Either add `MarshalJSON`/`UnmarshalJSON`
   to `MigrationState` (emit the string form `StateString()` over REST, matching the WS
   `CurrentState`), **or** make `loadSession` coerce: `String(state)` when the value is a
   number. Highest priority — it is the one BLOCKING defect (3.1).
2. **Reconcile `needs_manual_intervention` terminality.** If BE intends operator recovery,
   FE `isTerminalState` should exclude it and surface Retry/Resume/Cancel; if BE intends a
   hard stop, `IsTerminal` should include it. Pick one. (3.2)
3. **Add explicit stepper treatment for anomaly states:** `awaiting_cutover`, `failed`,
   `paused`, `interrupted`, `rolling_back`. A distinct status chip/icon (not just banners)
   so the step index never reads as "in progress" when it is actually paused/failed/
   rolling back. (3.3–3.6)
4. **Make Commit gated by backend confirmation**, not solely the local timer — or at least
   show the WS `committed`/`completed` as the primary unlock and the timer as a fallback.
   (3.7)
5. **Surface `409 cutover_not_confirmed`** with a specific message pointing at the
   checklist. (3.8)
6. **Remove dead branches:** `'archived'`, `rollback_failed` reconcile arm; fix/confirm
   reachability of the legacy snapshot states (3.9, 3.12, 3.13).
7. **Replace hardcoded ObservationPanel metrics** with real values or clearly label them as
   simulated. (3.10)
8. **Expose Cancel** if `handleCancel` is meant to be reachable from this page; otherwise
   remove the unused `pipelineApi.cancel` to avoid implying an action that isn't there.
   (3.11)
9. **Audit `restoreStep` localStorage scope** for multi-operator correctness. (3.14)

## 6. Verification basis

- `MigrationState` JSON proven by isolated `go run`: emits `{"state":1}` (int, no custom
  marshaling).
- `(1).toLowerCase()` proven to throw under `node`.
- `IsTerminal`, `StateString`, `transitionTable` read directly from `state.go`.
- `recoverStepFromState`, `recoverStepFromRecords`, `loadSession`, action bar read
  line-by-line from `+page.svelte`.
- `reconcileRollbackState`, `WIZARD_STEPS`, `MigrationSession` interface read from
  `pipeline.ts`.
- `buildSession`, `handlePipelineAction`, `CurrentState` emission read from
  `pipeline_handler.go`; rollback final states from `recovery.go`.
- Live-wire behavior inferred (no server running) from `CurrentState = state.StateString()`
  — a string — confirming the type mismatch is REST-only.
