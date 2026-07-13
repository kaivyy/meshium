# Phase 3 — All-Engine Investigation & Design (Part 1)

> Authoritative investigation of the Meshium database-migration safety baseline
> (Phase 1 / 2A / 2B / 2C / 2D) as it actually exists in code, migrations,
> tests, and caller paths — **not** as prior documents claim it. Companion to
> `phase-3-compatibility-matrix.md`. Every claim cites `file:line`.

## A. Baseline verification (what actually ships today)

### Build / test status (verified this session)
- `go build ./...` → exit 0
- `go vet ./...` → exit 0
- `go test ./internal/...` → 15 packages `ok`, 0 FAIL
- `cd web && npm run check` → 0 errors, 0 warnings
- Integration tests exist but are build-tagged `//go:build integration`
  (`pg_cutover_integration_test.go`, `mysql_cutover_integration_test.go`) and
  require a live Docker pair; they are **not** run by default CI.

### What is LIVE (wired into the running pipeline)
1. **Replication setup is live for all four engines.**
   `liveReplicationStage.Execute` (pipeline.go:1754) detects DBs on the source
   and calls `ReplicationEngine.SetupReplication` (pipeline.go:1773, 1792) per
   engine. It establishes replication and waits for initial catch-up (≤5m,
   lag ≤30s) — then **stops**. It does **not** call `Promote`, `FinalSync`, or
   any cutover. So the live path today = "set up streaming replication + initial
   sync", not automatic cutover.
2. **Fenced automatic cutover is LIVE and OPT-IN** via `AutoCutover=true`
   (default `false`, asserted by `TestAutoCutoverDefaultsOff`).
   `trafficSwitchStage` (pipeline.go:1949) branches to `runAutoCutover`
   (pipeline.go:2029), which builds the `CutoverOrchestrator` (11-step machine,
   cutover_orchestrator.go:195) and drives it.
3. **Fencing is enforced before every mutating step.** `advance()` calls
   `AssertHolds` (cutover_orchestrator.go:254, 269); a missing/stale/conflicting
   lease fails closed. The lease lifetime bounds a cutover via `CutoverTimeout`.

### What is NOT live / gaps (baseline defects to NOT regress, and to close only where safety requires)
1. **`liveReplicationStage` never promotes or cuts over.** It leaves the target
   as a standby and returns. The actual promote/switch is only the opt-in
   `AutoCutover` orchestrator. This is a *scope* gap, not a safety defect.
2. **PG source has NO freeze GUC.** `setupPostgreSQL` (replication.go:453) sets
   `wal_level=replica` / `max_wal_senders` but never sets the source
   read-only / freezes writes during switch→promote. No-dual-writer holds on the
   happy path via the lease, but a writer ignoring the lease could still write
   the source in that window (documented Phase 2A residual).
3. **`FinalSync` for MySQL notes its FTWRL is session-scoped and does not
   actually hold a lock across the sync** (replication.go:200-203) — a
   correctness gap for point-in-time consistency. Documented inline.
4. **Redis has NO source freeze** (replication.go:548 `setupRedis`, Phase 2C
   report). Dual-writer window bounded only by async repl lag.
5. **MongoDB has NO cutover contract** — `runAutoCutover` fails closed at line
   2052; `setupMongoDB`/`promoteMongoDB`/`rollbackMongoDB` all refuse (Phase 2C
   safety). No replica-set lag measurement exists.

### Dead-code / mock-only / not-wired inventory
- `SyncEngine` (`sync.go`) is the rsync-backed file-transfer engine. It is
  **NOT** driven by the live pipeline (`initialSyncStage` replays collected
  category data via appliers; pipeline.go:1353 NOTE). `BandwidthLimit` /
  `ParallelTransfers` are wired into `SyncConfig` (Phase 2D-7) but inert on the
  active path — surfaced by a runtime warning (honesty guard). NOT a safety
  defect; a scope note.
- `ReplicationEngine` is fully wired (see above). Not dead.
- All 20 state-machine states exist; `ForceTransition` is restricted (Phase 1
  P0-2 — only to `StateFailed`/`StateInterrupted` on recovery, never to
  `Committed`/`RolledBack`). `recovery_guidance.go` marks fail-closed states
  `safeToResume:false` and is unit-tested.
