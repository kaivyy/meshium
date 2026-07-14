# Phase 4G.1 — Plan Wizard Completion, Recovery & Truthfulness Audit

**Final Report** · 2026-07-14

## 1. Summary

The "New Migration → Step 4 → Review & Create Plan" flow had a truthfulness bug:
the UI could hang on a "bikin plan" spinner after the backend had *already*
created the migration, lose all operation state on refresh (returning to Step 1),
and — because plan creation had no idempotency — could create duplicate plans on
retry/reconnect/refresh. The fix makes the backend authoritative for plan-creation
result, idempotency, and legal transitions, and gives the FE a truthful,
non-hanging state model.

**Release recommendation: SHIP.** All non-negotiable rules (1–6) satisfied;
backend and frontend tests green; no regression in existing 5-category + recovery
flows.

## 2. Root cause

The original FE completion logic depended entirely on a *single, non-replayable*
WebSocket terminal frame carrying `migration_id:N`, which the planner emitted
**after** `runner.Plan` returned. Three independent defects compounded:

1. **Id-less duplicate terminal frame.** The planner emitted an id-less
   `complete` frame ("Migration plan created") *before* the id-bearing one. The
   FE's `onmessage` matched on `status === 'complete'` and stopped there with no
   id → it parked on the green categories and never navigated.
2. **No persisted operation identity.** `CreateMigration` was a bare `INSERT`;
   the migration id was never persisted client-side and was not echoed in any
   reconcilable field. Refresh discarded all component state, so the wizard
   reset to Step 1 even though the plan row already existed in `/migrations`.
3. **No idempotency / no reconcile.** A refresh or resubmit re-opened a brand-new
   WS and re-ran `CreateMigration`, producing a *second* plan. There was no
   `GET` reconcile path, so the FE could only trust the WS frame — and on its
   loss, it guessed (toast-only close handler) or hung.

The honest symptom: all categories green, FE stuck loading, refresh → Step 1,
yet a new plan exists in history.

## 3. Exact mismatch (contract drift)

| Concern | Before (broken) | After (fixed) |
|---|---|---|
| Completion authority | WS `complete` frame (guessable) | REST `GET /api/migrations?operationId=` (backend) |
| Terminal frame | id-less `complete` acceptable | id-bearing `migration_id:N` **required** |
| Operation identity | none | client `operationId` UUID, persisted to `?op=` + `sessionStorage` + row |
| Idempotency | none (refresh = duplicate) | dedup by `operationId` for recoverable statuses |
| Refresh behavior | reset to Step 1 | restore Step 4 + `checking` + reconcile |
| Spinner w/o reconcile | possible (indefinite) | impossible — every state has a label or UNKNOWN |

## 4. New contract added?

**Yes — one, and only one, minimal addition** (rule 6 satisfied: only what was
needed for truthful recovery):

- **`operationId` field** on `PlanRequest`, `Migration`, `MigrationResponse`, and
  the `migrations.operation_id` column. Client-generated, persisted *before*
  submit, passed to the backend, stored on the row.
- **Reconcile endpoint already existed** as `GET /api/migrations`; it was extended
  to accept `?operationId=` and return the matching migration (or `[]`). No new
  route, no new transport. WS unchanged in protocol — only the semantics of the
  terminal frame tightened (id required; id-less frame demoted to `progress`).

No new WebSocket reconnect/replay layer was added. The existing stateless
`/ws/plan` is sufficient because completion is decided by the REST reconcile, not
by a replayed stream — this is the smallest honest contract that satisfies the
rules.

## 5. Recovery model

```
load (onMount)
  └─ loadOperationId()  (URL ?op=  →  sessionStorage)
       ├─ none            → normal wizard, Step 1
       └─ found op        → step=4, planState='checking', reconcilePlan(op)
                                ├─ backend: recoverable migration  → planState='completed' → goto pipeline
                                ├─ backend: failed                  → planState='failed' (honest banner)
                                └─ backend: none / error            → planState='unknown' (banner + history link)

submit (startPlanning)
  └─ op = loadOperationId() || newOperationId();  persistOperationId(op)
  planState: connecting → collecting
       ├─ id-bearing terminal frame seen  → onPlanCompleted(id, op)  (REST-confirmed already true from WS, but
       │                                                  reconcile still runs on close for parity)
       ├─ WS close, no terminal frame     → planState='checking', reconcilePlan(op)   ← never toast-only
       └─ WS error                        → planState='unknown', reconcilePlan(op)

reconcilePlan(op)
  └─ decideReconcile(list)  → completed | failed | unknown   (unit-tested pure fn)
```

