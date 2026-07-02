# Meshium — Implementation Plan v2.0

**Tanggal:** 2026-07-03  
**Berdasarkan:** Audit v1.5.0 + Validasi source code  
**Status:** Semua 7 P0 dan 6 P1 yang diaudit CONFIRMED + 5 additional bugs ditemukan  
**Tujuan:** Menjadikan Meshium platform migrasi Linux yang aman, recoverable, observable, dan menuju zero-downtime  

---

## 1. Executive Summary

Meshium memiliki fondasi arsitektur yang solid — modular pipeline engine, state machine, dependency graph, 11-step wizard UI. Namun validasi source code mengkonfirmasi **7 critical bugs**, **18 high-priority gaps**, dan **5 additional bugs** yang membuat platform **belum aman untuk migrasi production**.

Plan ini mendefinisikan 5 fase implementasi:
- **Phase 0:** Validasi + test reproduction (1 minggu)
- **Phase 1:** P0 critical fixes (2 minggu)
- **Phase 2:** P1 reliability & security (3 minggu)
- **Phase 3:** P2 discovery & planner expansion (3 minggu)
- **Phase 4:** UX & quality improvements (2 minggu)

**Prinsip utama:** Jangan pernah claim success tanpa verification. Backend adalah source of truth. Setiap perubahan harus punya test.

---

## 2. Current Architecture Assessment

### Yang sudah baik (pertahankan)
- **Pipeline engine** — modular `PipelineStageHandler` interface, sequential execution, retry, checkpoint
- **State machine** — `transitionTable` dengan valid transitions, `ForceTransition()` untuk edge cases
- **Dependency graph** — `BuildDependencyGraph()` + `TopologicalSort()` di `discovery/graph.go`
- **Database schema** — 14 tabel pipeline, WAL mode, foreign keys, indexes
- **SSH encryption** — AES-256-GCM + PBKDF2 600k iterations untuk private keys
- **Frontend wizard** — 11-step flow, validation gates, live metrics dashboard
- **Module structure** — clean separation: discovery, planner, migration, ssh, transport

### Yang perlu diperbaiki (dengan evidence)
| Area | Masalah | Evidence |
|------|---------|----------|
| Sync | local-to-local, bukan remote-to-remote | `sync.go:63-68` — `rsync sourcePath targetPath` di sourceSSH |
| Cutover | freezeWrites() no-op | `cutover.go:221-230` — hanya comments + `return nil` |
| Observation | tidak auto-rollback | `observation.go:124-137` — hanya return error |
| Pipeline | defaultRegistry nil → panic | `pipeline.go:1280` — var tidak pernah di-assign |
| Frontend | cutover/commit UI-only | `+page.svelte:328-356` — hanya mutate local state |
| Provisioning | verified=true tanpa verify | `provision.go:43-97` — unconditional set |
| Rollback | failures dilaporkan success | `recovery.go:234-287` — errors jadi warnings |

### Bug tambahan yang ditemukan saat validasi
| # | Bug | File | Dampak |
|---|-----|------|--------|
| A1 | Cutover final-sync guard: cek `e.sync` tapi dereference `e.replication` | `cutover.go:80-87` | Skip final sync atau nil panic |
| A2 | Initial sync silently skip failed categories | `pipeline.go:888-918` | Report success padahal data tidak sync |
| A3 | Race condition pada `p.stages` — RegisterStage() lock, Execute() tidak | `pipeline.go:117-126 vs 232-337` | Data race |
| A4 | Observation falsely succeed saat AutoRollback=false | `observation.go:127-137` | Threshold breach dilaporkan success |
| A5 | Multiple persistence errors diabaikan | `cutover.go`, `pipeline.go`, `recovery.go` | State drift antara runtime dan DB |

---

## 3. Confirmed Critical Bugs (P0)

### C1. Sync engine tidak remote-to-remote
**Evidence:** `sync.go:63-68` — `buildRsyncCommand()` builds `rsync ... sourcePath targetPath`, executed on `sourceSSH.ExecContext()`. TargetSSH never used.

### C2. freezeWrites() no-op
**Evidence:** `cutover.go:221-230` — function body is comments + `return nil`.

### C3. Observation tidak auto-rollback
**Evidence:** `observation.go:124-137` — only returns error, no rollback invocation.

### C4. Pipeline panic di initialSyncStage
**Evidence:** `pipeline.go:1280` — `defaultRegistry` declared but never assigned. `getRegistryFromContext()` returns nil. `initialSyncStage.Execute()` calls `getRegistryFromContext(pc).Get(step.Category)` which panics on nil receiver.

### C5. Cutover/commit frontend UI-only
**Evidence:** `+page.svelte:328-356` — `confirmCutover()` sets `cutoverConfirmed = true`. `commitMigration()` mutates local state. No `pipelineApi` calls.

### C6. Provisioning verified=true tanpa verification
**Evidence:** `provision.go:43-97` — `UpdateProvisionState(..., true, true, true, version, "")` called unconditionally after install.

### C7. Rollback failures dilaporkan sebagai success
**Evidence:** `recovery.go:234-287` — `step.Rollback()` errors become warnings. Checkpoints still cleared. State set to `StateRolledBack`. Returns successful `RecoveryResult`.

---

## 4. Confirmed High-Priority Gaps (P1)

### H1. WebSocket tidak ada reconnection
### H2. Rollback button hilang saat disconnect
### H3. Pause = cancel (PARTIALLY_CORRECT — pause routed to Cancel())
### H4. Resume/retry stage creation unsafe — LastInsertId() after UPSERT
### H5. Traffic rollback broken (Cloudflare, Traefik, Docker, DNS)
### H6. PostgreSQL lag calculation salah — `pg_last_xact_replay_timestamp()` on wrong host
### H7. BullMQ sync unsafe — binary DUMP in shell command, errors ignored
### H8. Discovery missing 20+ item types
### H9. Tidak ada workload classification
### H10. Planner silently skips unsupported types
### H11. New migration dead-end after failure
### H12. WebSocket token di URL query string
### H13. SSH auto-accept unknown keys
### H14. Compatibility checker tidak detect target port conflicts
### H15. DB write errors diabaikan di stage handlers
### H16. Docker recreation tidak preserve ports/volumes/networks/restart/healthcheck
### H17. Risk engine tidak punya confidence score atau rollback feasibility
### H18. Redis usage type tidak terdeteksi

---

## 5. Target Architecture

### Backend Module Map

