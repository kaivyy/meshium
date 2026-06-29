# Changelog

All notable changes to Meshium are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.3.0] — 2026-06-29

### Security Hardening

#### Authentication & Authorization
- **Session-based auth middleware** — Bearer token authentication for all API routes with WebSocket token support via query parameter
- **Reload loop fix** — Middleware now allows API access when app is unlocked without requiring a session token, preventing infinite redirect loops between `/` and `/login`
- **Frontend 401 handling** — API client no longer hard-redirects to `/login` on 401; only clears token on 403 LOCKED and lets the layout handle redirects

#### Frontend UX Improvements
- **Login redirect race fix** — Layout now waits for `checkStatus()` to complete before performing auth-based redirects
- **Migration list error states** — Load and delete errors are now displayed to the user instead of being silently swallowed
- **Migration detail error handling** — Distinguishes 404 ("not found") from other API errors, showing the actual error message
- **Delete button HTML fix** — Moved delete button outside `<a>` tag for valid HTML
- **Delete loading state** — Per-row delete spinner with disabled button
- **Server list loading/error** — New migration page shows loading spinner and error state for server list fetch
- **Confirmation dialogs** — Added confirmation prompts for migration execute and rollback actions
- **SPA navigation** — Replaced `window.location.href` with `goto()` for proper SPA navigation
- **WebSocket JSON safety** — All WebSocket `JSON.parse` calls wrapped in try/catch to prevent crashes on malformed messages

### Bug Fixes
- **Migration model** — `Categories` changed to `[]string`, `Plan` to `*MigrationPlan`, added `RolledBackAt` field
- **DB schema** — Added `rolled_back_at` column; `SetMigrationRolledBackAt` no longer overwrites `completed_at`
- **Step status** — `CreateStep` now uses `pending` status instead of `completed`
- **Executor** — Checks step data presence instead of status when selecting steps to apply
- **Rollback validation** — Only allows rollback from `completed` or `failed` states
- **Frontend types** — Corrected `sourceId`/`targetId` field names to match backend JSON tags

### Infrastructure
- **Docker deployment** — Multi-stage Dockerfile (node → go → alpine) with docker-compose.yml, non-root user, health check
- **Structured logging** — JSON logger with configurable log level (`internal/shared/logger.go`)
- **Updated dependencies** — postcss 8.5.16, @sveltejs/kit 2.68.0, svelte 5.56.4, cookie override for CVE fixes

---

## [1.5.1] — 2026-06-28

### Bug Fixes
- **Atomic salt storage** — `pbkdf2_salt` is now stored in the same DB transaction as `master_password_hash`, `ssh_key_private_encrypted`, and `ssh_key_public`, preventing inconsistent unlock states.
- **Fail-fast Unlock()** — `Unlock()` now returns a clear error when `pbkdf2_salt` is missing or empty instead of silently deriving a key from an empty salt.
- **SSH helper decrypt errors** — `buildSSHConfig()` and `resolveBastion()` now return decrypt errors instead of ignoring them, and empty credential fields are skipped.
- **Snapshot null checks** — Discovery, monitoring, and dashboard pages now treat both `null` and `undefined` snapshots as missing, preventing crashes on `snap.capturedAt` after 404 fetches.

### Frontend
- **Shared snapshot cache** — Added `/web/src/lib/stores/snapshots.ts` so discovery, monitoring, docker, files, alerts, dashboard, and server detail pages share snapshot data and avoid redundant 404 retries.
- **403 LOCKED redirect** — The API client now clears the session token and redirects to `/login` on `403` responses with `code: "LOCKED"`.
- **Favicon** — Added `favicon.svg` and a `<link rel="icon">` in `app.html`.
- **Svelte 5 compatibility** — Fixed `{@const}` placement so it is an immediate child of `{#each}` blocks.
- **Card class handling** — Wrapped `Card` usage in `<div class="...">` instead of passing `class` as a prop.

### Phase 3 Completion
- **Alerts page** — Aggregates alerts from snapshots, jobs, and migrations.
- **Terminal page** — SSH connection test UI with WebSocket real-time output.
- **Files page** — Disk usage explorer with partition aggregation and filtering.
- **Dashboard page** — Real activity feed showing recent jobs and migrations.

---

## [1.5.0] — 2026-06-28

### Job Engine (Phase 6)

#### Added — Domain Types
- **JobType** enum — Migration, Discovery, CompatCheck
- **JobStatus** enum — Queued, Running, Paused, Done, Failed, Cancelled
  - `IsTerminal()` — true for Done, Failed, Cancelled
  - `IsActive()` — true for Queued, Running, Paused