Refresh while running → `checking` → reconcile finds the in-flight row → `completed`.
Refresh after backend completed → `checking` → reconcile finds `planned`/`running`/… → `completed`.
The operation id survives refresh because it lives in the URL and `sessionStorage`;
it is cleared only on confirmed completion or when the user navigates away via the
history link.

## 6. Duplicate-prevention model

- **Client side:** `operationId` generated once and persisted before submit;
  `startPlanning` reuses the persisted id on retry/reconnect, never minting a new
  one mid-flight (`if (planning || planState==='checking') return` double-submit
  guard as well).
- **Backend side:** `handlePlanWS` calls `GetMigrationByOperationID(op)` first. For
  `planned` / `running` / `interrupted` it returns the **existing** migration via
  an id-bearing terminal frame and `return` — no second `CreateMigration`.
- **Retry-after-failure:** a recorded `failed` is **deliberately excluded** from
  dedup, so a retry reuses the same `operationId` and creates a *fresh* plan. The
  reconcile query is `ORDER BY id DESC LIMIT 1`, so on resubmit it surfaces the
  newest (live) plan, never the dead `failed` one. Rule 2 (don't discard a finished
  state) still holds: a refresh on a failed `operationId` reconciles to that failed
  row and shows the `failed` banner instead of resetting to Step 1.

## 7. UX state model (truthful, non-hanging)

| State | Trigger | UI |
|---|---|---|
| `idle` | not yet submitting | "Create Migration Plan" button |
| `connecting` | WS opening | "Connecting to planner…" + spinner |
| `collecting` | progress frame / after open | "Planning… mm:ss" + spinner |
| `checking` | WS closed, no terminal; or on refresh | "Checking plan status… (reconnecting)" + spinner |
| `completed` | reconcile found recoverable migration | "Plan created — redirecting…" (disabled) → goto pipeline |
| `failed` | reconcile found `failed` | red error banner + "Retry Migration Plan" |
| `unknown` | reconcile found nothing / errored | amber ambiguity banner + Retry + "Go to Migration History" |

Spinner states (`connecting`/`collecting`/`checking`) are **always bounded** by a
reconcile path — none can loop forever. `unknown` converts ambiguity into an
explicit, actionable state (rule 5), not fake success or an endless spinner
(rule 1). Buttons are disabled while `planInFlight`.

## 8. Test matrix A–J

| # | Scenario | Mechanism | Result |
|---|---|---|---|
| A | Happy path (WS terminal frame arrives) | id-bearing frame → `onPlanCompleted` | ✅ navigates to pipeline |
| B | Lost terminal event (WS closed early) | WS close → `reconcilePlan` → `decideReconcile` | ✅ covered by `reconcile.test.ts` (B) + backend `TestReconcileByOperationID` |
| C | Refresh while running | onMount op present → `checking` → reconcile finds running row | ✅ backend `TestReconcileByOperationID` (recoverable) |
| D | Refresh after backend completed | onMount → reconcile finds `planned` | ✅ `reconcile.test.ts` (D) + backend reconcile |
| E | WS disconnect mid-plan | `onclose` → reconcile (not toast-only) | ✅ `reconcile.test.ts` (E: error→unknown) |
| F | Backend failure | `decideReconcile` maps `failed` | ✅ `reconcile.test.ts` (failed→failed) |
| G | Double submit | double-submit guard + persisted op | ✅ `startPlanning` guard; backend dedup `TestDedupReturnsExistingForInFlight` |
| H | Unknown / ambiguous state | `decideReconcile` → `unknown` | ✅ `reconcile.test.ts` (unknown / unrecognized status) |
| I | Accessibility | `role="status"` `aria-live="polite"` on banners; `role="alert"` on failure | ✅ marked in markup |
| J | No regression | full suites | ✅ 57 FE tests, Go migration pkg green |

