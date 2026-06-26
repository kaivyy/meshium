# Meshium Foundation: SSH Core + Server Manager + Connection Test

**Date:** 2026-06-26
**Status:** Approved
**Sub-project:** 1 of 12+ (Foundation Layer)

## Overview

This spec covers the foundation layer of Meshium (Infrastructure Migration Manager). The foundation provides SSH connectivity, server management, and connection testing. All future subsystems (Docker Manager, Migration Engine, Monitoring, etc.) depend on this layer.

## Scope

**In scope:**
- Go backend with modular domain-driven architecture
- Svelte frontend with SvelteKit
- SSH connection management (pool, key-based auth, password auth)
- Server CRUD with encrypted credential storage
- Connection test (system info collection via SSH)
- Master password / AES-256 credential encryption
- Known hosts verification
- App SSH key pair generation and auto-install

**Out of scope (future sub-projects):**
- Auto Discovery (full: Docker, databases, services, firewall, SSL, etc.)
- Remote Terminal
- File Manager
- Docker Manager
- Database Manager
- Migration Engine (planner, executor, clone, live migration)
- Health Check, Rollback, Snapshot
- Monitoring & Alerts
- Scheduler & Logging
- Tailscale Manager
- Plugin System
- Cloud Provider Support
- Desktop packaging (Wails/Tauri)

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Platform | Web first (browser-based) | Simpler to build and iterate; desktop packaging later |
| Backend language | Go 1.22+ | Per PRD; excellent SSH library, single binary |
| Frontend framework | SvelteKit + shadcn/ui + TailwindCSS | Per PRD |
| Database | SQLite | Per PRD; embedded, no server needed |
| SSH library | golang.org/x/crypto/ssh | Pure Go, well-maintained, supports all auth methods |
| API pattern | REST for CRUD + WebSocket for realtime | Conventional, flexible |
| Credential storage | Master password + AES-256 | Per PRD; OS keychain deferred (web first) |
| SSH key strategy | One key pair per user | Simpler to manage; auto-install on first password login |
| System info collection | Hybrid (SSH commands + scripts) | Standard commands for basics, scripts for complex discovery |
| Architecture | Modular domain-driven | Clear boundaries for future subsystem growth |

## Project Structure

```
meshium/
├── cmd/
│   └── server/
│       └── main.go              # Entry point, starts HTTP server
├── internal/
│   ├── mod/
│   │   ├── server/              # Server CRUD + credential storage
│   │   │   ├── handler.go       # REST: /api/servers
│   │   │   ├── service.go       # Business logic
│   │   │   ├── repo.go          # SQLite queries
│   │   │   └── model.go         # Server struct, DTOs
│   │   ├── ssh/                 # SSH connection management
│   │   │   ├── pool.go          # Connection pool
│   │   │   ├── keypair.go       # Generate, load, install key pair
│   │   │   ├── client.go        # SSH client wrapper
│   │   │   └── model.go
│   │   └── discovery/           # Connection test + system info
│   │       ├── handler.go       # WS: /ws/connect/:serverId
│   │       ├── service.go       # Orchestrates collection
│   │       ├── collector.go     # Runs commands via SSH
│   │       └── model.go         # SystemInfo struct
│   └── shared/
│       ├── crypto.go            # AES-256 encrypt/decrypt, bcrypt, PBKDF2
│       ├── config.go            # App config, DB path, key paths
│       └── types.go             # Shared types
├── web/                         # Svelte frontend
│   ├── src/
│   │   ├── lib/
│   │   │   ├── components/      # shadcn/ui components
│   │   │   ├── stores/          # Svelte stores
│   │   │   └── api/            # REST + WS client
│   │   ├── routes/
│   │   │   ├── setup/           # First launch: set master password
│   │   │   ├── login/           # Unlock app
│   │   │   ├── servers/         # Server list + detail + new + edit
│   │   │   └── settings/        # SSH key management
│   │   └── app.html
│   ├── package.json
│   └── svelte.config.js
├── docs/
│   ├── prd.md
│   └── superpowers/specs/
├── go.mod
├── go.sum
└── Makefile
```

## Tech Stack

- **Backend**: Go 1.22+, `golang.org/x/crypto/ssh`, `modernc.org/sqlite` (pure Go SQLite), `gorilla/websocket`
- **Frontend**: SvelteKit, shadcn/ui (Svelte port), TailwindCSS
- **Database**: SQLite (single file, stored in user data dir)
- **Build**: Go embeds `web/build/` into binary via `//go:embed`

