# Meshium — Master Implementation Plan

> Peran: Principal Go Engineer · Distributed Systems Engineer · DevOps/SRE ·
> Security Engineer · Database Migration Engineer · Svelte Frontend Architect ·
> QA/Integration Test Engineer.

Meshium adalah aplikasi self-hosted untuk migrasi server Linux/VPS/dedicated
server.

**Stack**
- Backend: Go
- Frontend: Svelte
- Control transport: SSH
- Runtime target: Linux, Docker Engine, Docker Compose v2
- Database utama MVP: PostgreSQL
- Database roadmap: MySQL/MariaDB, Redis, MongoDB

Tugasmu: audit codebase aktual, perbaiki risiko kritis, lalu implementasikan
roadmap ini secara bertahap. Bukan saran teoretis. Jangan anggap fitur benar
hanya karena interface/function ada — verifikasi tiap temuan terhadap codebase
aktual. Jangan refactor besar yang tidak perlu. Jangan klaim "zero downtime"
sebelum fencing, replication, lag verification, dan cutover aman benar-benar
berjalan.

---

## 1. Tujuan Meshium

Meshium memigrasikan workload dari Source Server A ke Target Server B, termasuk:

- File aplikasi dan konfigurasi
- File besar hingga puluhan/ratusan GB
- Docker container dan Docker Compose project
- Docker volume dan bind mount
- Database yang berjalan langsung pada host
- Database dalam Docker container
- Database dalam Docker Compose service
- Environment/configuration reference
- Planned/manual cutover
- Resume migration saat backend Meshium restart atau SSH terputus
- Progress realtime, log terstruktur, checkpoint, audit trail, dan verification

---

## 2. Prinsip Arsitektur Wajib

### 2.1 Control plane vs data plane

Meshium berjalan pada PC utama/operator machine dan merupakan **control
plane**, bukan jalur transfer payload utama.

**Tanggung jawab Meshium:**
- UI Svelte
- API/backend Go
- Orchestration migration
- SSH ke Server A dan Server B
- Preflight dan discovery
- Menjalankan command remote
- Menyimpan metadata migration, checkpoint kecil, audit trail, event progress,
  dan secret terenkripsi
- Menampilkan log yang sudah di-redact
- Menjalankan verification dan cutover workflow

**Meshium tidak boleh:**
- Mendownload file besar dari Server A ke PC utama sebagai default
- Menyimpan dump database besar di PC utama
- Menjadi relay default untuk file, Docker volume, atau database dump
- Menjadi bottleneck bandwidth/RAM/disk untuk migration data besar

**Target data flow:**

```
                         SSH control, status, logs, metrics
┌────────────────────────────────────────────────────────────────┐
│                    PC Utama: Meshium                            │
│  Go backend | Svelte UI | State | Events | Checkpoint | Audit   │
└───────────────┬───────────────────────────────────┬────────────┘
                │                                   │
                │ SSH control                       │ SSH control
                ▼                                   ▼
      ┌──────────────────┐      direct transfer   ┌──────────────────┐
      │ Server A         │ ─────────────────────> │ Server B         │
      │ Source           │     rsync / SSH stream │ Target           │
      │ files / volumes  │     DB replication     │ restored workload│
      │ Docker / DB      │                        │ Docker / DB      │
      └──────────────────┘                        └──────────────────┘
```

### 2.2 Default transfer policy

Default: **direct A → B**. Meshium tidak menyentuh payload.

### 2.3 Relay mode

Jika Server A tidak bisa mengakses Server B akibat firewall, NAT, private
network, ACL, atau routing:

- Preflight harus mendeteksi kondisi tersebut.
- UI harus menampilkan penyebab dan solusi.
- User harus diberi opsi membuka konektivitas A → B, memakai
  bastion/ProxyJump, atau memakai relay mode melalui Meshium.

Relay mode: **disabled secara default**, wajib explicit confirmation, wajib
warning besar, wajib bounded-memory streaming, tidak boleh menyimpan payload
penuh ke disk/RAM Meshium, tidak direkomendasikan untuk data >1 GB, tidak boleh
default untuk DB production, dan dicatat sebagai `transfer_mode=relay`.

**Warning UI:**
- Data akan transit melalui mesin Meshium.
- Bandwidth, koneksi, CPU, RAM, dan disk mesin operator dapat menjadi
  bottleneck.
- Gunakan direct transfer Server A → Server B bila memungkinkan.

---

## 3. Temuan Audit yang Harus Diverifikasi

