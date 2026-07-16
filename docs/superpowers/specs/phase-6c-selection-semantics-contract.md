# Phase 6C — Item Identity + Selection Semantics Contract (Part 2: Selection Semantics)

Companion to Part 1 (identity) and Part 3 (FE/BE contract). Defines action meaning, the four state dimensions, the keep_target/skip split, and apply/rollback/verify/migration-success/score consequences. All domain terms here MUST match Part 3 and the Go constants in `parity.go` / `model.go`.

---

## E. Parity item domain model (final shape)

The current `ParityItem` (category, itemKey, sourceValue, targetValue, status, suggested, applyLevel, deps, warnings) is **insufficient**: it fuses observed state + decision + execution + verification into one `status`. 6C splits into 4 dimensions (§F) carried on one object. Final `ParityItem`:

```go
type ParityItem struct {
    // 1. Identity
    Category     string  // "packages" | "configs" | ...
    ItemKey      string  // per Part 1 §D.2
    DisplayName  string  // human label (package name / path / db name)
    GroupKey     string  // "" or parentKey (e.g. compose-project:<x>, database-engine:<e>)
    Granularity  string  // "item" | "group" | "category" | "manual-placeholder"

    // 2. Observed comparison (computed by ComputeParity; immutable until recompute)
    SourceValue  string  // normalized source representation (NEVER raw secret)
    TargetValue  string  // normalized target representation
    ObservedState string  // §F.1 enum
    Freshness    string  // "fresh" | "stale"
    CompareNotes string  // free text, e.g. "target config differs; apply overwrites"

    // 3. Decision (operator layer; persisted in migration_selections)
    DecisionState    string // §F.2 enum; default derived from ObservedState (§G.5)
    SuggestedAction  string // engine default (apply_from_source / keep_target / review_manual)
    DecisionReason   string // operator note (required for keep_target/review_manual)
    RiskAcknowledged bool   // operator ticked risk box for guarded/different

    // 4. Execution (pipeline layer; derived from step outcome)
    ExecutionState   string // §F.3 enum
    LastExecutionAt  string // RFC3339 or ""
    StepRefs         []int  // migration_steps.id this item maps to
    ExecutionNotes   string

    // 5. Verification (post-apply probe layer)
    VerificationState string      // §F.4 enum
    VerificationLevel string      // "infra" | "runtime" | "app" | "" (manual-placeholder)
    VerifyEvidence    string      // one-line honest evidence
    VerifyNotes       string

    // 6. Dependency / risk
    ApplyLevel     int             // 1 safe | 2 warn | 3 guarded | 4 manual
    Deps          []DependencyRef  // kind: hard|recommended|verify_post|external
    HardBlocked   bool             // any unsatisfied hard dep
    Warnings      []string
    ManualFollowup string          // runbook hint for review_manual
}
```

**Required vs optional:**
- Required: Category, ItemKey, ObservedState, DecisionState, ApplyLevel.
- Optional/derived: DisplayName (default = ItemKey), GroupKey (""), all execution/verification fields (default to `not_applicable`/`not_verified` until pipeline runs).
- `SourceValue`/`TargetValue` MUST be normalized strings; for secrets/cert/dns placeholders they are redacted (`"<redacted>"`) — never the raw value.

---

## F. Four state dimensions (never merged)

### F.1 ObservedState (computed by compare; recomputed on parity refresh)

| State | Meaning | Recomputed when |
|---|---|---|
| `same` | source value == target value | every parity compute |
| `missing_on_target` | present on source, absent on target | every parity compute |
| `different` | present both sides, values differ | every parity compute |
| `unknown` | compare could not run (target unreachable, collector error) | on error |
| `stale` | last computed result older than freshness window (target may have drifted) | on timed re-check |
| `unsupported` | Meshium cannot compare/apply this item yet | static (category/placeholder) |
| `manual_required` | compare possible but apply needs human (placeholder categories) | static for placeholder |

