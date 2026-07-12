# Phase 2C — Additional Engines & Traffic Providers (Investigation + Design)

> **Status:** Investigation complete; design decision locked. This document is
> the Phase 2C contract. Phase 1, Phase 2A, and Phase 2B are **immutable
> baselines** — no invariant from those phases may regress. Phase 2C extends
> minimal-downtime cutover to additional engines/providers that can be proven
> correct in the available test topology. Default product wording stays
> **"minimal downtime"**; do not advertise an engine/provider pair until its own
> acceptance suite passes.

## 1. Investigation summary (verified by reading source at HEAD `5c64e43`)

### 1.1 MySQL replication is UNSEEDED (the core defect)

`ReplicationEngine.setupMySQL` (`replication.go:235`) issues
`SHOW MASTER STATUS` → `CHANGE MASTER TO MASTER_LOG_FILE=... MASTER_LOG_POS=...`
with **no initial data seed**. The target starts *empty*; the replica only
receives changes made *after* the binlog position. Any data written to the
source before replication was configured is silently lost on the target. This
violates the mandate's "initial seed wajib sebelum replication start" and is a
silent-data-loss bug on the cutover path. **Phase 2C fixes this: seed first.**

`promoteMySQL` (`replication.go:314`) is `STOP SLAVE; RESET SLAVE ALL;` with
**no fence assertion, no role/writability check before or after, no post-promote
verification**. `rollbackMySQL` (`replication.go:319`) already fails closed
(`ErrUnsafeTopology`) — good, keep it.

`mysqlLag` (`replication.go:296`) parses legacy `Seconds_Behind_Master` only.
It does not parse GTID (`Retrieved_Gtid_Set`/`Executed_Gtid_Set`), does not check
`Replica_IO_Running`/`Replica_SQL_Running`, and treats `NULL` as an error (good).
No version/binlog/server_id/privilege checks exist anywhere in the MySQL path.

### 1.2 Redis path is real but under-verified

`setupRedis` (`replication.go:443`) runs `REPLICAOF`, sleeps 3s, then verifies
`role:slave|replica` in `INFO replication`. `redisLag` (`replication.go:473`)
fails closed when `master_last_io_seconds_ago` is absent — good. `promoteRedis`
(`replication.go:499`) is `REPLICAOF NO ONE` — no role check before/after.
`rollbackRedis` (`replication.go:504`) re-points only after
`probeTopology(...).safeForRepoint()` — good. A reusable `redisRole` probe
exists (`topology.go:226`) and classifies master/slave/replica.

**Gap:** Redis has no pre-cutover role verification gate in the auto-cutover
path, and `promoteRedis` does not prove the target became a master afterward.
Both are fixable within the safety model.

### 1.3 MongoDB is already fail-closed (keep it that way)

`setupMongoDB` (`replication.go:531`) returns an explicit error because
`replica-set lag measurement is not implemented`. `mongoDBLag` (`replication.go:544`)
returns an error, never 0. `promoteMongoDB`/`rollbackMongoDB` are guarded. This
is the correct honest posture. Phase 2C keeps Mongo **deferred** — it adds an
explicit, early preflight block + operator-visible docs, but no automatic cutover.

### 1.4 Traffic-switch providers

`TrafficSwitchEngine` (`traffic.go`) has **8 providers**: Cloudflare, Nginx,
Traefik, HAProxy, Caddy, Docker, DNS (delegates to Cloudflare), plus a legacy
nginx path. The legacy `switchNginx`/`switchCloudflare` mutate config by regex +
Upload with **no idempotency key, no read-after-write ownership proof, no
sanitized persistence**.

The Phase 2A `NginxSwitcher` (`nginx_switch.go`) is the *correct* primitive:
idempotent (idempotency key + in-process cache), bounded (30s, 1 retry),
verified (read-after-write header/body marker → `ErrNginxVerifyFailed` fails
closed), sanitized (`shared.SanitizeJSONRawMessage`). It is the model for
Phase 2C's additional provider.

**Decision (one provider only, per mandate):** add **HAProxy** as the second
supported auto-switch provider. Rationale: it is config-file based (like nginx),
the switch is a server-line edit + reload, and ownership can be proven with the
same read-after-write health-marker pattern. Cloudflare/DNS/Traefik/Docker are
deferred (external-API / dynamic-reload / compose-risk — larger surface, weaker
ownership proof in the testbed). The legacy `switchHAProxy` already edits the
config; we replace it with a `HAProxySwitcher` mirroring `NginxSwitcher`.

