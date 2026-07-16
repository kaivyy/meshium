# Phase 6B — Source-vs-Target Compare, Selective Apply, Verification, Honest Automation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementation-grade spec for turning Meshium's "plan → replay steps" flow into a professional migration control center: **compare → select → apply-selected → verify parity**, without lying, without a second execution path, without checkbox-on-raw-diff.

**Architecture:** Reuse canonical wizard `/migrations/new` + pipeline `/migrations/:id/pipeline`. Build on existing primitives: `DiffService.Diff` (`diff.go`), `DryRun` persist pattern (`dryrun.go`), `category_meta.go` honesty contract, `Pipeline` 14-stage machine (`pipeline.go`), `VerificationResult` (`pipeline_models.go:280`), `reuse.go` freshness gate. Add: `ParityItem` model, `ComputeParity`, `migration_selections` table, `initialSyncStage` selective apply, deepened `healthVerificationStage`, new migration statuses.

**Tech Stack:** SvelteKit FE + Go `internal/mod/migration/*`.

## Global Constraints (non-negotiable — from Phase 6A audit)
1. Canonical path only: wizard + pipeline. No new flow.
2. Planner does NOT reuse onboarding discovery (`reuse.go` refuses all categories). Source of truth for apply = `migration_steps.data` (live collect at plan). Compare target side = live collect via `DiffService`.
3. Pipeline is the execution path. `initialSyncStage` (`pipeline.go:1560`) replays `Applier.Apply`; `StepStatusApplied` (`model.go:33`) is the guard. No parallel executor.
4. Compare primitives exist: `DiffService.Diff`, `DryRun`, `compatibility.go`, `category_meta.go`.
5. Docker/DB audit facts: `database`=dump&restore/offline_copy; `docker`=containers/images/volumes/compose; DB-in-Docker via `execMode`; non-Docker app does not auto-move.
6. Kancasoft audit: artifacts can be present while runtime/code/DB/cert/DNS are not → parity must go beyond server-level artifacts.
7. Hybrid model (wizard=summary, pipeline=item-level) is the locked placement.

---

## A. CURRENT REALITY (audit baseline)

- `DiffService.Diff` (`diff.go:57`) → `DiffResult{Categories:[{OnlyInSource,OnlyInTarget,Different,Same}]}` — **string lists only**, no per-item value/action/dependency.
- `DryRun` (`dryrun.go:25`) → `DryRunResult` with `DryRunChange{add,modify,remove}`; persists to `migration_steps action='dryrun'` to survive reload.
- `category_meta.go` → `DowntimeClassFor` (DB/docker=`offline_copy`), `DatabaseResumable` (MySQL/Mongo NOT resumable), `CategoryMeta{Warnings,BlockingIssues}`.
- `reuse.go` → `planReuseMaxAge=30m`; all categories refused reuse (honest shape mismatch).
- `healthVerificationStage` (`pipeline.go:1912`) → only "target responsive" + container list. **Shallow.**
- No `UserSelection`/`CompareResult`/`ParityItem` model exists (verified by grep).
- Collector data shapes (for `itemKey`): `packages.Packages`=[]string; `configs.Files`=map[path]content; `services.Services`=[]string; `users.Users`=[]UserData{name}; `docker`=containers[]/images[]/volumes[]/composeFiles[]; `database.Databases`=[]DBCatalogEntry.
- Step status set (`model.go:29-33`): `pending|running|completed|failed|applied`. **No `skipped` status yet.**

---

## B. ITEM IDENTITY CONTRACT (C.1)

### B.1 `itemKey` rules
- **deterministic**: pure function of (category, item identity) — no random/hash of volatile fields.
- **stable across compare/apply/rollback/verify**: same key derived independently each time from the same source/target state.
- **category-aware**: namespaced prefix.
- **collision-resistant**: include enough discriminator (path, name, engine, scope).
- **human-debuggable**: readable string.

### B.2 Per-category unit + key + lifecycle

