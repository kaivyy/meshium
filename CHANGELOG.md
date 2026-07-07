# Changelog

All notable changes to Meshium are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.5.0-beta.3] — 2026-07-08

Build-fix release. **v1.5.0-beta.1 and v1.5.0-beta.2 do not compile from a
fresh clone** — two source files under `internal/mod/server/` were silently
excluded by an over-broad `.gitignore` pattern and never committed, so the
`server` package failed to satisfy its `Repo` interface on any machine that
did not already have the files on disk. This release commits the missing files,
anchors the offending ignore patterns, and corrects the build/test docs. No
feature or runtime-behavior changes versus beta.2.

### Fixed — Fresh-clone build blocker

- **`.gitignore` swallowed package source** — the bare `server` binary pattern
  (intended for the compiled root binary) also matched the
  `internal/mod/server/` package directory, so newly added files there were
  ignored. `internal/mod/server/repo_enterprise.go` (which implements
  `CreateConnectionProfile` and 23 other `sqliteRepo` methods) and
  `internal/mod/server/socket_test.go` were never tracked in any commit.
  A fresh clone of beta.1/beta.2 therefore failed with
  `*sqliteRepo does not implement Repo (missing method CreateConnectionProfile)`.
  - `.gitignore` — anchored the binary patterns to the repo root: `server` →
    `/server`, `meshium-server` → `/meshium-server`, and `bin/` → `/bin/`, so
    they no longer match nested package paths.
  - `internal/mod/server/repo_enterprise.go`, `internal/mod/server/socket_test.go`
    — now tracked.

### Fixed — Documentation

- **README build/test instructions corrected for a fresh clone.** The embedded
  frontend output (`cmd/server/web/build/`) is generated, not committed, so a
  bare `go build ./...` / `go test ./...` fails until the frontend is built
  once. README now directs users to `make build` / `make test` and documents
  the raw-Go prerequisite explicitly.

### Note on superseded pre-releases

- **v1.5.0-beta.1 and v1.5.0-beta.2 are superseded by v1.5.0-beta.3** because
  their fresh-clone build was broken. The tags are left in place (not deleted or
  rewritten); use v1.5.0-beta.3 instead.

### Verification

- Verified from a clean checkout of the candidate: `make build` succeeds
  end-to-end, `go build ./...`, `go vet ./...`, `go test ./...`, and
  `go test -race ./...` all pass after the frontend is built; `npm run check`
  and `npm run build` are clean.

---

## [1.5.0-beta.2] — 2026-07-07

Second beta on the v1.5.0 line. Builds on v1.5.0-beta.1 by adding two
connection-breaking backend fixes (WebSocket origin rejection behind a reverse
proxy, and unregistered SSH key-management routes) and a full frontend redesign:
a semantic CSS-variable token system with a light/dark/system theme toggle
across all pages, plus WCAG-oriented tap-target and focus-visible handling.

This is still a **pre-release / beta** — not production-ready or enterprise-grade,
and no such claim is made. See the release notes for the validation checklist.

### Fixed — WebSocket Origin Check Behind Reverse Proxy

- **"Connection failed" on Test Connection / Terminal / Monitoring behind a proxy** — WebSocket upgrades were rejected with `403 Forbidden` whenever Meshium was accessed through a reverse proxy (e.g. Tailscale serve, nginx) on a public hostname. The origin check compared the browser's `Origin` (public host) against the request `Host` (internal `localhost:9527`); the mismatch killed the handshake before any steps streamed, while plain REST calls (which have no origin check) kept working — producing the misleading "everything works except connecting" symptom.
  - `internal/shared/types.go` — `CheckWebSocketOrigin` is now **proxy-aware**: the Origin hostname (port-stripped) is compared against both `r.Host` and any `X-Forwarded-Host` set by the proxy. Genuine cross-origin requests are still rejected. This is safe because browsers cannot set `X-Forwarded-Host` on a WebSocket handshake — only a trusted proxy can.
- **Inconsistent & broken origin checks across handlers** — Consolidated all 7 WebSocket upgraders onto the single shared `shared.CheckWebSocketOrigin`:
  - `internal/handler/terminal_handler.go`, `job_handler.go`, `monitoring_handler.go`, `logview_handler.go` — previously used `CheckOrigin: return true` (no CSRF protection at all).
  - `internal/mod/migration/handler.go` — its check read `r.Header.Get("Host")`, which is **always empty** in Go (the value is promoted to `r.Host`), so the same-origin comparison was broken.
  - `internal/mod/migration/pipeline_handler.go` — used `strings.Contains(origin, host)` with that empty host, i.e. `Contains(origin, "")` which is **always true** — effectively no origin protection.
  - Removed the now-dead `isSameOrigin` helper from the migration package.