- **LogLevel** enum — Info, Warn, Error
- **Job** struct — ID, Type, Status, CreatedAt, StartedAt, FinishedAt, PlanID, MigrationID, Progress, Logs, Error
- **JobProgress** — CurrentStep, TotalSteps, CurrentName, Percentage, BytesDone, BytesTotal, SpeedBPS, ETA
- **JobLog** — Timestamp, Level, Step, Message
- **JobRequest** — Type, PlanID, SourceID, TargetID, MigrationID
- **JobFilter** — Type, Status, Limit

#### Added — Job Queue
- **JobQueue interface** — Enqueue, Dequeue, Peek, Size, Remove
- **SQLiteJobQueue** — SQLite-backed FIFO queue
  - `job_queue` table with id, job (JSON), position, enqueued_at
  - Sequential position column for FIFO ordering
  - Index on position for efficient dequeue
  - Transaction support for atomic operations
- **InMemoryJobQueue** — In-memory implementation for testing

#### Added — Job Store
- **JobStore interface** — SaveJob, LoadJob, ListJobs, AppendLog, GetLogs, UpdateProgress
- **SQLiteJobStore** — SQLite-backed job + log persistence
  - `jobs` table with id, type, status, plan_id, migration_id, error, timestamps
  - `job_logs` table with FK to jobs, index on job_id + timestamp
  - Indexes on status, type, created_at
  - UPSERT support (ON CONFLICT DO UPDATE)
- **NoopJobStore** — No-op implementation for testing

#### Added — Progress Broadcaster
- **ProgressBroadcaster interface** — Subscribe, Unsubscribe, Broadcast, Cleanup
- **DefaultProgressBroadcaster** — Channel-based pub/sub
  - Per-job subscriber maps with RWMutex
  - Non-blocking broadcast: `select { case ch <- progress: default: }` — drops if channel full
  - 16-entry buffer per subscriber
  - Auto-cleanup: closes all channels and removes subscribers

#### Added — Job Handlers
- **JobHandler interface** — `Execute(ctx, job, onProgress, onLog)`
- **MigrationJobHandler** — Loads Phase 5 plan → builds steps via bridge → runs Phase 2 Engine
  - Reports progress from StepCallback
  - Reports logs at Info/Warn/Error levels
- **DiscoveryJobHandler** — SSH to server → runs Phase 4 CollectorRunner → saves snapshot
- **CompatCheckJobHandler** — Loads two snapshots → runs Phase 4 CheckCompatibility
  - Reports blockers and warnings as logs

#### Added — Engine
- **Engine** struct — queue, store, broadcaster, shutdown, handlerFactory, maxWorkers
- **HandlerFactory interface** — Creates handlers by job type (dependency injection)
- **EngineConfig** — Queue, Store, Broadcaster, HandlerFactory, MaxWorkers (default 1)
- **Start(ctx)** — Recovers interrupted jobs → launches worker goroutines
- **Stop(ctx)** — Closes stop channel → ShutdownManager waits up to 30s → workers exit
- **Submit(ctx, req)** — Creates Job → SaveJob → Enqueue → returns job
- **Cancel(ctx, jobID)** — Removes from queue (if queued) or cancels context (if running)
- **Pause(ctx, jobID)** — Cancels context, sets status to Paused
- **Resume(ctx, jobID)** — Re-enqueues a paused job
- **GetJob**, **ListJobs**, **GetLogs**, **SubscribeProgress**, **UnsubscribeProgress**
- **Worker loop** — Continuously dequeue → executeJob → handler.Execute → update status
  - 5-second dequeue timeout, 500ms idle sleep when queue empty
- **recoverJobs()** — Running → Paused (manual resume), Queued → stays queued (auto-processed)

#### Added — Graceful Shutdown
- **ShutdownManager** — Tracks running jobs with cancel functions
  - `Register(jobID, cancelFunc)` / `Unregister(jobID)`
  - `RunningCount()` — number of active jobs
  - `Shutdown(ctx)` — Polls every 500ms until all jobs finish or 30s timeout → force-cancel remaining
- Default timeout: 30 seconds

