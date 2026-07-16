# Phase UI-A — Canonical Migration UI Audit (Onboarding → Wizard → Pipeline)

> Audit only. No implementation, no redesign. Every claim cites a real
> route / file / label / state. Goal: confirm the FE matches the canonical
> product flow and flag misleading UX, especially anything that implies
> onboarding `discovery_snapshots` feed migration compare/apply.

Canonical flow (target):
1. Add server / server detail
2. Test connection / optional discovery
3. Wizard `/migrations/new`
4. Create plan
5. Pipeline `/migrations/:id/pipeline`
6. Compare (item-level)
7. Select action
8. Apply
9. Verify
10. Cutover / Commit / Rollback

Backend source-of-truth facts (verified):
- Planner persists per-category collect into `migration_steps.data` at plan-time.
- Compare derives source from those persisted steps, target from a LIVE target collect. Onboarding `discovery_snapshots` are NOT used for migration compare/apply.
- Pipeline stages (`internal/mod/migration/pipeline_models.go:39-48`):
  `discovery → analysis → planning → validation → preparation → initial_sync →
  live_replication → health_verification → pre_cutover_validation → traffic_switch`.
  There is **no `compare` stage** — compare/select is a pre-`initial_sync` decision surface, currently implemented as a standalone route (`/migrations/:id/compare`).

---

## Part 1 — Screen map (what exists now)

| # | Route / file | Purpose (from code) | User decision | Data shown | Primary action | Secondary | SoT | Next step |
|---|---|---|---|---|---|---|---|---|
| 1 | `servers/new/+page.svelte` | Register a server + credential | Save server | host/port/auth form | "Add Server" | test inline? | servers table | server detail |
| 2 | `servers/[id]/+page.svelte` | Server detail + connection test + discovery | Validate connectivity; optionally inventory | Connection Test panel, System Information, Credential Status | "Test Connection" | "Trigger Discovery" (via discoveryApi), Refresh | live SSH + `discovery_snapshots` | wizard |
| 3 | `discovery/+page.svelte` | Infrastructure inventory from discovery scans | Trigger/inspect inventory | per-server snapshot cards | "Re-scan" | filters | `discovery_snapshots` | — |
| 4 | `servers/compare/+page.svelte` | Compare TWO servers' infrastructure | Pick source/target servers | Blockers / Warnings / diff tables | "Compare" | — | discovery snapshots (both sides) | — |
| 5 | `migrations/new/+page.svelte` | Wizard: source→target→categories→review | Choose source/target/categories; cutover mode | server list, category cards, DB config, policy badges, live plan frames | "Create Migration Plan" (step 4) | Next/Back, op-reconcile | `migration_steps` (after plan) | `/migrations/:id/pipeline` |
| 6 | `migrations/[id]/+page.svelte` | Redirect | — | — | — | — | — | → `/pipeline` |
| 7 | `migrations/[id]/pipeline/+page.svelte` | Full pipeline: dry-run, compat, provision, execute, verify, cutover, commit, rollback | Run stages; confirm cutover/commit/rollback | stepper (`session.stages`), dry-run diff, provision states, cutover checklist | Execute / Cutover / Commit / Rollback | Dry Run, Compatibility Check, Refresh | backend session + `migration_steps` | done |
| 8 | `migrations/[id]/diff/+page.svelte` | Raw server diff for a migration (source vs target) | inspect raw diff | added/removed/changed sections | "Compare & Select" (links to /compare) | — | `migrationApi.diff` (live source/target) | → `/compare` |
| 9 | `migrations/[id]/compare/+page.svelte` | **Item-level parity + selection (6B3)** | pick action per item | grouped parity matrix, drawer, bulk buttons | per-item decision radios; bulk "Auto-apply safe" | Refresh | `migrationApi.parity` (live target) + `migration_selections` | apply in pipeline |

---

## Part 2 — Validation against canonical flow

### A. Onboarding server
- `servers/new` = pure register/credential. ✅ matches.
- `servers/[id]` "Test Connection" = connectivity + basic host profile. ✅ matches.
- `servers/[id]` "Trigger Discovery" + `/discovery` = server-level inventory only (`discovery_snapshots`). ✅ correctly scoped as inventory, NOT a migration plan.
- **Boundary risk:** "Discovery" here means onboarding inventory; the pipeline ALSO has a stage named `discovery`. Two different "discovery" meanings in one product (see Part 3).

