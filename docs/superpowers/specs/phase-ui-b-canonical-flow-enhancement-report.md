# Phase UI-B — Canonical Migration Flow Enhancement Report

> Implementation following the Phase UI-A audit. Tidy-up ("Rapikan") of the
> canonical flow: disambiguate the three "compare" surfaces, discovery, and
> terminal status; make state semantics honest. No redesign, no new
> executor/route, no change to backend semantics or the `initial_sync`/
> cutover/commit/rollback execution flow.

## Scope & constraints (as agreed)

- No total redesign. No new executor, no new route.
- `compare`/`select` stays a pre-`initial_sync` decision surface (standalone
  route `/migrations/:id/compare`), not a pipeline stage.
- Do not move apply/cutover/rollback out of the pipeline.
- Do not change backend semantics. Frontend only uses existing contracts.
- Do not display synthetic data. Where `migration_item_results` (Phase 6C) is
  not built, show honest fallback copy instead of fabricated green badges.
- Do not delete existing routes without a dependency check.

## Files changed

| File | Change |
|---|---|
| `web/src/routes/migrations/[id]/pipeline/+page.svelte` | Added persistent "Compare & select items" on-ramp banner + selection summary (loaded from `getSelections`/`parity`). Relabeled stage "Discovery" → "Migration pre-flight" and button "Run Discovery" → "Run pre-flight". Added `loadSelectionSummary()` (non-blocking, fire-and-forget). |
| `web/src/lib/components/MigrationHeader.svelte` | Replaced raw-string terminal status with `statusVisual()` — honest dot colors + human labels for all backend outcome statuses. |
| `web/src/routes/migrations/[id]/diff/+page.svelte` | Retitled "Server Diff" → "Source/target diff". Button "Compare & Select" → "Open item-level compare" + added helper line explaining this is the raw diff (selection happens on compare). |
| `web/src/routes/migrations/[id]/compare/+page.svelte` | `accepted_target` now neutral tone (not success). Added `hardDepUnsatisfied()` guard: `apply_from_source` disabled + blocked in `requestAction` when a hard dep is unsatisfied. Added honest banner: per-item execution/verification state not tracked by backend yet. |
| `web/src/routes/servers/[id]/+page.svelte` | "Re-scan" → "Scan server inventory"; added helper text clarifying inventory ≠ migration plan. |
| `web/src/lib/components/Sidebar.svelte` | `/servers/compare` label "Compare" → "Compare servers". |
| `web/src/routes/migrations/new/+page.svelte` | Step 4 review adds honest copy: detailed comparison/selection happen in the pipeline after the plan is created. |

## Navigation changes

- **Pipeline → compare:** a persistent banner in `migrations/[id]/pipeline`
  links to `/migrations/:id/compare` ("Open item-level compare"). This closes
  the primary UI-A gap — compare/select was previously only reachable via the
  raw `/diff` page.
- **Three compare surfaces now distinct:**
  - `/servers/compare` — "Compare servers" (infra, two snapshots)
  - `/migrations/:id/diff` — "Source/target diff" (raw added/removed/changed)
  - `/migrations/:id/compare` — "Compare & select items" (item-level parity + decisions)

## Wording changes

| Before | After | Where |
|---|---|---|
| "Server Diff" | "Source/target diff" | `/diff` h1 |
| "Compare & Select" (button) | "Open item-level compare" | `/diff` |
| — | helper line: raw diff vs selection | `/diff` |
| "Discovery" (stage) | "Migration pre-flight" | pipeline step 0 |
| "Run Discovery" | "Run pre-flight" | pipeline step 0 |
| "Re-scan" | "Scan server inventory" | server detail |
| — | "inventory ≠ migration plan" helper | server detail |
| "Compare" (sidebar) | "Compare servers" | sidebar |
| (none) | honest state-separation copy | `/compare` |

## State semantics

