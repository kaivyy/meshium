# Meshium Migration Engine

**Date:** 2026-06-27
**Status:** Approved
**Sub-project:** 2 of 12+ (Migration Engine)

## Overview

The Migration Engine enables selective server migration: copy packages, configs, services, and users/security settings from a source server to a target server via SSH. The engine uses an agent-based architecture where each category has a Collector (reads from source) and an Applier (writes to target). A Distro Adapter Layer abstracts package managers and service managers across all major Linux distributions.

## Scope

**In scope:**
- Migration planner: collect data from source, generate migration plan
- Migration executor: apply plan to target, stream progress via WebSocket
- Auto-backup + rollback: backup target before applying, restore on failure
- 4 category modules: Packages, Configs, Services, Users & Security
- Distro adapter: apt (Debian), dnf/yum (RHEL), pacman (Arch), apk (Alpine), zypper (SUSE)
- Interactive wizard frontend: select source, categories, target, preview, execute
- Migration history: list, detail, manual rollback
- Global sidebar navigation in frontend

**Out of scope (future sub-projects):**
- Full clone migration (all categories at once with single click)
- File/directory copy via SFTP (standalone feature, future)
- Live migration (zero-downtime with rsync + cutover)
- Migration templates (save/reuse migration plans)
- Bulk migration (multiple targets)
- Scheduler & logging
- Monitoring & alerts

## Architecture

```
Frontend (SvelteKit)
  /migrate — Wizard: source → categories → target → preview → execute
  /migrate/:id — Live progress (WebSocket)
  /migrate/history — Migration history table
  Sidebar: Servers, Migrate, Settings, Lock

Go Backend (internal/mod/migration)
  Planner    — Collect from source, build MigrationPlan
  Executor   — Apply to target, stream progress
  RollbackMgr — Backup target before apply, restore on failure

  Category Modules (each has Collector + Applier):
    Packages  — dpkg/rpm/pacman/apk list → install via adapter
    Configs   — SFTP download from source → SFTP upload to target
    Services  — systemctl list-enabled → enable on target
    Users/Sec — passwd/group/shadow/cron/iptables → create on target

  Distro Adapter Layer:
    apt (Debian/Ubuntu) | dnf/yum (RHEL/CentOS/Alma) | pacman (Arch)
    apk (Alpine) | zypper (SUSE)
```

## Interfaces

### Collector & Applier

```go
// SSHExecuter is reused from the discovery package (internal/mod/discovery/collector.go).
// It provides: Exec(cmd) (stdout, stderr, exitCode, error), IsAlive() bool.
// The migration module also needs Upload/Download (SFTP) — these are on ssh.Client directly.

// StepCallback is reused from the discovery package: func(msg WSMessage).

// Collector reads data from the source server.
type Collector interface {
    Collect(ssh SSHExecuter) (CategoryData, error)
}

// Applier writes data to the target server.
type Applier interface {
    Backup(ssh SSHExecuter) (BackupData, error)
    Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error
    Rollback(ssh SSHExecuter, backup BackupData) error
}
```

### Distro Adapter

```go
type DistroAdapter interface {
    Detect(ssh SSHExecuter) (DistroInfo, error)
    PackageManager() string
    ListPackages() string
    InstallPackages(pkgs []string) string
    EnableService(name string) string
}

type DistroInfo struct {
    Name      string // "debian", "ubuntu", "rhel", "arch", "alpine", "opensuse"
    Family    string // "debian", "rhel", "arch", "alpine", "suse"
    Version   string
    PackageManager string // "apt", "dnf", "yum", "pacman", "apk", "zypper"
}
```

### Migration Plan

```go
type MigrationPlan struct {
    Source      discovery.SystemInfo  // reused from discovery module
    Target      discovery.SystemInfo
    Categories  []string
    Steps       []PlanStep
    Warnings    []string
    EstimatedTime string
}

type PlanStep struct {
    Category    string // "packages", "configs", "services", "users"
    Action      string // "collect", "backup", "apply"
    Description string
    ItemCount   int
}
```

### WebSocket Messages