### Testing
- 31 new tests in `internal/jobengine` — all pass with `-race` detector
  - Submit job: queued/running, not started, all types (migration, discovery, compat_check)
  - Job execution: success, failure
  - Cancel: queued, running
  - Progress broadcast: receive updates, non-blocking
  - Graceful shutdown: with running job, empty
  - Concurrent jobs: sequential execution (maxWorkers=1)
  - Pause/Resume
  - Recovery after restart: crashed job → paused
  - List jobs with filter
  - Job status: IsTerminal, IsActive
  - SQLiteJobQueue: enqueue/dequeue, peek, remove
  - SQLiteJobStore: save/load, append/get logs, update progress
  - ProgressBroadcaster: non-blocking, multiple subscribers, cleanup
  - ShutdownManager: register/unregister, empty shutdown
  - Integration: Planner PlanStore + JobEngine, Discovery SnapshotStore
  - File-based DB

### Changed
- Architecture diagram updated to show Job Engine layer above Migration Engine
- Project structure updated with `internal/jobengine/` directory
- Test badge updated: "190+ passing" → "220+ passing with -race"

---

## [1.4.0] — 2026-06-28

### Migration Planner (Phase 5)

#### Added — Migration Plan Types
- **MigrationPlan** — Concrete migration plan with ordered steps, estimates, risk assessment, and warnings/blockers
  - `ID`, `CreatedAt`, `Source`/`Target` (ServerSummary), `Steps` ([]PlannedStep), `TotalEstimate`, `RiskLevel`, `Warnings`, `Blockers`
  - `HasBlockers()` — quick check for blocking issues
  - `StepCount()` — number of steps in the plan
- **PlannedStep** — Single step in a migration plan
  - `Order`, `Name`, `Type` (StepType), `DependsOn` ([]int), `Estimate` (TransferEstimate), `RiskLevel`, `Reversible`, `Config` (map[string]interface{})
  - Config holds workload-specific data used by the bridge to create MigrationStep instances
- **RiskLevel** — Low, Medium, High, Critical
- **StepType** — DockerVolume, DockerImage, Database, File, Config, Nginx, Service
- **ServerSummary** — Lightweight server summary (hostname, OS, RAM, disk)
- **TransferEstimate** — SizeBytes, DurationMin, DurationMax, Confidence (0.0–1.0)
- **PlanWarning** / **PlanBlocker** — Non-blocking and blocking issues with code + message
- **MigrationPlanSummary** — Lightweight summary for listing plans

#### Added — Planner
- **Planner interface** — `CreatePlan(ctx, source, target *discovery.ServerSnapshot) (*MigrationPlan, error)`
- **DefaultPlanner** — Implements the full planning pipeline:
  1. Run compatibility check (Phase 4 CompatibilityChecker)
  2. Build dependency graph from source snapshot (Phase 4 BuildDependencyGraph)
  3. Topological sort → determine safe migration order
  4. For each node: generate PlannedStep using workload-specific generators
  5. Compute DependsOn from graph edges
  6. Estimate transfer size and duration per step
  7. Assess risk per step and overall
  8. Convert compatibility blockers/warnings to PlanBlocker/PlanWarning
  9. Compute total estimate across all steps
  10. Assess overall risk level
- If blockers are present, CreatePlan still returns the plan — the caller decides whether to proceed
- Dependency cycle detection returns a plan with a Critical blocker

#### Added — Step Generators
- **StepGenerator interface** — `Generate(node, source, target) (*PlannedStep, error)`
- **DockerStepGenerator** — Generates DockerVolume + DockerImage steps for containers
  - Config: containerName, image, volumes, ports, networks, labels, composeProject, composeFile
- **DatabaseStepGenerator** — Generates Database dump/restore steps
  - Config: type, version, port, dataDir, sizeMB, dumpCommand, restoreCommand
- **FileStepGenerator** — Generates File transfer steps for non-Docker directories
  - Config: sourcePath, targetPath, isDirectory
- **NginxStepGenerator** — Generates Nginx config migration steps
  - Config: serverName, configFile, listen, proxyPass
- **ServiceStepGenerator** — Generates systemd service management steps
  - Config: name, type, dependsOn

#### Added — Risk Assessor
- **RiskAssessor interface** — `AssessStep(step, source, target) RiskLevel` and `AssessOverall(plan) RiskLevel`
- **DefaultRiskAssessor** with configurable thresholds:
  - Database migration → High (data loss risk)
  - Running container → High (requires downtime)
  - Large volume (>10GB) → High
  - Large database (>5GB) → High
  - Unknown service dependencies → Medium
  - Port conflict on target → Critical
  - Blockers present → Critical overall
  - 3+ high-risk steps → Critical overall
  - Highest step risk determines overall risk (with compound elevation)

