# Phase 4G — UI Truthfulness, Operator UX & Release-Parity Audit

> PART 1 of Phase 4G (UI workstream). Read-only investigation of the existing
> Meshium frontend against the backend reality established in Phase 1, 2A/B/C/D,
> Phase 3, and Phase 4. Companion to `phase-4g-ui-plan.md` (implementation) and
> `phase-4g-ui-final-report.md`.
>
> **Mandate (from directive):** UI is NOT the source of truth. Backend remains
> authoritative for authorization, fencing ownership, legal state transitions,
> action gating, idempotency, and cutover/rollback legality. This audit measures
> where the UI over-states, hides, or diverges from that authority — and where it
> *under*-exposes operator evidence the backend already produces.

## 0. Method & scope

- Frontend: SvelteKit + TypeScript at `web/src`. API client `lib/api/*`, WS
  client `lib/api/pipeline.ts` (`wsPipelineConnect`), routes under `routes/*`,
  shared components `lib/components/*`.
- Audit cross-checked against backend: `internal/mod/migration/pipeline_models.go`
  (`MigrationConfig.AutoCutover` JSON field), `policy.go` (`PolicyEngine`,
  `GET /api/pipeline/policy` → `PolicyMatrix`), `pipeline_handler.go`
  (`/api/pipeline/policy`, action endpoints, recovery guidance).
- Findings below are verified by direct read of source at the cited lines.
- Three parallel Explore agents ran for deeper screen-by-screen and
  WebSocket-layer coverage; where their output confirmed or extended a finding,
  it is folded in verbatim-style. OUTSTANDING: two agents (screens-depth,
  WS-depth) were still running at doc-compile time; their completion may surface
  additional detail but does not change the committed sequencing decision.

---

## A. Existing screens / components inventory

| Route / component | File | Data source | WS | Status |
|---|---|---|---|---|
| New migration wizard | `routes/migrations/new/+page.svelte` | `planner.startPlanning` (WS) | plan WS | **wired, but missing provider/autoCutover config** |
| Migration list | `routes/migrations/+page.svelte` | `api.getMigrations()` | none | wired; status badge gaps |
| Migration detail | `routes/migrations/[id]/+page.svelte` | `migrationApi.get()` | none | wired |
| Pipeline realtime | `routes/migrations/[id]/pipeline/+page.svelte` | `pipelineApi.getSession` + WS | pipeline WS | wired; richest screen |
| Diff | `routes/migrations/[id]/diff/+page.svelte` | `migrationApi.diff(src,tgt)` | none | wired |
| MigrationHeader | `lib/components/MigrationHeader.svelte` | props | prop `wsConnectionState` | wired; **hard-coded "No automatic cutover"** |
| PipelineStepper | `lib/components/PipelineStepper.svelte` | props | — | wired |
| CutoverChecklist | `lib/components/CutoverChecklist.svelte` | props (repl/lag/health) | — | wired |
| ObservationPanel | `lib/components/ObservationPanel.svelte` | props | — | wired |
| LiveMonitor | `lib/components/LiveMonitor.svelte` | props | — | wired |
| PlannerView | `lib/components/PlannerView.svelte` | props | — | wired |
| PlaceholderPage | `lib/components/PlaceholderPage.svelte` | — | — | shared empty-state |

Shared UI primitives available: `ui/Badge.svelte`, `ui/Button.svelte`,
`ui/Card.svelte`, `ui/DataTable.svelte`, `ui/LogViewer.svelte`,
`ui/Modal.svelte`, `ui/ProgressBar.svelte`, `ui/Toast.svelte`,
`ui/Skeleton.svelte`, `ui/EmptyState.svelte`, `ui/PageHeader.svelte`,
`ui/DropdownMenu.svelte`. **Badge is generic (color+text slot) — there is no
canonical support-status badge model** (gap for PART 3A).

---

## B. Backend parity inventory (per action)

