# Phase 5 — Workstream C: CutoverEngine + FreezeManager + Fencing

**2026-07-14** · Phase 5.

Audit demanded: wire `CutoverEngine`/`FreezeManager`/`TrafficSwitchEngine` (audit
said "zero live caller, trafficSwitchStage only writes manualrequired then
auto-advances"); add `AwaitingCutover` state; freeze source at cutover, unfreeze
target after; hold a fence token to prevent dual-writers; lease/fencing with TTL +
renewal; observation auto-rollback with stop-on-write → `NeedsManualIntervention`.

> The audit's "zero live caller" is **stale**. On this branch the cutover machinery
> is fully wired: `trafficSwitchStage` (`pipeline.go:1933`) stops at
> `StateAwaitingCutover` for manual mode and runs a **fenced auto-cutover** for
> configured providers; `FreezeManager`, `ObservationEngine`, and multiple fenced
> switchers (nginx/haproxy/caddy) are present.

---

## C1. Cutover + Freeze wiring (current reality)

- `trafficSwitchStage.Execute` (`pipeline.go:1960`) → for manual cutover returns
  `ErrAwaitingCutover`, recorded as `StateAwaitingCutover` (`pipeline.go:423`),
  loop stops cleanly (P0-2/P1-5 satisfied). For auto cutover → `runAutoCutover`
  (`pipeline.go:2041`).
- `runAutoCutover` acquires a **fence lease** before driving the switch
  (`pipeline.go:2041` asserts `fenceLeaseRepo`, fails closed if the repo is not
  fence-capable). It enforces engine allow-list (`postgres, mysql, redis`),
  requires `DatabaseConfig` + `TrafficConfig`, and refuses providers without a
  fenced switcher — **fails closed, never silently advances**.
- `FreezeManager.FreezeWrites`/`UnfreezeWrites` (`freeze.go:36/94`) cover
  MySQL/PostgreSQL/MongoDB (Redis handled via replica role, not write-freeze).
- `Cutover`/`Commit` (`pipeline.go:547/652`): commit refused (409) unless
  `StateAwaitingCutover` (`pipeline.go:656`), matching the FE's 409 handling.
- `postCutoverObservationStage` (`pipeline.go:2282`) runs the observation window.

### Remaining gap
- **Certify** in the live matrix that each engine's cutover (manual + fenced-auto)
  reaches `StateAwaitingCutover` and commits without a false `committed`.

## C2. Fencing / lease (current reality + gap)

- Lease acquisition exists inside `runAutoCutover` (`pipeline.go:2041`), driven by
  `fenceLeaseRepo` on the concrete `*sqliteRepo`.
- **Gap (genuine, P2-4):** the lease is acquired for the cutover action but there
  is **no independent TTL + renewal loop** protecting the primary during the whole
  observation window, and no partition-time re-validation that prevents a
  demoted source from accepting writes if the fence token is lost. The DB-level
  freeze (`freeze.go`) is the real split-brain guard today; the lease is a
  cutover-orchestration lock, not a persistent primary lease.

### Phase 5 net-new (C2)
- Add a `FenceLease` with TTL + renewal goroutine held for the migration's
  primary-ownership duration (cutover → commit/rollback), persisted in the repo.
  On partition/lease loss, the engine must treat the migration as
  `NeedsManualIntervention` (never assume the source is still read-only). This is
  the one C-item that is partial and worth finishing for the "no split-brain"
  certification claim.

## C3. Auto-rollback & observation (current reality)

- `ObservationEngine` (`observation.go`) has `AutoRollback` (default true,
  `observation.go:53`), threshold checks (`observation.go:95-147`), and triggers
  auto-rollback on breach.
- Stop-on-write → `NeedsManualIntervention` is enforced in the rollback terminal
  logic (`pipeline.go:892-909`; `force_transition_p0_test.go:49`).

### Remaining gap
- Confirm via the live matrix that an injected target-write during observation
  routes to `NeedsManualIntervention` (not auto-rollback) — this is a **test**, not
  a code change (the guard already exists).

---

## Phase 5 net-new work for Workstream C

| Item | Status | Action |
|---|---|---|
| CutoverEngine/FreezeManager wiring (P2-1) | done | certify via live matrix |
| AwaitingCutover state (P1-5) | done | certify |
| Fenced auto-cutover (P2-1) | done (nginx/haproxy/caddy) | certify |
| Lease TTL + renewal (P2-4) | **partial** | finish persistent lease + partition→NeedsManual |
| Observation auto-rollback + stop-on-write | done | certify via failure-injection test |

**Honest claim:** cutover/freeze/fencing infra is wired and fenced; the genuine
C-work is finishing the **persistent fence lease (TTL/renewal/partition→manual)**
and **certifying** the whole cutover/rollback path live.