| Category | Smallest item | itemKey | Stable? | Apply-map | Rollback-map | Verify | Fallback |
|---|---|---|---|---|---|---|---|
| packages | one package | `package:<name>` | ✅ | apt install name | (n/a — idempotent) | `which`/dpkg | — |
| configs | one file | `config:<normalized-abs-path>` | ✅ | Upload content to path (+mkdir parent) | restore backup | file exists + checksum | — |
| services | one unit | `service:<unit-name>` | ✅ | enable/start (after prereqs) | disable? risky → manual | `systemctl is-active` | — |
| users | one user | `user:<name>` | ✅ | create user/group | (manual) | `id <name>` | — |
| docker images | one image | `docker-image:<repo>:<digest>` (digest if available else tag) | ✅ (digest) / ⚠️(tag) | `docker pull` | (keep) | `docker images` | tag-only if no digest |
| docker volumes | one volume | `docker-volume:<name>` | ✅ | transfer volume data | (keep) | `docker volume ls` | — |
| docker containers | one container | `docker-container:<name>` | ✅ (name) | recreate from def+image | (keep) | `docker ps` | — |
| compose projects | one project | `compose-project:<project-name or normalized dir>` | ✅ (dir) | write compose + `up` | (manual) | `docker compose ps` | — |
| databases | one DB | `database:<engine>:<dbName or 'all'>` | ✅ | dump→restore | drop (rollback) | `psql -l`/`mysqlshow` | — |
| runtime | one runtime | `runtime:<name>:<version?>` | ⚠️ | provision (L4 manual today) | (manual) | `which`/`--version` | category-level |
| cert | one domain | `cert-placeholder:<domain>` | ✅ | **manual_required** | — | cert file exists | — |
| dns | one record | `dns-placeholder:<record>` | ✅ | **manual_required** | — | (manual) | — |
| secret | one ref | `secret-placeholder:<kind>:<scope>` | ✅ | **manual_required** | — | (manual) | — |
| app payload | one path | `app-payload-placeholder:<path>` | ✅ | **manual_required** | — | (manual) | — |

### B.3 Granularity verdict
- **Truly item-level (L1–L3):** packages, configs, services, users, docker images/volumes/containers/compose, databases.
- **Category-level first (runtime):** `runtime:*` — collector does not emit granular install units; treat as one category decision (provision) until a runtime inventory collector exists.
- **Not fit for selective apply now (L4 placeholders):** cert/dns/secret/app-payload — emitted as `manual_required` placeholder items so they are VISIBLE and auditable, never auto-applied.

---

## C. SELECTION SEMANTICS (C.2)

Four actions, precise meaning:

| Action | Meaning | Drift/parity effect | Migration success effect |
|---|---|---|---|
| `apply_from_source` | bring source item to target | counts toward matched after verify | required to succeed |
| `keep_target` | target value is accepted as canonical for this item | item marked `accepted_target` — NOT drift | success-neutral (intentional) |
| `skip` | operator declines to handle now | item marked `skipped_by_user` — explicit **unresolved-by-choice** | success-neutral but counts in `manual_gaps` |
| `review_manual` | defer to manual follow-up, surfaced in gap list | `manual_required`/`unresolved` | success-neutral; counts in `manual_gaps` |

**`keep_target` vs `skip` difference:**
- `keep_target` = "target is correct, do not touch" → parity treats it as a *resolved, accepted* state (no drift, no manual gap).
- `skip` = "I am not applying and not accepting as canonical" → recorded as *unresolved-by-choice*; appears in remaining-drift / manual-gaps panel so it is never silently forgotten.

**Status after execute (per item):** `applied` (apply_from_source done) | `accepted_target` (keep_target) | `skipped_by_user` (skip) | `manual_required` (review_manual / L4) | `failed` (apply errored) | `unresolved` (apply done but verify failed).

**Effect on migration success:** a migration is `completed` when all **selected** items reach `applied`/`accepted_target` and zero `failed`. Items `skipped_by_user`/`manual_required` do NOT fail the migration but ARE reported in status (see E.6).

**Effect on rollback:** only `applied` items are rolled back (reverse of Apply). `accepted_target`/`skipped_by_user`/`manual_required` are untouched. `failed` items: rollback runs for whatever pre-backup existed.

---

## D. SUCCESS SEMANTICS (C.3)

Add migration statuses (extend `model.go` status set, render honestly in FE):

| Status | Set when | Operator meaning |
|---|---|---|
| `completed` | all selected applied/accepted; parity verified; no unresolved | fully done |
| `completed_with_drift` | selected applied but parity shows unresolved infra diffs | done but not identical |
| `completed_with_manual_gaps` | done; `skip`/`review_manual`/`L4` items remain | done; manual follow-up required |
| `completed_partial` | some selected applied, some failed-but-nonfatal | partial |
| `verification_failed` | apply ok but runtime/app verification failed | needs investigation |
| `verification_partial` | some checks passed, some unresolved | partial verify |
| `manual_followup_required` | L4 items present and unhandled | operator action needed |

