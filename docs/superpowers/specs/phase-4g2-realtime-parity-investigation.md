# Phase 4G.2 — Realtime Operation Parity Sweep (dry-run, compatibility, execute, rollback)

**PART 1 — Investigation** · 2026-07-14

Reference model: **Phase 4G.1** (Plan Wizard) — explicit FE state machine,
client `operationId` persisted before submit, REST reconcile as completion
authority, terminal WS frame treated as `progress` not authority, explicit
`unknown` for ambiguity, no infinite spinner.

Audit scope: the four realtime operation flows that still carry drift risk after
the 4G.1 plan-wizard fix:
- **dry-run** (`/ws/dryrun/{id}`)
- **compatibility** (`/ws/compatibility/{id}`)
- **execute** (`/ws/pipeline/{id}`)
- **rollback** (`/ws/pipeline/{id}/rollback`)

---

## 1. dry-run

### A. Trigger model
- **Screen/component:** `routes/migrations/[id]/pipeline/+page.svelte` → `runDryRun()`.
- **REST:** none for starting. Result persisted server-side by the executor into a
  `migration_steps` row (`action='dryrun'`, `data` = JSON result). Reconciled
  via `loadSession()` → `session.dryRun` (`pipeline_handler.go:1762`).
- **WS:** `wsDryRun(migrationId)` → `GET /ws/dryrun/{id}` (`handleDryRunWS`,
  `handler.go:425`).
- **Identifier:** `migrationId` only (the route param). **No** per-run `operationId`,
  no idempotency token, no draft/session id for the run. `migrationId` is created
  when the migration row is created (in 4G.1, with its `operation_id`) and is
  stable for the lifetime of the migration.
- **When created/persisted:** `migrationId` exists at page load (URL param); the dry-run
  *result* is persisted by `DryRun()` at completion. No run-scoped identity is minted.

### B. FE completion model (current)
- **Success =** WS umbrella `dryrun` frame `status==='complete'`, **OR** the
  `onclose` handler calling `loadSession()` and marking `stepStatuses[4]='completed'`.
- **Failure =** any `dryrun` `status==='error'` frame sets `errored=true` → on
  close, `stepStatuses[4]='failed'` + toast.
- **Single WS terminal frame trusted?** Partly. The in-flight `complete` frame is
  trusted for the caption; but the `onclose` path reconciles via REST — the 4G.1
  pattern. The *interim* `complete` is still frame-trusted, not REST-confirmed.
- **Honest close/error?** Yes-ish: `onclose` calls `loadSession()` (REST
  reconcile) rather than toast-only; `onerror` marks `failed`.

### C. Recovery model (current)
- **Refresh while running:** `onMount → loadSession()` restores `session.dryRun`
  from the persisted `migration_steps` row. Result shows if the run finished. **Good.**
- **WS close mid-run:** `onclose` reconciles via `loadSession()`. **Good.**
- **Timeout:** no explicit WS timeout in the FE; relies on `onerror`/`onclose`.
- **Backend finished, FE missed terminal event:** `onclose` reconcile covers it. **Good.**
- **State after refresh:** `stepStatuses[4]` re-derived in `recoverStepFromRecords()`
  from persisted step data → **recoverable, not reset to empty. Good.**

### D. Duplicate-safety (current)
- **Double submit:** `actionLoading` guard — "Run Dry Run" button is
  `disabled={actionLoading}`. **Guarded at FE.**
- **Retry idempotency:** N/A — dry-run has **no** run token; each click re-runs
  `DryRun` against the same migration. Dry-run is **side-effect free** (read-only
  diff), so a duplicate run is low-risk, but the FE does not distinguish "viewing a
  fresh run" vs "viewing the persisted result".
- **Destructive double-send:** N/A (read-only).

### E. User-visible truthfulness (current)
- **running / reconnecting / checking / completed / failed / unknown?**
  No explicit state machine. Shows "Running…"/"Completed" captions + step
  `running`/`completed`/`failed` badges. **No `unknown` state, no `reconnecting`
  label** for dry-run — if `loadSession()` fails after close, it silently marks
  `failed` ("Dry run result could not be loaded") even when the backend may have
  succeeded. Ambiguity is collapsed to `failed`.
- **Ambiguity shown or hidden by spinner?** Hidden — a failed `loadSession` becomes
  `failed`, not `unknown`.
