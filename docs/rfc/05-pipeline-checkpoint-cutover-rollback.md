# RFC-001 Part 5 — Migration Pipeline, Checkpoint, Cutover, Rollback

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 5 dari 7
> **Baca dulu:** [Part 3 — Engines & State](./03-engines-and-state.md), [Part 4 — Planner & Drivers](./04-planner-and-drivers.md)
> **Konvensi:** Prosa Bahasa Indonesia. Tipe, interface, tabel, diagram dalam Bahasa Inggris.

---

## 1. Ringkasan Eksekutif

Part ini mendesain jalur eksekusi nyata: bagaimana sebuah `Plan` (DAG langkah dari Part 4) dijalankan sebagai `Run`, bagaimana kemajuan disimpan agar bisa resume setelah crash, bagaimana trafik benar-benar dipindah pada cutover, dan bagaimana rollback bekerja tanpa menghancurkan data live.

Empat cacat audit yang di-resolve secara langsung di sini:

| Kode | Cacat pada implementasi saat ini | Resolusi di Part ini |
|---|---|---|
| **C2** | `trafficSwitchStage.Execute` hanya menulis baris DB — cutover palsu; `CutoverEngine`/`TrafficSwitchEngine` nyata jadi dead code | §5 Cutover Engine: cutover HARUS melewati verifikasi trafik nyata; "sukses" tanpa trafik pindah = compile-time impossible via `CutoverProof` |
| **C1** | `rollbackPipeline` memutar ulang backup pra-migrasi di atas data live pasca-cutover → data loss | §6 Rollback directional & PONR-aware: pasca-PONR rollback = forward-recovery, bukan restore |
| **H2** | Dual state/status non-atomik (bisa saling bertentangan) | §3 event log: state adalah proyeksi dari event, satu sumber kebenaran |
| **H3/C3** | Tidak ada transaksi DB; concurrent `WriteJSON` data race | §3 checkpoint transaksional; §7 event fan-out via single writer goroutine |

---

## 2. Migration Pipeline — Stages

### 2.1 Ordered stages

Pipeline adalah **DAG**, bukan list — tetapi ada urutan kanonik fase yang menjadi tulang punggung. Setiap fase berisi satu atau banyak langkah driver yang bisa paralel di dalamnya.

```
 ┌──────────┐   ┌───────────┐   ┌────────────┐   ┌──────────────┐
 │ Discovery│──▶│ Inventory │──▶│ Validation │──▶│Compatibility │
 └──────────┘   └───────────┘   └────────────┘   └──────┬───────┘
                                                        │
        ┌───────────────────────────────────────────────┘
        ▼
 ┌──────────┐   ┌────────┐   ┌────────────┐   ┌───────────┐
 │ Planning │──▶│ Backup │──▶│ Checkpoint │──▶│ Migration │
 └──────────┘   └────────┘   └────────────┘   └─────┬─────┘
                                                    │
        ┌────────────────────────────────────────────┘
        ▼
 ┌──────────────┐   ┌═══════════┐   ┌────────────┐   ┌──────────┐
 │ Verification │──▶║  Cutover  ║──▶│Health Check│──▶│ Finalize │
 └──────────────┘   ╚═════╤═════╝   └─────┬──────┘   └────┬─────┘
                          │ PONR          │ fail          │
                          ▼               ▼               ▼
                    (after PONR:    ┌──────────┐    ┌─────────┐
                     forward-only)  │ Rollback │    │ Cleanup │
                                    └──────────┘    └────┬────┘
                                                         ▼
                                                   ┌──────────┐   ┌────────┐
                                                   │  Audit   │──▶│ Report │
                                                   └──────────┘   └────────┘
```

Garis ganda (`═`) pada Cutover menandai bahwa **PONR ada di dalam fase Cutover**, bukan sebelum/sesudahnya. Lihat §5.3.

### 2.2 Kontrak tiap stage

