# RFC-001 Part 6 — Observability, Security, Plugin System, Persistence, API, dan AI

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 6 dari 7
> **Baca dulu:** [Part 3 — Engines & State](./03-engines-and-state.md), [Part 4 — Planner & Drivers](./04-planner-and-drivers.md), [Part 5 — Pipeline](./05-pipeline-checkpoint-cutover-rollback.md)
> **Konvensi:** Prosa Bahasa Indonesia. Tipe/interface/tabel dalam English.

---

## 1. Ringkasan

Part ini mendesain **lapisan lintas-potong (cross-cutting)** yang membuat Meshium bisa dipercaya di produksi: bagaimana kita *melihat* apa yang terjadi (observability), *mengizinkan* siapa melakukan apa (security), *memperluas* tanpa fork (plugin), *menyimpan* kebenaran (persistence), *berkomunikasi* (API), dan *menasihati* tanpa mengambil alih kendali (AI).

Semua desain di sini mematuhi ADR yang sudah dikunci:

- **ADR-8** — gRPC internal + REST thin gateway + WebSocket/SSE streaming; CLI dan Web adalah klien setara.
- **ADR-9** — AI advisory & deterministik-by-default; tidak pernah jalur eksekusi tanpa persetujuan manusia.
- **ADR-10** — Secrets zero-knowledge di server; RBAC berbasis peran + scope per-target; least privilege by default.

---

## 2. Observability

### 2.1 Prinsip: observability adalah kontrak langkah, bukan tambahan

Dari **P5** (Part 1): sebuah operasi yang tidak bisa diamati tidak bisa dipercaya. Karena itu observability tidak "ditambahkan" ke engine — ia adalah bagian dari signature `Step`:

```go
// Dari Part 3 — setiap Step menerima ExecContext yang membawa
// telemetry handles. Step TIDAK PERNAH membuat logger/tracer sendiri.
type ExecContext struct {
    Ctx        context.Context      // carries trace span
    Logger     *slog.Logger         // structured, pre-tagged with run_id/step_id
    Metrics    MetricSink           // counters/histograms
    Tracer     trace.Tracer         // OpenTelemetry
    Audit      AuditSink            // append-only audit events
    Events     EventEmitter         // domain events → WS/SSE stream
}
```

### 2.2 Empat pilar dan pemetaannya

| Pilar | Teknologi | Apa yang direkam | Retensi |
|---|---|---|---|
| **Logs** | `slog` (stdlib) → JSON | Setiap keputusan langkah, error dengan konteks | 30 hari (rolling) |
| **Metrics** | Prometheus (pull) | Durasi langkah, retry count, bytes transferred, SRCR | 90 hari |
| **Traces** | OpenTelemetry → OTLP | Span per-run → per-stage → per-step → per-SSH-command | 7 hari (sampled) |
| **Audit** | Append-only tabel `audit_log` | Siapa, kapan, aksi apa, pada target apa, hasilnya | **Selamanya** (immutable) |

> **Keputusan:** kita **tidak** membundel Grafana/Jaeger/Prometheus di dalam Meshium (lihat product boundary §4.2 Part 1). Kita *mengekspos* OTLP + `/metrics` dan membiarkan operator menghubungkannya ke stack mereka. Default: semua telemetry juga bisa dilihat di UI Meshium sendiri (self-contained) tanpa stack eksternal.

### 2.3 Struktur log wajib

```go
// Setiap log entry dalam jalur eksekusi WAJIB membawa field korelasi ini.
// Lint CI: slog call di internal/engine/** tanpa run_id → gagal.
logger.Info("stage completed",
    "run_id",   run.ID,
    "stage",    stage.Name,
    "step_id",  step.ID,
    "driver",   step.Driver,
    "target",   step.TargetRef,
    "duration", elapsed,
    "outcome",  "ok",
)
```

**Aturan lint (mengikat P5):**

