# Meshium — Production Readiness Audit v1.5.0

**Tanggal:** 2026-07-03  
**Scope:** Seluruh platform zero-downtime migration — backend, frontend, WebSocket, API, engine, discovery, compatibility, risk, planning, execution, monitoring, rollback, export, logging, database, Docker, security, UX, testing, documentation  
**Metode:** Code review menyeluruh terhadap semua source file oleh 4 independent auditor  

---

## Executive Summary

Meshium memiliki **fondasi arsitektur yang solid** — 14-stage pipeline engine, 11-step wizard UI, state machine, dependency graph, dan 10 specialized engine. Namun, platform ini **belum production-ready** untuk migrasi server nyata. Dari 5 area audit, ditemukan **7 critical bugs**, **18 high-priority gaps**, dan **30+ medium-priority improvements**.

**Verdict:** Platform ini adalah **prototype/scaffold** yang berfungsi secara arsitektural, tetapi memiliki celah kritis di beberapa area fundamental — terutama sync engine yang tidak benar-benar melakukan remote-to-remote transfer, cutover yang tidak benar-benar freeze writes, dan observation engine yang tidak benar-benar auto-rollback.

---

## 🔴 Critical (P0) — Harus diperbaiki sebelum production

### C1. Sync engine tidak melakukan remote-to-remote migration
**File:** `internal/mod/migration/sync.go`  
**Masalah:** `buildRsyncCommand()` membangun command `rsync -avz /source/ /target/` yang dijalankan di source SSH session. Ini menyalin **local path ke local path di source host**, BUKAN dari source host ke target host.  
**Dampak:** Seluruh file sync — fondasi dari zero-downtime migration — tidak berfungsi. Data tidak pernah sampai ke target.  
**Rekomendasi:** Gunakan rsync over SSH ke target: `rsync -avz -e "ssh -p TARGET_PORT" /source/ user@target:/target/`, atau gunakan transport layer yang sudah ada untuk meng-orkestrasi source→target transfer.

### C2. Cutover `freezeWrites()` adalah no-op placeholder
**File:** `internal/mod/migration/cutover.go`  
**Masalah:** Fungsi `freezeWrites()` tidak melakukan apa-apa. Tidak ada write freeze di level aplikasi maupun database.  
**Dampak:** Selama cutover, writes tetap berjalan di source. Data bisa berubah setelah final sync, menyebabkan data loss.  
**Rekomendasi:** Implementasi real write freeze per database type:
- MySQL: `FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only = ON;`
- PostgreSQL: `SET default_transaction_read_only = on;` atau `pg_ctl pause`
- Redis: `CONFIG SET stop-writes-on-bgsave-error yes` + pause client writes
- App-level: scale down workers, enable maintenance mode

### C3. Observation engine tidak benar-benar auto-rollback
**File:** `internal/mod/migration/observation.go`  
**Masalah:** `Observe()` hanya return error saat threshold breached. Tidak ada actual rollback execution.  
**Dampak:** Jika health score drop setelah cutover, tidak ada mekanisme otomatis untuk restore. Operator harus manual rollback.  
**Rekomendasi:** Implementasi auto-rollback yang memanggil `cutover.Rollback()` saat threshold breached dan `config.AutoRollbackOnError = true`.

### C4. Pipeline akan panic di `initialSyncStage`
**File:** `internal/mod/migration/pipeline.go`  
**Masalah:** Global `defaultRegistry` tidak pernah di-assign di `NewPipeline()`. Ketika `initialSyncStage` dijalankan, akan panic karena nil pointer dereference.  
**Dampak:** Pipeline crash saat sampai ke stage InitialSync.  
**Rekomendasi:** Assign registry di `NewPipeline()` atau hapus global registry dan gunakan dependency injection.

### C5. Cutover & commit di frontend adalah UI-only tanpa backend call
**File:** `web/src/routes/migrations/[id]/pipeline/+page.svelte`  
**Masalah:** `confirmCutover()` hanya set `cutoverConfirmed = true` lokal. `commitMigration()` hanya mutasi local state. Tidak ada API call ke backend.  
**Dampak:** UI bisa menampilkan "Migration Complete" padahal backend belum benar-benar switch traffic atau commit.  
**Rekomendasi:** Ganti dengan explicit API call: `pipelineApi.confirmCutover(id)` dan `pipelineApi.commit(id)`. Advance wizard hanya setelah backend acknowledgement.