- **ObservedState vs DecisionState vs ExecutionState vs VerificationState** kept
  separate (per Phase 6C).
  - `accepted_target` (Keep target) → **neutral** (not success). It is a
    deliberate non-apply decision, never "done & healthy".
  - `verified` → success; `same` → success (observed==target, not conflated
    with verified); `unresolved` → warning; `manual_required` → error;
    `skipped_by_user`/`stale`/`unsupported`/`accepted_target` → neutral.
- **Per-item ExecutionState / VerificationState:** NOT shown per item. The
  backend has no `migration_item_results` table (Phase 6C recommends it but it
  is not built). The `/compare` page now states this honestly rather than
  fabricating "applied"/"verified" badges. Per-category verification scores
  (infra/runtime/app-health) remain surfaced from `paritySummary`.
- **Dependency honesty:** `apply_from_source` is disabled and blocked when an
  item has an unsatisfied hard dependency. `keep_target`/`skip` on
  `different`/`missing_on_target` still require the 6B7 confirmation modal.
  `review_manual` stays manual. Bulk "apply safe" already excludes explicit
  decisions, ApplyLevel > Warn, and unsatisfied hard deps (verified in
  `parity_engine.go` `BulkApply`).

## Honest terminal status (pipeline header)

`MigrationHeader` previously rendered **every** terminal state with a green dot
+ raw string. Now mapped via `statusVisual()`:

| Backend status | Dot | Label |
|---|---|---|
| `completed` | green | Completed |
| `completed_with_drift` | amber | Completed — drift accepted |
| `completed_with_manual_gaps` | amber | Completed — manual follow-up |
| `completed_partial` | amber | Partially applied |
| `manual_followup_required` | amber | Manual follow-up required |
| `verification_failed` | red | Verification failed |
| `verification_partial` | amber | Verification partial |
| `failed` / `rollback_failed` | red | (pretty) |
| `rolled_back` | neutral | Rolled back |
| `needs_manual_intervention` / `rollback_degraded` | red | (pretty) |

Only a fully clean `completed` is full green.

## Backend blockers (documented, not faked)

- **`migration_item_results` table does not exist.** Per-item ExecutionState
  (pending/blocked/applied/failed/…) and VerificationState
  (not_verified/verify_failed/unresolved) cannot be shown. The `/compare` page
  states this honestly. When Phase 6C builds the table, wire per-item
  ExecutionState + VerificationState badges (do not reuse the observed-status
  badge).
- **G2 (UI-A):** whether `initial_sync` blocks a `catMixed` item whose hard dep
  is unsatisfied vs warns-only is a backend semantics question, not addressable
  in this FE-only phase. The FE now prevents the *decision* of applying such an
  item; backend enforcement remains to confirm in a later phase.

## Tests run

- `cd web && npm run check` → **0 errors** (19 pre-existing a11y warnings in
  `/compare` item-row `div` click handler, unrelated to this change).
- `go build ./...` → OK.
- `go vet ./internal/mod/migration/` → OK.
- `go test ./internal/mod/migration/` → OK (cached, green).

No Go code changed, so backend tests are unaffected.

## Manual verification (checklist)

1. Pipeline page shows the "Compare & select items" banner with a working link
   to `/compare`; selection summary populates when selections exist.
2. `/diff` no longer implies selection: title "Source/target diff", button
   "Open item-level compare", helper line present.
3. Server detail "Scan server inventory" + inventory≠plan helper text present.
4. `/compare` "Keep target" item renders neutral (not green); "Verified" still
   green; honest "not tracked yet" banner visible.
5. Applying an item with an unsatisfied hard dep is blocked (button disabled +
   toast on attempt).
6. Pipeline terminal status for a selective-apply migration with gaps renders
   amber "Completed — manual follow-up" / "Partially applied", never bare green
   "Completed".
7. Wizard step 4 shows the honest "happens in the pipeline" note (no fake
   counts).

## Out of scope / deferred

- Building `migration_item_results` + per-item execution/verification badges
  (Phase 6C, backend).
- Confirming G2 backend hard-dep enforcement at apply time.
- Any change to the `initial_sync`/cutover/commit/rollback execution flow.