Setiap stage mengimplementasikan interface yang sama (`Stage`), sehingga executor tidak perlu tahu stage apa yang dijalankan:

```go
type Stage interface {
    // ID unik & stabil (dipakai sebagai idempotency scope + checkpoint key).
    ID() StageID
    // Fase kanonik (untuk ordering & UI grouping).
    Phase() Phase
    // Langkah-langkah driver di dalam stage; boleh > 1 (paralel).
    Steps(ctx context.Context, run *RunContext) ([]Step, error)
    // Apakah stage ini boleh diskip bila checkpoint menandai selesai.
    Idempotent() bool
    // Batas waktu wajar; executor menambah context deadline.
    Timeout() time.Duration
    // Kompensasi bila stage ini perlu di-rollback (nil = tak ada aksi).
    Compensate(ctx context.Context, run *RunContext) error
}
```

Tabel kontrak per-stage (input → output, error handling, retry, checkpoint, rollback):

| Stage | Input | Output | Retry | Timeout | Checkpoint | Rollback (Compensate) |
|---|---|---|---|---|---|---|
| **Discovery** | target host creds | `Inventory` | idempotent, 3× | 5m | inventory hash | none (read-only) |
| **Inventory** | raw discovery | normalized `Inventory` | pure, 0× | 30s | full inventory | none |
| **Validation** | Inventory | `ValidationReport` | pure, 0× | 1m | report | none |
| **Compatibility** | src+tgt info | `CompatReport` | pure, 0× | 1m | report | none |
| **Planning** | Inventory+constraints | `Plan` (DAG) | pure, 0× | 30s | plan JSON | none |
| **Backup** | Plan | `BackupManifest` | idempotent, 2× | per-size | manifest + marker | delete backup (only pre-PONR) |
| **Checkpoint** | all above | persisted snapshot | transactional | 10s | — | none |
| **Migration** | Plan steps | per-step results | per-step policy | per-step | per-step marker | driver.Rollback per-step |
| **Verification** | tgt state | `VerifyReport` | idempotent, 2× | 5m | report | none |
| **Cutover** | verified tgt | `CutoverProof` | NO auto-retry | 2m | proof + PONR flag | directional (§6) |
| **Health Check** | live tgt | `HealthScore` | poll | 10m window | scores timeseries | trigger rollback if fail |
| **Finalize** | committed | — | idempotent | 30s | committed marker | none (terminal) |
| **Cleanup** | any terminal | — | best-effort | 2m | cleanup marker | none |
| **Audit** | full event log | `AuditRecord` | append-only | 30s | audit id | none |
| **Report** | AuditRecord | `Report` doc | pure | 30s | — | none |

**Aturan kunci:** Cutover punya `Retry = NO auto-retry`. Cutover yang gagal sebagian tidak boleh diulang otomatis karena bisa men-split-brain trafik. Kegagalan cutover memanggil rollback, bukan retry.

### 2.3 Error handling, cancellation, tracing

Setiap step dieksekusi lewat `Execution Engine` (Part 3) yang membawa:

- `context.Context` dengan deadline per-stage dan trace span.
- Cancellation: `ctx.Done()` menghentikan step; step yang sudah men-side-effect harus meninggalkan marker agar resume idempotent.
- Setiap step memancarkan event (`StepStarted`, `StepSucceeded`, `StepFailed`, `StepSkipped`) ke event log (§3).

---

## 3. Checkpoint System

### 3.1 Model: event log + materialized state (ADR-5)

Checkpoint **bukan** sekadar "kolom `current_stage` di tabel migrasi". Itu akar cacat H2 (dua field bisa bertentangan). Sebagai gantinya:

> **State adalah proyeksi (fold) dari sebuah append-only event log.** Tidak ada state yang ditulis tanpa event penyebabnya. UI-string, `current_stage`, dan progress % semua diturunkan dari event yang sama, dalam transaksi yang sama.