Verifikasi semua poin ini terhadap codebase aktual sebelum melakukan patch.

### P0 — Release blocker

#### P0-1. Risiko split-brain database replication

Kemungkinan masalah:
- Rollback MySQL menghentikan/reset replication target sehingga target dapat
  writable, sementara source belum difence atau didemote.
- Rollback PostgreSQL dapat mempromosikan target secara tidak sengaja melalui
  manipulasi `standby.signal`.
- Source dan target berpotensi menjadi writable bersamaan.

**Requirement:**
- Tidak boleh ada kondisi source dan target sama-sama menerima write.
- Jangan gunakan `RESET SLAVE ALL`, `RESET REPLICA ALL`, hapus
  `standby.signal`, atau promote/demote command sebagai shortcut rollback tanpa
  topology verification.
- Jika topology tidak dapat dibuktikan aman: hentikan migration, jangan
  auto-rollback, simpan diagnostics, masuk `NeedsManualIntervention`, dan
  tampilkan runbook recovery.

#### P0-2. Cutover/zero-downtime palsu

Kemungkinan masalah:
- `CutoverEngine`, `TrafficSwitchEngine`, `FreezeManager`, atau
  `ObservationEngine` ada tetapi tidak dipanggil pipeline.
- Pipeline dapat menandai `manual_required`, lalu tetap auto-commit.
- Tidak ada state pause sebelum traffic switch.

**Requirement:**
- Jangan klaim zero downtime untuk MVP.
- Buat state `AwaitingCutover`.
- Pipeline harus berhenti di state tersebut.
- Operator harus memilih aksi: commit cutover, abort/cancel, retry, atau request
  rollback jika masih aman.
- Tidak boleh `Completed` sebelum verification target dan traffic-switch
  verification selesai.

#### P0-3. Checkpoint hilang saat restart

Kemungkinan masalah: checkpoint hanya berada di memory, function persistence
checkpoint belum dipanggil, dan backend restart menyebabkan migration tidak
bisa resume aman.

**Requirement:**
- Persist checkpoint pada storage.
- Checkpoint harus menyimpan stage, resource status, transfer progress, retry,
  checksum, database state, dan cutover state.
- Saat resume, jangan percaya checkpoint mentah; reconcile dengan kondisi nyata
  remote server.
- Jika kondisi remote ambigu, masuk `NeedsManualIntervention`.

#### P0-4. Transfer file besar tidak aman

Kemungkinan masalah: SFTP timeout 5 menit, restore database memakai timeout 30
detik, output command besar dikumpulkan pada `bytes.Buffer`, Docker volume masih
melalui tar + SFTP lewat Meshium, dan tidak ada resume yang benar.

**Requirement:**
- Long-running stream tidak boleh memakai hard timeout 30 detik/5 menit.
- Gunakan parent context, configurable inactivity timeout, cancellation
  cleanup, bounded stderr buffer.
- Jangan mengumpulkan stdout besar di RAM.
- Transfer file besar default memakai rsync direct A → B.
- SFTP hanya fallback untuk kasus kecil/khusus.

#### P0-5. Database container silent skip

Kemungkinan masalah: detection database memakai `pgrep` di host;
PostgreSQL/MySQL dalam container tidak terlihat host process detection;
pipeline bisa sukses dengan DB selected tetapi tidak ada DB yang termigrasi.

**Requirement:**
- Selected database yang tidak dapat dideteksi harus menghasilkan preflight
  error.
- Tidak boleh dianggap "no-op sukses".
- Tambahkan host/container/compose execution mode.
- Container discovery harus memakai Docker/Compose metadata, bukan nama proses
  host saja.

#### P0-6. Rollback status tidak jujur

Jika satu rollback step gagal, state harus `RollbackDegraded` atau
`NeedsManualIntervention`; simpan failure detail ter-redact, jangan menyatakan
rollback sukses, dan tampilkan langkah pemulihan operator.

#### P0-7. Command injection dan secret leakage

**Requirement:**
- Semua command remote harus dibangun dari argumen terstruktur atau
  shell-quoted dengan benar.
- Semua path, service name, container name, database name, username, compose
  file, dan host harus tervalidasi.
- Jangan pernah password pada argv, `ps`, log, error, WebSocket, API response,
  database persistence, atau audit export.
- Redis wajib memakai `REDISCLI_AUTH`, bukan `redis-cli -a`.
- PostgreSQL gunakan process-scoped `PGPASSWORD` atau `.pgpass` temporary 0600.
- MySQL gunakan `MYSQL_PWD` scoped atau `--defaults-extra-file` temporary 0600.
- Semua temporary credential wajib cleanup saat success/failure/cancel.