#### Added — Transfer Estimator
- **Estimator interface** — `EstimateStep(step, source) TransferEstimate`
- **DefaultEstimator** with configurable speed assumptions:
  - Network transfer: 100 MB/s (100 * 1024 * 1024 B/s)
  - Database dump: 50 MB/s (50 * 1024 * 1024 B/s)
  - rsync overhead: 1.2x (20% protocol overhead)
- Size estimation per step type:
  - Docker volumes: 500MB per volume
  - Docker images: from snapshot or 500MB default
  - Database: from SizeMB or 1GB default
  - Files: 1GB default
  - Configs: 50MB
  - Nginx: 5MB
  - Services: 0 (no data transfer)
- Confidence scoring (0.0–1.0) based on data availability
- Duration = size / speed, with rsync overhead and 50% pessimism for max

#### Added — Plan Store
- **PlanStore interface** — `SavePlan`, `LoadPlan`, `ListPlans`, `DeletePlan`
- **SQLitePlanStore** — SQLite-backed with WAL mode
  - `migration_plans` table with id, plan JSON, source/target host, step count, risk level, has_blockers, created_at
  - Index on created_at for efficient listing
  - Upsert support (ON CONFLICT DO UPDATE)
- **NoopPlanStore** — No-op implementation for testing
- **MigrationPlanSummary** — Lightweight summary for list operations

#### Added — Plan → Engine Bridge
- **BuildSteps(plan, sourceSSH, targetSSH)** — Converts MigrationPlan to []migration.MigrationStep
  - All returned steps implement the `migration.MigrationStep` interface (Prepare/Apply/Verify/Rollback)
  - **DockerVolumeMigrationStep** — Stop container, transfer volumes via io.Pipe streaming, start on target
  - **DockerImageMigrationStep** — Pull image on target via `docker pull`
  - **DatabaseMigrationStep** — Dump on source, stream via io.Pipe, restore on target
  - **NginxMigrationStep** — Copy config file, verify syntax with `nginx -t`, reload with `nginx -s reload`
  - **ServiceMigrationStep** — Enable and start systemd service via `systemctl enable --now`
  - File/Config steps use `transfer.FileTransferStep` from Phase 3
- Compile-time interface compliance verified via `var _ migration.MigrationStep = (*StepType)(nil)`

### Testing
- 43 new tests in `internal/mod/planner` — all pass with `-race` detector
  - Plan generation: basic plan, step order, step types, blockers, nil inputs, cancelled context, total estimate, dependency cycle, empty snapshot, no Docker, Docker mismatch
  - Risk assessment: database, Docker volume, Nginx, service, overall with blockers, multiple high-risk, low risk, port conflict
  - Estimator: database, Docker image, service, nil source, parseImageSize
  - PlanStore: save/load, load not found, list, delete, delete non-existent, save nil, save empty ID, concurrent save, concurrent read/write, file-based DB
  - Bridge: basic plan, empty plan, nil plan, with blockers
  - Integration: full pipeline (snapshot → plan → store → load → bridge → MigrationStep)
  - JSON roundtrip serialization
  - Concurrent plan creation

### Changed
- Architecture diagram updated to show Migration Planner layer between Discovery Engine and Migration Engine
- Project structure updated with `internal/mod/planner/` directory
- Test badge updated: "150+ passing" → "190+ passing with -race"

---

## [1.3.0] — 2026-06-28

### Discovery Engine (Phase 4)

#### Added — Server Snapshot System
- **ServerSnapshot** — Comprehensive server state snapshot, the output of the discovery engine and input to the migration planner
  - Captures: OS info, hardware resources, Docker state, systemd services, databases, Nginx config, disk usage, open ports
  - Partial snapshots supported — individual collector failures are recorded in `CollectionErrors` without aborting the snapshot
  - `CapturedAt` timestamp for point-in-time tracking
  - All sub-types documented with JSON tags for API serialization

- **8 Parallel Collectors** — Each collector runs independently with its own timeout
  - **OSCollector** — Distro, kernel, architecture, timezone, uptime, hostname, virtualization
  - **HardwareCollector** — CPU model/cores, RAM total/used, disk total/used
  - **DockerCollector** — Docker version, containers (with ports, volumes, networks, labels), images, compose projects
  - **ServiceCollector** — Active systemd services with load state, active state, sub state, type, and dependencies (After/Requires)
  - **DatabaseCollector** — Detects MySQL, PostgreSQL, MongoDB, Redis by checking installed binaries and running processes; collects version, port, data directory, size
  - **NginxCollector** — Nginx version, virtual hosts (server_name, listen, root, proxy_pass), upstreams, SSL certificates (with expiry and days remaining)
  - **DiskCollector** — Per-partition usage (filesystem, mount point, size, used, available, use percentage)
  - **PortCollector** — Open/listening TCP ports with process name, PID, and bind address