```
   append-only event log (source of truth)
   ┌────────────────────────────────────────────────────────┐
   │ seq │ run_id │ type            │ payload      │ ts       │
   ├─────┼────────┼─────────────────┼──────────────┼──────────┤
   │  1  │  R-42  │ RunCreated      │ {plan_id...} │ 10:00:00 │
   │  2  │  R-42  │ StageEntered    │ {stage:disc} │ 10:00:01 │
   │  3  │  R-42  │ StepSucceeded   │ {step:fs-1}  │ 10:02:11 │
   │ ... │        │                 │              │          │
   │  N  │  R-42  │ PONRCrossed     │ {proof_id}   │ 10:31:07 │
   └─────┴────────┴─────────────────┴──────────────┴──────────┘
                          │ fold()
                          ▼
   materialized state (a cache, rebuildable from log)
   ┌──────────────────────────────────────────────────────┐
   │ run_id │ state (typed) │ stage │ progress │ can_rollback │
   └──────────────────────────────────────────────────────┘
```

### 3.2 Interface

```go
type Event struct {
    Seq     uint64
    RunID   RunID
    Type    EventType
    Payload json.RawMessage
    At      time.Time
}

type EventLog interface {
    // Append MUST be atomic and MUST reject a seq gap (optimistic concurrency).
    Append(ctx context.Context, tx Tx, e Event) error
    // Load returns all events for a run, ordered by seq.
    Load(ctx context.Context, runID RunID) ([]Event, error)
    // Since streams events after a given seq (for live UI + resume).
    Since(ctx context.Context, runID RunID, afterSeq uint64) (<-chan Event, error)
}

// State is a pure fold — no I/O, fully deterministic, unit-testable.
func Project(events []Event) RunState { … }
```

### 3.3 Transaksionalitas (resolusi H3)

Setiap efek samping yang mengubah kemajuan ditulis dalam **satu transaksi DB** bersama event-nya:

```go
func (x *Executor) recordStep(ctx context.Context, runID RunID, ev Event, marker Marker) error {
    return x.db.WithTx(ctx, func(tx Tx) error {
        if err := x.log.Append(ctx, tx, ev); err != nil {   // 1. event
            return err
        }
        if err := x.markers.Set(ctx, tx, runID, marker); err != nil { // 2. idempotency marker
            return err
        }
        return x.state.Materialize(ctx, tx, runID, ev)       // 3. projected state
    })
}
```

Tidak mungkin ada state tertulis tanpa event, atau event tanpa marker. Ketiganya commit/rollback bersama.

### 3.4 Resume & crash recovery

Saat proses restart (mis. `meshium.service` di-restart — cacat main.go H3 yang menginterupsi migrasi):

```
1. Scan runs dengan state ∈ {running, cutover, ...} yang tidak terminal.
2. Untuk tiap run: Load(events) → Project() → RunState.
3. Tentukan resume point = stage terakhir dengan marker "started" tapi tanpa "succeeded".
4. Untuk stage idempotent: jalankan ulang dari awal stage (marker melindungi step selesai).
5. Untuk stage non-idempotent yang setengah jalan: masuk state `NeedsOperator` — jangan tebak.
```

**Idempotency key:** setiap step punya kunci `(runID, stageID, stepID)` yang persist. Driver menerima kunci ini dan wajib men-skip efek yang sudah tertandai. Contoh: `Backup` yang sudah menghasilkan manifest dengan marker tidak mengulang dump.

### 3.5 Partial failure & consistency

| Skenario | Perilaku |
|---|---|
| Crash sebelum event ditulis | Step dianggap belum terjadi → resume mengulang (idempotent) |
| Crash setelah event, sebelum efek eksternal commit | Tidak mungkin — efek eksternal (mis. dump file) ditulis dulu, lalu marker; marker absen → ulang |
| Efek eksternal selesai, marker gagal ditulis | Resume mengulang; driver mendeteksi artifact sudah ada (mis. file dump) → skip via content-addressed check |
| Crash di tengah cutover | State `cutover`, cek `PONRCrossed` event: ada → forward-recover; tidak ada → rollback |