---

## 4. Scope MVP

### MVP wajib
- Linux source dan target
- SSH transport
- Docker Engine dan Docker Compose v2
- File migration direct A → B
- Docker named volume dan bind mount direct A → B
- rsync resume/checksum
- PostgreSQL host mode
- PostgreSQL Docker container mode
- PostgreSQL Docker Compose mode
- Checkpoint persistence dan resume migration
- Preflight/compatibility checks
- Verified manual cutover
- Svelte progress UI
- WebSocket reconnect/replay
- Structured logging/secret redaction
- Integration test untuk seluruh jalur MVP

### Tidak boleh diklaim pada MVP
- Zero-downtime penuh
- Automatic rollback setelah target menerima write
- MongoDB replica set migration
- Automatic DNS/floating-IP switch universal
- Full multi-tenant RBAC
- Semua engine DB production-ready
- File transfer melalui Meshium sebagai default
- Universal cross-version database migration tanpa validation

### Positioning MVP

Meshium adalah tool self-hosted untuk operator-assisted Linux, Docker Compose,
file/volume, dan PostgreSQL migration dengan direct source-to-target transfer,
resumable migration, verification, dan planned/manual cutover.

---

## 5. Domain Model Go

```go
type ExecutionMode string

const (
    ExecHost      ExecutionMode = "host"
    ExecContainer ExecutionMode = "container"
    ExecCompose   ExecutionMode = "compose"
)

type MigrationState string

const (
    StateDraft                   MigrationState = "draft"
    StatePreflight               MigrationState = "preflight"
    StateDiscovery               MigrationState = "discovery"
    StatePreparingTarget         MigrationState = "preparing_target"
    StateSeeding                 MigrationState = "seeding"
    StateTransferring            MigrationState = "transferring"
    StateReplicating             MigrationState = "replicating"
    StateVerifying               MigrationState = "verifying"
    StateAwaitingCutover         MigrationState = "awaiting_cutover"
    StateCuttingOver             MigrationState = "cutting_over"
    StateObserving               MigrationState = "observing"
    StateCompleted               MigrationState = "completed"
    StateFailed                  MigrationState = "failed"
    StateCancelled               MigrationState = "cancelled"
    StateRollingBack             MigrationState = "rolling_back"
    StateRolledBack              MigrationState = "rolled_back"
    StateRollbackDegraded        MigrationState = "rollback_degraded"
    StateNeedsManualIntervention MigrationState = "needs_manual_intervention"
)

type DatabaseConfig struct {
    Engine         string        `json:"engine"`
    DatabaseName   string        `json:"databaseName"`
    Username       string        `json:"username"`
    SecretRef      string        `json:"secretRef"`
    Host           string        `json:"host"`
    Port           int           `json:"port"`
    TLSMode        string        `json:"tlsMode,omitempty"`
    ExecMode       ExecutionMode `json:"execMode"`
    Container      string        `json:"container,omitempty"`
    ComposeService string        `json:"composeService,omitempty"`
    ComposeFile    string        `json:"composeFile,omitempty"`
    ComposeProject string        `json:"composeProject,omitempty"`
    IncludeRoles   bool          `json:"includeRoles"`
    IncludeGlobals bool          `json:"includeGlobals"`
}

type TransferCheckpoint struct {
    MigrationID    string    `json:"migrationId"`
    ResourceID     string    `json:"resourceId"`
    SourcePath     string    `json:"sourcePath"`
    TargetPath     string    `json:"targetPath"`
    Strategy       string    `json:"strategy"`
    BytesDone      int64     `json:"bytesDone"`
    BytesTotal     int64     `json:"bytesTotal"`
    SpeedBytes     int64     `json:"speedBytes"`
    Attempt        int       `json:"attempt"`
    SourceChecksum string    `json:"sourceChecksum"`
    TargetChecksum string    `json:"targetChecksum"`
    RemoteMarker   string    `json:"remoteMarker,omitempty"`
    Status         string    `json:"status"`
    UpdatedAt      time.Time `json:"updatedAt"`
}

type MigrationEvent struct {
    MigrationID   string          `json:"migrationId"`
    Sequence      int64           `json:"sequence"`
    CorrelationID string          `json:"correlationId"`
    Stage         string          `json:"stage"`
    ResourceID    string          `json:"resourceId,omitempty"`
    Type          string          `json:"type"`
    Payload       json.RawMessage `json:"payload"`
    CreatedAt     time.Time       `json:"createdAt"`
}

type ErrorCategory string

const (
    ErrTransientNetwork  ErrorCategory = "transient_network"
    ErrAuth              ErrorCategory = "auth"
    ErrDiskFull          ErrorCategory = "disk_full"
    ErrIncompatible      ErrorCategory = "incompatible"
    ErrChecksumMismatch  ErrorCategory = "checksum_mismatch"
    ErrCommandNotFound   ErrorCategory = "command_not_found"
    ErrTargetUnavailable ErrorCategory = "target_unavailable"
    ErrSourceUnavailable ErrorCategory = "source_unavailable"
    ErrIntegrityRisk     ErrorCategory = "integrity_risk"
    ErrUnsafeTopology    ErrorCategory = "unsafe_topology"
    ErrFatal             ErrorCategory = "fatal"
)

type TypedError struct {
    Category ErrorCategory
    Stage    string
    Resource string
    Retry    bool
    Cause    error
}
```

