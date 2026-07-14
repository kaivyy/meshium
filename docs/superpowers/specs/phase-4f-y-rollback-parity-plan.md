# Phase 4F.Y — Rollback Parity Fix + E2E Sync Re-Certification (Plan)

**PART 2 — Implementation Plan** · 2026-07-14

Closes the two blocking-correctness gaps (G1, G2) from Phase 4F.X. **FE-only.**
Backend contract unchanged; every state the FE will render already exists
authoritatively in the backend (`MigrationState`, `state.go`).

Recap + contract: `migration-flow-sync-investigation.md` §G1/§G2; `state.go:164`
(`IsTerminal` includes `rollback_degraded` + `needs_manual_intervention`);
`pipeline.go:908` (rollback → `StateNeedsManualIntervention`).

---

## Recap — GAP & KONTRAK

**G1 — FE rollback never surfaces `needs_manual_intervention`** (blocking).
- BE: rollback can terminate in `StateNeedsManualIntervention` (`pipeline.go:908`);
  `IsTerminal()` treats it terminal-ish (`state.go:164`).
- FE: `reconcileRollbackState` (`pipeline.ts:633`) has no case → defaults to
  `idle`; rollback banner (`+page.svelte:1157-1179`) has no branch → operator
  sees nothing; `rollbackAvailable` may still show a live rollback button.

**G2 — FE `isTerminalState` omits `rollback_degraded` & `needs_manual_intervention`**
(blocking).
- BE: `IsTerminal()` (`state.go:164`) includes both.
- FE: `isTerminalState` (`+page.svelte:815-817`) =
  `{completed, committed, archived, rolled_back, cancelled}` → `rollbackAvailable`
  (`!isTerminalState(currentState) && step>=6`, `+page.svelte:1057`) can wrongly
  offer rollback on a terminal-ish state.

**Kontrak:** no new endpoint/backend change; fixes only make FE reflect existing
BE state; ambiguity stays ambiguity (we add a distinct `manual` rollback value,
not a faked `failed`).

---

## A. Fix G1 — surface `needs_manual_intervention` in rollback FE

### A.1 `reconcileRollbackState` (`web/src/lib/api/pipeline.ts:633`)
Add an explicit case (others unchanged — `rolling_back→running`,
`rolled_back→completed`, `rollback_degraded→degraded`, `rollback_failed→failed`):
```ts
case 'needs_manual_intervention': return 'manual';
```
Extend the exported `RollbackState` union to include `'manual'`:
```ts
export type RollbackState = 'idle' | 'running' | 'completed' | 'degraded' | 'failed' | 'unknown' | 'manual';
```

### A.2 Rollback banner (`web/src/routes/migrations/[id]/pipeline/+page.svelte:1157-1179`)
Add a branch after the `unknown` branch:
```svelte
{:else if rollbackState === 'manual'}
  <div class="flex items-center gap-1.5 text-xs text-error" role="alert">
    <svg ...><path d="M12 9v4"/><path d="M12 17h.01"/></svg>
    {rollbackStateDetail}
  </div>
```
Copy set in `applyRollbackReconcile` (`+page.svelte:267`):
```ts
next === 'manual' ? 'Rollback stopped — needs manual intervention' :
```

### A.3 Consistency with `SafetyStatePanel`
`SafetyStatePanel` already renders a red "Needs manual intervention" banner for
`currentState === 'needs_manual_intervention'` (`SafetyStatePanel.svelte:93-119`).
Our new rollback banner is a *complementary* signal next to the rollback control,
not a duplicate: same `role="alert"`, same `text-error` tone, copy echoes
"needs manual intervention". With G2 fixed, `rollbackAvailable` becomes `false` in
that state, so the panel's "Rollback available" badge (`SafetyStatePanel.svelte:112`)
correctly disappears — no contradiction.

## B. Fix G2 — terminal-state parity

### B.1 `isTerminalState` (`+page.svelte:815-817`)
```ts
function isTerminalState(state: string): boolean {
  return ['completed', 'committed', 'archived', 'rolled_back', 'rollback_degraded', 'needs_manual_intervention', 'cancelled'].includes(state.toLowerCase());
}
```
This now matches `IsTerminal()` (`state.go:164`) for every value the FE can
observe (`rollback_degraded`, `needs_manual_intervention` added; `StateRolledBack`
string is `rolled_back`, already present).

### B.2 `rollbackAvailable` (`+page.svelte:1057`)
No code change needed — it reads `!isTerminalState(currentState)`. After B.1 it
correctly returns `false` for `rollback_degraded` and `needs_manual_intervention`,
so the rollback button no longer appears on those terminal-ish states. Verify
behaviour on `completed`/`committed`/`rolled_back` is unchanged (still terminal →
no rollback offered).

## C. G5 clarification (non-blocking, document only)
The legacy `case 'rollback_failed': return 'failed'` branch stays; it is now
documented as the mapping for a fully-failed rollback (BE `StateFailed` string
`"failed"`). No simplification required.

---

## Tests (PART 3)
- Extend `web/src/lib/api/rollback-reconcile.test.ts`:
  - `needs_manual_intervention` → `manual` (G1).
  - `rollback_degraded` still → `degraded` and is terminal-shaped.
  - unchanged mappings (`rolling_back`, `rolled_back`, `rollback_failed`) still hold.
- `isTerminalState` is inline in `+page.svelte` (not exported); parity verified by
  `npm run check` (compiles) + code review against `state.go:164`. No refactor to
  extract it (YAGNI).

## Verification gates
- `npm run check` → 0 errors.
- `npx vitest run` → all green (8 + new cases).
- `npm run build` → success.
- `go build ./...` → unchanged (no BE edit).

## Out of scope
- G3 (coarse list stage), G4 (path naming doc), G6 (live walkthrough) — non-blocking,
  carried forward unchanged (see recert doc PART 4 §C).