- Restart reconciliation: `Pipeline.RecoverInterrupted` + `GracefulDrain` +
  `pipelineRegistry` exist; checkpoints persist per stage. Verified by unit
  tests (Phase 1 P0-3, Phase 2D-7 resource locks).

## B. Cross-engine inventory (current capability)

| Capability | PostgreSQL | MySQL | Redis | MongoDB |
|---|---|---|---|---|
| Adapter | `postgresMigrator` adapter.go:177 | `mysqlMigrator` adapter.go:233 | `redisMigrator` adapter.go:362 | `mongoMigrator` adapter.go:295 |
| Detect | pgrep postgres | pgrep mysqld/mariadbd | pgrep redis-server | pgrep mongod |
| List DBs | `pg_database_size` | information_schema | DBSIZE | listDatabases cmd |
| Dump/seed | `pg_basebackup` (file, not streamed) | `mysqldump` streamed | `redis-cli --rdb` (file+restart) | `mongodump --archive` streamed |
| Restore | `pg_basebackup` onto target | `mysql` streamed | replace dump.rdb+restart | `mongorestore --drop` streamed |
| Repl setup | **physical** slot + basebackup (replication.go:453) | seeded `CHANGE REPLICATION SOURCE TO` (replication.go:239, seeds first) | `REPLICAOF` (replication.go:548) | refused (fails closed) |
| Lag poll | `pg_stat_replication` (replication.go:514) | `Seconds_Behind_Source` (replication.go:401) | `master_repl_offset` diff (replication.go:578) | refused |
| Promote | `pg_promote()` (replication.go:531) | stop replica + writable (replication.go:419) | `REPLICAOF NO ONE` (replication.go:676) | refused |
| Source freeze | **none** (gap) | `read_only=ON` fenced (replication.go:419) | **none** (gap) | n/a |
| Fencing on mutation | via orchestrator `AssertHolds` (opt-in) | same | same | n/a |
| Checkpoint/restart | orchestrator `cutoverMachine` | same | same | n/a |
| Secret redaction | `ShellQuote`+`sqlEscapeSingleQuotes`+`REDISCLI_AUTH` | same | `REDISCLI_AUTH` (no `-a`) | mongosh creds |
| Integration test | `pg_cutover_integration_test.go` | `mysql_cutover_integration_test.go` | **unit only** (redis_cutover_test.go) | none |
| Unit tests | pg_cutover_test.go | mysql_cutover_test.go | redis_cutover_test.go | **none for cutover** |

## C. Cross-engine topology inventory (detected / supported)

| Topology | PG | MySQL | Redis | Mongo |
|---|---|---|---|---|
| host→host | yes | yes | yes | yes |
| Docker container pair | possible (SSH into container) | possible | possible | possible |
| Docker Compose pair | possible | possible | possible | possible |
| same-major | supported (PG physical, MySQL) | supported | supported | **same-major only** (deferred) |
| cross-major | **blocked** (no proof) | **blocked** | **blocked** | **blocked** |
| source/target direct conn | required (replicator) | required | required | required |
| SSH/bastion | via pool/hosts | same | same | same |
| version checks | none on PG | `log_bin`, `server_id`, read_only (replication.go:239) | none | none |
| traffic provider dep | nginx/haproxy only | nginx/haproxy only | nginx/haproxy only | n/a (blocked) |
| fencing authority dep | required for auto | required for auto | required for auto | n/a |

## D. Traffic-provider inventory (current)

| Provider | Status | Idempotent | Ownership proof | Notes |
|---|---|---|---|---|
| nginx | automatic-supported | yes (IdempotencyKey) | **read-after-write** GET VerifyURL (nginx_switch.go:193) | `haproxy -c` test + reload + verify |
| haproxy | automatic-supported | yes | read-after-write verify | mirrors nginx (haproxy_switch.go) |
| traefik | **blocked for auto** | n/a | n/a | no fenced switcher; fails closed (pipeline.go:2225) |
| cloudflare | **blocked for auto** | n/a | n/a | DNS API, not wired to orchestrator |
| caddy | **blocked for auto** | n/a | n/a | no fenced switcher |
| docker | **blocked for auto** | n/a | n/a | no fenced switcher |
| dns | **blocked for auto** | n/a | n/a | no fenced switcher |