### C6. Provisioning marks `verified=true` tanpa verification
**File:** `internal/mod/migration/provision.go`  
**Masalah:** `Provision()` set `installed=true`, `configured=true`, dan `verified=true` unconditionally setelah install, tanpa melakukan verifikasi apapun.  
**Dampak:** Component yang gagal install atau salah konfigurasi akan dilaporkan sebagai "verified". Pipeline lanjut ke stage berikutnya dengan target yang tidak siap.  
**Rekomendasi:** Implementasi actual verification per component: check binary exists, service running, port listening, health endpoint responding.

### C7. Rollback failures dilaporkan sebagai success
**File:** `internal/mod/migration/recovery.go`, `cutover.go`  
**Masalah:** Rollback errors di-log sebagai warning dan pipeline tetap melaporkan success. Partial rollback bisa terjadi tanpa indikasi.  
**Dampak:** Target bisa dalam state partial/inconsistent sementara operator percaya rollback berhasil.  
**Rekomendasi:** Fail pipeline ketika rollback gagal. Surface rollback errors ke UI. Jangan clear checkpoint sampai rollback benar-benar complete.

---

## 🟠 High Priority (P1) — Harus diperbaiki sebelum migrasi production pertama

### H1. WebSocket tidak ada reconnection
**File:** `web/src/lib/api/pipeline.ts`, `+page.svelte`  
**Masalah:** WebSocket close/error → `pipelineRunning = false` → tidak ada reconnect.  
**Dampak:** Network glitch saat cutover = operator kehilangan kontrol.  
**Rekomendasi:** Exponential backoff reconnect + rehydrate state dari backend session.

### H2. Rollback button hilang saat WebSocket disconnect
**File:** `+page.svelte` lines 511-528  
**Masalah:** Rollback hanya visible saat `pipelineRunning || pipelinePaused`. Disconnect → `pipelineRunning = false` → rollback button hilang.  
**Dampak:** Operator kehilangan akses rollback saat paling dibutuhkan.  
**Rekomendasi:** Keep rollback visible sampai backend confirm terminal state.

### H3. Pause API sebenarnya cancel, bukan pause
**File:** `internal/mod/migration/pipeline_handler.go`  
**Masalah:** Pause endpoint memanggil context cancel, yang menghentikan pipeline secara permanen. Resume tidak bisa menghidupkan kembali context yang sudah cancelled.  
**Dampak:** Pause → Resume tidak bekerja. Pipeline harus restart dari awal.  
**Rekomendasi:** Implementasi real pause menggunakan channel-based signaling, bukan context cancellation.

### H4. Resume/retry stage creation unsafe
**File:** `internal/mod/migration/pipeline_repo.go`  
**Masalah:** Stage creation menggunakan `INSERT ... ON CONFLICT` tapi masih bergantung pada `LastInsertId()`. Pada conflict, ID yang dikembalikan bukan ID row yang existing.  
**Dampak:** Resumed stages bisa mendapat ID salah, menyebabkan data corruption.  
**Rekomendasi:** Gunakan `RETURNING id` atau query ID setelah upsert.

### H5. Traffic rollback broken untuk beberapa provider
**File:** `internal/mod/migration/traffic.go`  
**Masalah:**  
- Cloudflare: rollback config tidak tersimpan di DB  
- Traefik: rollback menulis ke file yang salah (`dynamic.yml` bukan `traefik.yml`)  
- Docker: rollback selalu write ke `docker-compose.yml`, ignore original compose path  
- DNS: hanya alias ke Cloudflare, bukan real DNS implementation  
**Dampak:** Rollback traffic tidak bekerja untuk 4 dari 7 provider.  
**Rekomendasi:** Fix per-provider: simpan original config + path dengan benar, rollback ke file yang sama.

### H6. PostgreSQL replication lag calculation salah
**File:** `internal/mod/migration/replication.go`  
**Masalah:** Lag calculation menjalankan `pg_last_xact_replay_timestamp()` di SOURCE, bukan di target standby. Ini bukan cara valid mengukur standby lag.  
**Dampak:** Lag bisa dilaporkan 0 padahal replication belum catch up. Cutover bisa terjadi dengan data inconsistent.  
**Rekomendasi:** Hitung lag di target: `SELECT now() - pg_last_xact_replay_timestamp() AS lag;`