---

## 6. Executor Abstraction

Jangan menulis `if container != ""` pada setiap database adapter.

```go
type RemoteExecutor interface {
    Exec(ctx context.Context, req CommandRequest) (CommandResult, error)
    ExecPipe(ctx context.Context, req CommandRequest) (io.ReadCloser, CommandHandle, error)
    ExecWithStdin(ctx context.Context, req CommandRequest, stdin io.Reader) (CommandResult, error)
    Upload(ctx context.Context, src io.Reader, remotePath string) error
    Download(ctx context.Context, remotePath string, dst io.Writer) error
}

type CommandRequest struct {
    Program           string
    Args              []string
    Env               map[string]string
    WorkingDir        string
    Timeout           time.Duration
    InactivityTimeout time.Duration
    AllowLongRun      bool
    RedactArgs        []int
}

type CommandResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
    Started  time.Time
    Finished time.Time
}

type ExecutionWrapper interface {
    Wrap(req CommandRequest) (CommandRequest, error)
}

type HostWrapper struct{}
type DockerWrapper struct{ Container string }
type ComposeWrapper struct {
    ComposeFile string
    Service     string
}
```

**Aturan:**
- Host mode: command jalan langsung pada host remote.
- Container mode: `docker exec -i <container> -- <command>`.
- Compose mode: `docker compose -f <file> exec -T <service> <command>`.
- Jangan gunakan `docker exec -t` atau `docker compose exec` tanpa `-T` untuk
  binary stream.
- Semua argument harus structured dan validated.
- Tidak boleh string concatenation mentah dari input user.
- Long-running command tidak boleh buffer stdout tanpa batas.

---

## 7. File Transfer dan Docker Volume

### 7.1 Strategy selector

Input: ukuran data, jumlah file, tipe file, direct network reachability A → B,
tool availability (rsync, tar, zstd), free disk source/target, CPU/IOPS/
bandwidth, user bandwidth limit, serta relay mode diizinkan atau tidak.

### 7.2 rsync direct source ke target

Meshium SSH ke Source A, lalu Source A menjalankan rsync ke Target B.

```bash
rsync \
  -aHAX \
  --numeric-ids \
  --partial \
  --append-verify \
  --info=progress2 \
  --human-readable \
  -e "ssh -o StrictHostKeyChecking=yes" \
  /source/path/ \
  migration@target-server:/target/path/
```

**Requirement:**
- Jangan memakai `--delete` sebagai default; hanya setelah explicit user
  confirmation.
- `--bwlimit` optional.
- Compression tidak hardcoded.
- Parse progress dengan aman dan rate limit event.
- Persist checkpoint periodik.
- Resume memakai rsync native partial state.
- Lakukan checksum source/target setelah transfer.
- Jangan mengirim payload ke PC Meshium.

### 7.3 SSH credential A → B

Pilihan aman: ephemeral migration SSH key, temporary restricted
`authorized_keys` pada Server B, ProxyJump/bastion, atau pre-existing trusted
key yang diverifikasi user.

**Requirement:** key/credential tidak boleh plain text di log, credential punya
TTL, cleanup pada success/failure/cancel, host-key verification wajib, jangan
default `StrictHostKeyChecking=no`, audit log hanya mencatat metode transfer.

### 7.4 Docker volumes

**Requirement:**
- Discovery named volume, bind mount, anonymous volume, tmpfs, readonly mount.
- Resolve mountpoint melalui Docker inspect.
- Volume database tidak boleh dicopy generic; arahkan ke
  DatabaseAdapter/engine-specific consistency workflow.
