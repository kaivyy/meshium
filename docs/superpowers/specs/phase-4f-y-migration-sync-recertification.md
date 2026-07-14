# Phase 4F.Y — Migration Flow Sync Re-Certification

**PART 4 — Mini Re-Certification (end-to-end)** · 2026-07-14

After the FE-only fixes for G1 + G2, re-verify the whole journey is BE↔FE
synchronized. Plan: `phase-4f-y-rollback-parity-plan.md`. Final report:
`phase-4f-y-migration-sync-final-report.md`.

---

## A. Re-run ringkasan alur (unchanged five flows)

1. **Plan wizard** — `operationId` + REST reconcile (`GET /api/migrations?operationId=`).
2. **List/history** — legacy `Status*` + cross-vocab badges (`awaiting_cutover`,
   `needs_manual_intervention`, `rollback_degraded`).
3. **Pipeline/execute** — `MigrationState` + resilient `/ws/pipeline/` (reconnect,
   `?after_seq=` replay, stale timer).
4. **Cutover/commit/observation** — `AwaitingCutover` → `Observation` → `Committed`;
   commit guarded by 409 `cutover_not_confirmed`.
5. **Rollback** — `StateRollback*` + FE `rollbackState` machine.

These are unchanged from 4F.X; the fixes only touch rollback FE rendering + the
terminal set.

---

## B. Sinkronisasi BE↔FE setelah fix

### Rollback (the fixed flow)
- `StateRolledBack` (`"rolled_back"`) → FE `rollbackState = 'completed'` ✅
- `StateRollbackDegraded` (`"rollback_degraded"`) → FE `'degraded'`, **terminal** ✅
  (`isTerminalState` now includes `rollback_degraded` → `rollbackAvailable=false`).
- `StateNeedsManualIntervention` (`"needs_manual_intervention"`) → FE `'manual'`,
  **banner visible** ✅ (G1 closed) and **terminal** ✅ (G2 closed → no rollback
  offered).
- `StateFailed` (`"failed"`, fully-failed rollback) → FE `'failed'` ✅.
- `StateRollback` (`"rolling_back"`, in-flight) → FE `'running'` ✅.

### `isTerminalState` vs `IsTerminal()` parity
BE `IsTerminal()` (`state.go:164`): `committed, rolled_back, rollback_degraded,
cancelled, needs_manual_intervention`.
FE `isTerminalState` (after fix, `+page.svelte:815`): `completed, committed,
archived, rolled_back, rollback_degraded, needs_manual_intervention, cancelled`.

Match for every FE-observable terminal value:
`completed`(=committed), `committed`, `archived`, `rolled_back`, `rollback_degraded`,
`needs_manual_intervention`, `cancelled`. **No BE terminal state is still treated
as non-terminal by the FE.** (`archived` is FE-only spare; harmless.)

`rollbackAvailable` (`+page.svelte:1057` = `!isTerminalState(currentState) &&
currentStep >= 6`) now correctly hides the rollback button on `rollback_degraded`
and `needs_manual_intervention`.

### Plan / List / Pipeline / Cutover-Commit-Obs — re-check (no change)
- FE never improvises a pipeline state; `currentState` is the single authority
  (`session.state`).
- Commit still protected by BE 409 `cutover_not_confirmed`
  (`pipeline_handler.go:527`); FE `staleData` gate blocks actions on stale data.
- Plan wizard still uses `operationId` + REST reconcile (4G.1).
- List badges still render the cross-vocabulary states.

No regressions introduced by the rollback edit (gates: `npm run check` 0 errors,
`npx vitest run` 67 passing, `npm run build` OK, `go build ./...` OK).

---

## C. Gap re-evaluasi

| Gap | Status | Note |
|---|---|---|
| **G1** | **CLOSED** | `reconcileRollbackState` maps `needs_manual_intervention→'manual'`; banner branch added (`+page.svelte:1157+`); `SafetyStatePanel` already shows the red alert, now complemented not duplicated. |
| **G2** | **CLOSED** | `isTerminalState` extended with `rollback_degraded` + `needs_manual_intervention`; matches `IsTerminal()`; `rollbackAvailable` no longer offers rollback on terminal-ish states. |
| G3 | open (non-blocking) | Coarse list `running` stage — optional future enhancement. |
| G4 | resolved (doc) | Route path corrected in 4F.X map (`/api/pipeline/migrations/...`). |
| G5 | clarified (doc) | `rollback_failed` branch documented as the fully-failed-rollback mapping; no code change needed. |
| G6 | open (non-blocking) | Live walkthrough recommended before Phase 5 (PART 5), not a code blocker. |

Blocking-correctness gaps: **0 remaining.**

---

## D. Live walkthrough (PART 5) — not executed
No server was stood up in this environment. The code-level evidence path
(file:line in this doc + the plan) plus the passing unit tests
(`rollback-reconcile.test.ts` 10 cases covering every mapping incl. `manual`) are
the certification basis. A live walkthrough remains recommended before Phase 5 to
capture screenshots of the `manual` banner, but it is not a code blocker — the
fixes are pure FE render/terminal-set changes over authoritative BE state.