## Database Schema

```sql
-- App configuration (master password hash, encrypted SSH keys)
CREATE TABLE app_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Keys: master_password_hash, ssh_key_private_encrypted, ssh_key_public

-- Servers
CREATE TABLE servers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT,
    host        TEXT NOT NULL,
    port        INTEGER NOT NULL DEFAULT 22,
    username    TEXT NOT NULL,
    password    TEXT,           -- AES-256 encrypted
    ssh_key     TEXT,           -- AES-256 encrypted (custom key, if not using app key)
    passphrase  TEXT,           -- AES-256 encrypted
    tags        TEXT,           -- JSON array: ["web", "db"]
    environment TEXT,           -- production | staging | development
    region      TEXT,           -- indonesia | singapore | japan
    icon        TEXT,
    color       TEXT,
    favorite    INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Cached connection test results
CREATE TABLE server_info (
    server_id       INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    ssh_status      TEXT,
    latency_ms      INTEGER,
    cpu_model       TEXT,
    cpu_cores       INTEGER,
    ram_total_mb    INTEGER,
    disk_total_gb   REAL,
    kernel          TEXT,
    architecture    TEXT,
    os              TEXT,
    virtualization  TEXT,
    provider        TEXT,
    public_ip       TEXT,
    private_ip      TEXT,
    timezone        TEXT,
    hostname        TEXT,
    raw_data        TEXT,       -- Full JSON of discovery output
    last_checked    DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- SSH known hosts
CREATE TABLE known_hosts (
    server_id   INTEGER REFERENCES servers(id) ON DELETE CASCADE,
    host_key    TEXT NOT NULL,
    host        TEXT NOT NULL,
    port        INTEGER NOT NULL,
    verified    INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(host, port)
);
```

## SSH Core

### Connection Pool

```
ConnectionPool
├── connections: map[serverID]*SSHClient
├── maxIdle: 10 min (close idle connections)
├── maxLifetime: 30 min (reconnect)
├── keepalive: 30s interval on idle connections
└── methods:
    ├── Get(serverID) → *SSHClient (reuse if alive, create if not)
    ├── Close(serverID) → error
    └── CloseAll() → error
```

Thread-safe with `sync.RWMutex`.

### SSH Client

```go
type SSHClient struct {
    serverID  int
    conn      *ssh.Client
    createdAt time.Time
    lastUsed  time.Time
}

func (c *SSHClient) Exec(cmd string) (stdout, stderr string, exitCode int, err error)
func (c *SSHClient) ExecStream(cmd string, onOutput func(line string)) error
func (c *SSHClient) Upload(src io.Reader, remotePath string) error
func (c *SSHClient) Download(remotePath string, dst io.Writer) error
func (c *SSHClient) Close() error
```

### Key Pair Management

**First launch:**
1. User sets master password
2. App generates RSA 4096 key pair
3. Private key encrypted with AES-256 (key derived from master password via PBKDF2)
4. Stored in app_config table

**"Install SSH Public Key?" flow:**
1. User connects to server with password
2. App asks: "Install SSH Public Key?"
3. If YES: `mkdir -p ~/.ssh && echo "<pubkey>" >> ~/.ssh/authorized_keys && chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys`
4. Next connection uses key auth instead of password

### Authentication Flow

```
Connect(serverID):
1. Load server credentials from DB (decrypt with master password)
2. If server has SSH key → use key auth
3. Else if server has password → use password auth
   └── After connect: offer to install app public key
4. SSH client config:
   - HostKeyCallback: known_hosts verification
   - Timeout: 10s connect, 30s command
   - Ciphers: aes256-gcm@openssh.com, chacha20-poly1305@openssh.com
```

### Host Key Verification

1. First connect: fingerprint shown to user → accept/reject
2. Accepted: stored in `known_hosts` table
3. Subsequent: verified against stored key
4. Mismatch: connection refused, user warned (possible MITM)

## Server Manager API

### REST Endpoints

```
GET    /api/servers              → list (with filters: ?environment=&region=&tag=&q=)
GET    /api/servers/:id          → get single
POST   /api/servers              → create
PUT    /api/servers/:id          → update
DELETE /api/servers/:id          → delete
PATCH  /api/servers/:id/favorite → toggle favorite

POST   /api/auth/setup           → set master password (first launch)
POST   /api/auth/unlock          → verify master password, unlock
POST   /api/auth/lock            → lock app
GET    /api/auth/status          → check locked/unlocked

GET    /api/ssh-key/public        → get app public key
POST   /api/ssh-key/regenerate    → regenerate key pair
```

