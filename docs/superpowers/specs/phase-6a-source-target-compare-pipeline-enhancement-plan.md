# Phase 6A — Source vs Target Compare + Selective Apply: Enhancement Plan

> Companion to `phase-6a-source-target-compare-pipeline-audit.md`. This is the DESIGN/PLAN (no implementation here). Goal: extend the canonical wizard+pipeline flow with compare → select → apply-selected → verify-parity, without breaking existing paths.

**Architecture:** Reuse existing primitives — `DiffService` (`diff.go`), `DryRun` persistence pattern (`dryrun.go`), `category_meta.go` honesty contract, `Pipeline` 14-stage machine (`pipeline.go`), `VerificationResult` model (`pipeline_models.go:280`), `reuse.go` freshness gate. Add: `ParityItem` model, `ComputeParity` (extend `computeDiff`), selection persistence, `initialSyncStage` selective-apply, deepened `healthVerificationStage`.

**Tech Stack:** SvelteKit FE + Go `internal/mod/migration/*`.

---

## 1. Proposed Architecture

```
┌─ Wizard (/migrations/new) ─────────────────────────────┐
│ Step 3/4: reuse DiffService for COMPARE SUMMARY         │
│  - per-category delta count (added/removed/changed)     │
│  - honesty disclosure (category_meta, reuse warnings)   │
│  - category checkboxes (existing)                       │
└───────────────────────┬────────────────────────────────┘
                        │ plan (ws/plan) → migration_steps (per-item collect)
                        ▼
┌─ Pipeline (/migrations/:id/pipeline) ──────────────────┐
│ TAB: Compare   → ComputeParity(source steps, target)    │
│   matrix: item | src | tgt | status | action | risk     │
│   dependency drawer (DependencyRef)                      │
│ TAB: Apply     → selection persisted; initialSyncStage  │
│   applies only action=apply_from_source; skips others   │
│ TAB: Verify    → deepened healthVerificationStage       │
│   parity score: matched/failed/unresolved/manual        │
└──────────────────────────────────────────────────────────┘
```

No new execution path. `initialSyncStage` (`pipeline.go:1560`) already loops `migration_steps` and calls `mod.Applier.Apply` per step; we add a pre-check: read selection; if `keep_target`/`skip` → mark step `StepStatusSkipped` (new status) and continue; if `apply_from_source` → Apply as today. `StepStatusApplied` guard (`pipeline.go:1642`) preserved.

---

## 2. Backend Enhancement Plan

### 2.1 Normalized inventory + parity model (NEW `parity.go`)

```go
// ParityItem is one comparable, selectable unit.
type ParityItem struct {
    Category    string `json:"category"`
    ItemKey     string `json:"itemKey"`     // stable id, e.g. "service:pm2-kancasoft"
    SourceValue string `json:"sourceValue"` // human-readable
    TargetValue string `json:"targetValue"`
    Status      string `json:"status"`      // same|missing_on_target|different|stale|unknown|manual_required|unsupported
    Suggested   string `json:"suggested"`   // apply_from_source|keep_target|skip|review_manual
    ApplyLevel  int    `json:"applyLevel"`  // 1 safe | 2 warn | 3 guarded | 4 manual
    Deps        []DependencyRef `json:"deps"`
    Warnings    []string `json:"warnings"`
    Freshness   string `json:"freshness"`  // fresh|stale
}

type DependencyRef struct {
    ItemKey    string `json:"itemKey"`
    Kind       string `json:"kind"`     // hard|recommended|verify_post
    Note       string `json:"note"`
    Satisfied  bool   `json:"satisfied"` // computed against target inventory
}
```

Source of truth: `migration_steps.data` (collect, source side) + live target collect (reuse `DiffService` `mod.Collector.Collect(ctx, targetSSH)`). Compute delta per item, not just per-category string list.

### 2.2 Compare engine — extend `DiffService`

- Add `ComputeParity(ctx, sourceID, targetID, migrationID, categories) (*ParityResult, error)`.
- Reuse `DiffService.Diff`'s dual-collect (`diff.go:112-119`) but produce `ParityItem[]` with values + `ApplyLevel` (map from `category_meta.go` + per-item rules) + `DependencyRef` (new `depmap.go`).
- Freshness: reuse `planReuseMaxAge` (`reuse.go:15`); if target snapshot older → `Freshness:"stale"`.
- Route: extend `POST /api/diff` (already `handler.go:55`) to accept `migrationId` and return `ParityResult` instead of raw `DiffResult`. Keep `DiffResult` for the read-only `/migrations/[id]/diff` page.

### 2.3 Selection persistence (NEW table `migration_selections`)