| Action | Backend | UI surface | Parity |
|---|---|---|---|
| plan | `POST /plan` | wizard plan WS | parity (wizard → list) |
| execute / start | `POST /actions/...` | pipeline `startPipeline` | parity |
| dry-run | `wsDryRun` | wizard | parity |
| compatibility | `POST /compatibility?refresh` | wizard step 1 WS + `getCompatibilityChecks` | parity, but blockers shown as list only, no group/remediation (gap 3B) |
| health | `getHealth` | pipeline LiveMonitor | parity |
| risk | `getRisk` | header Risk badge | parity |
| replication | `getReplication` | pipeline lag card + CutoverChecklist | parity (no source/target role shown — gap 3C) |
| sync | `getSyncSessions` | **fetched but NOT rendered** in pipeline page (gap 3C) | stale/partial |
| metrics | `getMetrics` | persisted snapshot only | parity |
| audit | `getAuditTrail` | **not surfaced in UI** (gap 3F) | not wired |
| rollback | `POST /actions/rollback` | pipeline button | parity; **no explicit "no auto-rollback after target write" banner** (gap 3D) |
| cutover | `POST /actions/cutover` + confirm | `CutoverChecklist` confirm modal | parity, but confirm text not state-specific (gap 3G) |
| commit | `POST /actions/commit` | pipeline `commitMigration` | parity |
| resume / retry / cancel / pause | actions endpoints | pipeline buttons | parity |

**Support-status parity (critical):** The backend exposes a clean
`GET /api/pipeline/policy` → `PolicyMatrix{autoCutoverDefault, supportedEngines,
supportedTrafficProviders, supportedReplicationModes, supportedExecutionModes,
notes[]}`. **The frontend never calls this endpoint, and `MigrationConfig` (TS)
has no `autoCutover` field.** So the UI cannot reflect, opt into, or gate on the
backend's real support matrix. This is the single biggest parity gap (PART 3A).

---

## C. Support-status visibility (CORRECTNESS BUGS, not polish)

- **No per-migration support status anywhere.** No screen renders
  automatic / manual / degraded / blocked / deferred / staging-only. The wizard
  offers category + DB-config only; it never shows what the backend will permit
  for the selected engine/provider/topology.
- **Wizard has no traffic-provider selector** (`routes/migrations/new/+page.svelte`):
  backend supports nginx/haproxy/caddy (automatic, fenced) + traefik/cloudflare/
  docker/dns (manual only, blocked for automatic). The wizard never sets
  `trafficProvider` and never surfaces the support distinction — the operator
  cannot even choose, and is never told why a provider is manual-only.
- **List page status badge hides safety states** (`routes/migrations/+page.svelte:65`
  `statusBadge` / `:76` `statusDot`): only `completed/failed/running/planned/
  rolled_back` have cases. `awaiting_cutover`, `needs_manual_intervention`,
  `rollback_degraded`, `interrupted` fall through to the default (no color, label
  from `formatLabel` only) — they are silently de-emphasized, violating the
  immutable rule "do not hide blocked/degraded status."
- **MigrationHeader hard-codes capability down** (`lib/components/MigrationHeader.svelte:57`):
  `awaiting_cutover` guidance says "No automatic cutover." regardless of whether
  the backend actually supports automatic for that engine/provider pair. This is
  the inverse truthfulness bug — it under-claims. The string must be derived from
  the backend policy, not a constant.
- **No "why" / "what's required to upgrade" / "what can I safely do next"**
  anywhere. The directive's explicit-support-state rule is unmet.

---

## D. Safety-state visibility

- `MigrationHeader.svelte` `operatorGuidance()` handles `awaiting_cutover`,
  `needs_manual_intervention`, `rollback_degraded` — **good**, but the copy is
  generic and the `awaiting_cutover` copy is wrong (see C). It is surfaced only
  as a ⚠ tooltip on lg+ screens (`Header:88-90`), easy to miss.
- `Observing` state (observation timer) exists in pipeline page (`stepStatuses[9]`,
  observation timer) — **wired**.
- **Reconnect / replaying / stale** states are NOT first-class. `WSConnectionState`
  is `connecting|connected|disconnected|reconnecting|failed` — there is **no
  `stale` or `replaying` state** (gap 3E). The pipeline page tracks a local
  `metricsStale` flag (`:400`, `:750`, `formatLag` returns "menyambung ulang…"
  when stale) but plan/dry-run/compatibility flows have no equivalent.
- **Idempotent action already applied / rejected due to stale fence/token/authz/state**
  is not distinguished — the UI shows generic success/failure toasts only.
- `interrupted` plan state gets a dedicated banner in pipeline page (`:1084`) —
  good. But the list page de-emphasizes it (C).

---

## E. Realtime protocol audit (WebSocket / streaming)

Verified by reading `lib/api/pipeline.ts` (`wsPipelineConnect`, `replayEvents`)
and the pipeline page WS wiring:

1. **Endpoints:** only `/ws/pipeline/{id}` is opened from the migration UI
   (in `wsPipelineConnect`). Plan/dry-run/compatibility/rollback flows open
   their own WS elsewhere (`lib/api/*`), but ONLY the pipeline flow has
   reconnect+replay logic. **The other flows have no reconnect/replay parity
   (gap 3E).**
2. **Reconnect:** `wsPipelineConnect` auto-reconnects with exponential backoff
   (`onclose` → `setTimeout(connect, delay)`), max 10 retries, then `failed`.
   Connection state surfaced via `onStatusChange` callback → UI
   `wsConnectionState`. **Good.**
3. **Replay:** on `onopen`, if `lastSequence > 0`, calls `replayEvents(id,
   lastSequence)` → `GET /pipeline/migrations/{id}/events?after_seq=…` and replays
   mapped messages. **Good mechanism, pipeline-only.**
4. **Dedup/ordering:** `lastSequence` tracks the max sequence seen; onmessage
   updates `lastSequence` if `msg.sequence > lastSequence`. Replayed events only
   advance `lastSequence` if greater. **Ordering-safe for pipeline flow**, but
   replay maps `MigrationEvent`→`WSMessageExtended` and re-feeds `onMessage` —
   if the live reducer assumes monotonic application it is fine; no explicit
   per-message dedup set elsewhere.
5. **Final REST reconciliation:** pipeline page re-fetches `getSession` on mount
   and on certain transitions — but there is **no explicit "after reconnect,
   reconcile to REST final state" call** in `wsPipelineConnect` itself (the page
   does it via its own load). Other flows lack even that.
6. **Stale window:** pipeline page sets `metricsStale=true` until a fresh frame
   arrives (`:400`, `:750`) and shows "menyambung ulang…" for lag — **but only
   for the pipeline metrics snapshot**, not a global "data is stale" banner.
7. **Message shape** (`WSMessageExtended`) carries `sequence, currentState,
   stage, stageIndex, stageTotal, progress, bytesDone, bytesTotal, speedBytes,
   eta, replicationLag, healthScore, riskScore, riskClass, timestamp` — the
   backend already emits rich evidence; the UI uses a subset (F).

---

## F. Progress / proof audit (operator evidence gaps)

Backend produces, UI **does / does not** render:

- stage + stageIndex/stageTotal → **rendered** (PipelineStepper).
- currentState → rendered (header + stepper).
- progress → rendered.
- bytesDone/bytesTotal/speed/eta → **in SyncSession (fetched) but NOT rendered**
  anywhere on the pipeline page (confirmed: `syncSessions` fetched, no UI panel).
- replicationLag → rendered (lag card + CutoverChecklist).
- healthScore → rendered (header + LiveMonitor).
- riskScore/riskClass → rendered (header).
- sequence/timestamp → carried, not surfaced.
- **topology mode** (host/container/compose/bastion) → **NOT rendered**.
- **source/target role** (primary/standby/master/slave) → **NOT rendered**
  (replication shows lag/status only).
- **fence status / generation** → **NOT rendered** (no UI field).
- **traffic ownership verification** (read-after-write marker) → **NOT rendered**.
- **checkpoint/resume status** → **NOT rendered** (backend persists
  cutoverCheckpoint; UI has no panel).
- **target verification / post-switch ownership proof** → **NOT rendered**.

So: the pipeline page is evidence-rich on lag/health/risk, but blind on
topology, roles, fence, ownership proof, checkpoint, and sync bytes. (PART 3C.)

---

## G. Secret / redaction audit

- **No "zero downtime" string exists in the UI** (grep clean) — backend honesty
  is carried into copy. Good.
- Toasts are generic ("Failed to load…", "Migration committed successfully!")
  — they do not dump raw backend error payloads. **Good.**
