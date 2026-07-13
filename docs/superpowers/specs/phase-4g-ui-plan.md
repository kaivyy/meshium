# Phase 4G — UI Truthfulness & Release-Parity: Implementation Plan

> PART 2 of Phase 4G. Companion to `phase-4g-ui-investigation.md` (findings) and
> `phase-4g-ui-final-report.md`. Implements the 8 slices 3A→3H as separate,
> independently-testable commits. No backend contract change is required except
> slice 3A, which *consumes* the already-existing `GET /api/pipeline/policy` and
> adds the already-accepted `autoCutover` field to the TS config type + wizard.
>
> **Immutable rule:** UI is not the source of truth. Backend stays authoritative
> for authz, fencing, legal transitions, action gating, idempotency, cutover/
> rollback legality. Every slice only *reflects* or *guides*; it never re-derives
> safety decisions the server makes.

## Global Constraints (from directive — one line each)

- UI must never show a stronger capability than backend evidence supports.
- Every migration/config must show support status + why + what upgrades it + what to do next.
- If state is ambiguous, UI must show ambiguity; do not smooth over with optimistic success.
- Backend remains authoritative; UI may guide but not enforce safety.
- All user-visible text surfaces must be sanitized; when in doubt, redact more.
- Any realtime view must tolerate disconnect/reconnect/duplicate/out-of-order + final REST reconciliation.
- UI is for operators handling risky migrations; clarity beats visual cleverness.
- Do not hide blocked/degraded; do not show automatic where backend is manual/degraded; do not show success while backend is ambiguous; do not imply rollback is safe after target writes; do not show stale data as realtime; do not expose secrets; do not make destructive actions too easy/ambiguous.
- Wording: default "minimal-downtime migration"; never claim universal "zero downtime".
- One compatibility/UX axis per increment; commit + test each slice; no regression of Phase 1–4 backend behavior or existing UI flows.

## 1. Current UI inventory (from investigation)

Routes: `migrations/new`, `migrations/[id]`, `migrations/[id]/pipeline`,
`migrations/[id]/diff`, `migrations` (list). Components: `MigrationHeader`,
`PipelineStepper`, `CutoverChecklist`, `ObservationPanel`, `LiveMonitor`,
`PlannerView`, `PlaceholderPage` + `ui/*` primitives. API client `lib/api/*`,
WS client `lib/api/pipeline.ts`. Full table in the investigation doc §A.

## 2. Backend parity gaps (prioritized)

1. **Support status absent from UI**; wizard has no provider selector; no
   `autoCutover`; `/api/pipeline/policy` never called; `MigrationConfig` TS lacks
   `autoCutover`. (3A)
2. **MigrationHeader hard-codes "No automatic cutover"** (under-claims). (3A)
3. **List-page status badge hides** `awaiting_cutover` / `needs_manual_intervention`
   / `rollback_degraded` / `interrupted`. (3A)
4. **Preflight shown as flat list** — no severity grouping, remediation, or
   canProceed/cannotProceed. (3B)
5. **Evidence not rendered**: SyncSession bytes, topology mode, source/target
   role, fence status, traffic-ownership proof, checkpoint/resume. (3C)
6. **Manual-intervention states thin**: `AwaitingCutover` copy wrong,
   `NeedsManualIntervention`/`RollbackDegraded` lack known/unknown/blocked-reason/
   next-steps/runbook link + no-auto-rollback warning. (3D)
7. **Reconnect/replay pipeline-only**; no `stale`/`replaying` WS state; plan/
   dry-run/compatibility/rollback flows lack parity; no global stale banner. (3E)
8. **Audit trail not surfaced**; no searchable/filterable log/event view; no
   client-side redaction fallback; export not safe-guarded. (3F)
9. **Action safety**: disabled actions don't explain why; no state-specific
   confirm modals (commit has none); no double-submit guard; stale-page unsafe
   clicks not blocked. (3G)
10. **A11y/responsive/perf**: color-only status; LogViewer no virtualization;
    safety chips hidden on mobile; focus/ARIA unverified. (3H)

## 3. Prioritized implementation order

3A → 3B → 3C → 3D → 3E → 3F → 3G → 3H (each its own commit + tests).