---

## 4. Migration Execution — DAG Executor

### 4.1 Dari loop sekuensial ke DAG (resolusi arsitektural)

Implementasi saat ini adalah loop `for stage := range stages` sekuensial. Target:

```go
type DAGExecutor struct {
    log     EventLog
    drivers driver.Registry
    sem     *semaphore.Weighted   // batas paralelisme global
}

func (x *DAGExecutor) Run(ctx context.Context, plan *Plan, run *RunContext) error {
    dag := plan.DAG                      // sudah topologically valid (Kahn, dari discovery/graph.go)
    ready := dag.Roots()
    inflight := map[StepID]context.CancelFunc{}
    results := map[StepID]StepResult{}

    for len(results) < dag.Len() {
        // Dispatch semua node yang dependensinya sudah selesai.
        for _, node := range ready {
            if !x.dependenciesMet(node, results) { continue }
            x.dispatch(ctx, node, run)   // goroutine + semaphore
        }
        // Tunggu satu selesai; update results; hitung ready berikutnya.
        r := <-x.completions
        results[r.StepID] = r
        if r.Failed() && !r.Compensable() {
            x.cancelAll(inflight)        // fail-fast
            return x.beginRollback(ctx, run, results)
        }
        ready = dag.Unblocked(results)
    }
    return nil
}
```

Keuntungan: langkah independen (mis. migrasi dua volume berbeda) berjalan paralel; wall-clock = jalur kritis DAG, bukan jumlah semua langkah. Paralelisme dibatasi `semaphore` agar tidak membanjiri host.

### 4.2 Failure policy per node

Setiap node membawa `FailurePolicy` dari planner:

```go
type FailurePolicy struct {
    MaxRetries   int
    Backoff      BackoffStrategy   // exponential + jitter
    OnExhausted  Action            // FailRun | SkipStep | RollbackRun | NeedOperator
    Compensable  bool
}
```

---

## 5. Cutover Engine

### 5.1 Prinsip: cutover tidak bisa "melaporkan sukses" tanpa bukti (resolusi C2)

Cacat C2: `trafficSwitchStage.Execute` menulis baris DB `status='switched'` tanpa memindah trafik. Resolusi: cutover **wajib** menghasilkan `CutoverProof` yang hanya bisa dibuat oleh verifikasi trafik nyata. Stage cutover tidak "return nil" — ia "return proof or error".

```go
type CutoverProof struct {
    Strategy      CutoverStrategy
    SwitchedAt    time.Time
    // Bukti terukur — bukan sekadar flag:
    TargetServing bool          // health probe ke target via jalur trafik baru
    SourceDrained bool          // source tak lagi menerima koneksi baru
    Evidence      []Probe       // hasil probe konkret (HTTP 200, DNS resolves to tgt, LB member up)
    PONR          bool          // apakah titik ini melewati PONR
}

type CutoverEngine interface {
    // Execute mengembalikan proof HANYA jika verifikasi trafik lulus.
    // Tidak ada jalur yang mengembalikan proof tanpa Evidence non-kosong.
    Execute(ctx context.Context, plan CutoverPlan) (*CutoverProof, error)
}
```

Arch-test / review rule: `CutoverProof{}` literal (tanpa `Evidence`) dilarang di luar test; `Evidence` harus diisi oleh probe engine.

### 5.2 Strategi cutover

| Strategy | Cara | Kapan dipilih planner | PONR di mana |
|---|---|---|---|
| **Maintenance Mode** | source read-only / down, switch, verify | downtime budget longgar, data mutable | saat target mulai serving writes |
| **Blue-Green** | target (green) disiapkan penuh, switch atomik (LB/DNS) | butuh downtime minimal, ada LB | saat pointer LB pindah |
| **Canary** | sebagian trafik ke target, tambah bertahap | butuh validasi bertahap, stateless | saat 100% trafik + source dimatikan |
| **Rolling** | node demi node | banyak replika identik | saat replika terakhir pindah |
| **Read-Only + Promote** | source read-only, replica promote jadi primary | DB dengan replication | saat `PROMOTE`/`STOP SLAVE` dieksekusi |

