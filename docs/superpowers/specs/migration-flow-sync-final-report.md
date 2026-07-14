# Phase 4F.X — Migration Flow Sync: Final Report

**PART 6 — Report** · 2026-07-14

Maps: `migration-flow-map.md`. Investigation + gaps: `migration-flow-sync-investigation.md`.
Pattern: investigate → map → verify → report.

This audit adds **no features**. It verifies that what the backend claims,
persists, and emits is faithfully reflected by the frontend across the whole
migration journey. **Backend remains the source of truth; the FE only reflects
backend state and must show ambiguity as ambiguity.**

---

## 1. Ringkasan alur end-to-end (hasil map)

Five flows, all reconciled to backend authority:

1. **New Migration → Plan** — WS `/ws/plan` progress + terminal; completion
   authority `GET /api/migrations?operationId=`; `operationId` dedup on BE.
2. **Migration List / History** — `GET /api/migrations` (legacy `Status*`), 8+
   status badges incl. cross-vocabulary `awaiting_cutover`/`needs_manual_intervention`/
   `rollback_degraded`.
3. **Pipeline / Execute** — `GET /api/pipeline/migrations/:id` (`session.state` =
   `MigrationState` string); live `/ws/pipeline/` `WSMessageExtended`; reconnect
   replay via `/events?after_seq=`.
4. **Cutover / Commit / Observation** — `StateAwaitingCutover` → `StateObservation`
   → `StateCommitted`; commit refused (409) until cutover confirmed; observation
   timer survives reload.
5. **Rollback** — `StateRollback → RolledBack | RollbackDegraded |
   NeedsManualIntervention | Failed`; idempotent no-op when already rolled back;
   FE `rollbackState` machine + banner.

Critical structural fact: the backend carries **two parallel status vocabularies**
— legacy `Status*` (plan/list/rollback-sub) and the 20-state `MigrationState`
(pipeline). They agree on shared names; the FE consumes each where appropriate.

---

## 2. Status sinkronisasi per alur

| Flow | Sync status | Notes |
|---|---|---|
| Plan & wizard | ✅ Aligned (reference) | `operationId` dedup + REST reconcile; `unknown` honest |
| List & history | ✅ Aligned | shows cross-vocabulary states; coarse running stage (G3) |
| Pipeline & progress | ✅ Strongly aligned | `currentState` single authority; replay + stale guard |
| Cutover/Commit/Obs | ✅ Aligned | BE 409 guard; FE `staleData` gate; no false safe-rollback |
| Rollback & recovery | ⚠️ Partial | `needs_manual_intervention` invisible in FE (G1); wrong terminal set (G2) |

---

## 3. Strength (BE & FE already sinkron — reference patterns)

- **Plan wizard** (4G.1): `operationId` persisted before submit, BE dedup, REST
  reconcile as completion authority, honest `unknown`. Model for the rest.
- **Pipeline progress**: `wsPipelineConnect` resilient transport (exponential
  backoff, `lastSequence`, `?after_seq=` replay, stale timer) + `currentState`
  reconciled from `session.state`. FE never improvises a pipeline state.
- **Cutover/Commit**: BE `StateAwaitingCutover` is non-terminal by design
  (`state.go:162`); commit guarded by structured 409 `cutover_not_confirmed`
  (`pipeline_handler.go:527`). FE opens the confirmation modal only when live data
  is fresh (`staleData` guard, `+page.svelte:821`). No misleading "safe rollback"
  after commit.
- **Idempotency / duplicate-prevention**: plan (`operationId`), pipeline actions
  (`Idempotency-Key` header → audit replay, `pipeline_handler.go:499-512`),
  execute (run-lock + state machine), rollback (no-op from `RolledBack`). Refresh
  and reconnect never duplicate a side-effectful action.

---

## 4. Gaps (dengan severity)

### G1 — FE rollback never surfaces `needs_manual_intervention` — **blocking correctness**
BE can end a rollback in `StateNeedsManualIntervention` (`pipeline.go:908`; test
`force_transition_p0_test.go:49`). FE `reconcileRollbackState` (`pipeline.ts:633`)
has no case for it → defaults to `idle`; the rollback banner
(`+page.svelte:1157-1179`) has no `needs_manual` branch → **operator sees nothing**.
Worse, `rollbackAvailable` (`+page.svelte:1057`) may still present a live rollback
button. An unsafe-stopped rollback is silently invisible.

### G2 — FE `isTerminalState` omits `rollback_degraded` & `needs_manual_intervention` — **blocking correctness**
`isTerminalState` (`+page.svelte:815-817`) = `{completed, committed, archived,
rolled_back, cancelled}`. BE `IsTerminal()` (`state.go:164`) includes
`rollback_degraded` and `needs_manual_intervention`. So `rollbackAvailable`
(`!isTerminalState(currentState) && currentStep >= 6`) can wrongly offer rollback
on an already-terminal-ish migration — an action the BE will refuse or that is
unsafe to offer.

