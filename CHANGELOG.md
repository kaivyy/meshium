# Changelog

All notable changes to Meshium are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
| 1.0.0 | 2026-06-27 | Initial release — full migration engine, web UI, 85 tests |