### 5.3 PONR (Point of No Return) — eksplisit

PONR adalah **satu event** (`PONRCrossed`) yang ditulis ke log tepat sebelum operasi non-reversible pertama:

```
   pre-cutover (reversible)          PONR          post-cutover (forward-only)
 ──────────────────────────────────────┼──────────────────────────────────────▶
   • target siap                        │  • source promoted → replica
   • replikasi caught-up                │  • DNS TTL sudah kadaluarsa
   • dry-run switch OK                  │  • writes diterima target
   • BISA rollback = restore/revert     │  • rollback = forward-recover ke source
```

Sekuens penyeberangan PONR:

```
1. Verify: replication lag ≈ 0 (driver.Verify)          [reversible]
2. Freeze: source read-only / queue paused              [reversible — bisa unfreeze]
3. Final delta sync + verify checksum                   [reversible]
4. ── APPEND EVENT: PONRCrossed ──                       ⟵ titik komit
5. Promote target / switch traffic                       [FORWARD-ONLY]
6. Verify target serving (probe) → CutoverProof          [FORWARD-ONLY]
7. Drain + stop source                                   [FORWARD-ONLY]
```

Jika crash antara langkah 1–3: rollback biasa (unfreeze, buang delta). Jika crash setelah langkah 4: `PONRCrossed` ada di log → recovery masuk mode forward.

---

## 6. Rollback Engine

### 6.1 Prinsip: directional & PONR-aware (resolusi C1)

Cacat C1: `rollbackPipeline` me-restore backup pra-migrasi **di atas data live** setelah cutover — menghancurkan data yang sudah ditulis user ke target. Resolusi (P2 dari Part 1):

> Rollback **directional**. Sebelum PONR: rollback = *undo ke source, buang target*. Sesudah PONR: rollback = *forward-recovery, kembalikan trafik ke source yang masih utuh* — **JANGAN** menimpa target/source dengan backup lama.

```go
type RollbackEngine interface {
    Rollback(ctx context.Context, run *RunContext, cause error) (*RollbackResult, error)
}

func (r *rollbackEngine) Rollback(ctx context.Context, run *RunContext, cause error) (*RollbackResult, error) {
    if run.State.PONRCrossed {
        return r.forwardRecover(ctx, run)   // revert traffic to source; NEVER restore over live data
    }
    return r.compensate(ctx, run)           // walk completed steps in reverse, call driver.Rollback
}
```

### 6.2 Compensation (pra-PONR)

Rollback pra-PONR = jalankan `Compensate`/`driver.Rollback` untuk tiap step selesai, **dalam urutan terbalik** dari DAG:

```
completed:  [fs-1] → [db-1] → [nginx-1]        (forward order)
rollback:   [nginx-1] ← [db-1] ← [fs-1]        (reverse order, saga compensation)
```

Setiap driver bertanggung jawab atas rollback jenisnya sendiri (P7). Tabel cakupan rollback per-kategori:

| Kategori | Rollback pra-PONR | Rollback pasca-PONR (forward-recover) |
|---|---|---|
| **Filesystem** | hapus data yang ter-transfer di target | kembalikan mount/symlink ke source; jangan sentuh target |
| **Database** | drop DB target, buang replica | STOP SLAVE dibatalkan? tidak — arahkan app kembali ke source primary |
| **Queue** | resume source queue, buang target keys | arahkan worker ke source broker |
| **Container** | stop+rm container target | start container source, stop target |
| **Reverse Proxy** | pulihkan config source | flip upstream kembali ke source |
| **DNS** | tak ada perubahan (belum switch) | set record kembali ke source IP (dengan TTL rendah) |
| **Systemd** | disable unit target | enable+start unit source, stop target |
| **Firewall** | pulihkan ruleset source | buka kembali port source, tutup target |
| **Configuration** | restore file config source | flip config ke source |
| **Secrets** | revoke kredensial target yang dibuat | rotate kredensial yang mungkin bocor; jangan hapus source |

