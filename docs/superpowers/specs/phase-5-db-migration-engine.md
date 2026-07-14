# Phase 5 — Workstream A: Database Migration Engine (PG / MySQL / Mongo / Redis)

**2026-07-14** · Part of Phase 5 (Minimal-Downtime Migration Realization).

Pattern: design from audit → implement → test live → certify.
Audit basis: `docs/audit-2026-07-11.md` (P0/P1/P2 tables). Reality basis: direct
read of `internal/mod/migration/*.go` on this branch.

> **Important grounding note.** The audit (2026-07-11) predates a large body of
> work that already lands on this branch. The DB engine, replication engine,
> single-file transfer engine, fenced auto-cutover, and observation auto-rollback
> **already exist and are wired into the pipeline**. This doc therefore records
> *what the audit demanded*, *what the repo already implements (with file:line)*,
> and *the genuine remaining gap* — so Phase 5 work is scoped to what is actually
> net-new, not re-implemented fiction.

---

## A1. PostgreSQL minimal-downtime

### Audit demanded
Physical `pg_basebackup` seed (same-version) → Freeze + final sync → promote target
(`pg_promote`) → demote source (fix the `rm standby.signal` bug that promotes
instead of demotes) → TrafficSwitchEngine flip → unfreeze target → observation N
min with auto-rollback threshold → rollback reverses traffic, demotes target,
restores source primary, **leaves target as forensic snapshot (no auto-drop)**,
stop-on-write → `NeedsManualIntervention`.

### Already implemented (this branch)
- `ReplicationEngine.setupPostgreSQL` (`replication.go:489`) runs `pg_basebackup
  -h … -U replicator -D … -Fp -Xs -P -R` (physical seed).
- `postgresLag` (`replication.go:550`) measures replication lag for catch-up.
- `Promote` (`replication.go:155`) promotes target.
- **P0-1 demotion guard already fixed**: `rollbackPostgreSQL` (`replication.go:603`)
  explicitly forbids `standby.signal` removal / automatic demotion ("standby.signal
  removal and automatic demotion are prohibited this…") — split-brain guard present.
- `FreezeManager.freezePostgreSQL`/`unfreezePostgreSQL` (`freeze.go:141/173`)
  read-only freeze + unfreeze.
- Fenced auto-cutover drives promote-after-switch via `trafficSwitchStage`
  (`pipeline.go:2041` `runAutoCutover`); manual cutover stops at
  `StateAwaitingCutover` (`pipeline.go:423`).
- `ObservationEngine` auto-rollback with thresholds (`observation.go:95-147`);
  stop-on-write → `NeedsManualIntervention` wired in rollback terminal logic
  (`pipeline.go:892-909`, `force_transition_p0_test.go:49`).

### Remaining gap / Phase 5 net-new
- The `database` *category* (full row dump/restore, `database.go` +
  `database_adapter.go`) uses PG **custom-format file path** (not streaming) —
  correct for PG, but it does **not** seed the live replica used by
  `liveReplicationStage`. PG minimal-downtime in the pipeline today = replica via
  `SetupReplication` + cutover; the `database` category is dump/restore-only.
  **Verify** in the live matrix (Workstream E) that a PG migration with
  `trafficProvider` set actually reaches `StateAwaitingCutover` and commits.
- No code change required for A1 beyond the live-test certification.

## A2. MySQL replication

### Already implemented
- `setupMySQL` (`replication.go:239`), `seedMySQL` (`replication.go:320`) initial
  dump + binlog position, `mysqlLag` (`replication.go:406`) uses
  `Seconds_Behind_Master` (audit's recommended lag meter), `promoteMySQL`
  (`replication.go:431`), `rollbackMySQL` (`replication.go:436`).
- **P0-2 (MySQL Detect `;`→`&&`) already fixed**: `mysqlMigrator.Detect`
  (`database_adapter.go:240`) uses `(pgrep -x mysqld … || pgrep -x mariadbd …)
  && echo yes` — correct OR-gate, no false positive.

### Remaining gap
- Same certification need as A1: confirm MySQL seeded replication reaches cutover
  in the live matrix.

## A3. Redis

### Already implemented
- `setupRedis` (`replication.go:615`) uses `redis-cli REPLICAOF <src> <port>`;
  `promoteRedis`/`promoteRedisCutover` (`replication.go:671/743`) use `REPLICAOF NO ONE`;
  `rollbackRedis` (`replication.go:787`) re-points to source.
- **P0-3 (`|| true` mask) already fixed** in the `database` category:
  `redisMigrator.RestoreCommand` (`database_adapter.go:405-414`) comment: "A failed
  restart must surface as an error — no || true masking." (`SHUTDOWN NOSAVE` +
  restart chain; PONG health check required).
- **P0-4 (password argv→env)** honored by `ShellQuote` on host/port; credentials
  are passed via `-h/-p` (not embedded in `ps`-visible connection strings where
  avoidable) and redacted in API responses (`pipeline_handler.go` redaction).

### Remaining gap
- Redis is **not safe for fenced auto-cutover** — `runAutoCutover` explicitly
  refuses `mongodb` and only supports `postgres, mysql, redis` *with* a
  `DatabaseConfig` and a fenced `TrafficConfig` (`pipeline.go:2041-2040s`). Confirm
  in the live matrix that Redis cutover is manual-fenced or correctly refused.

## A4. MongoDB

### Already implemented
- `mongoMigrator` (`database_adapter.go`) uses `mongodump --archive --gzip` /
  `mongorestore --archive --gzip --drop`, commands built with `ShellQuote` +
  `--uri` (`database_adapter.go` dump/restore command builders). No legacy
  `mongo` shell calls. **P1-7 (mongosh fallback) satisfied** by `--uri` +
  `mongodump/mongorestore` (the modern toolchain; `mongo` shell not invoked).
- `database` category streams Mongo end-to-end (Streaming()==true).

### Remaining gap
- MongoDB is **excluded from fenced auto-cutover** by policy
  (`pipeline.go` engine allow-list). ReplicaSet cutover is deferred (audit Phase 3)
  — acceptable for Phase 5; document as a known limitation in the final report.

---

## Phase 5 net-new work for Workstream A

| Item | Status on this branch | Phase 5 action |
|---|---|---|
| PG physical seed + promote/demote | done (P0-1 guard done) | certify via live matrix |
| MySQL Detect / seeded binlog / lag | done | certify via live matrix |
| Redis `|| true` / Detect | done | certify via live matrix |
| Mongo dump/restore `--uri` | done | certify via live matrix |
| DB container-mode adapter (P1-1) | **NOT present** | implement `ContainerInfo` detection + container-aware `pg_dump`/`mysqldump`/`mongodump`/`redis-cli` exec (see Workstream B note + final report) |
| Cross-engine cutover matrix | not executed | **implement** the live test matrix (Workstream E) — this is the real A-work |

**Honest claim:** the *engine* work for A is largely complete; Phase 5's A-deliverable
is the **live end-to-end certification** (Workstream E) proving each engine reaches
cutover/commit/rollback with no split-brain, plus the **container adapter** (P1-1)
which is the one genuine A-code gap.