```
internal/mod/migration/
├── pipeline.go              — Orchestrator (FIX: nil registry, race condition, persistence errors)
├── pipeline_handler.go      — REST + WS API (FIX: pause/resume semantics, action endpoints)
├── pipeline_models.go       — Data models (EXTEND: new event, verification, workload types)
├── pipeline_repo.go         — Repository (FIX: LastInsertId, add event/metric tables)
├── state.go                 — State machine (FIX: remove ForceTransition abuse)
├── categories.go            — Category registry (FIX: nil guard)
│
├── sync.go                  — Sync engine (REWRITE: remote-to-remote)
├── cutover.go               — Cutover engine (FIX: freezeWrites, final-sync guard)
├── observation.go           — Observation engine (FIX: auto-rollback, false success)
├── provision.go             — Provisioning engine (FIX: verification framework)
├── recovery.go              — Recovery engine (FIX: rollback error reporting)
├── replication.go           — Replication engine (FIX: PostgreSQL lag, Redis type detection)
├── queue.go                 — Queue engine (FIX: BullMQ safe sync)
├── traffic.go               — Traffic engine (FIX: provider rollback)
├── health.go                — Health engine (EXTEND: more check types)
├── compatibility.go         — Compatibility engine (EXTEND: target port conflicts, filesystem, firewall)
├── risk_engine.go           — Risk engine (EXTEND: confidence, rollback feasibility)
├── docker.go                — Docker operations (EXTEND: full container recreation)
│
├── freeze.go                — NEW: Write freeze manager per workload type
├── verifier.go              — NEW: Post-provisioning verification framework
├── rollback_manager.go      — NEW: Step-based rollback with status tracking
├── event_bus.go             — NEW: Sequenced event system for WS + audit
├── workload_classifier.go   — NEW: Workload type detection and classification
├── strategy_selector.go     — NEW: Migration strategy selection based on workload
│
├── pipeline_test.go         — EXTEND: more coverage
├── pipeline_engines_test.go — EXTEND: more coverage
├── sync_test.go             — NEW: remote-to-remote integration tests
├── freeze_test.go           — NEW: write freeze tests
├── observation_test.go      — NEW: auto-rollback tests
├── verifier_test.go         — NEW: verification tests
├── rollback_test.go         — NEW: rollback failure tests
└── event_bus_test.go        — NEW: event sequencing tests

internal/mod/discovery/
├── collectors_basic.go      — EXTEND: swap, filesystem type, locale
├── collectors_service.go    — EXTEND: Apache, Caddy, Traefik, HAProxy, Envoy
├── collector.go             — KEEP: interface
├── collector_runner.go      — KEEP: parallel runner
├── graph.go                 — EXTEND: more node types, workload classification
├── compat.go                — EXTEND: target port conflicts, filesystem, firewall
│
├── collectors_user.go       — NEW: users, groups, SSH config
├── collectors_package.go    — NEW: packages, repositories
├── collectors_security.go   — NEW: firewall, SELinux, AppArmor
├── collectors_cron.go       — NEW: cron jobs, systemd timers
├── collectors_runtime.go    — NEW: Node, Python, Go, Java, PHP, Ruby, Rust, .NET, Bun, Deno
├── collectors_queue.go      — NEW: RabbitMQ, Kafka, NATS, ActiveMQ
├── collectors_monitoring.go — NEW: Prometheus, Grafana, Loki, Jaeger
├── collectors_ci.go         — NEW: GitHub Actions, GitLab CI, Jenkins
├── collectors_docker_ext.go — NEW: Dockerfile, networks, volumes, healthchecks, restart policies
├── workload_classifier.go   — NEW: classify workloads from discovery data
└── model.go                 — EXTEND: new data types for all collectors

web/src/
├── lib/api/
│   ├── pipeline.ts          — EXTEND: action endpoints, reconnect, event replay
│   ├── client.ts            — EXTEND: secure WS auth
│   └── migrations.ts        — KEEP
├── routes/migrations/
│   ├── +page.svelte         — FIX: nested button in link
│   ├── new/+page.svelte     — FIX: dead-end after failure
│   └── [id]/pipeline/+page.svelte — REWRITE: backend-driven state, WS reconnect, action buttons
```

### Frontend State Model

```
Frontend state = reflection of backend state + local UI state

Backend (source of truth):
  - migration status (created, running, paused, completed, failed, rolled_back)
  - pipeline stage (discovery, syncing, cutover, observing, etc.)
  - verification results
  - health scores
  - rollback status

Frontend (local only):
  - current wizard step (derived from backend stage)
  - form inputs (categories, config)
  - UI toggles (expanded panels, filter settings)
  - WS connection status

NEVER:
  - Set terminal status locally (completed, failed, rolled_back)
  - Advance wizard step without backend acknowledgement
  - Hide rollback/pause when migration is non-terminal
```

---

## 6. Migration Safety Model

### Core Principles
1. **Never claim success without verification** — every terminal state must be verified
2. **Never cutover without passing checklist** — all pre-cutover checks must pass
3. **Never commit without observation** — post-cutover observation must complete
4. **Never delete source automatically** — source stays until user explicitly finalizes
5. **Never hide unsupported workloads** — planner must warn, not skip
6. **Never silently ignore failed rollback** — rollback failures are critical errors
7. **Never trust frontend state for final status** — backend is source of truth
8. **Never expose secrets in logs** — mask env values, never log credentials
9. **Always keep source recoverable until commit** — maintain rollback path
10. **Always preserve rollback path until user explicitly finalizes** — don't auto-cleanup
11. **Always emit events for every critical action** — audit trail must be complete

### Write Freeze Strategy

```go
// internal/mod/migration/freeze.go

type FreezeManager struct {
    sourceSSH SSHExecuter
    targetSSH SSHExecuter
    repo      PipelineRepo
}

type FreezeResult struct {
    Success    bool     `json:"success"`
    Method     string   `json:"method"`     // "database_readonly", "app_maintenance", "proxy_block"
    FrozenDBs  []string `json:"frozenDbs"`
    FrozenApps []string `json:"frozenApps"`
    Errors     []string `json:"errors,omitempty"`
}

// FreezeWrites implements workload-specific write freezing
func (m *FreezeManager) FreezeWrites(ctx context.Context, config *MigrationConfig) (*FreezeResult, error) {
    result := &FreezeResult{}
    
    // 1. Freeze databases first (most critical)
    for _, db := range config.Databases {
        switch db.Type {
        case "mysql", "mariadb":
            // FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only = ON;
            // Verify: SHOW VARIABLES LIKE 'read_only';
            err := m.freezeMySQL(ctx, db)
            if err != nil {
                result.Errors = append(result.Errors, fmt.Sprintf("mysql freeze failed: %v", err))
                // Rollback any partial freezes
                m.unfreezeAll(ctx, result)
                return result, fmt.Errorf("mysql freeze failed: %w", err)
            }
            result.FrozenDBs = append(result.FrozenDBs, db.Type)
            
        case "postgresql":
            // SET default_transaction_read_only = on; on target
            // Or: pg_ctl pause if available
            err := m.freezePostgreSQL(ctx, db)
            // ...same pattern
            
        case "redis":
            // Detect usage type first
            usage := m.detectRedisUsage(ctx, db)
            if usage == "bullmq" || usage == "queue" {
                // Pause BullMQ workers
                m.pauseBullMQWorkers(ctx, db)
            }
            // For cache: no freeze needed (data can be rebuilt)
            // For session: enable maintenance mode on app
            
        case "mongodb":
            // db.fsyncLock() — blocks writes
            // Verify: db.currentOp(true).fsyncLock
        }
    }
    
    // 2. Pause workers (BullMQ, Celery, Sidekiq)
    for _, queue := range config.Queues {
        m.pauseWorkers(ctx, queue)
    }
    
    // 3. Enable app maintenance mode if available
    // Check for common maintenance mode indicators
    
    // 4. Verify writes are actually frozen
    verified := m.verifyFreeze(ctx, config)
    if !verified {
        m.unfreezeAll(ctx, result)
        return result, fmt.Errorf("freeze verification failed — writes may still be possible")
    }
    
    result.Success = true
    return result, nil
}

// UnfreezeWrites reverses all freeze actions (for rollback)
func (m *FreezeManager) UnfreezeWrites(ctx context.Context, result *FreezeResult) error { ... }

// verifyFreeze attempts a test write to confirm writes are blocked
func (m *FreezeManager) verifyFreeze(ctx context.Context, config *MigrationConfig) bool { ... }
```

