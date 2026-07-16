# Phase 6C — Item Identity + Selection Semantics Contract (Part 3: FE/BE Contract)

Companion to Part 1 (identity) and Part 2 (semantics). This document is the wire contract: FE and BE MUST use identical type names, enum values, and field shapes. All enum strings here match `parity.go` / `model.go` constants and Part 2 §F.

---

## O. FE contract

### O.1 Compare matrix row contract

Each row renders one `ParityItem`. Required visible fields:

- **identity**: `DisplayName` (fallback `ItemKey`) + small muted `ItemKey`.
- **observed state**: badge from ObservedState enum (§F.1) — `same` / `missing_on_target` / `different` / `unknown` / `stale` / `unsupported` / `manual_required`.
- **suggested action**: `SuggestedAction` (engine default).
- **chosen decision**: `DecisionState` (explicit) — if `undecided`, show suggestion, not a decision.
- **dependency state**: per-dep `kind` + `satisfied`; aggregate `HardBlocked` flag.
- **apply level / risk**: `ApplyLevel` (1 safe / 2 warn / 3 guarded / 4 manual) as a labeled badge.
- **execution state**: `ExecutionState` (§F.3) when pipeline has run.
- **verification state**: `VerificationState` (§F.4) when verified.
- **stale state**: `Freshness == "stale"` → "stale" caption; row tinted.
- **warnings**: `Warnings[]` list.
- **manual follow-up note**: `ManualFollowup` for `review_manual`.

### O.2 Row actions

- **Toggle decision active** when: item is selectable (Part 1 §D.4) AND not `unsupported`/`manual_required` (those are forced `review_manual`).
- **Disabled** when: `HardBlocked == true` for `apply_from_source` toggle; `unsupported`/`manual_required` (no apply possible).
- **Modal confirm required** (6B7) when: decision ∈ {`keep_target`, `skip`} AND `ObservedState` ∈ {`different`, `missing_on_target`} (discarding real source / overwriting target).
- **Dependency drawer auto-open** when: item opened AND has any unsatisfied `hard` or `recommended` dep.
- **Risk acknowledge required** when: `ApplyLevel >= 3` (guarded/manual) AND decision = `apply_from_source` on `different` — operator ticks `RiskAcknowledged` in modal/inline.

### O.3 Bulk actions (final)

| Action | Semantics | Implemented via |
|---|---|---|
| Select all safe | `apply_from_source` for items with `ApplyLevel <= 2` AND `!HardBlocked` AND observed ≠ `same` | `BulkApply` policy `apply_safe` (exists in 6B6) |
| Accept target for selected | `keep_target` for selected guarded items that are `same` | `BulkApply` policy `accept_risky_unchanged` (exists) |
| Skip selected | `skip` for checked items | NEW bulk policy `skip_selected` |
| Mark review manual | `review_manual` for checked items | NEW bulk policy `review_manual_selected` |
| Clear decisions | reset selected to `undecided` | bulk `clear` |
| Filter unresolved | show `different`/`missing_on_target`/`unknown` | client filter |
| Filter blocked | show `HardBlocked == true` | client filter |
| Filter stale | show `Freshness == "stale"` | client filter |
| Filter manual-only | show `manual_required`/`review_manual` | client filter |

Bulk respects: explicit operator decisions are never overwritten (6B6 `explicit` map). Bulk `apply_safe` never touches `manual_required`/`unsupported`/`review_manual`.

### O.4 Visual language (explicit labels, not color-only)

| State | Label | Badge variant |
|---|---|---|
| `same` | "Same" | success |
| `accepted_target` (derived display) | "Keep target" | neutral |
| `skipped_by_user` / deferred | "Deferred" | neutral |
| `manual_required` | "Manual required" | error |
| `blocked` (hard dep) | "Blocked" + dep icon | error |
| `selected_for_apply` | "Apply from source" | info |
| `applied` | "Applied" | success |
| `partially_applied` | "Partially applied" | warning |
| `verify_failed` | "Verify failed" | error |
| `verified` | "Verified" | success |
| `unresolved` | "Unresolved drift" | warning |

Each carries an icon + text (never color alone) per §U.

### O.5 Empty / loading / error / stale states

- **Parity not computed**: EmptyState "Nothing to compare" (exists).
- **Parity stale**: banner "Target may have changed since last compare" + Refresh.
- **Target/source may have changed**: same stale banner; recompute recommended.
- **Decisions unsaved**: inline "unsaved" dot while `saving == true`; rollback on error with toast.
- **Save decision failed**: toast error; row reverts to previous decision.
- **Verification not run**: summary shows "Verification not run" caption.
- **Verification stale**: "verify results older than N" caption.
- **Compare unsupported for category**: row shows `unsupported` badge + "Not comparable by Meshium" note.

---

## P. BE contract / API / persistence