| Aturan | Enforcement |
|---|---|
| Tidak ada `fmt.Print*` di `internal/engine/**` dan `internal/drivers/**` | `grep` CI gate |
| Setiap `slog` di jalur eksekusi punya `run_id` | custom `go vet` analyzer |
| Tidak ada secret di log (nilai kredensial) | redaction middleware wajib pada `Logger` |

### 2.4 Metrics kanonik (nama Prometheus)

```
# Counter
meshium_runs_total{outcome="committed|failed|rolled_back|cancelled"}
meshium_steps_total{driver, stage, outcome}
meshium_ssh_commands_total{host, outcome="ok|nonzero_exit|transport_error"}
meshium_rollback_total{directional="pre_ponr|post_ponr"}

# Histogram
meshium_stage_duration_seconds{stage}
meshium_bytes_transferred{driver}
meshium_verify_lag_bytes{datastore}   # replication lag at cutover

# Gauge (the North Star, computed)
meshium_srcr_ratio   # Successful Reversible Cutover Rate (Part 1 §2.2)
```

`meshium_ssh_commands_total{outcome="nonzero_exit"}` adalah metrik pengaman langsung untuk cacat sistemik `ExecContext` (P6): jika angka ini naik tapi `meshium_steps_total{outcome="failed"}` tidak, berarti ada langkah yang menelan exit code non-zero — alarm.

### 2.5 Execution timeline & event history

UI menampilkan **timeline** yang direkonstruksi dari event log (Part 3/5, event sourcing). Karena setiap transisi state adalah event ber-timestamp yang persist, timeline bersifat:

- **Deterministik** — replay event log menghasilkan timeline yang sama.
- **Resumable-aware** — menunjukkan titik crash dan titik resume secara eksplisit.
- **Auditable** — setiap entri timeline punya event ID yang merujuk ke `audit_log`.

```
Run #4821  compose+postgres+nginx → target-02
────────────────────────────────────────────────────────────
09:00:01  ● Discovery        ok     3.2s   (7 workloads found)
09:00:04  ● Validation       ok     1.1s
09:00:05  ● Compatibility    ok     0.8s   (0 blockers)
09:00:06  ● Backup           ok    44.0s   (checkpoint cp-1)
09:00:50  ● InitialSync      ok   612.0s   (12.4 GB, rsync)
09:11:02  ● LiveReplication  ok    30.0s   (lag < 1s)
09:11:32  ◆ PONR  ────────── PROMOTE ──────────────────────
09:11:33  ● TrafficSwitch    ok     2.1s   (LB → target-02)
09:11:35  ● PostVerify       ok     8.0s   (health 100/100)
09:11:43  ● Observation      ok   300.0s   (no regressions)
09:16:43  ✔ Committed
────────────────────────────────────────────────────────────
```

---

## 3. Security Model

### 3.1 Ancaman yang kita desain untuk melawannya

| Ancaman | Kontrol |
|---|---|
| Pencurian kredensial dari DB Meshium | Zero-knowledge: server tidak bisa dekripsi tanpa passphrase operator |
| MITM pada koneksi SSH | Strict host-key verification (control plane) + pinned keys untuk data plane |
| Privilege escalation via UI | RBAC peran + scope per-target; least privilege |
| Replay/hijack sesi WS | Token per-klien, subprotocol-safe, expiring (lihat memory) |
| Supply chain (binary dipalsukan) | SBOM + signed release + provenance (SLSA) |
| Secret bocor di log/error | Redaction middleware wajib; error tidak pernah membawa nilai secret |

### 3.2 Authentication

**Manusia → Meshium:**
- Login berbasis argon2id (sudah ada di repo — `internal/mod/auth`). Pertahankan.
- **Perbaikan wajib (dari audit):** token sesi global non-expiring → ganti dengan **token per-sesi ber-TTL + refresh**. Setiap klien (tab browser, CLI) mendapat tokennya sendiri.
- WebSocket auth: token disematkan sebagai subprotocol (RawURLEncoding, subprotocol-safe — lihat memory `project_ws_auth_token`), **bukan** `?token=` query param (bocor di log proxy).