### Redis Usage Detection

```go
type RedisUsage string

const (
    RedisUsageCache      RedisUsage = "cache"
    RedisUsageSession    RedisUsage = "session"
    RedisUsageBullMQ     RedisUsage = "bullmq"
    RedisUsageQueue      RedisUsage = "queue"
    RedisUsagePubSub     RedisUsage = "pubsub"
    RedisUsageLock       RedisUsage = "lock"
    RedisUsageRateLimit  RedisUsage = "rate_limiter"
    RedisUsageUnknown    RedisUsage = "unknown"
)

func detectRedisUsage(ssh SSHExecuter, port int) RedisUsage {
    // Check key patterns:
    // bull:* → BullMQ
    // session:* / sess:* → session
    // rate_limit:* / ratelimit:* → rate limiter
    // lock:* / redis-lock:* → lock
    // pub/sub channels → pubsub
    // High TTL + low memory → likely cache
    // Persistent + no expiry → likely queue/data
}
```

---

## 7. Data Consistency Model

### Sync Engine: Remote-to-Remote

```go
// internal/mod/migration/sync.go — REWRITE

type SyncStrategy string

const (
    SyncStrategyRsyncOverSSH  SyncStrategy = "rsync_over_ssh"   // Default: rsync -e ssh
    SyncStrategyTarStream     SyncStrategy = "tar_stream"        // tar | ssh | tar
    SyncStrategyPullFromTarget SyncStrategy = "pull_from_target" // Target pulls from source
)

type SyncConfig struct {
    Strategy        SyncStrategy
    SourcePath      string
    TargetPath      string
    SourceSSH       SSHExecuter
    TargetSSH       SSHExecuter
    TargetHost      string
    TargetPort      int
    TargetUser      string
    BandwidthLimit  string  // e.g., "10m"
    ExcludePatterns []string
    IncludePatterns []string
    Checksum        bool
    DryRun          bool
    PreservePerms   bool    // -p (permissions)
    PreserveOwner   bool    // -o (owner) - requires root
    PreserveGroup   bool    // -g (group) - requires root
    PreserveSymlinks bool   // -l
    PreserveTimes   bool    // -t
    Sparse          bool    // --sparse
    Delete          bool    // --delete (remove files on target not on source)
}

// buildRsyncCommand builds a SOURCE → TARGET remote rsync command
func buildRsyncCommand(config *SyncConfig) string {
    args := []string{"rsync", "-avz", "--partial", "--append-verify"}
    
    if config.Checksum { args = append(args, "--checksum") }
    if config.DryRun { args = append(args, "--dry-run") }
    if config.BandwidthLimit != "" { args = append(args, fmt.Sprintf("--bwlimit=%s", config.BandwidthLimit)) }
    if config.PreservePerms { args = append(args, "-p") }
    if config.PreserveOwner { args = append(args, "-o") }
    if config.PreserveGroup { args = append(args, "-g") }
    if config.PreserveSymlinks { args = append(args, "-l") }
    if config.PreserveTimes { args = append(args, "-t") }
    if config.Sparse { args = append(args, "--sparse") }
    if config.Delete { args = append(args, "--delete") }
    
    for _, excl := range config.ExcludePatterns {
        args = append(args, fmt.Sprintf("--exclude=%s", excl))
    }
    
    // KEY FIX: Remote destination via SSH
    sshCmd := fmt.Sprintf("ssh -p %d -o StrictHostKeyChecking=accept-new", config.TargetPort)
    args = append(args, fmt.Sprintf("-e %q", sshCmd))
    args = append(args, fmt.Sprintf("%s/", config.SourcePath))
    args = append(args, fmt.Sprintf("%s@%s:%s/", config.TargetUser, config.TargetHost, config.TargetPath))
    
    return strings.Join(args, " ")
}

// VerifySync compares source and target after transfer
func (e *SyncEngine) VerifySync(ctx context.Context, config *SyncConfig) error {
    // 1. Get file list + checksums from source
    sourceChecksums, err := e.getChecksums(ctx, config.SourceSSH, config.SourcePath)
    // 2. Get file list + checksums from target
    targetChecksums, err := e.getChecksums(ctx, config.TargetSSH, config.TargetPath)
    // 3. Compare — every source file must exist on target with matching checksum
    // 4. Report any mismatches
    // 5. Return error if any critical file mismatch
}
```

### Database Consistency

```go
// MySQL/MariaDB verification after sync
func verifyMySQLSync(sourceSSH, targetSSH SSHExecuter, db DatabaseInfo) error {
    // Compare row counts on key tables
    // Compare checksum on critical tables: CHECKSUM TABLE ...
    // Verify all tables exist on target
}

// PostgreSQL verification
func verifyPostgreSQLSync(sourceSSH, targetSSH SSHExecuter, db DatabaseInfo) error {
    // Compare row counts
    // Verify sequences are in sync
    // Check replication lag is 0
}
```

---

## 8. Rollback Model

### Step-Based Rollback with Status Tracking

```go
// internal/mod/migration/rollback_manager.go — NEW

type RollbackStep struct {
    ID           int    `json:"id"`
    MigrationID  int    `json:"migrationId"`
    StepOrder    int    `json:"stepOrder"`    // Execution order (reverse = rollback order)
    Name         string `json:"name"`         // e.g., "traffic_rollback", "database_promote_source"
    Status       string `json:"status"`       // pending, running, completed, failed, skipped
    Error        string `json:"error,omitempty"`
    StartedAt    *time.Time `json:"startedAt,omitempty"`
    CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

type RollbackResult struct {
    Status       string         `json:"status"` // completed, degraded, failed
    Steps        []RollbackStep `json:"steps"`
    FailedSteps  []string       `json:"failedSteps,omitempty"`
    Warnings     []string       `json:"warnings,omitempty"`
}

type RollbackManager struct {
    repo PipelineRepo
}

// ExecuteRollback runs rollback steps in reverse order
func (m *RollbackManager) ExecuteRollback(ctx context.Context, migrationID int, steps []RollbackStep) *RollbackResult {
    result := &RollbackResult{Status: "completed"}
    
    // Execute in REVERSE order (LIFO)
    for i := len(steps) - 1; i >= 0; i-- {
        step := &steps[i]
        step.Status = "running"
        m.repo.UpdateRollbackStep(ctx, step)
        
        err := m.executeStep(ctx, step)
        if err != nil {
            step.Status = "failed"
            step.Error = err.Error()
            result.FailedSteps = append(result.FailedSteps, step.Name)
            result.Status = "degraded" // At least one step failed
            
            // CRITICAL: Do NOT continue silently
            // Log the failure, record it, but continue trying remaining steps
            // (some rollback steps may be independent)
        } else {
            step.Status = "completed"
        }
        m.repo.UpdateRollbackStep(ctx, step)
    }
    
    if len(result.FailedSteps) > 0 {
        result.Status = "degraded" // Partial rollback
    }
    
    return result
}

// NEVER report "completed" if any critical step failed
func (r *RollbackResult) IsSuccess() bool {
    return r.Status == "completed" && len(r.FailedSteps) == 0
}
```

---

## 9. Observability Model

### Event Bus

