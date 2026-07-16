# Phase 6C-BE — Per-item Execution, Verification & Enforcement Enhancement

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (or
> subagent-driven-development) to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax.

**Goal:** Build the backend that makes the FE honesty claims in Phase UI-B actually
true at the item level: persist per-item execution + verification evidence,
enforce hard dependencies at apply time (incl. catMixed), compute an honest
rollback scope from item-level evidence, and derive a truthful terminal status.

**Architecture:** Extend the existing hybrid model — `migration_steps` stays coarse
per category; a new `migration_item_results` table holds granular per-item
execution/verification evidence; `migration_selections` gains decision-reason /
risk-ack / manual-followup columns; a new append-only `migration_selection_history`
audits decision changes. The `Applier.Apply` interface is NOT changed — instead
`initialSyncStage` filters the collected `CategoryData` to the selected-apply item
set before calling `Apply`, so true per-item apply works without touching the
5 appliers. Verification evidence is recomputed from `migration_item_results`
plus the existing `migration_verifications` rows.

**Tech Stack:** Go backend (`internal/mod/migration`), SQLite
(`internal/db/migrations.go` DDL + `repo.go` persistence), Svelte FE untouched
this phase (UI-B already shipped the honest copy relying on these contracts).

## Global Constraints (frozen from spec + audit)

- Canonical path unchanged: wizard → pipeline → compare/select → apply → verify.
- No new executor / route for apply; selective apply stays inside `initialSyncStage`.
- Do NOT move compare/select out of `/migrations/:id/compare`.
- Do NOT change cutover/commit/rollback into a new flow.
- Do NOT put per-item state in `migration_steps` (keep it coarse).
- Do NOT fake `verified`/`applied` from step-level status alone when item-level
  evidence is absent — derive best-effort from step status for legacy rows only.
- Do NOT display raw secret / env / private key / cert material / credential.
- All new `migration_selections` columns MUST be nullable/defaulted (backward
  compatibility for old rows).
- SQLite: use `INSERT ... ON CONFLICT(migration_id, item_key) DO UPDATE`
  for upserts (already proven in `UpsertSelection`); `ALTER TABLE ... ADD COLUMN`
  for new columns (ignore "duplicate column" error, proven in `alterPhase2D`).

---

## Audit findings (contract vs actual code) — reconciled before coding

| # | Contract says | Actual code | Resolution |
|---|---|---|---|
| A1 | `ParityItem` has 4 dims: ObservedState/DecisionState/ExecutionState/VerificationState + `Category` + `HardBlocked` | `parity.go` `ParityItem` has single `Status` (mixed) + `VerifyState`; NO `HardBlocked`/`ExecutionState`/`DecisionState`/`Category` | Extend `ParityItem` + populate in `ComputeParity`; keep `Status` as ObservedState for FE compat |
| A2 | `catMixed` applies SELECTED items only | `pipeline.go:1614` applies WHOLE category + warns "some selected-skip items were applied" | **Violation.** Filter `CategoryData` to apply-set before `Apply`; write per-item results |
| A3 | Hard dep unsatisfied → item `blocked`, not applied even if decision=apply | FE blocks only; backend `catMixed` ignores deps entirely | `initialSyncStage` reads `HardBlocked` per item from parity; blocked items get `blocked` row, never applied |
| A4 | `migration_item_results` exists, keyed `(migration_id, item_key)` | Table does NOT exist | CREATE TABLE in `migrations.go` |
| A5 | `migration_selection_history` append-only | Does NOT exist | CREATE TABLE |
| A6 | `migration_selections` has `decision_reason`/`risk_acknowledged`/`manual_followup` | Only `migration_id,item_key,category,action,updated_at` | ALTER ADD COLUMN (nullable) |
| A7 | `deriveMigrationStatus` supports `completed_with_drift`/`completed_with_unresolved_drift`/`verification_failed`/`verification_partial` | Only emits `completed`/`completed_partial`/`completed_with_manual_gaps` | Extend using item-level results |
| A8 | Rollback scope = applied subset only (skip/keep_target/review_manual/failed excluded) | `rollbackApplied` uses coarse `GetAppliedCategories` | Use `migration_item_results` `applied` subset |
| A9 | POST `/parity/recompute`, GET `/follow-up` exist | Neither exists | Add to handler dispatch + runner methods |
| A10 | `parity-summary` has 5 axes + counters | Only 3 scores + manualGaps/unresolvedDrift/passed/failed | Extend struct + `ComputeParitySummary` |