- `monitoring/+page.svelte:269` `toast.error(message.message)` and
  `processes/+page.svelte` `toast.error(message)` push a WS error-frame string to
  the user — a backend bug could leak a secret into that string. **Recommend a
  client-side redaction fallback** before rendering any backend-provided string
  (PART 3F, rule #5 "when in doubt, redact more").
- Password inputs exist only in auth/setup/ssh flows (expected, masked).
- `lib/api/migrations.ts:36` carries `password: string` for DB config — it is
  sent over authenticated WSS and redacted to `"set"` by the backend on read
  (Phase 2). Frontend must never render it; the wizard uses a password input, no
  echo. **OK, but add a redaction layer on any config-summary/export surface
  (PART 3F).**
- No raw `JSON.stringify` of config/audit is dumped to a visible surface today
  (diff page renders structured sections, not raw). LogViewer is server-logs,
  already backend-sanitized.

---

## H. Action-safety audit

- Destructive actions (start, cutover, commit, rollback, pause, resume, retry,
  cancel) exist as pipeline buttons with `toast` feedback — **parity** with
  backend action endpoints.
- **Disabled-state explanation:** the pipeline page gates steps (`canProceed` per
  step) but does NOT explain *why* a step is blocked, nor tie disabled actions to
  the backend support matrix (which it doesn't fetch — see B/C).
- **Confirmation modals:** `CutoverChecklist` shows a confirm modal before cutover
  — but the confirm text is generic, not state-specific (no migration identity,
  support status, or explicit "what this will/won't do"). `commitMigration` has no
  confirmation modal at all (PART 3G).
- **Double-submit / idempotency:** no client-side `pending`-state guard is
  visible; repeated clicks could fire duplicate action POSTs (backend has
  idempotency keys, but the UI doesn't reconcile the result). Gap 3G.
- **Stale-page unsafe action:** if the page is stale (disconnected) and the
  operator clicks rollback, the UI does not block/warn based on stale state.

---

## I. Accessibility / responsiveness audit (preliminary)

- Design system has semantic CSS-variable tokens + focus-visible handling
  (from Phase 2B redesign). Color tokens exist; **but support/safety states are
  conveyed by color alone in several places** (status dots, risk/health colors)
  without text/icon parity — violates "never rely only on color" (PART 3H).
- LogViewer / long event lists: no virtualization observed — long server logs
  could freeze the page (PART 3H perf).
- Responsive: header uses `hidden lg:inline` / `hidden md:flex` to hide chips on
  small screens — critical safety chips (rollback, WS state) can be hidden on
  mobile (PART 3H).
- Keyboard nav / ARIA: confirmation modals and action buttons need an audit pass
  (PART 3H). Not fully measured this pass; flagged for implementation.

---

## J. Phase 4G sequencing decision

From the investigation, the safe order (lowest risk → highest), one axis/slice
at a time, matching the directive's 8-part structure:

1. **3A Support matrix & truthfulness** — add `autoCutover` to `MigrationConfig`
   TS + wizard, call `GET /api/pipeline/policy`, build a canonical
   `SupportStatusBadge` + reason panel, fix `MigrationHeader` hard-coded copy,
   add provider selector + engine/topology support display, fix list-page status
   badge to cover all 20 states. *(highest truthfulness leverage, no behavioral
   risk)*
2. **3B Preflight / compatibility UX** — severity-grouped checklist with
   remediation text + canProceed/cannotProceed, exact environment summary.
3. **3C Pipeline timeline / progress / evidence** — render SyncSession bytes,
   topology mode, source/target role, fence status, traffic ownership proof,
   checkpoint/resume; distinguish in-progress/observing/awaiting/degraded/stale.
4. **3D Cutover / observation / manual intervention UX** — `AwaitingCutover`,
   `Observing`, `NeedsManualIntervention`, `RollbackDegraded` screens with
   known/unknown/blocked-reason/forbidden-assumptions/next-steps + runbook link +
   explicit no-auto-rollback-after-target-write messaging.
5. **3E Realtime reconnect / replay parity** — extend reconnect+replay+`stale`/
   `replaying` to plan/dry-run/compatibility/rollback flows; global stale banner;
   REST reconciliation after reconnect.
6. **3F Logs / audit / search / redaction** — surface audit trail, searchable
   filterable log/event view, client-side redaction fallback, safe export.
7. **3G Action safety & confirmation** — action bar aligned to legal transitions,
   disabled-reason tooltips, contextual confirmation modals with state-specific
   text, double-submit guard, idempotent reconciliation.
8. **3H Accessibility / responsive / performance** — focus rings, ARIA labels for
   status/actions, color+text parity, virtualization for long lists, mobile
   access paths for critical evidence/actions.

Each slice committed separately with build + `npm run check` green and the
slice's component/integration tests. No backend contract change required for any
slice except 3A (add `autoCutover` to the TS config type + wizard form — backend
already accepts it). The `/api/pipeline/policy` endpoint already exists.

**Stop conditions watch:** if any required backend field for operator evidence
does not exist (e.g. fence status / ownership proof / topology mode in the
session payload), implement will surface it and either (a) add a minimal backend
field, or (b) document the gap and degrade honestly rather than fake it.