```go
// internal/mod/migration/event_bus.go — NEW

type EventLevel string
const (
    EventLevelInfo     EventLevel = "info"
    EventLevelWarning  EventLevel = "warning"
    EventLevelError    EventLevel = "error"
    EventLevelCritical EventLevel = "critical"
)

type MigrationEvent struct {
    ID            int64       `json:"id"`
    MigrationID   int         `json:"migrationId"`
    Sequence      int64       `json:"sequence"`      // Monotonically increasing
    Timestamp     time.Time   `json:"timestamp"`
    Level         EventLevel  `json:"level"`
    Stage         string      `json:"stage"`
    Type          string      `json:"type"`          // e.g., "observation_warning", "auto_rollback_started"
    Message       string      `json:"message"`
    Details       json.RawMessage `json:"details,omitempty"`
    Source        string      `json:"source"`        // e.g., "observation_engine", "cutover_manager"
    CorrelationID string      `json:"correlationId"` // Links related events
}

type EventBus interface {
    Emit(ctx context.Context, event MigrationEvent) error
    GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error)
    Subscribe(migrationID int) <-chan MigrationEvent
    Unsubscribe(migrationID int, ch <-chan MigrationEvent)
}
```

### Observation Auto-Rollback

```go
// internal/mod/migration/observation.go — FIX

func (e *ObservationEngine) Observe(ctx context.Context, config ObservationConfig) error {
    // ... existing monitoring loop ...
    
    // When threshold breached:
    if breached {
        // Emit event
        e.eventBus.Emit(ctx, MigrationEvent{
            MigrationID: config.MigrationID,
            Level:       EventLevelCritical,
            Stage:       "observation",
            Type:        "observation_threshold_breached",
            Message:     fmt.Sprintf("Health score %d below threshold %d", score, config.MinHealthScore),
            Details:     json.RawMessage(`{"healthScore": ...}`),
        })
        
        if config.AutoRollbackOnError {
            // Emit auto-rollback started event
            e.eventBus.Emit(ctx, MigrationEvent{
                MigrationID: config.MigrationID,
                Level:       EventLevelCritical,
                Stage:       "observation",
                Type:        "auto_rollback_started",
                Message:     "Auto-rollback triggered by observation threshold breach",
            })
            
            // Execute actual rollback
            rollbackResult := e.rollbackManager.ExecuteRollback(ctx, config.MigrationID, rollbackSteps)
            
            if rollbackResult.IsSuccess() {
                e.eventBus.Emit(ctx, MigrationEvent{
                    MigrationID: config.MigrationID,
                    Type:        "auto_rollback_completed",
                    Message:     "Auto-rollback completed successfully",
                })
            } else {
                e.eventBus.Emit(ctx, MigrationEvent{
                    MigrationID: config.MigrationID,
                    Level:       EventLevelCritical,
                    Type:        "auto_rollback_failed",
                    Message:     fmt.Sprintf("Auto-rollback degraded/failed: %v", rollbackResult.FailedSteps),
                })
            }
            
            return fmt.Errorf("observation threshold breached, auto-rollback executed (status: %s)", rollbackResult.Status)
        }
        
        // If AutoRollback is OFF, still return error (don't falsely succeed)
        return fmt.Errorf("observation threshold breached: health=%d error_rate=%.1f%% latency=%dms", 
            score, errorRate, avgLatency)
    }
    
    // If thresholds were breached during observation but recovered, still warn
    if consecutiveFailures > 0 {
        e.eventBus.Emit(ctx, MigrationEvent{
            MigrationID: config.MigrationID,
            Level:       EventLevelWarning,
            Type:        "observation_warning",
            Message:     fmt.Sprintf("Observation completed with %d threshold breaches during window", consecutiveFailures),
        })
    }
    
    return nil
}
```

---

## 10. Frontend UX Model

### Backend-Driven State Machine

```
Frontend actions → REST API → Backend executes → WebSocket events → Frontend updates

NEVER:
  confirmCutover() → local state change
  commitMigration() → local state change

ALWAYS:
  confirmCutover() → POST /migrations/{id}/actions/cutover → backend executes → WS events → frontend advances
  commitMigration() → POST /migrations/{id}/actions/commit → backend executes → WS events → frontend advances
  pausePipeline()   → POST /migrations/{id}/actions/pause  → backend pauses → WS event → frontend shows paused
  rollbackPipeline() → POST /migrations/{id}/actions/rollback → backend rolls back → WS events → frontend shows rolled_back
```

### Action API Endpoints

```
POST /migrations/{id}/actions/pause     → Pause pipeline (real pause, not cancel)
POST /migrations/{id}/actions/resume    → Resume from checkpoint
POST /migrations/{id}/actions/cancel    → Cancel + optional rollback
POST /migrations/{id}/actions/cutover   → Execute cutover sequence
POST /migrations/{id}/actions/commit    → Finalize migration
POST /migrations/{id}/actions/rollback  → Execute rollback
POST /migrations/{id}/actions/retry     → Retry current failed step
```

### WebSocket Reconnection

```typescript
// pipeline.ts — EXTEND

export function wsPipelineConnect(
    migrationId: number,
    onMessage: (msg: WSMessageExtended) => void,
    onStatusChange: (status: 'connected' | 'reconnecting' | 'disconnected') => void,
    opts?: { maxRetries?: number; initialDelay?: number }
): { close: () => void } {
    let retries = 0;
    let lastSequence = 0;
    let ws: WebSocket;
    const maxRetries = opts?.maxRetries ?? 10;
    const initialDelay = opts?.initialDelay ?? 1000;

    function connect() {
        onStatusChange(retries > 0 ? 'reconnecting' : 'connected');
        ws = new WebSocket(wsUrl(`/ws/pipeline/${migrationId}?after_seq=${lastSequence}`));
        
        ws.onmessage = (event) => {
            const msg = JSON.parse(event.data);
            if (msg.sequence) lastSequence = msg.sequence;
            retries = 0; // Reset on successful message
            onStatusChange('connected');
            onMessage(msg);
        };
        
        ws.onclose = () => {
            if (retries < maxRetries) {
                retries++;
                const delay = Math.min(initialDelay * Math.pow(2, retries), 30000);
                onStatusChange('reconnecting');
                setTimeout(connect, delay);
            } else {
                onStatusChange('disconnected');
            }
        };
        
        ws.onerror = () => { /* same backoff as close */ };
    }
    
    connect();
    return { close: () => { retries = maxRetries; ws.close(); } };
}
```

### Rollback Always Visible

```svelte
<!-- Show rollback whenever migration is in a non-terminal state -->
{#if session && !['completed', 'rolled_back', 'cancelled'].includes(session.status)}
  <button on:click={rollbackPipeline} class="rollback-button">
    Rollback
  </button>
{/if}

<!-- REST fallback when WebSocket is disconnected -->
{#if wsStatus === 'disconnected'}
  <div class="warning-banner">
    WebSocket disconnected. Actions still available via REST API.
  </div>
{/if}
```

---

## 11. Backend Implementation Plan

### Phase 0: Validation & Test Reproduction (Week 1)