### H7. Queue engine tidak aman untuk BullMQ
**File:** `internal/mod/migration/queue.go`  
**Masalah:**  
- `syncBullMQ()` menggunakan `DUMP` binary payload yang di-shell-quote — tidak safe untuk binary Redis data  
- Menggunakan `RedisHost` yang sama untuk source dan target  
- Tidak ada worker pause/resume workflow  
**Dampak:** Queue sync bisa corrupt data. BullMQ jobs bisa duplicate atau lost.  
**Rekomendasi:** Gunakan `redis-cli --rdb` / `--pipe` untuk safe Redis sync. Implementasi BullMQ-specific: pause worker → sync → start worker → resume.

### H8. Discovery tidak detect: users, groups, packages, firewall, cron, swap, filesystem, runtime versions
**File:** `internal/mod/discovery/collectors_*.go`  
**Masalah:** Model punya field `Packages` dan `Users` tapi tidak ada collector yang mengisinya. Tidak ada collector untuk: users, groups, packages, repos, firewall, AppArmor, cron, swap, filesystem type, env vars, runtime versions (Node/Python/Go/Java/PHP/Ruby/Rust), queue systems, monitoring agents.  
**Dampak:** Migration plan tidak lengkap. Service yang bergantung pada runtime tertentu, cron jobs, atau firewall rules tidak terdeteksi.  
**Rekomendasi:** Tambahkan collectors untuk semua item di atas. Prioritas: packages, users/groups, cron, firewall, runtime versions.

### H9. Tidak ada workload classification
**File:** `internal/mod/discovery/graph.go`, `internal/mod/planner/`  
**Masalah:** Dependency graph hanya mengenali 4 tipe: container, service, database, nginx. Tidak ada workload taxonomy: stateless, stateful, queue, cache, worker, cron, reverse proxy, monitoring, storage, internal service, external dependency, shared service.  
**Dampak:** Planner tidak bisa menentukan migration order yang benar untuk workload kompleks.  
**Rekomendasi:** Tambahkan workload classification berdasarkan: port connections, env vars, volume mounts, labels, process names. Map ke taxonomy yang jelas.

### H10. Planner silently skip unsupported node types
**File:** `internal/mod/planner/`  
**Masalah:** Jika node type tidak punya generator, node di-skip tanpa warning atau blocker.  
**Dampak:** Workload yang tidak di-support hilang dari plan tanpa indikasi. Operator tidak tahu ada yang terlewat.  
**Rekomendasi:** Fail loudly untuk unsupported types. Tambahkan sebagai blocker di compatibility check.

### H11. New migration flow dead-end setelah planning failure
**File:** `web/src/routes/migrations/new/+page.svelte`  
**Masalah:** "Create Migration Plan" button hilang setelah `planMessages.length > 0`. Jika planning gagal, user stuck tanpa retry button.  
**Dampak:** User harus refresh atau navigate ulang.  
**Rekomendasi:** Tambahkan retry button dan error state yang jelas.

### H12. WebSocket token di URL query string
**File:** `web/src/lib/api/pipeline.ts`, `migrations.ts`  
**Masalah:** Bearer token di-append sebagai `?token=...` di WebSocket URL. Token bisa leak ke logs, browser history, dan proxy.  
**Dampak:** Session hijacking risk.  
**Rekomendasi:** Gunakan short-lived WS ticket atau subprotocol-based auth.

### H13. SSH host-key verification auto-accept unknown keys
**File:** `internal/mod/ssh/knownhosts.go`  
**Masalah:** Unknown host keys otomatis di-save dan di-accept. Tidak ada manual fingerprint verification.  
**Dampak:** MITM attack pada koneksi pertama bisa di-trust permanen.  
**Rekomendasi:** Require explicit fingerprint verification atau pre-seed known_hosts.

### H14. Compatibility checker tidak detect target port conflicts
**File:** `internal/mod/migration/compatibility.go`  
**Masalah:** `checkNetworkPorts()` hanya melaporkan jumlah source ports. Tidak check apakah port yang sama sudah digunakan di target.  
**Dampak:** Port conflict tidak terdeteksi sampai saat execution.  
**Rekomendasi:** Scan target ports dan compare dengan source.

