# Phase 4I.Y — Onboarding-to-Pipeline Map (Audit, no code changes)

> **For agentic workers:** READ-ONLY audit. No code was modified. Every claim is
> anchored to a real file/line/handler. Where a live server was required to
> confirm runtime behavior, that is called out explicitly.

**Scope:** the two discovery systems and the boundary between them.

- **Onboarding discovery** — `internal/mod/discovery` + `POST /api/servers/{id}/discover` (job `JobTypeDiscovery` → `DiscoveryJobHandler` → `CollectorRunner.Run` → persist to `discovery_snapshots`). Plus `Service.RunConnectionTest` (host system-info only) persisted to `server_info`.
- **Migration collect** — `internal/mod/migration` category collectors (`categories.go` registry) invoked by `planner.go` at plan time, persisted into `migration_steps.data`.

**Headline answer (detailed in the companion report):** the migration planner does **NOT** read onboarding discovery output. It re-opens an SSH client and re-runs category collectors from scratch. So "discovery again" at Create Plan is a **full re-collection**, not a reuse. Both systems collect overlapping inventory (services, packages, databases, docker) with no shared cache.

---

## 1. File / Route / API / WS Inventory

### A. Server onboarding (FE)
| Concern | File | Calls |
|---|---|---|
| Add server form + submit | `web/src/routes/servers/new/+page.svelte` | `createServer(data)` (POST `/api/servers`) — **no test-connection button here** |
| Server detail: test connection + discovery | `web/src/routes/servers/[id]/+page.svelte` | `discoveryApi` from `$lib/api/discovery` — `/servers/{id}/snapshot` (GET), `/servers/{id}/discover` (POST) |
| Discovery API lib | `web/src/lib/api/discovery.ts` | `discoveryApi.getSnapshot`, `discoveryApi.runDiscovery`, types `ServerSnapshot`, `ServerConnectionInfo` |

### B. Server onboarding (BE)
| Concern | File / Route | Notes |
|---|---|---|
| Create server | `internal/mod/server/handler.go:21-22` `POST /api/servers` | saves credentials (encrypted) |
| Test connection (host info) | `internal/mod/discovery/service.go:218` `Service.RunConnectionTest` | streamed over WS; persists to **`server_info`** via `repo.SaveServerInfo` (service.go:371) |
| Trigger discovery scan | `internal/handler/discovery_handler.go:64` `POST /api/servers/{id}/discover` | submits `JobTypeDiscovery` |
| Get snapshot | `internal/handler/discovery_handler.go:41` `GET /api/servers/{id}/snapshot` | `snapshotStore.LoadSnapshot` |
| Compat check | `internal/handler/discovery_handler.go:94` `GET /api/compat?source=&target=` | loads two snapshots, `CheckCompatibility` |
| Discovery job | `internal/jobengine/handlers.go:51` `DiscoveryJobHandler.Execute` | `runner.Run` → `SaveSnapshot` |
| Collector runner | `internal/mod/discovery/collector_runner.go:55` `Run` | builds full `ServerSnapshot` (services, packages, docker, databases, …) |
| Host-info collector | `internal/mod/discovery/collector.go` | hostname/os/kernel/arch/cpu/ram/disk/net/provider → `SystemInfo` |

### C. Migration creation (FE)
| Concern | File | Calls |
|---|---|---|
| New migration wizard | `web/src/routes/migrations/new/+page.svelte` | builds `PlanRequest` (`sourceServerId`, `targetServerId`, `categories`, `configPaths?`, `databaseConfig?`, `operationId?`) |
| Plan request type | `web/src/lib/api/migrations.ts:42` `PlanRequest` | source/target referenced by **server ID**, not re-entered creds |
| Plan WS | `web/src/lib/api/migrations.ts:157` `wsPlan(req, …)` | `new WebSocket('/ws/plan')`, sends `PlanRequest` JSON |
| Reconcile completion | `web/src/lib/api/migrations.ts:48-75` | `GET /api/migrations?operationId=` → `ReconcileOutcome` |

### D. Migration creation (BE)
| Concern | File | Notes |
|---|---|---|
| ws/plan handler | `internal/mod/migration/pipeline_handler.go` (plan route) | reads `PlanRequest`, calls `planner.Plan` |
| Planner | `internal/mod/migration/planner.go:63` `Plan` | `GetByID` source/target; `getSSHClient`; `CreateMigration`; **parallel `coll.Collect` per category** (planner.go:191) |
| Category registry | `internal/mod/migration/categories.go:48-53` | packages, configs, services, users, docker, database |
| Collect persistence | `internal/mod/migration/planner.go:191-209` | each category Collect result persisted to `migration_steps.data` |