- Untuk named volume non-DB: stop/quiesce bila perlu, buat target volume, rsync
  mountpoint direct A → B, preserve mode/owner/group/xattr/ACL bila tersedia.
- Semua path wajib shell-quoted.
- Jangan gunakan tar + SFTP melalui Meshium untuk volume besar.

---

## 8. Database Migration

### 8.1 Execution modes

Host / Container / Compose (lihat §6).

### 8.2 Discovery

Implement host process/service detection; `docker ps`, inspect, image, health
status; Compose labels (`com.docker.compose.project`,
`com.docker.compose.service`, `com.docker.compose.project.working_dir`,
`com.docker.compose.project.config_files`); verifikasi engine melalui
image/tool/health check; jangan mengandalkan container name saja; jangan
mengharuskan DB publish port ke host; dan jika database dipilih user tetapi
tidak ditemukan, fail preflight.

### 8.3 PostgreSQL MVP

Support PostgreSQL host, standalone container, dan Docker Compose service.

**Preflight wajib:**
- Source/target reachable
- Credentials valid
- `pg_dump`/`pg_restore` tersedia pada execution environment
- PostgreSQL version compatibility
- Extension list
- Encoding/collation/locale
- Database size dan disk target
- Role/owner/privilege compatibility
- Target overwrite policy
- Connection limit dan TLS mode jika digunakan

**Secrets:**
- Gunakan `PGPASSWORD` scoped atau `.pgpass` temporary 0600.
- Jangan password pada argumen command.
- Jangan persist plaintext password pada API response.

**Dump/restore:**
- Prioritaskan direct A → B.
- Meshium tidak boleh menjadi default pipe A → PC → B.
- Validasi mode dump/restore yang benar-benar pipe-compatible.
- Jika custom format membutuhkan file/seek, gunakan temporary file hanya pada
  source/target, bukan Meshium; hitung disk requirement pada preflight.
- Restore lama tidak boleh dibunuh timeout 30 detik.
- Jika restore gagal, stop source dump stream, simpan error ter-redact, dan
  jangan mark success.

**Verification:** database exists, schema/table count, extension list, optional
row count/sampled checksum, serta application-specific health query jika
tersedia.

### 8.4 MySQL/MariaDB roadmap

- **Phase 1:** detection benar, dump/restore aman, credential aman, stream
  direct A → B, basic verification.
- **Phase 2:** seed sebelum binlog replication, GTID/binlog position,
  `read_only`/`super_read_only` fencing, promotion/demotion aman, serta tidak
  ada unsafe `RESET REPLICA` rollback shortcut.

### 8.5 Redis roadmap

- **Phase 1:** detection benar, restore/restart health benar, `REDISCLI_AUTH`,
  dan jangan klaim consistent migration untuk live-write tanpa
  freeze/replication.
- **Phase 2:** `REPLICAOF`, full sync, `master_link_status`, offset/lag
  verification, controlled promotion, dan rollback policy aman.

### 8.6 MongoDB roadmap

- **Phase 1:** `mongodump`/`mongorestore`, host/container/compose, gunakan
  `mongosh`, verification basic.
- **Phase 3:** replica-set migration, initial sync, optime lag,
  election/reconfiguration, auth/TLS/write concern; tidak boleh diklaim
  production zero-downtime sebelum e2e test lengkap.

---

## 9. Pipeline dan State Machine

```
Draft → Preflight → Discovery → PreparingTarget → Seeding → Transferring →
Replicating → Verifying → AwaitingCutover → CuttingOver → Observing →
Completed
                                          ↘ Failed
                                          ↘ Cancelled
                                          ↘ RollingBack → RolledBack
                                                       ↘ RollbackDegraded
                                                       ↘ NeedsManualIntervention
```

**Aturan:**
- Semua transition tervalidasi.
- Jangan gunakan `ForceTransition` untuk menutup error.
- State dan event dipersist atomik.
- Semua event memiliki sequence monotonic.
- WebSocket replay memakai `afterSequence`.
- `Completed` hanya setelah verification selesai.
- `RolledBack` hanya bila seluruh rollback step sukses.
- Topology ambigu harus `NeedsManualIntervention`.

**Checkpoint per stage — Resume flow:**
load persisted checkpoint → reconnect source/target → validate SSH host keys →
reconcile real remote state → confirm remote marker/PID/resource state →
continue bila aman → masuk `NeedsManualIntervention` bila ambigu.