### H15. DB write errors diabaikan di stage handlers
**File:** `internal/mod/migration/pipeline.go`  
**Masalah:** Banyak persistence calls mengabaikan error. Stage state bisa tidak tersimpan tanpa indikasi.  
**Dampak:** Pipeline bisa lanjut ke stage berikutnya padahal state tidak terpersist. Recovery setelah crash tidak bisa diandalkan.  
**Rekomendasi:** Fail stage jika persistence gagal. At minimum, log error dan surface ke UI.

### H16. Docker `docker run` recreation tidak preserve ports, volumes, networks, restart policy, healthcheck
**File:** `internal/mod/migration/docker.go`  
**Masalah:** Container recreation hanya include env vars, labels, dan image. Port bindings, volume mounts, networks, restart policy, healthcheck, command/entrypoint, resource limits — semua hilang.  
**Dampak:** Container di target tidak sama dengan source. Service bisa tidak berfungsi.  
**Rekomendasi:** Collect dan recreate semua container attributes. Prioritaskan compose-based migration.

### H17. Risk engine tidak punya confidence score atau rollback feasibility
**File:** `internal/mod/migration/risk_engine.go`  
**Masalah:** Risk report punya score dan class, tapi tidak ada confidence score, rollback feasibility, external dependency weighting, atau maintenance window modeling.  
**Dampak:** Operator tidak punya informasi cukup untuk menilai apakah migration aman dilakukan.  
**Rekomendasi:** Tambahkan: confidence score (0-100%), rollback feasibility (easy/moderate/hard/impossible), external dependency count, maintenance window compatibility.

### H18. Redis usage type tidak terdeteksi
**File:** `internal/mod/migration/replication.go`, `queue.go`  
**Masalah:** Tidak ada detection apakah Redis digunakan sebagai cache, session store, BullMQ backend, queue, pub/sub, lock, atau rate limiter.  
**Dampak:** Strategi migration salah. BullMQ data harus di-sync dengan worker pause workflow, cache data bisa di-drop.  
**Rekomendasi:** Detect Redis usage type dari key patterns. Untuk BullMQ: pause worker → sync → start worker → resume.

---

## 🟡 Medium Priority (P2) — Penting untuk production quality

