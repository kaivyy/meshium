# RFC-001 Part 4 — Migration Planner & Driver SDK

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 4 dari 7
> **Baca dulu:** [Part 3 — Engines & State](./03-engines-and-state.md)
> **Konvensi:** Prosa Bahasa Indonesia. Tipe, interface, tabel dalam English.

---

## 1. Ringkasan

Part ini mengunci dua subsistem yang paling menentukan apakah Meshium "pintar" atau sekadar "runner skrip":

1. **Migration Planner** — mesin yang mengubah `Inventory` + `Constraints` menjadi `Plan` (DAG langkah). Ini adalah **[ADR-4]**: planner adalah *hybrid constraint-filter → cost-model → scoring*, bukan pohon `if-else`.
2. **Driver SDK** — kontrak yang membuat setiap workload (Postgres, Docker, Nginx, …) menjadi plugin yang tahu cara men-*discover/plan/migrate/verify/rollback* dirinya. Ini **[ADR-3]**: core tidak pernah tahu apa itu "Postgres".

Audit menemukan planner saat ini rusak secara fundamental: `SelectStrategy` (pohon 8-cabang) **tidak pernah dipanggil** oleh `CreatePlan` (dead code), dan `StrategyInput` **tidak punya** field bandwidth/latency/CPU/RAM/downtime/RPO/RTO — jadi bahkan jika dipanggil, ia menebak buta. Estimator memalsukan ukuran (500MB/volume, 1GB/DB, 100MB/s network hardcoded). Part ini merancang penggantinya dari nol.

---

## 2. Migration Planner

### 2.1 Kenapa bukan rule-based if-else [ADR-4]

Pendekatan naif (yang ada sekarang):

```go
// ANTI-PATTERN — yang harus dibuang
if workload.Type == "postgres" && size > 10*GB {
    return StrategyReplication
} else if workload.Type == "postgres" {
    return StrategyDumpRestore
} else if ... // 8 cabang, tak teruji, tak sadar-constraint
```

Masalahnya:

| Cacat | Konsekuensi |
|---|---|
| **Tidak sadar-constraint** | Memilih replication padahal downtime budget besar & bandwidth kecil (dump lebih cepat total) |
| **Tidak komposabel** | Tambah 1 dimensi (mis. RPO) = tulis ulang seluruh pohon |
| **Tidak dapat dijelaskan** | Tidak bisa bilang *kenapa* strategi X dipilih; hanya "cabang ke-5 kena" |
| **Tidak dapat di-override** | Operator tidak bisa bilang "aku terima downtime, pilih yang termurah" |

### 2.2 Arsitektur hybrid: tiga tahap

```
   Inventory (per-workload facts)          Constraints (RPO/RTO/downtime/budget)
            │                                        │
            ▼                                        ▼
   ┌─────────────────────────────────────────────────────────┐
   │  STAGE 1 — CONSTRAINT FILTER (hard gates)                 │
   │  Buang strategi yang MELANGGAR constraint keras.          │
   │  Output: himpunan strategi *feasible* (bisa kosong→error) │
   └─────────────────────────────────────────────────────────┘
            │  feasible[]
            ▼
   ┌─────────────────────────────────────────────────────────┐
   │  STAGE 2 — COST MODEL (deterministic estimation)          │
   │  Untuk tiap strategi feasible, hitung:                    │
   │  transferTime, downtime, riskDelta, resourceCost, RPO/RTO │
   │  dari PENGUKURAN NYATA (bukan konstanta).                 │
   └─────────────────────────────────────────────────────────┘
            │  costed[]
            ▼
   ┌─────────────────────────────────────────────────────────┐
   │  STAGE 3 — WEIGHTED SCORING (policy)                      │
   │  score = Σ wᵢ · normalize(costᵢ)                          │
   │  Bobot ditentukan oleh PROFILE operator (safety-first /   │
   │  speed-first / cost-first). Pilih argmax.                 │
   └─────────────────────────────────────────────────────────┘
            │
            ▼
   Selected Strategy  +  full Explanation (why + rejected alternatives)
```

Kenapa tiga tahap terpisah, bukan satu fungsi skoring:

- **Constraint filter** memberi jaminan *keras* (P1 safety-over-completeness): kita tidak pernah men-*skor* strategi yang melanggar RPO. Melanggar = dibuang, bukan diberi skor rendah.
- **Cost model** deterministik dan dapat diuji unit tanpa policy. Ia menjawab "berapa lama / berapa risiko", bukan "mana yang terbaik".
- **Scoring** memisahkan *policy* (bobot) dari *fakta* (cost). Operator mengubah profil tanpa menyentuh model biaya.

### 2.3 Input planner — lengkap (memperbaiki C2 audit)

`StrategyInput` lama tidak punya field penting. Kontrak baru:

```go
// PlannerInput adalah SEMUA yang planner butuh. Tidak ada konstanta
// tersembunyi; jika sebuah nilai tak diketahui, ia bertipe *T (nullable)
// dan planner WAJIB menolak (bukan menebak) bila strategi butuh nilai itu.
type PlannerInput struct {
    Workload   WorkloadFacts    // apa yang dimigrasi
    Link       LinkFacts        // karakteristik jaringan source→target
    Source     HostFacts        // kapasitas source
    Target     HostFacts        // kapasitas target
    Objectives Objectives       // constraint yang dinegosiasikan operator
    Profile    ScoringProfile   // policy: bagaimana menimbang trade-off
}

type WorkloadFacts struct {
    Kind          WorkloadKind  // postgres, docker, filesystem, ...
    DataSizeBytes int64         // DIUKUR, bukan ditebak (0 = belum diukur → error)
    ObjectCount   int64         // rows / files / keys — untuk estimasi verify
    ChangeRateBps int64         // laju perubahan (untuk RPO & catch-up feasibility)
    IsStateful    bool
    Capabilities  CapabilitySet // dari driver: SupportsLiveReplication, dll.
}

type LinkFacts struct {
    BandwidthBps  int64          // DIUKUR via probe (bukan 100MB/s hardcoded)
    RTTMillis     float64        // latency source↔target
    Reliable      bool           // packet loss di bawah ambang?
}

type HostFacts struct {
    CPUCores   int
    RAMBytes   int64
    FreeDisk   int64
    IOPS       int64
}

type Objectives struct {
    MaxDowntime   *time.Duration // nil = tak dibatasi
    RPO           *time.Duration // recovery point objective
    RTO           *time.Duration // recovery time objective
    Deadline      *time.Time     // migrasi harus selesai sebelum ...
    MaxCostUnits  *float64       // anggaran resource abstrak
}

type ScoringProfile struct {
    Name    string             // "safety-first" | "speed-first" | "cost-first" | custom
    Weights map[CostAxis]float64
}
```

**Prinsip anti-fabrikasi (memperbaiki C3 estimator):** setiap field ukuran adalah `int64` yang **harus** diisi oleh Discovery dari pengukuran nyata. Nilai `0` berarti "belum diukur" dan planner **menolak** dengan `ErrInsufficientData`, tidak menyulih konstanta. Ini konsekuensi langsung P1.

### 2.4 Stage 1 — Constraint filter

```go
type Strategy interface {
    Kind() StrategyKind // DumpRestore, LiveReplication, FilesystemSync, ...
    // Feasible mengembalikan (true, nil) bila strategi ini BISA memenuhi
    // objectives untuk input ini; (false, reason) bila melanggar constraint keras.
    Feasible(in PlannerInput) (bool, RejectReason)
}
```

Contoh gate keras:

| Strategi | Gate feasibility |
|---|---|
| `LiveReplication` | butuh `Capabilities.SupportsLiveReplication` **dan** `ChangeRateBps < BandwidthBps` (catch-up mungkin) |
| `DumpRestore` | butuh `MaxDowntime == nil \|\| estDowntime ≤ MaxDowntime` |
| `FilesystemSync` | butuh workload **tidak** stateful-DB (konsistensi tak terjamin untuk DB live) |
| `BlueGreen` | butuh target punya kapasitas jalan paralel (`Target.RAM ≥ Source.workingSet`) |

Bila himpunan feasible kosong → `ErrNoFeasibleStrategy{Reasons: [...]}`. Operator melihat *kenapa tiap strategi ditolak* — bukan kegagalan diam.

### 2.5 Stage 2 — Cost model (deterministik)