No silent assumptions: every delta above is reconciled into a task below.

---

## File structure

- **Modify** `internal/db/migrations.go` — DDL: `migration_item_results`,
  `migration_selection_history`; ALTERs for selection columns; index.
- **Modify** `internal/mod/migration/parity.go` — extend `ParityItem` (4 dims +
  `Category` + `HardBlocked` + execution/verification fields); extend `ParitySummary`
  (5 axes + counters); add `ItemResult` + `SelectionHistory` types; add
  `ExecutionState`/`VerificationState`/`DecisionState` const blocks.
- **Modify** `internal/mod/migration/repo.go` — `ItemResult` persistence
  (UpsertItemResult/GetItemResults/GetItemResult), `SelectionHistory`
  append (AppendSelectionHistory), extend `UpsertSelection` + `SelectionDecision`
  scan to carry new columns, extend `PipelineRepo` interface.
- **Modify** `internal/mod/migration/parity_engine.go` — `ComputeParity` populates
  4 dims + `HardBlocked` + `Category`; `BulkApply` already correct (no change
  needed, verified); `ComputeParitySummary` → 5 axes + counters from item results;
  add `GetFollowUp` + `RecomputeParity`.
- **Modify** `internal/mod/migration/pipeline.go` — `initialSyncStage.Execute`
  filters apply-set per item, enforces hard-dep block, writes per-item results;
  `deriveMigrationStatus` extended; `rollbackApplied` uses item-level applied subset.
- **Modify** `internal/mod/migration/handler.go` — add `recompute` + `follow-up`
  route cases + handlers; extend `selectionRequest` to carry reason/ack/followup;
  extend `handlePutSelection` to write history; extend `handleParitySummary` shape.
- **Modify** `internal/mod/migration/runner.go` — expose `RecomputeParity` /
  `GetFollowUp` / `ParitySummary` (already delegates) wiring.
- **Create** `internal/mod/migration/item_result_test.go` — unit tests (see Testing).
- **Create** `internal/mod/migration/parity_summary_test.go` — 5-axis tests.
- **Create** `internal/mod/migration/derive_status_test.go` — terminal status tests.
- **Create** `internal/mod/migration/selection_history_test.go` — audit tests.
- **Create** `internal/mod/migration/backward_compat_test.go` — legacy no-results tests.
- **Modify** `internal/mod/migration/pipeline_test.go` (append) — catMixed + block tests.
- **Create** `docs/superpowers/specs/phase-6c-be-execution-verification-report.md`.
- **Create** `docs/superpowers/specs/phase-6c-be-execution-verification-acceptance.md`.

---

## Task 1: Schema (DDL) — `internal/db/migrations.go`

**Files:** Modify `internal/db/migrations.go` (in `statements` slice + `alterPhase2D`).

**Interfaces:** none new at this layer; consumed by `repo.go`.

- [ ] **Step 1:** Add to the `statements` slice (after `migration_selections`):
  ```sql
  CREATE TABLE IF NOT EXISTS migration_item_results (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id  INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    item_key      TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT '',
    execution_state TEXT NOT NULL DEFAULT 'not_applicable',
    last_execution_at DATETIME,
    step_refs     TEXT DEFAULT '[]',
    execution_notes TEXT DEFAULT '',
    verification_state TEXT NOT NULL DEFAULT 'not_verified',
    verification_level TEXT NOT NULL DEFAULT '',
    verify_evidence TEXT DEFAULT '',
    verify_notes TEXT DEFAULT '',
    backup_ref    TEXT DEFAULT '',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(migration_id, item_key)
  );
  CREATE INDEX IF NOT EXISTS idx_item_results_migration ON migration_item_results(migration_id, execution_state);
  CREATE TABLE IF NOT EXISTS migration_selection_history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    item_key     TEXT NOT NULL,
    from_action  TEXT NOT NULL DEFAULT '',
    to_action    TEXT NOT NULL DEFAULT '',
    reason       TEXT DEFAULT '',
    actor        TEXT DEFAULT 'operator',
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  CREATE INDEX IF NOT EXISTS idx_sel_history_migration ON migration_selection_history(migration_id, item_key);
  ```
