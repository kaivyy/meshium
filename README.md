<div align="center">

# Meshium

### Server Migration Engine for Linux

Migrate packages, configurations, services, and users across Linux servers — securely, over SSH, with a live web dashboard.

[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![SvelteKit](https://img.shields.io/badge/SvelteKit-2.x-FF3E00?style=flat-square&logo=svelte)](https://svelte.dev)
[![CI](https://img.shields.io/badge/CI-GitHub%20Actions-2088FF?style=flat-square&logo=githubactions)](https://github.com/kaivyy/meshium/actions)
[![License](https://img.shields.io/badge/License-MIT-22c55e?style=flat-square)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-90%2B%20passing-22c55e?style=flat-square)](#testing)

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
- **Dry Run Mode** — Preview what will change before applying. See per-category additions, modifications, and removals.
- **Server Diff** — Compare source and target servers side-by-side across all categories before migrating.
- **Pre-Flight Validation** — Check disk space, OS compatibility, Docker availability, and SSH connectivity before execution.
- **Docker Migration** — Migrate containers, images, volumes, and compose files across servers.
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
                    │  │         Migration Engine              │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │ Planner│ │Executor│ │ Rollback │  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │Dry Run │ │  Diff  │ │Pre-Flight│  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────────────────────────────┐  │  │
                    │  │  │ Category Registry               │  │  │
                    │  │  │ packages · configs · services   │  │  │
                    │  │  │ users · docker · distro adapter │  │  │
                    │  │  └────────────────────────────────┘  │  │
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
│   │   ├── discovery/        # SSH connect, system info, WebSocket
│   │   └── migration/        # The migration engine
│   │       ├── model.go      # Types, interfaces, status constants
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
│   └── ...
├── web/                     # SvelteKit frontend
│   ├── src/
│   │   ├── lib/
│   │   │   ├── api/         # REST + WebSocket clients
│   │   │   ├── stores/       # Svelte stores (auth, servers, migrations)
│   │   │   └── components/  # Sidebar, etc.
│   │   └── routes/          # Pages (servers, migrations, settings)
│   └── ...
├── docs/                    # Design specs and plans
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

### REST Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/auth/status` | Check auth status |
| `POST` | `/api/auth/setup` | Initial password setup |
| `POST` | `/api/auth/unlock` | Unlock with password |
| `POST` | `/api/auth/lock` | Lock session |
| `GET` | `/api/servers` | List all servers |
| `POST` | `/api/servers` | Add a server |
| `GET` | `/api/servers/{id}` | Get server details |
| `PUT` | `/api/servers/{id}` | Update server |
| `DELETE` | `/api/servers/{id}` | Delete server |
| `POST` | `/api/servers/{id}/connect` | Test SSH connection |
| `GET` | `/api/servers/{id}/info` | Get system info |
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
| `ws://host/ws/discovery/{id}` | Connect to server & stream discovery steps |

---

## How Migration Works

### 0. Pre-Flight Validation

Before executing a migration, run pre-flight checks to validate readiness:

- **SSH connectivity** — Can we reach the target server?
- **Disk space** — Does the target have enough free space? (warns if < 1GB)
- **OS compatibility** — Are the source and target distro families compatible?
- **Docker availability** — If migrating Docker, is Docker installed on the target?

Access via `GET /api/migrations/{id}/preflight` — returns errors (blocking) and warnings (non-blocking).

### 1. Plan Phase

The **Planner** connects to the source server via SSH and collects data for each selected category:

- **Packages** → `dpkg --get-selections` / `rpm -qa` / `pacman -Q`
- **Configs** → SFTP download of `/etc/` and custom paths (OS-critical files excluded)
- **Services** → `systemctl list-unit-files --state=enabled`
- **Users** → `/etc/passwd`, `/etc/group`, `crontab -l`, `ufw status`
- **Docker** → `docker ps`, `docker images`, `docker volume ls`, compose file discovery

The collected data is persisted to SQLite as migration steps.

### 2. Dry Run (Optional)

Before applying, run a **dry run** to preview what will change:

- Compares collected source data with the target's current state
- Reports per-category changes: **add** (new on target), **modify** (exists but differs), **remove** (only on target)
- No changes are made to the target server

Access via `GET /api/migrations/{id}/dryrun` or WebSocket `ws://host/ws/dryrun/{id}`.

### 3. Execute Phase

The **Executor** connects to the target server and for each category:

1. **Backs up** the current state (installs, copies, snapshots)
2. **Applies** the collected data from the source
3. If any step fails → **auto-rollback** kicks in

Progress is streamed live via WebSocket to the web UI.

### 4. Rollback Phase

The **Rollback Manager** restores the target server to its pre-migration state by applying backups in reverse order. Can be triggered manually or automatically on failure.

### Status Lifecycle

```
planned → running → completed
                   ↘ failed → rolling_back → rolled_back
                                          ↘ rollback_failed
```

---

## Testing

```bash
# Run all tests
make test

# Or with Go directly
go test ./... -v

# Run a specific package
go test ./internal/mod/migration/ -v
```

All 90+ tests pass across 7 packages:

| Package | Tests |
|---------|-------|
| `internal/shared` | Crypto (encrypt/decrypt, key derivation) |
| `internal/db` | Database open, migrations |
| `internal/mod/auth` | Auth service, handler |
| `internal/mod/ssh` | Client, pool (concurrency, idle cleanup), keypair, known-hosts |
| `internal/mod/server` | Model, repo, service, handler |
| `internal/mod/discovery` | Collector, service, handler |
| `internal/mod/migration` | Distro, packages, configs, services, users, docker, planner, executor, rollback, dry run, diff, pre-flight, handler |

Tests run automatically on every push and pull request via GitHub Actions.

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.24, net/http, gorilla/websocket |
| Database | SQLite (modernc.org/sqlite — pure Go, no CGO) |
| SSH | golang.org/x/crypto/ssh, github.com/pkg/sftp |
| Frontend | SvelteKit 2, TypeScript, TailwindCSS |
| Icons | lucide-svelte |
| Encryption | AES-256-GCM (Go crypto) |
| Build | Make, Vite, go:embed |

---

## Security

- **Credentials at rest** — Server passwords and SSH keys are encrypted with AES-256-GCM using a key derived from your master password via scrypt.
- **SSH key management** — Meshium auto-generates an Ed25519 keypair for connecting to servers. You can also use password auth.
- **SSH bastion** — Connections can be tunneled through a bastion/jump host for firewalled targets.
- **Known hosts** — SSH host keys are auto-accepted on first connection (like `ssh -o StrictHostKeyChecking=accept-new`) and verified on subsequent connections to prevent MITM attacks.
- **Config exclusion** — 20 OS-critical file paths are automatically excluded from migration to prevent breaking the target server.
- **SSH pool limits** — Maximum 10 concurrent SSH connections (configurable) to prevent resource exhaustion.
- **Session management** — The web UI locks after inactivity, requiring password re-entry.

---

## License

MIT — see [LICENSE](LICENSE) file for details.

---

<div align="center">

**Built with Go, Svelte, and a lot of SSH.**

</div>
