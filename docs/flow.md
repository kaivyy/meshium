# Meshium — User Flow: Assisted Server Migration

## Overview

Meshium provides a guided 11-step wizard for performing assisted server migrations. The wizard walks the user through discovery, risk assessment, planning, provisioning, execution, and post-cutover observation — with real-time monitoring and rollback of the changes the pipeline itself applied.

> **Important — cutover is not automated.** The live pipeline transfers data and configuration and sets up database replication (when enabled), but it does **not** automatically switch production traffic from the source to the target. The traffic-switch stage records a **manual-cutover checkpoint** and instructs the operator to move traffic themselves (DNS / reverse proxy / load balancer) and then verify. Meshium does not advertise "zero-downtime"; see `docs/architecture.md` ("Cutover & Downtime Honesty").

---

## Prerequisites

Before starting a migration, the user must have:

1. **Source server** registered in Meshium (the server to migrate FROM)
2. **Target server** registered in Meshium (the server to migrate TO)
3. SSH credentials configured for both servers

---

## End-to-End Flow

### Phase 1: Server Registration

```
Dashboard → Servers → Add Server
```

1. User navigates to **Servers** page
2. Clicks **Add Server**
3. Enters server details: name, host, SSH port, username, auth method (password/key)
4. Meshium tests the SSH connection
5. Repeat for the second server

### Phase 2: Create Migration

```
Dashboard → Migrations → New Migration
```

URL: `/migrations/new`

| Step | Action |
|------|--------|
| **1. Select Source** | Choose the server to migrate FROM |
| **2. Select Target** | Choose the server to migrate TO (must differ from source) |
| **3. Choose Categories** | Select what to migrate: packages, configs, services, users, docker |
| **4. Review & Create** | Confirm selections → Click **Create Migration Plan** |

After the plan is created via WebSocket (`/ws/plan`), the backend returns the migration ID. The user is **automatically redirected** to the pipeline wizard.

### Phase 3: Pipeline Wizard

URL: `/migrations/{id}/pipeline`

This is the primary migration interface — a guided 11-step wizard with validation gates, real-time monitoring, and rollback capabilities.

---

## The 11 Wizard Steps

### Step 1: Discovery

**Purpose:** Discover services, containers, databases, and volumes on both source and target servers.

| Element | Description |
|---------|-------------|
| Action | Click **Run Discovery** |
| Shows | Source/target server info, discovered services, containers, databases |
| Gate | Discovery must complete successfully to proceed |

### Step 2: Compatibility

**Purpose:** Check source/target compatibility for migration blockers.

| Element | Description |
|---------|-------------|
| Action | Click **Run Compatibility Check** |
| Shows | A **live per-check checklist** fills as each of the 11 serial checks runs (streamed over `/ws/compatibility/{id}`, e.g. "architecture 1/11… ✓"), then the final results with severity badges (info / warning / high / critical) |
| Gate | **No critical failures** allowed to proceed |
| Checks | Architecture, RAM, disk, Docker, kernel, OS, package manager, OpenSSL, storage driver, SELinux, ports, timezone |
| Persistence | Results are written to the DB (`verification_result`) and restored on refresh / browser close — the checklist does not reset |

### Step 3: Risk Assessment

**Purpose:** Evaluate migration risk score and estimate downtime.

| Element | Description |
|---------|-------------|
| Action | Click **Run Risk Assessment** |
| Shows | Risk score (0–100), risk class (low/medium/high/critical), estimated downtime, rollback complexity |
| Gate | Assessment must complete to proceed |
| Factors | Data size, database size, container count, replication availability, network speed, resource mismatch, compatibility issues |

### Step 4: Plan

**Purpose:** Review the migration plan and configure options.

| Element | Description |
|---------|-------------|
| Action | Select categories, configure migration settings |
| Shows | Category checkboxes, traffic provider, replication mode, sync method, cutover options, auto-rollback settings |
| Gate | At least one category must be selected |

**Configuration options:**

| Option | Default | Description |
|--------|---------|-------------|
| Traffic Provider | — | Cloudflare / Nginx / Traefik / HAProxy / Caddy / Docker |
| Replication Enabled | true | Enable database replication |
| Replication Mode | streaming | Streaming or dump-based |
| Sync Method | rsync | Rsync with delta support |
| Freeze Write on Cutover | true | Freeze writes on source during cutover |
| Drain Queues on Cutover | true | Drain BullMQ/RabbitMQ before switching |
| Observation Duration | 300s | Post-cutover monitoring window |
| Auto-Rollback on Error | true | Automatically rollback if health drops |
| Max Error Rate | 5% | Error rate threshold for auto-rollback |
| Max Latency | 2000ms | Latency threshold for auto-rollback |