```go
type CostVector struct {
    TransferTime  time.Duration // dataSize / effectiveBandwidth
    Downtime      time.Duration // tergantung strategi (0 utk live, transfer utk dump)
    RiskDelta     float64       // 0..100, dari RiskEngine (Part 3)
    ResourceCost  float64       // CPU·s + RAM·s + IO abstrak
    AchievedRPO   time.Duration // 0 utk sync-replication, changeRate·cutoverGap utk async
    AchievedRTO   time.Duration // estimasi waktu rollback bila perlu
}

func (s dumpRestoreStrategy) Cost(in PlannerInput) CostVector {
    // effectiveBandwidth memperhitungkan RTT & overhead protokol — DIUKUR
    eff := effectiveBandwidth(in.Link.BandwidthBps, in.Link.RTTMillis)
    transfer := time.Duration(in.Workload.DataSizeBytes / eff) * time.Second
    return CostVector{
        TransferTime: transfer,
        Downtime:     transfer, // dump-restore = downtime selama transfer
        RiskDelta:    riskEngine.Delta(in, StrategyDumpRestore),
        ResourceCost: cpuSeconds(in) + ramSeconds(in),
        AchievedRPO:  0,        // konsisten pada titik dump
        AchievedRTO:  transfer, // restore balik butuh waktu sama
    }
}
```

Model biaya **wajib** teruji unit dengan angka nyata (bukan menegaskan "> 0"). Contoh test yang benar:

```go
func TestDumpRestoreCost_10GB_at_1Gbps(t *testing.T) {
    in := PlannerInput{
        Workload: WorkloadFacts{DataSizeBytes: 10 << 30},
        Link:     LinkFacts{BandwidthBps: 1 << 30 / 8, RTTMillis: 1}, // ~125 MB/s
    }
    c := dumpRestoreStrategy{}.Cost(in)
    // 10GiB / 125MB/s ≈ 82s; toleransi overhead ±20%
    if c.TransferTime < 68*time.Second || c.TransferTime > 98*time.Second {
        t.Fatalf("transfer time = %v, want ~82s", c.TransferTime)
    }
}
```

### 2.6 Stage 3 — Weighted scoring

```go
type CostAxis int
const (
    AxisDowntime CostAxis = iota
    AxisTransferTime
    AxisRisk
    AxisResource
    AxisRPO
)

// Score menormalkan tiap sumbu ke [0,1] (0 terbaik) lalu jumlahkan berbobot.
// Skor LEBIH RENDAH = LEBIH BAIK. Argmin dipilih.
func Score(c CostVector, p ScoringProfile, bounds CostBounds) float64 {
    return p.Weights[AxisDowntime]     * norm(c.Downtime, bounds.Downtime) +
           p.Weights[AxisTransferTime] * norm(c.TransferTime, bounds.TransferTime) +
           p.Weights[AxisRisk]         * c.RiskDelta/100 +
           p.Weights[AxisResource]     * norm(c.ResourceCost, bounds.Resource) +
           p.Weights[AxisRPO]          * norm(c.AchievedRPO, bounds.RPO)
}
```

Profil bawaan:

| Profile | Downtime | Transfer | Risk | Resource | RPO | Persona |
|---|---|---|---|---|---|---|
| `safety-first` (default) | 0.30 | 0.10 | 0.45 | 0.05 | 0.10 | Solo operator |
| `speed-first` | 0.40 | 0.35 | 0.15 | 0.05 | 0.05 | Deadline ketat |
| `cost-first` | 0.15 | 0.15 | 0.15 | 0.50 | 0.05 | MSP hemat resource |

**Default = `safety-first`** (konsekuensi P1). Operator memilih profil; ia tidak mengedit bobot kecuali advanced.

### 2.7 Output: Plan + Explanation

```go
type PlanDecision struct {
    Selected     StrategyKind
    Score        float64
    CostVector   CostVector
    Rejected     []RejectedStrategy // {kind, reason, score?} — TRANSPARANSI
    Explanation  string             // human-readable "why"
}
```

Setiap plan membawa *kenapa* dan *apa yang ditolak*. Ini bukan fitur mewah — ia prasyarat kepercayaan (North Star SRCR bergantung pada operator memahami keputusan). UI menampilkan: *"LiveReplication dipilih (skor 0.18). DumpRestore ditolak: downtime 82s > budget 30s."*

### 2.8 Planner selalu deterministik

Planner **tidak** memakai AI (lihat Part 6 §AI). Given input yang sama → output sama. Ini penting untuk:
- **Reproducibility** — replan menghasilkan plan identik.
- **Testability** — cost model diuji dengan angka pasti.
- **Auditability** — keputusan dapat dijelaskan tanpa "model bilang begitu".

