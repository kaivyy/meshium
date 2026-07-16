# Phase UI-A — UI Wording & State Semantics Audit

Companion to `ui-canonical-flow-audit.md`. Audit-only: terminology, state
semantics, misleading wording, badge/status confusion, and copy/layout
recommendations (no implementation). All claims cite real files/labels.

---

## Part 3 — Terminology / label audit

| Term | Where it appears | UI meaning | Backend meaning | Accurate? | Misleading risk |
|---|---|---|---|---|---|
| "Add Server" | `servers/new` h1 + button | register server | insert servers row | ✅ | none |
| "Test Connection" | `servers/[id]` line 577 | check connectivity + basic host profile | live SSH connect + auth-status + credential-health | ✅ | none |
| "Trigger Discovery" | `servers/[id]` (discoveryApi.triggerDiscovery) | start server inventory scan | write `discovery_snapshots` | ✅ but name collides (below) | user may think this is migration prep |
| "Discovery" (page) | `/discovery` title "Infrastructure inventory from discovery scans" | server inventory browser | reads `discovery_snapshots` | ✅ | none standalone |
| "Discovery" (stage) | `pipeline_models.go:39` `StageDiscovery = "discovery"` | pipeline pre-stage | backend stage name | ⚠️ | **Same word as onboarding discovery** → implies onboarding snapshot feeds migration |
| "Compare" (sidebar) | `Sidebar.svelte` `/servers/compare` label "Compare" | compare TWO servers' infra | `drift/compare` on two snapshots | ✅ but 1 of 3 "compare" meanings | user may expect migration compare |
| "Server Diff" | `migrations/[id]/diff` h1 + title | raw added/removed/changed diff | `migrationApi.diff` live source/target | ✅ as a diff | — |
| "Compare & Select" | `migrations/[id]/diff` button (line 222) → `/compare` | item-level select | `/compare` parity+selection | ⚠️ | **button on a raw-diff page implies diff = selection** |
| "Compare & Select" | `migrations/[id]/compare` h1 (line 193) | item-level parity + decision | `migrationApi.parity` + `migration_selections` | ✅ | — |
| "Dry Run" | `pipeline` `runDryRun` | preview changes | `wsDryRun` per-category diff | ✅ | none |
| "Compatibility Check" | `pipeline` h2 (line 1485) "Verify source and target are compatible" | preflight compat | compat service | ✅ | none |
| "Provision" / "Installed" / "Verified" | `pipeline` provision states (line 1720-1726) | target deps present/verified | `provisionStates` | ✅ | none |
| "Execute" / "Apply" | `pipeline` initial_sync; `/compare` "Apply from source" | run apply | `initialSyncStage.Apply` | ✅ | none |
| "Verify" | `pipeline` `health_verification` stage | post-apply health | verify layers | ✅ | risk: step "completed" ≠ verified (Part 6) |
| "Completed" | stepper `completed` state | stage done | step status `applied`/`completed` | ⚠️ | **completed step can read as "done & healthy"** though verification may be partial/failed |
| "Cutover" / "Commit" / "Rollback" | `pipeline` confirm modal (line 1997) | traffic move / finalize / revert | `traffic_switch` / commit / rollback state machine | ✅ | none |
| "Migrated" | (not found in canonical screens) | — | — | n/a | ensure never used loosely |
| "Success" | toasts ("Migration plan created", "Dry run completed") | operation finished | op finished | ✅ for the op, not for migration health | toast "completed" ≠ target healthy |

### Misleading wording — shortlist
1. **"Discovery" overloaded** — onboarding inventory (`servers/[id]`, `/discovery`) vs pipeline stage `discovery` (`pipeline_models.go:39`). Fix: show pipeline stage as "Pre-flight / Discovery" with a tooltip, or rename label to "Pre-flight".
2. **"Compare & Select" on `/diff`** — raw diff pagebutton implies diff = selection. Fix: relabel to "Open item-level compare" or move the button to the pipeline.
3. **Three "compare" surfaces** — `/servers/compare` (infra), `/migrations/:id/diff` (raw), `/migrations/:id/compare` (item-level). Fix: name them distinctly ("Compare servers", "Source/Target diff", "Compare & select items").
4. **"Server Diff" page titled diff but drives selection** — fix title/button alignment.

---

## Part 6 — State semantics audit (badge/status confusion)

### ObservedState (compare page)
- Badges: `statusLabel` map (`compare` line 98-117): same→Same, missing_on_target→Missing on target, different→Different, stale→Stale, unsupported→Unsupported, selected_for_apply→Selected, accepted_target→Keep target, skipped_by_user→Skipped, manual_required→Manual required, applied→Applied, verified→Verified, unresolved→Unresolved.
- `statusTone` (line 120): same/verified/accepted_target→success; missing/different→warning; unresolved/manual_required→error; skipped/stale/unsupported→neutral; else info.
- **Confusion risk:** `accepted_target` (Keep target) and `verified` both render `success` tone. A keep_target item is NOT verified/healthy — coloring it success can read as "done & good". Recommend `accepted_target` use `neutral`/`info`, not `success`.
- **Confusion risk:** `same` → success tone. `same` is observed, not verified — acceptable but should not be conflated with "verified".

### DecisionState
- Shown via decision radios (apply_from_source / keep_target / skip / review_manual) + `→ {action}` chip when a selection exists (`compare` line 208-210). ✅ distinct from observed.
- **Confusion risk:** `keep_target` chip shows as info `→ keep_target`; observed badge may still be `different` (warning). Two badges side by side is honest IF labeled — currently the row shows observed badge + level badge + `→ action` chip. Acceptable; verify label clarity in live run.