- **Recovery path offered?** Partial — a reload shows the result if persisted.

### F. Backend contract sufficiency (current)
- **Sufficient?** Mostly. `GET /api/pipeline/session/{id}` returns the persisted
  `dryRun` result; `handleDryRunWS` itself persists nothing but `DryRun()` stores
  the result. **Gap:** `handleDryRunWS` has **no dedup** — a reconnect re-runs
  `DryRun` from scratch (acceptable for a read-only op, but wasteful and not
  self-describing).
- **Minimal API/WS addition needed?** None strictly required. A per-run token is
  unnecessary for a side-effect-free op. The only real gap is the FE `unknown` state.
- **List/detail/status for reconcile?** Yes — `loadSession` + `migration_steps`.
- **Sequence/replay / final-status lookup?** No sequence needed; the persisted result
  is the final-status lookup.

---

## 2. compatibility

### A. Trigger model
- **Screen:** `runCompatibilityCheck()` in the pipeline page.
- **REST:** `POST /api/pipeline/compatibility/{id}` returns stored results without
  re-running when `?refresh!=true` and stored rows exist (`pipeline_handler.go:858`).
  Result persisted to `verification_result` rows.
- **WS:** `wsCompatibility(migrationId)` → `GET /ws/compatibility/{id}`
  (`handleCompatibilityWS`, `pipeline_handler.go:900`). WS carries **only progress**;
  result persisted server-side and reconciled via `loadSession()`.
- **Identifier:** `migrationId` only. No per-run token.
- **Created/persisted:** `migrationId` in URL; results persisted to
  `verification_result` rows by `CheckName`.

### B. FE completion model (current)
- **Success =** `onclose` → `loadSession()` → if any `critical && !passed`,
  `stepStatuses[1]='failed'` else `'completed'`. The `compat` `complete` frame is
  **not** trusted for the verdict — it only sets the caption.
- **Failure =** `errored` (any `compat` `status==='error'` frame) or `loadSession()`
  failure on close.
- **Single WS terminal frame trusted?** No — explicitly reconciles via REST. **This
  is the 4G.1 model already.**
- **Honest close/error?** Yes.

### C. Recovery model (current)
- **Refresh while running:** `loadSession()` restores `session.compatibilityResults`
  from `verification_result`. **Good.**
- **WS close / backend finished, FE missed event:** `onclose` reconcile. **Good.**
- **State after refresh:** re-derived from persisted verification rows. **Good.**

### D. Duplicate-safety (current)
- **Double submit:** `disabled={actionLoading}` guard. **Guarded.**
- **Retry idempotency:** REST dedup returns stored results instead of re-running. The
  WS path (`handleCompatibilityWS`) re-runs and **appends** fresh `verification_result`
  rows (no upsert key) — a reconnect appends duplicate rows. The FE
  `getStoredCompatibilityResults` returns all rows, so a reconnect can show duplicate
  checks. Side-effect free; cosmetic.
- **Destructive double-send:** N/A.

### E. User-visible truthfulness (current)
- **State machine?** No explicit `idle/running/.../unknown`. Captions + step badges.
  **No `unknown`** — a failed `loadSession` on close becomes `failed`.
- **Ambiguity hidden?** Yes — collapsed to `failed`.
- **Recovery path?** Partial.

### F. Backend contract sufficiency (current)
- **Sufficient.** `loadSession` + persisted `verification_result` + REST dedup.
  **Gap:** `handleCompatibilityWS` appends duplicate rows on reconnect (no upsert
  key). FE `unknown` state missing.

---

## 3. execute

### A. Trigger model
- **Screen:** "Start Pipeline" → `startPipeline()` → `wsPipelineConnect(migrationId)`.
- **REST:** none for starting; `loadSession()` reconciles the full state machine.
- **WS:** `wsPipelineConnect` → `GET /ws/pipeline/{id}` (`handlePipelineWS`) —
  the **resilient** WS from 4G slice 3E: exponential-backoff reconnect,
  `lastSequence`, `?after_seq=` replay, stale-timer. Strongest realtime transport
  in the codebase.
- **Identifier:** `migrationId` + the pipeline's **internal run-lock**
  (`tryAcquire`/`release`) + a server-side **state machine** (`StateCreated` → …
  → `StateCommitted`).
