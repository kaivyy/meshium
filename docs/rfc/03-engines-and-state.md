# RFC-001 Part 3 — Core Engines, State Machine, dan Workflow Model

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 3 dari 7
> **Baca dulu:** [Part 2 — Domain & Layout](./02-domain-and-layout.md)
> **Konvensi:** Prosa Bahasa Indonesia. Tipe, interface, dan diagram dalam Bahasa Inggris.

---

## 1. Ringkasan Eksekutif

Part ini mendesain **jantung eksekusi** Meshium: bagaimana satu migrasi direpresentasikan, dijalankan, di-checkpoint, di-resume, dan di-rollback. Tiga keputusan dikunci di sini:

1. **[ADR-2] Hybrid: Durable Workflow Engine + FSM per-entity + Saga untuk kompensasi.** Bukan salah satu — masing-masing menyelesaikan masalah yang berbeda.
2. **[ADR-5] Checkpoint = event log + materialized state.** Resume dan crash-recovery dibangun di atas event sourcing pada layer eksekusi.
3. **[ADR-7] Tipe `Result` memaksa pengecekan exit code.** Pola `if err != nil`-saja dibuat tidak-bisa-dikompilasi untuk jalur yang salah.

---

## 2. Kenapa Bukan Satu Pola Saja

Audit menemukan pipeline saat ini adalah *sequential for-loop* dengan dua sumber kebenaran status yang bisa bertentangan (H2), tanpa DAG, tanpa transaksi (H3). Sebelum mendesain ulang, kita harus jujur soal pola mana yang cocok.

### 2.1 Empat kandidat

| Pola | Kekuatan | Kelemahan | Cocok untuk |
|---|---|---|---|
| **Pure FSM** | Sederhana, mudah diaudit, transisi eksplisit | Tidak menangani paralelisme; state explosion saat langkah banyak | Status lifecycle per-entity |
| **Workflow engine (durable)** | Resume tahan-crash, retry, timeout, paralel (DAG) | Kompleks; butuh event store | Orkestrasi langkah jangka panjang |
| **Event sourcing** | Audit sempurna, replay, time-travel | Overhead; eventual consistency di read-model | Checkpoint & recovery |
| **Saga** | Kompensasi terdistribusi eksplisit | Tidak menyimpan progress; hanya urutan undo | Rollback multi-langkah |

### 2.2 Keputusan: gabungkan sesuai lapisan

```
┌─────────────────────────────────────────────────────────────┐
│  Migration (aggregate root)                                   │
│                                                               │
│   FSM  ──────────────  status lifecycle per-migration         │
│    │                   (Created → … → Committed/RolledBack)    │
│    │                                                          │
│   Workflow Engine  ──  menjalankan DAG langkah, durable,      │
│    │                   resume, retry, timeout, paralel        │
│    │                                                          │
│   Event Log  ─────────  setiap transisi & step-result         │
│    │                   dicatat; checkpoint = fold(events)     │
│    │                                                          │
│   Saga  ──────────────  tiap langkah punya Compensate();      │
│                         rollback = jalankan Compensate        │
│                         dalam urutan terbalik s/d PONR         │
└─────────────────────────────────────────────────────────────┘
```

- **FSM** menjawab "migrasi ini sekarang di fase apa, boleh pindah ke mana".
- **Workflow engine** menjawab "langkah mana yang jalan, paralel atau tidak, sudah sampai mana".
- **Event log** menjawab "apa yang sudah benar-benar terjadi" — satu-satunya sumber kebenaran; FSM state & workflow progress adalah *materialized view* darinya.
- **Saga** menjawab "kalau harus mundur, apa yang di-undo dan dalam urutan apa".

Ini bukan over-engineering: tiap lapisan memperbaiki satu cacat audit spesifik (dual-state H2 → event log tunggal; sequential loop → workflow DAG; rollback destruktif C1 → saga sadar-PONR).

---

## 3. State Machine (FSM)