Note: `accepted_target` / `applied` / `verified` / `skipped_by_user` / `selected_for_apply` from current `ParityStatus` are **NOT observed states** — they are decision/execution/verification states (§F.2–F.4). 6C keeps the observed dimension to the 7 values above to stop the one-badge confusion.

### F.2 DecisionState (operator layer)

| State | Meaning |
|---|---|
| `undecided` | no selection recorded; FE shows suggested action |
| `apply_from_source` | operator wants source state on target |
| `keep_target` | operator accepts target deviation as intentional |
| `skip` | operator defers; drift stays unresolved |
| `review_manual` | operator flags for manual handling |

Decision may be **implicit default** (derived, shown as suggestion) until the operator acts; once `PUT selection` is called, it is explicit and persisted. Default policy: §G.5.

### F.3 ExecutionState (pipeline layer; maps to existing step status)

| State | Maps to step status | When |
|---|---|---|
| `not_applicable` | — | manual-placeholder / keep_target / skip |
| `pending` | `pending`/`running` | step queued/running |
| `blocked` | (step not started) | hard dep unsatisfied → item cannot apply |
| `skipped` | `skipped` | decision keep_target/skip → step skipped |
| `applied` | `applied` | apply ran to completion |
| `failed` | `failed` | apply errored |
| `rolled_back` | (rollback path) | rollback reverted this item |
| `partially_applied` | n/a at step level* | step = many items, only some applied |

*A step representing N items cannot show `partially_applied` in a single step-status string; partial state lives at **item level** (`partially_applied` on the item) while the step shows `applied` if ≥1 applied or `failed` if all failed. See §J.6.

Item enters `blocked` when `HardBlocked == true` (unsatisfied hard dep) at apply time — pipeline must NOT apply it even if decision = apply_from_source.

### F.4 VerificationState (post-apply probe)

| State | Meaning |
|---|---|
| `not_verified` | no probe run / not applicable |
| `infra_verified` | target state exists (file present, pkg installed, container up) |
| `runtime_verified` | target state matches + runs (service active, db connectable) |
| `app_verified` | app-level health confirmed (HTTP OK / business check) |
| `partial_verified` | some layers passed, not all |
| `verify_failed` | probe ran, target still wrong / app unhealthy |
| `unresolved` | applied but no probe available (never counted as healthy) |

Honest rule: `applied` ≠ any `verified`. An item with no reachable probe stays `unresolved`/`not_verified` — never auto-promoted to green.

---

## G. Final selection semantics (§B.4 / §G)

### G.1 `apply_from_source`
- **Meaning:** bring the source representation of this item onto the target (install pkg, write config, enable service, create volume/container, restore DB).
- Valid only for **selectable** items (Part 1 §D.4 list). Placeholder/unsupported items reject it.
- Valid for `same`? Yes but pointless — engine still allows; parity shows "already same" and apply is a no-op copy. Bulk-safe skips `same` (no change needed).
- Valid for `different` / `missing_on_target`? **Yes** — this is the primary case.
- Valid for `manual_required`? **No** — rejected; must be `review_manual` or handled externally.
- Prerequisite: all `hard` deps satisfied (`HardBlocked == false`). Otherwise item is `blocked`, not applied.
- Rollback: **yes** — applied items auto-enter rollback scope (backup taken before apply).
- Failed apply: item stays `selected` (decision unchanged) but `ExecutionState = failed`. It is counted as "selected but execution failed", surfaced in follow-up, not silently dropped.

### G.2 `keep_target`
- **Meaning:** operator ACCEPTS the target's current (differing) state as intentional deviation. The source representation is NOT applied.
- It is acceptance of difference, explicitly.
- It moves the item from *unresolved drift* to *accepted drift* — it does NOT make source==target. ObservedState remains `different`/`missing_on_target`; DecisionState = `keep_target`; a new derived display `accepted_target` is shown for operator clarity only (NOT an observed state).
- Parity summary: counted in `acceptedDrift` bucket, not `unresolvedDrift`. Migration success may be `completed_with_drift` (drift accepted) — honest because drift is acknowledged.
- Rollback: **NOT in rollback scope** (we never touched target).
- MUST store `DecisionReason` (operator note) + `RiskAcknowledged = true`. FE requires the confirm modal (6B7) for `different`/`missing_on_target`.

