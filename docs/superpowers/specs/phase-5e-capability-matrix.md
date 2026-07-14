# Phase 5E — Capability Matrix (honest)

> Source of truth: the canonical migration engine in `internal/mod/migration`.
> Every row below is verified against real code, not aspiration. Where a
> capability is NOT yet real, the matrix says so explicitly — no silent skip,
> no fake-success, no capability claimed before it ships.

## A. Transfer / resume capability by category

| Category | Collector/Applier | Transfer mechanism | Resumable after interruption? | Notes |
|---|---|---|---|---|
| configs | ConfigsCollector/Applier | SFTP small / SCP large (resumable) | Yes (file path, `reconcile`) | After reconcile: `RESUME` if source unchanged, `FRESH_START` on size change, `MANUAL_INTERVENTION` on partial invalid |
| docker | DockerCollector/Applier | Volume/diff transfer (resumable path) | Yes (file/volume path) | Large volumes use resumable transfer |
| database (file-path engines) | DatabaseCollector/Applier | Local dump → SCP upload to target | Yes for **upload leg** (source dump unchanged) | PostgreSQL, Redis. Download leg re-runs if local dump unavailable (D3) |
| database (streaming engines) | DatabaseCollector/Applier | Pipe stream (source stdout → target stdin) | **No — restarts from beginning** | MySQL, MongoDB (D5). `resume_not_supported` emitted, never a resumable badge |
| packages | PackagesCollector/Applier | Package-manager install | Metadata only | Not a data transfer; no resumable artifact |
| services | ServicesCollector/Applier | systemd enable/start | Metadata only | Depends on packages + configs applied first |
| users | UsersCollector/Applier | Apply users/groups/cron/firewall | Metadata only | Destructive ops require explicit confirmation |

## B. Database engine capability (explicit, per 5E-D5/E)

| Engine | Collection | Snapshot copy (dump+restore) | Live replication | Resume after interruption | Downtime class (today) |
|---|---|---|---|---|---|
| postgres | Detected (`pgrep`/catalog) | ✅ real (`pg_dump -Fc` → SCP → `pg_restore`) | ❌ not wired (Phase 2) | ✅ upload leg | offline_copy |
| mysql | Detected | ✅ real (stream `mysqldump` → `mysql`) | ❌ not wired | ❌ restart | offline_copy |
| mongodb | Detected | ✅ real (stream `mongodump` → `mongorestore`) | ❌ not wired | ❌ restart | offline_copy |
| redis | Detected | ✅ real (RDB snapshot → replace + restart) | ❌ not wired | ✅ upload leg | offline_copy |

`DatabaseResumable(engine)` (category_meta.go) returns true for postgres/redis,
false for mysql/mongodb/others — this is the single source the FE renders.

## C. Execution location (5E-E added this phase)

| execMode | Where the engine command runs | Wiring |
|---|---|---|
| host | `localhost` on the source server | default; `resolveExecMode("")` → host |
| container | `docker exec -i <name> --` the engine | `dbContainer` feeds `Container` |
| compose | `docker compose exec <service>` | `dbComposeService`/`dbComposeFile` |

All three drive real engine commands via `execPrefix` in `applyFile`
(database.go) — verified in commit `7db7c8a`.

## D. Downtime honesty (5E-G)

- `DowntimeClassFor("database", false)` and `("docker", false)` =
  `offline_copy`. They cap at offline_copy while `zeroDowntimeCapable=false`.
- `minimal_downtime` is NOT claimed for database/docker by default (no default
  cutover stage). It would require seeded replica + fenced cutover, which is
  Phase 2.
- `zero_downtime` is unreachable for dump+restore. It is only emitted if a real
  live-replication + freeze/fencing + verifiable-lag + traffic-switch + observed
  rollback chain is wired and verified end-to-end (`zeroDowntimeCapable=true`
  gate). That gate is currently false everywhere.
- The risk-engine `~0 seconds (zero-downtime)` text is gated on operator-
  asserted `ReplicationAvailable`, which the snapshot_copy DB path never sets
  (it downgrades `live_replication` → `snapshot_copy`). So a dump+restore
  migration cannot reach that branch through the canonical DB path.

## E. Resume-state tokens emitted (5E-D2)

fresh_transfer · resuming_upload · restarting_download ·
resume_refused_source_changed · resume_refused_partial_invalid ·
resume_not_supported · manual_intervention_required ·
verification_in_progress · verified_complete

Each is a distinct, non-empty token so the FE renders explicit state instead of
collapsing every transfer into "running". A refused resume is emitted as an
error event (not a blind restart).

## F. What is explicitly NOT real (do not claim)

- No live replication cutover for any engine (Phase 2 deferred).
- Streaming engines (MySQL/Mongo) do not resume — they restart.
- No zero-downtime copy.
- Download leg (dump from source) is not independently resumable when the local
  dump is lost (it re-runs).
- SFTP-based large transfers are bounded by the SFTP client ceiling
  (documented, not silently exceeded).