### 3.1 States

Repo saat ini punya 22 state (terverifikasi di `pipeline_engines_test.go`). Kita **pertahankan** himpunan itu — ia sudah benar secara domain — tetapi memperketat transisi dan menambah PONR sebagai konsep eksplisit.

```go
type MigrationState int

const (
    StateCreated MigrationState = iota
    StatePlanning
    StateDiscovery
    StateCompatibilityCheck
    StateRiskAssessment
    StateBackup
    StateProvisionTarget
    StateInstallDependencies
    StateInitialSync
    StateLiveReplication
    StateVerification
    StatePreCutover        // ← langkah terakhir SEBELUM PONR
    StateTrafficSwitch     // ← PONR: setelah sukses, rollback = revert traffic
    StatePostVerification
    StateObservation
    StateCommitted         // terminal (sukses)
    StateFailed
    StateRollback
    StateRolledBack        // terminal (mundur)
    StateInterrupted
    StateResuming
    StateCancelled         // terminal (batal)
)
```

### 3.2 Point of No Return

PONR bukan state tersendiri — ia adalah **predikat** atas state:

```go
// PONR tercapai begitu TrafficSwitch berhasil di-commit.
func (s MigrationState) PastPONR() bool {
    switch s {
    case StatePostVerification, StateObservation, StateCommitted:
        return true
    }
    return false
}
```

Konsekuensi mengikat (P2): rollback engine **menolak** operasi destruktif (restore backup di atas data live) bila `PastPONR()`; yang diizinkan hanya *forward-recovery* (kembalikan trafik ke source). Ini memperbaiki cacat C1 secara struktural.

### 3.3 Transition table

```
Created         → Planning | Cancelled
Planning        → Discovery | Failed | Cancelled
Discovery       → CompatibilityCheck | Failed | Interrupted
CompatibilityCheck → RiskAssessment | Failed | Interrupted
RiskAssessment  → Backup | Failed | Cancelled
Backup          → ProvisionTarget | Failed | Interrupted
ProvisionTarget → InstallDependencies | Failed | Interrupted
InstallDependencies → InitialSync | Failed | Interrupted
InitialSync     → LiveReplication | Verification | Failed | Interrupted
LiveReplication → Verification | Failed | Interrupted
Verification    → PreCutover | Failed | Rollback
PreCutover      → TrafficSwitch | Rollback | Failed      ← batas sebelum PONR
TrafficSwitch   → PostVerification | Rollback | Failed   ← PONR crossing
PostVerification → Observation | Rollback                (rollback = fwd-recovery)
Observation     → Committed | Rollback                   (rollback = fwd-recovery)
Failed          → Rollback | Interrupted
Rollback        → RolledBack | Failed
Interrupted     → Resuming | Cancelled | Rollback
Resuming        → <state sebelum interrupt>
```

Aturan yang dikunci:
- Tidak ada lompatan (mis. `Created → Committed` harus gagal — sudah diuji di `TestStateMachineExtended_FailedTransition`).
- `Interrupted` bisa dicapai dari semua fase non-terminal (crash/restart).
- `Resuming` mengembalikan ke fase terakhir yang tercatat di event log, bukan menebak.

### 3.4 Transisi transaksional (memperbaiki H2/H3)

```go
// Satu-satunya cara mengubah state. Menulis event DAN materialized state
// dalam SATU transaksi DB. Tidak ada UpdateStatus() terpisah.
func (m *MigrationAggregate) Transition(
    ctx context.Context, tx Tx, to MigrationState, cause Event,
) error {
    if !m.fsm.CanTransition(m.state, to) {
        return &InvalidTransitionError{From: m.state, To: to}
    }
    // 1) append event (source of truth)
    if err := tx.AppendEvent(ctx, m.id, cause); err != nil {
        return err
    }
    // 2) update materialized state DALAM tx yang sama
    if err := tx.UpdateState(ctx, m.id, to); err != nil {
        return err // rollback tx → event & state tidak pernah divergen
    }
    m.state = to
    return nil
}
```