- [ ] **Step 2:** Add to `alterPhase2D` (idempotent, ignore "duplicate column"):
  ```sql
  ALTER TABLE migration_selections ADD COLUMN decision_reason   TEXT DEFAULT '';
  ALTER TABLE migration_selections ADD COLUMN risk_acknowledged INTEGER DEFAULT 0;
  ALTER TABLE migration_selections ADD COLUMN manual_followup   TEXT DEFAULT '';
  ```
- [ ] **Step 3:** `go build ./...` green.

## Task 2: Domain types — `parity.go`

**Files:** Modify `internal/mod/migration/parity.go`.

- [ ] **Step 1:** Add const blocks (ExecutionState / VerificationState / DecisionState):
  ```go
  // ExecutionState — pipeline layer, per item (§F.3).
  const (
    ExecNotApplicable = "not_applicable"
    ExecPending       = "pending"
    ExecBlocked       = "blocked"
    ExecSkipped       = "skipped"
    ExecApplied       = "applied"
    ExecFailed        = "failed"
    ExecRolledBack    = "rolled_back"
    ExecPartiallyApplied = "partially_applied"
  )
  // VerificationState — post-apply probe (§F.4).
  const (
    VerifyNotVerified = "not_verified"
    VerifyInfra       = "infra_verified"
    VerifyRuntime     = "runtime_verified"
    VerifyApp         = "app_verified"
    VerifyPartial     = "partial_verified"
    VerifyFailed      = "verify_failed"
    VerifyUnresolved  = "unresolved"
  )
  // DecisionState — operator layer (§F.2).
  const (
    DecisionUndecided     = "undecided"
    DecisionApply        = "apply_from_source"
    DecisionKeepTarget   = "keep_target"
    DecisionSkip         = "skip"
    DecisionReviewManual = "review_manual"
  )
  ```
- [ ] **Step 2:** Extend `ParityItem` (keep `Status` as ObservedState for FE compat;
  add the 4 dims + identity + execution/verification fields):
  ```go
  type ParityItem struct {
    Category    string `json:"category"`
    ItemKey     string `json:"itemKey"`
    SourceValue string `json:"sourceValue"`
    TargetValue string `json:"targetValue"`
    Status      ParityStatus `json:"status"`          // ObservedState (§F.1)
    DecisionState string `json:"decisionState"`     // §F.2; default derived
    Suggested   ParityAction `json:"suggested"`
    ApplyLevel  ApplyLevel `json:"applyLevel"`
    Deps        []DependencyRef `json:"deps,omitempty"`
    HardBlocked bool `json:"hardBlocked"`           // any unsatisfied hard dep
    Warnings   []string `json:"warnings,omitempty"`
    Freshness   string `json:"freshness,omitempty"`
    // execution (§F.3) — populated after apply
    ExecutionState string `json:"executionState,omitempty"`
    LastExecutionAt string `json:"lastExecutionAt,omitempty"`
    // verification (§F.4) — populated after verify
    VerificationState string `json:"verificationState,omitempty"`
    VerificationLevel string `json:"verificationLevel,omitempty"`
    VerifyEvidence string `json:"verifyEvidence,omitempty"`
    // decision metadata
    DecisionReason   string `json:"decisionReason,omitempty"`
    RiskAcknowledged bool `json:"riskAcknowledged"`
    ManualFollowup  string `json:"manualFollowup,omitempty"`
  }
  ```
- [ ] **Step 3:** Extend `ParitySummary` (5 axes + counters):
  ```go
  type ParitySummary struct {
    MigrationID int `json:"migrationId"`
    // 5 axes (§N)
    ObservedParity       float64 `json:"observedParity"`
    DecisionCoverage     float64 `json:"decisionCoverage"`
    ExecutionCompletion  float64 `json:"executionCompletion"`
    VerificationConfidence float64 `json:"verificationConfidence"`
    ManualDeferredBurden float64 `json:"manualDeferredBurden"`
    // counters
    AcceptedDrift  int `json:"acceptedDrift"`
    UnresolvedDrift int `json:"unresolvedDrift"`
    ManualGaps     int `json:"manualGaps"`
    Failed         int `json:"failed"`
    Passed         int `json:"passed"`
    ComputedAt     string `json:"computedAt"`
  }
  ```