AI boleh *menyarankan* objektif atau *menandai* anomali (advisory), tapi tiga tahap di atas tetap deterministik.

---

## 3. Driver SDK

### 3.1 Prinsip: drivers are the product (P7)

Core adalah *wasit*: ia tahu urutan langkah (Discovery→…→Cutover) tapi **tidak** tahu cara men-dump Postgres atau me-reload Nginx. Semua kecerdasan spesifik hidup di driver. Konsekuensi arsitektural:

- Core **tidak pernah** `import "meshium/drivers/postgres"`.
- Ekstensi terjadi lewat **registry + interface**, bukan `switch type`.
- Driver pihak ketiga bisa ditambah tanpa fork (H2 horizon).

### 3.2 Kontrak driver inti

```go
// Driver adalah kontrak yang SETIAP workload-type implementasikan.
// Semua metode menerima context (P5 observability) dan mengembalikan
// typed Result (P6 exit-codes) — lihat Part 3.
type Driver interface {
    // Identitas & kapabilitas
    Kind() WorkloadKind
    Capabilities() CapabilitySet

    // Fase pipeline (tiap fase = satu tanggung jawab)
    Discover(ctx context.Context, h Host) (Inventory, error)
    Validate(ctx context.Context, in Inventory) (ValidationReport, error)
    Compat(ctx context.Context, src, dst Inventory) (CompatReport, error)
    Plan(ctx context.Context, in PlannerInput) (DriverPlan, error)

    // Eksekusi — SEMUA idempotent (P3), diberi IdempotencyKey
    Migrate(ctx context.Context, step Step) (StepResult, error)
    Verify(ctx context.Context, step Step) (VerifyReport, error)

    // Recovery — directional & sadar-PONR (P2)
    Rollback(ctx context.Context, step Step, ponr bool) error
    Resume(ctx context.Context, cp Checkpoint) (Step, error)

    // Non-destruktif
    DryRun(ctx context.Context, step Step) (DryRunReport, error)
    HealthCheck(ctx context.Context, h Host) (HealthReport, error)
}
```

**Wajib compile-time:** driver tanpa `DryRun`/`Verify` **tidak bisa** dikompilasi (interface memaksa). Ini konsekuensi P1 yang dijamin kompiler, bukan review.

### 3.3 Capability negotiation

Tidak semua driver bisa semua hal. Redis bisa live-replication; SQLite tidak. Alih-alih `switch`, driver mendeklarasikan kapabilitas:

```go
type Capability string
const (
    CapLiveReplication  Capability = "live_replication"
    CapIncrementalSync  Capability = "incremental_sync"
    CapZeroDowntime     Capability = "zero_downtime"
    CapPointInTimeRestore Capability = "pitr"
    CapChecksumVerify   Capability = "checksum_verify"
    CapResume           Capability = "resume"
)

type CapabilitySet map[Capability]bool

func (c CapabilitySet) Has(cap Capability) bool { return c[cap] }
```

Planner **query** kapabilitas saat filter feasibility (§2.4). Bila operator minta zero-downtime tapi driver tidak `CapZeroDowntime` → strategi itu di-filter keluar dengan alasan jelas, bukan crash saat eksekusi.

### 3.4 Driver lifecycle & hooks

```
  Register ──▶ Discover ──▶ Validate ──▶ Compat ──▶ Plan
                                                      │
                              ┌───────────────────────┘
                              ▼
   ┌── PreMigrate hook ──▶ Migrate ──▶ Verify ──▶ PreCutover hook ──┐
   │                          │           │                          │
   │                    (checkpoint)  (checkpoint)                   ▼
   │                                                          Cutover (core)
   └──◀── Rollback ◀── (on failure, directional) ◀──────────────────┘
```

Hooks opsional (default no-op) agar driver menyisip logika spesifik:

```go
type HookProvider interface {
    PreMigrate(ctx context.Context, step Step) error   // mis. FLUSH, quiesce
    PostMigrate(ctx context.Context, step Step) error  // mis. ANALYZE, warm cache
    PreCutover(ctx context.Context, step Step) error   // mis. final sync, lock
    PostCutover(ctx context.Context, step Step) error  // mis. reset replication
}
```

### 3.5 Driver registry (bagaimana core menemukan driver)