**Rules:**
- `completed` is ONLY used when no `unresolved`/`manual_required`/`failed` remain. **Never** claim `completed` when app-health layer is unverified for a selected app item.
- `skip`/`manual_required` do not block `completed_*` but force the `*_with_manual_gaps` / `manual_followup_required` variant — never a bare `completed`.
- FE renders these with distinct severity colors; no "100% success" masking.

---

## E. VERIFICATION SEMANTICS — 3 LAYERS (C.4)

### E.1 Layer 1 — Infra parity
- What: file exists + checksum matches (configs); package installed (`dpkg -l`); service unit file present; image present (`docker images`); volume present; DB exists (`psql -l`); cert file exists (`ls /etc/letsencrypt/live/<d>`); compose file written.
- Reliable: yes (filesystem/CLI queries).
- Weak inference: "file exists" ≠ "service runs" (kancasoft).
- Auto-run: always after apply.

### E.2 Layer 2 — Runtime readiness
- What: `systemctl is-active <unit>`; `node --version`/`pm2 --version` present; `nginx -v`; port listening (`ss -tlnp`); container `Up` (`docker ps`); DB accepts connection (`pg_isready`/`mysqladmin ping`); nginx can bind (config test `nginx -t`).
- Reliable: mostly. False-negative risk: probe timing (service still starting) → retry with backoff.
- Weak inference: port listening ≠ app logic healthy.
- Live probe: yes (SSH ExecContext). Auto-run after Layer 1.

### E.3 Layer 3 — App health / functional
- What: `/health` 200 on expected port; proxy_pass upstream returns 200; DB contains expected schema/object; compose `Up` AND app responds.
- Reliable: only if app exposes a health endpoint. **Otherwise inference is weak → mark `unresolved`, require manual acknowledgment.**
- Manual acknowledgment: for items with no probe, operator confirms "app verified" → `verified` by acknowledgment (recorded in audit trail, not synthesized).
- Auto-run: Layer 3 only for items with a known health endpoint; else `manual_required`.

**Per-item verify state:** `verified` (all 3 layers pass or acknowledged) | `infra_only` (L1 only) | `runtime_only` (L1+L2) | `unresolved` (L2/L3 failed or no probe) | `manual_required` (L4) | `skipped` (operator).

---

## F. API BOUNDARY (C.5)

**Decision: NEW dedicated parity API. Do NOT overload `POST /api/diff`.**

Rationale: `DiffService.Diff` returns raw `DiffResult` (string lists) used by the read-only `/migrations/[id]/diff` page and `/servers/compare`. Parity is a different domain (item-level values + actions + deps + selection + verify). Overloading would break the existing read-only consumers and muddy semantics.

New endpoints (in `pipeline_handler.go`):
- `GET /api/migrations/:id/parity` → `ParityResult` (compute or cached; source=`migration_steps.data`, target=live collect). **Pipeline-authorized compare.**
- `PUT /api/migrations/:id/selection` → bulk upsert `SelectionDecision[]`.
- `GET /api/migrations/:id/selection` → restore.
- `GET /api/migrations/:id/verification` → `ParitySummary` (post-apply).
- Keep `POST /api/diff` + `/ws/diff` for the legacy read-only compare page (unchanged).

---

## G. WIZARD PREVIEW (C.6)

**Decision: lightweight, preview-only, stale-capable, NOT authoritative, optionally deferred.**

- Wizard Step 3/4 calls `migrationApi.diff(sourceId, targetId, categories)` (already exists in `migrations.ts`) for a **high-level per-category delta count** only.
- Framing: a clear "Preview (not authoritative — final compare happens in pipeline)" banner. No item-level toggles, no dependency graph in wizard (those belong to pipeline).
- Avoids double heavy collect: diff is summary-only; the authoritative per-item parity is computed once in pipeline (reusing `migration_steps.data` for source side → no re-collect of source).
- Stale: if source plan collected > `planReuseMaxAge`, banner says "plan data may be stale — re-run in pipeline".

---

## H. TARGET EXPERIENCE

### H.1 Wizard
Shows: category checkboxes (existing), database mode/execMode (existing), **preview-only compare summary** (per-category delta count + reuse/honesty warnings from `category_meta`), estimated bytes, downtime class, resumability, honesty disclosure.
Does NOT show: item-level selection, dependency graph, per-item toggles.