---

## 10. Retry dan Error Policy

- Jangan blanket retry DB restore.
- Jangan retry cutover otomatis jika topology berubah.
- Jangan rollback otomatis setelah traffic switch atau target write.
- Rollback memakai context terpisah dari cancellation context pipeline utama.

---

## 11. Manual Cutover MVP

MVP harus memakai **verified manual cutover**, bukan zero-downtime claim.

1. Preflight sukses
2. Discovery selesai
3. Transfer seed file/volume/database selesai
4. Verification awal berhasil
5. Pipeline masuk `AwaitingCutover`
6. Operator review checklist
7. Operator freeze/maintenance source jika perlu
8. Meshium menjalankan final delta sync
9. Operator/adapter melakukan traffic switch
10. Target health verified
11. Observing window
12. Completed

**Checklist UI:**
- Backup source tersedia dan verified
- Target free disk cukup
- File/volume checksum verified
- Database verification passed
- Target service health passed
- Environment/config target benar
- Traffic switch method dipilih
- Downtime expectation diketahui
- Source write freeze/maintenance plan siap
- Rollback eligibility diketahui
- Operator confirmation dilakukan

**Traffic switch methods:** DNS, reverse proxy, load balancer, floating IP,
application configuration, atau manual external action.

**Aturan:** jangan auto-delete source; jangan auto-destroy target setelah
failure; jangan auto-rollback jika target sudah menerima write; jika
source/target write state tidak jelas, gunakan `NeedsManualIntervention`.

---

## 12. API dan WebSocket

REST untuk resource/action, WebSocket untuk realtime/progress/control.

```
POST   /api/migrations
GET    /api/migrations
GET    /api/migrations/:id
PUT    /api/migrations/:id/config
POST   /api/migrations/:id/discovery
POST   /api/migrations/:id/preflight
POST   /api/migrations/:id/plan
POST   /api/migrations/:id/execute
POST   /api/migrations/:id/pause
POST   /api/migrations/:id/resume
POST   /api/migrations/:id/cancel
POST   /api/migrations/:id/retry
POST   /api/migrations/:id/cutover/commit
POST   /api/migrations/:id/rollback
GET    /api/migrations/:id/stages
GET    /api/migrations/:id/resources
GET    /api/migrations/:id/checkpoints
GET    /api/migrations/:id/events?afterSequence=
GET    /api/migrations/:id/logs
GET    /api/migrations/:id/metrics
GET    /api/migrations/:id/audit
GET    /api/migrations/:id/export
```

**Contoh event progress:**

```json
{
  "migrationId": "mig_123",
  "sequence": 142,
  "correlationId": "corr_456",
  "stage": "transferring",
  "resourceId": "volume:app-data",
  "type": "progress",
  "bytesDone": 524288000,
  "bytesTotal": 1610612736,
  "speedBytes": 12582912,
  "etaSeconds": 86,
  "message": "Transfer sedang berjalan",
  "timestamp": "2026-07-11T12:00:00Z"
}
```

**Requirement:** reconnect memakai `afterSequence`, action memiliki idempotency
key, payload ter-redact, tidak expose secret, rate-limit progress/log events,
support pause/resume/cancel/cutover action, dan audit semua action operator.

---

## 13. Svelte Frontend

### Halaman wajib

**Migration list** — Migration ID, source, target, state, transfer mode, size
estimate, progress, risk, last update, action.

**New migration wizard** — step:
1. Source server
2. Target server
3. SSH connection verification
4. Test direct connectivity Source A → Target B
5. Discovery resource
6. Pilih categories
7. File/volume strategy
8. Database configuration
9. Host/container/compose DB mode
10. Compatibility/preflight
11. Risk acknowledgement
12. Review/create plan

Database UI harus memiliki: engine selector, host/container/compose toggle,
container selector, Compose service selector, Compose file/project selector,
secret input sekali saja, backend mengembalikan `secretRef` bukan password, test
connection, dan preflight errors.

**Pipeline view** — stage timeline; overall dan per-resource progress;
bytes/speed/ETA; transfer mode direct/relay; database dump/restore status;
replication lag bila tersedia; checkpoint/resume status; connection state; logs
filterable; error detail ter-redact; retry/cancel/pause controls.

**Cutover view** — `AwaitingCutover`; downtime expectation; checklist; traffic
switch plan; confirmation modal; commit cutover; abort; safe rollback
availability; warning split-brain/manual intervention.