**Meshium → host remote:**
- SSH key-based; host key **selalu** diverifikasi untuk control plane.
- Data plane (rsync bulk): pinned host key dari fase discovery — **bukan** `accept-new` TOFU (memperbaiki H5 audit).

### 3.3 Authorization — RBAC

```go
type Role string

const (
    RoleViewer   Role = "viewer"   // read-only: lihat run, timeline, report
    RoleOperator Role = "operator" // jalankan migrasi pada target yang di-grant
    RoleApprover Role = "approver" // approve PONR gate & destructive rollback
    RoleAdmin    Role = "admin"    // kelola user, target, credential
)

// Otorisasi selalu (subject, action, resource-scope).
type Permission struct {
    Action Action     // "run.create", "run.cutover", "target.read", ...
    Scope  TargetRef  // scope PER-TARGET, bukan global
}

// Least privilege: RoleOperator pada target-A tidak bisa menyentuh target-B.
func (a *Authz) Can(u User, act Action, res TargetRef) bool
```

**Gate kritikal yang butuh `RoleApprover` (tidak boleh di-bypass):**
- Melewati **PONR** (cutover commit).
- Menjalankan **destructive rollback** pasca-PONR (revert traffic).
- Menghapus credential atau target.

Ini memetakan ke pipeline gate di Part 5 — PONR bukan sekadar state, ia adalah **authorization boundary**.

### 3.4 Secrets & Key Management (zero-knowledge)

```
Operator passphrase (never stored)
        │
        ▼  Argon2id (per-vault salt, stored)
   Vault Key (in memory only, TTL)
        │
        ▼  AES-256-GCM
  Encrypted credential blobs in DB
```

- Server **tidak pernah** menyimpan passphrase. Tanpa passphrase operator aktif, blob kredensial adalah sampah acak.
- Sudah ada di repo: AES-256-GCM, PBKDF2 600k, Argon2id. **Pertahankan**; naikkan PBKDF2→Argon2id untuk konsistensi.
- **Credential rotation:** vault mendukung re-encrypt dengan key baru tanpa downtime (envelope encryption: rotate KEK, bukan DEK).

### 3.5 Supply chain security

| Artefak | Kontrol | Tool |
|---|---|---|
| SBOM | Dihasilkan tiap release | `syft` / `cyclonedx-gomod` |
| Binary signing | Semua rilis ditandatangani | `cosign` (keyless OIDC) |
| Provenance | SLSA level 3 attestation | GitHub Actions + `slsa-github-generator` |
| Dependency pinning | `go.sum` + Dependabot; versi di-pin | Go modules |

> **Prinsip:** tanpa mengubah dependency runtime (kendala proyek: no new deps di runtime). Tool supply-chain hidup di CI, bukan di binary.

---

## 4. Plugin System

### 4.1 Kenapa plugin, dan batasnya

Dari **P7** (drivers are the product): driver **adalah** unit ekstensi utama. Plugin system = mekanisme yang membuat driver pihak ketiga bisa didaftarkan **tanpa fork** core.

**Keputusan model plugin [ADR-11]:**

Kita **tidak** memakai Go `plugin` package (`.so`) — rapuh, terikat versi toolchain persis, tidak lintas-OS. Sebagai gantinya, dua tingkat:

| Tingkat | Mekanisme | Kapan |
|---|---|---|
| **In-tree drivers** | Registry + interface, dikompilasi ke binary | Driver first-party (35 driver inti) |
| **Out-of-process drivers** | Sub-process berbicara gRPC (pola go-plugin HashiCorp) | Driver pihak ketiga / eksperimental |

Out-of-process memberi **isolasi** (driver crash ≠ core crash), **keamanan** (sandbox), dan **kebebasan bahasa** (driver bisa ditulis dalam bahasa lain), dengan harga overhead IPC yang dapat diterima untuk operasi migrasi (bukan hot-path).