### Fixed — Unregistered SSH Key-Management Routes

- **`/api/servers/{id}/test-auth` and 7 sibling routes returned `NOT_FOUND`** — `server.KeyHandler` was never instantiated or registered in `cmd/server/main.go`, so `test-auth`, `install-key`, `verify-key`, `rotate-key`, `clear-key`, `auth-status`, `history`, and `metrics` all fell through to the catch-all handler and returned `404 NOT_FOUND`.
  - `cmd/server/main.go` — construct `serverKeyHandler := server.NewKeyHandler(...)` and call `serverKeyHandler.RegisterRoutes(mux)`.

### Added — Tests

- `internal/shared/crypto_test.go` — added coverage for the proxy scenario in `TestCheckWebSocketOrigin` (cross-origin request allowed when `X-Forwarded-Host` matches; still rejected without it).

### Changed — Frontend Redesign (Light / Dark / System)

- **Semantic CSS-variable token system** — all colors, surfaces, borders, and
  accents are now driven by semantic tokens (`--color-bg`, `--color-surface`,
  `--color-fg`, `--color-accent`, etc.) defined once and themed per mode, rather
  than hard-coded palette values scattered across pages.
  - `web/src/app.css` — light and dark token sets; added `--shadow-card` /
    `--shadow-card-hover`; WCAG tap-target floor (44px on mobile) and global
    `focus-visible` handling.
  - `web/tailwind.config.ts` — exposes the tokens as Tailwind utilities and the
    `shadow-card` / `shadow-card-hover` box-shadow utilities.
- **Light / dark / system theme toggle** — a persisted theme store with a
  three-state toggle (light / dark / follow-system) in the top bar.
  - `web/src/lib/components/ThemeToggle.svelte`, theme store, `TopBar.svelte`,
    `Sidebar.svelte`.
- **Shared UI primitives restyled on the token layer** — `Button`, `Card`,
  `DataTable`, `DropdownMenu` pick up the semantic tokens, card shadows, and
  focus states (`web/src/lib/components/ui/`).
- **All pages swept onto the token system** — cron, docker, drift, files,
  firewall, migrations (list + pipeline), monitoring, plans (list + new),
  processes, server detail, services, ssh (+ history, known-hosts), terminal,
  updates. Existing page layouts were preserved; only the styling layer changed.
- **Server-detail tabs on one line** — the Overview / SSH / Docker / Services /
  Databases / Network / Nginx tab row now scrolls horizontally instead of
  wrapping (`web/src/routes/servers/[id]/+page.svelte`).
- **Terminal fits the mobile viewport** — fixed horizontal overflow on connect
  by adding `min-w-0` to the grid column and `overflow-hidden` to the terminal
  container (`web/src/routes/terminal/+page.svelte`).

### Verification

- `go test ./...` passes; Go backend and web frontend rebuilt.
- Verified live on port 9527 across `/ws/connect`, `/ws/terminal`, and `/ws/monitoring`: same-origin → `101`, cross-origin without proxy header → `403`, cross-origin with `X-Forwarded-Host` → `101`.

---

## [1.4.2] — 2026-07-03

### Fixed — WebSocket Authentication & Subprotocols
- **WebSocket Handshake Failures** — Fixed authentication failures on several WebSocket routes (`/ws/connect/`, `/ws/plan`, `/ws/migrate/`, `/ws/dryrun/`, `/ws/diff`, and `/ws/jobs/`) by negotiating and echoing back the `Sec-WebSocket-Protocol` subprotocol header (`meshium-auth.<token>`). Previously, these routes upgraded connections with `nil` response headers, causing browsers to reject the WebSocket handshakes with connection failures.
- **Verification** — Rebuilt Go backend and web frontend, and ran the complete backend test suite to ensure successful builds and test pass.

---

## [1.4.1] — 2026-07-03

Patch release building on v1.4.0. Adds a complete 14-stage zero-downtime migration pipeline engine with 10 specialized engines, 11-step guided wizard, real-time monitoring dashboard, automatic rollback, enterprise-grade SSH authentication with key management & fingerprints, enhanced discovery collectors, intelligence modules, workload classifier, planner strategy selector, WebSocket reliability improvements, and comprehensive security audit remediation.

