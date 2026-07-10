# Database Migration — Design Document

Status: **Phase 1 (in progress)** — dump/restore for PostgreSQL, MySQL/MariaDB, MongoDB, Redis.
Phase 2 (replication + cutover) is designed-not-blocked, explicitly deferred.

## Problem

Meshium migrates server *metadata* (packages, configs, services, users, docker) but
not actual database data. The pipeline's `initialSyncStage` only replays each
category's `Applier.Apply` over metadata collected at plan time
(`internal/mod/migration/pipeline.go:1217` — the NOTE there admits the SyncEngine is
not wired). `ReplicationEngine` exists (`replication.go`) but `setupMySQL` configures
replication against an **unseeded** target (no dump), and `CutoverEngine` has no live
caller. So migrating a server with a multi-GB / hundred-million-row database today
moves **none of the DB data**.

This feature adds a `database` category that performs a real dump→restore of the row
data, for all four engines, via a pluggable per-engine adapter (Meshium is opensource —
engines register and extend).

## Goals (Phase 1)

- Real data migration: dump source DB → stream/transfer → restore on target.
- Four engines: PostgreSQL, MySQL/MariaDB, MongoDB, Redis.
- Pluggable `DatabaseMigrator` adapter so future engines drop in without touching the
  pipeline.
- Idempotent restore + retry-safe (a half-restored DB retried must not corrupt).
- Encrypted DB credentials at rest; redacted in API responses; never in step data or logs.
- No new disk burden on meshium for streamable engines (MySQL/Mongo pipe end-to-end).
- Downtime = transfer time (no replication in Phase 1 — stated honestly in the UI).

## Non-goals (Phase 2)

- Replication (binlog/WAL/_change streams) and live lag monitoring.
- Near-zero-downtime cutover (freeze → final sync → promote → traffic switch).
- Cross-engine conversion (PG→MySQL etc.) — same engine only.

Phase 2 infra already exists in the repo (`replication.go`, `cutover.go`, `freeze.go`)
but is unwired. Phase 2 wires it and reuses this adapter's dump methods for the replica
seed.

## Transfer mechanism

User chose **streamed pipe (no temp file)**. MySQL and MongoDB stream end-to-end
(`mysqldump | mysql`, `mongodump --archive | mongorestore --archive`). PostgreSQL and
Redis cannot stream cleanly to a restore stdin (PG custom-format `pg_restore` needs a
file; Redis restore = replace `dump.rdb` + restart), so they use the file path (SFTP
download/upload), still with **no meshium-local temp file** for the streamable engines.

### Why an optional streaming interface, not core

`SSHExecuter` (`internal/mod/transport/transport.go:18`) has no streaming exec —
`ExecContext` buffers all stdout in a `bytes.Buffer` (`client.go:360`), so a 5GB dump
OOMs. Adding streaming methods to the **core** interface would force ~9 test mocks
(`mockSSH` across migration/discovery/transport/transfer/jobengine tests) to implement
them — a spreading break.

Instead, define narrow **optional interfaces** and type-assert inside `DatabaseApplier`:

```go
// internal/mod/migration/stream.go
type StreamExecuter interface {
    ExecPipe(ctx context.Context, cmd string) (io.ReadCloser, error) // source stdout
}
type WriteExecuter interface {
    ExecWithStdin(ctx context.Context, cmd string, stdin io.Reader) (string, int, error) // target stdin
}
```

The concrete `*ssh.Client` satisfies both (new methods `ExecPipe`/`ExecWithStdin` in
`client.go`, reusing the `StdoutPipe`+`Start` pattern from `ExecStreamLinesContextWithTimeout`
at `client.go:453`). `Apply` asserts; if a mock doesn't satisfy, it falls back to the file
path — so existing tests stay green. ponytail: drop the fallback once mocks grow streaming.

## DatabaseMigrator adapter

Dispatched by engine string inside the collector/applier — mirrors `ReplicationEngine`'s
`switch config.DatabaseType` (`replication.go:92`). No top-level registry; a
`map[string]DatabaseMigrator` inside `database.go`.

```go
type DatabaseMigrator interface {
    Engine() string
    Detect(ctx, ssh) bool
    ListDatabases(ctx, ssh, DBCredentials) ([]DBCatalogEntry, error)
    StreamDumpCommand(DBCredentials, db) string    // stdout (streamable engines)
    StreamRestoreCommand(DBCredentials, db) string // stdin  (streamable engines)
    DumpCommand(DBCredentials, db, remotePath) string   // file-path engines
    RestoreCommand(DBCredentials, db, remotePath) string
    DropDatabaseCommand(DBCredentials, db) string  // rollback
    Streaming() bool                                // MySQL/Mongo true; PG/Redis false
}
```

| Engine    | Detect              | Dump (stream)                                  | Restore (stream)                              | Idempotent flag        |
|-----------|---------------------|------------------------------------------------|-----------------------------------------------|------------------------|
| PostgreSQL| `pgrep -x postgres` | file: `pg_dump -Fc --clean --if-exists -Z 1 -f <path> <db>` | file: `pg_restore --clean --if-exists -d <db> <path>` | `--clean --if-exists` |
| MySQL     | `pgrep -x mysqld\|mariadbd` | `mysqldump --single-transaction --routines --triggers --add-drop-database <db>` | `mysql <db>` | `--add-drop-database` |
| MongoDB   | `pgrep -x mongod`   | `mongodump --archive --gzip --db <db>`         | `mongorestore --archive --gzip --drop --nsInclude <db>.*` | `--drop`              |
| Redis     | `pgrep -x redis-server` | file: `redis-cli --rdb -` → RDB; restore = stop, replace dump.rdb, restart | (restart path) | full RDB replace |