### 4.2 Kontrak plugin = kontrak Driver (Part 4)

Plugin tidak punya interface baru; ia mengekspos `Driver` (Part 4) lewat gRPC:

```proto
service Driver {
  rpc Capabilities(Empty) returns (CapabilitySet);
  rpc Discover(DiscoverRequest) returns (stream WorkloadInfo);
  rpc Validate(ValidateRequest) returns (ValidationResult);
  rpc Plan(PlanRequest) returns (StepPlan);
  rpc Apply(ApplyRequest) returns (stream Progress);   // idempotent, keyed
  rpc Verify(VerifyRequest) returns (VerifyResult);
  rpc Rollback(RollbackRequest) returns (stream Progress);
  rpc HealthCheck(HealthRequest) returns (HealthResult);
}
```

### 4.3 Lifecycle plugin

```
register → handshake (version + capability negotiation) → validate
    → invoke (per-step, isolated) → health-monitor → upgrade/unregister
```

- **Handshake** menegosiasikan versi protokol; core menolak plugin dengan protokol tak-kompatibel (fail fast, bukan silent).
- **Capability negotiation** (Part 4): core menanyakan `Capabilities()` dulu; planner hanya memilih strategi yang didukung driver. Driver yang tidak mendukung `SupportsResume` tidak akan diberi langkah yang butuh resume.
- **Isolation:** setiap invocation punya timeout + resource limit; crash plugin → step gagal dengan typed error, bukan panic core.
- **Upgrade:** plugin versioned; core bisa menjalankan dua versi berdampingan selama migrasi kompatibilitas.

### 4.4 Validasi & keamanan plugin

- Plugin pihak ketiga berjalan dengan **least privilege** (tidak otomatis dapat akses credential vault; harus di-grant scope).
- Manifest plugin dideklarasikan (capability + permission yang diminta); operator meng-approve saat registrasi.
- Binary plugin diverifikasi checksum saat load.

---

## 5. Persistence Layer

### 5.1 Apa yang disimpan dan di mana

| Data | Model | Store | Alasan |
|---|---|---|---|
| **Migration state** | Materialized dari event log | SQL (tabel `runs`) | Query cepat untuk UI |
| **Execution history** | Append-only event log | SQL (tabel `run_events`) | Source of truth; replay-able (Part 3) |
| **Planner cache** | Key-value (inventory hash → plan) | SQL (tabel `plan_cache`) | Hindari re-discovery mahal |
| **Checkpoints** | Snapshot state + marker idempoten | SQL (tabel `checkpoints`) | Resume & crash recovery (Part 5) |
| **Audit log** | Append-only, immutable | SQL (tabel `audit_log`) | Kepatuhan; selamanya |
| **Credentials** | Encrypted blob (AES-256-GCM) | SQL (tabel `credentials`) | Zero-knowledge (§3.4) |

### 5.2 Keputusan datastore [ADR-12]

**SQLite untuk H1, dengan abstraksi yang siap Postgres untuk H2+.**

- H1 (solo operator, persona inti): SQLite = zero-ops, embedded, cukup untuk ribuan run. Sudah dipakai di repo.
- H2+ (platform engineer, MSP multi-tenant): butuh concurrency & remote → Postgres.
- **Kunci:** semua akses lewat interface repo (Part 2), **tidak ada SQL mentah di engine**. Migrasi SQLite→Postgres = ganti implementasi repo, bukan rewrite.

```go
// Semua persistence lewat interface — engine tidak tahu SQLite vs Postgres.
type RunRepo interface {
    AppendEvent(ctx context.Context, runID RunID, ev Event) error   // transactional
    LoadEvents(ctx context.Context, runID RunID) ([]Event, error)
    Materialize(ctx context.Context, runID RunID) (*RunState, error)
}
```