- [ ] **Step 4:** Add `ItemResult` + `SelectionHistory` types:
  ```go
  type ItemResult struct {
    MigrationID int `json:"migrationId"`
    ItemKey string `json:"itemKey"`
    Category string `json:"category"`
    ExecutionState string `json:"executionState"`
    LastExecutionAt string `json:"lastExecutionAt,omitempty"`
    StepRefs []int `json:"stepRefs"`
    ExecutionNotes string `json:"executionNotes,omitempty"`
    VerificationState string `json:"verificationState"`
    VerificationLevel string `json:"verificationLevel,omitempty"`
    VerifyEvidence string `json:"verifyEvidence,omitempty"`
    VerifyNotes string `json:"verifyNotes,omitempty"`
    BackupRef string `json:"backupRef,omitempty"`
    CreatedAt string `json:"createdAt,omitempty"`
    UpdatedAt string `json:"updatedAt,omitempty"`
  }
  type SelectionHistory struct {
    MigrationID int `json:"migrationId"`
    ItemKey string `json:"itemKey"`
    FromAction string `json:"fromAction"`
    ToAction string `json:"toAction"`
    Reason string `json:"reason"`
    Actor string `json:"actor"`
    CreatedAt string `json:"createdAt,omitempty"`
  }
  ```
- [ ] **Step 5:** `go build ./...` green.

## Task 3: Selection persistence extend — `parity.go` + `repo.go`

**Files:** Modify `parity.go` (`SelectionDecision` + `selectionRequest` add fields),
`repo.go` (`UpsertSelection`/`GetSelections`/`SelectionDecision` scan + new methods).

- [ ] **Step 1:** Extend `SelectionDecision` with `DecisionReason`,
  `RiskAcknowledged`, `ManualFollowup` (omitempty).
- [ ] **Step 2:** In `repo.go`, extend `UpsertSelection` to accept
  `reason, riskAck bool, manualFollowup string` and persist them (ON CONFLICT
  DO UPDATE includes them). Update `GetSelections` scan to read the 3 new columns.
- [ ] **Step 3:** Add `PipelineRepo` interface methods:
  ```go
  UpsertItemResult(ctx, migrationID int, r ItemResult) error
  GetItemResults(ctx, migrationID int) ([]ItemResult, error)
  GetItemResult(ctx, migrationID int, itemKey string) (ItemResult, error)
  AppendSelectionHistory(ctx, migrationID int, h SelectionHistory) error
  GetSelectionHistory(ctx, migrationID int) ([]SelectionHistory, error)
  ```
  Implement all in `sqliteRepo`.
- [ ] **Step 4:** `go build ./...` green; `go test ./internal/mod/migration/`
  (existing) green.

## Task 4: ComputeParity populates 4 dims + HardBlocked — `parity_engine.go`

**Files:** Modify `parity_engine.go` `ComputeParity` (item assembly loop).

- [ ] **Step 1:** For each item, set `it.Category`, derive `it.DecisionState`
  from selection (default `DecisionUndecided` if no selection; else the action),
  set `it.HardBlocked = hasUnsatisfiedHardDep(it)` (reuse existing helper),
  set `it.Status` = observed state (unchanged behavior), set
  `it.VerificationState` from any existing `ItemResult` row (lookup by itemKey) if
  present (best-effort, legacy-safe: empty → `not_verified`), set
  `it.ExecutionState` from `ItemResult` if present (else `not_applicable`).
- [ ] **Step 2:** `go build ./...` + a focused unit test asserting an item with an
  unsatisfied hard dep has `HardBlocked == true` and `DecisionState` reflects its
  selection (or default).

## Task 5: initialSyncStage — per-item apply + hard-dep block — `pipeline.go`

**Files:** Modify `pipeline.go` `initialSyncStage.Execute` + add a helper that
filters a `CategoryData`'s item list to the apply-set and classifies each item.

**Interfaces:** Reads `ParityItem` (via `runner.Parity`), `SelectionDecision`, writes
`ItemResult` (new repo method).