- **Created/persisted:** `migrationId` in URL; run-lock + state machine persisted
  server-side.

### B. FE completion model (current)
- **Success =** a live frame with `currentState` in `committed|completed` → marks
  all steps `completed`, `pipelineRunning=false`.
- **Failure =** any frame `status==='error'` → `pipelineRunning=false` + toast.
- **Single WS terminal frame trusted?** No — the resilient WS replays history on
  reconnect (`streamPipelineHistory` when not startable), so a missed frame is
  recovered. **4G.1 model, fully realized.**
- **Honest close/error?** Yes — `wsConnectionState`
  (`connecting|connected|reconnecting|failed`) drives `metricsStale` and a
  "WebSocket connection lost" toast on `failed`.

### C. Recovery model (current)
- **Refresh while running:** `onMount → loadSession()` restores `currentState` +
  steps from the persisted state machine. **Good.** WS reconnects and replays.
- **WS close / backend finished, FE missed event:** `handlePipelineWS` checks
  `currentMigrationState`; if not `StateCreated|StateResuming`, it **streams history
  instead of re-executing** — a refresh/reconnect NEVER relaunches a
  running/finished migration. **Exemplary.**
- **State after refresh:** fully recoverable from the state machine. **Good.**

### D. Duplicate-safety (current)
- **Double submit:** `startPipeline` only reachable from a startable state;
  `handlePipelineWS` refuses `Execute` unless `state==StateCreated|StateResuming`;
  `pipeline.Execute` also `tryAcquire`s a run-lock. **Doubly guarded server-side.**
- **Retry idempotency:** state machine + run-lock make duplicate execution
  impossible.
- **Destructive double-send:** N/A (forward op; covered by state machine).

### E. User-visible truthfulness (current)
- **Distinguishes running/reconnecting/checking/completed/failed/unknown?**
  Yes — `wsConnectionState` exposes `reconnecting` and `failed`; step states cover
  `running/completed/failed`; `metricsStale` shows "reconnecting, data may be
  stale". **This flow is the reference.**
- **Ambiguity hidden?** No — `failed` WS shows "WebSocket connection lost".
- **Recovery path?** Yes — reconnect automatic; user can also refresh.

### F. Backend contract sufficiency (current)
- **Sufficient and exemplary.** No change needed.

---

## 4. rollback

### A. Trigger model
- **Screen:** "Rollback" → `openConfirmation('rollback')` → `runConfirmedAction`
  → `rollbackPipeline()` → `pipelineApi.rollbackMigration(migrationId)`.
- **REST:** `POST /api/migrations/{id}/rollback` → `handleRollback` →
  `pipeline.Rollback(...)`. **Rollback is a pure REST call, NOT a raw WS in the
  current pipeline page** (the `wsRollback` helper exists but is unused here).
- **WS:** `wsPipelineConnect(migrationId)` stays open and streams rollback progress
  frames (`step:'rollback'`), but the *trigger* is REST.
- **Identifier:** `migrationId` + server-side **state machine**
  (`StateRollback|StateRolledBack` and the `rolling_back`/`rolled_back` step states).
- **Created/persisted:** `migrationId` in URL; rollback state persisted.

### B. FE completion model (current)
- **Success =** `rollbackPipeline()` resolves → `loadSession()` sets the UI from the
  new state; toast "Rollback initiated". The WS `rollback` `complete` frame is
  progress, not authority.
- **Failure =** `pipelineApi.rollbackMigration` throws → toast "Rollback failed".
- **Single WS terminal frame trusted?** No — REST `Rollback` + `loadSession`
  reconcile. **4G.1 model.**
- **Honest close/error?** Yes (REST error → toast).

### C. Recovery model (current)
- **Refresh while rolling back:** `loadSession()` restores `currentState`
  (`rolling_back`/`rolled_back`). **Good.**
- **WS close / backend finished, FE missed event:** `loadSession()` on the next
  action reconciles; rollback state persisted server-side. **Good.**
- **State after refresh:** recoverable from state machine. **Good.**

### D. Duplicate-safety (current) — highest-risk flow
- **Double submit:** `runConfirmedAction` has `if (actionLoading) return;` guard +
  confirmation modal. **FE-guarded.**
- **Retry idempotency:** `pipeline.Rollback` early-returns `nil` if
  `currentState == StateRollback || StateRolledBack` — **idempotent server-side.** A
  second `POST /rollback` is a no-op. **Good.**