### ExecutionState
- **Not yet reflected per item in UI.** After apply, the `/compare` page does not show item-level `applied`/`failed`/`skipped`. Only the parity-summary 3-axis scores exist. The pipeline stepper shows step-level `completed`/`failed`. → **"applied" can look like "verified"** because the UI has no per-item execution/verification badge yet (blocked on `migration_item_results`, 6C).
- Required: once 6C `migration_item_results` lands, the `/compare` row must show ExecutionState (pending/blocked/skipped/applied/failed/rolled_back/partially_applied) distinct from ObservedState and VerificationState.

### VerificationState
- Only surfaced as parity-summary 3 scores (infra/runtime/app-health) on `/compare` (line 218-240) + honest "not a bare completed" note. No per-item `not_verified`/`verify_failed`/`unresolved` badge yet.
- **Confusion risk:** a migration can reach terminal "completed" (pipeline step) while app-health score < 100 and manual gaps > 0. The pipeline header must show the honest terminal status (`completed_with_manual_gaps` etc.), not a green "Completed". **Live run needed to confirm what the pipeline header renders for a selective-apply migration** — this is an explicit gap, not confirmed broken.

### Badge/status confusion list (consolidated)
1. `accepted_target` (keep_target) and `verified` both `success` tone → keep_target looks "done & healthy". Fix: neutral/info tone for accepted_target.
2. `same` → success tone conflates observed==target with verified.
3. No per-item ExecutionState badge → "applied" reads as "verified".
4. No per-item VerificationState badge → "completed" step reads as healthy.
5. `skipped_by_user` (skip) → neutral tone, but skip = unresolved (not done). Current neutral is honest; ensure summary counts it as burden (6C N).

---

## Part 7 — Dependency / risk / manual boundaries (UI honesty)

- `/compare` shows `applyLevel` (Safe/Warning/Guarded/Manual) + dependency list with satisfied/unsatisfied icons (hard unsatisfied = `CircleSlash` red). ✅ honest.
- 6B7 modal requires confirm for keep_target/skip on `different`/`missing_on_target`. ✅ guards overwrite/discard.
- Bulk "Auto-apply safe" limited to `applyLevel ≤ Warn` + satisfied hard deps (6B6). ✅ no auto-apply of guarded.
- **Gap to confirm:** whether `initial_sync` actually blocks a `catMixed` item with unsatisfied hard dep, or warns-only (6B4 `catMixed` applies whole category). If it applies despite unsatisfied hard dep, that violates the 6C "hard dep blocks apply" rule. **Needs code check / live run** — explicit gap.

---

## Part 8 — Empty / loading / error / stale (consolidated)

| Screen | Loading | Empty | Error | Stale | Restore |
|---|---|---|---|---|---|
| `/compare` | spinner ✅ | "Nothing to compare" ✅ | error banner ✅ | freshness banner + Refresh ✅ | GET selection + summary on load ✅ |
| `/migrations/new` | truthful state machine ✅ | — | failed/unknown ✅ | op-reconcile ✅ | op id in URL/session ✅ |
| `/pipeline` | stage spinners ✅ | — | dry-run unknown ✅ | stale WS refused for cutover/commit ✅ | backend session reload ✅ |
| `/diff` | load ✅ | empty diff ✅ | error ✅ | — | — |
| `/servers/compare` | ✅ | "No matching servers" ✅ | ✅ | — | — |

**Gap:** no explicit "verification not run" vs "verification failed" user-facing distinction at migration level (only parity-summary scores). Needs 6C `VerificationState` wiring.

---

## Recommendations (copy/layout, no implementation)

1. **Disambiguate "Discovery":** pipeline stage label shown to users = "Pre-flight" (keep backend constant `discovery`); tooltip: "Onboarding discovery is server inventory only — migration compare uses the plan-time collect, not discovery snapshots."
2. **Rename the three compare surfaces:** sidebar `/servers/compare` → "Compare servers"; `/migrations/:id/diff` → "Source/Target diff"; `/migrations/:id/compare` → "Compare & select items".
3. **Relabel `/diff` button** "Compare & Select" → "Open item-level compare" (or remove; on-ramp belongs on pipeline).
4. **Add pipeline on-ramp:** a "Compare & select" tab/link on `migrations/[id]/pipeline` → `/compare`.
5. **Wizard step 4:** add a light per-category "N differ / M same" summary (counts only) + link to full compare. No raw decisions in wizard.
6. **Badge tone fixes:** `accepted_target` → neutral/info (not success); keep `verified` success; add per-item ExecutionState + VerificationState badges post-apply (after `migration_item_results`).
7. **Honest terminal status:** pipeline header must render `completed_with_manual_gaps` / `completed_partial` / `completed_with_drift` distinctly (amber), never a single green "Completed" for selective-apply migrations with gaps.
8. **Confirm hard-dep block:** verify `initial_sync` honors unsatisfied hard deps at item level (code check / live run) before claiming UI honesty on dependencies.

---

## Explicit gaps requiring live run / code check (not confirmed from static audit)
- G1: What terminal status + header the pipeline shows for a selective-apply migration with manual gaps (expect `completed_with_manual_gaps`).
- G2: Whether `initial_sync` blocks a `catMixed` item whose hard dep is unsatisfied, or applies it (6B4 warns-only behavior).
- G3: Whether the `/compare` row after apply visually distinguishes observed `same` from `verified` (no per-item VerificationState badge exists yet).