## 4. Components / screens to modify (per slice)

- **3A:** `lib/api/pipeline.ts` (add `getPolicy` + `autoCutover` to `MigrationConfig`),
  `routes/migrations/new/+page.svelte` (provider selector + autoCutover toggle +
  support panel), `lib/components/MigrationHeader.svelte` (derive copy from
  policy), `routes/migrations/+page.svelte` (full state badge), new
  `lib/components/SupportStatusBadge.svelte` + `lib/support-status.ts` (model).
- **3B:** `routes/migrations/new/+page.svelte` step 1 (severity-grouped
  checklist + remediation + canProceed), reuse new `CompatibilityChecklist.svelte`.
- **3C:** `routes/migrations/[id]/pipeline/+page.svelte` (render SyncSession,
  topology, roles, fence, ownership proof, checkpoint), new
  `MigrationEvidencePanel.svelte`.
- **3D:** `MigrationHeader.svelte` + pipeline page sections for `AwaitingCutover`
  / `Observing` / `NeedsManualIntervention` / `RollbackDegraded` (new
  `SafetyStatePanel.svelte`).
- **3E:** `lib/api/pipeline.ts` (`wsPipelineConnect` gains `stale`/`replaying`
  states + REST reconciliation; extract reusable reconnect for other flows),
  pipeline + plan + dry-run + compatibility + rollback pages use the shared indicator.
- **3F:** new `MigrationLogAudit.svelte` (audit trail + searchable events +
  redaction fallback + safe export); wire `getAuditTrail` + `getEvents`.
- **3G:** pipeline action bar (`ActionBar.svelte`), confirm modals
  (`CutoverConfirm.svelte`, `CommitConfirm.svelte`, `RollbackConfirm.svelte`),
  double-submit guard, disabled-reason tooltips.
- **3H:** focus/ARIA pass on new components, color+text parity, LogViewer
  virtualization, mobile access paths, loading/empty/error states.

## 5. APIs / WS contracts consumed

- `GET /api/pipeline/policy` → `PolicyMatrix` (NEW client call, 3A). Already exists.
- `MigrationConfig.autoCutover` JSON field (NEW TS field + wizard, 3A). Already accepted.
- Existing: `getSession`, `getStages`, `getCompatibilityChecks`, `getReplication`,
  `getSyncSessions`, `getHealth`, `getAuditTrail`, `getEvents`, `configure`,
  action POSTs — all unchanged.
- WS: `/ws/pipeline/{id}` (replay+reconnect already present; extended to
  `stale`/`replaying` + shared across flows, 3E). Other flows use their existing
  WS; 3E adds the shared reconnect/replay wrapper.

## 6. Truthfulness / support-matrix plan (3A)

- Canonical model `lib/support-status.ts`: `supportStatus(state, policy, config)`
  → `{ level: 'automatic'|'manual'|'degraded'|'blocked'|'deferred'|'staging-only',
  reason, upgrade, nextAction }`. Driven by `PolicyMatrix` from the server.
- `SupportStatusBadge.svelte`: shared, color+text+icon (never color alone).
- Wizard: fetch `getPolicy()` on mount; show engine/provider/topology support
  panel; provider `<select>` limited to `supportedTrafficProviders` for automatic
  (others shown as "manual-only, not selectable as automatic" with reason);
  `autoCutover` checkbox enabled only when policy + selection permit, with the
  exact reason when disabled.
- `MigrationHeader`: replace constant "No automatic cutover" with policy-derived
  copy (`awaiting_cutover` → "Automatic cutover configured: confirm switch" or
  "Manual cutover required — reason: <policy note>").
- List page: `statusBadge`/`statusDot` cover all 20 states with distinct,
  text-bearing labels.

## 7. Safety-state UX plan (3D)

`SafetyStatePanel.svelte` renders, per state:
- `AwaitingCutover`: readiness checklist (source frozen/fenced, lag, target
  health/role, provider, confirm requirement, exact next action).
- `Observing`: observation timer, target health, traffic-ownership verification,
  write indicators, rollback-policy reminder.
- `NeedsManualIntervention`: what is known / unknown / exact blocked reason /
  unsafe actions / recommended next steps / runbook + evidence link.
