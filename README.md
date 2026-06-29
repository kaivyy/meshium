<div align="center">

# Meshium

### Server Migration Engine for Linux

Migrate packages, configurations, services, and users across Linux servers — securely, over SSH, with a live web dashboard.

[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![SvelteKit](https://img.shields.io/badge/SvelteKit-2.x-FF3E00?style=flat-square&logo=svelte)](https://svelte.dev)
[![CI](https://img.shields.io/badge/CI-GitHub%20Actions-2088FF?style=flat-square&logo=githubactions)](https://github.com/kaivyy/meshium/actions)
[![License](https://img.shields.io/badge/License-MIT-22c55e?style=flat-square)](LICENSE)
[![Release](https://img.shields.io/badge/Release-v1.3.0-22c55e?style=flat-square)](https://github.com/kaivyy/meshium/releases)
[![Tests](https://img.shields.io/badge/Tests-220%2B%20passing%20with%20%2Drace-22c55e?style=flat-square)](#testing)

</div>

---

## Overview

Meshium is a self-hosted server migration tool that lets you replicate the configuration of one Linux server onto another — all through a clean web interface. Connect your servers via SSH, select what to migrate (packages, config files, services, users), and watch the progress in real-time.

No agents. No daemons on target machines. Just SSH.

### What It Migrates

| Category | What Gets Transferred | Supported Distros |
|----------|----------------------|-------------------|
| **Packages** | Installed package list (install missing on target) | apt, dnf, pacman, apk, zypper |
| **Config Files** | Files from `/etc/` and custom paths (OS-critical files excluded) | All (via SFTP) |
| **Services** | Enabled systemd units | systemd-based distros |
| **Users & Security** | Users, groups, cron jobs, firewall rules | All (via SSH commands) |
| **Docker** | Containers, images, volumes, compose files | Any server with Docker installed |

### Key Features

- **Zero-Agent Architecture** — Everything runs over SSH. No software to install on target machines.
- **Mobile-Friendly UI** — Responsive layout with bottom navigation bar on mobile, full sidebar on desktop.
- **Live WebSocket Progress** — Watch each migration step unfold in real-time through the web UI.
- **Discovery Engine** — 8 parallel collectors build a comprehensive ServerSnapshot: OS, hardware, Docker, systemd services, databases, Nginx, disk usage, and open ports.
- **Shared Snapshot Cache** — A centralized snapshot store lets discovery, monitoring, Docker, files, alerts, dashboard, and server detail pages reuse cached snapshots and avoid redundant 404 retries.
- **Dependency Graph** — Automatically builds a directed graph of service dependencies (containers → databases, Nginx → containers, service → service) with topological sort for migration ordering.
- **Compatibility Checker** — Validates source vs target compatibility (RAM, disk, Docker version, port conflicts, OS family) before migration. Reports blockers and warnings.
- **Dry Run Mode** — Preview what will change before applying. See per-category additions, modifications, and removals.
- **Server Diff** — Compare source and target servers side-by-side across all categories before migrating.
- **Pre-Flight Validation** — Check disk space, OS compatibility, Docker availability, and SSH connectivity before execution.
- **Docker Migration** — Migrate containers, images, volumes, and compose files across servers.
- **Transfer Engine** — SCP and rsync strategies with resume support, SHA256 checksum verification, and progress tracking.
- **State Machine Engine** — 13-state migration lifecycle with validated transitions, checkpointing, and automatic rollback in LIFO order.
- **Crash Recovery** — Interrupted migrations are automatically detected on restart. Resume from the last verified checkpoint or cancel with rollback.
- **Migration Planner** — Takes ServerSnapshot + DependencyGraph from Phase 4 and generates a concrete migration plan with ordered steps, transfer estimates, and risk assessment.
- **Risk Assessment** — Per-step and overall risk evaluation (Low/Medium/High/Critical) based on database size, container state, port conflicts, and dependency complexity.
- **Transfer Estimator** — Estimates transfer size and duration per step with configurable speed assumptions (100 MB/s network, 50 MB/s database dump, 1.2x rsync overhead).
- **Plan Storage** — Migration plans persisted to SQLite with WAL mode for concurrent access. Save, load, list, and delete plans.
- **Job Engine** — Top-level orchestrator that queues, executes, and monitors migration/discovery/compatibility jobs through a persistent SQLite-backed queue with worker pool, real-time progress broadcasting, pause/resume/cancel, and graceful shutdown.
- **Progress Broadcasting** — Channel-based pub/sub system for real-time job progress. Non-blocking broadcast drops updates to slow subscribers instead of blocking the worker.
- **Graceful Shutdown** — 30-second timeout lets running jobs finish their current step before force-cancelling. Job state is persisted for resume on restart.
- **SSH Bastion / Jump Host** — Tunnel connections through a bastion server for firewalled or private network targets.
- **Config Exclusion List** — OS-critical files (`/etc/fstab`, `/etc/passwd`, `/etc/shadow`, etc.) are automatically protected from overwrite.
- **Automatic Rollback** — Every migration creates backups. If something fails, Meshium rolls back automatically.
- **Multi-Distro Support** — Detects the source distro and translates package names for the target distro.
- **Export Migration Plans** — Download migration plans as JSON for audit or import on another instance.
- **Secure by Design** — AES-256-GCM encrypted credentials at rest, SSH key management, known-hosts verification.
- **Single Binary** — Frontend is embedded in the Go binary. Deploy with one file.
- **SQLite Storage** — No external database needed. Everything in one file.
- **CI/CD** — GitHub Actions workflow runs all tests and builds on every push and pull request.

---

## Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              Meshium Binary                  │
                    │                                             │
                    │  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
                    │  │  Auth    │  │  Server   │  │Discovery  │ │
                    │  │ Module   │  │  Manager  │  │ Module    │ │
                    │  └────┬─────┘  └────┬─────┘  └────┬─────┘ │
                    │       │             │              │        │
                    │  ┌────┴─────────────┴──────────────┴─────┐  │
                    │  │     SSH Connection Pool (max 10)     │  │
                    │  │     + bastion/jump host tunneling    │  │
                    │  └────────────────┬────────────────────┘  │
                    │                   │                        │
                    │  ┌────────────────┴────────────────────┐  │
                    │  │       Discovery Engine (Phase 4)      │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │ 8 Coll.│ │Dep Graph│ │Compat Chk│  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────────────────────────────┐  │  │
                    │  │  │ Snapshot Store (SQLite)          │  │  │
                    │  │  └────────────────────────────────┘  │  │
                    │  └────────────────┬────────────────────┘  │
                    │                   │                        │
                    │  ┌────────────────┴────────────────────┐  │
                    │  │    Migration Planner (Phase 5)        │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │Planner │ │Risk Asmt│ │ Estimator │  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────┐ ┌──────────────────────┐  │  │
                    │  │  │Bridge  │ │ Plan Store (SQLite)   │  │  │
                    │  │  └────────┘ └──────────────────────┘  │  │
                    │  └────────────────┬────────────────────┘  │
                    │                   │                        │
                    │  ┌────────────────┴────────────────────┐  │
                    │  │         Job Engine (Phase 6)          │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │ Queue  │ │ Store  │ │Broadcaster│ │  │
                    │  │  │(SQLite)│ │(SQLite)│ │ (pub/sub) │ │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │Workers │ │Shutdown│ │ Handlers  │  │  │
                    │  │  │(pool)  │ │(30s)   │ │ (3 types) │  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  └────────────────┬────────────────────┘  │
                    │                   │                        │
                    │  ┌────────────────┴────────────────────┐  │
                    │  │         Migration Engine              │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │ Planner│ │Executor│ │ Rollback │  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │Dry Run │ │  Diff  │ │Pre-Flight│  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────┐ ┌──────────────────────┐  │  │
                    │  │  │ Engine │ │ Recovery Manager      │  │  │
                    │  │  │(State  │ │ (interrupt/resume)    │  │  │
                    │  │  │Machine)│ └──────────────────────┘  │  │
                    │  │  └────────┘                            │  │
                    │  │  ┌────────────────────────────────┐  │  │
                    │  │  │ Category Registry               │  │  │
                    │  │  │ packages · configs · services   │  │  │
                    │  │  │ users · docker · distro adapter │  │  │
                    │  │  └────────────────────────────────┘  │  │
                    │  └──────────────────────────────────────┘  │
                    │                                             │
                    │  ┌──────────────────────────────────────┐  │
                    │  │  Transfer Engine (SCP + rsync)       │  │
                    │  │  Resume · Checksum · Progress        │  │
                    │  └──────────────────────────────────────┘  │
                    │                                             │
                    │  ┌──────────────────────────────────────┐  │
                    │  │  Embedded SvelteKit Frontend (SPA)   │  │
                    │  └──────────────────────────────────────┘  │
                    │                                             │
                    │  ┌──────────────────────────────────────┐  │
                    │  │  SQLite Database (encrypted creds)   │  │
                    │  └──────────────────────────────────────┘  │
                    └─────────────────────────────────────────────┘
                                    │
                         SSH/SFTP over network
                                    │
                    ┌───────────┐       ┌───────────┐
                    │  Source    │       │  Target    │
                    │  Server    │       │  Server    │
                    └───────────┘       └───────────┘
                                          │ (optional)
                                    ┌───────────┐
                                    │  Bastion   │
                                    │  Jump Host │
                                    └───────────┘
```

### Project Structure

```
meshium/
├── cmd/server/
│   ├── main.go              # Entry point — wires all modules
│   ├── embed.go             # Embeds frontend build into binary
│   └── web/build/           # Compiled SvelteKit output (embedded)
├── internal/
│   ├── db/
│   │   ├── db.go            # SQLite connection
│   │   └── migrations.go     # Schema migrations
│   ├── shared/
│   │   ├── config.go         # Env-based config
│   │   ├── crypto.go         # AES-256-GCM encryption
│   │   └── types.go         # Shared HTTP helpers
│   ├── mod/
│   │   ├── auth/             # Password auth, session management
│   │   ├── ssh/              # SSH client, pool, keypair, known-hosts
│   │   ├── server/           # Server CRUD (the "server manager")
│   │   ├── discovery/        # Discovery engine (Phase 4)
│   │   │   ├── model.go           # SystemInfo, WSMessage, StepResult
│   │   │   ├── service.go         # Connection test orchestrator
│   │   │   ├── handler.go         # WebSocket handler for /ws/connect
│   │   │   ├── snapshot.go        # ServerSnapshot + all sub-types
│   │   │   ├── collector_runner.go # Parallel collector runner
│   │   │   ├── collectors_service.go # 8 collectors (OS, HW, Docker, etc.)
│   │   │   ├── graph.go           # Dependency graph builder + topo sort
│   │   │   ├── compat.go          # Compatibility checker
│   │   │   └── store.go          # SQLite + Noop snapshot stores
│   │   ├── discoverystep/    # DiscoveryStep + CompatibilityStep (Phase 4)
│   │   │   └── step.go            # MigrationStep implementations
│   │   ├── planner/         # Migration planner (Phase 5)
│   │   │   ├── plan.go            # MigrationPlan, PlannedStep, all types
│   │   │   ├── planner.go         # Planner interface + DefaultPlanner
│   │   │   ├── generators.go      # Step generators per workload type
│   │   │   ├── risk.go            # RiskAssessor implementation
│   │   │   ├── estimator.go       # Transfer estimator
│   │   │   ├── store.go           # PlanStore SQLite implementation
│   │   │   └── bridge.go          # BuildSteps: MigrationPlan → MigrationStep
│   │   ├── transfer/         # File transfer engine (Phase 3)
│   │   │   ├── strategy.go        # SCP/rsync strategy selector
│   │   │   ├── step.go            # FileTransferStep + DirectoryTransferStep
│   │   │   ├── scp.go             # SCP/SFTP strategy
│   │   │   ├── rsync.go           # rsync over SSH strategy
│   │   │   ├── checksum.go        # SHA256 checksum verifier
│   │   │   └── directory.go        # Directory transfer with resume
│   │   └── migration/        # The migration engine
│   │       ├── model.go      # Types, interfaces, status constants
│   │       ├── state.go      # State machine (13 states, validated transitions)
│   │       ├── step.go       # MigrationStep interface + CategoryStepAdapter
│   │       ├── engine.go     # Engine (Prepare→Apply→Verify, checkpoint, resume)
│   │       ├── job.go        # JobRepository, JobStep, Checkpoint types
│   │       ├── recovery.go   # RecoveryManager (interrupt discovery, resume, cancel)
│   │       ├── distro.go     # Distro detection + package mapping
│   │       ├── categories.go # Category registry
│   │       ├── packages.go   # Package collector/applier
│   │       ├── configs.go    # Config file collector/applier (with exclusion list)
│   │       ├── services.go   # Systemd service collector/applier
│   │       ├── users.go      # User/group/cron/firewall collector/applier
│   │       ├── docker.go     # Docker container/image/volume/compose collector/applier
│   │       ├── planner.go    # Collects from source, persists plan
│   │       ├── executor.go   # Applies plan to target with backups
│   │       ├── rollback.go   # Restores from backups in reverse
│   │       ├── dryrun.go     # Preview changes without applying
│   │       ├── diff.go       # Bidirectional source vs target comparison
│   │       ├── preflight.go  # Pre-flight validation (disk, OS, Docker, SSH)
│   │       ├── ssh_helper.go # Shared SSH connection helper with bastion support
│   │       ├── repo.go       # SQLite CRUD for migrations
│   │       ├── handler.go    # REST + WebSocket handlers
│   │       └── runner.go     # Composite runner delegation
│   ├── jobengine/           # Job Engine (Phase 6)
│   │   ├── job.go            # Domain types (Job, JobStatus, JobProgress, etc.)
│   │   ├── queue.go          # SQLite + in-memory FIFO job queue
│   │   ├── store.go          # SQLite job + log persistence
│   │   ├── progress.go       # Channel-based pub/sub progress broadcaster
│   │   ├── handlers.go       # Migration/Discovery/CompatCheck job handlers
│   │   ├── engine.go         # Main orchestrator (worker pool, submit, cancel)
│   │   ├── shutdown.go       # Graceful shutdown manager (30s timeout)
│   │   └── engine_test.go    # 31 tests (all pass with -race)
│   └── ...
├── web/                     # SvelteKit frontend
│   ├── src/
│   │   ├── lib/
│   │   │   ├── api/         # REST + WebSocket clients
│   │   │   ├── stores/       # Svelte stores (auth, servers, migrations)
│   │   │   └── components/  # Sidebar, etc.
│   │   └── routes/          # Pages (servers, migrations, settings)
│   └── ...
├── docs/                    # Design specs, API docs, and plans
│   ├── prd.md               # Product requirements document
│   └── api.md               # Comprehensive API reference
├── .github/workflows/       # CI/CD (GitHub Actions)
├── Makefile
├── go.mod
└── go.sum
```

---

## Getting Started

### Prerequisites

- **Go** 1.24+
- **Node.js** 20+ (only for frontend development)
- A Linux machine with SSH access to your target servers

### Quick Start

```bash
# Clone
git clone https://github.com/kaivyy/meshium.git
cd meshium

# Build (compiles Go + embeds pre-built frontend)
make build

# Run
./bin/meshium

# Or with a custom port
MESHium_PORT=9527 ./bin/meshium
```

Then open `http://localhost:8080` (or your custom port) in your browser. On first launch, you'll be prompted to set up a password.

### Building the Frontend

The frontend is pre-built and embedded in the binary. To rebuild it:

```bash
cd web
npm install
npm run build    # outputs to ../cmd/server/web/build/
cd ..
make build      # recompiles Go with new frontend embedded
```

### Running as a Systemd Service

```ini
# /etc/systemd/system/meshium.service
[Unit]
Description=Meshium Server
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/meshium
ExecStart=/opt/meshium/bin/meshium
Environment=MESHium_PORT=9527
Environment=HOME=/root
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable --now meshium
```

---

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MESHium_PORT` | `8080` | HTTP server port |
| `MESHium_DATA_DIR` | `~/.meshium` | Data directory (database, keys) |

Data is stored in `~/.meshium/` by default:
- `meshium.db` — SQLite database (servers, migrations, credentials)
- `id_ed25519` / `id_ed25519.pub` — Auto-generated SSH keypair

---

## API Reference

> See [docs/api.md](docs/api.md) for comprehensive API documentation with request/response schemas, error codes, and examples.

### REST Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/health` | Health check |
| `GET` | `/api/auth/status` | Check auth status |
| `POST` | `/api/auth/setup` | Initial password setup |
| `POST` | `/api/auth/unlock` | Unlock with password |
| `POST` | `/api/auth/lock` | Lock session |
| `GET` | `/api/servers` | List all servers (supports `?environment=`, `?region=`, `?tag=`, `?q=` filters) |
| `POST` | `/api/servers` | Add a server |
| `GET` | `/api/servers/{id}` | Get server details |
| `PUT` | `/api/servers/{id}` | Update server |
| `DELETE` | `/api/servers/{id}` | Delete server |
| `PATCH` | `/api/servers/{id}/favorite` | Toggle favorite |
| `GET` | `/api/servers/{id}/info` | Get cached system info |
| `GET` | `/api/migrations` | List all migrations |
| `POST` | `/api/migrations` | Create migration plan |
| `GET` | `/api/migrations/{id}` | Get migration details |
| `DELETE` | `/api/migrations/{id}` | Delete migration |
| `POST` | `/api/migrations/{id}/rollback` | Rollback migration |
| `GET` | `/api/migrations/{id}/steps` | Get migration steps |
| `GET` | `/api/migrations/{id}/dryrun` | Preview changes (dry run) |
| `GET` | `/api/migrations/{id}/preflight` | Pre-flight validation checks |
| `GET` | `/api/migrations/{id}/export` | Export migration plan as JSON |
| `POST` | `/api/diff` | Compare source vs target servers |

### WebSocket Endpoints

| Path | Description |
|------|-------------|
| `ws://host/ws/plan` | Plan a migration (send `PlanRequest`, receive progress) |
| `ws://host/ws/migrate/{id}` | Execute a migration (receive live progress) |
| `ws://host/ws/migrate/{id}/rollback` | Rollback a migration (receive live progress) |
| `ws://host/ws/dryrun/{id}` | Dry run a migration (receive live progress) |
| `ws://host/ws/diff` | Compare source vs target (receive live progress) |
| `ws://host/ws/connect/{id}` | Connect to server & stream discovery steps |

---

## How Migration Works

### 0. Discovery Engine (Phase 4)

Before planning a migration, the **Discovery Engine** builds a comprehensive snapshot of each server:

- **8 Parallel Collectors** — Each runs independently with its own timeout. One collector failure doesn't stop the others.
  - **OS Collector** — Distro, kernel, architecture, timezone, uptime, hostname, virtualization
  - **Hardware Collector** — CPU model/cores, RAM total/used, disk total/used
  - **Docker Collector** — Containers, images, compose projects (nil if Docker not installed)
  - **Service Collector** — Active systemd services with dependencies (After/Requires)
  - **Database Collector** — Detected MySQL, PostgreSQL, MongoDB, Redis instances
  - **Nginx Collector** — Version, virtual hosts, upstreams, SSL certificates
  - **Disk Collector** — Per-partition usage (filesystem, mount, size, used, avail)
  - **Port Collector** — Open/listening TCP ports with process and PID

- **ServerSnapshot** — The combined output of all collectors, stored as JSON in SQLite.

- **Dependency Graph** — Built from the snapshot:
  - Docker container → Database (port matching)
  - Nginx vhost → Docker container (proxy_pass/upstream port matching)
  - Systemd service → Systemd service (After/Requires directives)
  - Systemd service → Database (name convention matching)
  - Topological sort determines migration order

- **Compatibility Checker** — Compares source and target snapshots:
  - **RAM**: target total ≥ source used (blocker if insufficient)
  - **Disk**: target total ≥ source used (blocker if insufficient)
  - **Docker**: if source has Docker, target must too (blocker); version mismatch (warning)
  - **Port conflicts**: target ports already in use by different process (blocker)
  - **OS**: different OS family or architecture (warning)

Snapshots are persisted via the **SnapshotStore** (SQLite-backed) and can be loaded for compatibility checks or migration planning.

### 0.5. Migration Planner (Phase 5)

The **Migration Planner** takes the output of the Discovery Engine (ServerSnapshot + DependencyGraph) and generates a concrete, executable migration plan:

- **Planner** — Runs the compatibility checker, builds the dependency graph, performs topological sort, and generates ordered steps:
  1. Run compatibility check → convert blockers/warnings to plan blockers/warnings
  2. Build dependency graph from source snapshot
  3. Topological sort → determine safe migration order
  4. For each node: generate PlannedStep using workload-specific generators
  5. Compute DependsOn from graph edges
  6. Estimate transfer size and duration per step
  7. Assess risk per step and overall

- **Step Generators** — One per workload type:
  - `DockerStepGenerator` — Container volumes + image pull steps
  - `DatabaseStepGenerator` — Dump → transfer → restore steps
  - `FileStepGenerator` — File/directory transfer steps
  - `NginxStepGenerator` — Config copy → syntax verify → reload
  - `ServiceStepGenerator` — Systemd service enable + start

- **Risk Assessment** — Per-step and overall risk level:
  - Database migration → High (data loss risk)
  - Running container → High (requires downtime)
  - Large volume (>10GB) → High
  - Unknown service dependencies → Medium
  - Port conflict on target → Critical
  - 3+ high-risk steps → Critical overall
  - Blockers present → Critical overall

- **Transfer Estimator** — Size and duration estimates:
  - Network transfer: 100 MB/s (configurable)
  - Database dump: 50 MB/s (configurable)
  - rsync overhead: 1.2x (20% protocol overhead)
  - Confidence scoring (0.0–1.0) based on data availability

- **Plan Storage** — Plans persisted to SQLite with WAL mode:
  - `SavePlan` / `LoadPlan` / `ListPlans` / `DeletePlan`
  - Plans stored as JSON with metadata (source, target, step count, risk level, blockers)

- **Plan → Engine Bridge** — `BuildSteps()` converts a MigrationPlan into `[]MigrationStep`:
  - DockerVolume → DockerVolumeMigrationStep (stop → transfer volumes via io.Pipe → start)
  - DockerImage → DockerImageMigrationStep (pull image on target)
  - Database → DatabaseMigrationStep (dump → stream via io.Pipe → restore)
  - Nginx → NginxMigrationStep (copy config → verify syntax → reload)
  - Service → ServiceMigrationStep (enable + start systemd unit)
  - File/Config → transfer.FileTransferStep (from Phase 3)

If blockers are present, `CreatePlan` still returns the plan — the caller decides whether to proceed.

### 1. Pre-Flight Validation

Before executing a migration, run pre-flight checks to validate readiness:

- **SSH connectivity** — Can we reach the target server?
- **Disk space** — Does the target have enough free space? (warns if < 1GB)
- **OS compatibility** — Are the source and target distro families compatible?
- **Docker availability** — If migrating Docker, is Docker installed on the target?

Access via `GET /api/migrations/{id}/preflight` — returns errors (blocking) and warnings (non-blocking).

### 2. Plan Phase

The **Planner** connects to the source server via SSH and collects data for each selected category:

- **Packages** → `dpkg --get-selections` / `rpm -qa` / `pacman -Q`
- **Configs** → SFTP download of `/etc/` and custom paths (OS-critical files excluded)
- **Services** → `systemctl list-unit-files --state=enabled`
- **Users** → `/etc/passwd`, `/etc/group`, `crontab -l`, `ufw status`
- **Docker** → `docker ps`, `docker images`, `docker volume ls`, compose file discovery

The collected data is persisted to SQLite as migration steps.

### 3. Dry Run (Optional)

Before applying, run a **dry run** to preview what will change:

- Compares collected source data with the target's current state
- Reports per-category changes: **add** (new on target), **modify** (exists but differs), **remove** (only on target)
- No changes are made to the target server

Access via `GET /api/migrations/{id}/dryrun` or WebSocket `ws://host/ws/dryrun/{id}`.

### 4. Execute Phase

The **Engine** runs each migration step through a state machine with validated transitions:

```
Created → Planning → Backup → Snapshot → Transferring → Applying → Verifying → Committed
```

For each step:
1. **Prepare** — Validates prerequisites (disk space, connectivity, etc.)
2. **Apply** — Makes the actual changes on the target server
3. **Verify** — Confirms the changes were applied correctly
4. **Checkpoint** — Records the verified step for resume on interruption

If any phase fails, **Rollback** is called for all already-applied steps in reverse (LIFO) order.

Progress is streamed live via WebSocket to the web UI.

### 5. Rollback Phase

The **Rollback Manager** restores the target server to its pre-migration state by applying backups in reverse order. Can be triggered manually or automatically on failure.

### 6. Crash Recovery

If the application crashes mid-migration, the **Recovery Manager** detects interrupted migrations on restart:

- **Discover** — Finds all migrations in `interrupted` state
- **Resume** — Continues from the last verified checkpoint (skips already-verified steps)
- **Cancel** — Rolls back all applied steps in LIFO order and marks as `restored`

### Status Lifecycle

```
created → planning → backup → snapshot → transferring → applying → verifying → committed
                                                                 ↘ failed → rollback → restored
                          ↘ interrupted → resuming → (any state)
```

### 7. Job Engine (Phase 6)

The **Job Engine** is the top-level orchestrator that converts Phase 5 migration plans into executable background jobs:

- **Job Queue** — SQLite-backed FIFO queue. Jobs survive application restarts. Queued jobs can be cancelled before they start.
- **Worker Pool** — Configurable number of workers (default 1 — migrations are heavy, no parallel by design). Workers continuously dequeue and execute jobs.
- **Job Types**:
  - **Migration** — Loads a Phase 5 plan, builds steps via the bridge, runs the Phase 2 migration Engine
  - **Discovery** — SSH to server, run Phase 4 CollectorRunner, save snapshot
  - **CompatCheck** — Load two snapshots, run Phase 4 CheckCompatibility
- **Progress Broadcasting** — Channel-based pub/sub for real-time progress. Non-blocking broadcast (drops updates to slow subscribers instead of blocking the worker). 16-entry buffer per subscriber.
- **Job Lifecycle** — Queued → Running → (Paused ↔ Running) → Done/Failed/Cancelled
  - **Pause** — Cancels the job's context; worker finishes current step; status saved as Paused
  - **Resume** — Re-enqueues a paused job; worker picks it up and continues
  - **Cancel** — Removes from queue (if queued) or cancels context (if running)
- **Graceful Shutdown** — 30-second timeout lets running jobs finish their current step. If they don't finish in time, they are force-cancelled. All job state is persisted for resume on restart.
- **Recovery** — On restart, the engine recovers interrupted jobs: Running → Paused (manual resume), Queued → stays queued (auto-processed).

---

## Testing

```bash
# Run all tests
make test

# Or with Go directly (with race detector)
go test ./... -race -v

# Run a specific package
go test ./internal/mod/discovery/ -race -v
go test ./internal/mod/migration/ -race -v
go test ./internal/mod/transfer/ -race -v
go test ./internal/mod/ssh/ -race -v

go test ./internal/mod/planner/ -race -v

go test ./internal/jobengine/ -race -v
```

All 220+ tests pass across 11 packages with `-race` detector:

| Package | Tests | Description |
|---------|-------|-------------|
| `internal/shared` | Crypto | encrypt/decrypt, key derivation |
| `internal/db` | Database | open, migrations |
| `internal/mod/auth` | Auth | service, handler |
| `internal/mod/ssh` | SSH | client, pool (concurrency, idle cleanup), keypair, known-hosts |
| `internal/mod/server` | Server | model, repo, service, handler |
| `internal/mod/discovery` | 35 tests | 8 collectors, collector runner, dependency graph, compatibility checker, snapshot store |
| `internal/mod/discoverystep` | Steps | DiscoveryStep, CompatibilityStep |
| `internal/mod/planner` | 43 tests | Migration planner, step generators, risk assessor, estimator, plan store, bridge |
| `internal/mod/transfer` | Transfer | SCP strategy, rsync strategy, checksum verifier, directory transfer, file/directory transfer steps |
| `internal/mod/migration` | Migration | distro, packages, configs, services, users, docker, planner, executor, rollback, dry run, diff, pre-flight, state machine, engine, recovery, handler |
| `internal/jobengine` | 31 tests | Job queue, store, progress broadcaster, shutdown manager, engine submit/cancel/pause/resume, concurrent jobs, recovery after restart, integration |

Tests run automatically on every push and pull request via GitHub Actions.

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.24, net/http, gorilla/websocket |
| Database | SQLite (modernc.org/sqlite — pure Go, no CGO) |
| SSH | golang.org/x/crypto/ssh, github.com/pkg/sftp |
| Transfer | SCP/SFTP, rsync over SSH |
| Frontend | SvelteKit 2, TypeScript, TailwindCSS |
| Icons | lucide-svelte |
| Encryption | AES-256-GCM (Go crypto) |
| Build | Make, Vite, go:embed |

---

## Security

- **Credentials at rest** — Server passwords and SSH keys are encrypted with AES-256-GCM using a key derived from your master password via Argon2id (64MB memory, 3 iterations, 2 parallelism). Legacy PBKDF2 hashes are still verified for backward compatibility. The salt is stored atomically with the encrypted credential bundle to prevent inconsistent unlock states.
- **Session-based auth** — Bearer token authentication for all API routes. WebSocket endpoints validate tokens via query parameter. Tokens are generated using cryptographically secure random bytes.
- **CSRF protection** — Content-Type validation middleware blocks state-changing requests (POST/PUT/DELETE/PATCH) that don't have `application/json` Content-Type, preventing cross-origin form submissions.
- **Security headers** — X-Content-Type-Options, X-Frame-Options, X-XSS-Protection, Referrer-Policy, and Content-Security-Policy headers on all responses.
- **SSH key management** — Meshium auto-generates an Ed25519 keypair for connecting to servers. Per-server key management with AddServerKey/GetServerKey/RemoveServerKey.
- **SSH bastion** — Connections can be tunneled through a bastion/jump host for firewalled targets. Bastion host keys are verified independently.
- **Known hosts** — SSH host keys are auto-accepted on first connection (like `ssh -o StrictHostKeyChecking=accept-new`) and verified on subsequent connections to prevent MITM attacks.
- **Config exclusion** — 20 OS-critical file paths (`/etc/shadow`, `/etc/passwd`, SSH host keys, etc.) are automatically excluded from migration to prevent breaking the target server.
- **SSH pool limits** — Maximum 10 concurrent SSH connections (configurable) to prevent resource exhaustion. Stale connections are swept by a background goroutine.
- **Session management** — The web UI locks after inactivity, requiring password re-entry. Invalid session tokens are rejected; missing tokens are allowed when the app is unlocked to prevent reload loops.
- **Error handling** — Internal errors are logged with structured JSON logging; 5xx responses return generic messages to avoid leaking internal details.

## Troubleshooting

- **`cipher: message authentication failed` during unlock** — This usually means the `pbkdf2_salt` is missing or empty, or the credential bundle was written inconsistently. Re-run setup so the salt and encrypted values are written together.
- **Snapshot 404 errors** — Discovery data is only available after a server has been discovered. Run discovery first so the shared snapshot cache has data to serve.
- **Web page keeps reloading** — If the app is unlocked but the browser has no session token (e.g., after server restart), the frontend should not enter a redirect loop. This was fixed in v1.3.0 — the middleware now allows API access when unlocked without requiring a token.
- **Migration not found vs API error** — The migration detail page now distinguishes 404 ("not found") from other API errors, showing the actual error message instead of always saying "not found".

---

## License

MIT — see [LICENSE](LICENSE) file for details.

---

<div align="center">

**Built with Go, Svelte, and a lot of SSH.**

</div>