| # | Area | Masalah | Rekomendasi |
|---|------|---------|-------------|
| M1 | **Discovery** | Tidak detect: MariaDB, Cassandra, CockroachDB, ClickHouse, TimescaleDB, InfluxDB, Elasticsearch, Neo4j | Tambahkan database detectors |
| M2 | **Discovery** | Tidak detect: Apache, Traefik, HAProxy, Caddy, Envoy sebagai reverse proxy | Tambahkan web server collectors |
| M3 | **Discovery** | Tidak detect: RabbitMQ, Kafka, NATS, ActiveMQ sebagai queue | Tambahkan queue detectors |
| M4 | **Discovery** | Tidak detect: Prometheus, Grafana, Loki, Jaeger, Zabbix | Tambahkan monitoring collectors |
| M5 | **Discovery** | Tidak detect runtime versions: Node.js, Python, Go, Java, PHP, Ruby, Rust, Deno, Bun | Tambahkan runtime version collectors |
| M6 | **Discovery** | Tidak detect: PM2, Supervisor, Podman, Containerd, Kubernetes | Tambahkan deployment method detectors |
| M7 | **Discovery** | Tidak detect: Certbot, ACME, wildcard certificates, self-signed certs | Tambahkan SSL/cert collectors |
| M8 | **Discovery** | Tidak detect: Dockerfile, multi-stage builds, overlay networks, named volumes, secrets, configs | Expand Docker collector |
| M9 | **Compatibility** | Filesystem type/mount compatibility tidak di-check | Tambahkan filesystem compatibility check |
| M10 | **Compatibility** | AppArmor/SELinux policy checks tidak ada | Tambahkan security policy checks |
| M11 | **Compatibility** | Package conflict detection tidak ada | Tambahkan package dependency resolution |
| M12 | **Compatibility** | Missing dependency/binary/env var detection tidak ada | Tambahkan prerequisite checks |
| M13 | **Planning** | Tidak ada dependency graph untuk migration order (database → cache → app → worker → proxy → monitoring → cutover) | Gunakan `TopologicalSort()` yang sudah ada di `graph.go` untuk determine execution order |
| M14 | **Planning** | Planner tidak recognize deployment methods: GitHub Actions, GitLab CI, ArgoCD, FluxCD, Terraform, Ansible | Tambahkan CI/CD detection dan migration strategy per method |
| M15 | **Planning** | Planner tidak language-agnostic — tidak detect framework: Next.js, Django, Spring Boot, Laravel, Gin, Phoenix, dll | Detect dari package.json, requirements.txt, go.mod, pom.xml, Gemfile, Cargo.toml |
| M16 | **Execution** | Pipeline tidak idempotent — beberapa operations append config, ignore errors, persist optimistic state | Buat setiap stage operation idempotent dengan check-before-act pattern |
| M17 | **Execution** | Pipeline tidak punya transaction-like behavior — partial failure bisa leave inconsistent state | Implementasi per-stage rollback dan compensation actions |
| M18 | **Monitoring** | CPU/RAM/Disk/Network metrics tidak dikirim via WebSocket | Backend harus kirim resource metrics secara periodik |
| M19 | **Monitoring** | Tidak ada alerting saat threshold exceeded | Tambahkan alert system dengan configurable thresholds |
| M20 | **Monitoring** | Tidak ada queue length trends, ETA calculation, atau progress rollup | Implementasi proper progress tracking |
| M21 | **Rollback** | Partial rollback tidak didukung — hanya full rollback | Tambahkan per-stage rollback capability |
| M22 | **Rollback** | Rollback tidak restore: DNS, Nginx config files, database state, container exact settings | Per-stage rollback harus restore ke known-good state |
| M23 | **Export** | Report tidak punya: downtime calculation, warning/error rollup, recommendations, human-readable summary | Tambahkan ke export endpoint |
| M24 | **Logging** | Tidak ada structured logging — hanya `log.Printf()` | Implementasi structured logger dengan context (migration ID, stage, server) |
| M25 | **Logging** | Audit trail tidak capture semua state transitions | Emit audit event pada setiap meaningful state change |
| M26 | **Testing** | Tidak ada integration/E2E tests | Tambahkan pipeline integration tests dengan real DB |
| M27 | **Testing** | Tidak ada chaos/failure injection tests | Tambahkan: network failure, disk full, low memory, partial rollback |
| M28 | **Testing** | Tidak ada Docker simulation tests | Tambahkan container migration simulation |
| M29 | **Security** | `X-Forwarded-For` trusted tanpa proxy allowlist | Hanya trust header dari known proxies |
| M30 | **Security** | Auth model coarse-grained — "app unlocked" bukan per-user | Pertimbangkan RBAC jika multi-user |
| M31 | **UX** | Error messages generis tanpa remediation | Surface backend error details + remediation text |
| M32 | **UX** | Tidak ada per-step: estimated duration, expected output, help, documentation links | Tambahkan ke setiap wizard step |
| M33 | **UX** | Responsive design tidak adequate — fixed grids tanpa breakpoints | Gunakan responsive breakpoints |
| M34 | **UX** | Accessibility lemah — icon-only controls tanpa aria-labels, window.confirm() | Tambahkan semantic labels dan proper modal |
| M35 | **UX** | Realtime updates bisa stale — zero/empty values tidak clear existing state | Gunakan `!== undefined` checks |

---

## 📊 Audit Scorecard

| Area | Skor | Keterangan |
|------|------|------------|
| **Architecture** | 🟢 7/10 | Modular, extensible, good separation of concerns. State machine dan dependency graph solid. |
| **Discovery** | 🟡 4/10 | OS/hardware/Docker/Nginx/DB basic detection ada. Tapi missing: users, packages, firewall, cron, runtimes, queues, monitoring, workload classification. |
| **Compatibility** | 🟡 4/10 | Basic checks ada (RAM, disk, arch, Docker, ports). Tapi shallow — no target port conflicts, no filesystem/firewall/runtime checks, no AppArmor. |
| **Risk Assessment** | 🟡 5/10 | Quantitative scoring ada. Tapi no confidence, no rollback feasibility, no external dependencies, no maintenance window. |
| **Planning** | 🟡 3/10 | Dependency graph + topological sort ada. Tapi no workload taxonomy, no deployment method awareness, silently skips unsupported types, not language-agnostic. |
| **Provisioning** | 🟡 3/10 | Basic install ada. Tapi verified-without-verification, no SSL/cert/network/volume/repo/healthcheck, not idempotent. |
| **Execution** | 🔴 2/10 | Pipeline orchestration ada. Tapi sync tidak remote-to-remote, freezeWrites no-op, not idempotent, not transaction-like, not restart-safe. |
| **Monitoring** | 🟡 5/10 | Health engine 11 check types. Observation engine ada. Tapi no auto-rollback, no resource metrics via WS, no alerting, no ETA/progress rollup. |
| **Rollback** | 🔴 2/10 | Framework ada. Tapi partial rollback failures reported as success, traffic rollback broken for 4/7 providers, no per-stage rollback, not restart-safe. |
| **Frontend/UX** | 🟡 5/10 | 11-step wizard solid. Tapi cutover/commit UI-only, no WS reconnect, dead-end navigation, generic errors, not responsive/accessible. |
| **Security** | 🟡 5/10 | SSH key encryption at rest excellent (AES-256-GCM, PBKDF2 600k). Tapi token in localStorage+URL, auto-accept SSH keys, X-Forwarded-For spoofing, coarse auth. |
| **Testing** | 🟡 4/10 | Good unit test coverage for state machine, risk, compatibility, health. Tapi no integration/E2E, no chaos, no simulation, no concurrency tests. |
| **Documentation** | 🟢 7/10 | flow.md comprehensive, CHANGELOG detailed. Tapi docs/implementation out of sync untuk auto-rollback story. |