### 5.3 Invariant transaksional (memperbaiki H2/H3 audit)

- **State + status string ditulis dalam SATU transaksi** (P4 — honest state). Tidak ada dua field yang bisa bertentangan.
- **Event append + checkpoint dalam satu transaksi** — resume tidak pernah melihat state setengah-tertulis.
- **Audit write tidak boleh gagal diam** — jika audit gagal, langkah gagal (fail-closed pada aksi sensitif).

---

## 6. CLI & API

### 6.1 Arsitektur [ADR-8]

```
        ┌──────────┐        ┌──────────┐
        │   CLI    │        │   Web    │   ← klien setara
        └────┬─────┘        └────┬─────┘
             │ gRPC              │ REST + WS
             │                   │
        ┌────▼───────────────────▼────┐
        │   API Gateway (thin)         │  REST→gRPC transcode
        └──────────────┬───────────────┘
                       │ gRPC (internal)
        ┌──────────────▼───────────────┐
        │      Application Services     │  (Part 2)
        └──────────────┬───────────────┘
                       │
              ┌────────▼────────┐
              │  Domain / Engine │
              └──────────────────┘
```

**Prinsip:** CLI dan Web **tidak** punya jalur istimewa. Keduanya memanggil API yang sama. Ini mencegah drift di mana UI bisa melakukan sesuatu yang CLI tidak bisa (atau sebaliknya).

### 6.2 Protokol per-kebutuhan

| Kebutuhan | Protokol | Alasan |
|---|---|---|
| Command (create run, cutover) | gRPC (internal) / REST (eksternal) | Request-response, typed |
| Streaming progress | WebSocket / SSE | Push real-time ke UI |
| Event delivery | WebSocket (multiplex per-run) | Timeline live |
| CLI ↔ backend | gRPC langsung | Streaming + typed, tanpa transcode |

### 6.3 Concurrency-safe WebSocket (memperbaiki C3 audit)

Cacat audit: `conn.WriteJSON` dari banyak goroutine = data race. Perbaikan arsitektural:

```go
// Satu goroutine WRITER per koneksi; semua pengirim menaruh ke channel.
// TIDAK ADA WriteJSON langsung dari goroutine lain.
type wsConn struct {
    out chan any        // buffered
    // ...
}

func (c *wsConn) writePump() {
    for msg := range c.out {
        c.conn.WriteJSON(msg)   // hanya di sini
    }
}

// Resume/read loop TIDAK boleh blocking (memperbaiki H4):
// read loop hanya mem-parse & dispatch ke handler async.
```

### 6.4 API surface (contoh REST)

```
POST   /api/v1/runs                 # create migration run from plan
GET    /api/v1/runs/{id}            # run state (materialized)
GET    /api/v1/runs/{id}/events     # SSE stream of events
POST   /api/v1/runs/{id}/cutover    # RoleApprover: pass PONR
POST   /api/v1/runs/{id}/rollback   # RoleApprover: directional rollback
GET    /api/v1/runs/{id}/report     # final audit report
WS     /api/v1/runs/{id}/stream     # live progress (token via subprotocol)
```

Semua endpoint state-changing butuh otorisasi (§3.3) dan menulis `audit_log` (§5.1).

---

## 7. AI Integration

### 7.1 Prinsip [ADR-9]: AI menasihati, manusia & mesin deterministik memutuskan

> AI **tidak pernah** menjadi jalur eksekusi. Ia menghasilkan *rekomendasi* dan *penjelasan* yang harus lolos gate deterministik atau approval manusia sebelum berdampak.

Ini bukan pembatasan defensif; ini keputusan kepercayaan. Sebuah tool migrasi yang bisa kehilangan data (P1) tidak boleh menyerahkan keputusan destruktif ke output probabilistik.

### 7.2 Batas deterministik vs AI