```go
// Registry adalah SATU-SATUNYA titik di mana driver konkret diketahui.
// Ini di package drivers/registry, BUKAN di core. Core menerima Registry
// via dependency injection dan hanya melihat interface Driver.
type Registry struct {
    drivers map[WorkloadKind]DriverFactory
}

func (r *Registry) Register(kind WorkloadKind, f DriverFactory) { r.drivers[kind] = f }
func (r *Registry) Get(kind WorkloadKind) (Driver, error)       { ... }

// main.go merakit registry (composition root):
func buildRegistry() *Registry {
    r := NewRegistry()
    r.Register(KindPostgres, postgres.New)
    r.Register(KindDocker, docker.New)
    // ... core TIDAK tahu daftar ini; hanya main.go tahu.
    return r
}
```

Arch-test menjamin `core` tidak pernah `import drivers/*` (P7, ADR-3).

---

## 4. Katalog Driver — 33 Driver × 14 Aspek

Setiap driver dinilai pada 14 aspek. Legenda:
- ✅ = didukung penuh · ⚠️ = didukung terbatas/butuh syarat · ❌ = tidak didukung (fallback dipakai) · N/A = tak relevan

Aspek: **Disc**overy · **Val**idation · **Compat** · **Plan**ning · **Migr**ation · **Verif**y · **RB** (Rollback) · **Res**ume · **Rec**overy · **Dry**Run · **HC** (HealthCheck) · **Risk** (Score) · **ZDT** (Zero-Downtime) · **FB** (Fallback strategy)

### 4.1 Filesystem & Transfer

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Filesystem** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ checksum | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | snapshot→copy |
| **Rsync** | ✅ | ✅ | ✅ | ✅ | ✅ delta | ✅ `--checksum` | ✅ | ✅ partial | ✅ | ✅ `-n` | ✅ | ✅ | ⚠️ | full copy |
| **SCP** | ✅ | ✅ | ⚠️ | ✅ | ✅ | ⚠️ size-only | ⚠️ | ❌ | ⚠️ | ⚠️ | ✅ | ✅ | ❌ | rsync |
| **SFTP** | ✅ | ✅ | ⚠️ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ❌ | rsync |
| **NFS** | ✅ mounts | ✅ | ✅ | ✅ | ✅ remount | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | rsync copy |

**Detail Filesystem (driver acuan):**
- **Discovery:** enumerasi path, size (`du` portable), perms, xattr, ACL, symlink, hardlink, sparse.
- **Migration:** rsync dengan `-aHAX --numeric-ids` (preserve hardlink/ACL/xattr — memperbaiki H5 audit yang hilang flag ini).
- **Verify:** checksum per-file (`--checksum`), bukan size+mtime.
- **Rollback:** sebelum cutover = hapus target; sesudah = N/A (data tak menimpa source).
- **ZDT:** ⚠️ hanya bila di atas shared storage; jika tidak, butuh brief freeze.
- **Fallback:** bila rsync tak ada di target → `tar | ssh | tar` streaming.

### 4.2 Container & Runtime

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Docker** | ✅ | ✅ | ✅ arch | ✅ | ✅ image+vol | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | save/load |
| **Docker Compose** | ✅ parse | ✅ | ✅ | ✅ per-svc | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | recreate |
| **Podman** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | save/load |
| **Kubernetes** | ✅ manifests | ✅ | ⚠️ ver | ✅ | ⚠️ obj+PV | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ | ✅ svc | Velero |

**Detail Docker Compose:**
- **Discovery:** parse `compose.yaml` dengan **YAML parser sungguhan** (`gopkg.in/yaml.v3`, sudah ada di deps) — bukan parser indentasi tangan (memperbaiki H3 audit). Ekstrak service, image, volume, network, depends_on → jadikan sub-DAG.
- **Planning:** tiap service jadi node DAG; `depends_on` jadi edge; volume stateful direncanakan terpisah (bisa pakai driver DB bila volume itu data Postgres).
- **ZDT:** ⚠️ via blue-green recreate bila stateless; stateful volume ikut aturan driver datanya.