**Overall: 🟡 4.2/10** — Prototype yang architecturally sound, tapi memerlukan significant hardening sebelum bisa digunakan untuk migrasi production.

---

## 🗺️ Roadmap ke Production

### Phase 1: Critical Fixes (1-2 minggu)
Perbaiki semua P0 items:
1. Fix sync engine → remote-to-remote transfer
2. Implement real write freeze per database type
3. Implement observation auto-rollback
4. Fix pipeline panic di initialSyncStage
5. Wire cutover/commit ke backend API
6. Fix provisioning verification
7. Fix rollback error reporting

### Phase 2: Hardening (2-3 minggu)
Perbaiki semua P1 items:
1. WebSocket reconnection + state resumption
2. Real pause/resume semantics
3. Fix traffic rollback per provider
4. Fix PostgreSQL lag calculation
5. Fix BullMQ sync safety
6. Add critical discovery collectors (users, packages, cron, firewall, runtimes)
7. Add workload classification
8. Fix new-migration dead-end
9. Fix security issues (WS auth, SSH host verification)

### Phase 3: Production Quality (3-4 minggu)
Perbaiki P2 items secara bertahap:
1. Expand discovery (more DBs, queues, monitoring, CI/CD)
2. Expand compatibility (filesystem, firewall, runtime, packages)
3. Make execution idempotent and transaction-like
4. Add resource metrics to WebSocket
5. Add structured logging and audit trail
6. Add integration/E2E tests
7. Improve UX (error messages, per-step info, responsive, accessible)
8. Expand export report

### Phase 4: Enterprise Features (ongoing)
1. Kubernetes support
2. Blue-green deployment strategy
3. Canary migration
4. Multi-region migration
5. RBAC dan audit compliance
6. Migration templates untuk common stacks (Next.js+Redis+MySQL, Django+PostgreSQL+Celery, dll)

---

## 🏗️ Desain yang Lebih Baik — Key Architectural Improvements

### 1. Sync Engine: Source→Target Transfer

```go
// SEKARANG (broken):
cmd := fmt.Sprintf("rsync -avz %s/ %s/", sourcePath, targetPath)
// Ini jalan di source SSH, copy local-to-local

// SEHARUSNYA:
func (e *SyncEngine) buildRsyncCommand(config SyncConfig) string {
    // Option A: rsync over SSH dari source ke target
    return fmt.Sprintf(
        "rsync -avz --partial --append-verify -e 'ssh -p %d -o StrictHostKeyChecking=no' %s/ %s@%s:%s/",
        config.TargetPort, config.SourcePath, config.TargetUser, config.TargetHost, config.TargetPath,
    )
    
    // Option B: Orkestrasi dua SSH session — tar di source, pipe ke target
    // source: tar czf - /source/ | target: tar xzf - -C /target/
}
```

### 2. Workload Classification

