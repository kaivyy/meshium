# Phase 2C — Additional Cutover Engines & Traffic Providers

> **Date:** 2026-07-13
> **Scope:** Extend the Phase 2A fenced PostgreSQL cutover to additional engines
> (MySQL seeded, Redis) and one additional traffic provider (HAProxy), behind
> the same fail-closed contract. Immutable baselines: Phase 1, Phase 2A, Phase
> 2B. MongoDB remains deferred (fails closed).
> **Spec/contract:** `docs/superpowers/specs/2026-07-13-phase2c-engine-providers.md`

## 1. Commit list (8 commits)

| # | Commit | Summary |
|---|---|---|
| 54 | `docs` | Investigation + design decision + compatibility matrix |
| 55 | `feat(migration)` | MySQL preflight gates (same-major, log_bin, server_id, read_only, replicator) + unit tests |
| 56 | `feat(migration)` | MySQL **seeded** replication: seed before `CHANGE REPLICATION SOURCE TO` (fixed the unseeded-defect); MySQL 8 syntax; lag via `Replica_IO/SQL_Running` |
| 57 | `feat(migration)` | MySQL fenced promote: freeze source → STOP REPLICA → target writable, post-promote verification; rollback already fail-closed |
| 58 | `feat(migration)` | HAProxy provider: `HAProxySwitcher` mirroring `NginxSwitcher` (config-test, reload, read-after-write verify, sanitized persist) + pipeline dispatch |
| 59 | `feat(migration)` | Redis supported-path automation: `preflightRedis` + `promoteRedisCutover` (REPLICAOF NO ONE + role verify), fail-closed on any ambiguity |
| 60 | `docs` | MongoDB compat blocking (verified at 4 layers) + `docs/mongodb-cutover-runbook.md` + known-limitations matrix |
| 61 | `docs` | Integration suites (MySQL seeded live pair) + known-limits update + this report |

## 2. Supported / experimental / deferred matrix

| Engine | Cutover | Seeded? | Source freeze | Dual-writer guarantee | Notes |
|---|---|---|---|---|---|
| PostgreSQL | supported (Phase 2A) | n/a | `read_only` GUC deferred (lease is the hard fence) | happy-path only | verified live |
| MySQL / MariaDB | **supported (new)** | **yes** (defect fixed) | `read_only` ON (closes window on happy path) | happy-path | same-major only |
| Redis | **supported (new)** | n/a (replica) | **none** | **gap-bounded by async repl** | accepted residual risk |
| MongoDB | **deferred** | n/a | fail closed | n/a — refused | no lag measurement |

| Traffic provider | Auto-switch | Verify |
|---|---|---|
| nginx | supported (Phase 2A) | read-after-write marker |
| haproxy | **supported (new)** | read-after-write marker |
| cloudflare/traefik/caddy/docker/dns | **not supported** | fail closed at dispatch |

## 3. Tests per engine/provider pair

| Pair | Unit (fail-closed gates) | Integration (live) |
|---|---|---|
| MySQL preflight | `mysql_cutover_test.go` ×6 (cross-major, log_bin off, shared server_id, source RO, target not-RO, happy) | `mysql_cutover_integration_test.go` (seeded pair, preflight, replicate, promote, verify) |
| MySQL promote | `mysql_cutover_test.go` ×4 (order/freeze, no-replica, freeze-fail, stays-RO) | same live test |
| HAProxy | `haproxy_switch_test.go` ×7 (success, idempotent, config-test fail, reload fail, verify mismatch, sanitize, empty-verify) | — (identical contract to nginx unit) |
| Redis | `redis_cutover_test.go` ×6 (preflight happy/source-not-master/wrong-master/link-down, promote happy/no-replica/not-master) | — |
| Dispatch | `phase2c_dispatch_test.go` (engine map, provider map, mongo preflight/promote fail-closed) | — |
| Orchestrator | `cutover_orchestrator_test.go` (updated to engine-agnostic `cutoverDriver`) | `pg_cutover_integration_test.go` (live PG full cutover) |

Total new/updated `migration` package tests: **311 passing, 0 failing**.

## 4. Evidence

- **Seeded-replication defect fixed:** `setupMySQL` now seeds (streamed
  `mysqldump` → `mysql` restore) *before* `CHANGE REPLICATION SOURCE TO`.
  `TestMySQLCutoverPrimitivesLive` asserts the target holds both the seeded
  probe row and a post-seed replicated row after promote (count = 2).
- **No silent data loss:** without the seed, the live integration test would
  have shown count = 1 (only the replicated row). It shows 2.
- **Fail-closed on every unsupported path:** Mongo refused at dispatch
  (`cutoverEngineType` → `failAutoCutover`), in `CutoverPreflight`,
  `CutoverPromote`, and the per-primitive `setupMongoDB`/`promoteMongoDB`/
  `rollbackMongoDB`.
- **No-dual-writer for MySQL:** `promoteMySQLCutover` freezes the source
  (`read_only=ON`) *before* `STOP REPLICA` and *before* making the target
  writable; a source that resists the freeze prevents the promote entirely.
- **Traffic ownership proven:** both switchers require a read-after-write
  verify marker; absence → `ErrTrafficVerifyFailed` → `NeedsManualIntervention`.

## 5. Residual risks (honest)

1. **Redis has no source freeze.** The dual-writer window is bounded by the
   async replication gap, not eliminated. For strict no-dual-writer
   requirements, use manual cutover.
2. **MySQL source-freeze is `read_only` GUC**, not a PG-style hard freeze; a
   writer bypassing the lease could touch the source during the switch→promote
   window. The fence lease serializes; `read_only` closes the window on the
   happy path.
3. **Cutover bounded by one lease lifetime** (~4m50s); no renewal loop.
4. **MongoDB cutover is manual only** (runbook provided).
5. **No automatic backout**; the operator owns it (runbooks provided).

## 6. Product wording recommendation

Keep **"minimal downtime"**; do **not** advertise "zero-downtime" for any
engine. Per-provider: describe PostgreSQL/MySQL as "fenced, seeded cutover";
Redis as "fenced cutover (bounded async gap)"; MongoDB as "manual cutover
(automated cutover not supported)". nginx + haproxy are the supported
auto-switch providers. Do not claim automatic backout.