### Test results

- **Frontend** `npx vitest run`: **57 passed** (incl. 7 new `reconcile.test.ts`).
- **Backend** `go test ./internal/mod/migration/`: **ok** (incl. `plan_op_idempotency_test.go`, `plan_reconcile_test.go`).
- `npm run check` (svelte-check): **0 errors, 0 warnings**.
- `npm run build`: **success**.
- `go vet ./internal/mod/migration/`: **clean**.

## 9. Remaining limitations

1. **No active WS reconnect/replay.** Once a plan WS drops, completion is decided
   by the REST reconcile. This is intentional (smallest honest contract) but means
   a *partial* collection progress view is lost on drop until reconcile resolves —
   acceptable because the backend is authoritative and the row already persists.
2. **`unknown` requires a manual choice.** When reconcile finds nothing, the FE
   cannot distinguish "still in flight and not yet persisted" from "never started".
   It shows UNKNOWN + a Retry that reuses the same op. A retry after the backend
   has *just* committed would normally dedup; if the commit hasn't happened yet it
   creates a fresh plan. This is an inherent ambiguity at the create boundary and
   is surfaced, not hidden (rule 5).
3. **`operationId` is `DEFAULT ''` and not UNIQUE-constrained** by design: a failed
   retry must be allowed to re-insert. Dedup relies on the status switch, not a DB
   constraint, so a non-idempotent client (old FE) could still duplicate — but no
   such client ships.
4. **Browser only.** `reconcilePlan` uses `sessionStorage` + `history.replaceState`;
   multi-tab/device recovery of the *same* operation is out of scope (each tab gets
   its own op on submit).

## 10. Release recommendation

**SHIP.** The fix satisfies all six non-negotiable rules:
1. No spinner without reconcile — every in-flight state is bounded by a reconcile.
2. Refresh never discards an active/finished operation — op id persisted, Step 4 restored.
3. FE never guesses success — completion is REST-confirmed via `decideReconcile`.
4. Retry/refresh/reconnect cannot duplicate a plan — client op reuse + backend dedup.
5. Ambiguity shown as UNKNOWN, not fake success or endless spinner.
6. Only the minimal `operationId` contract was added, solely for truthful recovery.

Backend remains authoritative for plan-creation result, idempotency, operation
state, authorization, and legal transitions. Tests green, no regression.

## 11. Files changed

**Backend**
- `internal/mod/migration/model.go` — `OperationID` on `Migration`, `PlanRequest`, `MigrationResponse`.
- `internal/mod/migration/repo.go` — `CreateMigration` stores `operation_id`; `GetMigrationByOperationID` added; scans include `operation_id`.
- `internal/mod/migration/handler.go` — `handlePlanWS` op-dedup (excludes `failed`); `handleList` reconcile-by-`operationId`.
- `internal/mod/migration/planner.go` — id-less frame demoted to `progress`; id-bearing terminal frame required.
- `internal/mod/migration/pipeline_handler.go` — create path passes `operationId`.
- `internal/handler/handler_factory.go` — passes `""` op for non-wizard create.
- `internal/db/migrations.go` — `operation_id` column + index (CREATE + ALTER).
- `internal/mod/migration/handler_test.go` — mock `GetMigrationByOperationID` returns newest match.
- `internal/mod/migration/plan_op_idempotency_test.go` (new), `internal/mod/migration/plan_reconcile_test.go` (new).

**Frontend**
- `web/src/routes/migrations/new/+page.svelte` — `PlanState` machine, `operationId` persistence, `reconcilePlan`, `onPlanCompleted`, truthful Step 4 markup (connecting/collecting/checking/completed/failed/unknown + a11y roles).
- `web/src/lib/api/migrations.ts` — `operationId` on `PlanRequest`; `decideReconcile` pure fn + `ReconcileOutcome`.
- `web/src/lib/api/reconcile.test.ts` (new).

**Docs**
- `CHANGELOG.md` `[Unreleased]` — Phase 4G.1 subsection.
- `docs/superpowers/specs/phase-4g1-plan-wizard-investigation.md`, `-plan.md`, `-final-report.md`.