Karena event dan state ditulis dalam transaksi yang sama, **mustahil** ada dua field yang bertentangan (P4). String status untuk UI diturunkan dari `to` — bukan disimpan terpisah.

---

## 4. Execution Contract — `Result` yang Memaksa Kebenaran

### 4.1 Akar masalah sistemik

Audit menemukan `ssh.ExecContext` mengembalikan `(stdout, stderr, exitCode, error)` dan **error NIL saat perintah berjalan tapi exit non-zero**. Ratusan pemanggil melakukan `if err != nil` saja → pengecekan itu **no-op**; kegagalan perintah lolos diam-diam (H1, dan akar C1 replikasi).

### 4.2 Desain: tipe yang tidak bisa disalahgunakan

```go
// Package: internal/platform/exec

// Result WAJIB dievaluasi via OK()/Err(); field mentah tidak diekspor
// sehingga pemanggil tidak bisa "lupa" cek exit code.
type Result struct {
    stdout   []byte
    stderr   []byte
    exitCode int
    // no exported error field — kegagalan transport vs kegagalan perintah dipisah
}

func (r Result) Stdout() string { return string(r.stdout) }
func (r Result) Stderr() string { return string(r.stderr) }
func (r Result) ExitCode() int  { return r.exitCode }

// OK() true hanya jika exit code == 0.
func (r Result) OK() bool { return r.exitCode == 0 }

// Err() mengembforcekan pengecekan: mengubah exit non-zero JADI error typed.
func (r Result) Err() error {
    if r.exitCode == 0 {
        return nil
    }
    return &CommandError{ExitCode: r.exitCode, Stderr: string(r.stderr)}
}

type Executor interface {
    // Run mengembalikan error HANYA untuk kegagalan transport (SSH putus,
    // context cancel). Kegagalan PERINTAH (exit != 0) ada di Result.Err().
    Run(ctx context.Context, cmd Command) (Result, error)

    // MustRun menggabungkan keduanya: satu error untuk transport ATAU exit!=0.
    // Ini jalur default; pemanggil yang butuh exit code spesifik pakai Run.
    MustRun(ctx context.Context, cmd Command) (Result, error)
}
```

### 4.3 Analyzer kustom (P6 sebagai lint yang mengikat)

```go
// tools/analyzer/execcheck: go vet plugin.
// GAGAL bila Result dari Run() dibuang tanpa memanggil OK()/Err()/ExitCode().
//
//   res, err := ex.Run(ctx, cmd)
//   if err != nil { ... }   // ← FLAGGED: Result diabaikan
//
// Pola benar:
//   res, err := ex.Run(ctx, cmd)
//   if err != nil { ... }          // transport
//   if err := res.Err(); err != nil { ... }  // exit code
```

Ini membuat cacat H1 **tidak bisa dikompilasi ulang** di CI, bukan sekadar "diperbaiki sekali".

### 4.4 Command injection (memperbaiki C3 transfer)

`Command` tidak menerima string mentah; ia membangun argv terkuota:

```go
type Command struct {
    argv []string // selalu argv, tidak pernah "sh -c <string interpolasi>"
}

func NewCommand(bin string, args ...string) Command { … }

// Bila perlu shell (pipe/redirect), argumen WAJIB lewat ShellQuote.
func Shell(script string, args ...ShellArg) Command { … }
```

Pemanggil tidak bisa lagi menulis `fmt.Sprintf("... '%s'", userVal)` — API tidak menyediakan jalan itu (memperbaiki C3 di `helpers.go`/`scp.go`).

---

## 5. Workflow Engine (DAG Executor)

### 5.1 Kenapa DAG, bukan for-loop