### Zero-Downtime Migration Pipeline

#### Added — Pipeline Engine (`internal/mod/migration/pipeline.go`)
- **14-stage pipeline engine** — Orchestrates the full migration lifecycle: Planning → Discovery → CompatibilityCheck → RiskAssessment → Backup → ProvisionTarget → InstallDependencies → InitialSync → LiveReplication → Verification → PreCutover → TrafficSwitch → PostVerification → Observation → Committed
- **State machine** with 20+ states and strict transition validation — any state can transition to Failed/Interrupted, Failed → Rollback → RolledBack
- **Checkpoint persistence** — pipeline can resume from last checkpoint after interruption
- **LIFO rollback** — reverses changes in reverse order on failure
- **Configurable retry** — max retries, retry delay, auto-rollback thresholds

#### Added — Health Engine (`internal/mod/migration/health.go`)
- **11 health check types** — HTTP, HTTPS, TCP, DNS, SSL, Docker, Disk, Memory, CPU, Process, Custom
- **Health scoring** (0–100) based on latency thresholds
- **Context-aware DNS resolution** — respects cancellation
- **SSL certificate expiry** checking with configurable warning days

#### Added — Traffic Switch Engine (`internal/mod/migration/traffic.go`)
- **7 traffic providers** — Cloudflare (DNS API), Nginx, Traefik, HAProxy, Caddy, Docker Compose, DNS
- **Config backup & restore** — original config saved before switch, restored on rollback
- **Health-check-gated switching** — verifies target health before completing switch

#### Added — Replication Engine (`internal/mod/migration/replication.go`)
- **4 database types** — MySQL (master/replica), PostgreSQL (streaming), Redis (REPLICAOF), MongoDB (replica set)
- **Lag monitoring** — tracks replication lag in seconds
- **Catch-up detection** — signals when replica is within threshold

#### Added — Sync Engine (`internal/mod/migration/sync.go`)
- **rsync-based file sync** with delta transfer support
- **Checksum verification** — SHA256 post-sync validation
- **Bandwidth limiting** and parallel transfer support

#### Added — Queue Engine (`internal/mod/migration/queue.go`)
- **BullMQ** and **RabbitMQ** drain support
- **Pause → Drain → Verify → Resume** lifecycle
- **Job counting** and sync verification

#### Added — Provision Engine (`internal/mod/migration/provision.go`)
- **20+ components** — Docker, Compose, Nginx, Caddy, HAProxy, Redis, MySQL, PostgreSQL, MongoDB, Node.js, Certbot, etc.
- **Install → Configure → Verify** lifecycle per component
- **Version detection** and compatibility checking

#### Added — Risk Engine (`internal/mod/migration/risk_engine.go`)
- **10 risk factors** — data size, database size, containers, replication, network, resource mismatch, compatibility, queue depth, volume count, config complexity
- **Risk classification** — low / medium / high / critical
- **Downtime estimation** — predicts expected downtime window
- **Rollback complexity assessment** — simple / moderate / complex / very_complex

#### Added — Compatibility Engine (`internal/mod/migration/compatibility.go`)
- **20+ compatibility checks** — architecture, RAM, disk, Docker, kernel, OS, package manager, OpenSSL, storage driver, SELinux, ports, timezone, glibc, systemd, swap, etc.
- **Severity classification** — info / warning / high / critical
- **Critical blockers** — prevent migration from proceeding

#### Added — Cutover & Observation Engines (`internal/mod/migration/cutover.go`, `observation.go`)
- **Cutover sequence** — freeze writes → delta sync → catch-up → drain queues → health check → switch traffic → promote → resume writes
- **Observation monitoring** — configurable duration, health score threshold, auto-rollback trigger

#### Added — Pipeline API (`internal/mod/migration/pipeline_handler.go`)
- **REST endpoints** — session, stages, risk, compatibility, health, replication, sync, queue, provision, metrics, audit, config, control, export
- **WebSocket** — `/ws/pipeline/{id}` for real-time progress updates
- **CSRF protection** — WebSocket origin validation with `isSameOrigin`

#### Added — Database Schema (`internal/db/migrations.go`)
- **14 new tables** — pipeline_stages, replication_status, traffic_switch_configs, health_check_results, risk_reports, compatibility_checks, sync_sessions, queue_states, provision_states, migration_metrics, migration_audit, migration_configs, migration_sessions, migration_checkpoints

