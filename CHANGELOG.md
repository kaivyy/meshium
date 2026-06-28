# Changelog

All notable changes to Meshium are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
| 1.2.0 | 2026-06-28 | Mobile bottom navbar, SSH host key auto-accept, API null→[] fix |
| 1.1.0 | 2026-06-28 | Docker migration, dry run, diff view, bastion/jump host, pre-flight validation, config exclusion, CI/CD, SSH pool concurrency |
| 1.0.0 | 2026-06-27 | Initial release — full migration engine, web UI, 85 tests |