```json
{ "step": "packages:collect", "status": "success", "value": "247 packages found" }
{ "step": "packages:backup", "status": "success", "value": "backup saved" }
{ "step": "packages:apply", "status": "progress", "value": "Installing 120/247: nginx" }
{ "step": "packages:apply", "status": "success", "value": "247 packages installed" }
{ "step": "configs:apply", "status": "error", "error": "Permission denied: /etc/nginx/nginx.conf" }
{ "step": "rollback", "status": "progress", "value": "Restoring backup..." }
{ "step": "rollback", "status": "success" }
{ "step": "done", "status": "complete" }
{ "step": "done", "status": "failed", "error": "Migration failed, rollback complete" }
```

## REST API

| Method | Path | Description |
|---|---|---|
| POST | `/api/migrations/plan` | Create migration plan (collect from source, preview) |
| POST | `/api/migrations` | Execute migration (create record, start) |
| GET | `/api/migrations` | List migration history |
| GET | `/api/migrations/:id` | Get migration detail + steps |
| DELETE | `/api/migrations/:id` | Delete migration record |
| POST | `/api/migrations/:id/rollback` | Manual rollback |
| WS | `/ws/migrate/:migrationId` | Stream migration progress |

## Database Schema

```sql
CREATE TABLE migrations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id    INTEGER NOT NULL REFERENCES servers(id),
    target_id    INTEGER NOT NULL REFERENCES servers(id),
    categories   TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    plan         TEXT,
    error        TEXT,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE TABLE migration_steps (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    category     TEXT NOT NULL,
    action       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    output       TEXT,
    error        TEXT,
    started_at   DATETIME,
    completed_at DATETIME
);

CREATE TABLE migration_backups (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    server_id    INTEGER NOT NULL REFERENCES servers(id),
    category     TEXT NOT NULL,
    backup_path  TEXT NOT NULL,
    backup_type  TEXT NOT NULL,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Category Modules

### 1. Packages

**Collector:** Detect distro, run distro-specific list command (`dpkg -l`, `rpm -qa`, `pacman -Q`, `apk info -v`, `zypper se --installed-only`). Return list of package names.

**Applier:** Detect target distro, install packages via adapter. Skip packages already installed. If distro differs from source, attempt package name mapping (e.g., `nginx` is same across distros, but `python3-dev` → `python3-devel` on RHEL). Log unmapped packages as warnings.

**Backup:** Save `dpkg -l` / `rpm -qa` output to `/tmp/meshium-backup/migration-N/packages/installed.txt`.

**Rollback:** Remove packages that were installed by the migration (diff against backup list).

### 2. Configs

**Collector:** SFTP download files from selected paths (`/etc/nginx/`, `/etc/ssh/`, custom). Return file map: path → content.

**Applier:** SFTP upload to target. Backup original files first to `/tmp/meshium-backup/migration-N/configs/`. Preserve permissions and ownership.

**Backup:** Tarball original config files at selected paths.

**Rollback:** Restore tarball, overwriting migrated files.

### 3. Services

**Collector:** `systemctl list-unit-files --state=enabled` → list of enabled service names.

**Applier:** Enable same services on target. Skip services not installed on target (log as warning). Start services after enabling.

**Backup:** Save list of currently enabled services on target.

**Rollback:** Disable services that were enabled by migration, restore original enabled set.

### 4. Users & Security

**Collector:**
- Users: parse `/etc/passwd` for non-system users (UID >= 1000)
- Groups: parse `/etc/group` for non-system groups
- Cron jobs: `crontab -l` for each user + `/etc/crontab`
- Firewall: `iptables-save` or `ufw status numbered`

**Applier:**
- Create users with `useradd` if not exist, set password from `/etc/shadow`
- Create groups with `groupadd` if not exist
- Import cron jobs via `crontab`
- Apply firewall rules via `iptables-restore` or `ufw`

**Backup:** Save `/etc/passwd`, `/etc/group`, `/etc/shadow`, crontab list, firewall rules.

**Rollback:** Restore original passwd/group/shadow, remove added users/groups, restore cron, restore firewall rules.

## Distro Adapter

Auto-detect distro on both source and target via `/etc/os-release`. Select adapter based on `ID` field:

| Distro ID | Family | Package Manager | List Command | Install Command |
|---|---|---|---|---|
| debian, ubuntu, linuxmint | debian | apt | `dpkg -l` | `apt-get install -y` |
| rhel, centos, rocky, alma, fedora | rhel | dnf/yum | `rpm -qa` | `dnf install -y` |
| arch, manjaro | arch | pacman | `pacman -Q` | `pacman -S --noconfirm` |
| alpine | alpine | apk | `apk info -v` | `apk add` |
| opensuse, suse | suse | zypper | `zypper se --installed-only` | `zypper install -y` |

If source and target distros differ, package name mapping is attempted for common packages. Unmapped packages are logged as warnings and skipped.

## Frontend

### Sidebar Navigation

Global sidebar visible on all authenticated pages (hidden on `/setup` and `/login`):

```
┌──────────────┐
│  Meshium     │
│              │
│  Servers     │ → /
│  Migrate     │ → /migrate
│  Settings    │ → /settings
│              │
│  ────────── │
│  Lock        │ → POST /api/auth/lock, redirect to /login
└──────────────┘
```

### Migration Wizard (`/migrate`)

4-step wizard:
1. **Select Source Server** — dropdown from `GET /api/servers`
2. **Select Categories** — checkboxes: Packages, Configs, Services, Users & Security. For Configs, optional path input.
3. **Select Target Server** — dropdown (excludes source)
4. **Preview Plan** — `POST /api/migrations/plan` → display steps, warnings, estimated time. "Start Migration" button.

### Migration Progress (`/migrate/:id`)

WebSocket connection to `/ws/migrate/:migrationId`. Per-category progress cards with:
- Step status icons (pending, running, success, error)
- Progress bar for apply steps
- Error messages with rollback status
- Cancel and Rollback buttons

### Migration History (`/migrate/history`)

Table: ID, Source → Target, Categories, Status, Date, Actions (view, rollback).

## Error Handling

- **Non-fatal errors** (package not found, service not installed on target): log as warning, continue migration
- **Fatal errors** (SSH disconnect, permission denied): abort current category, trigger rollback for completed categories, mark migration as failed
- **Rollback failure**: mark migration as "failed_rollback" and alert user to manual intervention
- **WebSocket disconnect**: migration continues in background, user can reconnect to `/ws/migrate/:id` to resume viewing
- **Distro mismatch**: warn user in preview, attempt package name mapping, skip unmapped

## File Structure

```
internal/mod/migration/
├── model.go           # MigrationPlan, PlanStep, CategoryData, BackupData, WSMessage types
├── repo.go            # SQLite CRUD for migrations, steps, backups
├── planner.go         # Collect from source, build plan
├── executor.go        # Apply to target, orchestrate categories
├── rollback.go        # Backup and rollback logic
├── distro.go          # Distro detection and adapter selection
├── distro_test.go     # Distro adapter tests
├── packages.go        # Packages collector + applier
├── configs.go         # Configs collector + applier
├── services.go        # Services collector + applier
├── users.go           # Users & security collector + applier
├── handler.go         # REST handlers + WebSocket handler
├── handler_test.go    # Handler tests
├── planner_test.go    # Planner tests
├── executor_test.go   # Executor tests
└── rollback_test.go   # Rollback tests

web/src/
├── lib/
│   ├── api/
│   │   └── migration.ts   # Migration API client
│   └── stores/
│       └── migration.ts   # Migration Svelte store
├── routes/
│   └── migrate/
│       ├── +page.svelte          # Wizard
│       ├── [id]/
│       │   └── +page.svelte      # Live progress
│       └── history/
│           └── +page.svelte      # History table
└── components/
    └── Sidebar.svelte            # Global sidebar component
```

## Testing Strategy

- **Unit tests:** Each collector and applier tested independently with mock SSH executer
- **Distro adapter tests:** Detect logic for each distro, command generation for each package manager
- **Integration tests:** Planner + executor with mock SSH servers (similar to SSH client tests)
- **Handler tests:** REST endpoints with httptest, WebSocket with gorilla/websocket test helpers
- **Rollback tests:** Verify backup creation, restore on failure, partial rollback scenarios
