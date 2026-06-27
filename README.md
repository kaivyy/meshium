<div align="center">

# Meshium

### Server Migration Engine for Linux

Migrate packages, configurations, services, and users across Linux servers — securely, over SSH, with a live web dashboard.

[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![SvelteKit](https://img.shields.io/badge/SvelteKit-2.x-FF3E00?style=flat-square&logo=svelte)](https://svelte.dev)
[![License](https://img.shields.io/badge/License-MIT-22c55e?style=flat-square)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-85%20passing-22c55e?style=flat-square)](#testing)

</div>

---

## Overview

Meshium is a self-hosted server migration tool that lets you replicate the configuration of one Linux server onto another — all through a clean web interface. Connect your servers via SSH, select what to migrate (packages, config files, services, users), and watch the progress in real-time.

No agents. No daemons on target machines. Just SSH.

### What It Migrates

| Category | What Gets Transferred | Supported Distros |
|----------|----------------------|-------------------|
| **Packages** | Installed package list (install missing on target) | apt, dnf, pacman, apk, zypper |
| **Config Files** | Files from `/etc/` and custom paths | All (via SFTP) |
| **Services** | Enabled systemd units | systemd-based distros |
| **Users & Security** | Users, groups, cron jobs, firewall rules | All (via SSH commands) |

### Key Features

- **Zero-Agent Architecture** — Everything runs over SSH. No software to install on target machines.
- **Live WebSocket Progress** — Watch each migration step unfold in real-time through the web UI.
- **Automatic Rollback** — Every migration creates backups. If something fails, Meshium rolls back automatically.
- **Multi-Distro Support** — Detects the source distro and translates package names for the target distro.
- **Secure by Design** — AES-256-GCM encrypted credentials at rest, SSH key management, known-hosts verification.
- **Single Binary** — Frontend is embedded in the Go binary. Deploy with one file.
- **SQLite Storage** — No external database needed. Everything in one file.

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
                    │  │           SSH Connection Pool         │  │
                    │  └────────────────┬────────────────────┘  │
                    │                   │                        │
                    │  ┌────────────────┴────────────────────┐  │
                    │  │         Migration Engine              │  │
                    │  │  ┌────────┐ ┌────────┐ ┌──────────┐  │  │
                    │  │  │ Planner│ │Executor│ │ Rollback │  │  │
                    │  │  └────────┘ └────────┘ └──────────┘  │  │
                    │  │  ┌────────────────────────────────┐  │  │
                    │  │  │ Category Registry               │  │  │
                    │  │  │ packages · configs · services  │  │  │
                    │  │  │ users · distro adapter          │  │  │
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
│   │       ├── configs.go    # Config file collector/applier
│   │       ├── services.go   # Systemd service collector/applier
│   │       ├── users.go      # User/group/cron/firewall collector/applier
│   │       ├── planner.go    # Collects from source, persists plan
│   │       ├── executor.go   # Applies plan to target with backups
│   │       ├── rollback.go   # Restores from backups in reverse
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

### WebSocket Endpoints

| Path | Description |
|------|-------------|
| `ws://host/ws/plan` | Plan a migration (send `PlanRequest`, receive progress) |
| `ws://host/ws/migrate/{id}` | Execute a migration (receive live progress) |
| `ws://host/ws/migrate/{id}/rollback` | Rollback a migration (receive live progress) |
| `ws://host/ws/discovery/{id}` | Connect to server & stream discovery steps |

---

## How Migration Works

### 1. Plan Phase

The **Planner** connects to the source server via SSH and collects data for each selected category:

- **Packages** → `dpkg --get-selections` / `rpm -qa` / `pacman -Q`
- **Configs** → SFTP download of `/etc/` and custom paths
- **Services** → `systemctl list-unit-files --state=enabled`
- **Users** → `/etc/passwd`, `/etc/group`, `crontab -l`, `ufw status`

The collected data is persisted to SQLite as migration steps.

### 2. Execute Phase

The **Executor** connects to the target server and for each category:

1. **Backs up** the current state (installs, copies, snapshots)
2. **Applies** the collected data from the source
3. If any step fails → **auto-rollback** kicks in

Progress is streamed live via WebSocket to the web UI.

### 3. Rollback Phase

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

All 85 tests pass across 7 packages:

| Package | Tests |
|---------|-------|
| `internal/shared` | Crypto (encrypt/decrypt, key derivation) |
| `internal/db` | Database open, migrations |
| `internal/mod/auth` | Auth service, handler |
| `internal/mod/ssh` | Client, pool, keypair, known-hosts |
| `internal/mod/server` | Model, repo, service, handler |
| `internal/mod/discovery` | Collector, service, handler |
| `internal/mod/migration` | Distro, packages, configs, services, users, planner, executor, rollback, handler |

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
- **Known hosts** — SSH host key verification is enforced. New hosts are stored and verified on subsequent connections.
- **Session management** — The web UI locks after inactivity, requiring password re-entry.

---

## License

MIT — see [LICENSE](LICENSE) file for details.

---

<div align="center">

**Built with Go, Svelte, and a lot of SSH.**

</div>