```
┌─────────────────────────────────────────────────────────────┐
│ DETERMINISTIK (tidak pernah AI)                               │
│ • Exit-code checking (P6)                                     │
│ • State transitions & PONR gate                               │
│ • Rollback directional logic                                  │
│ • Checkpoint/resume                                           │
│ • Credential decryption & authz                               │
│ • Verification pass/fail (checksum, row count, lag)           │
├─────────────────────────────────────────────────────────────┤
│ AI-ASSISTED (advisory, di-review)                             │
│ • Discovery enrichment (identifikasi workload tak-dikenal)    │
│ • Compatibility analysis (jelaskan mengapa versi bentrok)     │
│ • Migration planning hints (usulkan strategi, planner tetap   │
│   yang memutuskan via cost-model Part 4)                      │
│ • Risk narrative (jelaskan skor risiko yang sudah dihitung)   │
│ • Rollback recommendation (usul, approver yang eksekusi)      │
│ • Troubleshooting (analisis log kegagalan)                    │
│ • Post-migration analysis (ringkasan & lessons)               │
└─────────────────────────────────────────────────────────────┘
```

### 7.3 Pola integrasi: AI sebagai advisor di sekitar deterministic core

```go
// AI menghasilkan rekomendasi; core memvalidasi & manusia meng-approve.
type Advisor interface {
    // Menjelaskan hasil deterministik (skor risiko sudah dihitung engine).
    ExplainRisk(ctx context.Context, score RiskScore, inv Inventory) (string, error)

    // Mengusulkan strategi; planner deterministik tetap yang memilih final.
    SuggestStrategies(ctx context.Context, inv Inventory) ([]StrategyHint, error)

    // Menganalisis kegagalan; TIDAK mengeksekusi apa pun.
    Troubleshoot(ctx context.Context, run RunState, logs []LogEntry) (Diagnosis, error)
}

// Rekomendasi AI SELALU melewati gate:
//   1. Deterministic validation (apakah usul ini bahkan legal?)
//   2. Human approval (untuk aksi sensitif)
// Baru kemudian bisa berdampak.
```

### 7.4 Kenapa bukan chatbot

Meshium AI **bukan** asisten chat. Ia adalah lapisan *penjelasan & saran terstruktur* yang:

- **Input terstruktur** — inventory, skor, log (bukan free-text prompt bebas dari user).
- **Output terstruktur** — `StrategyHint`, `Diagnosis`, narasi (bukan percakapan).
- **Auditable** — setiap saran AI dicatat dengan input & model version di `audit_log`.
- **Dapat dimatikan** — Meshium berfungsi penuh tanpa AI (kendali deterministik lengkap). AI adalah augmentasi, bukan dependensi.

Repo saat ini punya `internal/mod/ai/service.go` (1548 baris) yang sebenarnya **rule-based keyword router**, bukan AI. Rekomendasi: **jangan berpura-pura**. Entah (a) jadikan advisor LLM sungguhan di belakang interface `Advisor` di atas, atau (b) rename menjadi `heuristics` dan berhenti menyebutnya AI. Kejujuran positioning (Part 1) berlaku juga ke fitur.

---

## 8. Keputusan yang Dikunci oleh Part Ini

| ADR | Keputusan | Dampak |
|---|---|---|
| ADR-11 | Plugin = in-tree registry + out-of-process gRPC (pola go-plugin), **bukan** Go `.so` | Isolasi, keamanan, lintas-bahasa |
| ADR-12 | SQLite untuk H1, interface-repo siap Postgres untuk H2+ | Zero-ops sekarang, skalabel nanti |
| — | WS: satu writer-goroutine per koneksi | Memperbaiki data race C3 |
| — | Token sesi per-klien ber-TTL, subprotocol-safe | Memperbaiki auth global non-expiring |
| — | AI di belakang interface `Advisor`, deterministik-by-default | Memenuhi ADR-9; kejujuran fitur |

---

*Lanjut ke Part 7 — Roadmap v0.5→v3.0 & Rekomendasi Jujur.*
