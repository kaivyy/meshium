# Phase 4G.2 — Realtime Operation Parity Sweep: Final Report

**PART 7 — Final Report** · 2026-07-14

Workstream: `investigate → design decision → implement → test → report`.
Scopes the four realtime operation flows still judged at drift risk after 4G.1:
**dry-run, compatibility, execute, rollback**. Reference model: **Phase 4G.1**
(Plan Wizard) — backend authoritative, FE never trusts a single WS frame, terminal
WS demoted to `progress`, explicit `unknown` for ambiguity, no infinite spinner.

Supporting docs:
- Investigation (PART 1, per-flow A–F audit): `phase-4g2-realtime-parity-investigation.md`
- Decision grid (PART 2): `phase-4g2-realtime-parity-decision.md`

---

## 1. Audited flows

| Flow | In-flight transport | Completion authority (pre-4G.2) | Reconcile path |
|---|---|---|---|
| **dry-run** | bare `wsDryRun` (progress-only) | hybrid: WS `complete` frame + `onclose`→`loadSession()` REST | `loadSession()` persisted `dryRun` |
| **compatibility** | bare `wsCompatibility` (progress-only) | REST `loadSession().compatibilityResults` | `loadSession()` + backend REST dedup |
| **execute** | resilient `wsPipelineConnect` (reconnect + replay) | resilient WS + `loadSession()` state machine + run-lock | `loadSession()` + `handlePipelineWS` history stream |
| **rollback** | pure REST `pipelineApi.rollbackMigration` (no WS) | REST + `loadSession()` state | `loadSession()` `state` enum |

The central finding (PART 2) reshaped the work: the four flows do **not** use the
*bare* `wsExecute`/`wsRollback` helpers the audit assumed. They already reconcile
via `loadSession()` (REST) and `wsPipelineConnect` (resilient). So the drift is
**narrow** — FE truthfulness gaps, not missing transport/reconcile.

---

## 2. Root cause per flow

- **dry-run** — `onclose` handler forced `stepStatuses[4] = 'failed'` even when the
  only problem was a *reconcile* failure (backend could not be confirmed). A
  recovered/ambiguous result was rendered as a definite `failed` — a false negative
  that violates the ambiguity rule.
- **compatibility** — same shape: `onclose` forced `stepStatuses[1] = 'failed'`.
  Also, `handleCompatibilityWS` *appends* a `verification_result` row on every WS
  connect, so a reconnect duplicates rows (cosmetic, read-only — not a
  truthfulness blocker).
- **execute** — **no root-cause defect.** Already the reference implementation:
  exponential-backoff reconnect + `?after_seq=` replay + stale timer; backend
  refuses re-execute from a non-startable state; run-lock (`tryAcquire`) + state
  machine; `unknown`-equivalent surfaced via `wsConnectionState`
  (`reconnecting`/`failed`) and `metricsStale`. Marked safe enough.
- **rollback** — backend already tracks the full vocabulary
  (`rolling_back`/`rolled_back`/`rollback_degraded`/`rollback_failed`), but the FE
  collapsed it into a single one-shot toast. An operator could not distinguish a
  *completed* rollback from a *degraded* (some steps unreverted) or *failed* one,
  and a failed REST call could leave the UI silent (no evidence of an in-flight
  backend rollback). This is the one real fix.

---

## 3. Which flows were safe enough (no change)

- **execute** — reference; no change.

## 4. Which flows changed

- **dry-run** — added explicit `unknown` on `loadSession()` failure after `onclose`
  (was `failed`). No backend change.
- **compatibility** — same `unknown` fix. The verification-row upsert (dedup) is
  optional/cosmetic — deferred (PART 2 decision; rule 7 — minimal additions only).
- **rollback** — added an explicit FE rollback state machine reconciled from the
  authoritative backend `state` on mount and after the REST call. Renders
  `running`/`completed`/`degraded`/`failed`/`unknown` distinctly instead of a toast.
  No backend change.

---

## 5. Backend contract changed?

**No.** Every flow already persists its result/state in the contract
(`loadSession()` carries `dryRun`, `compatibilityResults`, and the migration
`state` enum including all four rollback terminal shapes). 4G.2 is FE-only: render
states the backend already authoritatively holds. The only *optional* backend
change is the compatibility verification-row upsert — deferred as cosmetic
(PART 8 stop condition not triggered; see §11).

---

## 6. Chosen authority per flow

| Flow | Authority |
|---|---|
| dry-run | REST `loadSession().dryRun` (WS stays progress-only) |
| compatibility | REST `loadSession().compatibilityResults` (WS progress-only) |
| execute | resilient WS `wsPipelineConnect` + REST `loadSession()` state machine |
| rollback | REST `rollbackMigration` + `loadSession().state` |

All four keep the backend as the single completion authority (4G.1 rule: FE
reconciles, never trusts one terminal frame).

## 7. Chosen recovery model per flow

| Flow | Recovery model |
|---|---|
| dry-run | existing `onclose`→`loadSession()` reconcile; **+ explicit `unknown`** when `loadSession()` fails (no infinite spinner — `actionLoading` always clears in `finally`) |
| compatibility | same `loadSession()` reconcile + **explicit `unknown`** on failure |
| execute | **unchanged** — `onMount`→`loadSession()`; `handlePipelineWS` streams history; auto-reconnect with replay |
| rollback | **new FE state machine** reconciled from `loadSession().state` on mount + after REST; `unknown` on session-load failure; in-flight `running` set *before* the call so a mid-call refresh reconciles to `running`, never fake-completed |

## 8. Duplicate-prevention per flow