### 1.5 Fencing & orchestrator model (preserve exactly)

`FencingAuthority` (`fencing_authority.go`): durable lease, `AssertHolds` before
**every** mutating step, stale/expired/conflicting token → `NeedsManualIntervention`.
`CutoverOrchestrator` (`cutover_orchestrator.go`): 12-step machine, switch
**before** promote, `pgCutoverDriver` interface lets tests inject fakes. The
MySQL/Redis paths plug into a parallel `mysqlCutoverDriver` /
`redisCutoverDriver` interface (same shape) so the orchestrator can drive
multiple engines without coupling to `*ReplicationEngine`.

`runAutoCutover` (`pipeline.go:1852`) currently hard-gates `pgEngine(dc.Engine)`
and `TrafficConfig != ""`. Phase 2C extends it: if the engine is MySQL+seeded or
Redis (and a supported provider is configured), build the matching driver +
switcher; otherwise keep the explicit "unsupported" fail-closed error.

## 2. Design decision

1. **MySQL (highest priority):** seeded replication only, same-major-compatible
   topology, behind a `MySQLPreflight` gate that blocks unsupported configs
   before any mutation. Seed path reuses `mysqlMigrator.StreamDumpCommand` /
   `StreamRestoreCommand` (already idempotent, streaming). Then `CHANGE
   REPLICATION SOURCE TO` (MySQL 8) / `CHANGE MASTER TO` (legacy) from the
   seeded position. Lag via `Seconds_Behind_Source` + `Replica_IO/SQL_Running`.
   Promote/demote require a valid fence token + role checks; dual-writer →
   `NeedsManualIntervention`.
2. **HAProxy (one provider):** `HAProxySwitcher` mirroring `NginxSwitcher`
   (idempotency key, bounded retry, read-after-write verify, sanitized persist,
   fail-closed on verify failure).
3. **Redis:** automate only the proven-safe path — pre-switch role gate +
   `REPLICAOF` + post-promote `role:master` verification + fenced. Otherwise
   remain manual `AwaitingCutover` / explicit degraded. No zero-downtime claim.
4. **MongoDB:** preflight block + docs + operator-visible limitation. No auto
   cutover.

## 3. Compatibility matrix (per engine / provider)

### Engines

| Engine | Phase 2C status | Topologies | Cutover |
|---|---|---|---|
| PostgreSQL | supported (Phase 2A) | same-major, 1 target | automatic (opt-in) |
| MySQL | **supported (2C, seeded)** | same-major; binlog ON; unique server_id; REPLICATION SLAVE priv; GTID optional (non-GTID for 2C) | automatic (opt-in, after seed) |
| MariaDB | experimental/internal | same as MySQL | automatic (shared path) |
| Redis | **supported (2C, gated)** | async replica; RDB/AOF; single target | automatic (fenced + role-verified) |
| MongoDB | **deferred** | replica-set (unimplemented lag) | manual only; auto cutover blocked |

### Traffic providers

| Provider | Phase 2C status | Ownership proof |
|---|---|---|
| Nginx | supported (Phase 2A) | read-after-write header/body marker |
| HAProxy | **supported (2C)** | read-after-write health marker |
| Cloudflare | deferred | external API; weak local proof |
| DNS | deferred | delegated to Cloudflare; TTL scatter |
| Traefik | deferred | dynamic-reload; no reload event proof |
| Caddy | deferred | config-validate only; no ownership probe |
| Docker | deferred | compose restart risk |

### MySQL blocked (preflight) configs → actionable errors

- binary logging OFF (`SHOW MASTER STATUS` empty binlog) → "enable log_bin".
- `server_id` missing/duplicate on target → "set unique server_id".
- replication user lacks `REPLICATION SLAVE` → "grant REPLICATION SLAVE".
- cross-major source/target → "same-major required".
- target not empty / unexpected replication state → "target must be a clean standby".
- source not a writable primary → "source must be primary".

## 4. Stop conditions (re-confirmed)

- Stop if MySQL seeded replication cannot be proven end-to-end in the
  dockerized MySQL pair.
- Stop if any MySQL/Redis path can still produce dual-writer ambiguity.
- Stop if HAProxy cannot verify ownership after switch.
- Stop if Mongo cannot be blocked cleanly.
- Stop if any engine/provider requires weakening the fencing/state-machine model.
- Stop if Phase 1/2A/2B tests regress.