- **CollectorRunner** — Runs all 8 collectors in parallel with individual timeouts
  - Each collector runs in its own goroutine
  - Per-collector timeout via `context.WithTimeout`
  - One collector failure does not stop other collectors
  - Results assembled into a `ServerSnapshot` with errors recorded in `CollectionErrors`

#### Added — Dependency Graph Builder
- **DependencyGraph** — Directed graph of service dependencies for migration ordering
  - Node types: `container`, `service`, `database`, `nginx`
  - Edge types with human-readable reasons:
    - Container → Database (port matching)
    - Nginx vhost → Container (proxy_pass/upstream port matching)
    - Service → Service (systemd After/Requires directives)
    - Service → Database (name convention matching, e.g., "mysql.service" → MySQL)
  - `TopologicalSort()` — Kahn's algorithm for dependency-ordered migration sequence
  - Cycle detection returns error if circular dependency exists

#### Added — Compatibility Checker
- **CompatibilityReport** — Validates source vs target server compatibility before migration
  - **Blockers** (prevent migration):
    - RAM: target total RAM < source used RAM
    - Disk: target total disk < source used disk
    - Docker: source has Docker but target doesn't
    - Port conflicts: target port already in use by a different process
  - **Warnings** (non-blocking):
    - RAM: target RAM < source total RAM (may be tight)
    - Disk: target disk < source total disk (may be tight)
    - Docker: target Docker version is older than source
    - OS: different OS family (package names may differ) or different architecture
  - `Compatible` boolean — true if no blockers
  - `HasBlockers()` method for quick check

#### Added — Snapshot Store
- **SnapshotStore interface** — Persistence for server snapshots
  - `SaveSnapshot` — Store a snapshot for a server
  - `LoadSnapshot` — Retrieve the latest snapshot for a server
  - `LoadSnapshotAt` — Retrieve a snapshot at or before a specific time
  - `DeleteSnapshot` — Remove all snapshots for a server
- **SQLiteSnapshotStore** — SQLite-backed implementation
  - `discovery_snapshots` table with server_id, snapshot JSON, captured_at
  - `EnsureTable()` for schema creation
- **NoopSnapshotStore** — No-op implementation for testing

#### Added — Discovery Steps
- **DiscoveryStep** (`internal/mod/discoverystep`) — Implements `migration.MigrationStep` for server discovery
  - Prepare: verifies SSH connection is alive, sets up collectors
  - Apply: runs all collectors in parallel, builds dependency graph, stores snapshot
  - Verify: confirms snapshot is not empty (at least OS info collected)
  - Rollback: no-op (discovery does not modify server state)
  - Accessors: `Snapshot()` and `Graph()` for retrieved data

- **CompatibilityStep** — Implements `migration.MigrationStep` for compatibility checking
  - Prepare: validates snapshot store is configured
  - Apply: loads source and target snapshots, runs `CheckCompatibility()`
  - Verify: fails if any blockers are found
  - Rollback: no-op

#### Added — Transfer Engine (Phase 3)
- **TransferStrategy interface** — Pluggable file transfer strategies
  - **SCPStrategy** — SFTP via SSHExecuter.Upload/Download, suitable for <1GB files
  - **RsyncStrategy** — rsync over SSH, suitable for large files or when resume is required
- **StrategySelector** — Picks best strategy based on file size and resume requirement
  - RsyncThreshold: 1GB (configurable)
  - Prefers rsync for large files or when resume is needed
- **ChecksumVerifier** — SHA256 checksum verification at both source and destination
- **FileTransferStep** — MigrationStep implementation for single file transfers
  - Prepare: checks source exists, gets file size, checks disk space, selects strategy
  - Apply: performs transfer with progress tracking
  - Verify: compares SHA256 checksums
  - Rollback: deletes transferred file
- **DirectoryTransferStep** — MigrationStep for directory transfers
  - Lists all files, transfers with resume support (skips matching files)
  - Verifies all files with checksums
  - Rollback removes all transferred files