| Flow | Side-effectful? | Prevention |
|---|---|---|
| dry-run | No (read-only) | FE `actionLoading` double-submit guard; no per-run id needed (cheap to re-run, result recovered) |
| compatibility | No (read-only) | same guard; backend REST `POST` dedups returned stored results; WS reconnect row-append is cosmetic |
| execute | **Yes** | double-guarded server-side: backend refuses from non-startable state + `tryAcquire` run-lock + full state machine; refresh reconnect replays, never re-executes |
| rollback | **Yes (destructive)** | FE `actionLoading` + confirmation modal guard + set `running` before call; backend no-ops when `StateRollback`/`StateRolledBack` (idempotent re-entry). A retry from `failed`/`interrupted` is an intended re-attempt, not a duplicate |

No flow can create a duplicate action on refresh/retry/reconnect.

---

## 9. Test results

### Frontend
- `npm run check` — **0 errors, 0 warnings**.
- `npx vitest run` — **65 tests passing** across 12 files. New:
  `src/lib/api/rollback-reconcile.test.ts` (**8 tests**) pinning every rollback
  vocabulary mapping (`rolling_back→running`, `rolled_back→completed`,
  `rollback_degraded→degraded`, `rollback_failed→failed`, unrecognized→idle/unknown,
  null/empty handling). Pre-existing 57 tests unaffected.
- `npm run build` — **success** (adapter-static, wrote `cmd/server/web/build`).

### Backend
- `go build ./...` — **OK** (no Go change in 4G.2; contract unchanged).
- Backend test run for `internal/mod/migration/` unchanged in scope; the rollback
  no-op idempotency (`StateRollback`/`StateRolledBack`) and execute run-lock
  (`tryAcquire`) were already covered by prior phases and required no 4G.2 edit.

### Manual truthfulness matrix (executed against the code, not a live server)
- dry-run `onclose` with `loadSession()` success → step 4 `completed`.
- dry-run `onclose` with `loadSession()` failure → step 4 `unknown` + toast
  "Dry run status unknown — check results below or reload" (was `failed`).
- compat `onclose` with `loadSession()` failure → step 1 `unknown` (was `failed`).
- rollback `StateRollback` + refresh → banner "Rollback in progress…" (`running`).
- rollback `StateRolledBack` + refresh → banner "Rollback completed" (`completed`).
- rollback `StateRollbackDegraded` → "completed with some steps that could not be
  reverted" (`degraded`).
- rollback `StateRollbackFailed` → "Rollback failed" alert (`failed`).
- rollback REST throw + backend still rolling back → `unknown` + "check the
  migration state"; evidence NOT hidden (catch path deliberately does not reconcile).

---

## 10. Remaining limitations

1. **Compatibility verification-row append on WS reconnect** (cosmetic only):
   `handleCompatibilityWS` appends duplicate `verification_result` rows on reconnect.
   Read-only, de-duped at display by check name, no truthfulness impact. Optional
   backend upsert keyed by `(migration_id, target, verification_type)` deferred
   (rule 7 — not needed for correctness).
2. **Rollback `unknown` after a REST throw cannot auto-recover**: by design we keep
   `unknown` and let the operator reload, because `loadSession()` swallows its own
   errors and reconciling could hide an in-flight backend rollback. Acceptable
   trade-off per the truthfulness rule (ambiguity shown as ambiguity).
3. **No live end-to-end server test** for the rollback banner in this phase — the
   matrix in §9 was verified against code paths, not a running backend. The
   `reconcileRollbackState` mapping itself is unit-tested (8 cases).

---

## 11. Release recommendation

**Release.** 4G.2 closes the realtime-operation parity gap with FE-only,
low-risk changes that strictly follow the 4G.1 authority model:

- execute already met the bar — no change, no regression risk.
- dry-run / compatibility gained an honest `unknown` instead of a faked `failed`.
- rollback gained a truthful state machine (`running`/`completed`/`degraded`/
  `failed`/`unknown`) over states the backend already held, with no duplicate-risk
  and no hidden evidence.

**No backend contract change.** No new endpoint, no new transport. All required
fixes render states the backend already authoritatively holds — consistent with
the non-negotiable rules (FE reconciles to backend; FE never hangs on one terminal
frame; ambiguity shown as ambiguity).

**PART 8 stop conditions — none triggered:**
- Every flow has an authoritative backend state to reconcile against (`loadSession()`).
- Duplicate-prevention guaranteed server-side for both side-effectful flows
  (execute run-lock + state machine; rollback no-op idempotency).
- Refresh recovery is truthful without backend changes.
- FE is never forced to fake success/failure.

Optional follow-up (deferred, not blocking): compatibility verification-row upsert.

---

## Files changed (4G.2)

- `web/src/routes/migrations/[id]/pipeline/+page.svelte` — `unknown` on dry-run/
  compat `onclose` reconcile failure; explicit `rollbackState`/`rollbackStateDetail`
  state machine + status banner; `stepStatuses` union extended with `'unknown'`.
- `web/src/lib/api/pipeline.ts` — added `RollbackState` type + pure
  `reconcileRollbackState(backendState, current)` helper (unit-tested).
- `web/src/lib/components/PipelineStepper.svelte` — `unknown` color cases.
- `web/src/lib/api/rollback-reconcile.test.ts` — **new**, 8 tests.
- `docs/superpowers/specs/phase-4g2-realtime-parity-investigation.md` — **new** (PART 1).
- `docs/superpowers/specs/phase-4g2-realtime-parity-decision.md` — **new** (PART 2).
- `docs/superpowers/specs/phase-4g2-realtime-parity-final-report.md` — this file (PART 7).