### E. Pipeline (FE)
| Concern | File | Calls |
|---|---|---|
| Pipeline page | `web/src/routes/migrations/[id]/pipeline/+page.svelte` | `GET /api/pipeline/migrations/:id` (session.state authority) |
| WS pipeline | `web/src/lib/api/migrations.ts` `wsExecute` `GET /ws/migrate/:id` | live state stream |
| Actions | ObservationPanel / CutoverChecklist | execute / pause / resume / retry / cancel / cutover / commit / rollback |

### F. Pipeline (BE)
| Concern | File / Route | Notes |
|---|---|---|
| Session load | `internal/mod/migration/pipeline_handler.go` `GET /api/pipeline/migrations/:id` | returns `MigrationSession.State` (string) |
| Execute | `internal/mod/migration/pipeline.go` `Execute` | replays `migration_steps` (already-collected data) — **does NOT re-collect** |
| Step application | `internal/mod/migration/executor.go` | `BuildStepsFromCategories` → apply each category |
| State machine | `internal/mod/migration/state.go` | `IsTerminal`, `CanResume`, `transitionTable` |

---

## 2. End-to-End Journey Map (Add Server → Rollback)

```
[1] Add Server
    FE: /servers/new  -> POST /api/servers  (creds encrypted at rest)
    BE: server.Repo.Insert  -> server_info row NOT created yet
        |
[2] Test Connection  (on /servers/[id], NOT on /new)
    FE: discoveryApi.runDiscovery? NO -> RunConnectionTest (WS stream)
    BE: Service.RunConnectionTest -> SSH connect + host-info commands
        -> SystemInfo {hostname,os,cpu,ram,disk,net,provider}
        -> repo.SaveServerInfo -> server_info (UPSERT, last_checked=CURRENT_TIMESTAMP)
        OUTPUT: host reachable + basic hardware. NO service/package/db inventory.
        |
[3] Run Checks / Discovery  (on /servers/[id])
    FE: discoveryApi.runDiscovery() -> POST /api/servers/{id}/discover
    BE: JobTypeDiscovery -> DiscoveryJobHandler.Execute
        -> CollectorRunner.Run -> FULL ServerSnapshot
           (services via systemctl, packages via distro list,
            docker inspect, databases via pg/mysql/mongo/redis probes,
            users, cron, ssl, monitoring, ...)
        -> snapshotStore.SaveSnapshot -> discovery_snapshots (JSON blob)
        OUTPUT: full inventory. PERSISTED, but in a SEPARATE table.
        |
[4] New Migration Plan  (/migrations/new)
    FE: PlanRequest{sourceServerId, targetServerId, categories, ...}
        -> wsPlan('/ws/plan')
    BE: planner.Plan:
        - GetByID(source/target)  (reads server RECORD, not server_info/snapshot)
        - getSSHClient(source)    (FRESH ssh connection)
        - for each category: coll.Collect(ctx, sshClient)  <-- RE-COLLECTS
        - persisted into migration_steps.data
        OUTPUT: per-category collected data. PERSISTED to migration_steps.
        >>> NO read of server_info or discovery_snapshots <<<
        |
[5] Pipeline Session Load  (/migrations/:id/pipeline)
    FE: GET /api/pipeline/migrations/:id -> session.state
    BE: loads migration_steps (already collected at plan time)
        |
[6] Execute
    BE: pipeline.Execute -> executor applies each category's
        ALREADY-COLLECTED migration_steps.data (no re-collect)
        |
[7] Cutover / [8] Commit / [9] Rollback
    BE: state-machine transitions on MigrationSession
```

---

## 3. Stage Table — Purpose / Input / Output / Persistence

| # | Stage | Purpose | Input | Output | Persisted? | Source of truth |
|---|---|---|---|---|---|---|
| 1 | Add Server | Register server credentials | host/port/user/creds | server record (encrypted creds) | yes (`servers`) | server repo |
| 2 | Test Connection | Verify SSH + capture host profile | decrypted creds | `SystemInfo` (host stats) | yes (`server_info`, UPSERT) | `server_info` |
| 3 | Discovery scan | Full inventory of one server | decrypted creds | `ServerSnapshot` (svc/pkg/db/docker/…) | yes (`discovery_snapshots`) | `discovery_snapshots` |
| 4 | Create Plan | Collect migration-scoped data per category | source/target IDs + categories + dbConfig | per-category `CategoryData` | yes (`migration_steps`) | `migration_steps` |
| 5 | Pipeline load | Resume/observe session | migration ID | `MigrationSession` (state) | yes (migration row) | migration session |
| 6 | Execute | Apply collected data to target | `migration_steps` | applied state per step | yes (`migration_steps.status`) | executor |
| 7 | Cutover | Switch traffic | confirmation | state→traffic_switch/observation | yes | state machine |
| 8 | Commit | Finalize | none | state→committed | yes | state machine |
| 9 | Rollback | Revert | none | state→rolled_back / rollback_degraded | yes | state machine |

---

## 4. Terminology Table (the confusing part)