#### Added — State Machine Engine (Phase 2)
- **13-State Migration Lifecycle** — Typed state machine with validated transitions
  - States: Created, Planning, Backup, Snapshot, Transferring, Applying, Verifying, Committed, Failed, Rollback, Restored, Interrupted, Resuming
  - `transitionTable` defines valid from→to transitions
  - `StateMachine` with thread-safe `Transition()` and `ForceTransition()`
  - State persistence to database with backward-compatible string mapping
- **Engine** — Orchestrates migration step execution through the state machine
  - Prepare → Apply → Verify for each step with checkpointing after Verify
  - LIFO rollback on failure
  - Context cancellation handling (marks as Interrupted)
  - `Resume()` — Resumes from last verified checkpoint
- **RecoveryManager** — Handles interrupted migrations on application restart
  - `DiscoverInterrupted()` — Finds all interrupted migrations
  - `ResumeMigration()` — Continues from last verified checkpoint
  - `CancelMigration()` — Rolls back all applied steps in LIFO order
  - `AutoRecover()` — Automatically discover and handle all interrupted migrations

#### Added — Job Repository
- **JobRepository interface** — Extends Repo with checkpoint and step-state management
  - Job steps with per-step state tracking (pending, preparing, prepared, applying, applied, verifying, verified, rolling_back, rolled_back, failed)
  - Checkpoints with state (verified, rolled_back)
  - Context-aware variants for all state mutations
  - `GetInterruptedMigrations()` for recovery discovery

### Testing
- 35 new tests in `internal/mod/discovery` — all pass with `-race` detector
  - 8 collector tests (OS, hardware, Docker, services, databases, Nginx, disk, ports)
  - Collector runner tests (parallel execution, partial failure, timeout)
  - Dependency graph tests (node creation, edge building, topological sort)
  - Compatibility checker tests (RAM, disk, Docker, port conflicts, OS)
  - Snapshot store tests (SQLite save/load, noop store)
- All existing tests continue to pass with `-race` across all packages:
  - `internal/mod/ssh` — all pass
  - `internal/mod/migration` — all pass
  - `internal/mod/transfer` — all pass
  - `internal/mod/discovery` — 35 tests pass

### Changed
- `discovery/service.go` — Mock SSH uses longest-prefix-first matching algorithm for command responses
- `discovery/collectors_service.go` — Nginx version parsing uses `strings.LastIndex` to extract version after final slash
- Architecture diagram updated to show discovery engine, transfer engine, and recovery manager
- Project structure updated with all new files

---

## [1.2.0] — 2026-06-28

### Mobile & UX

#### Fixed — Mobile Responsive Layout
- **Bottom Navbar** — Replaced desktop sidebar with a mobile bottom navigation bar on screens < 768px
  - 4-item navbar: Servers, Migrations, Settings, Lock
  - Fixed positioning with `safe-area-inset-bottom` for iPhone notch/home indicator
  - Desktop sidebar hidden on mobile (`hidden md:flex`), bottom navbar hidden on desktop (`md:hidden`)
  - Removed hamburger menu, mobile top bar, and slide animations from layout
  - Added `pb-16` padding on main content to prevent bottom navbar overlap

#### Fixed — SSH Host Key Auto-Accept
- **First-Connection Trust** — New SSH host keys are now auto-accepted on first connection (like `ssh -o StrictHostKeyChecking=accept-new`)
  - Previously, connections to new servers failed with "host key not found — needs verification" before authentication
  - Host keys are now automatically saved to the `known_hosts` table on first connect
  - Existing host keys are still verified for mismatch (MITM protection)
  - `MakeHostKeyCallback` now accepts a `serverID` parameter to associate keys with server records

#### Fixed — API Null Response Bug
- **Empty Arrays Instead of Null** — All list API endpoints now return `[]` instead of `null` when there are no records
  - Affected endpoints: `GET /api/migrations`, `GET /api/servers`, `GET /api/migrations/{id}/steps`, backups
  - Go nil slices were marshaling to JSON `null`, causing the frontend to stay in loading state
  - All repo `List*` functions now use `make([]T, 0)` initialization

### Changed
- `HostKeyStore` interface updated: `MakeHostKeyCallback(serverID int)` instead of `MakeHostKeyCallback()`
- `discovery/service.go` and `migration/ssh_helper.go` pass `serverID` to callback
- All `ListMigrations`, `List`, `GetSteps`, `GetBackups` functions return initialized empty slices

---

## [1.1.0] — 2026-06-28

### Migration Engine Enhancements

