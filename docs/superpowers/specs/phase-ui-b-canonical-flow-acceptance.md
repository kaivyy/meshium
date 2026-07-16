# Phase UI-B — Canonical Migration Flow Enhancement: Acceptance

Checklist of the 9 UI-B target implementations. Status labels:
**Done** (implemented + static-verified), **Needs live verification**
(implemented, requires a running backend/migration to confirm visually),
**Blocked by backend** (cannot be done until backend work lands),
**Out of scope** (explicitly excluded by constraints).

---

## 1. Pipeline becomes canonical hub (on-ramp + selection summary)

- [x] **Done** — Persistent "Compare & select items" banner in
      `migrations/[id]/pipeline` with "Open item-level compare" →
      `/migrations/:id/compare`.
- [x] **Done** — Selection summary (to apply / keep target / deferred / manual /
      blocked / stale) computed from `getSelections` + `parity`. Loaded
      non-blocking. Verified: `npm run check` 0 errors, `go test` green.
- [ ] **Needs live verification** — Summary counts render correctly for a real
      migration with mixed selections. (Static code is correct; visual confirm
      needs a live backend.)

## 2. Raw diff stays raw diff

- [x] **Done** — `/diff` retitled "Source/target diff"; button relabeled
      "Open item-level compare"; helper line added clarifying raw diff vs
      selection. `npm run check` 0 errors.

## 3. Wizard only light summary

- [x] **Done** — Step 4 adds honest copy: detailed comparison/selection happen
      in the pipeline after the plan is created. No fake pre-plan counts
      (parity is not computable before planning). Non-blocking.

## 4. Disambiguate discovery

- [x] **Done** — Pipeline stage "Discovery" → "Migration pre-flight" (button
      "Run pre-flight"); server detail "Re-scan" → "Scan server inventory" +
      helper "inventory ≠ migration plan". Backend constant `discovery`
      unchanged.

## 5. Distinguish 3 compare surfaces

- [x] **Done** — Sidebar `/servers/compare` → "Compare servers";
      `/diff` → "Source/target diff"; `/compare` → "Compare & select items".
      Nav/breadcrumb/title/button/link audited.

## 6. Honest state in compare page

- [x] **Done** — `accepted_target` → neutral (not success). `verified` success;
      `unresolved` warning; `manual_required` error.
- [x] **Done** — Honest fallback banner: per-item ExecutionState/
      VerificationState not tracked by backend yet (`migration_item_results`
      not built) → no fabricated badges.
- [ ] **Blocked by backend** — Actual per-item ExecutionState/VerificationState
      badges (requires `migration_item_results`, Phase 6C). Documented as
      blocker; honest fallback shipped instead.

## 7. Dependency / action honesty

- [x] **Done** — `apply_from_source` disabled + blocked in `requestAction`
      when unsatisfied hard dep; inline reason shown.
- [x] **Done (pre-existing, verified)** — `keep_target`/`skip` on
      `different`/`missing_on_target` require 6B7 confirm modal;
      `review_manual` stays manual.
- [x] **Done (pre-existing, verified)** — Bulk "apply safe" skips explicit
      decisions, ApplyLevel > Warn, unsatisfied hard deps (`parity_engine.go`).

## 8. Honest terminal status in pipeline

- [x] **Done** — `MigrationHeader.statusVisual()` maps all backend outcome
      statuses: only clean `completed` is full green; drift/manual-gaps/
      partial/manual-followup = amber; verification_failed/failed/
      rollback_degraded = red; rolled_back = neutral.
- [ ] **Needs live verification** — A selective-apply migration that reaches
      `completed_with_manual_gaps` renders amber "Completed — manual follow-up"
      (not green). Requires a live run; code path verified statically.

## 9. Empty / loading / error / stale states

- [x] **Done** — `/compare` already had loading/empty/error/stale+Refresh/
      save-failure rollback (UI-A audit). Added honest "not tracked yet" banner
      distinguishing verification-not-run from verification-failed at the
      item level (the latter is a *backend* gap, shown as neutral fallback).
- [x] **Done** — Pipeline reload reconciles to backend session (`loadSession`);
      wizard compare-summary failure cannot block plan creation (it is now
      honest copy, not a data fetch).

---

## Summary

- **Implemented + statically verified:** targets 1, 2, 3, 4, 5, 6 (tone +
  fallback), 7, 8, 9 baseline. 0 svelte-check errors; Go build/vet/test green.
- **Needs live verification:** 1 (summary counts), 8 (amber terminal for a real
  selective-apply migration).
- **Blocked by backend:** 6 per-item ExecutionState/VerificationState badges
  (`migration_item_results` not built) — honest fallback shipped.
- **Out of scope:** building `migration_item_results`; changing
  `initial_sync`/cutover/commit/rollback flow; G2 backend hard-dep enforcement
  at apply time.