> **Note on cutover-related options.** *Traffic Provider*, *Freeze Write on Cutover*, and *Drain Queues on Cutover* configure the (not-yet-wired) `TrafficSwitchEngine`/`QueueEngine`. The executed pipeline does not switch traffic, freeze source writes, or drain queues at cutover today — see Step 9. These options are recorded for planning and future use, and *Max Error Rate* / *Max Latency* do not drive an automatic cutover rollback.

### Step 5: Dry Run

**Purpose:** Preview what will change without actually applying anything.

| Element | Description |
|---------|-------------|
| Action | Click **Run Dry Run** |
| Shows | A **live per-category checklist** (packages, configs, …) fills as each category is analyzed over `/ws/dryrun/{id}`; on completion the full add/modify/remove change list per category |
| Gate | Dry run must complete to proceed |
| Persistence | The computed result is written to the DB (`migration_steps`, `action='dryrun'`) and restored on refresh / browser close, so the change preview survives a page reload |
| Speed | The config scan now hashes all planned paths in a single `sha256sum` round-trip (no per-file downloads); the Docker probe runs as one fused SSH command |

### Step 6: Provision

**Purpose:** Install required dependencies on the target server.

| Element | Description |
|---------|-------------|
| Action | Click **Provision Target** |
| Shows | Provisioning progress per component (Docker, Compose, Nginx, Caddy, HAProxy, Redis, MySQL, PostgreSQL, MongoDB, Node.js) |
| Gate | Provisioning must complete to proceed |

### Step 7: Execute

**Purpose:** Start the migration pipeline — initial data sync and replication setup.

| Element | Description |
|---------|-------------|
| Action | Click **Start Migration** |
| Shows | Pipeline running status, live metrics dashboard |
| Behavior | Auto-advances to **Live Monitoring** when replication is established |
| Controls | Pause, Rollback available |

**Pipeline stages executed:** Discovery → Analysis → Planning → Validation → Preparation → Initial Sync → Live Replication

### Step 8: Live Monitoring

**Purpose:** Monitor sync progress and wait for replication to catch up.

| Element | Description |
|---------|-------------|
| Shows | Real-time metrics dashboard (see below), replication status per database |
| Gate | Replication lag must drop to **≤ 5 seconds** |
| Indicator | Green badge "Ready for Cutover" when lag ≤ 5s; yellow "Waiting for replication..." otherwise |
| Controls | Pause, Rollback available |

### Step 9: Cutover (manual)

**Purpose:** Record a manual-cutover checkpoint. The pipeline does **not** switch traffic automatically.

| Element | Description |
|---------|-------------|
| Action | Click **Confirm Cutover** (with explicit confirmation dialog) |
| Shows | Manual-cutover checklist, real-time progress |
| Gate | Must be explicitly confirmed by user |

**What the pipeline does at this stage:**

The traffic-switch stage records a manual-cutover checkpoint and emits a **warning** that a manual cutover is required. It writes a cutover record with `traffic_switched = false` and leaves the recorded state pointing at the source. It does **not** freeze source writes, switch traffic, or promote the target.

**What the operator must do manually:**

1. Verify replication lag is acceptable (Step 8) and the target is healthy
2. Freeze or drain writes on the source if the workload requires it
3. Switch traffic to the target (DNS / reverse proxy / load balancer)
4. Verify the target is serving production traffic correctly

Auto-advances to **Observation** after the checkpoint is recorded.

### Step 10: Observation

**Purpose:** Monitor post-cutover health and stability.

| Element | Description |
|---------|-------------|
| Shows | Countdown timer, health score, replication lag, remaining time |
| Duration | 300 seconds (configurable) |
| Auto-rollback | Triggers if health score drops below threshold |
| Action | Click **Commit Migration** when observation passes |

### Step 11: Finish

**Purpose:** Migration complete — review summary.

| Element | Description |
|---------|-------------|
| Shows | Risk score, downtime estimate, health score, categories migrated |
| Actions | **Export Report** (JSON download), **Back to Migrations** |
| Timeline | Full audit trail of all migration events |

---

## Live Metrics Dashboard

During steps 7–10, a real-time metrics dashboard is displayed:

| Metric | Description |
|--------|-------------|
| **Progress** | Overall pipeline progress percentage, with a plain-language running line ("…45% selesai, estimasi selesai: 5m") |
| **Transfer** | Bytes transferred / total, shown human-readable ("1.2 GB / 4.0 GB") |
| **Speed / Bandwidth** | Current transfer speed + network RX/TX, human-readable ("340 MB/s") |
| **Replication Lag** | Seconds behind source (target: ≤ 5s); shown as "Keterlambatan Replikasi: sehat (2s)" green, or the raw seconds when elevated |
| **Health Score** | Aggregated health score (0–100) |
| **ETA** | Estimated time to completion |
| **CPU Usage** | Target server CPU percentage with bar |
| **RAM** | Memory usage with bar |
| **Disk** | Disk usage percentage with bar |
| **Queue Status** | BullMQ/RabbitMQ: paused/active, job count |
| **Container Health** | Running containers: name, status, healthy/unhealthy |
| **Timeline** | Audit trail of state transitions |
| **Live Logs** | Real-time WebSocket message log |

**Refresh / browser-close behavior.** The live metric snapshot (progress, bytes, speed, lag, ETA, CPU/RAM/disk, cutover-confirmed, container health, queue state) is persisted per-migration to `localStorage` and restored on load, so a page refresh does not flash zeros or a misleading "Ready for Cutover". Replication lag is inherently live and not persisted as truth: after a refresh the lag badge shows **"menyambung ulang…"** (reconnecting) until a fresh frame arrives, then the real value. The snapshot is cleared on terminal states. Raw values remain available via element tooltips for advanced users.

---

## Controls

Available during pipeline execution (steps 7–10):

| Control | Description |
|---------|-------------|
| **Pause** | Pause the running pipeline |
| **Resume** | Resume a paused pipeline |
| **Rollback** | Revert all changes (with confirmation) |
| **Export** | Download migration report as JSON |
| **Diff** | View source/target differences |

---

## Database Replication Support

| Database | Replication Mode | Setup |
|----------|-----------------|-------|
| **MySQL** | Master/Replica (CHANGE MASTER TO) | mysqldump + position-based replication |
| **PostgreSQL** | Streaming replication | pg_basebackup + standby.signal |
| **Redis** | REPLICAOF | BGSAVE + replica promotion |
| **MongoDB** | rs.add() | mongodump + replica set join |

---

## Traffic Switching Support

> **Not wired into the live pipeline.** A `TrafficSwitchEngine`
> (`internal/mod/migration/traffic.go`) implements the providers below, but the
> executed pipeline's traffic-switch stage does **not** invoke it — it only
> records a manual-cutover checkpoint (see Step 9). The table documents the
> engine's intended provider support, not an automated capability the pipeline
> performs today. Switching traffic is a manual operator step.

| Provider | Method | Rollback |
|----------|--------|----------|
| **Cloudflare** | DNS API | Revert DNS record |
| **Nginx** | Config upload + reload | Restore original config |
| **Traefik** | Dynamic config | Restore original config |
| **HAProxy** | Config + socket reload | Restore original config |
| **Caddy** | Caddyfile reload | Restore original config |
| **Docker** | Compose up | Compose down + restore |

---

## State Machine

The pipeline progresses through 20+ states with strict transition validation:

```
Created → Planning → Discovery → CompatibilityCheck → RiskAssessment
→ Backup → ProvisionTarget → InstallDependencies → InitialSync
→ LiveReplication → Verification → PreCutover → TrafficSwitch
→ PostVerification → Observation → Committed
```

Any state can transition to **Failed** or **Interrupted**. Failed → Rollback → RolledBack. Interrupted → Resuming → (any in-progress state).

---

## Automatic Rollback

Rollback is triggered automatically when:

- Health score drops below the configured threshold during observation (when auto-rollback is enabled)
- User manually triggers rollback

Rollback reverses the changes the pipeline itself applied, in LIFO order (e.g. applied file/config categories, database replication setup). Because the pipeline never switches production traffic (see Step 9), there is no automatic traffic configuration to restore; if the operator performed a manual cutover, they must revert it manually.

---

## URL Map

| URL | Purpose |
|-----|---------|
| `/servers` | Server management |
| `/migrations` | Migration list |
| `/migrations/new` | Create new migration |
| `/migrations/{id}/pipeline` | **Pipeline wizard** (primary migration interface) |
| `/migrations/{id}/diff` | Source/target diff view |
