# Phase 4G.1 — Plan Wizard Completion, Recovery & Truthfulness Audit (PART 1: Investigation)

**Bug anchor (verbatim from report):** On "New Migration", Step 4 of 4, the user
sees every category succeed (`packages`, `users`, `services`, `docker` all green)
yet the UI stays on a "bikin plan" (creating plan) spinner. On refresh the wizard
returns to Step 1 — but the plan already appears in `/migrations`. The backend
persisted/created the plan; the FE never received or acted on the terminal
completion state, and wizard state is not recoverable after refresh.

---

## A. Current flow inventory

| Concern | Finding |
|---|---|
| Route / component | `web/src/routes/migrations/new/+page.svelte` (Svelte **legacy mode** — `on:` handlers, `$:` reactive statements; not runes). 580 lines. |
| Wizard steps | 4 steps. State in local vars: `step`, `sourceServerId`, `targetServerId`, `selectedCategories`, `configPaths`, `db*`, `trafficProvider`, `autoCutover`. |
| Submit action | `startPlanning()` (line 163) sets `planning=true`, opens `wsPlan(req, onMessage, onClose, onError)` from `$lib/api/migrations.ts`. |
| WS connection | `wsPlan()` (migrations.ts:125) → `new WebSocket('/ws/plan')`. Fire-and-forget: `onopen` sends the `PlanRequest` JSON; `onmessage` parses `WSMessage`; `onclose`/`onerror` callbacks. **No reconnect, no replay, no lastSequence, no stale timer.** |
| Success criteria (FE) | `planDone = planMessages.some(m => m.step === 'plan' && m.status === 'complete')` (line 61). Terminal handling fires **only** inside `onMessage` when `step==='plan' && status==='complete'`. |
| Terminal-id extraction | The callback regex-matches `migration_id:(\d+)` out of `msg.value` (line 188). If missing, `newId=''` → `goto('/migrations')` (lost the created migration's id). |
| Navigation after success | `setTimeout(() => goto(/migrations/{newId}/pipeline), 1000)` (line 205). |
| State persistence | **None durable.** `planning`, `planMessages`, `ws` are component-local. `onDestroy` closes the WS. No URL op params except `source`/`target` (and those only force `step=2`/`step=3`, never Step 4). No `operationId`/`idempotencyKey`/`migrationId` persisted. |
| Refresh behavior | `onMount` (line 88) reloads servers + policy; if `source`/`target` query params present it sets `step=2` or `step=3`; otherwise `step=1`. A running/completed Step-4 plan is **not** restored. |
| History/list refresh | `migrationApi.list()` → `GET /api/migrations` (sorted `created_at DESC`). The created plan is present here; the wizard simply doesn't reconcile to it. |

**Backend terminal writing (the crux):** `handlePlanWS` (handler.go:219) calls
`runner.Plan(...)` and *after it returns* writes one terminal frame:
`WSMessage{Step:"plan", Status:"complete", Value: fmt.Sprintf("migration_id:%d", migrationID)}`
(handler.go:285). **But** the *planner* itself also emits a `complete` frame **before**
that: `WSMessage{Step:"plan", Status:"complete", Value:"Migration plan created"}`
(planner.go:229) — with **no** migration id. So the FE can receive a `complete`
frame (line 229) that sets `planDone=true` while `newId` is still empty.

---

## B. Terminal event audit (exact mismatch)

- **Does the backend send an explicit terminal event?** Yes — but *two* of them, and
  only the **second** carries the migration id (handler.go:285). The first
  (planner.go:229) is id-less.
- **FE success criteria:** `step==='plan' && status==='complete'`. This matches
  **both** frames. The id-bearing one is the *last* message, so `newId` parsing
  usually works **if the connection stays open through `runner.Plan` returning.**
- **WS closes before the terminal frame is written?** `runner.Plan` does `wg.Wait()`
  (planner.go:196) and returns; the handler then writes the terminal frame. If the
  browser/WS drops during collection (very common: the collector takes seconds, and
  proxies/NAT/refresh can close the socket), `onClose` fires, `planning=false`, and
  the id-bearing terminal frame is **never delivered**. The FE therefore never sees
  `planDone` become true → no `goto`.
- **Can all category successes occur without the id-bearing terminal frame?** **Yes.**
  Categories stream `plan:<cat>` success frames during collection (planner.go:188).
  The migration row is created *early* (CreateMigration at planner.go:76, before
  collection). So a user can see all four categories green while the final
  `migration_id:N` frame is lost to a disconnect → exactly the reported bug.
- **How is WS close treated today?** `onClose` (wizard line 214) sets `planning=false`
  and toasts "Connection closed mid-plan. Check the migration list…" **only if**
  `planDone` is false. There is **no reconciliation**. The user is left on Step 4
  with a Retry button and a toast — and a refresh throws them back to Step 1.
- **Mismatch, stated plainly:** FE completion = receipt of one specific WS frame
  (`migration_id:N`) sent *after* the operation and *only once*. Backend completion =
  a persisted migration row (created at planner.go:76) + terminal frames. The FE has
  **no authority to re-derive completion** after the frame is lost; it just hangs on
  Step 4 / shows Retry / loses state on refresh.

---

## C. Persisted plan vs wizard-state mismatch (the reported case)

1. `CreateMigration` persists the row **early** (planner.go:76), before collection.
2. Each category `collect` step is persisted (`CreateStep`, planner.go:187).
3. FE receives `migrationId` **only** from the terminal WS frame (handler.go:285).
4. FE stores **no** identifier durably (no URL, no store, no server draft).
5. On refresh, `onMount` has no recovery token → resets to Step 1.
6. Server-side the migration **is** in `/migrations` (the user confirmed it).
   → FE and backend diverge. Root cause: **the id is delivered on a single,
   non-replayable WS frame, and is never persisted for reconciliation.**

---

## D. Idempotency audit

- `CreateMigration` (repo.go:54) is a bare `INSERT` with **no** idempotency key,
  operation id, or dedup. Every `wsPlan()` call = a **new** migration row.
- `PlanRequest` / `WSMessage` carry **no** `operationId` field.
- Backend traffic-switch idempotency keys exist, but **only for cutover**, not plan creation.
- **Duplicate vectors:**
  - Double-click "Create Migration Plan" (button isn't disabled while `planning` in
    the `planFailed`/`else` branch — only the `planning` branch shows a spinner).
  - Refresh during Step 4 (no pending-op guard → a fresh plan is created on resubmit).
  - Reconnect/replay (wsPlan has none, so a reconnect would open a new plan).
  - Second browser tab to the same wizard (independent component → new plan).
- Backend does **not** return the same resource for an idempotent retry — it always
  inserts. There is no key to even *attempt* dedup.

---

## E. Recovery / reconnect audit (wsPlan vs wsPipelineConnect)

`wsPlan` (migrations.ts:125) — **missing everything**:
- ❌ no reconnect / backoff
- ❌ no replay via sequence / `afterSeq`
- ❌ no `lastSequence` tracking
- ❌ no stale timer
- ❌ no REST reconciliation fallback
- ❌ close/error just flips `planning` + toast (not a truthful state machine)

By contrast `wsPipelineConnect` (pipeline.ts, added in 4G slice 3E) has exponential
backoff reconnect, `lastSequence` tracking, `?after_seq=` replay, a stale timer
(stale/replaying states). **The plan flow is materially behind the pipeline flow.**
[The user's report flags this gap explicitly: the pipeline WS was audited and fixed,
the plan WS was not.]

---

## F. Refresh-state audit

| Trigger | Today | Truthful desired |
|---|---|---|
| Refresh while running | resets to Step 1 (no pending-op token) | restore Step 4 "checking status", reconcile |
| Refresh after backend done but FE missed terminal frame | resets to Step 1 | reconcile → navigate to created migration |
| Refresh after WS disconnect | resets to Step 1; migration orphaned | reconcile → find migration by op id |
| Browser back/forward | SvelteKit client nav, no plan recovery | no resubmit; if pending op, hold |
| Second tab to same wizard | independent component → new plan | share op id / block duplicate |

**Required recovery identifiers** (generated/stored early enough to survive refresh):
a client-generated **`operationId`** (stable for the whole create-plan attempt), the
**PlanRequest signature** (source/target/categories) for fallback reconciliation, and
the **migrationId** once known. Persist to: URL query (`?op=<id>`), and a durable FE
store (sessionStorage is enough — survives refresh, cleared on tab close).

---

## G. Truthfulness audit (current violations)

- ❌ Infinite-ish loading possible: if `planDone` never becomes true (terminal frame
  lost) the FE shows the Retry button on Step 4 with no "this may already exist"
  guidance, and a refresh discards the in-flight plan silently.
- ❌ Can imply still-running when status unknown: the spinner stays until WS close;
  there is no "checking status / reconnecting" state.
- ❌ Returns to Step 1 as if never submitted (refresh).
- ❌ Does not tell the user a result may already be saved (only a toast).
- ❌ Provides a Retry button that **creates a duplicate** (no idempotency, no guard).

---

## H. Existing backend contract audit

| Need | Exists? | Notes |
|---|---|---|
| Operation status endpoint | ❌ | No `GET /api/plan-operations/:id`. |
| Query by idempotency/operation id | ❌ | No `operationId` field at all. |
| Session/draft endpoint | ❌ | None. |
| WS replay / sequence for `/ws/plan` | ❌ | None. |
| List migrations (for reconcile) | ✅ | `GET /api/migrations` returns `MigrationPlan[]` with `sourceId/targetId/status`. |
| Get migration by id | ✅ | `GET /api/migrations/{id}`. |

**Conclusion:** Reconciliation by `source+target+categories` via the existing list
endpoint is *possible* but fragile (status `planned` vs `interrupted` vs truly orphaned
is ambiguous, and a matching row might be an unrelated prior plan). To **guarantee**
truthful recovery and duplicate prevention (STOP CONDITION 3 & the directive's
non-negotiable rule 4), the minimal contract addition is:
- add `operation_id` to the `migrations` row, accept `operationId` on `PlanRequest`,
  dedup (return existing migration if a recoverable op with the same key exists),
  and expose `GET /api/migrations?operationId=<id>` for reconciliation.
- terminal frame already carries `migration_id:N`; keep it and make the **id-less**
  planner frame (planner.go:229) non-terminal (rename status or omit) so FE never
  stops on a frame without an id.

No redundant extra status endpoints are needed — the list filter + the existing
per-id GET cover reconcile.