#### Added — Pipeline Tests (`internal/mod/migration/pipeline_test.go`, `pipeline_engines_test.go`)
- **25+ unit tests** for pipeline orchestration, state transitions, checkpoint persistence, error handling, rollback scenarios, resume capability
- **Engine tests** for health (11 check types), risk (10 factors), compatibility (20+ checks), replication validation

### Frontend — 11-Step Migration Wizard

#### Added — Pipeline API Client (`web/src/lib/api/pipeline.ts`)
- **15+ data model types** — PipelineStage, ReplicationStatus, TrafficSwitchConfig, HealthCheckResult, RiskReport, CompatibilityCheckResult, SyncSession, QueueState, ProvisionState, ServerResourceMetrics, ContainerHealthInfo, MigrationConfig, MigrationSession, WSMessageExtended, AuditEntry
- **REST API methods** — session, stages, risk, compatibility, health, replication, sync, queue, provision, metrics, audit, config, control, export
- **WebSocket client** — real-time pipeline progress with resource metrics (CPU, RAM, disk, network)
- **Wizard step definitions** — WIZARD_STEPS array with validation gates

#### Added — Migration Wizard (`web/src/routes/migrations/[id]/pipeline/+page.svelte`)
- **11-step guided wizard** — Discovery → Compatibility → Risk → Plan → Dry Run → Provision → Execute → Live Monitoring → Cutover → Observation → Finish
- **Validation gates** — each step requires completion before proceeding; critical compatibility failures block progression
- **Live metrics dashboard** — Progress, Transfer, Speed, ETA, Replication Lag, CPU, RAM, Disk, Queue Status, Container Health, Health Score, Timeline, Logs
- **Cutover confirmation** — explicit 7-step checklist dialog before traffic switch
- **Observation timer** — configurable countdown with auto-advance on completion
- **Auto-advance** — wizard progresses automatically based on pipeline state
- **State recovery** — resumes to correct step on page reload
- **Controls** — Pause, Resume, Rollback (with confirmation), Export Report, Diff view

### Flow Integration

#### Changed — Migration List (`web/src/routes/migrations/+page.svelte`)
- Migration links now point to `/migrations/{id}/pipeline` instead of `/migrations/{id}`

#### Changed — New Migration (`web/src/routes/migrations/new/+page.svelte`)
- After plan creation, auto-redirects to `/migrations/{id}/pipeline` instead of `/migrations`
- Backend now returns `migration_id` in the WebSocket plan-complete message

#### Changed — Migration Detail (`web/src/routes/migrations/[id]/+page.svelte`)
- Replaced with auto-redirect to `/migrations/{id}/pipeline`
- Old detail page functionality fully superseded by the wizard

#### Changed — Backend Plan Handler (`internal/mod/migration/handler.go`)
- Plan complete WebSocket message now includes `migration_id:{id}` in value field

### Security Fixes
- **WebSocket CSRF** — `CheckOrigin` validation with `isSameOrigin` helper for pipeline WebSocket
- **MigrationID persistence** — fixed `handler_factory.go` to preserve `job.MigrationID`
- **Engine.Stop() restartability** — `stopCh` is re-created after stop to prevent nil channel panic
- **Docker nil-panic** — empty data check before JSON unmarshalling in docker.go

### Documentation
- **docs/flow.md** — comprehensive user flow documentation covering all 11 wizard steps, dashboard metrics, database replication, traffic switching, state machine, and automatic rollback

---

## [1.4.0] — 2026-07-01

The major management update. Adds real interactive PTY terminal, 10 new server management modules (Docker, Services, Processes, Logs, Cron, Firewall, Monitoring, System Updates, Drift Detection, AI Assistant), file explorer with in-browser editor, security hardening (Phase 0), and comprehensive bug fixes.

### Interactive PTY Terminal

#### Added
- **Real PTY terminal** (`web/src/routes/terminal/+page.svelte`) — Full interactive SSH terminal using xterm.js
  - WebSocket-backed PTY session via `/ws/terminal/{serverId}` endpoint
  - `@xterm/xterm`, `@xterm/addon-fit`, `@xterm/addon-web-links` integrated
  - Lazy browser-only import of xterm modules (SSR-safe with `browser` guard)
  - FitAddon auto-resizes terminal to container dimensions
  - WebLinksAddon makes URLs clickable
  - Server selector dropdown — connect to any registered server
  - Connection status badge (Connecting / Connected / Disconnected)
  - Reconnect button and terminal clear action
  - PTY resize messages sent to backend on window resize