Pipeline saat ini sequential: migrasi 5 database + 3 volume berjalan serial walau independen. DAG memungkinkan paralelisme aman berdasarkan **dependency graph** yang sudah ada dan benar di `discovery/graph.go` (Kahn topo-sort + cycle detection — salah satu bagian yang audit nilai bagus dan **dipertahankan**).

### 5.2 Kontrak langkah

```go
type Step interface {
    ID() StepID
    // DependsOn: langkah yang harus selesai sebelum ini boleh jalan.
    DependsOn() []StepID

    // Execute: idempotent by construction. Menerima IdempotencyKey; efek
    // samping dijaga marker "sudah dilakukan" (P3).
    Execute(ctx context.Context, in StepInput) (StepOutput, error)

    // Compensate: undo untuk saga. WAJIB idempotent & aman dipanggil walau
    // Execute gagal separuh jalan.
    Compensate(ctx context.Context, in StepInput) error

    // Retryable & timeout dideklarasikan, bukan hardcoded di loop.
    Policy() StepPolicy
}

type StepPolicy struct {
    MaxRetries  int
    Backoff     BackoffStrategy
    Timeout     time.Duration
    Cancellable bool
    // Destructive: bila true, executor MENOLAK menjalankan Compensate
    // saat migrasi PastPONR() (mencegah restore backup di atas data live).
    Destructive bool
}
```

### 5.3 Eksekusi

```
1. Topo-sort steps → levels (level = himpunan langkah tanpa dependensi tersisa)
2. Untuk tiap level: jalankan semua langkah PARALEL (bounded worker pool)
3. Tiap langkah: Execute dengan retry/timeout sesuai Policy
4. Tiap hasil: append StepCompleted/StepFailed event (checkpoint)
5. Bila ada langkah gagal & tidak-retryable:
     - hentikan penjadwalan level baru
     - tunggu langkah in-flight selesai (graceful)
     - trigger saga rollback (§7)
```

### 5.4 Cancellation & timeout

Setiap `Execute` menerima `context.Context` turunan yang di-cancel saat: (a) timeout langkah, (b) user cancel, (c) langkah lain gagal fatal. Langkah **wajib** menghormati `ctx.Done()` — ini bagian kontrak, diuji dengan `TestHealthEngine_CheckDNS_CancelledContext` sebagai pola.

---

## 6. Checkpoint & Resume (Event Sourcing pada Layer Eksekusi)

### 6.1 Model

```
Event log (append-only, per-migration):
  [MigrationCreated]
  [StateChanged: Planning]
  [PlanGenerated: plan_id=…, steps=12]
  [StateChanged: Discovery]
  [StepCompleted: discover-fs, output_ref=…]
  [StepCompleted: discover-db, output_ref=…]
  [StateChanged: InitialSync]
  [StepStarted: sync-vol-1]
  [StepCompleted: sync-vol-1, bytes=…, checksum=…]
  [StepStarted: sync-db-1]         ← crash terjadi di sini
  ────────────────────────────────────────────────
Checkpoint = fold(events) → materialized state:
  current_state = InitialSync
  completed_steps = {discover-fs, discover-db, sync-vol-1}
  in_flight = {sync-db-1}          ← harus di-resume/re-run (idempotent)
```

### 6.2 Resume (memperbaiki shutdown H3 di main.go)

Audit: server shutdown memanggil `cancel()` sebelum `engine.Stop()` → migrasi terinterupsi tiap restart. Desain resume:

```go
func (e *Engine) Resume(ctx context.Context, migrationID ID) error {
    events, _ := e.store.LoadEvents(ctx, migrationID)
    state := Fold(events) // rekonstruksi tanpa menebak

    if state.CurrentState == StateInterrupted {
        // kembali ke fase terakhir yang stabil (bukan in-flight)
        target := state.LastStableState
        // langkah in-flight di-RE-RUN — aman karena idempotent (P3)
        return e.runFrom(ctx, migrationID, target, state.CompletedSteps)
    }
    …
}
```