| Term (UI/BE) | Lives in | When called | Input | Output | Side effect | Authoritative? |
|---|---|---|---|---|---|---|
| **Test Connection** | `discovery.Service.RunConnectionTest` (service.go:218) | `/servers/[id]` detail, manual | creds | host `SystemInfo` | persists `server_info` | host profile only |
| **Discovery / Run Checks** | `DiscoveryJobHandler` + `CollectorRunner.Run` | `/servers/[id]`, manual (POST /discover) | creds | full `ServerSnapshot` | persists `discovery_snapshots` | inventory snapshot |
| **Collect** (migration) | `categories.go` collectors `Collect()` | plan time (ws/plan) | ssh client + category | `CategoryData` | persists `migration_steps` | plan data |
| **Compatibility** | `discovery.CheckCompatibility` (compat.go) | `/api/compat` (source/target snapshots) | 2 snapshots | `CompatibilityReport` | none (computed) | derived |
| **Risk** | migration `risk_assessment` category | plan/pipeline | collected data | risk report | persisted in steps | plan |
| **Health** | pipeline `/health` endpoint | pipeline | live metrics | health score | none | live |
| **Plan** | `planner.Plan` (ws/plan) | `/migrations/new` | PlanRequest | migration + steps | persisted | plan |
| **Dry run** | `wsDryRun` `/ws/dryrun/:id` | pipeline preflight | migration steps | dry-run report | persisted | plan |

**Naming trap:** "discovery" at stage 3 and "collect" at stage 4 both enumerate
services/packages/databases/docker. In the UI they read as the same verb ("scan
the server") but they are two independent code paths writing to two independent
tables, with no shared read.

---

## 5. Reuse vs Re-run Table

| Data | Stage 2 (test) | Stage 3 (discover) | Stage 4 (plan collect) | Stage 6 (execute) |
|---|---|---|---|---|
| SSH connection | fresh | fresh | **fresh** (getSSHClient) | from session |
| Host `SystemInfo` | produced | produced (in snapshot) | **produced again? NO** — planner never reads it | n/a |
| Services list | no | **yes** | **yes** (ServicesCollector) | replay |
| Packages list | no | **yes** | **yes** (PackagesCollector) | replay |
| Databases | no | **yes** (intelligence) | **yes** (DatabaseCollector) | replay |
| Docker | no | **yes** | **yes** (DockerCollector) | replay |
| Reads prior stage output? | — | no | **NO** (ignores server_info + discovery_snapshots) | yes (migration_steps) |

**Conclusion row:** Plan-time collect re-runs the same remote inspections that
onboarding discovery already ran, and does **not** read either persisted result.

---

## 6. Overlap / Ambiguity List

1. **Inventory overlap (services/packages/databases/docker):** collected by both
   `CollectorRunner.Run` (discovery) and `categories.go` collectors (migration).
   Same remote commands (`systemctl list-unit-files`, distro package list,
   `pg_database`/`information_schema`/`mongod`/`redis-cli`). Different tables.
2. **No shared cache:** `planner.Plan` opens a fresh SSH client and calls
   `coll.Collect` — it never calls `GetServerInfo` or `LoadSnapshot`
   (grep: zero matches in planner.go for either).
3. **Duplicate DB detection logic:** `discovery/intelligence_database.go`
   (`AnalyzeDatabaseIntelligence`, pg/mysql/mongo/redis probes) vs
   `migration/database.go` `DatabaseCollector`/`getMigrator`/`ListDatabases`.
   Two implementations of "what DBs run on this host".
4. **No TTL / no invalidation:** `server_info` uses `INSERT … ON CONFLICT …
   last_checked=CURRENT_TIMESTAMP` (repo.go:226) but nothing compares age;
   `discovery_snapshots` has `captured_at` but no expiry job; changing server
   creds/host never invalidates either store. The migration plan likewise has no
   "is my collected data stale?" check.
5. **`Source`/`Target discovery.SystemInfo` field exists but is never populated:**
   `model.go:80-81` declares `Source/Target SystemInfo`, but the planner never
   assigns it (grep: no assignment in planner.go / pipeline_models.go). Dead-ish
   field — suggests an *intended* reuse that was never wired.
6. **UI wording:** "Test Connection" (stage 2) and "Discovery / Run Checks"
   (stage 3) on the server page, then "Create Plan" runs its own silent
   collection with no user-facing label tying it to the prior scans. The user
   sees "scan" twice and reasonably assumes reuse.

---

## 7. What could NOT be confirmed (live-server gap)

- Runtime cost of the double-collection (SSH round-trips) was not measured —
  no live source/target available. The overlap is proven structurally (same
  collectors, no shared read), not by timing.
- Whether any FE code *voluntarily* re-triggers `/discover` before `/migrations/new`
  was not observed in a live session; the wizard sends only `PlanRequest` and
  relies on `ws/plan`. Confirmed by reading `migrations.ts` + the wizard page.