### Server Management Features

#### Added — Docker Manager (`/docker`)
- **Container operations** — Start, stop, restart, remove containers with confirmation dialogs
- **Image management** — List images, pull new images, remove unused images
- **Log streaming** — Live container logs via WebSocket with tail lines control
- **Compose projects** — List and manage Docker Compose stacks

#### Added — Service Manager (`/services`)
- **Systemd service control** — Start, stop, restart, enable, disable services
- **Service list** — Filter by active/inactive/failed state with search
- **Status badges** — Color-coded active/inactive/failed/unknown states

#### Added — Process Manager (`/processes`)
- **Live process table** — List running processes sorted by CPU or memory
- **Kill/signal** — Send SIGTERM, SIGKILL, or custom signals to PIDs with PID validation
- **Auto-refresh** — Configurable polling interval for real-time updates

#### Added — Log Viewer (`/logs`)
- **File log streaming** — Read and tail arbitrary log files via WebSocket
- **Journalctl integration** — Stream system journal and per-service logs
- **Search** — Filter log output by keyword
- **Log file browser** — Common log paths pre-populated (`/var/log/syslog`, nginx, etc.)

#### Added — Cron Manager (`/cron`)
- **Cron job list** — Read per-user and system crontabs
- **Add/edit/delete jobs** — CRUD for cron entries with schedule preset picker
- **Validation** — Schedule expression validation before save

#### Added — Firewall Manager (`/firewall`)
- **Rule list** — View active ufw/iptables rules
- **Add/delete rules** — Port, protocol, source IP configuration
- **Enable/disable** — Toggle firewall on/off with status indicator

#### Added — Monitoring Dashboard (`/monitoring`)
- **Real-time metrics** — CPU, memory, disk, network I/O, load average streamed via WebSocket
- **Top processes** — Live top-N processes by CPU/memory from snapshot
- **History charts** — Time-series sparklines for CPU and memory trends

#### Added — System Update Manager (`/updates`)
- **Update checker** — Detect available updates for apt/yum/dnf/pacman
- **Install updates** — Apply all or selected package updates with live output
- **Package management** — Install/remove arbitrary packages with real-time streaming

#### Added — Drift Detection (`/drift`)
- **Snapshot comparison** — Compare two server snapshots to detect configuration drift
- **Server-to-server diff** — Side-by-side diff of services, packages, Docker containers, nginx config
- **Change summary** — Categorized added/removed/modified items per category

#### Added — AI Assistant (`/assistant`)
- **Rule-based chat** — Context-aware chat interface with server selector
- **Command help** — Explains CLI commands and suggests alternatives
- **Log analysis** — Paste log output for error identification and fix suggestions
- **Proactive suggestions** — Recommends actions based on snapshot data (high CPU, expiring certs, etc.)

#### Added — Backend (10 new modules)
- `internal/mod/docker/service.go` — Docker API over SSH (containers, images, volumes, compose)
- `internal/mod/service/service.go` — systemd unit management via `systemctl`
- `internal/mod/process/service.go` — Process listing and signal via `/proc` and `kill`
- `internal/mod/logview/service.go` — File tailing and journalctl streaming
- `internal/mod/cron/service.go` — Crontab read/write with validation
- `internal/mod/firewall/service.go` — ufw/iptables rule management
- `internal/mod/monitoring/service.go` — `/proc`-based metrics collection with WebSocket broadcast
- `internal/mod/sysupdate/service.go` — Package manager abstraction (apt/yum/dnf/pacman)
- `internal/mod/drift/service.go` — Snapshot diff engine
- `internal/mod/ai/service.go` — Rule-based assistant with server context integration
- All handlers use `shared.ShellQuote()` for shell injection safety

### File Explorer