### 6.3 Aturan yang mengikat (arch-test)

```
- rollback pasca-PONR TIDAK PERNAH memanggil restore-backup-over-live.
- setiap driver.Rollback menerima `PONRCrossed bool`; kategori destruktif
  (restore over live) menolak eksekusi bila true (return ErrRollbackWouldDestroyLiveData).
- forward-recover HANYA mengubah routing/pointer (DNS/LB/proxy/app-config),
  tidak menyentuh isi datastore.
```

---

## 7. Event Fan-Out & WebSocket (resolusi C3)

Cacat C3: banyak goroutine memanggil `con.WriteJSON` bersamaan → data race. Resolusi: **single-writer goroutine** per koneksi; semua produser mengirim ke channel.

```go
type wsConn struct {
    out chan Event   // buffered
}

// SATU-SATUNYA penulis ke socket.
func (c *wsConn) writePump() {
    for ev := range c.out {
        _ = c.conn.WriteJSON(ev)   // hanya goroutine ini yang menulis
    }
}

// Produser (executor, resume, dll) tidak pernah menyentuh socket:
func (c *wsConn) Send(ev Event) {
    select {
    case c.out <- ev:
    default:            // slow consumer: drop / coalesce, jangan blok executor
    }
}
```

Resume tidak lagi memblok read loop (cacat H4): resume berjalan di goroutine terpisah, progress mengalir lewat `c.out` yang sama.

---

## 8. Sequence Diagram — Happy Path (Blue-Green + Postgres)

```
Operator   API      Executor    Planner   PG-Driver   EventLog   Target
   │        │          │           │          │          │         │
   │ start  │          │           │          │          │         │
   ├───────▶│ createRun│           │          │          │         │
   │        ├─────────▶│ plan()    │          │          │         │
   │        │          ├──────────▶│          │          │         │
   │        │          │◀──DAG─────┤          │          │         │
   │        │          ├─ append RunCreated ──────────▶│         │
   │        │          │ discovery/validate/compat ...  │         │
   │        │          ├─ Backup (marker) ────────────▶│         │
   │        │          ├──────────────────▶│ dump+ship │         │
   │        │          │                    │ initial   ├────────▶│
   │        │          │                    │ replicate │         │
   │        │          ├─ Verify: lag≈0 ───▶│ check ────┼────────▶│
   │        │          │  ══ CUTOVER ══      │          │         │
   │        │          ├─ freeze source ────▶│          │         │
   │        │          ├─ final delta ──────▶│          │         │
   │        │          ├─ append PONRCrossed ──────────▶│  ⟵PONR  │
   │        │          ├─ promote target ───▶│ PROMOTE ─┼────────▶│
   │        │          ├─ probe target ─────────────────┼────────▶│ 200 OK
   │        │          │◀── CutoverProof (Evidence) ─────┤         │
   │        │          ├─ health window ────────────────┼────────▶│
   │        │          ├─ append Committed ────────────▶│         │
   │◀───────┤ done     │           │          │          │         │
```

---

## 9. Keputusan yang Dikunci oleh Part Ini

1. **Event log = source of truth**, state = fold → dipakai Part 6 (persistence schema) dan menutup H2/H3.
2. **`CutoverProof` dengan `Evidence` wajib** → menutup C2; Part 6 audit log menyimpannya.
3. **`PONRCrossed` event eksplisit** → rollback directional (§6) menutup C1.
4. **Single-writer WS** → menutup C3/H4; Part 6 API layer memakai pola ini.
5. **DAG executor + per-node FailurePolicy** → menggantikan loop sekuensial.

---

*Lanjut ke Part 6 — Observability, Security, Plugin System, Persistence, CLI/API, AI Integration.*
