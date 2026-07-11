# Live verification — database migration

How to verify the `database` category end-to-end with real data, per engine,
and what to check. This is the pre-release verification checklist.

## Prerequisites

Meshium migrates the source server's DB to the target via **SSH** on both
hosts. Both source and target must be reachable as SSH-managed servers in
Meshium. A plain Docker container has no SSH — see **DB inside a container**
below for the container case.

You need a DB engine installed on each host (or in a reachable container). The
meshium user on each host needs shell access to the DB client CLI
(`pg_dump`/`pg_restore`, `mysqldump`/`mysql`, `mongodump`/`mongorestore`,
`redis-cli`) and credentials to connect.

## Setup per engine (two bare hosts with SSH)

Create matching engine installs on source + target (target starts empty):

| Engine | Source seed | Verify target |
|--------|------------|---------------|
| PostgreSQL | `pgbench -i -s 100` (~1.5 GB) | `SELECT count(*) FROM pgbench_accounts;` |
| MySQL | `sysbench oltp_read_write prepare` or sample dump | `SELECT count(*) FROM <table>;` |
| MongoDB | insert N docs | `db.<col>.countDocuments()` |
| Redis | `SET key:1..N val` (loop) | `DBSIZE` |

## Migration flow (UI)

1. **New Migration** → pick source + target → select the **Databases** category.
2. Fill the database config block:
   - Engine: `postgres` | `mysql` | `mongodb` | `redis`
   - Host: as seen **from the source host** (usually `localhost` or `127.0.0.1`)
   - Port: default per engine (overridable)
   - Username + Password (Redis: password only; username ignored)
   - Database name: leave empty to migrate **all** user databases
3. **Create Plan** → the collect step lists the detected DB(s) + size.
4. Run the pipeline → watch `initial_sync:database`.
5. After success, run the count check on the **target** — it must equal the source.

## Transfer path per engine

- **MySQL / MongoDB** — streamed pipe (`dump | restore`) over SSH. No temp file
  on meshium; not buffered (a multi-GB dump won't OOM).
- **PostgreSQL** — `pg_dump -Fc` to a file on the source, SFTP to meshium,
  SFTP to target, `pg_restore` from the file. (Custom-format restore can't read
  from stdin.)
- **Redis** — `redis-cli --rdb` to a file on source, SFTP to target, replace
  `dump.rdb` in the Redis data dir, restart Redis so it loads the RDB.

## What to verify

### Row count parity
Source and target counts must match exactly. Note the source count **before**
running the migration.

### Rollback
Trigger rollback from the UI. The migrated DB(s) must be gone from the target
(Postgres `DROP DATABASE`, MySQL `DROP DATABASE`, Mongo `dropDatabase`, Redis
`FLUSHALL`). Pre-existing DBs on the target are preserved.

### Retry safety
Kill the restore mid-flight (e.g. stop the target DB service) and re-run.
Idempotent restore flags make the retry clean:
- Postgres `--clean --if-exists`
- MySQL `--add-drop-database`
- Mongo `--drop`
- Redis full RDB replace (inherently idempotent)

The `StepStatusApplied` skip-guard also prevents re-applying a completed DB.

### Credential security
- `sqlite3 meshium.db "SELECT config FROM migrations ORDER BY id DESC LIMIT 1;"`
  → the DB password must be an encrypted blob, **not** plaintext.
- `GET /api/migrations/{id}/config` → password shows redacted (`"set"`), not the value.
- `journalctl -u meshium.service` → no password strings anywhere.

## DB inside a Docker container

When the DB runs inside a Docker container on the source/target host (not
directly on the host), the current adapter's `pgrep -x postgres` detection and
`pg_dump`/`pg_restore` invocation run **on the host SSH shell** — they will not
see a process or CLI living inside a container.

Two workarounds today (no code change needed):

1. **Install the DB client CLI on the host** (e.g. `apt install postgresql-client`)
   and point the host/port at the container's published port. Detection via
   `pgrep` still won't match (the server is in the container), so you must
   select the engine and provide creds explicitly — detection failure is a
   no-op, not an error, and the named-DB path still dumps/restores via the host
   client against the published port. This is the simplest path and covers the
   common "DB in a container with a published port" case.

2. **Exec into the container** for both dump and restore. Currently the adapter
   builds plain `pg_dump …` commands, not `docker exec <c> pg_dump …`. To
   support DBs that expose no port or whose client you don't want on the host,
   the adapter needs a container-aware mode (planned, see **Container mode**
   below). Until then, option 1 (host client + published port) is the path.

### Container mode (planned)
A `container` field on `DatabaseConfig` (e.g. `{"container":"pg-src"}`).
When set, the adapter wraps every command in `docker exec -i <container> …`
(source) / `docker exec -i <container> …` (target), and `Detect` checks
`docker ps --filter name=<container>` instead of `pgrep`. Streaming still
works: `docker exec -i <c> pg_dump` pipes to `docker exec -i <c> pg_restore`.
This keeps the same adapter surface — only the command prefix changes.

## Replica / zero-downtime (Phase 2)

Dump/restore (Phase 1) implies **downtime = transfer time**. For zero
downtime, Phase 2 wires live replication so writes during the transfer are
captured and replayed, then a short cutover flips traffic. The infra already
exists (`ReplicationEngine`, `CutoverEngine`, `FreezeManager`) but is not yet
connected to the pipeline's `liveReplicationStage` / `trafficSwitchStage`.

Per-engine replication approach:

| Engine | Method | Seed | Catch-up | Cutover |
|--------|--------|------|----------|---------|
| PostgreSQL | Logical replication (publication/subscription) or `pg_basebackup` physical replica | `pg_basebackup` (already in `setupPostgreSQL`) | `pg_stat_subscription` lag → 0 | promote target, repoint app |
| MySQL | Binlog streaming replica | dump+`--master-data` (seed to add) | `Seconds_Behind_Master` → 0 | promote target, repoint app |
| MongoDB | Replica set (target joins source's set, or separate set + initial sync) | initial sync | optime lag → 0 | stepdown source / reconfigure |
| Redis | `REPLICAOF source port` | full resync | `master_link_status:up`, offset equal | promote target (`REPLICAOF NO ONE`), repoint app |

The Phase 1 dump/restore is reused for the **seed** (the initial bulk copy),
then replication catches up the delta. Cutover = freeze writes briefly, wait
for lag → 0, switch traffic, unfreeze. The `liveReplicationStage` already runs
in the pipeline; Phase 2 adds the seed call and the per-engine replication
setup, then the `trafficSwitchStage` becomes automatic instead of
`manual_required`.

See `docs/database-migration.md` § "Phase 2" for the design boundary.