Only nginx + haproxy dispatch in `newTrafficSwitcher` (pipeline.go:2218).
Ownership verification is real (`NginxSwitcher.verify`, nginx_switch.go:193)
and a switch success without it does not count as cutover success.

## E. State-machine inventory (current)

20 states; legal transitions validated by `StateMachine` (state.go). The
orchestrator adds a sub-machine (`cutoverMachine`):
`Preflight→Seed→Replicating→Verifying→AwaitingCutover→FencingSource→
CatchingUp→VerifyingTarget→Switching→Promoting→Observing→Completed`.

Fail-closed: `Failed`, `RollbackDegraded`, `NeedsManualIntervention`.
Per state (documented in `recovery_guidance.go`):
- **AwaitingCutover** — entry: replication caught up, traffic not yet switched.
  Mutation: none (waits for operator commit). Fence: not required to *wait*.
  Restart: persists; resumes at same state. Retry: operator-confirmed switch
  only. Rollback: allowed (abandon). Forbidden rollback: none.
- **NeedsManualIntervention** — any ambiguous gate (topology/fence/traffic).
  Terminal-ish: operator resolves + retry or rollback. Never auto-forwarded.
- **RollbackDegraded** — any step rollback warned. Honest terminal.

All transitions persist state + checkpoint before advance (Phase 1 P0-3).
`ForceTransition` restricted (Phase 1 P0-2).

## F. Compatibility matrix → see `phase-3-compatibility-matrix.md`

## G. Phase 3 sequencing decision (evidence-based)

The directive's default order is **already validated by the codebase**:

1. **PostgreSQL** — physical replication ALREADY implemented and integration-
   tested (`pg_cutover_integration_test.go`). Logical replication is **NOT
   justified** by current evidence: no use case is proven that physical
   same-major replication fails to cover, and the codebase/testbed already
   proves physical safety. **Decision: Phase 3A = qualify/cement PG physical
   cutover with full acceptance (restart + failure injection), do NOT add
   logical replication.** This is the smallest safe sub-phase and respects
   "don't implement merely to increase feature count".
2. **MySQL** — seeded replication ALREADY implemented (Phase 2C seeds before
   `CHANGE REPLICATION SOURCE TO`) and integration-tested. **Decision: Phase 3B
   = certify MySQL seeded cutover** with the fencing/traffic/observation
   contract + failure injection. Verify the seed-before-repl invariant holds
   under restart.
3. **Redis** — replication primitives + cutover exist (unit-tested, NO
   integration test). **Decision: Phase 3C = qualify Redis** with a real local
   Redis pair; if source-write-control/RPO cannot be proven, gate to manual/
   degraded honestly (do not claim automatic).
4. **MongoDB** — NO cutover contract; fails closed. **Decision: Phase 3D =
   implement a dedicated same-major replica-set path** behind full preflight
   (exactly one writable primary, FCV, oplog window) before any automatic
   support; otherwise block/defer honestly.
5. **Phase 3E** — all-engine certification matrix + one bounded expansion
   increment + runbooks + final report.

### Blockers found during investigation (none block start; safety gaps are
pre-existing and documented)
- B1: PG source-freeze GUC missing → close only in 3A if a test proves it safe.
- B2: MySQL `FinalSync` FTWRL is session-scoped (no held lock) → verify in 3B.
- B3: Redis has no source freeze, no integration test → 3C must prove or gate.
- B4: MongoDB replica-set lag/role measurement absent → 3D must build it.

### Baseline regressions to guard (must stay green throughout Phase 3)
All Phase 1–2D invariants in the user's immutable list. Concretely re-asserted
each slice by keeping `AutoCutoverDefault=false`, `AssertHolds` before every
mutation, fail-closed on ambiguous topology/fence/traffic, no
`ForceTransition(Committed)`, honest product wording, central secret redaction,
idempotency keys, and the diagnostic/recovery APIs.