| Task | File | Detail |
|------|------|--------|
| 0.1 | `pipeline_test.go` | Add test that reproduces C4: call `initialSyncStage.Execute()` with nil registry → expect clear error, not panic |
| 0.2 | `sync_test.go` | NEW: Add test that verifies `buildRsyncCommand()` includes remote destination syntax |
| 0.3 | `cutover_test.go` | NEW: Add test that verifies `freezeWrites()` is not a no-op |
| 0.4 | `observation_test.go` | NEW: Add test that verifies auto-rollback is triggered on threshold breach |
| 0.5 | `provision_test.go` | NEW: Add test that `verified=true` only after verification passes |
| 0.6 | `recovery_test.go` | NEW: Add test that rollback failure is not reported as success |
| 0.7 | `rollback_manager_test.go` | NEW: Test step-based rollback with partial failures |

### Phase 1: P0 Critical Fixes (Weeks 2-3)

| Task | File | Detail | Test |
|------|------|--------|------|
| 1.1 | `sync.go` | **REWRITE** `buildRsyncCommand()` — add remote SSH destination. Add `SyncConfig.TargetHost/Port/User`. Execute rsync on sourceSSH with `-e ssh` flag pointing to target. | Integration test with two Docker containers simulating source→target |
| 1.2 | `sync.go` | Add `VerifySync()` — compare checksums on source and target SSH sessions | Unit test with mock SSH |
| 1.3 | `sync.go` | Add `SyncStrategy` enum — `rsync_over_ssh` (default), `tar_stream`, `pull_from_target` | Unit test for each strategy |
| 1.4 | `sync.go` | Support: bandwidth limit, exclude/include, checksum, dry-run, progress events, retry, permission/owner/group/symlink/sparse preservation | Per-feature unit test |
| 1.5 | `freeze.go` | **NEW** `FreezeManager` — implement `FreezeWrites()` per database type: MySQL `FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only=ON`, PostgreSQL `SET default_transaction_read_only=on`, Redis usage detection + BullMQ worker pause, MongoDB `fsyncLock()` | Per-database unit test + integration test |
| 1.6 | `freeze.go` | Implement `UnfreezeWrites()` for rollback | Unit test |
| 1.7 | `freeze.go` | Implement `verifyFreeze()` — attempt test write, confirm blocked | Integration test |
| 1.8 | `cutover.go` | Replace no-op `freezeWrites()` with `FreezeManager.FreezeWrites()` call | Unit test |
| 1.9 | `cutover.go` | Fix final-sync guard bug (A1) — check `e.replication` not `e.sync` | Unit test |
| 1.10 | `cutover.go` | Add `UnfreezeWrites()` call in rollback path | Unit test |
| 1.11 | `observation.go` | Add auto-rollback execution when threshold breached and `AutoRollback=true` | Unit test |
| 1.12 | `observation.go` | Fix false success when `AutoRollback=false` (A4) — return error if thresholds breached, even if duration expires | Unit test |
| 1.13 | `observation.go` | Emit events: `observation_started`, `observation_warning`, `observation_failed`, `auto_rollback_started/completed/failed` | Unit test |
| 1.14 | `pipeline.go` | Fix C4: assign `defaultRegistry = registry` in `NewPipeline()` | Unit test |
| 1.15 | `pipeline.go` | Fix A2: `initialSyncStage` must return error if categories were skipped | Unit test |
| 1.16 | `pipeline.go` | Fix A3: add `p.mu.Lock()` around stage iteration in `Execute()` | Unit test |
| 1.17 | `pipeline.go` | Fix A5: handle persistence errors — fail stage if critical DB write fails | Unit test |
| 1.18 | `pipeline.go` | Add panic recovery in `Execute()` — catch panic, set state to `failed` with clear message | Unit test |
| 1.19 | `pipeline_handler.go` | Add action endpoints: `POST /migrations/{id}/actions/cutover`, `commit`, `rollback`, `pause`, `resume`, `cancel`, `retry` | API test |
| 1.20 | `pipeline_handler.go` | Add `GET /migrations/{id}/events?after_seq=N` for event replay | API test |
| 1.21 | `verifier.go` | **NEW** Verification framework — `VerifyProvision()` checks: binary exists, service running, port listening, health endpoint, DB reachable, Redis reachable, nginx config valid, SSL cert valid | Per-check unit test |
| 1.22 | `provision.go` | Replace unconditional `verified=true` with `Verifier.VerifyProvision()` call | Unit test |
| 1.23 | `rollback_manager.go` | **NEW** Step-based rollback with `RollbackStep` tracking, `RollbackResult` with `degraded`/`failed` status | Unit test |
| 1.24 | `recovery.go` | Fix C7: use `RollbackManager`, never report success if rollback steps failed | Unit test |
| 1.25 | `event_bus.go` | **NEW** Sequenced event system — `Emit()`, `GetEvents()`, `Subscribe()`, persistence to `migration_events` table | Unit test |
| 1.26 | `pipeline_models.go` | Add models: `MigrationEvent`, `RollbackStep`, `VerificationResult`, `FreezeResult` | — |
| 1.27 | `pipeline_repo.go` | Fix H4: replace `LastInsertId()` with `RETURNING id` in `CreateStage()` | Unit test |
| 1.28 | `pipeline_repo.go` | Add repo methods: `CreateEvent()`, `GetEvents()`, `CreateRollbackStep()`, `UpdateRollbackStep()`, `CreateVerificationResult()` | Unit test |

### Phase 2: P1 Reliability & Security (Weeks 4-6)

| Task | File | Detail | Test |
|------|------|--------|------|
| 2.1 | `pipeline.ts` | **EXTEND** WebSocket with reconnect, exponential backoff, event replay via `?after_seq=N` | Frontend test |
| 2.2 | `+page.svelte` | Wire `confirmCutover()` → `POST /actions/cutover`, `commitMigration()` → `POST /actions/commit` | Frontend test |
| 2.3 | `+page.svelte` | Show rollback button whenever migration is non-terminal (not just when WS connected) | Manual test |
| 2.4 | `+page.svelte` | Add WS connection status indicator (connected/reconnecting/disconnected) | Manual test |
| 2.5 | `+page.svelte` | REST fallback for all actions when WS disconnected | Frontend test |
| 2.6 | `pipeline_handler.go` | Fix H3: implement real pause using channel-based signaling, not Cancel() | Unit test |
| 2.7 | `pipeline.go` | Add `Pause()` / `Resume()` methods with `pauseCh` / `resumeCh` channels | Unit test |
| 2.8 | `traffic.go` | Fix Cloudflare: persist rollback config after `switchCloudflare()` | Unit test |
| 2.9 | `traffic.go` | Fix Traefik: rollback restores to `traefik.yml`, not `dynamic.yml` | Unit test |
| 2.10 | `traffic.go` | Fix Docker: rollback uses stored `ComposePath` | Unit test |
| 2.11 | `traffic.go` | Add `TrafficProvider` interface with `Validate()`, `Switch()`, `Rollback()`, `Verify()` | Unit test |
| 2.12 | `replication.go` | Fix H6: PostgreSQL lag — run query on target standby, use `now() - pg_last_xact_replay_timestamp()` | Unit test |
| 2.13 | `replication.go` | Add Redis usage detection (`detectRedisUsage()`) | Unit test |
| 2.14 | `replication.go` | Add BullMQ workflow: pause worker → sync Redis → start worker → resume | Integration test |
| 2.15 | `queue.go` | Fix H7: replace `DUMP/ShellQuote/RESTORE` with `redis-cli --rdb` + `--pipe` for safe binary transfer | Integration test |
| 2.16 | `queue.go` | Don't ignore restore errors | Unit test |
| 2.17 | `queue.go` | Fix `RedisHost` variable usage inconsistency | Unit test |
| 2.18 | `knownhosts.go` | Add explicit fingerprint verification flow for first connection | Unit test |
| 2.19 | `knownhosts.go` | Detect host key change and block connection | Unit test |
| 2.20 | `pipeline.ts` | Move WS auth from URL query string to subprotocol or short-lived ticket | Security review |
| 2.21 | `client.ts` | Add `HttpOnly` + `SameSite` cookie support for HTTP auth | Security review |
| 2.22 | `+page.svelte` | Fix new-migration dead-end: add retry button after planning failure | Frontend test |
| 2.23 | `+page.svelte` | Fix stale metrics: use `!== undefined` checks instead of truthy checks | Frontend test |
| 2.24 | `+page.svelte` | Fix "Ready for Cutover" vs `canProceed(7)` mismatch — use same source of truth | Frontend test |
| 2.25 | `compatibility.go` | Add target port conflict detection — scan target ports, compare with source | Unit test |
| 2.26 | `pipeline.go` | Fix persistence error handling — fail stage if critical DB write fails | Unit test |
| 2.27 | `docker.go` | Extend container collection: ports, volumes, networks, restart policy, healthcheck, command/entrypoint | Unit test |
| 2.28 | `docker.go` | Extend container recreation: include all collected attributes | Integration test |
| 2.29 | `risk_engine.go` | Add confidence score (0-100%), rollback feasibility (easy/moderate/hard/impossible) | Unit test |
| 2.30 | `+page.svelte` | Fix nested button-in-link accessibility issue in migration list | Manual test |