```sql
CREATE TABLE migration_selections (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  migration_id INTEGER NOT NULL,
  item_key     TEXT NOT NULL,
  category     TEXT NOT NULL,
  action       TEXT NOT NULL,  -- apply_from_source|keep_target|skip|review_manual
  created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(migration_id, item_key)
);
```
Rationale: `dryrun.go` already persists to `migration_steps action='dryrun'` to survive reload — but selection is a first-class concept deserving its own table (clearer queries, no action-string overloading). Alternative (lighter): store selection JSON in `MigrationConfig` blob. **Recommend separate table** for queryability + per-item update.

API:
- `PUT /api/migrations/:id/selection` (bulk upsert actions).
- `GET /api/migrations/:id/selection` (restore on reload).

### 2.4 Apply translation — selective `initialSyncStage`

In `initialSyncStage.Execute` (`pipeline.go:1583` loop), before `mod.Applier.Apply`:
```go
sel := repo.GetSelection(step.MigrationID, itemKeyFor(step))
switch sel.Action {
case "skip", "keep_target":
    repo.UpdateStepStatus(step.ID, StepStatusSkipped, "user chose keep_target/skip")
    continue
case "apply_from_source", "": // default = apply (backward compat)
    mod.Applier.Apply(...) // existing path
}
```
`itemKeyFor` derives a stable key from step category+data (e.g. service name, package name, config path). New `StepStatusSkipped` constant added to `model.go` status set. Rollback path (`rollbackApplied`, `pipeline.go:1733`) must skip steps that were never applied.

### 2.5 Verification engine — deepen `healthVerificationStage`

Current (`pipeline.go:1912`) only checks target responsive + lists containers. Extend to verify per `ParityItem` that was `apply_from_source`:
- service active (`systemctl is-active`)
- package/runtime installed (`which`/`--version`)
- port listening (`ss -tlnp`)
- container running (`docker ps`)
- DB exists (`psql -l` / `mysqlshow`)
- nginx upstream healthy (HTTP probe to proxy_pass target)
- cert exists (`ls /etc/letsencrypt/live/<domain>`)
- drift sisa: re-run `ComputeParity` → items still `different/missing` = unresolved.

Persist each as `VerificationResult` (`pipeline_models.go:280`) with `Passed`. Parity score = matched / (matched+failed+unresolved).

### 2.6 Freshness / staleness policy