- [ ] **Step 1:** In `Execute`, for a `catMixed` (or any) step, before `Apply`:
  load parity items for the category (or reuse a parity pass), build a per-item
  decision map. For each item in the collected `CategoryData`:
  - if `HardBlocked` (unsatisfied hard dep) → do NOT apply; write
    `ItemResult{ExecutionState: ExecBlocked, Notes: "hard dep unsatisfied"}`.
  - if decision `keep_target`/`skip` → do NOT apply; write
    `ItemResult{ExecutionState: ExecSkipped}`.
  - if `review_manual` → `ItemResult{ExecutionState: ExecNotApplicable}`.
  - if `apply_from_source` (or no decision) and not blocked → include in apply-set;
    after `Apply` success, write `ItemResult{ExecutionState: ExecApplied,
    LastExecutionAt: now, StepRefs:[step.ID], BackupRef: <backup id if any>}`.
- [ ] **Step 2:** Filter the collected `CategoryData` to ONLY the apply-set items
  before calling `mod.Applier.Apply` (preserves the `Applier` interface — no
  signature change). Per-category appliers (packages/configs/...) then naturally apply
  only the selected items. **This fixes A2** (catMixed no longer applies all).
- [ ] **Step 3:** On `Apply` error for the category, mark applied-set items
  `ExecFailed` (rollback already reverts the category); items NOT in apply-set keep
  their `blocked`/`skipped`/`not_applicable` rows.
- [ ] **Step 4:** `go build ./...` green.

## Task 6: Verification evidence — `parity_engine.go` + `pipeline.go`

**Files:** Modify `parity_engine.go` (add `WriteVerification` helper or extend
`ComputeParitySummary`), `pipeline.go` health_verification stage writes `ItemResult`
verification fields.

- [ ] **Step 1:** Add `runner` method `RecordVerification(ctx, migrationID, itemKey, level, state, evidence)` → `UpsertItemResult`.
- [ ] **Step 2:** In the health-verification stage, for each applied item, probe
  per contract §L (package→infra_verified; config identical→infra_verified;
  service active→runtime_verified; app health check→app_verified; probe missing→
  `unresolved`/`not_verified`; verify failed→`verify_failed`). Keep `migration_verifications`
  rows for step-level scores; ALSO write per-item `ItemResult.VerificationState`.
- [ ] **Step 3:** Honest rules enforced: keep_target→`not_verified` (ok);
  skip/review_manual→`unresolved`; applied-but-unprobed→`unresolved` (never green).
- [ ] **Step 4:** `go build ./...` + test that verify_failed maps correctly.

## Task 7: deriveMigrationStatus extended — `pipeline.go`

**Files:** Modify `pipeline.go` `deriveMigrationStatus`.

- [ ] **Step 1:** Load `ItemResult`s. Compute buckets:
  - `blocked` = items with ExecBlocked
  - `applied` = ExecApplied; `failed` = ExecFailed
  - `keepTarget` = selections keep_target; `skip`/`reviewManual` = manual gaps
  - `verified` = VerifyApp/VerifyRuntime/VerifyInfra; `verifyFailed` = VerifyFailed;
    `unresolved` = VerifyUnresolved/not_verified on applied items
  - `acceptedDrift` = keep_target count
- [ ] **Step 2:** Honest terminal mapping (§M):
  - any `verifyFailed` → `StatusVerificationFailed`
  - any applied item `unresolved`/`not_verified` AND partial → `StatusVerificationPartial`
  - `failed`(ExecFailed) > 0 → `StatusCompletedPartial`
  - `skip`/`unknown` open > 0 (no failed) → `StatusCompletedWithUnresolvedDrift`
  - `reviewManual` > 0 (no failed) → `StatusCompletedWithManualGaps`
  - `keepTarget` > 0, zero failed/skip/manual → `StatusCompletedWithDrift`
  - everything applied + verified, zero gaps → `StatusCompleted` (full green)
- [ ] **Step 3:** Legacy fallback — if NO `ItemResult` rows exist for the
  migration, derive best-effort from step status (applied step → items applied) so
  old migrations still read (backward compat, A-note in spec §10).
- [ ] **Step 4:** `go build ./...` + `derive_status_test.go`.

## Task 8: Rollback scope from item-level evidence — `pipeline.go`

**Files:** Modify `pipeline.go` `rollbackApplied` + `initialSyncStage.Rollback`.

- [ ] **Step 1:** Replace coarse `GetAppliedCategories` with `GetItemResults`
  filtered to `ExecutionState == ExecApplied`. Rollback ONLY those item keys'
  categories (subset), using their `BackupRef` for audit.