```go
type WorkloadType string

const (
    WorkloadStateless      WorkloadType = "stateless"
    WorkloadStateful       WorkloadType = "stateful"
    WorkloadDatabase       WorkloadType = "database"
    WorkloadQueue          WorkloadType = "queue"
    WorkloadCache          WorkloadType = "cache"
    WorkloadWorker         WorkloadType = "worker"
    WorkloadCron           WorkloadType = "cron"
    WorkloadReverseProxy   WorkloadType = "reverse_proxy"
    WorkloadMonitoring     WorkloadType = "monitoring"
    WorkloadStorage        WorkloadType = "storage"
    WorkloadInternal       WorkloadType = "internal_service"
    WorkloadExternal       WorkloadType = "external_dependency"
    WorkloadShared         WorkloadType = "shared_service"
)

// Classification berdasarkan:
// - Container labels (com.docker.compose.service)
// - Port connections (who connects to what)
// - Environment variables (DATABASE_URL, REDIS_URL, etc.)
// - Volume mounts (data volumes = stateful)
// - Process names (celery, sidekiq, bullmq = worker)
```

### 3. Migration Order via Dependency Graph

```go
// Gunakan TopologicalSort() yang sudah ada di graph.go
// Tambahkan migration-specific ordering:

// Default migration order (reverse dependency):
// 1. Monitoring (Prometheus, Grafana) — no dependency
// 2. Reverse Proxy (Nginx, Caddy) — depends on apps
// 3. Workers (BullMQ, Celery) — depends on queues + DBs
// 4. Application (Next.js, Django, etc.) — depends on DBs + caches
// 5. Cache (Redis as cache) — depends on DBs
// 6. Queue (RabbitMQ, Kafka) — depends on DBs
// 7. Database (MySQL, PostgreSQL) — foundation
// 8. Cutover — after everything is synced

// Strategi per workload type:
// - Stateless: parallel migration, no data sync needed
// - Stateful: sequential, needs data sync + verification
// - Database: replication setup, lag monitoring, promote
// - Cache: can often be dropped and rebuilt
// - Queue: pause → drain → sync → resume
// - Worker: stop → migrate → start after dependencies ready
```

### 4. Real Pause/Resume

```go
// Ganti context cancellation dengan channel-based pause:
type Pipeline struct {
    pauseCh  chan struct{} // signal to pause
    resumeCh chan struct{} // signal to resume
    cancelCh chan struct{} // signal to cancel
}

func (p *Pipeline) executeStage(ctx context.Context, stage PipelineStage) error {
    // Check for pause before each sub-operation
    if err := p.checkPause(ctx); err != nil {
        return err
    }
    // ... do work ...
}

func (p *Pipeline) checkPause(ctx context.Context) error {
    select {
    case <-p.pauseCh:
        // Wait for resume or cancel
        select {
        case <-p.resumeCh:
            return nil // resumed
        case <-p.cancelCh:
            return ErrCancelled
        case <-ctx.Done():
            return ctx.Err()
        }
    default:
        return nil // not paused
    }
}
```

### 5. WebSocket Reconnection

```typescript
// pipeline.ts
export function wsPipelineReconnect(
    migrationId: number,
    onMessage: (msg: WSMessageExtended) => void,
    maxRetries: number = 10
): { ws: WebSocket; close: () => void } {
    let retries = 0;
    let ws: WebSocket;
    
    function connect() {
        ws = new WebSocket(wsUrl(`/ws/pipeline/${migrationId}`));
        ws.onmessage = (event) => onMessage(JSON.parse(event.data));
        ws.onclose = () => {
            if (retries < maxRetries) {
                retries++;
                const delay = Math.min(1000 * Math.pow(2, retries), 30000);
                setTimeout(connect, delay);
            }
        };
        ws.onerror = () => { /* same backoff */ };
    }
    
    connect();
    return { ws, close: () => { retries = maxRetries; ws.close(); } };
}
```

---

## Kompatibilitas dengan Arsitektur Saat Ini

Semua rekomendasi di atas **kompatibel** dengan arsitektur Meshium yang sudah ada:

- **State machine** tidak perlu diubah — hanya perlu fix transitions yang salah
- **Dependency graph** sudah ada dan punya `TopologicalSort()` — hanya perlu expand node types
- **Pipeline engine** modular — setiap stage bisa di-fix secara independent
- **Frontend wizard** sudah 11-step — hanya perlu wire ke backend dengan benar
- **Database schema** sudah support 14 tabel — tidak perlu schema change untuk fixes
- **WebSocket protocol** bisa di-extend tanpa breaking changes

**Tidak ada rekomendasi yang memerlukan rewrite arsitektur.** Semua bisa di-implementasi secara incremental.