### Phase 3: P2 Discovery & Planner Expansion (Weeks 7-9)

| Task | File | Detail |
|------|------|--------|
| 3.1 | `collectors_user.go` | **NEW** Users, groups, SSH config collector |
| 3.2 | `collectors_package.go` | **NEW** Packages, repositories collector |
| 3.3 | `collectors_security.go` | **NEW** Firewall (ufw/iptables/firewalld), SELinux, AppArmor collector |
| 3.4 | `collectors_cron.go` | **NEW** Cron jobs, systemd timers collector |
| 3.5 | `collectors_runtime.go` | **NEW** Node.js, Python, Go, Java, PHP, Ruby, Rust, .NET, Bun, Deno version detection |
| 3.6 | `collectors_queue.go` | **NEW** RabbitMQ, Kafka, NATS, ActiveMQ detection |
| 3.7 | `collectors_monitoring.go` | **NEW** Prometheus, Grafana, Loki, Jaeger detection |
| 3.8 | `collectors_ci.go` | **NEW** GitHub Actions, GitLab CI, Jenkins detection |
| 3.9 | `collectors_docker_ext.go` | **NEW** Dockerfile, multi-stage builds, networks, volumes, healthchecks, restart policies, compose file contents |
| 3.10 | `collectors_service.go` | **EXTEND** Apache, Caddy, Traefik, HAProxy, Envoy detection |
| 3.11 | `collectors_basic.go` | **EXTEND** Swap, filesystem type, locale, inode usage |
| 3.12 | `model.go` | **EXTEND** New data types for all collectors |
| 3.13 | `workload_classifier.go` | **NEW** Classify workloads: stateless, stateful, database, cache, queue, worker, cron, reverse_proxy, monitoring, storage, internal, external, shared, unknown |
| 3.14 | `graph.go` | **EXTEND** Add workload type to `DependencyNode`, add edges for: env var connections, volume sharing, DNS dependencies |
| 3.15 | `compat.go` | **EXTEND** Target port conflicts, filesystem compatibility, firewall reachability, runtime version checks |
| 3.16 | `compatibility.go` | **EXTEND** AppArmor checks, package conflict detection, missing dependency detection |
| 3.17 | `planner/` | Fix H10: emit `unsupported_workload` warnings instead of silently skipping |
| 3.18 | `planner/` | Use workload classification to determine migration order |
| 3.19 | `strategy_selector.go` | **NEW** Select migration strategy based on workload type, data size, DB type, queue type, risk score |
| 3.20 | `risk_engine.go` | Add external dependency weighting, maintenance window compatibility |

### Phase 4: UX & Quality (Weeks 10-11)

| Task | File | Detail |
|------|------|--------|
| 4.1 | `+page.svelte` | Add per-step: estimated duration, expected output, help text, documentation links |
| 4.2 | `+page.svelte` | Surface backend error details with remediation text |
| 4.3 | `+page.svelte` | Add responsive breakpoints for dashboard grids |
| 4.4 | `+page.svelte` | Add `aria-label` attributes, replace `window.confirm()` with modal component |
| 4.5 | `+page.svelte` | Add persistent warning banners (not just toasts) |
| 4.6 | `+page.svelte` | Add filterable log viewer |
| 4.7 | `pipeline_handler.go` | Extend export report: downtime calculation, warning/error rollup, recommendations |
| 4.8 | `pipeline.go` | Add structured logging with context (migration ID, stage, server) |
| 4.9 | `pipeline.go` | Emit audit events on every state transition |
| 4.10 | Monitoring | Add CPU/RAM/Disk/Network metrics to WebSocket events |
| 4.11 | `+page.svelte` | Add dependency graph visualization |
| 4.12 | `+page.svelte` | Add verification checklist UI for provisioning step |

---

## 12. Frontend Implementation Plan

### Phase 1: Wire Actions to Backend

| Component | Change | Detail |
|-----------|--------|--------|
| `pipeline.ts` | Add action methods | `cutover(id)`, `commit(id)`, `rollback(id)`, `pause(id)`, `resume(id)`, `cancel(id)`, `retry(id)` — all via REST POST |
| `pipeline.ts` | Add event replay | `getEvents(id, afterSeq)` — REST GET for reconnection |
| `+page.svelte` | Replace `confirmCutover()` | Call `pipelineApi.cutover(id)`, advance only on backend success |
| `+page.svelte` | Replace `commitMigration()` | Call `pipelineApi.commit(id)`, advance only on backend success |
| `+page.svelte` | Rollback always visible | Show when `session.status` is non-terminal, not just when WS connected |
| `+page.svelte` | Action buttons via REST | Pause/Resume/Cancel/Rollback all call REST API, not just WS |

### Phase 2: WebSocket Reliability

| Component | Change | Detail |
|-----------|--------|--------|
| `pipeline.ts` | Reconnect logic | Exponential backoff, max 10 retries, `onStatusChange` callback |
| `pipeline.ts` | Event replay | On reconnect, call `getEvents(id, lastSeq)` to fill gap |
| `pipeline.ts` | Heartbeat | Send ping every 30s, expect pong, detect stale connection |
| `+page.svelte` | Connection indicator | Show "Connected" / "Reconnecting..." / "Disconnected" in header |
| `+page.svelte` | REST fallback | When disconnected, actions still work via REST, show "Offline mode" banner |

### Phase 3: Error Handling & UX

| Component | Change | Detail |
|-----------|--------|--------|
| `+page.svelte` | Error details | Surface backend error messages with remediation text |
| `+page.svelte` | Persistent warnings | Show warnings in a banner, not just toast |
| `+page.svelte` | Per-step info | Add estimated duration, expected output, help icon with tooltip |
| `+page.svelte` | Verification checklist | Show provisioning verification results as a checklist |
| `+page.svelte` | Rollback timeline | Show rollback steps with status (completed/failed/pending) |
| `new/+page.svelte` | Retry after failure | Show retry button when planning fails |
| `+page.svelte` | Responsive design | Add `sm:` / `md:` / `lg:` breakpoints to grid layouts |
| `+page.svelte` | Accessibility | Add `aria-label` to icon buttons, replace `window.confirm()` with modal |