#### Added — Backend File Service
- **File Service** (`internal/mod/file/`) — SSH-based file operations on remote servers
  - `ListDirectory` — List directory contents with `ls -la --time-style=+%s`, parse permissions, ownership, timestamps, symlinks
  - `ReadFile` — Read file content with binary/text detection via MIME type, truncation for large files (>100KB text, >1MB binary)
  - `WriteFile` — Write file content using base64 encoding for safe shell transmission, with overwrite protection and chmod support
  - `Delete` — Delete files/directories with recursive flag
  - `Rename` — Move/rename files with automatic parent directory creation
  - `Mkdir` — Create directories with optional mode
  - `Stat` — Get file metadata (size, type, permissions, owner, group, timestamps)
  - `DownloadFile` — Download file content as base64 for HTTP response
  - Credential decryption using `shared.Decrypt()` for password, SSH keys, passphrases
  - Bastion/jump host support mirroring migration executor pattern
  - Pool adapter for SSH connection pool integration

#### Added — File HTTP Handler
- **9 REST endpoints** (`internal/handler/file_handler.go`)
  - `GET /api/servers/{id}/files` — List directory (with `path`, `hidden` query params)
  - `GET /api/servers/{id}/files/content` — Read file content (with `path`, `maxSize` params)
  - `GET /api/servers/{id}/files/download` — Download file with proper Content-Disposition headers
  - `POST /api/servers/{id}/files/upload` — Upload file via multipart form (100MB max)
  - `POST /api/servers/{id}/files` — Write file content (JSON body)
  - `DELETE /api/servers/{id}/files` — Delete file/directory (with `recursive` param)
  - `PUT /api/servers/{id}/files` — Rename/move file
  - `POST /api/servers/{id}/files/mkdir` — Create directory
  - `GET /api/servers/{id}/files/stat` — Get file metadata

#### Added — Frontend File Browser
- **File API Client** (`web/src/lib/api/files.ts`) — TypeScript client with `listFiles`, `getFileContent`, `downloadFile`, `uploadFile`, `writeFile`, `deleteFile`, `renameFile`, `mkdir`, `statFile`, plus helper functions (`formatFileSize`, `getFileIcon`, `isPreviewable`)
- **File Browser Page** (`web/src/routes/files/[id]/+page.svelte`)
  - Breadcrumb navigation with clickable path segments
  - File list with type-specific icons (folder, code, image, archive, terminal, symlink)
  - Sortable columns (name, size, modified date) with asc/desc toggle
  - Search/filter by filename
  - Show/hide hidden files toggle
  - File preview modal with syntax-highlighted code display
  - Binary file detection with MIME type display
  - Download, rename, delete, mkdir actions
  - Upload modal with drag-and-drop file selection, overwrite flag, remote path configuration
  - Stats bar showing folder count, file count, total size
  - Loading skeletons and empty states

#### Added — File Editor
- **In-browser file editor** — Edit remote files directly without nano/vim/cat
  - Edit button (pencil icon) on each file row and in preview modal
  - Full-screen editor modal with monospace textarea
  - Line numbers gutter with scroll sync
  - **Ctrl+S / Cmd+S** keyboard shortcut to save
  - Unsaved changes indicator (amber dot) and saved state (green dot)
  - Binary file detection prevents editing
  - Confirm dialog when closing with unsaved changes
  - File info bar showing path, MIME type, size, and save status

#### Added — Files Index Page
- **Server browser section** — Grid of server cards with "Browse Files" buttons linking to per-server file browser
- **Browse Files button** on each partition card in the disk usage view

### Migration Flow Fixes (P0-P2)

#### Security
- **P0-1: WebSocket auth via session token** — All WebSocket endpoints (`/ws/plan`, `/ws/migrate`, `/ws/dryrun`, `/ws/diff`) now validate session tokens from query parameters, preventing unauthorized WebSocket access
- **P0-1b: Token validation on migration WS handlers** — Migration WebSocket handlers (handleMigrateWS, handlePlanWS, handleDryRunWS, handleDiffWS) validate session tokens before upgrading to WebSocket
- **P0-1c: Frontend WS token injection** — All frontend WebSocket connections (wsPlan, wsExecute, wsRollback, wsDryRun) now send session token as query parameter

#### Migration Engine
- **P0-2: Wire createMigrationHandler** — Migration job handler is now properly wired to create a MigrationJobHandler with migration engine, repo, and SSH connections (previously returned "not yet supported" error)
- **P0-3: RecoverInterrupted on startup** — `RecoverInterrupted()` is now called after migration components are created but before server starts, automatically recovering interrupted migrations on application restart
- **P0-4: Atomic migration execution lock** — In-memory lock (sync.Map) in executor prevents concurrent execution of the same migration. DB-level atomic check (UPDATE...WHERE status='planned') prevents double execution across restarts

