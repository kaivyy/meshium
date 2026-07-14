# Phase 4G.2 — Realtime Operation Parity Sweep: Decision Grid

**PART 2 — Decision Grid** · 2026-07-14

Reference model: **Phase 4G.1** (Plan Wizard) — explicit FE state machine,
persisted `operationId`, REST reconcile as completion authority, terminal WS frame
treated as `progress` not authority, explicit `unknown` for ambiguity, no infinite
spinner.

Investigation: `docs/superpowers/specs/phase-4g2-realtime-parity-investigation.md`.

---

## Key finding that changes the framing

The audit assumed the four flows still used the *bare* WS helpers
(`wsExecute`/`wsRollback`/`wsDryRun`/`wsCompatibility`). They do **not**.
The pipeline wizard (`routes/migrations/[id]/pipeline/+page.svelte`) uses:

- **execute** → `wsPipelineConnect` (the resilient 4G slice-3E transport:
  exponential-backoff reconnect, `lastSequence`, `?after_seq=` replay, stale timer).
- **dry-run / compatibility** → bare `wsDryRun`/`wsCompatibility`, **but** both
  reconcile via `loadSession()` (REST) on `onclose` — the 4G.1 pattern.
- **rollback** → pure REST `pipelineApi.rollbackMigration` (the `wsRollback`
  helper is **unused** by the wizard).

So the drift is **not** a missing-reconnect problem for execute, and is **not**
a missing-reconcile problem for the others. The real gaps are narrower and
truthfulness-focused (PART 1 §E/F). The grid below maps each.

---

## Decision grid

| Flow | Current completion authority | Current recovery | Duplicate risk | Needs backend change? | Chosen authority | Chosen recovery model |
|---|---|---|---|---|---|---|
| **dry-run** | hybrid: in-flight WS `complete` frame + `onclose`→`loadSession()` REST reconcile | `onMount`/`onclose` → `loadSession()` restores persisted `dryRun` result; re-derives step state | Low (read-only); FE `actionLoading` double-submit guard; no per-run id (fine for read-only) | No (optional upsert only) | REST `loadSession().dryRun` (keep WS for progress) | keep `loadSession()` reconcile; add explicit `unknown` on `loadSession()` failure instead of collapsing to `failed` |
| **compatibility** | REST `loadSession().compatibilityResults` (WS carries progress only) | same as dry-run; backend REST `POST` also dedups returned stored results | Low (read-only); same guards; `handleCompatibilityWS` *appends* duplicate `verification_result` rows on reconnect (cosmetic) | Optional: upsert verification rows keyed by `(migration_id, target, verification_type)` to stop append-duplicates | REST `loadSession().compatibilityResults` | keep; add explicit `unknown` on `loadSession()` failure |
| **execute** | resilient WS `wsPipelineConnect` + `loadSession()` reconcile; backend refus execute unless `StateCreated/StateResuming`; run-lock + state machine | `onMount`→`loadSession()`; `handlePipelineWS` streams history instead of re-executing when not startable; auto-reconnect with replay | None — double-guarded server-side (state machine + `tryAcquire` run-lock) | No | resilient WS + REST `loadSession()` state machine | **No change** — reference implementation |
| **rollback** | REST `rollbackMigration` + `loadSession()` reconcile; backend no-ops if `StateRollback/RolledBack` | `onMount`→`loadSession()` restores `rolling_back`/`rolled_back`; FE `actionLoading` + confirmation modal guard | Low–Medium (destructive): FE guard + backend `StateRollback`/`RolledBack` no-op idempotency; re-run from `failed`/`interrupted` is intended retry | No (state already in contract) | REST `rollbackMigration` + `loadSession()` state | **FE change**: explicit rollback state machine (`idle→running→completed|degraded|failed|unknown`) reconciled from backend state; surface `rolling_back`/`rolled_back`/`rollback_degraded`/`rollback_failed` distinctly; `unknown` on session-load failure |

---

## Per-flow decisions

### dry-run — keep authority, fix truthfulness
- **WS stays progress-only.** No reconnect/replay layer needed (read-only, cheap to
  re-run, and `loadSession()` already recovers the result).