**Failure/manual intervention view** — failed stage; typed error; redacted
diagnostics; recommended next step; runbook; export audit report; safe actions
only.

### UI rules
- Critical preflight memblokir Start.
- Warning membutuhkan acknowledgement.
- Jangan tampilkan success sebelum verification.
- Jangan sebut manual cutover sebagai zero downtime.
- Jangan menampilkan secret pada UI/log.
- Render SyncSession/replication session bila backend menyediakannya.

---

## 14. Logging, Metrics, Audit

**Structured logs:**
`timestamp`, `level`, `migration_id`, `correlation_id`, `stage`,
`resource_id`, `source_alias`, `target_alias`, `operation`, `duration`,
`exit_code`, `retry_attempt`, `transfer_mode`, `sanitized_error`.

**Metrics:**
bytes transferred, effective throughput, ETA, rsync retry count, checksum
result, source/target disk free, CPU/RAM/IO snapshot, database dump/restore
duration, replication lag, stage duration, reconnect count, relay mode usage,
manual intervention count, verification pass/fail.

**Aturan:** semua log/event melalui redaction; jangan gunakan `log.Printf` raw
untuk remote stderr/stdout; jangan persist payload credential; correlation ID
wajib terpropagasi.

---

## 15. Testing

### 15.1 Unit tests
Tambahkan test untuk state transition, illegal-state rejection, error
classification, retry policy, shell quote, path/identifier validation, command
generation host/container/compose, secret redaction, Redis password tidak
berada di argv, PostgreSQL/MySQL password tidak berada di argv, checkpoint
serialization, resume reconciliation, transfer strategy selector,
direct-vs-relay policy, rollback topology safety, dan no dual-primary path.

### 15.2 Integration tests
Gunakan Docker Compose/Testcontainers bila sesuai:
- PostgreSQL host-like migration
- PostgreSQL container migration
- PostgreSQL Compose migration
- Container tanpa published DB port
- Missing container/Compose service/DB client
- MySQL absent detection false
- Redis restart failure returns failure
- Restore >30 seconds
- Direct rsync A → B
- Docker named volume dan bind mount migration
- Direct connectivity unavailable
- Relay requires explicit opt-in

### 15.3 Failure injection
Simulasikan: SSH source/target putus, backend restart, rsync source/target
killed, source dump/target restore failure, target disk full, permission denied,
checksum mismatch, container hilang di tengah job, network loss/latency dengan
`tc netem`, cutover partial failure, topology DB ambiguous, dan target
menerima write setelah cutover.

Expected: tidak ada corruption diam-diam, false success, atau auto rollback
berbahaya; gunakan `RollbackDegraded`/`NeedsManualIntervention` bila diperlukan.

### 15.4 Large-file tests
- Minimal 50 GB sparse/dummy file jika environment memungkinkan.
- Integration profile lokal bila CI terbatas.
- Putus transfer di tengah, resume, checksum verify.
- Pastikan disk PC Meshium tidak menyimpan payload.
- Pastikan stdout besar tidak OOM.
- Uji volume besar.

### 15.5 Security tests
- Injection: semicolon, quote, command substitution, newline, spaces, path
  traversal.
- Snapshot API/WebSocket/log: password tidak boleh terlihat.
- Temporary credential cleanup.
- SSH host-key verification.
- Docker command allowlist.

---

## 16. Dokumentasi

Buat atau update:

- `docs/architecture.md`
- `docs/direct-data-plane.md`
- `docs/security-model.md`
- `docs/database-migration.md`
- `docs/postgresql-migration.md`
- `docs/docker-compose-migration.md`
- `docs/large-file-transfer.md`
- `docs/cutover-runbook.md`
- `docs/rollback-runbook.md`
- `docs/manual-intervention-runbook.md`
- `docs/testing.md`
- `docs/known-limitations.md`
- `docs/implementation-plan-direct-data-plane.md`

Dokumentasi wajib menjelaskan: control plane, payload normal direct A → B,
syarat konektivitas/bastion, risiko relay mode, supported workload/DB mode,
batasan versi DB, downtime semantics, backup responsibility, interrupted
migration recovery, safe rollback limitations, security model, tool requirement,
preflight, dan known limitations.

---

## 17. Prioritized Backlog

### P0
- **P0-1 — Prevent split-brain rollback.** Goal: tidak ada dual-primary state.
  Acceptance: semua topology ambiguity masuk `NeedsManualIntervention`; test
  dual-primary prevention tersedia.