#### Rollback
- **P1-1: Rollback error tracking** — Rollback failures are no longer silently swallowed. Status is set to `rollback_failed` if any category fails, instead of always `rolled_back`
- **P1-2: Backup query ordering** — `GetBackups()` now has `ORDER BY id ASC` so LIFO rollback iteration is correct (previously had no ordering, causing non-deterministic rollback order)

#### Planner
- **P2-1: ConfigsCollector singleton mutation** — Planner now creates a new ConfigsCollector instance per plan instead of mutating the shared singleton's Paths field, preventing race conditions
- **P2-2: Fatal collection failures** — Failed collection in planner now prevents migration execution (previously created a step with empty data that was silently skipped by executor)

### Security Hardening

#### Phase 0 Refactor
- **Shell injection prevention** — All new handlers (`cron`, `docker`, `drift`, `firewall`, `process`, `service`, `sysupdate`) use `shared.ShellQuote()` for all user-supplied arguments passed to shell commands
- **CSRF protection hardening** — Strengthened CSRF token validation; updated CSRF middleware and added `csrf_test.go` with 68 test cases covering token generation, validation, and edge cases
- **Graceful shutdown** — `cmd/server/main.go` now registers OS signal handler (`SIGINT`, `SIGTERM`) and calls `ShutdownManager.Shutdown()` with 30s timeout before process exit
- **SSH pool invalidation** — `internal/mod/server/service.go` invalidates SSH pool entries on server credential update or delete, preventing stale connections
- **WebSocket token validation** — New WebSocket endpoints validate session token from query parameter before upgrading connection (`internal/shared/websocket.go`)
- **Database indexes** — Added indexes: `migrations(status, created_at)`, `migrations(source_id)`, `migrations(target_id)`, `migration_steps(migration_id, status)`, `servers(favorite, name)` for query performance

### Bug Fixes

#### Audit Fixes
- **Server ID validation** — Added `serverID <= 0` guard to all new handlers (cron, docker, drift, firewall, process, service, sysupdate) returning 400 on invalid ID
- **PID validation** — Process handler now validates `pid <= 0` before sending signals, returning 400 instead of sending signal to PID 0
- **Process error classification** — SSH/backend errors in process handler now return 500 instead of 400/404
- **WriteFile mode validation** — `WriteFile` now validates file mode before writing to prevent partial writes on invalid mode
- **File handler maxSize parsing** — Returns 400 on malformed `maxSize` query parameter instead of silently ignoring
- **Monitoring history endpoint** — Returns proper error instead of empty success response for unimplemented history feature
- **Logs page reactivity** — `viewerEl` declared with `$state()` for proper Svelte 5 reactivity
- **Services page Card class** — Wrapped `Card` with `<div>` instead of passing unsupported `class` prop
- **Updates page Spinner** — Added required `label` prop to all 7 Spinner instances

#### Previous Bug Fixes
- **ls parser field count** — Parser for `ls -la --time-style=+%s` output required 9 fields but `--time-style=+%s` produces only 7. Caused all regular directories to be skipped — only symlinks passed the check. Fixed minimum field count from 9 to 7.
- **Modal open prop** — All 6 Modal components in the file browser were missing `open` prop (defaults to `false`), preventing any modal from rendering. Added `open={true}` to all modals.

---

## [1.3.0] — 2026-06-29

The first major update since v1.2.0. Includes Discovery Engine, Migration Planner, Job Engine, security hardening, frontend UX improvements, Docker deployment, and structured logging.

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
- **Atomic salt storage** — `pbkdf2_salt` is now stored in the same DB transaction as `master_password_hash`, `ssh_key_private_encrypted`, and `ssh_key_public`, preventing inconsistent unlock states
- **Fail-fast Unlock()** — `Unlock()` now returns a clear error when `pbkdf2_salt` is missing or empty
- **SSH helper decrypt errors** — `buildSSHConfig()` and `resolveBastion()` now return decrypt errors instead of ignoring them
- **Snapshot null checks** — Discovery, monitoring, and dashboard pages now treat both `null` and `undefined` snapshots as missing