### H.2 Pipeline — "Migration Control Center"
Tabs/sections (data source → states → success):
1. **Overview** — source/target, categories, status, parity score, freshness. Loading/error/empty handled.
2. **Compare** — `ParityResult` matrix. Stale banner if >freshness. Bulk toolbar.
3. **Apply** — `wsExecute`; rows update live (selected/applied/skipped/failed). No fake success.
4. **Verify** — `ParitySummary` 3-layer cards. Re-run + refresh-compare actions.
5. **Drift / Remaining gaps** — `unresolved` + `skipped_by_user` + `manual_required` list with remediation notes.
6. **Logs / Events / Audit** — existing event stream + new selection/apply/verify decisions.

### H.3 Compare matrix columns (premium)
`Category | Item | Source | Target | Status | Suggested Action | Risk Level | Freshness | Dependency Indicator | Verification State`.
Status set: `same, missing_on_target, different, accepted_target, skipped_by_user, selected_for_apply, applied, verified, unresolved, manual_required, unsupported, stale`.
Actions: `apply_from_source, keep_target, skip, review_manual`.
Bulk: select-all-safe (L≤2), select-all-missing, accept-target-selected, clear, by-category, by-risk, by-status.

### H.4 Dependency drawer
For selected item shows: hard deps (red, blocking), recommended deps (amber), post-apply verify deps (blue), restart impact, overwrite impact, downtime class, resumability, rollback confidence, manual follow-up. Examples wired per §B.2 (PM2 service → Node+PM2+code+dump+env+port; nginx vhost → nginx+cert+upstream+DNS; compose → file+image/build-context+volumes+env; database → engine+creds+execMode+restore+downtime+rollback).

### H.5 Verification report
Cards: **Infra parity score**, **Runtime readiness score**, **App health score**, **Manual gap count**, **Unresolved drift count**. Plus: what passed / failed / unresolved / manual_required / skipped-by-choice / target-kept.

---

## I. BACKEND ARCHITECTURE

### I.1 Models (NEW `parity.go`)
```go
type ParityItem struct {
    Category    string          `json:"category"`
    ItemKey     string          `json:"itemKey"`      // §B.2
    SourceValue string          `json:"sourceValue"`
    TargetValue string          `json:"targetValue"`
    Status      string          `json:"status"`       // §H.3 status set
    Suggested   string          `json:"suggested"`    // default action
    ApplyLevel  int             `json:"applyLevel"`   // 1..4
    Deps        []DependencyRef `json:"deps"`
    Warnings    []string        `json:"warnings"`
    Freshness   string          `json:"freshness"`    // fresh|stale
    VerifyState string          `json:"verifyState"`  // §E.3
}
type DependencyRef struct {
    ItemKey   string `json:"itemKey"`
    Kind      string `json:"kind"`    // hard|recommended|verify_post
    Note      string `json:"note"`
    Satisfied bool   `json:"satisfied"`
}
type ParityResult struct {
    MigrationID int          `json:"migrationId"`
    Categories  []ParityItem `json:"categories"` // flattened items
    Freshness   string       `json:"freshness"`
    ComputedAt  string       `json:"computedAt"`
}
type ParitySummary struct { // post-apply verification
    InfraScore      float64 `json:"infraScore"`
    RuntimeScore    float64 `json:"runtimeScore"`
    AppHealthScore  float64 `json:"appHealthScore"`
    ManualGaps      int     `json:"manualGaps"`
    UnresolvedDrift int     `json:"unresolvedDrift"`
    Passed          int     `json:"passed"`
    Failed          int     `json:"failed"`
}
```
Producers: `ComputeParity` (source=`migration_steps.data`, target=live collect). Consumers: pipeline Compare tab + Apply + Verify. Lifecycle: computed on demand, cached in `migration_parity` (optional) or recomputed; persisted verification in existing `VerificationResult` (`pipeline_models.go:280`).

### I.2 `ComputeParity` (extend `diff.go`)
Reuse `DiffService.Diff`'s dual-collect (`diff.go:112-119`) but emit `ParityItem[]` with values + `ApplyLevel` (from `category_meta.go` + per-item rules) + `DependencyRef` (new `depmap.go`). Freshness via `planReuseMaxAge` (`reuse.go:15`).

### I.3 Selection persistence (NEW `migration_selections`)
```sql
CREATE TABLE migration_selections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  migration_id INTEGER NOT NULL,
  item_key TEXT NOT NULL,
  category TEXT NOT NULL,
  action TEXT NOT NULL, -- apply_from_source|keep_target|skip|review_manual
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(migration_id, item_key)
);
```
Survives reload/reconnect/reopen (SQL). Auditable (rows = decision log). FE optimistic update then PUT.