### G.3 `skip`
- **Meaning:** operator defers — no action now, no acceptance. Drift remains **unresolved**.
- Skip IS unresolved (unlike keep_target). Appears in follow-up / unresolved list.
- Distinct from `undecided`: `undecided` = not yet chosen (still shows suggestion); `skip` = explicitly deferred (no suggestion shown, "deferred" badge).
- Skip does NOT block execute eligibility of *other* items; the category step still runs (skipped items within it are not applied).
- Parity score: counted in `unresolvedDrift` / `manualDeferred` burden bucket. Never in "applied" or "verified".

### G.4 `review_manual`
- Explicit operator mark: item needs human handling outside Meshium.
- Fully out of auto-apply path (bulk-safe never touches it).
- Stays in verify / follow-up list until external completion evidenced.
- **Requires** `ManualFollowup` runbook hint (auto-suggested from depmap/notes) — FE shows it; operator may edit.
- CANNOT be marked "done" by the system without explicit external confirmation. In 6C there is no external-completion ingest; such items remain `review_manual` / `unresolved` until a future confirmation model (Part 3 §P notes `migration_item_results`).

### G.5 Default decision policy (FE must not guess)

| ObservedState | Default DecisionState | SuggestedAction |
|---|---|---|
| `same` | `undecided` (implicit) | `keep_target` (no change needed) |
| `missing_on_target` | `undecided` | `apply_from_source` |
| `different` | `undecided` | `apply_from_source` (guarded items → warn, require ack) |
| `stale` | `undecided` | recompute first; treat as `different` suggestion |
| `unknown` | `undecided` | `review_manual` |
| `manual_required` | `undecided` | `review_manual` |
| `unsupported` | `undecided` | `review_manual` |

Once operator acts, explicit decision overrides default permanently (persisted).

---

## H. keep_target vs skip (the hard split)

**Final:** `keep_target` = operator ACCEPTS deviation as intentional. `skip` = operator DEFERS; drift stays unresolved. This IS final — it is the best model because it preserves two semantically different audit intents that product honesty requires: "I looked at this difference and chose the target" vs "I am not dealing with this now." Collapsing them hides which one happened.

