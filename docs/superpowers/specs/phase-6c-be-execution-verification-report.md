# Phase 6C-BE — Per-item Execution, Verification & Enforcement (Backend Report)

**Goal:** Give the compare/pipeline flow *real* per-item state (applied ≠ verified), an honest rollback scope, and correct hard-dependency enforcement at apply time — backend only, no FE/product-flow redesign.

**Status:** Implemented + tested. 18 new tests pass; full `internal/mod/migration` package green; `go build ./...` clean.

---

## 1. What was built

### 1.1 Schema (`internal/db/migrations.go`)
- **`migration_item_results`** — one row per (migration_id, item_key). Fields: `execution_state`, `last_execution_at`, `step_refs`, `execution_notes`, `verification_state`, `verification_level`, `verify_evidence`, `verify_notes`, `backup_ref`, `category` (index helper). Indexed on `(migration_id)`.
- **`migration_selection_history`** — append-only audit of every selection change (from_action → to_action, reason, actor). Never updated.
- **`migration_selections`** extended: `decision_reason`, `risk_acknowledged`, `manual_followup` (nullable / defaulted so old rows still scan).
- All additions are idempotent: `CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ... ADD COLUMN` that ignores "duplicate column". `Migrate()` is safe to run twice (verified by `TestMigrateIdempotent`).

### 1.2 Four never-merged state dimensions (`parity.go`)
Each `ParityItem` now carries, independently:
- **ObservedState** (`status`: same/different/missing/…) — what the live compare saw.
- **DecisionState** (`decisionState`: undecided/apply/keep_target/skip/review_manual) — the operator's choice.
- **ExecutionState** (`executionState`: not_applicable/pending/blocked/skipped/applied/failed/rolled_back) — what apply actually did.
- **VerificationState** (`verificationState`: not_verified/infra/runtime/app/partial/failed/unresolved) — what a probe confirmed.

Honest rules enforced: applied ≠ verified; keep_target ≠ equality; skip = unresolved; review_manual never auto-completes; a no-probe item stays not_verified/unresolved (never green).

### 1.3 Apply-time backend enforcement (`pipeline.go`)
`applyItemPlan` classifies every item in a category against the operator's per-item decisions **and** re-verifies hard dependencies against a freshly-built live `targetInventory` (`itemHardBlocked` reuses `depsFor`):
- Hard-blocked → `ExecBlocked`, **never** in the apply set, even if the decision was `apply_from_source`.
- keep_target / skip → `ExecSkipped`.
- review_manual → `ExecNotApplicable` (non-executable placeholder).
- apply_from_source / undecided → `ExecPending`, enters the per-item apply set.

`initialSyncStage.Execute` filters each category's `CategoryData.Data` to the apply set **before** calling the (unchanged, coarse) `Applier.Apply`. This fixes the prior `catMixed` bug where a mixed category applied *all* items. Only `ExecPending` items that actually applied are finalized to `ExecApplied` (with `step_refs`); failed items are `ExecFailed` and rolled back.

### 1.4 Verification evidence (`healthVerificationStage.Execute`)
Loads applied `ItemResults`; attests `infra`/`runtime`/`app` layers from probes, **never** downgrading a level once reached. `verify_failed` on probe failure. Mapping: package installed → infra; config identical → infra; service active → runtime; app health → app; no probe → unresolved (not green).

### 1.5 Honest rollback scope (`rollbackApplied`)
Rollback now filters to categories containing ≥1 `ExecApplied` item (read from `ItemResults`, not the coarse step). keep_target/skip/review_manual are **not** in scope. Applied items are marked `ExecRolledBack` only after a successful rollback.

### 1.6 Terminal-status honesty (`deriveStatusFromItems`)
Reads **only** per-item evidence. Priority: `verify_failed` → `unresolved/unverified` (verification_partial) → `apply_failed` (completed_partial) → `skip/review_manual` (completed_with_unresolved_drift) → `blocked` (completed_with_unresolved_drift) → `keep_target` (completed_with_drift) → all verified (completed). A pure skip/review/keep_target migration is **not** mislabeled `verification_partial` (regression caught and fixed during testing).

### 1.7 API/contract (`handler.go`, `runner.go`)
- `GET /parity` — already returns per-item 4 dimensions.
- `GET/PUT /selection`, `PUT /selection/bulk` — extended with `decisionReason`, `riskAcknowledged`, `manualFollowup`; PUT records `SelectionHistory`.
- Bulk policies: `skip_selected`, `review_manual_selected`, `clear` (new) alongside existing `apply_safe`/`accept_risky`.
- `GET /parity-summary` — now 5 axes (observedParity, decisionCoverage, executionCompletion, verificationConfidence, manualDeferredBurden) + counters (acceptedDrift, unresolvedDrift, manualGaps, failed, passed).
- `POST /parity/recompute` — re-runs the live compare.
- `GET /follow-up` — items still needing operator action.

---

## 2. "Don't say done if" — gate check

| Gate | Status |
|------|--------|
| `migration_item_results` actually used | ✅ written in `initialSyncStage`, read in `healthVerificationStage`, `deriveStatusFromItems`, `rollbackApplied`, `ComputeParitySummary` |
| Hard dep enforced in backend, not just FE | ✅ `itemHardBlocked` re-verified at apply time |
| `catMixed` applies only selected | ✅ `filterCategoryToApplySet` pre-filters `CategoryData.Data` |
| Rollback not reliant on coarse step alone | ✅ item-level `ExecApplied` scope |
| Terminal never green with unresolved/manual/verify_failed | ✅ `deriveStatusFromItems` strict priority |

---

## 3. Security constraints honored
No production migration performed without validation; no "zero downtime" claim; no source/target mutated outside the canonical apply path; no destructive rollback without reason; no fabricated verified/applied status; no raw secret/env/private-key/cert/credential in any report or API response (selection `decision_reason`/`manual_followup` are operator free-text, not credentials).