---

## 13. Database Migration Plan

### New Tables

```sql
-- Migration events (for event replay and audit)
CREATE TABLE IF NOT EXISTS migration_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL,
    sequence INTEGER NOT NULL,
    timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    level TEXT NOT NULL DEFAULT 'info',
    stage TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    details TEXT DEFAULT '{}',
    source TEXT NOT NULL DEFAULT '',
    correlation_id TEXT DEFAULT '',
    FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
);
CREATE INDEX idx_events_migration_seq ON migration_events(migration_id, sequence);

-- Rollback steps (step-based rollback tracking)
CREATE TABLE IF NOT EXISTS migration_rollback_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL,
    step_order INTEGER NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT DEFAULT '',
    started_at DATETIME,
    completed_at DATETIME,
    FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
);

-- Verification results
CREATE TABLE IF NOT EXISTS migration_verifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL,
    component TEXT NOT NULL,
    check_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    message TEXT DEFAULT '',
    checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
);

-- Workload classification
CREATE TABLE IF NOT EXISTS migration_workloads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL,
    server_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'unknown',
    category TEXT NOT NULL DEFAULT '',
    details TEXT DEFAULT '{}',
    FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
);

-- Freeze records
CREATE TABLE IF NOT EXISTS migration_freezes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    migration_id INTEGER NOT NULL,
    method TEXT NOT NULL,
    frozen_dbs TEXT DEFAULT '[]',
    frozen_apps TEXT DEFAULT '[]',
    errors TEXT DEFAULT '[]',
    success INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME,
    completed_at DATETIME,
    FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
);
```

### Modified Tables

```sql
-- Add sequence column to migration_audit for event replay
ALTER TABLE migration_audit ADD COLUMN sequence INTEGER DEFAULT 0;
ALTER TABLE migration_audit ADD COLUMN correlation_id TEXT DEFAULT '';
```

**All migrations are additive — no breaking changes to existing schema.**

---

## 14. Testing Plan

### Unit Tests (per P0 fix)

| Fix | Test File | Tests |
|-----|-----------|-------|
| C1 | `sync_test.go` | `TestBuildRsyncCommand_IncludesRemoteDestination`, `TestBuildRsyncCommand_BandwidthLimit`, `TestBuildRsyncCommand_Checksum`, `TestVerifySync_MismatchReturnsError` |
| C2 | `freeze_test.go` | `TestFreezeMySQL_SetsReadOnly`, `TestFreezePostgreSQL_SetsReadOnly`, `TestFreezeRedis_PausesBullMQWorkers`, `TestUnfreeze_ReversesAllFreezes`, `TestVerifyFreeze_ConfirmsBlocked` |
| C3 | `observation_test.go` | `TestObserve_AutoRollbackOnThresholdBreach`, `TestObserve_ReturnsErrorWhenAutoRollbackOff`, `TestObserve_EmitsEventsOnThresholdBreach` |
| C4 | `pipeline_test.go` | `TestInitialSyncStage_NilRegistry_ReturnsError`, `TestNewPipeline_AssignsDefaultRegistry` |
| C5 | (frontend) | Manual test: cutover calls API, not local state |
| C6 | `verifier_test.go` | `TestVerify_DockerInstalled`, `TestVerify_ServiceRunning`, `TestVerify_PortListening`, `TestVerify_HealthEndpoint` |
| C7 | `rollback_manager_test.go` | `TestRollback_StepFailure_StatusDegraded`, `TestRollback_AllStepsFail_StatusFailed`, `TestRollback_NeverReportsSuccessOnFailure` |

### Integration Tests

| Test | Setup | Validates |
|------|-------|-----------|
| Remote-to-remote sync | Two Docker containers (source + target) with test files | Data actually arrives on target |
| Full cutover flow | Source: MySQL + Redis + app + Nginx. Target: empty | Write freeze works, data syncs, traffic switches |
| Rollback flow | Same as above, but trigger rollback after cutover | Source restored, traffic back |
| Observation auto-rollback | Target has broken app | Auto-rollback triggers, source restored |
| BullMQ migration | Source: Redis + BullMQ worker + queue jobs | Workers pause, data syncs, workers resume |

### Failure Injection Tests

| Test | Failure | Expected |
|------|---------|----------|
| Network failure during sync | Kill network mid-transfer | Sync retries or reports clear error |
| Disk full on target | Fill target disk | Sync reports error, no data corruption |
| SSH connection lost | Kill SSH mid-operation | Pipeline marks as interrupted, can resume |
| Database crash during replication | Kill MySQL on source | Replication reports error, pipeline pauses |
| Partial rollback failure | Make one rollback step fail | Status = "degraded", failed step visible |

### Simulation Lab

```yaml
# docker-compose.test.yml
version: "3.8"
services:
  source:
    build: ./testlab/source
    ports: ["2222:22", "3306:3306", "6379:6379", "80:80"]
    # Pre-loaded with: MySQL, Redis, Nginx, sample app, BullMQ worker
  
  target:
    build: ./testlab/target
    ports: ["2223:22"]
    # Clean Ubuntu/Debian, no services
  
  control:
    build: .
    depends_on: [source, target]
    environment:
      - TEST_SOURCE_HOST=source
      - TEST_TARGET_HOST=target
```

---

## 15. Security Plan

| Task | Priority | Detail |
|------|----------|--------|
| SSH known_hosts | P1 | Require explicit fingerprint verification on first connect. Show fingerprint to user. Block on key mismatch. |
| WS auth | P1 | Move token from URL query string to WebSocket subprotocol or short-lived ticket exchange |
| HTTP auth | P2 | Add `HttpOnly` + `SameSite` cookie support alongside bearer token |
| Rate limiting | P2 | Only trust `X-Forwarded-For` from known proxy IPs |
| Secret masking | P1 | Never log env values — only names. Mask credentials in audit events |
| Least privilege | P2 | Document recommended SSH account permissions. Add permission check during discovery |
| Audit logging | P1 | All destructive actions (cutover, rollback, commit, delete) must emit audit events |
| CSP headers | P2 | Add Content-Security-Policy for browser-exposed deployments |

---

## 16. Release Plan

| Release | Phase | Content | Criteria |
|---------|-------|---------|----------|
| v1.5.1 | Phase 1 | All P0 fixes | All P0 acceptance tests pass, no regression |
| v1.6.0 | Phase 2 | P1 reliability + security | WS reconnect works, pause≠cancel, traffic rollback fixed, SSH known_hosts |
| v1.7.0 | Phase 3 | Discovery + planner expansion | 15+ new collectors, workload classification, strategy selector |
| v1.8.0 | Phase 4 | UX + quality | Responsive, accessible, structured logging, verification UI |
| v2.0.0 | Phase 5 | Enterprise | RBAC, K8s, blue-green, canary, multi-user |

---

## 17. Acceptance Criteria

### P0 Acceptance (v1.5.1)