#### Added — Docker Migration
- **Docker Category** — Full collector, applier, backup, and rollback for Docker state
  - Collects running containers (with env vars and labels), images, volumes, and compose files from source
  - Pulls images, creates volumes, uploads compose files, and recreates containers on target
  - Falls back to `docker run` for containers without compose files
  - Rollback removes containers, images, and volumes that were added by the migration

#### Added — Dry Run Mode
- **Dry Run** — Preview what a migration would change without applying anything
  - Compares collected source data with the target's current state
  - Reports per-category changes: add, modify, remove with resource names and descriptions
  - Summary with total/add/modify/remove counts
  - REST endpoint: `GET /api/migrations/{id}/dryrun`
  - WebSocket endpoint: `ws://host/ws/dryrun/{id}` with live progress

#### Added — Diff View
- **Server Diff** — Bidirectional comparison between source and target servers
  - Collects from both servers and compares per category
  - Reports: only-in-source, only-in-target, different, same count
  - Supports all 5 categories: packages, configs, services, users, docker
  - REST endpoint: `POST /api/diff`
  - WebSocket endpoint: `ws://host/ws/diff` with live progress

#### Added — SSH Bastion / Jump Host
- **Bastion Support** — Tunnel SSH connections through a bastion/jump host
  - Per-server bastion configuration via `bastionId` field
  - Bastion connection tunneled via `ssh.Dial` through bastion, then `NewClientConn` to target
  - Resolves bastion credentials from encrypted server records
  - Shared helper `getSSHClientForServer()` used by executor, rollback, diff, and dry run

#### Added — Pre-Flight Validation
- **Pre-Flight Checks** — Validate migration readiness before execution
  - SSH connectivity test to target server
  - Target disk space check (`df -h /`) with warning if < 1GB available
  - OS compatibility check (source vs target distro family)
  - Docker availability check if docker category is selected
  - REST endpoint: `GET /api/migrations/{id}/preflight`
  - Returns `PreFlightResult` with errors, warnings, and OK status

#### Added — Config Exclusion List
- **OS-Critical File Protection** — 20 paths excluded from config migration
  - Protected: `/etc/fstab`, `/etc/hostname`, `/etc/machine-id`, `/etc/hosts`, `/etc/shadow`, `/etc/passwd`, `/etc/group`, `/etc/subuid`, `/etc/subgid`, `/etc/resolv.conf`, `/etc/network/`, `/etc/netplan/`, `/etc/sysconfig/network-scripts/`, `/etc/udev/`, `/etc/crypttab`, `/etc/mdadm.conf`, `/etc/dracut.conf`, `/etc/kernel/`, `/etc/grub.d/`, `/etc/default/grub`
  - Enforced in Collect (skip download), Backup (skip backup), and Apply (safety net with warning)

#### Added — Export Migration Plan
- **Export** — Download migration plan as JSON
  - REST endpoint: `GET /api/migrations/{id}/export`
  - Returns migration record and all steps as downloadable JSON attachment

### Infrastructure Enhancements

#### Added — GitHub Actions CI
- **CI Workflow** — Automated testing on push and pull request
  - Go tests with module caching (`go test ./... -count=1`)
  - Frontend build verification (`npm ci && npm run build`)
  - Test results uploaded as artifacts

#### Added — SSH Pool Concurrency
- **MaxConcurrent** — Configurable concurrent connection limit (default: 10)
  - Semaphore-based gating in `Get()` prevents connection storms
  - `CloseIdle()` method prunes connections idle longer than `MaxIdle`
  - Pending map prevents duplicate connections for the same server ID

### Frontend Enhancements

#### Added
- **Dry Run UI** — Purple dry run button on migration detail page, results display with summary and per-category changes
- **Export Button** — Download migration plan as JSON from migration detail page
- **Bastion Selector** — Dropdown on server edit page to choose bastion/jump host from existing servers
- **Server Interface** — Added optional `bastionId` field to Server type

### Changed
- **SSH Helper Refactor** — Shared `getSSHClientForServer()` replaces duplicated SSH connection code in planner, rollback, and diff
- **Migration Runner Interface** — Added `DryRun()`, `Diff()`, and `PreFlight()` methods

---

## [1.0.0] — 2026-06-27

### Initial Release

The first complete release of Meshium — a self-hosted server migration engine for Linux.

---

### Added — Core Platform