### P.1 Parity response (GET `/migrations/:id/parity`)

```jsonc
{
  "migrationId": 5,
  "items": [ /* ParityItem per Part 2 §E */ ],
  "freshness": "fresh",        // fresh | stale
  "computedAt": "RFC3339"
}
```

### P.2 Selection persistence

Table `migration_selections` (exists) — one row per `(migration_id, item_key)`:

```
id, migration_id, item_key, category, action, updated_at
```

`action` ∈ {apply_from_source, keep_target, skip, review_manual}. `PUT` upserts (ON CONFLICT). For 6C, ADD columns (non-breaking, nullable):
- `decision_reason TEXT` (required for keep_target/review_manual)
- `risk_acknowledged BOOLEAN DEFAULT FALSE`
- `manual_followup TEXT`

### P.3 Verification summary (GET `/migrations/:id/parity-summary`)

Extend existing `ParitySummary` with the 5 axes (Part 2 §N):

```jsonc
{
  "migrationId": 5,
  "observedParity": 0-100,
  "decisionCoverage": 0-100,
  "executionCompletion": 0-100,
  "verificationConfidence": 0-100,
  "manualDeferredBurden": 0-100,
  "acceptedDrift": int,
  "unresolvedDrift": int,
  "manualGaps": int,
  "failed": int,
  "passed": int,
  "computedAt": "RFC3339"
}
```

### P.4 Audit trail of decision changes

6C recommends a **new table** `migration_selection_history` (append-only):
```
id, migration_id, item_key, from_action, to_action, reason, actor, created_at
```
Written on every `PUT selection` that changes an existing decision. Gives true audit trail without mutating `migration_selections` history.

### P.5 Summary counters

`parity-summary` returns the bucket counts (acceptedDrift / unresolvedDrift / manualGaps / failed / passed) used by FE amber/red badges (Part 2 §M).

### P.6 Endpoints (final)

| Method | Path | Purpose |
|---|---|---|
| GET | `/migrations/:id/parity` | compute + return items (live target collect) |
| GET | `/migrations/:id/selection` | list all decisions |
| PUT | `/migrations/:id/selection` | upsert one decision `{itemKey, category, action, decisionReason?, riskAcknowledged?}` |
| PUT | `/migrations/:id/selection/bulk` | bulk `{policy}` → `apply_safe` | `accept_risky_unchanged` | `skip_selected` | `review_manual_selected` | `clear` |
| GET | `/migrations/:id/parity-summary` | 5-axis verification summary |
| POST | `/migrations/:id/parity/recompute` | force re-collect (clears stale) |
| GET | `/migrations/:id/follow-up` | list `skip`+`review_manual`+`unknown`+`verify_failed` items |

### P.7 Persistence recommendation (direct answer)

**Hybrid** (not `migration_selections` alone, not step-field alone):

- `migration_selections` — sufficient for decision state (extend with reason/ack/followup columns). Keep.
- **ADD `migration_item_results`** — per-item execution + verification evidence, keyed `(migration_id, item_key)`:
  ```
  id, migration_id, item_key, execution_state, last_execution_at,
  step_refs TEXT, verification_state, verification_level, verify_evidence,
  backup_ref TEXT, created_at, updated_at
  ```
  This is REQUIRED for honest rollback (§K) and for item-level `partially_applied` / `verify_failed` without overloading the coarse step status.
- `migration_steps` — keep coarse; do NOT add per-item fields. Step status stays `applied`/`skipped`/`failed`; fine-grained state lives in `migration_item_results`.
- `migration_selection_history` — append-only audit (§P.4).

### P.8 Backward compatibility

- Existing migrations without `migration_item_results` rows: pipeline treats them as pre-6C; item-level execution state derived best-effort from step status (applied step → items `applied`; skipped step → items `skipped`). No migration of old data required.
- `migration_selections` UNIQUE(migration_id, item_key) unchanged; new columns nullable → old rows valid.
- FE restore on reload: GET `/selection` + GET `/parity-summary` repopulate `selections` map and summary (already wired in 6B3 compare page `load()`).

---

## Direct answers (§T / §P)

- **FE row contract:** §O.1. **Interactions:** §O.2. **Bulk:** §O.3. **Visual states:** §O.4. **Empty/error/stale:** §O.5.
- **BE shapes:** §P.1–P.5. **Endpoints:** §P.6.
- **Persistence:** hybrid — keep `migration_selections` (extended), ADD `migration_item_results` (execution/verify evidence) + `migration_selection_history` (audit). Do NOT put per-item state in `migration_steps`.
- **Backward compat:** derived best-effort from step status for pre-6C rows; no data migration.
- **FE reload restore:** GET selection + summary (wired).
- **Unsafe/deferred:** app/secret/cert/dns/runtime/hybrid stay `manual_required` placeholder (Part 2 §R).