- **Chosen authority: REST `loadSession().dryRun`** (persisted `migration_steps`
  `action='dryrun'`). The in-flight WS `complete` frame remains a *progress*
  signal, not the authority.
- **Chosen recovery:** existing `onclose`→`loadSession()` reconcile; **add** an
  explicit `unknown` state when `loadSession()` itself fails after close (today it
  silently becomes `failed`). No infinite spinner possible because `actionLoading`
  always clears in `finally`.
- **No backend change required.** (Optional future: `handleDryRunWS` dedup — not
  needed for a read-only op.)

### compatibility — same as dry-run, plus optional upsert
- **Chosen authority: REST `loadSession().compatibilityResults`** (persisted
  `verification_result`). WS progress-only.
- **Chosen recovery:** same `loadSession()` reconcile + explicit `unknown` on failure.
- **Optional backend tweak:** `handleCompatibilityWS` currently `CreateVerificationResult`
  (append) on every WS connect, so a reconnect duplicates rows. Low severity
  (read-only; `getStoredCompatibilityResults` could de-dup by check name, but
  the simplest honest fix is an **upsert** keyed by
  `(migration_id, target, verification_type)`). This is a *cosmetic* dedup, not a
  correctness/truthfulness blocker — defer unless trivial.

### execute — no change
- Already the reference: resilient WS (reconnect + replay), backend refuses
  re-execute from non-startable states, run-lock, full state machine, `unknown`-
  equivalent via `wsConnectionState` (`reconnecting`/`failed`) + `metricsStale`.
- Mark **safe enough**; do not touch.

### rollback — FE truthfulness gap (the one real fix)
- Backend already tracks the full vocabulary: `rolling_back`, `rolled_back`,
  `rollback_degraded`, `rollback_failed` (via `StateRollback`/`StateRolledBack`/
  `StateRollbackDegraded`/`StateRollbackFailed` and the step states). The FE just
  does not render it.
- **Chosen authority: REST `rollbackMigration` + `loadSession()` state.**
- **Chosen recovery:** add an explicit FE rollback state machine reconciled from
  `loadSession().state` (and the rollback step states) on mount and after the REST
  call; render `running` (rolling_back) / `completed` (rolled_back) /
  `degraded` (rollback_degraded) / `failed` (rollback_failed) / `unknown`
  (session load failed). Keep the destructive double-submit guard + backend no-op.
- **No backend change** — the contract already carries the states; this is a FE
  rendering gap only.

---

## Backend contract sufficiency (rule 7: minimal additions only)

| Flow | Contract sufficient? | Addition needed? |
|---|---|---|
| dry-run | Yes (`loadSession().dryRun`) | None |
| compatibility | Yes (`loadSession().compatibilityResults`) | None required; optional upsert (cosmetic) |
| execute | Yes (state machine + `loadSession`) | None |
| rollback | Yes (state machine carries all 4 terminal/degraded states) | **None** — FE must render existing states |

**Conclusion:** No new endpoint, no new transport. The only optional backend change
is the compatibility verification-row upsert (cosmetic, deferrable). All required
fixes are **FE rendering** of states the backend already authoritatively holds —
fully consistent with rule 2 (reuse existing authoritative resource) and rule 7
(minimal contract additions only if needed, and here: not needed).

---

## Stop-condition check (PART 8)

- A flow with no authoritative backend state to reconcile against? **No** — every flow
  persists its result/state; `loadSession()` is the reconciliation authority for all four.
- Duplicate-prevention guaranteed for side-effectful actions? **Yes** — execute
  (run-lock + state machine) and rollback (state guard no-op) are both server-side
  guaranteed.
- Refresh recovery implementable truthfully without backend changes? **Yes** — the
  contract already supports it; the FE only needs to render `unknown` and the
  rollback vocabulary.
- FE forced to fake success/failure? **No.**

→ **No stop condition triggered.** Proceed to PART 3–4 (minimum truthful FE
changes: rollback state machine + `unknown` for dry-run/compat/rollback session
failures). Backend changes: none required; optional compat upsert deferred.
