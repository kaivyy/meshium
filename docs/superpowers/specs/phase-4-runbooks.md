# Phase 4 — Operator Runbooks & Certification Drills

> Operator-facing runbooks for the Phase 4 supported configurations, plus the
> certification drills that must pass on a release commit. Companion to
> `phase-4-production-certification.md` and `phase-4-release-matrix.md`.

## Runbook A — Verify support before planning (5 minutes)

1. `GET /api/pipeline/policy` on the Meshium server.
2. Confirm the engine (postgres/mysql/redis), the traffic provider
   (nginx/haproxy/caddy), and the execution mode (host/container/compose) are all
   listed as supported.
3. If the engine is **redis**, expect `degraded`: you MUST freeze source writes
   manually before cutover (no source-freeze primitive). Plan a maintenance
   window.
4. If the engine is **mongodb** or the provider is **traefik/cloudflare/docker/dns**,
   automatic cutover is rejected — use the manual switch path only.
5. If the topology is **bastion** or **cross-major**, automatic cutover is blocked —
   do not attempt it.

## Runbook B — Automatic cutover (PostgreSQL/MySQL, nginx/haproxy/caddy)

1. Plan with `autoCutover=true` and a fenced provider + `healthCheckUrl`.
2. The pipeline reaches the `traffic_switch` stage and acquires a durable fence
   lease. Every step asserts the lease holds (`AssertHolds`) before mutating.
3. Order is **switch → promote** (switch-before-promote): traffic moves to the
   target (still read-only standby), then the target is promoted to primary.
   There is no window where both source and target accept writes.
4. Ownership is proven by read-after-write: the switcher GETs the verify URL and
   requires the target marker. A failed verify fails closed — **no auto-rollback**.
5. On success the stage records `switched` + a `traffic_switch` cutover record.
6. On any failure the cutover stops at `NeedsManualIntervention` with a sanitized
   failure on the checkpoint. Proceed to Runbook C.

## Runbook C — Fail-closed recovery (NeedsManualIntervention)

The cutover fails closed: it never silently leaves traffic split or data
corrupted. Recovery:

1. Read the persisted cutover record / checkpoint (`GET /api/pipeline/recovery/<id>`).
2. Determine the last completed sub-state (`Preflight` → `Seed` → `Replicating` →
   `Verifying` → `AwaitingCutover` → `FencingSource` → `CatchingUp` →
   `VerifyingTarget` → `Switching` → `Promoting` → `Observing` → `Completed`).
3. If `Switching` failed: traffic did NOT move to target (or moved but verify
   failed). Inspect the switcher result. If the config IS pointing at target but
   verify failed, decide rollback vs. manual confirm — NOT automatic.
4. If `Promoting` failed: target is a standby that did not promote. Re-run
   `pg_promote()`/`REPLICAOF NO ONE` manually, then verify the target serves
   reads+writes.
5. Resume restarts from the last checkpoint (idempotent; re-entries skipped).

## Runbook D — Redis degraded cutover

Redis has **no source-freeze** primitive. Before an automatic (degraded) cutover:

1. Freeze application writes to the source Redis manually (maintenance window).
2. Let replication drain (async gap is the RPO; minimal, not zero).
3. Run the cutover; the orchestrator records `source_freeze_not_supported` as a
   degraded condition — this is honest, not a defect.
4. After promote, release the source write freeze.

Never select Redis as `automatic` without a manual freeze step — the dual-writer
window is bounded by the async gap only.

## Runbook E — PostgreSQL logical replication (scoped `degraded`)

Logical pub/sub is proven at the primitive level (4D) but **not wired through the
fenced orchestrator**. To use it today:

1. Set source `wal_level=logical`; create a replicator role + hba rule.
2. Run `PreflightLogicalPG` (same-major, source primary, target independent
   primary, connectivity).
3. Run `SetupLogicalReplicationPG` (idempotent publication + subscription).
4. Wait for parity via `WaitForLogicalCatchUpPG` (per-table row-count).
5. Repoint traffic via a fenced provider as a SEPARATE manual step. The logical
   path does not auto-promote; it is a continuous replication channel, not a
   one-shot cutover. Treat it as `degraded` until the orchestrator wiring lands.

## Certification drills (must pass on a release commit)

Run on the release commit, in CI or on a staging host with Docker:

```
go test ./internal/mod/migration/                                   # unit: no regression
go test -tags integration ./internal/mod/migration/ \
  -run 'TestPGComposeCutoverLive|TestCaddySwitcherLive|TestSyncResumeAfterKillLive|TestPGLogicalReplicationLive'
```

Drill assertions (all must hold):

- [x] 4A: `TestPGComposeCutoverLive` — compose pair promoted, data intact.
- [x] 4B: `TestCaddySwitcherLive` — caddy config swap + reload + marker moved.
- [x] 4C: `TestSyncResumeAfterKillLive` — killed transfer resumes byte-identical.
- [x] 4D: `TestPGLogicalReplicationLive` — pub/sub row replicates, parity reached.
- [x] 4E: `TestPolicy*` — guardrail unification + execution-time gate + endpoint.
- [x] Guardrails: `TestSupportedProviderGuardrail` — traefik/cloudflare/etc. rejected.

A drill failure is a **no-ship**: fix the regression, re-run all drills, then tag.