- **P0-2 — Honest rollback state.** Goal: jangan mark `RolledBack` bila ada
  rollback step gagal. Acceptance: partial rollback menjadi `RollbackDegraded`.
- **P0-3 — Secure command construction.** Goal: tutup command injection.
  Acceptance: path/container/service/user/db tervalidasi/quoted; injection
  tests pass.
- **P0-4 — Secret redaction.** Goal: tidak ada secret pada
  argv/log/event/API. Acceptance: snapshot security tests pass.
- **P0-5 — Long stream safety.** Goal: tidak ada timeout 30 detik/5 menit
  untuk transfer/restore besar. Acceptance: restore >30 detik dan transfer >5
  menit sukses.
- **P0-6 — Detection correctness.** Goal: tidak ada silent DB skip.
  Acceptance: selected resource missing menghasilkan preflight error.

### P1
- **P1-1 — Direct rsync source-to-target.** Goal: file payload tidak melewati
  Meshium. Acceptance: Source A menjalankan rsync langsung ke B; PC Meshium
  tidak menyimpan payload.
- **P1-2 — Transfer checkpoint/resume/checksum.** Goal: resume aman setelah
  disconnect/restart. Acceptance: interrupted transfer resume dan checksum
  match.
- **P1-3 — Docker volume migration.** Goal: named volume/bind mount direct A
  → B. Acceptance: volume besar tidak melalui tar/SFTP Meshium.
- **P1-4 — DB execution modes.** Goal: host/container/compose support.
  Acceptance: container DB tanpa publish port tetap terdeteksi/migrated.
- **P1-5 — PostgreSQL MVP.** Goal: host/container/compose PostgreSQL
  migration. Acceptance: integration test source/target pass.
- **P1-6 — AwaitingCutover.** Goal: manual cutover state yang benar.
  Acceptance: tidak ada auto-commit setelah manual-required.
- **P1-7 — Persisted pipeline/events.** Goal: state, checkpoint, event
  replay. Acceptance: backend restart dan WS reconnect berhasil.
- **P1-8 — Svelte operational UI.** Goal: wizard, preflight, pipeline,
  cutover, recovery. Acceptance: user melihat risk/progress/next action dengan
  jelas.

### P2
- PostgreSQL logical replication
- Fencing/lease mechanism
- Automatic traffic switch dengan verification
- MySQL seeded replication
- Redis replication cutover

### P3
- MongoDB replica-set migration
- Multi-user RBAC
- Provider-specific DNS/load-balancer integrations
- Advanced transfer parallelism
- Advanced reporting/export

---

## 18. Urutan Implementasi Wajib

**Phase 0:** Inventory repository; verify audit; write architecture and
implementation plan.

**Phase 1:** P0 safety fixes; regression tests; do not add new migration
engines yet.

**Phase 2:** Direct rsync A → B; checkpoint/resume/checksum; Docker volume
direct transfer.

**Phase 3:** Database host/container/compose abstraction; PostgreSQL MVP
integration tests.

**Phase 4:** State machine persistence; `AwaitingCutover`; manual verified
cutover.

**Phase 5:** Svelte operational UI; event replay; audit export.

**Phase 6:** Full E2E tests, failure injection, docs.

**Phase 7:** PostgreSQL live replication and fencing; only after MVP is stable.

---

## 19. Response Format

Saat mulai bekerja, gunakan format:

```
# Repository Understanding
- Struktur repository
- Go packages
- Svelte routes/components
- Existing migration flow

# Verified Audit Findings
| ID | Severity | File | Function | Verified issue | Impact |

# Target Data-Plane Decision
- Current mode: direct / relay / mixed
- Required changes
- Risks

# Prioritized Backlog
- P0 hingga P3

# Current Phase
- Fase yang sedang dikerjakan
- Scope
- Non-goals

# Files Changed
- path
- purpose

# Tests Executed
- command
- result
- failures if any

# Remaining Risks
- unresolved issue
- reason
- next safe action
```

---

## Aturan akhir

- Jangan lanjut ke fase berikutnya sebelum test fase saat ini lulus.
- Jangan menyebut implementasi selesai jika baru membuat design atau sebagian
  patch.
- Jika requirement tidak dapat dipenuhi tanpa perubahan besar, jelaskan
  trade-off secara jujur.
- Jika kondisi remote ambigu, pilih `NeedsManualIntervention`, bukan auto-retry
  atau auto-rollback.
- Selalu prioritaskan data integrity, security, resumability, dan observability
  dibanding kecepatan implementasi.
```