Karena setiap langkah idempotent (P3) dan `completed_steps` persist, re-run langkah in-flight **tidak** menduplikasi efek samping. Ini yang membuat crash-recovery aman tanpa koordinasi dua-fase yang rumit.

### 6.3 Shutdown yang benar

```go
// main.go — urutan yang BENAR (audit menemukan urutan terbalik):
func (s *Server) Shutdown(ctx context.Context) error {
    // 1) berhenti menerima kerja baru
    s.httpServer.Shutdown(ctx)
    // 2) beri engine waktu men-checkpoint migrasi in-flight → Interrupted
    if err := s.engine.Stop(ctx); err != nil { // MENULIS StateChanged:Interrupted
        return err
    }
    // 3) BARU cancel context global
    s.cancel()
    return nil
}
```

---

## 7. Saga / Rollback Engine (Directional, Sadar-PONR)

### 7.1 Prinsip

Rollback bukan "restore backup". Rollback adalah **menjalankan `Compensate()` tiap langkah selesai dalam urutan terbalik**, dengan satu aturan keras:

```go
func (s *Saga) Rollback(ctx context.Context, m *MigrationAggregate) error {
    completed := m.CompletedStepsReverse() // LIFO
    for _, step := range completed {
        if step.Policy().Destructive && m.state.PastPONR() {
            // P2: JANGAN timpa data live. Ganti ke forward-recovery.
            s.log.Warn("skip destructive compensate past PONR", "step", step.ID())
            continue
        }
        if err := step.Compensate(ctx, s.inputFor(step)); err != nil {
            // kompensasi gagal → catat, lanjut (best-effort), tandai perlu intervensi
            s.recordCompensationFailure(m, step, err)
        }
    }
    return m.Transition(ctx, s.tx, StateRolledBack, EventRolledBack{})
}
```

### 7.2 Directional recovery pasca-PONR

Setelah PONR, "rollback" berarti:
- **Traffic:** kembalikan DNS/LB/VIP ke source (langkah cutover punya `Compensate` = revert traffic — ini non-destruktif).
- **Data:** JANGAN restore backup lama di atas target yang sudah menerima tulisan. Alih-alih, hentikan tulisan ke target, dan bila source masih hidup, jadikan source kembali otoritatif.

Ini memperbaiki C1 pipeline (rollback destruktif) dan C2 (cutover palsu) sekaligus: cutover jadi langkah nyata dengan `Compensate` yang benar, bukan sekadar tulis baris DB.

---

## 8. Peta Cacat Audit → Solusi Part Ini

| Cacat | Solusi struktural |
|---|---|
| C1 rollback destruktif pasca-cutover | PONR predikat + saga menolak destructive compensate (§3.2, §7.1) |
| C2 cutover palsu (hanya tulis DB) | Cutover jadi `Step` nyata dengan `Compensate` (§7.2) |
| H1 exit-code no-op | Tipe `Result.Err()` + analyzer (§4) |
| H2 dual-state divergen | Transisi transaksional event+state (§3.4) |
| H3 no transaction | `Transition` dalam satu `Tx` (§3.4) |
| H3 shutdown terbalik | Urutan Stop→cancel (§6.3) |
| C3 command injection | `Command` argv-only (§4.4) |
| Sequential no-DAG | Workflow DAG executor (§5) |

---

## 9. Keputusan yang Dikunci oleh Part Ini

1. **`Result`/`Executor` contract** → Part 4 (driver) dibangun di atasnya; tiap driver call lewat `Executor`.
2. **`Step` interface + Saga** → Part 4 mendefinisikan driver sebagai penyedia langkah; Part 5 merangkai langkah jadi pipeline.
3. **Event log + Fold** → Part 5 (checkpoint detail) & Part 6 (persistence schema) mengimplementasikannya.
4. **PONR predikat** → Part 5 menempatkan PONR persis di TrafficSwitch commit.

---

*Lanjut ke Part 4 — Planner Algorithm & Driver SDK.*