- `RollbackDegraded`: attempt summary, succeeded/failed sub-steps, source/target
  role uncertainty, forbidden assumptions, next actions, explicit
  no-auto-rollback-after-target-write note.

## 8. Realtime / reconnect plan (3E)

- Extend `WSConnectionState` with `'stale'` and `'replaying'`.
- `wsPipelineConnect` already reconnects + replays via `after_seq`; add: on
  reconnect start → emit `replaying` until first live frame; if no frame within
  `staleTimeout` → emit `stale`; re-fetch `getSession` after reconnect to
  reconcile final REST state.
- Extract a shared `connectWithReplay(url, {afterSeq, onMessage, onStatus})`
  and route plan/dry-run/compatibility/rollback WS through it.
- Global "Data may be stale" banner when `stale`/`reconnecting` and data is
  operator-actionable.

## 9. Log / audit / redaction plan (3F)

- `MigrationLogAudit.svelte`: tabs for Event stream / Audit trail / User summary
  / Raw diagnostics (clearly labeled). Search box (message/correlation id),
  level filter, stage filter.
- Client-side redaction fallback `redactForDisplay(s)` (reuse backend patterns:
  `-pPASSWORD`, `REDISCLI_AUTH`, `Bearer`, connection-string creds) applied to
  every backend-provided string before render, even though backend sanitizes.
- Export: `exportReport` Blob download only if authorized; preview is redacted.

## 10. Accessibility / responsiveness plan (3H)

- All status/safety conveyed by text+icon+color (never color alone).
- `aria-live="polite"` on status/state-change regions; `role="status"`.
- Modal focus trap + Esc + return-focus; buttons have `aria-label` with state.
- LogViewer virtualized (windowed render) for long lists.
- Critical evidence/actions reachable on mobile (no `hidden` that drops safety
  chips on small screens — move to a collapsible "Safety & status" sheet).
- Loading/empty/error states for every new panel.

## 11. Test plan (PART 4)

Project has **no test runner** (only `svelte-check`). Plan adds `vitest` +
jsdom + `@testing-library/svelte` and a `test` script; folds existing
`websocket.retry.test.ts` in.

**A. Unit/component (vitest):**
- `support-status.test.ts` — `supportStatus()` returns correct level/reason for
  automatic/manual/degraded/blocked/deferred given a `PolicyMatrix`.
- `SupportStatusBadge` render — blocked/degraded/manual labels + text (not color-only).
- `CompatibilityChecklist` — critical blocker renders remediation; canProceed false.
- `MigrationEvidencePanel` — bytesDone/bytesTotal/speed/ETA formatting; null-safe.
- `wsReducer` — dedup/replay by sequence; stale/replaying transitions.
- stale/disconnected banner logic.
- `redactForDisplay` — strips password/REDISCLI_AUTH/Bearer/conn-string creds.
- action enable/disable logic + confirm-modal content (state-specific).

**B. Integration/E2E:** no e2e harness exists; add a lightweight Playwright-free
   path is out of scope. Instead, component tests drive the flows above against
   mocked `api`/`ws`. (Documented limitation; backend integration tests cover
   the server side.)

**C. Failure-state UI tests (component):** backend returns blocked → badge
   blocked + reason; degraded/manual → correct label; WS disconnect mid-stage →
   banner; replay duplicate events → no double increment; stale + forbidden
   action → blocked with explanation; terminal error → evidence persisted.

**D. Security/redaction tests:** `redactForDisplay` on config summary / toast /
audit payload / export — no secret survives.

**E. A11y tests:** rendered HTML has `aria-live`/`role`/`aria-label`; status not
   conveyed by color alone (text present).

Run after every commit: `npm run build`, `npm run check`, `npm test`.

## Stop conditions (from directive)

Stop and report if: backend contracts too ambiguous for truthful UI; required
backend fields (fence status / ownership proof / topology mode) absent from the
session payload; UI cannot reflect a safety state without faking it. In that
case either add a minimal backend field (smallest safe change) or document the
gap and degrade honestly. (None expected — `/api/pipeline/policy` and the
session payload already carry what 3A–3E need; 3C evidence fields may require a
small backend addition for fence/ownership/topology, to be assessed at 3C.)