### B. Wizard migration
- `/migrations/new` steps 1-3 = source/target/categories. ✅ matches.
- Step 4 = review + create plan. ✅ matches.
- **Light compare summary?** The wizard shows NO per-category compare summary at all — step 3 only lists selected category *names* (line 683: `{cat}`), not a diff. So the canonical "light compare summary per category in wizard" is **absent**. The wizard jumps source/target/category → plan with zero compare feedback. This is a gap, but it errs on the safe side (no premature/raw compare shown).

### C. Pipeline
- `initial_sync` is the apply stage. ✅
- `health_verification` is the verify stage. ✅
- **Compare/Select location:** The item-level compare/select (Part 1 #9) is NOT wired into the pipeline page. `migrations/[id]/pipeline` has **no link** to `/migrations/:id/compare` (grep of the file returns zero `/compare` references). The only path to compare/select is from the `/diff` page button "Compare & Select". So a user in the pipeline sees Dry Run / Compatibility / Provision / Execute but no "Compare & Select items" entry point. **This is the primary navigation mismatch.**

---

## Part 3 — Wording / terminology audit (see separate doc for full table)

Highlights (full list in `ui-wording-and-state-audit.md`):
- **"Discovery"** used for (a) onboarding inventory (`servers/[id]` "Trigger Discovery", `/discovery` "Infrastructure inventory from discovery scans") and (b) a pipeline stage `discovery` (`pipeline_models.go:39`). Same word, different meaning → confusion risk that onboarding discovery feeds migration.
- **"Server Diff"** page (`/migrations/:id/diff`) titled "Server Diff" but its action button reads "Compare & Select" → conflates raw diff (added/removed/changed) with item-level selection.
- **"Compare"** in the sidebar (`/servers/compare`, label "Compare") is server-vs-server infra diff, NOT migration item-level compare. Three different "compare" surfaces: `/servers/compare` (infra), `/migrations/:id/diff` (raw diff), `/migrations/:id/compare` (item-level selection).
- **"Completed"** stepper state: pipeline marks `initial_sync` step `completed` after apply. The FE distinguishes running/completed/degraded/failed (pipeline page lines 99-100) — but the *migration* terminal status can be `completed_with_manual_gaps` / `completed_partial` (6B5). Need to confirm the pipeline page surfaces the honest terminal status, not a bare "Completed" (audit gap — live run needed to confirm what the pipeline header shows for a selective-apply migration).

---

## Part 4 — User decision hierarchy audit

| Stage | Correct decision | Where asked now | Mixed? |
|---|---|---|---|
| Add server | save server | `servers/new` ✅ | no |
| Test connection | validate connectivity | `servers/[id]` ✅ | no |
| Discovery | view inventory | `servers/[id]` / `/discovery` ✅ | no (but word collision, Part 3) |
| Wizard | source/target/categories + plan | `migrations/new` ✅ | no compare decision asked (gap, not error) |
| Compare | attitude to each drift/item | `/migrations/:id/compare` ✅ (but orphaned) | **misplaced**: only reachable via /diff |
| Apply | run chosen actions | pipeline `initial_sync` ✅ | no |
| Verify | check real result | pipeline `health_verification` ✅ | risk: "completed" step ≠ verified (Part 6) |
| Cutover | move traffic | pipeline `traffic_switch` ✅ | no |
| Commit/Rollback | finalize | pipeline confirm modal ✅ | no |

No screen mixes operational + compare + verification in one place. But **compare is disconnected from the pipeline** (Part 2-C), so the decision hierarchy has a missing on-ramp.

---

## Part 5 — Compare UX: wizard vs pipeline

- **Wizard** should show only a light per-category compare summary. Currently shows **none**. Recommended (no impl): a small "X items differ / Y same" per selected category after plan, with a link to the full compare. Today the wizard gives no compare signal at all.
- **Pipeline** should be the item-level compare/select center. The `/compare` page already does this well (grouped matrix, drawer, decision radios, bulk, dependency display, stale warning). But it is **not linked from the pipeline**.
- **Misplaced:** the "Compare & Select" entry point lives on `/diff` (raw diff), not on the pipeline. A user doing the canonical flow (pipeline) never sees the item-level compare unless they happen through /diff.

---

## Part 6 — State semantics in UI (see separate doc)

The `/compare` page already separates ObservedState (status badge), DecisionState (decision radios + `→ action` chip), ApplyLevel (level badge), ExecutionState (not yet reflected post-apply — see gap), VerificationState (summary scores only). The pipeline stepper uses step-level status (pending/running/completed/failed/...) which collapses item-level execution + verification into one "completed". **Gap:** after apply, the `/compare` page does not yet reflect `ExecutionState`/`VerificationState` per item (the `migration_item_results` table from 6C is not built). So item-level "applied vs verified" distinction is not yet visible in the UI — only the 3-axis parity-summary score exists.

---

## Part 7 — Dependency / risk / manual boundaries

- `/compare` correctly: shows `applyLevel` badge (Safe/Warning/Guarded/Manual), dependency list with satisfied/unsatisfied icons, hard-dep `CircleSlash` when unsatisfied, and a 6B7 confirmation modal for keep_target/skip on `different`/`missing_on_target`. ✅ honest.
- **Gap:** bulk "Auto-apply safe" (6B6) is correctly limited to `applyLevel ≤ Warn` + satisfied hard deps. ✅
- **Gap to confirm live:** whether the pipeline `initial_sync` actually *blocks* a `catMixed` item whose hard dep is unsatisfied, or warns-only (6B4 `catMixed` currently warns + applies whole category). The 6C contract says item-level block; the 6B4 implementation applies the whole category on mixed. **This is a real semantics gap** — needs live run / code check to confirm whether an unsatisfied hard dep can still be applied.

---

## Part 8 — Empty / loading / error / stale states

- `/compare`: has loading spinner, error banner, EmptyState "Nothing to compare", stale banner ("Target may have changed" via `freshness` + Refresh), unsaved/save-failed via toast. ✅ strong.
- `/migrations/new`: truthful plan state machine (idle/connecting/collecting/checking/completed/failed/unknown) with reconcile, no fake success. ✅ exemplary.
- `/pipeline`: dry-run unknown → "check results below or reload"; rollback reconciled from backend. ✅
- **Gap:** no explicit "verification not run" vs "verification failed" distinction surfaced to the user at the migration level (only the parity-summary 3-axis scores exist). Needs the 6C `VerificationState` wiring to be visible.

---

## Part 9 deliverables

Two docs:
1. `docs/superpowers/specs/ui-canonical-flow-audit.md` (this file)
2. `docs/superpowers/specs/ui-wording-and-state-audit.md`

## Part 10 — Direct answers

- **Ideal UI flow:** servers/new → servers/[id] (test/discovery) → migrations/new (wizard) → migrations/:id/pipeline → migrations/:id/compare (item-level) → apply (initial_sync) → verify (health_verification) → cutover/commit/rollback.
- **Onboarding screens:** `servers/new`, `servers/[id]`, `/discovery`, `servers/compare`.
- **Plan screen:** `migrations/new`.
- **Compare screen:** `migrations/:id/compare` (item-level) — currently orphaned.
- **Apply screen:** `migrations/:id/pipeline` `initial_sync` stage.
- **Verify screen:** `migrations/:id/pipeline` `health_verification` stage (+ parity-summary on /compare).
- **Cutover/commit/rollback:** `migrations/:id/pipeline` `traffic_switch` + confirm modal.
- **Misleading terms:** "Discovery" (onboarding vs pipeline stage), "Compare" (3 meanings), "Server Diff" button "Compare & Select".
- **Misplaced component:** the "Compare & Select" on-ramp on `/diff` instead of the pipeline; wizard shows no compare summary.
- **5 most important mismatches:**
  1. `/compare` (item-level select) is not linked from the pipeline — no on-ramp in the canonical flow.
  2. "Compare & Select" button lives on the raw `/diff` page, conflating diff with selection.
  3. Wizard shows zero compare feedback (no light per-category summary).
  4. "Discovery" overloaded (onboarding inventory vs pipeline stage `discovery`).
  5. Item-level `applied` vs `verified` not yet reflected in UI (no `migration_item_results`); "completed" step may read as done/healthy.
- **Minimum changes to align (no new flow):**
  1. Add a "Compare & Select" entry/tab on `migrations/[id]/pipeline` linking to `/migrations/:id/compare` (and reflect execution/verification state back).
  2. Relabel `/diff` button from "Compare & Select" to "Item-level compare" (or move it); keep `/diff` as raw diff only.
  3. Add a light per-category diff-count summary on wizard step 4 (counts only, no raw decisions).
  4. Disambiguate "Discovery": rename the pipeline stage label shown to users (e.g. "Pre-flight") or add a tooltip clarifying onboarding discovery ≠ migration source.
  5. After apply, surface honest terminal status (`completed_with_manual_gaps`/`completed_partial`) + per-item execution/verification state on `/compare` once `migration_item_results` exists (6C).