### WebSocket Endpoints

```
/ws/connect/:serverId     → connect + stream connection test results
```

### Request/Response

Server create request includes credentials in plaintext (encrypted before storage). All responses strip credential fields. Master password sent over request body, kept in memory only after unlock.

## Connection Test & Discovery

### Connection Test Commands

| Info | Command |
|---|---|
| Hostname | `hostname` |
| OS | `cat /etc/os-release \| grep PRETTY_NAME` |
| Kernel | `uname -r` |
| Architecture | `uname -m` |
| CPU Model | `lscpu \| grep "Model name"` |
| CPU Cores | `nproc` |
| RAM Total | `free -m \| awk '/Mem:/{print $2}'` |
| Disk Total | `df -BG / \| awk 'NR==2{print $2}'` |
| Virtualization | `systemd-detect-virt 2>/dev/null \|\| echo` |
| Public IP | `curl -s ifconfig.me` (with timeout) |
| Private IP | `hostname -I \| awk '{print $1}'` |
| Timezone | `timedatectl \| grep "Time zone"` |

### WebSocket Streaming

Results stream as they complete:

```json
{"step": "ssh", "status": "success", "latency_ms": 45}
{"step": "hostname", "status": "success", "value": "web-01"}
{"step": "os", "status": "success", "value": "Ubuntu 22.04 LTS"}
...
{"step": "done", "status": "complete"}
```

Errors per-step are non-fatal — continue collecting other info. Results cached to `server_info` table after completion.

## Frontend

### Pages

| Route | Purpose |
|---|---|
| `/setup` | First launch: set master password |
| `/login` | Unlock app with master password |
| `/servers` | Server list (grid/list, filters, search) |
| `/servers/:id` | Server detail (connection test, system info) |
| `/servers/new` | Add server form |
| `/servers/:id/edit` | Edit server |
| `/settings` | SSH key management, app settings |

### Server List Page

- Grid or list view (toggle)
- Filter bar: search, environment, region, tags
- Sort by: name, last connected, favorite
- Server card: name, host, environment badge, region badge, favorite star, connection status
- Click card → server detail

### Server Detail Page

- Header: name, description, host:port, username
- "Connect" button → triggers WebSocket connection test
- Results stream in as progress cards
- After completion: system info summary
- Tabs (future): Overview | Terminal | Files | Docker | Database | Migration
- Only "Overview" tab active in this scope

### State Management

- Svelte stores: `authStore` (locked/unlocked), `serverStore` (server list, selected)
- WebSocket managed per-component (connect on mount, close on destroy)

## Security

### Credential Encryption

1. Master password → bcrypt hash → stored in `app_config`
2. Master password (plaintext, in memory) → PBKDF2 → AES-256 key
3. Server credentials encrypted with AES key before SQLite write
4. On unlock: master password → PBKDF2 → AES key (in memory only)
5. On lock: AES key zeroed, master password cleared

### Session Management

- In-memory state: locked/unlocked
- Auto-lock after 30 min inactivity (configurable)
- On auto-lock: all SSH connections closed, keys cleared from memory

## Error Handling

### SSH Errors

| Error | Response |
|---|---|
| Connection refused | "Server unreachable. Check host/port." |
| Auth failed | "Authentication failed. Check credentials." |
| Host key mismatch | "WARNING: Host key changed. Possible MITM." |
| Timeout | "Connection timed out after 10s." |
| Network error | "Network error: <details>" |

### API Error Format

```json
{
  "error": "string",
  "code": "AUTH_FAILED" | "SERVER_NOT_FOUND" | "VALIDATION_ERROR" | "SSH_ERROR" | "INTERNAL"
}
```

### Discovery Errors

- Command not found → skip, mark "unknown", continue
- Command timeout → skip, mark "timeout", continue
- Permission denied → skip, mark "no_access", continue
- SSH disconnected → abort, send error to WS client

## Testing Strategy

### Backend (Go)

- Unit tests: crypto, repo, service layers
- SSH tests: mock SSH server (`golang.org/x/crypto/ssh` test server)
- API tests: `httptest` with in-memory SQLite
- Integration: test SSH against real server (manual, skip in CI)

### Frontend (Svelte)

- Component tests: vitest + @testing-library/svelte
- Store tests: unit test auth/server stores
- E2E: playwright (server list, add server, connect)