### 4.3 Databases (SQL)

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **PostgreSQL** | ✅ | ✅ | ✅ ver | ✅ | ✅ repl/dump | ✅ row+checksum | ✅ | ✅ WAL | ✅ | ✅ | ✅ | ✅ | ✅ logical repl | pg_dump |
| **MySQL** | ✅ | ✅ | ✅ ver | ✅ | ✅ repl/dump | ✅ | ✅ | ✅ binlog | ✅ | ✅ | ✅ | ✅ GTID repl | mysqldump |
| **MariaDB** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | mariadb-dump |
| **SQLite** | ✅ file | ✅ | ✅ | ✅ | ✅ file copy | ✅ `.dump` diff | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | ❌ | file copy+lock |

**Detail PostgreSQL (memperbaiki C1/H3 audit):**
- **Migration (live):** base backup via `pg_basebackup` **DULU**, lalu streaming replication. Audit menemukan MySQL setup melakukan CHANGE MASTER **tanpa base dump** → total data loss; desain ini menjadikan base backup sebagai langkah wajib berkode, bukan opsi.
- **Compat:** cek versi mayor (logical replication butuh ≥10), extension (`pg_stat_statements`, `postgis`), collation, encoding.
- **Verify:** `COUNT(*)` per tabel + checksum agregat (`md5(array_agg)`) untuk sampel; bukan hanya "koneksi OK".
- **pg_hba:** driver **tidak** membuka `0.0.0.0/0` (audit H3); ia menambah baris scoped ke IP target saja, dihapus saat cleanup.
- **Rollback pra-cutover:** drop replication slot + hapus target cluster. Pasca-PONR: promosi dibalik (target→source repl), **bukan** restore backup atas data live (P2).
- **ZDT:** logical replication + promosi; downtime = jeda cutover (detik).

### 4.4 Databases (NoSQL & Cache)

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Redis** | ✅ | ✅ | ✅ | ✅ | ✅ repl/RDB | ✅ DBSIZE+sample | ✅ | ⚠️ | ✅ | ✅ | ✅ | ✅ | ✅ replicaof | RDB copy |
| **MongoDB** | ✅ | ✅ | ✅ ver | ✅ | ✅ repl/dump | ✅ count+sample | ✅ | ✅ oplog | ✅ | ✅ | ✅ | ✅ replica set | mongodump |
| **Elasticsearch** | ✅ | ✅ | ⚠️ ver | ✅ | ✅ snapshot/reindex | ✅ doc count | ⚠️ | ✅ | ⚠️ | ✅ | ✅ | ✅ | ⚠️ alias | snapshot |
| **OpenSearch** | ✅ | ✅ | ⚠️ | ✅ | ✅ snapshot | ✅ | ⚠️ | ✅ | ⚠️ | ✅ | ✅ | ✅ | ⚠️ | snapshot |

**Detail Redis (memperbaiki M2 audit):**
- **Verify lag:** `redisLag` harus membedakan "field absent" (=belum sinkron/error) dari "0" (=caught up). Audit: kode lama return 0 saat field hilang → false "caught up". Desain: absennya `master_link_status:up` = **belum siap**, bukan siap.
- **Migration:** `REPLICAOF` target→source untuk live; RDB `SAVE`+copy untuk cold.
- **ZDT:** `REPLICAOF` lalu `REPLICAOF NO ONE` di target saat cutover.

**Detail MongoDB (memperbaiki H2 audit):**
- **Lag:** `mongoDBLag` (audit: selalu return error = stub) diimplementasikan via `rs.printSecondaryReplicationInfo()` / oplog timestamp diff. Bukan stub.

### 4.5 Object & Distributed Storage

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **MinIO** | ✅ | ✅ | ✅ | ✅ | ✅ mirror | ✅ etag | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | mc mirror |
| **Ceph** | ✅ | ⚠️ | ⚠️ | ✅ | ⚠️ rbd/rados | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ | ⚠️ | rados export |
| **GlusterFS** | ✅ | ⚠️ | ⚠️ | ✅ | ⚠️ geo-rep | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ | ⚠️ | rsync |

### 4.6 Message Queues

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **RabbitMQ** | ✅ | ✅ | ✅ | ✅ | ✅ shovel | ✅ queue depth | ✅ | ⚠️ | ✅ | ✅ | ✅ | ✅ | ⚠️ pause | export defs |
| **Kafka** | ✅ | ✅ | ⚠️ ver | ✅ | ✅ MirrorMaker2 | ✅ offset | ⚠️ | ✅ | ⚠️ | ✅ | ✅ | ✅ | ✅ MM2 | MM2 |
| **BullMQ** | ✅ | ✅ | ✅ | ✅ | ✅ (Redis) | ✅ | ✅ | ⚠️ | ✅ | ✅ | ✅ | ✅ | ⚠️ | RDB copy |