- [ ] **Step 2:** keep_target / skip / review_manual / failed items are excluded
  by construction (they are never `ExecApplied`). Document this in the report.
- [ ] **Step 3:** `go build ./...` + test asserting rollback touches only applied subset.

## Task 9: API shapes final — `handler.go` + `runner.go`

**Files:** Modify `handler.go` (dispatch + handlers), `runner.go` (delegate).

- [ ] **Step 1:** Add `parity/recompute` (POST) → `runner.RecomputeParity`
  (clears stale, re-runs `ComputeParity`, returns `ParityResult`). Add
  `follow-up` (GET) → `runner.GetFollowUp` (returns items with
  decision skip / review_manual / unknown / verify_failed).
- [ ] **Step 2:** Extend `handleParitySummary` response to the 5-axis
  `ParitySummary` (already returns the struct; just the struct changed in Task 2).
- [ ] **Step 3:** Extend `selectionRequest` to carry `decisionReason`,
  `riskAcknowledged`, `manualFollowup`; `handlePutSelection` writes history
  (compare old vs new action) via `AppendSelectionHistory` when action changes.
- [ ] **Step 4:** `go build ./...` + handler test for recompute + follow-up
  payload/response shape.

## Task 10: Tests

- [ ] **Step 1:** `item_result_test.go` — upsert/get round-trip; blocked item
  not applied; catMixed applies only selected subset (integration via `initialSyncStage`
  with a fake registry + in-memory repo).
- [ ] **Step 2:** `parity_summary_test.go` — 5-axis + counters for a
  mixed migration (keep_target→acceptedDrift; skip→unresolvedDrift; applied+
  verified→passed; verify_failed→failed).
- [ ] **Step 3:** `derive_status_test.go` — completed vs drift vs manual vs
  unresolved vs partial vs verify_failed.
- [ ] **Step 4:** `selection_history_test.go` — append-only; PUT changing
  action records from/to; old row valid.
- [ ] **Step 5:** `backward_compat_test.go` — migration with NO
  `migration_item_results` rows still derives a status (best-effort from steps).
- [ ] **Step 6:** `go test ./internal/mod/migration/` all green.

## Task 11: Verify backward compatibility

- [ ] **Step 1:** Boot a fresh DB via `db.Migrate`; assert old
  `migration_selections` rows (without new columns) still scan (nullable default).
- [ ] **Step 2:** Run `Migrate` twice (idempotent ALTERs) — no "duplicate column" failure.
- [ ] **Step 3:** `go test ./...` green at repo root.

## Task 12: Write reports

- [ ] **Step 1:** `docs/superpowers/specs/phase-6c-be-execution-verification-report.md`
  (files changed, schema, API changes, execution path, verification path,
  terminal status logic, backward compat, tests run, known limitations).
- [ ] **Step 2:** `docs/superpowers/specs/phase-6c-be-execution-verification-acceptance.md`
  (checklist: implemented/tested, needs-live-verification, blocked, out-of-scope).
- [ ] **Step 3:** CHANGELOG Unreleased entry (Phase 6C-BE).

---

## Self-review against the spec's "don't say done if"

- `migration_item_results` truly used? → Task 5/6/7/8 write + read it. ✅
- hard dep blocked in backend, not just FE? → Task 5 (ExecBlocked, never applied) + Task 4 HardBlocked. ✅
- catMixed applies only selected? → Task 5 Step 2 filters CategoryData. ✅ (fixes the warn-only bug)
- rollback uses item-level evidence, not coarse step? → Task 8. ✅
- terminal status can't be green with unresolved/manual/verify_failed? → Task 7
  mapping excludes full-green unless all verified+no gaps. ✅

## Known limitations (documented, not hidden)

- Per-item verification probes are limited to what the existing health/verification
  layers can reach; deep app-health for manual-placeholder items stays `unresolved`.
- `catMixed` filtering requires the collected `CategoryData` to be item-addressable
  (packages/configs/services/users/docker/db all are); if a future collector emits
  opaque blobs, per-item filtering degrades to category-level (documented).
- No FE changes this phase — UI-B already shipped the honest copy that consumes
  these contracts; a follow-up FE task can surface the new `executionState`/
  `verificationState` fields (out of scope per spec §8 "don't invent FE fields
  without wiring" — wiring is done here; FE display is a separate decision).
