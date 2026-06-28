# Meshium API Reference

Complete API documentation for frontend implementation. All endpoints require authentication (session token) except `/api/health`, `/api/auth/status`, `/api/auth/setup`, and `/api/auth/unlock`.

## Table of Contents

- [Authentication](#authentication)
- [Error Format](#error-format)
- [REST Endpoints](#rest-endpoints)
  - [Auth](#auth)
  - [SSH Keys](#ssh-keys)
  - [Servers](#servers)
  - [Migrations](#migrations)
  - [Plans](#plans)
  - [Diff](#diff)
  - [Jobs](#jobs)
- [WebSocket Endpoints](#websocket-endpoints)
  - [Connection Test](#connection-test)
  - [Plan Migration](#plan-migration)
  - [Execute Migration](#execute-migration)
  - [Rollback Migration](#rollback-migration)
  - [Dry Run](#dry-run)
  - [Diff (WebSocket)](#diff-websocket)
  - [Job Progress](#job-progress) *(Phase 6 — not yet exposed via WebSocket)*
- [Data Types](#data-types)
  - [ServerSnapshot](#serversnapshot)
  - [DependencyGraph](#dependencygraph)
  - [CompatibilityReport](#compatibilityreport)
  - [Migration Types](#migration-types)
  - [MigrationPlan](#migrationplan)
  - [PlannedStep](#plannedstep)
  - [TransferEstimate](#transferestimate)
  - [RiskLevel](#risklevel)
  - [StepType](#steptype)
  - [Job Types](#job-types)

---

## Authentication

All authenticated endpoints require a session token. The token is obtained via `/api/auth/setup` (first run) or `/api/auth/unlock` (subsequent logins).

**Token delivery:** The frontend stores the session token in localStorage as `meshium_session_token` and sends it on authenticated API requests using `Authorization: Bearer <token>`. The server may also set a `session` cookie for browser/WebSocket flows where applicable.

**Middleware chain:** CORS → CSRF → Auth → Handler

---

## Error Format

All errors use a consistent JSON format:

```json
{
  "error": "human-readable error message",
  "code": "ERROR_CODE"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `VALIDATION_ERROR` | 400 | Invalid request body or parameters |
| `METHOD_NOT_ALLOWED` | 405 | HTTP method not supported for this path |
| `NOT_FOUND` | 404 | Resource not found |
| `SERVER_NOT_FOUND` | 404 | Server with given ID not found |
| `MIGRATION_NOT_FOUND` | 404 | Migration with given ID not found |
| `AUTH_FAILED` | 401 | Invalid password |
| `LOCKED` | 403 | App is locked and an authenticated endpoint was accessed |
| `ALREADY_SETUP` | 400 | Master password already set |
| `RATE_LIMITED` | 429 | Too many auth attempts (5 per minute) |
| `INTERNAL` | 500 | Internal server error |
| `SERVICE_UNAVAILABLE` | 503 | Service not configured |

---

## REST Endpoints

### Auth

#### `GET /api/auth/status`

Check if the master password has been set and whether the session is locked.

**Auth required:** No

**Response:**
```json
{
  "setup": true,
  "locked": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `setup` | boolean | True if master password has been set |
| `locked` | boolean | True if session is currently locked |

---

#### `POST /api/auth/setup`

Set the master password (first run only). Returns a session token. During setup, `pbkdf2_salt` is stored atomically in the same transaction as `master_password_hash`, `ssh_key_private_encrypted`, and `ssh_key_public`.

**Auth required:** No

**Rate limited:** 5 attempts per minute per IP

**Request body:**
```json
{
  "password": "your-secure-password"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `password` | string | Yes | Master password (min 8 chars, max 1024 chars) |

**Response (200):**
```json
{
  "status": "ok",
  "sessionToken": "session-token-string"
}
```

**Errors:**
- `400 ALREADY_SETUP` — Master password already set
- `400 VALIDATION_ERROR` — Password too short (< 8 chars) or too long (> 1024 chars)
- `429 RATE_LIMITED` — Too many attempts

---

#### `POST /api/auth/unlock`

Unlock the session with the master password.

**Auth required:** No

**Rate limited:** 5 attempts per minute per IP

**Request body:**
```json
{
  "password": "your-secure-password"
}
```

**Response (200):**
```json
{
  "status": "ok",
  "sessionToken": "session-token-string"
}
```

**Errors:**
- `401 AUTH_FAILED` — Invalid password
- `429 RATE_LIMITED` — Too many attempts

---

#### `POST /api/auth/lock`

Lock the current session.

**Auth required:** Yes

**Response (200):**
```json
{
  "status": "ok"
}
```

---

### SSH Keys

#### `GET /api/ssh-key/public`

Get the auto-generated Ed25519 SSH public key.

**Auth required:** Yes

**Response (200):**
```json
{
  "publicKey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
}
```

---

#### `POST /api/ssh-key/regenerate`

Regenerate the SSH keypair. The old key will stop working.

**Auth required:** Yes

**Response (200):**
```json
{
  "publicKey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
}
```

---

### Servers

#### `GET /api/servers`

List all servers. Returns `[]` (empty array) when no servers exist.

**Auth required:** Yes

**Query parameters (all optional):**

| Parameter | Type | Description |
|-----------|------|-------------|
| `environment` | string | Filter by environment (e.g., `production`) |
| `region` | string | Filter by region (e.g., `us-east`) |
| `tag` | string | Filter by tag |
| `q` | string | Search query (matches name, host, description) |

**Response (200):**
```json
[
  {
    "id": 1,
    "name": "web-server-01",
    "description": "Primary web server",
    "host": "192.168.1.100",
    "port": 22,
    "username": "root",
    "authMethod": "key",
    "tags": ["web", "production"],
    "environment": "production",
    "region": "us-east",
    "icon": "server",
    "color": "#3b82f6",
    "favorite": true,
    "bastionId": 0,
    "createdAt": "2026-06-27T10:00:00Z",
    "updatedAt": "2026-06-27T10:00:00Z"
  }
]
```

> **Note:** Credential fields (`password`, `sshKey`, `passphrase`) are never returned in API responses.

---

#### `POST /api/servers`

Create a new server.

**Auth required:** Yes

**Request body:**
```json
{
  "name": "web-server-01",
  "description": "Primary web server",
  "host": "192.168.1.100",
  "port": 22,
  "username": "root",
  "password": "secret-password",
  "sshKey": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
  "passphrase": "key-passphrase",
  "tags": ["web", "production"],
  "environment": "production",
  "region": "us-east",
  "icon": "server",
  "color": "#3b82f6",
  "bastionId": 0
}
```

| Field | Type | Required | Validation |
|-------|------|----------|------------|
| `name` | string | Yes | Non-empty, max 255 chars |
| `host` | string | Yes | Non-empty, max 255 chars |
| `username` | string | Yes | Non-empty, max 255 chars |
| `port` | int | No (default 22) | 1–65535 |
| `description` | string | No | Max 1024 chars |
| `password` | string | No* | One of password/sshKey required |
| `sshKey` | string | No* | One of password/sshKey required |
| `passphrase` | string | No | For encrypted SSH keys |
| `tags` | []string | No | Max 20 tags, 64 chars each |
| `environment` | string | No | |
| `region` | string | No | |
| `icon` | string | No | |
| `color` | string | No | |
| `bastionId` | int | No | 0 = direct, >0 = use server as bastion |

**Response (200):** Server object (same shape as list item)

**Errors:**
- `400 VALIDATION_ERROR` — Missing required fields or validation failure

---

#### `GET /api/servers/{id}`

Get a single server by ID.

**Auth required:** Yes

**Response (200):** Server object

**Errors:**
- `404 SERVER_NOT_FOUND` — Server not found

---

#### `PUT /api/servers/{id}`

Update a server. Only provided fields are updated (partial update).

**Auth required:** Yes

**Request body (all fields optional):**
```json
{
  "name": "new-name",
  "host": "10.0.0.50",
  "port": 2222,
  "tags": ["updated", "staging"],
  "bastionId": 3
}
```

All fields from the create request are accepted, but all are optional. Pointer types (`*string`, `*int`, `*[]string`) allow distinguishing between "not provided" (don't change) and "set to zero value" (clear).

**Response (200):** Updated server object

---

#### `DELETE /api/servers/{id}`

Delete a server.

**Auth required:** Yes

**Response (200):**
```json
{
  "status": "ok"
}
```

---

#### `PATCH /api/servers/{id}/favorite`

Toggle the favorite status of a server.

**Auth required:** Yes

**Response (200):**
```json
{
  "status": "ok"
}
```

---

#### `GET /api/servers/{id}/snapshot`

Get the server's discovery snapshot.

**Auth required:** Yes

**Response (200):** `ServerSnapshot` object

**Errors:**
- `404 NOT_FOUND` — No discovery snapshot exists for this server

---

#### `GET /api/servers/{id}/info`

Get the server's connection info (SSH status, latency, hostname, OS, etc.).

**Auth required:** Yes

**Response (200):**
```json
{
  "sshStatus": "connected",
  "latencyMs": 45,
  "hostname": "web-server-01",
  "os": "Ubuntu 22.04.3 LTS",
  "kernel": "5.15.0-91-generic",
  "architecture": "x86_64",
  "cpuModel": "Intel Xeon E5-2680 v4",
  "cpuCores": 8,
  "ramTotalMb": 16384,
  "diskTotalGb": 250.0,
  "virtualization": "kvm",
  "provider": "digitalocean",
  "publicIp": "203.0.113.50",
  "privateIp": "10.0.0.50",
  "timezone": "UTC"
}
```

**Errors:**
- `404 NOT_FOUND` — No connection info exists for this server

---

#### `POST /api/servers/{id}/discover`

Trigger a discovery job for the server.

**Auth required:** Yes

**Response (200):**
```json
{
  "jobID": "job-20260628120000.123456789"
}
```

**Errors:**
- `404 SERVER_NOT_FOUND` — Server not found

---

### Migrations

#### `GET /api/migrations`

List all migrations, ordered by creation date (newest first). Returns `[]` when empty.

**Auth required:** Yes

**Response (200):**
```json
[
  {
    "id": 1,
    "sourceId": 1,
    "targetId": 2,
    "categories": "[\"packages\",\"configs\",\"services\"]",
    "status": "completed",
    "plan": "{...}",
    "error": "",
    "createdAt": "2026-06-27T12:00:00Z",
    "completedAt": "2026-06-27T12:05:00Z"
  }
]
```

> **Note:** `categories` is a JSON-encoded string. `plan` is a JSON-encoded `MigrationPlan` object (or empty string if not yet planned).

---

#### `POST /api/migrations`

Create a migration plan. Connects to the source server, collects data for each category, and persists the plan.

**Auth required:** Yes

**Request body:**
```json
{
  "sourceServerId": 1,
  "targetServerId": 2,
  "categories": ["packages", "configs", "services", "users", "docker"],
  "configPaths": ["/etc/nginx", "/etc/mysql"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sourceServerId` | int | Yes | Server ID to collect from |
| `targetServerId` | int | Yes | Server ID to migrate to |
| `categories` | []string | Yes | At least one category. Options: `packages`, `configs`, `services`, `users`, `docker` |
| `configPaths` | []string | No | Additional config paths beyond `/etc/` |

**Response (201):** `MigrationPlan` object
```json
{
  "id": 1,
  "sourceServerId": 1,
  "targetServerId": 2,
  "status": "planned",
  "categories": ["packages", "configs", "services"],
  "source": {
    "sshStatus": "connected",
    "hostname": "source-server",
    "os": "Ubuntu 22.04.3 LTS",
    "kernel": "5.15.0-91-generic",
    "architecture": "x86_64",
    "cpuModel": "Intel Xeon E5-2680 v4",
    "cpuCores": 8,
    "ramTotalMb": 16384,
    "diskTotalGb": 250.0
  },
  "target": {
    "sshStatus": "connected",
    "hostname": "target-server",
    "os": "Debian 12.1",
    "kernel": "6.1.0-12-amd64",
    "architecture": "x86_64",
    "cpuModel": "AMD EPYC 7763",
    "cpuCores": 16,
    "ramTotalMb": 32768,
    "diskTotalGb": 500.0
  },
  "steps": [
    {
      "category": "packages",
      "action": "collect",
      "description": "Collect installed packages",
      "itemCount": 342
    },
    {
      "category": "configs",
      "action": "collect",
      "description": "Collect configuration files",
      "itemCount": 47
    }
  ],
  "warnings": [],
  "estimatedTime": "5 minutes"
}
```

**Errors:**
- `400 VALIDATION_ERROR` — Missing sourceServerId, targetServerId, or categories
- `500 INTERNAL` — Failed to connect to source or collect data

---

#### `GET /api/migrations/{id}`

Get migration details.

**Auth required:** Yes

**Response (200):** Migration object (same shape as list item)

**Errors:**
- `404 MIGRATION_NOT_FOUND` — Migration not found

---

#### `DELETE /api/migrations/{id}`

Delete a migration and all its steps.

**Auth required:** Yes

**Response (200):**
```json
{
  "status": "ok"
}
```

---

#### `POST /api/migrations/{id}/rollback`

Rollback a migration (non-streaming). For live progress, use the WebSocket endpoint instead.

**Auth required:** Yes

**Response (200):**
```json
{
  "status": "rolled_back"
}
```

---

#### `GET /api/migrations/{id}/steps`

Get all steps for a migration. Returns `[]` when empty.

**Auth required:** Yes

**Response (200):**
```json
[
  {
    "id": 1,
    "migrationId": 1,
    "category": "packages",
    "action": "collect",
    "status": "completed",
    "data": "{...}",
    "output": "",
    "error": "",
    "startedAt": "2026-06-27T12:00:00Z",
    "completedAt": "2026-06-27T12:00:30Z"
  }
]
```

---

#### `GET /api/migrations/{id}/dryrun`

Preview what a migration would change (non-streaming). For live progress, use the WebSocket endpoint.

**Auth required:** Yes

**Response (200):** `DryRunResult`
```json
{
  "migrationId": 1,
  "categories": [
    {
      "category": "packages",
      "changes": [
        {
          "type": "add",
          "resource": "package:nginx",
          "detail": "Package nginx will be installed"
        },
        {
          "type": "modify",
          "resource": "package:curl",
          "detail": "Package curl will be upgraded"
        }
      ],
      "summary": "5 changes"
    },
    {
      "category": "docker",
      "changes": [
        {
          "type": "add",
          "resource": "docker:container:web-app",
          "detail": "Container web-app (image: nginx:latest) will be created"
        }
      ],
      "summary": "3 changes"
    }
  ],
  "summary": {
    "totalChanges": 8,
    "addCount": 6,
    "modifyCount": 2,
    "removeCount": 0
  }
}
```

| Change Type | Description |
|-------------|-------------|
| `add` | Resource exists on source but not on target |
| `modify` | Resource exists on both but differs |
| `remove` | Resource exists on target but not on source |

---

#### `GET /api/migrations/{id}/preflight`

Run pre-flight validation checks. Returns blocking errors and non-blocking warnings.

**Auth required:** Yes

**Response (200):** `PreFlightResult`
```json
{
  "errors": [
    "docker is not installed on the target server"
  ],
  "warnings": [
    "target root filesystem has only 800MB available",
    "source family debian differs from target family rhel"
  ],
  "ok": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `errors` | []string | Blocking issues — migration should not proceed |
| `warnings` | []string | Non-blocking issues — migration can proceed with caution |
| `ok` | boolean | True if no blocking errors |

**Pre-flight checks:**
1. SSH connectivity — `echo ok` over SSH
2. Disk space — `df -h /` (warning if < 1GB available)
3. OS compatibility — source vs target distro family
4. Docker availability — if `docker` category is selected

---

#### `GET /api/migrations/{id}/export`

Export a migration plan as a downloadable JSON file.

**Auth required:** Yes

**Response:** JSON file download (`Content-Disposition: attachment; filename="migration-{id}.json"`)

```json
{
  "migration": {
    "id": 1,
    "sourceId": 1,
    "targetId": 2,
    "categories": "[\"packages\",\"configs\"]",
    "status": "completed",
    "createdAt": "2026-06-27T12:00:00Z"
  },
  "steps": [
    {
      "id": 1,
      "migrationId": 1,
      "category": "packages",
      "action": "collect",
      "status": "completed",
      "data": "{...}"
    }
  ]
}
```

---

### Plans

#### `POST /api/plans`

Create a migration plan.

**Auth required:** Yes

**Request body:** Same as `POST /api/migrations` (`PlanRequest`)

```json
{
  "sourceServerId": 1,
  "targetServerId": 2,
  "categories": ["packages", "configs", "services", "users", "docker"],
  "configPaths": ["/etc/nginx", "/etc/mysql"]
}
```

**Response (201):** `MigrationPlan` object

---

#### `GET /api/plans/{id}`

Get plan details.

**Auth required:** Yes

**Response (200):** `MigrationPlan` object

---

### Diff

#### `POST /api/diff`

Compare source and target servers across categories (non-streaming). For live progress, use the WebSocket endpoint.

**Auth required:** Yes

**Request body:**
```json
{
  "sourceId": 1,
  "targetId": 2,
  "categories": ["packages", "configs", "services", "users", "docker"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sourceId` | int | Yes | Source server ID |
| `targetId` | int | Yes | Target server ID |
| `categories` | []string | No | Categories to compare. If empty, all categories are compared |

**Response (200):** `DiffResult`
```json
{
  "sourceId": 1,
  "targetId": 2,
  "categories": [
    {
      "category": "packages",
      "onlyInSource": ["nginx", "redis-tools"],
      "onlyInTarget": ["apache2", "postgresql"],
      "different": [],
      "same": 340
    },
    {
      "category": "docker",
      "onlyInSource": ["web-app", "image:nginx:latest"],
      "onlyInTarget": ["db-container"],
      "different": [],
      "same": 2
    }
  ]
}
```

---

## WebSocket Endpoints

All WebSocket endpoints use the `ws://` protocol. The session cookie must be present for authentication.

### WebSocket Message Format

#### Migration WebSocket Messages

All migration WebSocket endpoints send `WSMessage` objects:

```json
{
  "step": "plan",
  "status": "progress",
  "value": "Collecting packages...",
  "error": ""
}
```

| Field | Type | Description |
|-------|------|-------------|
| `step` | string | Step name (e.g., `"plan"`, `"execute"`, `"rollback"`, `"dryrun"`, `"diff"`, or `"category:packages"`) |
| `status` | string | One of: `"progress"`, `"success"`, `"warning"`, `"error"`, `"complete"` |
| `value` | string | Human-readable progress message (omitted if empty) |
| `error` | string | Error message (omitted if empty) |

**Status values:**

| Status | Meaning |
|--------|---------|
| `progress` | Step is in progress |
| `success` | Step completed successfully |
| `warning` | Non-blocking issue encountered |
| `error` | Error occurred |
| `complete` | Entire operation finished |

#### Discovery WebSocket Messages

The discovery connection test uses a slightly different message format:

```json
{
  "step": "hostname",
  "status": "success",
  "value": "web-server-01",
  "error": "",
  "latencyMs": 45
}
```

| Field | Type | Description |
|-------|------|-------------|
| `step` | string | Step name (see below) |
| `status` | string | `"progress"`, `"success"`, `"error"`, `"complete"` |
| `value` | any | Step result value (string, number, or object) |
| `error` | string | Error message (omitted if empty) |
| `latencyMs` | int | SSH latency in milliseconds (only on first step) |

**Discovery steps (sent in order):**

| Step | Value Type | Description |
|------|-----------|-------------|
| `ssh` | string | SSH connection status (`"connected"`) |
| `hostname` | string | Server hostname |
| `os` | string | OS name and version |
| `kernel` | string | Kernel version |
| `cpu` | string | CPU model |
| `cores` | int | CPU core count |
| `ram` | int | RAM in MB |
| `disk` | float | Disk in GB |
| `virtualization` | string | Virtualization type |
| `ips` | object | `{ "public": "1.2.3.4", "private": "10.0.0.1" }` |
| `timezone` | string | Server timezone |
| `provider` | string | Cloud provider (if detected) |
| `complete` | - | All steps finished |

---

### Connection Test

#### `ws://host/ws/connect/{serverId}`

Connect to a server via SSH and stream system discovery steps.

**Auth required:** Yes (session cookie)

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `serverId` | int | Server ID to connect to |

**Messages sent (server → client):** Discovery WSMessage objects (see above)

**Example flow:**
```
→ {"step":"ssh","status":"progress","value":"Connecting..."}
→ {"step":"ssh","status":"success","value":"connected","latencyMs":45}
→ {"step":"hostname","status":"success","value":"web-server-01"}
→ {"step":"os","status":"success","value":"Ubuntu 22.04.3 LTS"}
→ {"step":"kernel","status":"success","value":"5.15.0-91-generic"}
→ {"step":"cpu","status":"success","value":"Intel Xeon E5-2680 v4"}
→ {"step":"cores","status":"success","value":8}
→ {"step":"ram","status":"success","value":16384}
→ {"step":"disk","status":"success","value":250.0}
→ {"step":"virtualization","status":"success","value":"kvm"}
→ {"step":"ips","status":"success","value":{"public":"203.0.113.50","private":"10.0.0.50"}}
→ {"step":"timezone","status":"success","value":"UTC"}
→ {"step":"provider","status":"success","value":"digitalocean"}
→ {"step":"complete","status":"complete"}
```

---

### Plan Migration

#### `ws://host/ws/plan`

Plan a migration with live progress streaming.

**Auth required:** Yes (session cookie)

**Request (two methods):**

**Method 1: POST with JSON body (before upgrade)**
```json
{
  "sourceServerId": 1,
  "targetServerId": 2,
  "categories": ["packages", "configs", "services"],
  "configPaths": ["/etc/nginx"]
}
```

**Method 2: GET with query parameters**
```
ws://host/ws/plan?source=1&target=2&categories=packages,configs,services
```

**Messages sent (server → client):** Migration WSMessage objects

**Example flow:**
```
→ {"step":"plan","status":"progress","value":"Connecting to source server..."}
→ {"step":"plan","status":"success","value":"Connected to source"}
→ {"step":"plan:packages","status":"progress","value":"Collecting packages..."}
→ {"step":"plan:packages","status":"success","value":"Collected 342 packages"}
→ {"step":"plan:configs","status":"progress","value":"Collecting configs..."}
→ {"step":"plan:configs","status":"success","value":"Collected 47 config files"}
→ {"step":"plan","status":"complete"}
```

---

### Execute Migration

#### `ws://host/ws/migrate/{id}`

Execute a migration with live progress streaming.

**Auth required:** Yes (session cookie)

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | int | Migration ID to execute |

**Messages sent (server → client):** Migration WSMessage objects

**Example flow:**
```
→ {"step":"execute","status":"progress","value":"Starting migration..."}
→ {"step":"packages","status":"progress","value":"Backing up packages..."}
→ {"step":"packages","status":"success","value":"Backed up 340 packages"}
→ {"step":"packages","status":"progress","value":"Installing 2 packages..."}
→ {"step":"packages","status":"progress","value":"Installing nginx..."}
→ {"step":"packages","status":"success","value":"Installed nginx"}
→ {"step":"packages","status":"progress","value":"Installing redis-tools..."}
→ {"step":"packages","status":"success","value":"Installed redis-tools"}
→ {"step":"configs","status":"progress","value":"Backing up configs..."}
→ {"step":"configs","status":"success","value":"Backed up 47 files"}
→ {"step":"configs","status":"progress","value":"Uploading 47 config files..."}
→ {"step":"configs","status":"success","value":"Uploaded 47 files"}
→ {"step":"execute","status":"complete"}
```

**On error:**
```
→ {"step":"packages","status":"error","error":"failed to install nginx: package not found"}
→ {"step":"packages","status":"progress","value":"Rolling back..."}
→ {"step":"execute","status":"error","error":"migration failed: package installation error"}
```

---

### Rollback Migration

#### `ws://host/ws/migrate/{id}/rollback`

Rollback a migration with live progress streaming.

**Auth required:** Yes (session cookie)

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | int | Migration ID to rollback |

**Messages sent (server → client):** Migration WSMessage objects

**Example flow:**
```
→ {"step":"rollback","status":"progress","value":"Starting rollback..."}
→ {"step":"configs","status":"progress","value":"Restoring configs..."}
→ {"step":"configs","status":"success","value":"Restored 47 files"}
→ {"step":"packages","status":"progress","value":"Restoring packages..."}
→ {"step":"packages","status":"success","value":"Restored 340 packages"}
→ {"step":"rollback","status":"complete"}
```

---

### Dry Run

#### `ws://host/ws/dryrun/{id}`

Dry run a migration with live progress streaming.

**Auth required:** Yes (session cookie)

**Path parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | int | Migration ID to dry run |

**Messages sent (server → client):** Migration WSMessage objects

**Example flow:**
```
→ {"step":"dryrun","status":"progress","value":"Connecting to target server..."}
→ {"step":"dryrun","status":"success","value":"Connected to target server"}
→ {"step":"dryrun:packages","status":"progress","value":"Analyzing packages..."}
→ {"step":"dryrun:packages","status":"success","value":"Found 2 changes for packages"}
→ {"step":"dryrun:configs","status":"progress","value":"Analyzing configs..."}
→ {"step":"dryrun:configs","status":"success","value":"Found 5 changes for configs"}
→ {"step":"dryrun","status":"complete","value":"Dry run complete: 7 total changes"}
```

---

### Diff (WebSocket)

#### `ws://host/ws/diff`

Compare source and target servers with live progress streaming.

**Auth required:** Yes (session cookie)

**Request (two methods):**

**Method 1: POST with JSON body (before upgrade)**
```json
{
  "sourceId": 1,
  "targetId": 2,
  "categories": ["packages", "configs"]
}
```

**Method 2: GET with query parameters**
```
ws://host/ws/diff?source=1&target=2&categories=packages,configs
```

**Messages sent (server → client):** Migration WSMessage objects

**Example flow:**
```
→ {"step":"diff","status":"progress","value":"Connecting to source and target..."}
→ {"step":"diff","status":"success","value":"Connected to both servers"}
→ {"step":"diff:packages","status":"progress","value":"Comparing packages..."}
→ {"step":"diff:packages","status":"success","value":"Compared packages: 2 only in source, 1 only in target, 0 different"}
→ {"step":"diff:configs","status":"progress","value":"Comparing configs..."}
→ {"step":"diff:configs","status":"success","value":"Compared configs: 5 only in source, 0 only in target, 3 different"}
→ {"step":"diff","status":"complete","value":"Diff complete"}
```

---

## Data Types

### ServerSnapshot

The `ServerSnapshot` is the comprehensive output of the Discovery Engine. It is produced by running 8 parallel collectors and is stored as JSON in the `discovery_snapshots` SQLite table.

```json
{
  "capturedAt": "2026-06-28T12:00:00Z",
  "os": {
    "name": "Ubuntu 22.04.3 LTS",
    "version": "22.04.3",
    "kernel": "5.15.0-91-generic",
    "architecture": "x86_64",
    "hostname": "web-server-01",
    "timezone": "UTC",
    "uptime": 864000,
    "virtualization": "kvm"
  },
  "hardware": {
    "cpuModel": "Intel Xeon E5-2680 v4",
    "cpuCores": 8,
    "ramTotalBytes": 17179869184,
    "ramUsedBytes": 8589934592,
    "diskTotalBytes": 268435456000,
    "diskUsedBytes": 134217728000
  },
  "docker": {
    "installed": true,
    "version": "24.0.7",
    "containers": [
      {
        "name": "web-app",
        "image": "nginx:latest",
        "status": "running",
        "ports": ["80:80", "443:443"],
        "volumes": ["/data:/usr/share/nginx/html"],
        "networks": ["bridge"],
        "labels": {"com.docker.compose.service": "web"}
      }
    ],
    "images": ["nginx:latest", "redis:7-alpine"],
    "composeProjects": [
      {
        "name": "web-stack",
        "configFile": "/opt/web-stack/docker-compose.yml",
        "services": ["web", "redis"]
      }
    ]
  },
  "services": [
    {
      "name": "nginx.service",
      "loadState": "loaded",
      "activeState": "active",
      "subState": "running",
      "type": "service",
      "after": ["network.target", "remote-fs.target"],
      "requires": ["network.target"]
    }
  ],
  "databases": [
    {
      "type": "mysql",
      "version": "8.0.35",
      "port": 3306,
      "dataDir": "/var/lib/mysql",
      "sizeBytes": 1073741824
    },
    {
      "type": "redis",
      "version": "7.2.3",
      "port": 6379,
      "dataDir": "/var/lib/redis",
      "sizeBytes": 104857600
    }
  ],
  "nginx": {
    "installed": true,
    "version": "1.22.1",
    "virtualHosts": [
      {
        "serverName": "example.com",
        "listen": ["80", "443 ssl"],
        "root": "/var/www/html",
        "proxyPass": "http://127.0.0.1:8080"
      }
    ],
    "upstreams": [
      {
        "name": "backend",
        "servers": ["127.0.0.1:8080", "127.0.0.1:8081"]
      }
    ],
    "sslCertificates": [
      {
        "domain": "example.com",
        "path": "/etc/letsencrypt/live/example.com/fullchain.pem",
        "expiryDays": 45,
        "issuer": "Let's Encrypt"
      }
    ]
  },
  "diskUsage": [
    {
      "filesystem": "/dev/sda1",
      "mount": "/",
      "size": "250G",
      "used": "120G",
      "available": "118G",
      "usePercent": "52%"
    },
    {
      "filesystem": "/dev/sdb1",
      "mount": "/data",
      "size": "500G",
      "used": "300G",
      "available": "200G",
      "usePercent": "60%"
    }
  ],
  "ports": [
    {
      "port": 80,
      "protocol": "tcp",
      "process": "nginx",
      "pid": 1234,
      "bindAddress": "0.0.0.0"
    },
    {
      "port": 3306,
      "protocol": "tcp",
      "process": "mysqld",
      "pid": 5678,
      "bindAddress": "127.0.0.1"
    }
  ],
  "collectionErrors": [
    {
      "collector": "DatabaseCollector",
      "error": "failed to detect PostgreSQL: command not found"
    }
  ]
}
```

### DependencyGraph

The dependency graph is built from the `ServerSnapshot` and determines migration ordering.

```json
{
  "nodes": [
    {"id": "container:web-app", "type": "container", "label": "web-app"},
    {"id": "database:mysql", "type": "database", "label": "MySQL"},
    {"id": "nginx:example.com", "type": "nginx", "label": "example.com"},
    {"id": "service:nginx.service", "type": "service", "label": "nginx.service"}
  ],
  "edges": [
    {
      "from": "container:web-app",
      "to": "database:mysql",
      "reason": "container web-app exposes port 3306 matching MySQL database"
    },
    {
      "from": "nginx:example.com",
      "to": "container:web-app",
      "reason": "nginx vhost example.com proxies to port 80 matching container web-app"
    },
    {
      "from": "service:nginx.service",
      "to": "service:network.target",
      "reason": "nginx.service requires network.target (After directive)"
    }
  ]
}
```

**Node types:** `container`, `service`, `database`, `nginx`

**Topological sort** produces a dependency-ordered list of nodes for migration sequencing.

### CompatibilityReport

The compatibility report validates source vs target server compatibility.

```json
{
  "compatible": false,
  "blockers": [
    {
      "check": "ram",
      "message": "target RAM (8GB) is less than source used RAM (12GB)"
    },
    {
      "check": "docker",
      "message": "source has Docker but Docker is not installed on target"
    },
    {
      "check": "ports",
      "message": "port 80 is already in use by nginx on target (PID 1234)"
    }
  ],
  "warnings": [
    {
      "check": "disk",
      "message": "target disk (250GB) is less than source total disk (500GB)"
    },
    {
      "check": "docker_version",
      "message": "target Docker version (20.10) is older than source (24.0)"
    },
    {
      "check": "os",
      "message": "source OS family (debian) differs from target OS family (rhel)"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `compatible` | boolean | True if no blockers |
| `blockers` | []CheckResult | Blocking issues — migration should not proceed |
| `warnings` | []CheckResult | Non-blocking issues — migration can proceed with caution |

**Blocker checks:**

| Check | Condition |
|-------|-----------|
| `ram` | Target total RAM < source used RAM |
| `disk` | Target total disk < source used disk |
| `docker` | Source has Docker but target doesn't |
| `ports` | Target port already in use by a different process |

**Warning checks:**

| Check | Condition |
|-------|-----------|
| `ram` | Target RAM < source total RAM (may be tight) |
| `disk` | Target disk < source total disk (may be tight) |
| `docker_version` | Target Docker version is older than source |
| `os` | Different OS family or architecture |

---

### Migration Types

#### Migration

```json
{
  "id": 1,
  "sourceId": 1,
  "targetId": 2,
  "categories": "[\"packages\",\"configs\",\"services\"]",
  "status": "completed",
  "plan": "{...MigrationPlan...}",
  "error": "",
  "createdAt": "2026-06-27T12:00:00Z",
  "completedAt": "2026-06-27T12:05:00Z"
}
```

**Migration status values:**

| Status | Description |
|--------|-------------|
| `planned` | Migration plan created, not yet executed |
| `running` | Migration is in progress |
| `completed` | Migration completed successfully |
| `failed` | Migration failed |
| `rolling_back` | Migration is being rolled back |
| `rolled_back` | Migration was rolled back |
| `rollback_failed` | Rollback failed |
| `interrupted` | Migration was interrupted (crash/restart) |
| `resuming` | Interrupted migration is being resumed |

#### MigrationStepRecord

```json
{
  "id": 1,
  "migrationId": 1,
  "category": "packages",
  "action": "collect",
  "status": "completed",
  "data": "{...}",
  "output": "",
  "error": "",
  "startedAt": "2026-06-27T12:00:00Z",
  "completedAt": "2026-06-27T12:00:30Z"
}
```

**Step status values:**

| Status | Description |
|--------|-------------|
| `pending` | Step not yet started |
| `running` | Step in progress |
| `completed` | Step completed successfully |
| `failed` | Step failed |
| `applied` | Step has been applied (checkpoint marker) |

#### PlanRequest

```json
{
  "sourceServerId": 1,
  "targetServerId": 2,
  "categories": ["packages", "configs", "services", "users", "docker"],
  "configPaths": ["/etc/nginx", "/etc/mysql"]
}
```

#### MigrationPlan

```json
{
  "id": 1,
  "sourceServerId": 1,
  "targetServerId": 2,
  "status": "planned",
  "categories": ["packages", "configs", "services"],
  "source": { /* SystemInfo */ },
  "target": { /* SystemInfo */ },
  "steps": [
    {
      "category": "packages",
      "action": "collect",
      "description": "Collect installed packages",
      "itemCount": 342
    }
  ],
  "warnings": [],
  "estimatedTime": "5 minutes"
}
```

#### DryRunResult

```json
{
  "migrationId": 1,
  "categories": [
    {
      "category": "packages",
      "changes": [
        {
          "type": "add",
          "resource": "package:nginx",
          "detail": "Package nginx will be installed"
        }
      ],
      "summary": "5 changes"
    }
  ],
  "summary": {
    "totalChanges": 8,
    "addCount": 6,
    "modifyCount": 2,
    "removeCount": 0
  }
}
```

#### DiffResult

```json
{
  "sourceId": 1,
  "targetId": 2,
  "categories": [
    {
      "category": "packages",
      "onlyInSource": ["nginx", "redis-tools"],
      "onlyInTarget": ["apache2"],
      "different": [],
      "same": 340
    }
  ]
}
```

#### PreFlightResult

```json
{
  "errors": ["docker is not installed on the target server"],
  "warnings": ["target root filesystem has only 800MB available"],
  "ok": false
}
```

---

## Frontend Implementation Notes

### WebSocket Connection Pattern

```typescript
// Example: WebSocket connection for migration execution
const ws = new WebSocket(`ws://${host}/ws/migrate/${migrationId}`);

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  // msg: { step: string, status: string, value?: string, error?: string }

  switch (msg.status) {
    case 'progress':
      // Update progress UI
      break;
    case 'success':
      // Mark step as complete
      break;
    case 'warning':
      // Show warning
      break;
    case 'error':
      // Show error, update UI to failed state
      break;
    case 'complete':
      // Migration finished — close connection
      ws.close();
      break;
  }
};

ws.onerror = (error) => {
  // Handle connection errors
};

ws.onclose = () => {
  // Connection closed
};
```

### WebSocket Plan/Diff with POST Body

For `ws://host/ws/plan` and `ws://host/ws/diff`, you can send the request as a POST before upgrading:

```typescript
// The WebSocket API doesn't support custom headers, so use query params
const ws = new WebSocket(
  `ws://${host}/ws/plan?source=${sourceId}&target=${targetId}&categories=${categories.join(',')}`
);
```

### Categories

The five migration categories:

| Category | Description | Collector | Applier |
|----------|-------------|-----------|---------|
| `packages` | Installed packages | `dpkg --get-selections` / `rpm -qa` / `pacman -Q` | Install missing packages on target |
| `configs` | Configuration files | SFTP download from `/etc/` and custom paths | Upload files to target (exclusion list enforced) |
| `services` | Enabled systemd units | `systemctl list-unit-files --state=enabled` | Enable and start services on target |
| `users` | Users, groups, cron, firewall | `/etc/passwd`, `/etc/group`, `crontab -l`, `ufw status` | Create users/groups, import cron, firewall rules |
| `docker` | Containers, images, volumes, compose | `docker ps`, `docker images`, `docker volume ls` | Pull images, create volumes, upload compose, recreate containers |

### Config Exclusion List

These paths are automatically excluded from config migration (cannot be overridden):

```
/etc/fstab, /etc/hostname, /etc/machine-id, /etc/hosts, /etc/shadow,
/etc/passwd, /etc/group, /etc/subuid, /etc/subgid, /etc/resolv.conf,
/etc/network/, /etc/netplan/, /etc/sysconfig/network-scripts/,
/etc/udev/, /etc/crypttab, /etc/mdadm.conf, /etc/dracut.conf,
/etc/kernel/, /etc/grub.d/, /etc/default/grub
```

---

## Migration Planner Types (Phase 5)

The Migration Planner takes ServerSnapshot + DependencyGraph from Phase 4 and produces a MigrationPlan with ordered steps, estimates, and risk assessment.

### MigrationPlan

```json
{
  "id": "plan-1719567890123456789",
  "createdAt": "2026-06-28T12:00:00Z",
  "source": {
    "hostname": "web-prod-01",
    "os": "Ubuntu 22.04.3 LTS",
    "ip": "",
    "ramTotalMb": 4096,
    "diskTotalGb": 80.0
  },
  "target": {
    "hostname": "web-new-01",
    "os": "Ubuntu 24.04.1 LTS",
    "ip": "",
    "ramTotalMb": 8192,
    "diskTotalGb": 160.0
  },
  "steps": [...],
  "totalEstimate": {
    "sizeBytes": 5368709120,
    "durationMin": "53.687s",
    "durationMax": "2m0.53s",
    "confidence": 0.3
  },
  "riskLevel": "high",
  "warnings": [
    {
      "code": "os",
      "message": "source OS family \"debian\" differs from target OS family \"debian\""
    }
  ],
  "blockers": []
}
```

### PlannedStep

Each step in the `steps` array:

```json
{
  "order": 0,
  "name": "database:mysql",
  "type": "database",
  "dependsOn": [],
  "estimate": {
    "sizeBytes": 2147483648,
    "durationMin": "40.96s",
    "durationMax": "1m1.44s",
    "confidence": 0.85
  },
  "riskLevel": "high",
  "reversible": true,
  "config": {
    "type": "mysql",
    "version": "8.0.35",
    "port": 3306,
    "dataDir": "/var/lib/mysql",
    "sizeMB": 2048,
    "dumpCommand": "mysqldump --all-databases --single-transaction --routines --triggers",
    "restoreCommand": "mysql < dump.sql"
  }
}
```

### Step Config Shapes

Each step type has a different `config` shape:

#### DockerVolume
```json
{
  "containerName": "web-app",
  "image": "nginx:latest",
  "volumes": ["/data:/usr/share/nginx/html"],
  "ports": [{"hostPort": 80, "containerPort": 80, "protocol": "tcp"}],
  "networks": ["bridge"],
  "labels": {"com.docker.compose.service": "web"},
  "composeProject": "web-stack",
  "composeFile": "/opt/web-stack/docker-compose.yml"
}
```

#### DockerImage
```json
{
  "image": "nginx:latest",
  "containerName": "web-app"
}
```

#### Database
```json
{
  "type": "mysql",
  "version": "8.0.35",
  "port": 3306,
  "dataDir": "/var/lib/mysql",
  "sizeMB": 2048,
  "dumpCommand": "mysqldump --all-databases --single-transaction --routines --triggers",
  "restoreCommand": "mysql < dump.sql"
}
```

#### File
```json
{
  "sourcePath": "/var/www/html",
  "targetPath": "/var/www/html",
  "isDirectory": true
}
```

#### Config
```json
{
  "sourcePath": "/etc/nginx/sites-available",
  "targetPath": "/etc/nginx/sites-available"
}
```

#### Nginx
```json
{
  "serverName": "example.com",
  "configFile": "/etc/nginx/sites-available/example.com",
  "listen": "80",
  "proxyPass": "http://127.0.0.1:8080"
}
```

#### Service
```json
{
  "name": "nginx.service",
  "type": "simple",
  "dependsOn": ["network.target"]
}
```

### TransferEstimate

```json
{
  "sizeBytes": 2147483648,
  "durationMin": "40.96s",
  "durationMax": "1m1.44s",
  "confidence": 0.85
}
```

| Field | Type | Description |
|-------|------|-------------|
| `sizeBytes` | int64 | Estimated total bytes to transfer |
| `durationMin` | Duration | Minimum estimated duration (optimistic) |
| `durationMax` | Duration | Maximum estimated duration (pessimistic, includes 50% buffer) |
| `confidence` | float64 | Confidence score (0.0–1.0) based on data availability |

### RiskLevel

| Level | Description |
|-------|-------------|
| `low` | Routine operation, minimal risk of data loss or downtime |
| `medium` | Moderate risk, some potential for issues but recoverable |
| `high` | Significant risk, requires careful execution and verified backups |
| `critical` | Severe risk, should not proceed without explicit user override |

### StepType

| Type | Description | Generator |
|------|-------------|-----------|
| `docker_volume` | Transfer Docker container volumes | DockerStepGenerator |
| `docker_image` | Pull/build Docker images on target | DockerStepGenerator |
| `database` | Dump database on source, transfer, restore on target | DatabaseStepGenerator |
| `file` | Transfer files/directories (non-Docker, non-config) | FileStepGenerator |
| `config` | Transfer configuration files | FileStepGenerator |
| `nginx` | Copy Nginx config, verify syntax, reload | NginxStepGenerator |
| `service` | Enable and start systemd services on target | ServiceStepGenerator |

### Plan → Engine Bridge

The `BuildSteps()` function converts a MigrationPlan into `[]migration.MigrationStep` that can be executed by the Phase 2 Engine:

| StepType | MigrationStep Implementation | Description |
|----------|------------------------------|-------------|
| `docker_volume` | DockerVolumeMigrationStep | Stop → transfer volumes via io.Pipe → start |
| `docker_image` | DockerImageMigrationStep | Pull image on target |
| `database` | DatabaseMigrationStep | Dump → stream via io.Pipe → restore |
| `nginx` | NginxMigrationStep | Copy config → verify syntax → reload |
| `service` | ServiceMigrationStep | Enable + start systemd unit |
| `file` | transfer.FileTransferStep | SCP/rsync with checksum verification |
| `config` | transfer.FileTransferStep | SCP/rsync with checksum verification |

All custom step types implement the `migration.MigrationStep` interface (Prepare/Apply/Verify/Rollback) and are verified at compile time.

### Plan Store

Migration plans are persisted to SQLite with WAL mode:

| Operation | Method | Description |
|-----------|--------|-------------|
| Save | `SavePlan(ctx, plan)` | Store or update a plan (upsert) |
| Load | `LoadPlan(ctx, planID)` | Retrieve a plan by ID |
| List | `ListPlans(ctx)` | List all plans (newest first) |
| Delete | `DeletePlan(ctx, planID)` | Remove a plan by ID |

Plans are stored as JSON in the `migration_plans` table with metadata columns for efficient listing.

---

## Job Types

*(Phase 6 — Job Engine. The Job Engine is implemented in `internal/jobengine/` but not yet exposed via REST/WebSocket endpoints. Phase 7 (API layer) will wire these up.)*

### Job

```typescript
interface Job {
  id: string;           // Unique job ID (format: job-YYYYMMDDHHMMSS.nnnnnnnnn)
  type: JobType;        // "migration" | "discovery" | "compat_check"
  status: JobStatus;    // "queued" | "running" | "paused" | "done" | "failed" | "cancelled"
  createdAt: string;    // ISO timestamp
  startedAt: string | null;
  finishedAt: string | null;
  planID: string;       // For migration jobs
  migrationID: string;  // For migration jobs
  progress: JobProgress;
  logs: JobLog[];
  error: string;        // Error message if failed
}
```

### JobType

```typescript
type JobType = "migration" | "discovery" | "compat_check";
```

### JobStatus

```typescript
type JobStatus = "queued" | "running" | "paused" | "done" | "failed" | "cancelled";

// IsTerminal: true for "done", "failed", "cancelled"
// IsActive: true for "queued", "running", "paused"
```

### JobProgress

```typescript
interface JobProgress {
  currentStep: number;
  totalSteps: number;
  currentName: string;
  percentage: number;    // 0-100
  bytesDone: number;
  bytesTotal: number;
  speedBPS: number;      // bytes per second
  eta: string;           // estimated time remaining
}
```

### JobLog

```typescript
interface JobLog {
  timestamp: string;
  level: LogLevel;       // "info" | "warn" | "error"
  step: string;
  message: string;
}
```

### JobRequest

```typescript
interface JobRequest {
  type: JobType;
  planID?: string;       // Required for migration jobs
  sourceID?: string;     // Required for discovery/compat_check jobs
  targetID?: string;     // Required for compat_check jobs
  migrationID?: string;  // Optional for migration jobs
}
```

### JobFilter

```typescript
interface JobFilter {
  type?: JobType;
  status?: JobStatus;
  limit?: number;
}
```

### Jobs

Current REST endpoints for job inspection and logs.

#### `GET /api/jobs`

List jobs with optional filters.

**Auth required:** Yes

**Query parameters (all optional):**

| Parameter | Type | Description |
|-----------|------|-------------|
| `type` | string | Filter by job type |
| `status` | string | Filter by job status |
| `limit` | int | Maximum number of jobs to return |

**Response (200):** `Job[]`

---

#### `GET /api/jobs/{id}`

Get job details.

**Auth required:** Yes

**Response (200):** `Job` object

---

#### `GET /api/jobs/{id}/logs`

Get job logs.

**Auth required:** Yes

**Response (200):** `JobLog[]`

---

### Job Progress *(Phase 7 — not yet exposed)*

| Path | Description |
|------|-------------|
| `ws://host/ws/jobs/{id}/progress` | Subscribe to real-time job progress updates |

The WebSocket sends `JobProgress` objects as JSON. The broadcast is non-blocking — if the subscriber is slow, updates are dropped (not buffered indefinitely).