- [ ] **C1:** Data is transferred from source to target and verified via checksums on both hosts
- [ ] **C2:** Writes are actually frozen on MySQL/PostgreSQL/Redis before final sync — verified by test write attempt
- [ ] **C3:** Observation can trigger automatic rollback when health score drops below threshold
- [ ] **C4:** Pipeline cannot panic from nil dependency — returns clear error instead
- [ ] **C5:** Frontend cannot claim commit/cutover success without backend confirmation
- [ ] **C6:** Provisioning `verified=true` only after actual verification checks pass
- [ ] **C7:** Rollback failure is visible and correctly reported as "degraded" or "failed", never as "success"
- [ ] **A1:** Cutover final-sync guard checks `e.replication`, not `e.sync`
- [ ] **A2:** Initial sync returns error if categories were skipped
- [ ] **A4:** Observation returns error if thresholds breached, even when AutoRollback=false
- [ ] All existing unit tests still pass
- [ ] Integration test: remote-to-remote sync with two Docker containers succeeds
- [ ] Integration test: full cutover flow with write freeze succeeds

### P1 Acceptance (v1.6.0)

- [ ] WebSocket reconnects automatically with exponential backoff
- [ ] Rollback button visible whenever migration is non-terminal
- [ ] Pause/resume are distinct from cancel — pipeline can resume from checkpoint
- [ ] Traffic rollback works for all 7 providers (Cloudflare, Nginx, Traefik, HAProxy, Caddy, Docker, DNS)
- [ ] PostgreSQL lag calculated correctly on target standby
- [ ] BullMQ sync uses safe binary transfer, not shell-quoted DUMP
- [ ] Discovery covers: users, groups, packages, firewall, cron, runtime versions
- [ ] Workload classification produces: stateless, stateful, database, cache, queue, worker, etc.
- [ ] Planner emits warnings for unsupported workload types instead of skipping
- [ ] SSH requires explicit fingerprint verification on first connect
- [ ] WebSocket auth does not expose token in URL
- [ ] New migration flow has retry button after planning failure

---

## 18. Rollout Strategy

### v1.5.1 (P0 fixes)
1. Deploy to staging with Docker simulation lab
2. Run full integration test suite
3. Run failure injection tests
4. Manual test: migrate a real workload (MySQL + Redis + Nginx + app)
5. If all pass → release

### v1.6.0 (P1 fixes)
1. Deploy to staging
2. Test WS reconnect under network instability
3. Test pause/resume/cancel distinction
4. Test traffic provider rollback for each provider
5. Security review: SSH known_hosts, WS auth
6. If all pass → release

### v1.7.0+ (P2 + expansion)
1. Incremental rollout per new collector
2. Each new collector has its own test
3. Planner changes tested against real-world workload patterns
4. If regression → rollback to previous version

### Rollback Plan (for releases)
- Each release is tagged and can be reverted
- Database migrations are additive only — no breaking changes
- Frontend changes are backward-compatible with previous API versions

---

## 19. Risks and Trade-offs

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| rsync over SSH requires target SSH key on source | High | Medium | Use SSH key forwarding or control-plane orchestration. Document setup requirements. |
| Write freeze may not work for all app types | Medium | High | Fall back to app-level maintenance mode. Document limitations. |
| Auto-rollback may trigger unnecessarily | Medium | Medium | Make thresholds configurable. Add cooldown period. Require manual confirmation for production. |
| New discovery collectors may slow down scan | Medium | Low | Run collectors in parallel (already supported). Add timeout per collector. |
| Workload classification may be inaccurate | Medium | Medium | Default to "unknown" with warning. Let user override classification. |
| Breaking existing migration flows | Low | High | All changes are additive. Old API paths still work. Frontend redirects preserved. |
| SQLite concurrency under load | Low | Medium | WAL mode already enabled. Add busy_timeout. Consider PostgreSQL for high-concurrency deployments. |

### Key Trade-offs

1. **rsync over SSH vs tar stream vs control-plane orchestration**
   - Choose: rsync over SSH (simplest, most reliable)
   - Trade-off: requires SSH key setup between source and target
   - Alternative: control-plane reads from source, writes to target (slower, but no inter-host SSH needed)

2. **Auto-rollback on vs off by default**
   - Choose: ON by default, with configurable thresholds
   - Trade-off: may trigger unnecessary rollbacks in flaky environments
   - Mitigation: add cooldown and consecutive-failure threshold

3. **Pause via channel vs context**
   - Choose: channel-based pause (real suspend)
   - Trade-off: more complex than context cancellation
   - Benefit: can actually resume

4. **Event replay vs full state sync**
   - Choose: event replay with sequence numbers
   - Trade-off: events may be lost if DB fails
   - Mitigation: persist events to DB before sending to WS

---

## 20. Final Checklist

### Phase 0 — Before Starting
- [ ] All P0 bugs reproduced with failing tests
- [ ] Development environment set up with Docker simulation lab
- [ ] Feature freeze — no new features until P0 fixed
- [ ] Branch strategy: `fix/p0-sync-engine`, `fix/p0-freeze-writes`, etc.

### Phase 1 — P0 Fixes
- [ ] C1: Sync engine rewritten for remote-to-remote
- [ ] C1: VerifySync() implemented and tested
- [ ] C2: FreezeManager implemented per database type
- [ ] C2: Freeze verification (test write blocked) works
- [ ] C3: Observation auto-rollback executes actual rollback
- [ ] C3: Observation returns error on threshold breach even without auto-rollback
- [ ] C4: defaultRegistry assigned in NewPipeline()
- [ ] C4: Panic recovery in Execute()
- [ ] C5: Frontend cutover/commit wired to backend API
- [ ] C6: Verification framework implemented
- [ ] C6: verified=true only after verification passes
- [ ] C7: RollbackManager with step tracking
- [ ] C7: Rollback failure never reported as success
- [ ] A1: Cutover final-sync guard fixed
- [ ] A2: Initial sync returns error on skipped categories
- [ ] A3: Race condition on p.stages fixed
- [ ] A4: Observation false success fixed
- [ ] A5: Persistence errors handled
- [ ] All P0 acceptance tests pass
- [ ] Integration test with Docker lab passes
- [ ] Release v1.5.1

### Phase 2 — P1 Fixes
- [ ] WebSocket reconnect with backoff
- [ ] Rollback always visible
- [ ] Real pause/resume (not cancel)
- [ ] Traffic rollback fixed for all providers
- [ ] PostgreSQL lag calculation fixed
- [ ] BullMQ safe sync
- [ ] Discovery: users, packages, firewall, cron, runtimes
- [ ] Workload classification
- [ ] Planner warns on unsupported types
- [ ] SSH known_hosts verification
- [ ] WS auth not in URL
- [ ] New migration retry button
- [ ] All P1 acceptance tests pass
- [ ] Release v1.6.0

### Phase 3 — P2 Expansion
- [ ] 15+ new discovery collectors
- [ ] Compatibility checks expanded
- [ ] Strategy selector implemented
- [ ] Risk engine: confidence, rollback feasibility
- [ ] Release v1.7.0

### Phase 4 — UX & Quality
- [ ] Per-step info (duration, help, docs)
- [ ] Error details with remediation
- [ ] Responsive design
- [ ] Accessibility improvements
- [ ] Structured logging
- [ ] Verification checklist UI
- [ ] Release v1.8.0

---

**Plan ini bersifat living document. Update saat implementasi berjalan, terutama jika ditemukan bug baru atau asumsi perlu direvisi.**