### I.4 Apply orchestration (modify `pipeline.go` `initialSyncStage`)
Before `mod.Applier.Apply` (`pipeline.go:1631`): read `GetSelection(step.ItemKey)`; map step→itemKey (derive from category+data, e.g. config path, service name, package name, DB name). Switch:
- `keep_target`/`skip` → `UpdateStepStatus(step.ID, StepStatusSkipped, reason)`; `continue`.
- `apply_from_source`/`""` (default, backward-compat) → Apply as today.
Add `StepStatusSkipped = "skipped"` to `model.go:29-33`. Rollback (`rollbackApplied`, `pipeline.go:1733`) skips steps with `skipped`/`accepted_target` status.

### I.5 Migration status semantics (modify `model.go`)
Add statuses from §D. Set by `finalizationStage`/`archiveStage` based on step + verification outcomes. FE renders with severity.

### I.6 Verification engine (deepen `healthVerificationStage`, `pipeline.go:1912`)
3-layer probes (§E). Per selected `apply_from_source` item: L1 (file/pkg/unit/image/volume/DB/cert exists), L2 (active/listening/Up/ready), L3 (health endpoint or manual acknowledgment). Persist each as `VerificationResult`. Compute `ParitySummary`.

### I.7 Freshness policy
Three clocks: source-collect freshness (`migration_steps` age vs `planReuseMaxAge`), target-compare freshness (parity compute age), verification freshness (verify age). Stale → banner + "re-run" action. No silent re-collect.

### I.8 Secrets/cert/DNS/app-payload
Emitted as `manual_required` placeholder `ParityItem` (`cert-placeholder:*`, etc., §B.2). Never auto-applied. Linked remediation note (e.g. Phase APP-C report). `VerificationResult` for cert = existence/validity only; no key material.

---

## J. FRONTEND ARCHITECTURE

### J.1 Wizard enhancement
Add preview-only compare summary (reuse `migrationApi.diff`), "Preview (not authoritative)" banner, "manual-required may remain after apply" disclosure. No item toggles.

### J.2 Pipeline IA
Top summary + tabs (Overview/Compare/Apply/Verify/Drift/Logs). Action bar per tab.

### J.3 Compare matrix component
Row = `ParityItem`; badges for status/risk/freshness/verify; selection controls (radio: 4 actions); bulk toolbar; filter/search/sort; stale indicator; dependency popover/drawer.

### J.4 Selection UX rules
- Toggle disabled when hard dep unsatisfied (red) — cannot apply.
- `keep_target`/`skip` on a `different` item → confirm modal ("target state will differ from source").
- Selecting item auto-HIGHLIGHTS deps (L4) but auto-selects only for L1 safe deps; L2/L3 deps warning only.
- Force-skip allowed but recorded as `skipped_by_user` (never hidden).
- Manual gap acknowledge required before `completed` claimed.

### J.5 Execution UX
Rows update live: `selected_for_apply` → `applied`/`failed`; non-selected → `accepted_target`/`skipped_by_user`. Progress summary counts applied vs skipped honestly. Retry re-runs failed items only.

### J.6 Verification UX
Parity summary cards (3 scores + gaps). Verified/unverified chips. Failed checks expandable. Unresolved drift + manual-next list. "Re-run verify" + "Refresh compare" + "Accept target state" for unresolved-by-choice.

### J.7 Professional polish
Consistent naming (compare/apply/verify/gap), severity colors (red=block/hard, amber=warn/recommended, blue=verify_post, green=ok), concise explanations, operator confidence without clutter.

---

## K. AUTOMATION STRATEGY (honest)

### K.1 Recommendation rule engine
Per item compute `ApplyLevel` (§B.2) + deps satisfied → suggested action:
- L1 + deps ok → `apply_from_source` (safe).
- L2 → `apply_from_source` with warning.
- L3 → `apply_from_source` + guarded confirm modal.
- L4 / unsatisfied hard dep → `review_manual` / `manual_required`.

### K.2 Smart bulk
- "Apply all safe" = all L≤2 with satisfied hard deps.
- "Accept target for unchanged risky" = `keep_target` on `same`/`different` high-risk.
- Group by category; blockers highlighted first; missing prerequisites suggested.

### K.3 Dependency-assisted selection
- L4 (safe) deps auto-selected with parent.
- L2/L3 deps: highlight + warning, not auto-select (operator confirms).
- Hard dep unsatisfied: parent toggle disabled.