| Aspect | keep_target | skip |
|---|---|---|
| Audit trail | "accepted deviation, reason stored" | "deferred, no acceptance" |
| Parity summary | `acceptedDrift` (resolved-intentionally) | `unresolvedDrift` / `manualDeferred` (open) |
| Migration success | allows `completed_with_drift` | forces `completed_with_unresolved_drift` or `completed_with_manual_followup` |
| Future re-compare | item still `different` but DecisionState=keep_target → not re-flagged as new drift | item still `different`, skip → still flagged open |
| Verify | no infra probe needed (we didn't change target); `not_verified` acceptable | no probe; `unresolved` |
| Rerun execute | step skipped (already decided) | step skipped |
| Rollback | NOT in scope (untouched) | NOT in scope (untouched) |
| Operator communication | "Target retained intentionally" | "Deferred — follow up" |
| Product honesty | honest: source≠target but deviation approved | honest: gap remains visible |

---

## I. Item ↔ Step ↔ Apply mapping (§B.3 / §I)

1. **One step = many items** for every collectable category today (`initialSyncStage` iterates categories; one apply step per category).
2. Categories currently 1 step = N items: **all six** (packages, configs, services, users, docker, database).
3. **Best option = HYBRID per category** (do NOT force one model):
   - Item-level categories (packages, configs, services, users, docker images/volumes/containers, database data): keep the coarse step (backward-compatible) but store **per-item selection** in `migration_selections`. Pipeline reads selections and applies only selected items *within* the category step. (`initialSyncStage` already buckets selection → `catSkip`/`catApply`/`catMixed`; 6C extends `catMixed` to apply selected items and skip others inside the step instead of warning-only.)
   - category-level only (database engine): engine install is part of the DB step, not a separate item.
   - manual-placeholder: no step work; item exists only in parity/selection for visibility.

| Category | Current step granularity | Proposed selectable unit | Execution mapping | Rationale |
|---|---|---|---|---|
| packages | 1 step / category | item | apply selected pkgs in step | safe, independent |
| configs | 1 step / category | item | apply selected files in step | independent files |
| services | 1 step / category | item | enable selected units in step | per-unit atomic |
| users | 1 step / category | item | create selected users in step | independent |
| docker images | 1 step / category | item | pull selected images in step | safe |
| docker volumes | 1 step / category | item | create+copy selected in step | guarded |
| docker containers | 1 step / category | item | recreate selected in step | guarded, dep on image |
| compose files | 1 step / category | item (group) | apply compose file in step | guarded |
| database engine | 1 step / category | category (anchor) | installed by DB step | prerequisite |
| database data | 1 step / category | item | restore selected DBs in step | real data, guarded |
| runtime/app/secret/cert/dns | none | placeholder | none (visibility only) | unsafe to auto-apply |

---

## J. Apply consequences & step status (§J)

| Decision | Low-level apply behavior | Item executionState | Step status impact | Notes |
|---|---|---|---|---|
| `apply_from_source` | apply runs for this item | `applied` (or `failed`) | step → `applied` if ≥1 applied; `failed` if all fail | unsatisfied hard dep → item `blocked`, not applied |
| `keep_target` | NO apply | `skipped` | step may still run for OTHER selected items; this item skipped | item not in rollback scope |
| `skip` | NO apply | `skipped` | step runs for other items; this skipped | drift stays unresolved |
| `review_manual` | NO apply | `not_applicable` | step runs for other items; this skipped | stays in follow-up |

- One `StepStatusSkipped` is sufficient at step level for "whole category skipped" (catSkip). For `catMixed` (some selected, some not) the step is **not** skipped — it runs and applies selected items; unselected items get item-level `skipped` without changing the step status away from `applied`/`running`.
- `keep_target` and `skip` MAY both map to item-level `skipped` at low level BUT remain distinct in `DecisionState` (item-level audit) and in parity summary buckets. Honest: low-level step doesn't care why; audit does.
- `partially_applied` lives at **item level** (§F.3). Step level shows `applied`/`failed` only.

---

## K. Rollback semantics (§K)

- Rollback ONLY for items with `ExecutionState == applied` (truly applied). Source of truth = item execution evidence + step history.
- `keep_target` items: NOT in rollback scope (untouched).
- `skip` items: NOT in rollback scope.
- `review_manual` items: NOT in rollback scope (nothing applied) unless a future external-confirmation model marks them applied.
- Partial apply failure: item marked `failed` (not `applied`) → excluded from rollback; the category step's successful items roll back independently.
- Category step with N items, only some applied: rollback scope = exactly the applied subset (computed from item execution state, not the coarse step status).
- **Per-item applied evidence:** 6C requires persisting minimal applied evidence (what was backed up / what was written) — recommend `migration_item_results` table (Part 3 §P) keyed by `(migration_id, item_key)`. Without it, rollback must re-derive from step history, which is coarser.
- Honest rollback limit for manual/guarded: if a guarded item was applied but its *downstream* effect (e.g. app health) failed, rollback reverts the item but cannot undo external side effects — flagged in rollback report.

---

## L. Verification semantics (§L)

Levels: `infra` (state exists) < `runtime` (state runs) < `app` (business health) < `manual` (external confirmation).

| Category | Min verify level | Evidence | Honest success boundary | Notes |
|---|---|---|---|---|
| packages | infra | `dpkg -l <pkg>` present | pkg installed | not "app runs" |
| configs | infra | file hash matches source | file present + identical | NOT runtime ready |
| services | runtime | `systemctl is-active` | service active | exists≠runnable honored |
| users | infra | user exists on target | account present | no home-data guarantee |
| docker images | infra | `docker image inspect` | image present | build context ≠ present |
| docker volumes | infra | volume exists + data size | volume + data | data integrity not deep-checked |
| docker containers | runtime | container running | container up | depends on image+config |
| compose files | runtime | `docker compose ps` healthy | stack up | multi-container |
| database data | app | row-count / table list matches source | data synced | discovered≠synced honored |
| runtime anchor | manual | operator confirms install | external | placeholder until collector |
| app/secret/cert/dns | manual | external confirmation | none in-system | placeholder |

- `keep_target` item: verification = `not_verified` (acceptable; we didn't change target). Not counted as healthy.
- `skip` item: `unresolved` (open).
- `review_manual` item: `unresolved` until external confirmation (future model).
- applied but app-health failed: `verify_failed` (NOT `verified`). Migration success cannot be `completed` green.
- applied but target still `different` after recompute: `verify_failed`/`unresolved` — recompute may show the apply didn't take (e.g. config overwritten post-apply by another process).

---

## M. Migration success semantics (§M)

Statuses (aligned to existing `model.go` constants; `completed_with_drift` already exists, ADD `completed_with_unresolved_drift`):

| Status | Definition | Set by | Condition | User ack? | FE display |
|---|---|---|---|---|---|
| `completed` | all selected applied + verified, no gaps | `deriveMigrationStatus` | no failed, no manual gaps, no unresolved drift | no | green "Completed" |
| `completed_with_drift` | done; drift **accepted** via keep_target | pipeline | ≥1 keep_target, 0 skip/manual, 0 failed | yes (reason stored) | amber "Completed — drift accepted" |
| `completed_with_manual_followup` | done; review_manual items remain | pipeline | ≥1 review_manual, 0 failed, 0 unresolved | no | amber "Completed — manual follow-up" |
| `completed_with_unresolved_drift` | done; skip/unknown remain open | pipeline | ≥1 skip/unknown unresolved | no | amber "Completed — unresolved drift" |
| `completed_partial` | some selected applied, some failed non-fatally | pipeline | ≥1 failed but pipeline continued | no | amber "Partially applied" |
| `verification_partial` | applied but not all verify layers passed | verify layer | applied + partial_verified | no | amber "Verified partially" |
| `verification_failed` | applied but verify_failed present | verify layer | ≥1 verify_failed | no | red "Verification failed" |
| `failed` | pipeline hard-failed | pipeline | fatal error | n/a | red "Failed" |

Rules (§U honesty):
- keep_target NEVER makes source==target in any report.
- skip NEVER counted as done.
- manual_required NEVER silently dropped from summary.
- applied without verify NEVER full green.

---

## N. Parity score (honest, multi-axis) (§N)

Do NOT use one percentage. 6C defines 5 axes (0–100 each):

1. **ObservedParity** = fraction of items `same` among all compared (excludes unsupported/manual_required).
2. **DecisionCoverage** = fraction of items with explicit decision (not `undecided`).
3. **ExecutionCompletion** = fraction of apply_from_source items with `applied` (not failed/blocked).
4. **VerificationConfidence** = fraction of applied items with `verified` (any layer) — `unresolved`/`not_verified` lower it; never infers health.
5. **ManualDeferredBurden** = fraction of items `skip` + `review_manual` + `unknown` (higher = more open work; shown as a *burden* number, not a success).

Bucket assignment (direct answer to §N bullets):

| Item | ObservedParity | DecisionCoverage | ExecutionCompletion | VerificationConfidence | ManualDeferredBurden |
|---|---|---|---|---|---|
| `same` | counts (same) | counts if decided | n/a (no apply) | n/a | no |
| `keep_target` | no (still different) | counts (decided) | n/a | n/a | no (accepted, not burden) |
| `skip` | no | counts (decided) | n/a | n/a | **yes** |
| `review_manual` | no | counts (decided) | n/a | n/a | **yes** |
| `applied unverified` | no (if was different) | counts | counts (applied) | **no** (lowers) | no |
| `applied verify_failed` | no | counts | counts | **no (failed)** | no |
| `manual done outside` (no evidence) | no | counts | n/a | **no** | **yes** (until external confirmation model) |

FE shows 4 green-ish axes + 1 burden axis explicitly; no single composite score hides drift.

---

## Q. Dependency & blocking policy (§Q)

Dependency kinds: `hard`, `recommended`, `verify_post`, `external/manual`.

| Kind | Effect on selection | Effect on apply | Effect on FE | Effect on verify |
|---|---|---|---|---|
| `hard` | must be satisfied or item `blocked` | apply blocked until satisfied | auto-open dep drawer; disable apply toggle if unsatisfied | verify waits on dep |
| `recommended` | suggested alongside; not required | allowed without | warning badge | informational |
| `verify_post` | n/a | n/a | n/a | verification must run after apply |
| `external/manual` | item likely `review_manual` | blocked (manual) | placeholder note | manual confirmation |

Rules:
- `hard` dep unsatisfied → `apply_from_source` is **blocked** (item `blocked`, not applied). FE MUST disable the apply toggle (§U: never enable apply with unsatisfied hard dep).
- `keep_target` ALWAYS allowed (no apply, no dep needed).
- `skip` ALWAYS allowed.
- `review_manual` ALWAYS allowed.
- FE auto-opens dependency drawer when an item has an unsatisfied `hard` or any `recommended` dep and the operator opens the item.
- System MAY auto-select a `hard` dependency's `apply_from_source` when the operator selects the dependent item (opt-in, shown as "also selected dependency X") — never silent.
- System only WARNS (does not block) for `recommended`.

---

## R. Unsafe / deferred scope (§R)

| Scope | Why unsafe now | Disposition | Future path |
|---|---|---|---|
| app code | no collector; build context not captured | `manual_required` placeholder | app-collector + build-context capture |
| PM2 dump | process manager state not structured | `manual_required` | PM2 export collector |
| runtime install / version coupling | no runtime inventory collector | `runtime:*` anchor only | runtime collector |
| secrets / env values | raw secrets in collected data; unsafe to compare/persist/display | `secret-placeholder:*`; redacted values | secret-vault integration, never raw |
| cert issuance | no cert collector; issuance is external | `cert-placeholder:*` | cert-manager integration |
| DNS cutover | no DNS collector; cutover is external+downtime | `dns-placeholder:*` | DNS provider API |
| local build contexts | not captured by image collect | part of `manual_required` | build-context collector |
| complex compose projects | multi-stack, networks, secrets | `compose-file:*` item but guarded; complex ones → `review_manual` | richer compose analyzer |
| grouped DB restore edge cases | partial restore / cross-engine | `database:*` item, guarded; edge → `review_manual` | per-engine restore validation |
| external SaaS secrets | outside server scope | `secret-placeholder:*` | out-of-band |

All above stay `manual_required` / placeholder; never auto-applied.

---

## S/T direct answers

- **Final item unit per category:** see Part 1 §D.2 / table.
- **Final itemKey per category:** `package:<name>`, `config:<abs-path>`, `service:<unit>`, `user:<name>`, `docker-image:<repo>:<tag>`, `docker-volume:<name>`, `docker-container:<name>`, `compose-file:<abs-path>` (NEW), `database:<engine>:<db>`. Anchors: `database-engine:<e>`, `runtime:<n>`. Placeholders: `app:*`/`secret-placeholder:*`/`cert-placeholder:*`/`dns-placeholder:*`/`hybrid-placeholder:*`.
- **Granularity:** item-level executable = packages, configs, services, users, docker images/volumes/containers, compose files, database data. category-level only = database engine, runtime anchor. manual-placeholder = app/secret/cert/dns/hybrid/unknown runtime.
- **Action meanings:** §G.1–G.4.
- **keep_target vs skip:** §H (accepted vs deferred).
- **State dimensions:** §F (ObservedState / DecisionState / ExecutionState / VerificationState).
- **Mapping to apply/rollback/verify:** §I/§J/§K/§L.
- **Migration success:** §M.
- **Parity score:** §N (5 axes, no single composite).
- **Unsafe/deferred:** §R.