### Frontend
- **Shared snapshot cache** — Added `/web/src/lib/stores/snapshots.ts` so discovery, monitoring, docker, files, alerts, dashboard, and server detail pages share snapshot data
- **403 LOCKED redirect** — The API client now clears the session token and redirects to `/login` on `403` responses with `code: "LOCKED"`
- **Favicon** — Added `favicon.svg` and a `<link rel="icon">` in `app.html`
- **Svelte 5 compatibility** — Fixed `{@const}` placement so it is an immediate child of `{#each}` blocks
- **Card class handling** — Wrapped `Card` usage in `<div class="...">` instead of passing `class` as a prop
- **Alerts page** — Aggregates alerts from snapshots, jobs, and migrations
- **Terminal page** — SSH connection test UI with WebSocket real-time output
- **Files page** — Disk usage explorer with partition aggregation and filtering
- **Dashboard page** — Real activity feed showing recent jobs and migrations

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

### Migration Planner (Phase 5)

#### Added — Migration Plan Types
- **MigrationPlan** — Concrete migration plan with ordered steps, estimates, risk assessment, and warnings/blockers
  - `ID`, `CreatedAt`, `Source`/`Target` (ServerSummary), `Steps` ([]PlannedStep), `TotalEstimate`, `RiskLevel`, `Warnings`, `Blockers`
  - `HasBlockers()` — quick check for blocking issues
  - `StepCount()` — number of steps in the plan
- **PlannedStep** — Single step in a migration plan
  - `Order`, `Name`, `Type` (StepType), `DependsOn` ([]int), `Estimate` (TransferEstimate), `RiskLevel`, `Reversible`, `Config` (map[string]interface{})
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
- **DatabaseStepGenerator** — Generates Database dump/restore steps
- **FileStepGenerator** — Generates File transfer steps for non-Docker directories
- **NginxStepGenerator** — Generates Nginx config migration steps
- **ServiceStepGenerator** — Generates systemd service management steps

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

#### Added — Transfer Estimator
- **Estimator interface** — `EstimateStep(step, source) TransferEstimate`
- **DefaultEstimator** with configurable speed assumptions:
  - Network transfer: 100 MB/s
  - Database dump: 50 MB/s
  - rsync overhead: 1.2x (20% protocol overhead)
- Confidence scoring (0.0–1.0) based on data availability

#### Added — Plan Store
- **PlanStore interface** — `SavePlan`, `LoadPlan`, `ListPlans`, `DeletePlan`
- **SQLitePlanStore** — SQLite-backed with WAL mode
  - `migration_plans` table with id, plan JSON, source/target host, step count, risk level, has_blockers, created_at
  - Index on created_at for efficient listing
  - Upsert support (ON CONFLICT DO UPDATE)
- **NoopPlanStore** — No-op implementation for testing

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

### Transfer Engine (Phase 3)

#### Added — File Transfer
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

### State Machine Engine (Phase 2)

#### Added — Migration Lifecycle
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

### Infrastructure
- **Docker deployment** — Multi-stage Dockerfile (node → go → alpine) with docker-compose.yml, non-root user, health check
- **Structured logging** — JSON logger with configurable log level (`internal/shared/logger.go`)
- **Updated dependencies** — postcss 8.5.16, @sveltejs/kit 2.68.0, svelte 5.56.4, cookie override for CVE fixes

### Testing
- 220+ tests pass across 11 packages with `-race` detector
  - 35 tests in `internal/mod/discovery` — 8 collectors, collector runner, dependency graph, compatibility checker, snapshot store
  - 43 tests in `internal/mod/planner` — plan generation, risk assessment, estimator, plan store, bridge
  - 31 tests in `internal/jobengine` — job queue, store, progress broadcaster, shutdown manager, engine submit/cancel/pause/resume, concurrent jobs, recovery
  - All existing tests in ssh, migration, transfer, auth, server, shared, db continue to pass

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
| 1.4.0 | 2026-07-01 | Interactive PTY terminal (xterm.js), 10 new management modules (Docker, Services, Processes, Logs, Cron, Firewall, Monitoring, Updates, Drift, AI Assistant), file explorer + in-browser editor, security hardening (Phase 0), DB indexes, audit bug fixes |
| 1.3.0 | 2026-06-29 | Security hardening, discovery engine, migration planner, job engine, transfer engine, state machine, crash recovery, Docker deployment, structured logging, frontend UX fixes |
| 1.2.0 | 2026-06-28 | Mobile bottom navbar, SSH host key auto-accept, API null→[] fix |
| 1.1.0 | 2026-06-28 | Docker migration, dry run, diff view, bastion/jump host, pre-flight validation, config exclusion, CI/CD, SSH pool concurrency |
| 1.0.0 | 2026-06-27 | Initial release — full migration engine, web UI, 85 tests |