### K.4 Progressive automation
- L1 auto now (6B4). L2 guarded now. L3 later (6B6/6B7). L4 manual for foreseeable future (app code/runtime/cert/DNS).

---

## L. IMPLEMENTATION ROADMAP

### 6B1 — Contract hardening
- Goal: lock itemKey (§B.2), selection (§C), success (§D), API boundary (§F).
- BE: `model.go` (add `StepStatusSkipped` + new migration statuses), `parity.go` (types only).
- FE: none yet.
- Risk: status string churn → keep old statuses valid.
- Compat: existing `completed` still emitted when no selection.
- Exit: spec frozen; types compile.

### 6B2 — Backend parity foundation
- Goal: `ComputeParity` + `depmap.go` + `migration_selections` + GET/PUT selection + GET `/parity`.
- BE: `parity.go`, `depmap.go`, `pipeline_handler.go` (new routes), migration repo (table + methods), `diff.go` (extend, not break).
- FE: none.
- Risk: double-collect stale → freshness gate.
- Compat: `POST /api/diff` unchanged.
- Exit: `GET /api/migrations/:id/parity` returns `ParityItem[]` with values+levels+deps; selection persists + restores.

### 6B3 — FE compare experience
- Goal: wizard preview (§G) + pipeline Compare tab (§H.3) + bulk + dependency drawer.
- FE: `new/+page.svelte` (preview banner), `pipeline/+page.svelte` (Compare tab + matrix + drawer), `migrations.ts` (parity/selection API).
- Risk: UI complexity → matrix read-only first (no toggle until 6B4).
- Compat: existing diff page untouched.
- Exit: matrix renders from `ParityResult`; wizard shows preview-only summary.

### 6B4 — Selective apply
- Goal: `initialSyncStage` selective; skipped/applied; rollback; status semantics.
- BE: `pipeline.go` (skip pre-check + `StepStatusSkipped`), `model.go` (statuses), rollback path.
- FE: Apply tab wired to selection; live row status.
- Risk: rollback of skipped items → guard `rollbackApplied`.
- Compat: no selection = apply all (backward compat).
- Exit: user can skip/apply L1/L2; reload restores; rollback skips non-applied.

### 6B5 — Verification layering
- Goal: 3-layer verify + `ParitySummary` + persist.
- BE: deepen `healthVerificationStage`; `VerificationResult` writes; `ParitySummary` compute; GET `/verification`.
- FE: Verify tab + cards.
- Risk: probe timing false-negatives → retry/backoff.
- Compat: old shallow verify still runs as L1 minimum.
- Exit: matched/failed/unresolved + 3 scores shown.

### 6B6 — Professional operator experience
- Goal: remaining-drift panel, manual follow-up checklist, accept-target, stale/freshness UX, audit trail.
- FE: Drift tab, Logs tab polish, stale banners.
- BE: audit trail of decisions (selection + apply + verify).
- Risk: clutter → severity-gated display.
- Exit: operator sees gaps + manual steps + can accept target state.

### 6B7 — Guarded automation expansion
- Goal: smarter deps, better suggestions, safer auto-select, category refinements.
- BE/FE: recommendation engine (§K), per-category tuning.
- Risk: over-automation → keep L4 manual, guardrails on.
- Exit: "apply all safe" reliable; blockers surfaced first.

---

## M. WHAT REMAINS MANUAL / GUARDED / UNSUPPORTED

| Class | Items |
|---|---|
| Auto (L1/L2) | packages, safe configs, users, docker registry images, compose defs, volumes |
| Guarded (L3) | DB dump/restore, config overwrite, service restart, volume overwrite, nginx site activate (needs cert) |
| Manual (L4) | app source code, PM2 dump, runtime install (Node/PM2/nginx), TLS cert issuance, DNS/cutover, unknown hybrid deps |

Consistent with `category_meta.go`, `reuse.go`, Phase APP-C, Phase 5E, Phase 6A audit.

## N. HONESTY NON-NEGOTIABLES
- No one-click parity claim until 6B2–6B7 land.
- No "service ready" from file presence only (verification layers enforce).
- No "completed" when app-health unverified for a selected app item.
- Image present ≠ build context present; db discovered ≠ db data synced; execute succeeded ≠ app healthy.
- Secrets/cert/DNS never in compare/apply result values.
- `skip`/`manual_required` never hidden — always in drift/manual panel.
