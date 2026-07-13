# Phase 4G — UI Truthfulness, Operator UX & Release-Parity Audit

**Status:** DONE — 8 slices shipped (3A–3H), frontend green
(`npm run check` 0 errors, 50 unit tests pass, `npm run build` green).

**Mandate (immutable):** The UI is NOT the source of truth. The backend stays
authoritative for authorization, fencing ownership, legal state transitions,
action gating, idempotency, and cutover/rollback legality. The UI only reflects
or guides — it never re-derives a safety decision. Every UI string is truthful;
ambiguity is shown as ambiguity; secrets are never rendered.

---

## What was true at the start

The frontend had drifted from the backend built across Phases 1, 2A/B/C/D, 3,
and 4:

1. It **ignored `GET /api/pipeline/policy`** entirely — the support matrix lived
   on the backend but the UI hard-coded "No automatic cutover" and let unsupported
   engines/providers be picked.
2. The pipeline action bar had **no confirmation for commit**, no disabled-reason
   tooltips, and **no double-submit guard**.
3. Realtime views had **no stale/disconnected handling** — a frozen frame could be
   read as "live".
4. Logs and audit were not redacted client-side; **secrets could be shown** in the
   event/audit panel.
5. Status states (`awaiting_cutover`, `needs_manual_intervention`,
   `rollback_degraded`, `interrupted`) could fall through to a near-invisible dot.

## Slices (committed separately)

| Slice | Commit | What changed |
|---|---|---|
| 3A support matrix & truthfulness | `cd5269e` | Consume `/api/pipeline/policy`; `lib/support-status.ts` + `SupportStatusBadge.svelte` (color+text+icon); wizard traffic-provider select + gated autoCutover; degrades to `unknown` not `deferred` when policy unloaded; list badge covers all states. |
| 3B preflight/compat UX | `69809ef` | `CompatibilityChecklist.svelte` groups Blocking / Review required / Passed; `role="status"` can-proceed summary; replaces inline list at step 1. |
| 3C timeline/progress/evidence | `74e676c` | `MigrationEvidencePanel.svelte` renders sync sessions, replication status, and cutover-safety evidence (fence + generation, traffic-verify, topology) from the audit trail, with honest empty states. |
| 3D cutover/observation/manual | `5648404` | `SafetyStatePanel.svelte` state-specific guidance (`role="alert"`, `aria-live`), manual-vs-automatic driven by `autoCutover`; forbids assumptions in degraded/unknown; rollback-not-safe caveat. |
| 3E realtime reconnect/replay | `1914b17` | `wsPipelineConnect` stale timer → `stale` state; `replaying` during reconcile; header words (not color) + global stale banner; replay via `?after_seq=` then REST sweep. |
| 3F logs/audit/search/redaction | `f80bdf7` | `lib/redact.ts` (creds, `-p`/`-P`, `REDISCLI_AUTH`/`PGPASSWORD`, Bearer, secret keys, token params); `MigrationLogAudit.svelte` Audit tab, search + level filter, fully redacted, redaction note. |
| 3G action safety/confirmation | `6453564` | Commit gets its own confirm modal; cutover/commit/rollback copy each states irreversibility (commit: rollback no longer safe); double-submit guard; stale-data blocks actions + toast; disabled-reason tooltips. |
| 3H a11y/responsive/perf | `c184f1b` | Confirm dialog Esc + autofocus + `aria-labelledby`/`aria-describedby`; log row cap (200 / 1000 filtering) + refine hint; badges pair color with text+icon. |

## Honesty invariants enforced

- **No stronger claim than evidence:** `engineSupport` returns `unknown` when the
  policy is unloaded; the badge never shows `automatic`/`degraded` without backend
  backing.
- **Ambiguity shown:** `stale`/`replaying` WS states are rendered in words; the
  global banner tells operators the data may be out of date.
- **No safe-rollback implication after target writes:** the commit modal says so
  explicitly; `SafetyStatePanel` carries the same caveat for `rollback_degraded`.
- **Secrets never rendered:** central `redactForDisplay` masks before display in
  the Audit tab; a visible note states the text is redacted client-side.
- **Destructive actions not too easy/ambiguous:** every destructive action is
  state-specific-confirmed, disabled with a reason while loading or stale, and
  guarded against double-submit.
- **Wording:** default "minimal-downtime migration"; no universal "zero downtime".

## Security constraints honored (verbatim, unchanged)

- UI tidak boleh: menyembunyikan blocked/degraded status; menampilkan automatic
  jika backend hanya manual/degraded; menampilkan success saat backend masih
  ambiguous; membuat operator mengira rollback aman setelah target write;
  menampilkan stale live data seolah-olah realtime; menampilkan action yang
  backend pasti tolak tanpa penjelasan; mengekspos secret di log/event/export/
  modal/form preview; membuat destructive action terlalu mudah/ambigu.
- Backend invariants intact: no secret on argv; `PGPASSWORD` via env; DSN creds
  SQL-escaped; central redaction; idempotency keys; audit + correlation IDs;
  ownership = read-after-write; unsupported engine/provider/topology NOT
  selectable as automatic; "minimal-downtime migration" wording.

## Verification

- `npm run check` → 0 errors, 0 warnings.
- `npx vitest run` → 50 tests pass (across support-status, CompatibilityChecklist,
  MigrationEvidencePanel, SafetyStatePanel, MigrationHeader, redact,
  MigrationLogAudit, ActionSafety, Accessibility).
- `npm run build` → green (adapter-static → `cmd/server/web/build`).

## Not changed (out of scope / intentionally left)

- No new backend endpoints — 4G is a UI parity audit; the backend from Phases 1–4
  is authoritative and unchanged.
- No design-token rework — existing tokens (`bg-success`, `text-warning`, etc. with
  `/10`–`/30` opacity modifiers) were used as-is.
- Live end-to-end operator walkthroughs were not executed in this environment; the
  parity guarantees are backed by unit tests on the components that render them,
  not a click-through of a running migration.