- **SSH Engine** — Connection pool with idle/lifetime management, auto-generated Ed25519 keypairs, known-hosts verification, and SFTP support
- **Server Manager** — Full CRUD for server records with tagging, environments, regions, favorites, and search/filter
- **Auth System** — Master password setup, AES-256-GCM credential encryption at rest, session lock/unlock, scrypt key derivation
- **Discovery Module** — Real-time SSH connection testing with WebSocket streaming, system info collection (hostname, OS, kernel, CPU, memory, disk, network)
- **SQLite Database** — Pure-Go SQLite (no CGO) with auto-migrations for all tables

### Added — Migration Engine

- **Distro Adapter** — Automatic detection of source distro (apt, dnf, pacman, apk, zypper) with cross-distro package name mapping
- **Category Registry** — Pluggable architecture for migration categories with collector/applier/backup/rollback interfaces

- **Package Migration** — Collects installed packages from source, translates package names across distros, installs missing packages on target, backs up existing package list for rollback
- **Config File Migration** — SFTP download of `/etc/` and custom paths from source, upload to target, backs up existing configs before overwriting
- **Service Migration** — Collects enabled systemd units from source, enables them on target, backs up current service state
- **User & Security Migration** — Migrates users, groups, cron jobs, and firewall rules, backs up existing state for rollback

- **Planner** — Connects to source server, runs all selected collectors, persists migration plan and steps to database
- **Executor** — Connects to target server, runs backups then applies collected data per category, auto-rollback on any failure
- **Rollback Manager** — Restores target server to pre-migration state by applying backups in reverse order

- **REST API** — Full CRUD for migrations: list, create, get, delete, rollback, get steps
- **WebSocket API** — Live progress streaming for planning (`/ws/plan`), execution (`/ws/migrate/{id}`), and rollback (`/ws/migrate/{id}/rollback`)
- **Composite Runner** — Delegates to planner/executor/rollback manager, wired into main server

### Added — Frontend

- **Sidebar Navigation** — Consistent sidebar across all pages with Servers, Migrations, Settings, and Lock
- **Server List Page** — Searchable, filterable server table with favorites, tags, environment/region display
- **Server Detail Page** — System info dashboard with live SSH connection via WebSocket
- **Server Add/Edit Forms** — Full form with validation for server configuration
- **Migration Wizard** — 4-step wizard: select source → select target → choose categories (with optional config paths) → review & plan with live WebSocket progress
- **Migration Detail Page** — Status badge, category list, step list with per-step status, live WebSocket progress log, execute/rollback/delete buttons
- **Migration History Page** — List of all migrations with status badges, category tags, and delete
- **Auth Pages** — Setup (first-time password), Login (unlock), with automatic routing
- **Settings Page** — System configuration
- **Embedded SPA** — Frontend compiled to static assets and embedded in Go binary via `go:embed`

### Added — Developer Experience

- **Makefile** — `build`, `dev`, `test`, `clean` targets
- **85 Tests** — Full test coverage across all 7 internal packages
- **Design Docs** — PRD, design specs, and 10-part implementation plan in `/docs`
- **Systemd Service** — Ready-to-use systemd unit file for production deployment

### Security

- Master password encrypted with scrypt + AES-256-GCM
- Server credentials encrypted at rest in SQLite
- SSH host key verification (known-hosts)
- Auto-generated Ed25519 SSH keypair
- Session lock/unlock with inactivity protection

### Tech Stack

- **Backend:** Go 1.24, gorilla/websocket, golang.org/x/crypto, github.com/pkg/sftp, modernc.org/sqlite
- **Frontend:** SvelteKit 2, TypeScript, TailwindCSS, lucide-svelte
- **Database:** SQLite (pure Go, no CGO)
- **Build:** Make, Vite, go:embed

---

## Version History Summary

| Version | Date | Highlights |
|---------|------|------------|
| 1.4.0 | 2026-06-28 | Migration planner (step generators, risk assessor, transfer estimator, plan store, plan→engine bridge), 43 new tests |
| 1.3.0 | 2026-06-28 | Discovery engine (8 collectors, dependency graph, compatibility checker), transfer engine (SCP/rsync), state machine engine, crash recovery, 35 new tests |
| 1.2.0 | 2026-06-28 | Mobile bottom navbar, SSH host key auto-accept, API null→[] fix |
| 1.1.0 | 2026-06-28 | Docker migration, dry run, diff view, bastion/jump host, pre-flight validation, config exclusion, CI/CD, SSH pool concurrency |
| 1.0.0 | 2026-06-27 | Initial release — full migration engine, web UI, 85 tests |