### G3 — List page hides granular running stage — **operator confusion**
List badge shows `running` for every in-flight `MigrationState`
(`+page.svelte:84`); cannot distinguish `live_replication` from `observation`. By
design (granularity lives on detail page) but reduces operator evidence at a
glance.

### G4 — Route-path mismatch in the brief — **cosmetic**
Brief said `/api/pipelinemigrations/:id/...`; reality is `/api/pipeline/migrations/:id/...`
(`pipeline_handler.go:126`). FE matches BE. This report corrects the brief.

### G5 — Two parallel state vocabularies; `rollback_failed` has no `MigrationState` peer — **operator confusion**
Legacy `rollback_failed` (`model.go:23`) vs `MigrationState` which uses
`rollback_degraded` and collapses a fully-failed rollback to `StateFailed`
(string `"failed"`). FE rollback helper keeps a `rollback_failed` branch
(`pipeline.ts:633`) that is effectively dead for new-state rollbacks; an operator
reading `failed` cannot tell "migration failed" from "rollback failed".

### G6 — No live e2e evidence captured — **audit completeness**
Verification was static (code-level), not a running server. No screenshots/logs
persisted. Map and gaps are evidence-backed by file:line reads, but a live
walkthrough (PART 5) was not executed.

---

## 5. Rekomendasi

### FE changes (recommended, small, parity-only)
- **G1 fix**: add `case 'needs_manual_intervention': return 'failed'` (or a new
  `manual` state) to `reconcileRollbackState` (`pipeline.ts:633`), and add a
  `{:else if rollbackState === 'manual'}` banner branch in
  `+page.svelte:1157-1179` with `role="alert"` ("Rollback stopped — needs manual
  intervention"). Mirror the `SafetyStatePanel` banner which already renders for
  `needs_manual_intervention`.
- **G2 fix**: extend `isTerminalState` (`+page.svelte:815`) to
  `{completed, committed, archived, rolled_back, rollback_degraded,
  needs_manual_intervention, cancelled}` so `rollbackAvailable` is correct.
- **G5 fix (clarity)**: rename the FE `failed` rollback branch to be explicit, or
  document that `StateFailed` from a rollback context means "rollback failed".
  Low priority — value agreement holds.

### BE changes
- **None required for parity.** All gaps are FE rendering/terminal-set gaps over
  states the backend already authoritatively holds. Do **not** add endpoints.
- Optional (not blocking): the legacy `rollback_failed` vs `StateFailed` split
  (G5) could be unified later, but it is cosmetic and out of scope for this audit.

### G3 / G4 / G6
- G3: optionally show `session.state`-equivalent granular stage on the list row
  (defer; operator-confusion only).
- G4: brief corrected by this report.
- G6: run a live walkthrough before Phase 5 if a server is available; persist
  screenshots/audit logs as the evidence path.

---

## 6. Bukti (tests / references)

- **Unit tests (existing, passing)**: `rollback-reconcile.test.ts` (8 cases,
  4G.2) pins `reconcileRollbackState` mappings — these are exactly what G1/G2
  must extend. `reconcile.test.ts` (7 cases, 4G.1) pins plan reconcile.
- **Backend state-machine tests**: `state_test.go`, `force_transition_p0_test.go`
  (asserts `rollbackTerminalState(ErrUnsafeTopology)=NeedsManualIntervention`),
  `cutover_p0_test.go` (asserts `AwaitingCutover`/`NeedsManualIntervention`
  transitions). These prove G1's BE state is real and reachable.
- **Static evidence (this audit)**: every claim cites `file:line` in
  `migration-flow-map.md` and `migration-flow-sync-investigation.md`.
- **Verification run**: `npm run check` 0 errors, `npx vitest run` 65 passing,
  `go build ./...` OK (pre-conditions from 4G.2; no 4FX code change yet).
- **Live walkthrough (PART 5)**: not executed (no server). Recommended before
  Phase 5 sign-off.

---

## Release recommendation

**Conditional — do NOT enter Phase 5 yet.** The flows are aligned except two
**blocking-correctness** gaps (G1, G2) where a rolled-back-to-`needs_manual` or
`rollback_degraded` migration is either invisible or wrongly offers a further
rollback in the FE. Both are small FE-only fixes (extend `reconcileRollbackState`
+ add a banner branch; extend `isTerminalState`). Once G1+G2 are fixed and the
existing 4G.1/4G.2 tests extended, the minimal-downtime migration journey is
BE↔FE synchronized and Phase 5 may proceed.

G3/G4/G5/G6 are non-blocking (operator confusion / cosmetic / completeness).