**Detail queue (grounded pada `queue.go` yang sudah ada & benar):** pause pada **source** host, drain active, sync (shovel/MM2/RDB), resume pada **source** saat rollback (bukan target — bug arah yang sudah diperbaiki di kode). Verify = queue depth match, bukan asумsi.

### 4.7 Web Servers & Reverse Proxy

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Nginx** | ✅ conf | ✅ `-t` | ✅ | ✅ | ✅ conf+certs | ✅ `-t`+curl | ✅ | N/A | ✅ | ✅ `-t` | ✅ | ✅ | ✅ reload | copy+reload |
| **Apache** | ✅ | ✅ `-t` | ✅ | ✅ | ✅ | ✅ | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ graceful | copy |
| **Caddy** | ✅ | ✅ validate | ✅ | ✅ | ✅ Caddyfile | ✅ | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ reload | copy |
| **Traefik** | ✅ | ✅ | ✅ | ✅ | ✅ dynamic | ✅ | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ hot | copy |

**Detail reverse proxy:** ini kunci cutover trafik. **Verify wajib `nginx -t` (config valid) + `curl` smoke test** sebelum reload. Rollback = restore config lama + reload (idempotent, tanpa downtime). ZDT ✅ karena reload graceful.

### 4.8 System & OS

| Driver | Disc | Val | Compat | Plan | Migr | Verif | RB | Res | Rec | Dry | HC | Risk | ZDT | Fallback |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Systemd** | ✅ units | ✅ | ✅ | ✅ | ✅ unit files | ✅ is-active | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ⚠️ | copy+reload |
| **Cron** | ✅ | ✅ | ✅ | ✅ | ✅ crontab | ✅ diff | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ | copy |
| **Firewall** | ✅ rules | ✅ | ⚠️ | ✅ | ✅ nft/ufw | ✅ diff | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ | ruleset copy |
| **Users/Groups** | ✅ | ✅ | ⚠️ uid | ✅ | ✅ passwd | ✅ diff | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ | getent copy |

**Detail Systemd:** copy unit files, `daemon-reload`, `enable`, tapi **start hanya saat cutover** (jangan jalankan dua instance stateful bersamaan). Compat: cek dependency unit ada di target.

**Detail Firewall (risiko lock-out):** urutan aturan penting; driver **verify konektivitas SSH tetap ada** sebelum apply (jangan kunci diri sendiri). Rollback cepat wajib.

### 4.9 Ringkasan cakupan

- **33 driver** terdefinisi (Filesystem, Rsync, SCP, SFTP, NFS, Docker, Compose, Podman, K8s, Postgres, MySQL, MariaDB, SQLite, Redis, MongoDB, Elasticsearch, OpenSearch, MinIO, Ceph, GlusterFS, RabbitMQ, Kafka, BullMQ, Nginx, Apache, Caddy, Traefik, Systemd, Cron, Firewall, Users, Groups, SH).
- **Prioritas H1 (0–12 bln):** Filesystem, Rsync, Docker, Compose, Postgres, MySQL, Redis, Nginx, Systemd — 9 driver menutup ~80% kasus solo-operator.
- **Prioritas H2:** sisanya, dibangun oleh komunitas via SDK.

---

## 5. Keputusan yang Dikunci Part Ini

| # | Keputusan | Dampak ke part lain |
|---|---|---|
| 1 | Planner = constraint-filter → cost-model → scoring [ADR-4] | Part 5 pipeline memanggil planner ini; Part 7 roadmap memprioritaskan cost model nyata |
| 2 | `PlannerInput` lengkap; `0`=belum diukur → `ErrInsufficientData` | Discovery (Part 3) wajib mengisi ukuran nyata |
| 3 | Driver interface memaksa `DryRun`+`Verify` compile-time | Part 6 arch-test menjaga `core !import drivers` |
| 4 | Capability negotiation menggerakkan feasibility filter | Cutover engine (Part 5) query `CapZeroDowntime` |
| 5 | 9 driver prioritas H1 | Part 7 roadmap v0.5/v0.8 |

---

*Lanjut ke Part 5 — Migration Pipeline, Checkpoint, Cutover, Rollback.*
