# Phase 2A — Fenced PostgreSQL Cutover: Delivery Report

**Date:** 2026-07-12
**Phase:** 2A-2 → 2A-6 (fenced cutover, opt-in `AutoCutover`)
**Baseline (immutable):** Phase 1 P0 safety fixes (`013f96c` → `0f8512c`)
**Head after Phase 2A:** `05dd990` + Commit 7 (`43b…` below)

## Decision: SHIP-PHASE-2A (opt-in, no Phase 1 regression)

The fenced cutover is delivered behind an opt-in flag (`AutoCutover=false` by
default). The Phase 1 manual path is untouched. Unit + integration tests pass.
One real production bug was found and fixed by the integration test (see §4).

---

## 1. Commits in this phase

| # | Commit | What |
|---|---|---|
| 6 | `05dd990` | Fenced cutover orchestration: 12-step machine, fence lease re-assert on every mutation, checkpoint reconciliation, fail-closed, no-dual-writer ordering. 12 unit tests. |
| 7 | `43b…` | Integration tests (live Docker PG pair), operator runbook, known-limitations update, this report. |

Earlier Phase 2A commits (context): `b4de1c1` (fencing authority + lease
table), `a78c810` (sub-state machine), `ddcc811` (PG preflight/lag/promote
primitives), `05da45d` (Nginx idempotent switch + verify).

---

## 2. What was built

- **`FencingAuthority`** (`fencing_authority.go`): durable lease in SQLite.
  `Acquire`/`AssertHolds`/`SetState`/`Release`/`Renew`/`Load`. Sentinels:
  `ErrFenceLeaseNotHeld`, `ErrFenceLeaseConflict`, `ErrFenceLeaseStale`.
  `FenceTTL = 5m`. Token is monotonic per migration.
- **`cutoverMachine`** (`cutover_substate.go`): `Load`/`Save`/`MarkStep`.
  Checkpoint mirrors sub-state onto the lease. `Load` short-circuits a
  completed cutover (the lease is deliberately released).
- **`CutoverOrchestrator`** (`cutover_orchestrator.go`): `Run` drives 11 edges
  (12 sub-states) from `preflight` to `completed`. `advance` asserts the lease
  BEFORE every mutation. Idempotent re-entry (`ErrCutoverReentryDone`).
  Hard failure persists + fails closed. Success → `Release` + `Completed`.
- **`ReplicationEngine`** primitives (`pg_cutover.go`): `Preflight`,
  `WaitForCatchUpPG`, `PromotePG` (SQL `pg_promote`, post-promote probe).
- **`NginxSwitcher`** (`nginx_switch.go`): idempotent switch + read-after-write
  verify (Phase 2A-5).
- **`trafficSwitchStage.runAutoCutover`** (`pipeline.go`): builds the
  orchestrator from `PipelineContext`; short-circuits when `AutoCutover` is
  false (manual path unchanged).

---

## 3. Test results

### Unit (`go test ./internal/mod/migration/` — ~1s, all pass)
12 orchestrator tests cover: happy path, no-dual-writer ordering, fail-closed
on preflight/switch/promote failure, stale-lease mid-run, idempotent re-entry,
resume-after-partial-fail, target-not-standby-before-switch, failure
sanitization, migration-ID/holder validation gates.

### Integration (`go test -tags integration …` — ~64s, all pass)
Live Docker PostgreSQL primary+standby pair:
- `TestPGCutoverPrimitivesLive` — real preflight → lag drain → promote; target
  serves the replicated row and accepts writes after promote.
- `TestPGPreflightFailsWhenTargetNotStandby` — promoted-out-of-band target
  fails the preflight gate (proves the pre-switch safety boundary).
- `TestFencedCutoverWithRealPG` — full 11-step orchestrator with a real fence
  lease + real `ReplicationEngine`; reaches `SubCompleted`, target promoted.

### Regression gate
Phase 1 manual path: `AutoCutover=false` default → unchanged behavior. Full
`migration` package unit suite green.

---

## 4. Bug found and fixed by integration testing

**`pgReplicatorConnectivity` omitted `-d postgres`.** The probe ran
`psql -h … -U replicator -tAc 'SELECT 1;'` with no `-d`, so psql defaulted the
dbname to the username `replicator` — a replication-only role with no DB of
that name → `FATAL: database "replicator" does not exist` → exit 2 →
**preflight would have falsely failed on every real cutover.** Fixed in
`pg_cutover.go` to connect to the `postgres` DB explicitly. This is the kind
of defect unit fakes cannot catch (the fake `PGPASSWORD=… psql …` returns
`"1"` without parsing dbname).

---

## 5. Residual limitations (must not be over-claimed)

See `docs/known-limitations.md` (Phase 2A section). Key ones:
- Source Postgres read-only **GUC is deferred** (hard fence = lease only).
- Single target + single nginx provider; no multi-standby pinning.
- One lease lifetime (~4m50s) cap; no renewal loop.
- No automatic backout (operator-owned, runbook §5).
- PostgreSQL **same-major only**; MySQL/Mongo/Redis cutover not implemented.

## 6. Operator guidance

`docs/cutover-runbook.md` — pre-conditions, trigger, failure handling
(resume vs. fresh run decision tree), manual backout, verification commands.
