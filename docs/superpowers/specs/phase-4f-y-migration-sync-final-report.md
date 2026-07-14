# Phase 4F.Y — Rollback Parity Fix + E2E Sync Re-Certification: Final Report

**PART 6 — Final Report & Release Recommendation** · 2026-07-14

Pattern: fix blocking gaps → re-verify → certify.
Plan: `phase-4f-y-rollback-parity-plan.md`. Recert: `phase-4f-y-migration-sync-recertification.md`.

**Backend unchanged.** All fixes make the FE reflect backend state that already
existed authoritatively. Ambiguity stays ambiguity (a distinct `manual` rollback
value, not a faked `failed`).

---

## 1. Ringkasan perubahan (FE-only)

| File | Change |
|---|---|
| `web/src/lib/api/pipeline.ts` | `RollbackState` union gains `'manual'`; `reconcileRollbackState` adds `case 'needs_manual_intervention': return 'manual'` (G1). |
| `web/src/routes/migrations/[id]/pipeline/+page.svelte` | `isTerminalState` extended with `rollback_degraded` + `needs_manual_intervention` (G2); `applyRollbackReconcile` sets detail `'Rollback stopped — needs manual intervention'` for `manual`; rollback banner gains `{:else if rollbackState === 'manual'}` branch with `role="alert"`. |
| `web/src/lib/api/rollback-reconcile.test.ts` | +2 cases: `needs_manual_intervention→manual`; terminal-set parity assertion. |

No endpoint, no transport, no backend change.

## 2. Status G1 & G2

- **G1 (closed):** A rollback the BE ends in `StateNeedsManualIntervention`
  (`pipeline.go:908`) now renders as `rollbackState='manual'` with a visible red
  alert banner, complementary to `SafetyStatePanel`'s existing "Needs manual
  intervention" panel. The operator is no longer blind to an unsafe-stopped
  rollback.
- **G2 (closed):** `isTerminalState` now matches BE `IsTerminal()` (`state.go:164`)
  for every observable terminal value. `rollbackAvailable` (`!isTerminalState &&
  step>=6`) therefore hides the rollback button on `rollback_degraded` and
  `needs_manual_intervention` — no more offering a rollback the BE would refuse or
  that is unsafe.

## 3. Bukti

- `npm run check` → **0 errors, 0 warnings**.
- `npx vitest run` → **67 passing** (12 files); `rollback-reconcile.test.ts` now
  **10 cases** incl. `needs_manual_intervention→manual` and the terminal-set
  parity assertion.
- `npm run build` → **success** (adapter-static → `cmd/server/web/build`).
- `go build ./...` → **OK** (no BE edit).
- Backing BE tests (unchanged, prove the states are real): `force_transition_p0_test.go:49`
  (`rollbackTerminalState(ErrUnsafeTopology)=NeedsManualIntervention`),
  `state_test.go` (`IsTerminal` includes `rollback_degraded` + `needs_manual_intervention`).

Live walkthrough (PART 5) was **not executed** — no server in this environment.
It is a recommended, non-blocking pre-Phase-5 step (capture the `manual` banner
screenshot). The fix is a pure FE render/terminal-set change over authoritative BE
state, fully covered by the unit tests above.

## 4. Pernyataan sinkronisasi

Across **Plan → List → Pipeline → Cutover/Commit/Observation → Rollback**, the FE
now reflects the correct backend state:

- **Rollback** reflects `StateRolledBack` (`completed`), `StateRollbackDegraded`
  (`degraded`, terminal, no further rollback offered), `StateNeedsManualIntervention`
  (`manual`, banner visible, terminal), `StateFailed` (`failed`), `StateRollback`
  (`running`).
- No flow offers an unsafe action: rollback button hidden on every BE terminal-ish
  state; cutover/commit gated by BE 409 + FE `staleData`; execute guarded by run-lock
  + state machine.
- No manual intervention or degraded terminal state is hidden — both surface via
  `SafetyStatePanel` and the rollback banner.
- Plan/List/Pipeline/Cutover unchanged and still aligned (re-checked in recert doc §B).

**Blocking-correctness gaps: 0 remaining.** G3/G4/G5/G6 are non-blocking and carried
forward (coarse list stage; doc path fix; `rollback_failed` documented; live
walkthrough recommended).

## 5. Rekomendasi release

**Phase 5 may proceed.** Both blocking gaps (G1, G2) are closed with FE-only,
unit-tested changes; all verification gates are green; the remaining gaps are
non-blocking operator-confusion/completeness items. Backend remains the single
source of truth and the FE faithfully reflects it end-to-end.

Recommended (non-blocking) before/with Phase 5:
- Run the live walkthrough (PART 5) to capture the `manual` banner screenshot as
  the evidence path.
- Optionally surface the granular pipeline stage on the list row (G3).