- Reuse `planReuseMaxAge=30m` (`reuse.go:15`) for compare validity.
- On pipeline Compare tab open: if `migration_steps` collected > 30m ago OR target changed since selection → emit warning "source/target may have changed since compare — re-run".
- No auto-re-collect without user action (honesty: don't silently skip).

### 2.7 Secrets / cert / DNS

- Compare marks these `manual_required` (`ApplyLevel:4`) — never attempts.
- `.env`/`letsencrypt`/`DNS` referenced but not dumped (consistency with Phase APP-C honesty).
- `VerificationResult` for cert = exists/valid check only; never stores key material.

---

## 3. Frontend Enhancement Plan

### 3.1 Wizard compare summary (reuse, minimal)
- In Step 3/4, after category select, call `migrationApi.diff(sourceId, targetId, categories)` (already in `migrations.ts`) → show per-category delta count + reuse warnings from `category_meta`.
- No new selection UI in wizard (keep it light).

### 3.2 Pipeline compare tab (NEW)
- Route already exists: `/migrations/[id]/pipeline`. Add a "Compare" sub-tab.
- Fetch `GET /api/diff?migrationId=...` → `ParityResult`.
- Render matrix: columns Category | Item | Source | Target | Status | Suggested | Risk. Status badge colors (reuse `variantForKind` pattern from existing diff page `:144`).
- Per-item toggle: apply_from_source / keep_target / skip / review_manual.
- Bulk: select-all-safe (ApplyLevel≤2), select-all-missing (status=missing_on_target), select-category, clear.
- Dependency drawer: on item select, show `Deps[]` (hard=blocking red, recommended=amber, verify_post=blue).

### 3.3 Selection persistence UX
- `PUT /api/migrations/:id/selection` on every toggle (debounced) + on bulk.
- On tab reload, `GET` restores toggles. Survive reconnect (pattern: dryrun persist).

### 3.4 Apply handoff
- "Apply selected" button → `wsExecute` (existing). `initialSyncStage` reads selection (§2.4).
- During execute, matrix reflects step status (applied/skipped) live via WS.

### 3.5 Verification UI
- "Verify" sub-tab (or post-apply auto): parity score + matched/failed/unresolved + manual-next list.
- "Refresh compare" action → re-run `ComputeParity`.

### 3.6 Honesty rules (enforced in UI)
- No fake 100%: parity score shows unresolved explicitly.
- No synthetic success: `healthVerificationStage` failures surface as failed.
- No hidden manual-only: L4 items show "manual_required" badge, not auto-checked.
- No zero-downtime claim: DB/docker always offline_copy (`category_meta.go`).
- No "service ready" from file presence: verification probes runtime (active/port), consistent with kancasoft lessons.

---

## 4. Compare API / State / Persistence Proposal (summary)

| Concern | Mechanism | File |
|---|---|---|
| Compare compute | `ComputeParity` (extend `DiffService`) | `parity.go` (new) + `diff.go` |
| Dependency map | `BuildDeps(category, item)` | `depmap.go` (new) |
| Selection write | `PUT /api/migrations/:id/selection` | `pipeline_handler.go` |
| Selection read | `GET /api/migrations/:id/selection` | `pipeline_handler.go` |
| Selection store | `migration_selections` table | migration repo |
| Selective apply | `initialSyncStage` pre-check | `pipeline.go` |
| Skip status | `StepStatusSkipped` | `model.go` |
| Verify | deepened `healthVerificationStage` | `pipeline.go` |
| Verify store | `VerificationResult` (exists) | `pipeline_models.go:280` |
| Freshness | reuse `planReuseMaxAge` | `reuse.go:15` |

---

## 5. Selection / Apply Model

- Default (no selection) = apply all (backward compatible with current `initialSyncStage`).
- Selection is opt-out + opt-in: user can `skip`/`keep_target` specific items, or `apply_from_source` selectively.
- Hard dependency unsatisfied → UI blocks the toggle (red), prevents misleading apply.
- Rollback skips non-applied steps (`StepStatusSkipped`).

---

## 6. Verification / Parity Model

- After apply, `healthVerificationStage` runs per selected item.
- `ParityResult` re-computed; `Status` transitions: `missing_on_target`→`same` (matched), `different`→`same` (matched) or stays `different` (failed/unresolved).
- Parity score = matched / total-selected. Unresolved shown as manual-next.
- `VerificationResult.Passed` persisted per check.

---

## 7. Phased Roadmap

### 6A1 — Audit complete ✅ (this doc + audit doc)
- Exit: placement=Hybrid, source-of-truth=gabungan+normalisasi, parity boundary L1/L2 auto, L3 guarded, L4 manual.

### 6A2 — Normalized inventory + compare engine
- BE: `parity.go` (`ParityItem`, `ParityResult`), `depmap.go` (`DependencyRef`), `ComputeParity` (extend `diff.go`).
- Risk: double-collect stale → freshness gate.
- Exit: `GET /api/diff?migrationId` returns `ParityResult` with values+levels+deps.

### 6A3 — FE compare summary + matrix
- BE: wizard summary reuse `DiffService`. FE: pipeline Compare tab (matrix + badges + bulk).
- Risk: UI complexity → keep matrix read-only first, no toggle yet.
- Exit: matrix renders from `ParityResult`; wizard shows summary.

### 6A4 — Selection persistence + safe selective apply
- BE: `migration_selections` table + PUT/GET; `initialSyncStage` selective apply (L1/L2 only).
- FE: toggles + bulk + persist.
- Risk: skip semantics in rollback → handle `StepStatusSkipped`.
- Exit: user can skip/apply L1/L2 items; reload restores.

### 6A5 — Verification / parity result
- BE: deepen `healthVerificationStage`; persist `VerificationResult`; parity score.
- FE: Verify tab.
- Exit: matched/failed/unresolved + score shown.

### 6A6 — Guarded categories
- BE: DB (L3) guarded apply; docker/compose (L2/L3); service restart (L3) dependency enforcement via `depmap.go`.
- Exit: selecting PM2 service without Node/PM2/code blocked; DB shows guarded warning.

### 6A7 — App/runtime/manual-required handling
- BE/FE: explicit L4 workflow for app code, PM2 dump, runtime install (Node/PM2/nginx provision), cert, DNS.
- Mark `manual_required`; link to deploy runbook (e.g. Phase APP-C report).
- Exit: L4 items never auto-checked; honest manual-next list.

---

## 8. What Remains Manual / Guarded / Unsupported

| Class | Items |
|---|---|
| **Auto (L1/L2)** | packages, safe configs, users, docker registry images, compose defs, volumes |
| **Guarded (L3)** | DB dump/restore, config overwrite, service restart, volume overwrite, nginx site activate (needs cert) |
| **Manual (L4)** | app source code, PM2 dump, runtime install (Node/PM2/nginx), TLS cert issuance, DNS/cutover, unknown hybrid deps |

Consistent with `category_meta.go`, `reuse.go`, Phase APP-C, Phase 5E honesty.

---

## 9. Non-Negotiables (carry from audit)

- No new flow outside wizard/pipeline.
- `StepStatusApplied` guard preserved; skip path explicit.
- Compare honest: service≠runnable, image≠build-context, db-discovered≠db-synced, execute≠healthy.
- Secrets/cert/DNS never in compare/apply result.
- No one-click parity claim until 6A2–6A7 land.