- **Destructive double-send:** **prevented** by the state guard + FE guard. However —
  `Rollback` only no-ops for `rolling_back`/`rolled_back`. From a `failed` or
  `interrupted` state a second rollback **re-runs** (intended for retry), correct,
  but the FE gives **no explicit "rollback running / completed / degraded / failed /
  unknown" distinction** — it shows a single toast and relies on the operator
  noticing the state change in `loadSession`.

### E. User-visible truthfulness (current)
- **Distinguishes rollback running/completed/degraded/failed/unknown?** **No.** No
  rollback-specific state machine in the FE. The user sees a toast and infers status
  from overall `currentState`/`stepStatuses`. A **degraded** rollback (some steps
  rolled back, some failed — `RollbackDegraded` exists in the backend state
  model) is **not surfaced distinctly** in the FE. Ambiguity after a dropped
  connection is collapsed to whatever `loadSession` returns, with no `unknown`
  fallback if the session load itself fails.
- **Ambiguity hidden?** Partly — degraded hidden; a failed session reload silent.
- **Recovery path?** Weak — no explicit CTA for "rollback status unknown, check
  history".

### F. Backend contract sufficiency (current)
- **Sufficient for duplicate-prevention** (state guard). **Insufficient for FE
  truthfulness** — the FE does not expose the `rolling_back` / `rolled_back` /
  `rollback_degraded` / `failed` vocabulary the backend state machine already
  tracks. The contract exists; the FE just doesn't render it.

---

## Root-cause summary per flow

| Flow | Drift root cause | Severity |
|---|---|---|
| dry-run | FE has no `unknown` state; `loadSession` failure silently becomes `failed`; no per-run identity (acceptable: read-only) | Low |
| compatibility | Same FE gaps as dry-run; `handleCompatibilityWS` appends duplicate verification rows on reconnect (read-only, low) | Low |
| execute | Already 4G.1-compliant (resilient WS + state machine + run-lock). No change needed. | None |
| rollback | Backend tracks `rolling_back`/`rolled_back`/`rollback_degraded`/`failed` but the FE renders none distinctly; no `unknown` fallback on session-load failure; high-risk destructive op deserves an explicit state machine | **Medium** |

## Which flows are already safe enough
- **execute** — reference implementation; no change.
- **compatibility** — REST dedup + persisted results + onclose reconcile; only the FE
  `unknown` gap + a backend append-duplicate cosmetic issue remain.
- **dry-run** — onclose reconcile + persisted result; only the FE `unknown` gap.

## Which flows need change
- **rollback (FE):** add an explicit rollback state machine
  (`idle → running → completed | degraded | failed | unknown`) reconciled from the
  backend state on `loadSession` and after the REST call; surface `degraded`
  distinctly; add `unknown` on session-load failure; keep the destructive
  double-submit + backend `StateRollback` no-op guards (already present).
- **dry-run / compatibility (FE):** add `unknown` state on `loadSession` failure
  after WS close (instead of collapsing to `failed`); minor.
- **compatibility (backend, optional):** `handleCompatibilityWS` should upsert
  verification rows keyed by `(migration_id, target, verification_type)` to avoid
  append-duplicates on reconnect.

## Backend contract change needed?
- **No new endpoint.** `loadSession` already exposes the persisted dry-run,
  compatibility, and pipeline/rollback state. The only backend tweak is the
  *optional* compat upsert (cosmetic dedup). All truthfulness gaps are **FE
  rendering** gaps, not contract gaps — consistent with rule 2 (reuse existing
  authoritative resource) and rule 7 (minimal contract additions only if needed).

## Stop-condition check (PART 8)
- Does any flow lack an authoritative backend state to reconcile against? **No** — every
  flow persists its result/state; `loadSession` is the reconciliation authority for
  all four.
- Can duplicate-prevention be guaranteed for side-effectful actions? **Yes** —
  execute (run-lock + state machine) and rollback (state guard no-op) are both
  server-side guaranteed.
- Can refresh recovery be implemented truthfully without backend changes? **Yes** — the
  contract already supports it; the FE only needs to render `unknown` and the
  rollback vocabulary.
- Would the FE be forced to fake success/failure? **No.**

→ **No stop condition triggered.** Proceed to PART 2 (decision grid).