Shell args via `shared.ShellQuote`; SQL values via `sqlEscapeSingleQuotes`
(`replication.go:39` — reuse).

## Category integration

Follows the **stateful collector** pattern (like `configs` with `Paths`):

- `categories.go:52` — `r.Register("database", &DatabaseCollector{}, &DatabaseApplier{})`.
- `planner.go:132-142` — special-case block: type-assert `mod.Collector.(*DatabaseCollector)`,
  substitute a fresh `&DatabaseCollector{Creds: req.DatabaseConfig, …}`. Credentials used
  in-memory for `ListDatabases`; **only metadata** (`{engine, databases:[{name,sizeMb}]}`)
  persisted to `migration_steps.data` — never creds, never row data.

### Collect (plan time, step 4)
`Detect` → if absent return `CategoryData{Type:"database", Data:[]byte("{}")}` (mirror
`docker.go:63` — absence is not an error). If present, `ListDatabases` → marshal metadata.

### Apply (execute time, `initialSyncStage`)
Branch on `migrator.Streaming()`:
- **stream (MySQL/Mongo):** `source.ExecPipe(StreamDumpCommand)` → `io.Pipe` →
  `target.ExecWithStdin(StreamRestoreCommand)`. No meshium-local file.
- **file (PG/Redis):** dump to remote gzip → SFTP `Download` → SFTP `Upload` → restore.
  Reuses streaming SFTP (`io.Copy`, multi-GB safe, `client.go:549/610`).

The `DatabaseApplier` needs credentials at execute time. They live on `MigrationConfig`
(persisted), not `PlanRequest` (not re-read at execute). `initialSyncStage` type-asserts
`mod.Applier.(*DatabaseApplier)` and injects creds via `SetConfig(pc.DatabaseConfig)` —
keeps the `Applier` interface stable (ponytail: type-assert cost for interface stability).

## Credential handling

Mirror `server.Service` (`server/service.go:41-70`):
- Plan time: plaintext over authenticated WSS → used in-memory → **never** in step data.
- Persisted on `MigrationConfig.DatabaseConfig` with password `shared.Encrypt`-ed
  (`crypto.go`), exactly as server SSH passwords are stored.
- `Pipeline.Execute` (`pipeline.go:182`) decrypts via `p.authSvc.GetAESKey()` into
  `pc.DatabaseConfig`.
- API `GET /config/:id` redacts password (mirror `redactServer`, `service.go:77`) — opaque
  "set" indicator, never the value. Never logged.

## Retry / idempotency

A half-restored DB retried can duplicate/corrupt. Two layers:
1. Idempotent restore flags per engine (table above).
2. New guard in `initialSyncStage` (`pipeline.go:~1256`): `if step.Status == StepStatusApplied { continue }`
   — skip already-applied steps on retry/resume. Protects ALL categories (an existing bug
   today: retry re-applies completed categories). Idempotent flags are defense-in-depth.

## Files

**Create:** `stream.go`, `database_adapter.go`, `database.go`, `database_test.go`.
**Modify:** `categories.go`, `model.go` (PlanRequest), `pipeline_models.go` (MigrationConfig),
`planner.go` (special-case), `pipeline.go` (PipelineContext + Execute decrypt + skip-applied
guard + config inject), `pipeline_handler.go` (encrypt/redact), `ssh/client.go`
(ExecPipe/ExecWithStdin), `web/.../new/+page.svelte` (category + config block),
`web/.../api/migrations.ts` (PlanRequest + DatabaseConfig type).

**No change:** core `SSHExecuter` interface (streaming is optional), `replication.go`/
`cutover.go`/`freeze.go` (Phase 2), ~9 test mocks.

## Risks

1. **5m command timeout** (`DatabaseTimeouts.Command`) kills multi-GB dump/restore. Stream
   path runs under the caller's context deadline, not the 5m cap. File-path engines
   background the dump + poll `kill -0 <pid>` via cheap `ExecContext` (<5m).
2. **SFTP `FileTransfer` 10m ceiling** (`ssh/model.go:45`) for PG/Redis large dumps.
   Documented ceiling; ponytail: streaming/longer-timeout SFTP in Phase 2. Compressed
   dumps over LAN usually <10m.
3. **Cred leak** → encrypted at rest, redacted in API, never in step data/logs.
4. **migration_steps bloat** → DB collect metadata is <1KB (engine + db names + sizes).
   The dump is a transfer, never stored in the DB.

## Verification

1. `go build ./... && go vet ./internal/mod/migration/ && go test ./internal/mod/migration/` — green.
   `database_test.go` asserts command shapes (idempotent flags, system-DB exclusion,
   shell-quoting, streaming/file branch).
2. `cd web && npm run check` — 0 errors.
3. `make build` + restart `meshium.service` — active, HTTP 200.
4. **Live per engine** (Docker pairs):
   - PG: source `pgbench -i -s 100` (~1.5GB), target empty → `SELECT count(*) FROM pgbench_accounts` = 10M. Rollback drops it.
   - MySQL: `sysbench` data → row count matches. Mongo: `mongodump --archive | mongorestore --drop` → count matches. Redis: `--rdb` → `DBSIZE` matches.
5. Retry test: kill restore mid-flight → retry clean (idempotent + skip-guard).
6. Cred test: `migrations.config` shows encrypted blob; `GET /config/:id` redacted; logs clean.
7. No regression: existing 5 categories + plan/recovery still green.
