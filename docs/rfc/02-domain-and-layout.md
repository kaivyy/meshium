# RFC-001 Part 2 — Domain, Bounded Context, dan Package Layout Ideal

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 2 dari 7
> **Baca dulu:** [Part 1 — Visi & Positioning](./01-vision-philosophy-boundary.md)
> **Konvensi:** Prosa Bahasa Indonesia; kode/interface/tabel Bahasa Inggris.

---

## 1. Ringkasan

Part 1 mengunci *apa* yang Meshium adalah. Part ini mengunci *bagaimana kode diorganisasi* agar prinsip P1–P7 tidak bisa dilanggar secara tidak sengaja. Klaim utama: **struktur package harus membuat pelanggaran arsitektur gagal compile**, bukan sekadar gagal review. Dependency direction adalah invariant yang di-*enforce* oleh arch-test di CI.

Kondisi repo saat ini (`internal/mod/*` yang saling `import` bebas, `core` tahu tentang Postgres, dua compatibility engine hidup berdampingan) adalah akibat langsung dari tidak adanya batas yang di-*enforce*. Part ini merancang batas itu.

---

## 2. Domain Utama & Ubiquitous Language

Domain inti Meshium: **memindahkan sebuah Workload dari Source ke Target secara reversible dan terverifikasi.**

Istilah kanonik (dipakai identik di kode, DB, API, UI):

| Term | Definisi | Bukan |
|---|---|---|
| **Workload** | Unit yang dimigrasi (DB, container, volume, proxy config, unit systemd) | Bukan "server"; sebuah host punya banyak workload |
| **Inventory** | Hasil Discovery: kumpulan Workload + dependensi antar-mereka | Bukan "backup" |
| **Plan** | DAG langkah yang dihasilkan Planner dari Inventory + Constraints | Bukan "script" |
| **Run** | Satu eksekusi Plan; punya event log + state machine sendiri | Bukan "job" |
| **Step** | Simpul DAG; unit idempotent terkecil yang punya checkpoint | Bukan "command" |
| **Driver** | Plugin yang tahu cara meng-handle satu jenis Workload | Bukan "adapter generik" |
| **Cutover** | Peralihan trafik source→target | Bukan "sync selesai" |
| **PONR** | Point of No Return; batas setelah cutover terverifikasi | — |

---

## 3. Bounded Contexts

Empat bounded context. Tiap context punya model, bahasa, dan siklus hidup sendiri; komunikasi antar-context hanya lewat kontrak eksplisit (interface / event), tidak pernah lewat struct bersama yang bocor.

```
┌───────────────────────────────────────────────────────────────┐
│                        MESHIUM DOMAIN                           │
│                                                                 │
│  ┌──────────────┐   Inventory    ┌──────────────┐              │
│  │  DISCOVERY   │ ─────────────▶ │   PLANNING   │              │
│  │  context     │                │   context    │              │
│  └──────────────┘                └──────┬───────┘              │
│         ▲                                │ Plan (DAG)           │
│         │ probes via Driver              ▼                      │
│  ┌──────┴───────────────────────────────────────────┐         │
│  │              EXECUTION context                     │         │
│  │  (Run · Step · Checkpoint · State Machine · Saga)  │         │
│  └──────┬───────────────────────────────────────────┘         │
│         │ emits events                                          │
│         ▼                                                       │
│  ┌──────────────┐                                              │
│  │ OBSERVABILITY│  (Audit · Metrics · Timeline · Trace)        │
│  │  context     │                                              │
│  └──────────────┘                                              │
│                                                                 │
│  Cross-cutting (dipakai semua, tergantung tak-satu-pun):        │
│  ┌───────────────────────────────────────────────────────┐    │
│  │  Transport (ssh/local) · Secrets · Result · IDs · Log  │    │
│  └───────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────┘
```

**Aturan arah dependensi (ADR-11):**

1. Discovery → (tak tergantung context lain; hanya cross-cutting + Driver contract).
2. Planning → membaca Inventory (tipe milik Discovery, di-*share* sebagai read-model), tak menulis apa pun.
3. Execution → mengeksekusi Plan; memanggil Driver; menulis event.
4. Observability → hanya *mengkonsumsi* event; tak pernah dipanggil oleh yang lain secara sinkron (tak boleh memblok eksekusi).
5. **Driver** bergantung pada Driver *contract* + cross-cutting; **tak pernah** pada Execution/Planning.
6. **Core (Execution)** tak pernah `import` driver konkret (P7).

---

## 4. Dependency Flow (acyclic, di-enforce)

```
                    ┌─────────────┐
                    │   cmd/*     │  (composition root: wiring saja)
                    └──────┬──────┘
                           │ constructs & injects
        ┌──────────────────┼──────────────────────┐
        ▼                  ▼                        ▼
 ┌────────────┐    ┌──────────────┐        ┌──────────────┐
 │ api (grpc/ │    │  engine      │        │  drivers/*   │
 │  rest/ws)  │───▶│ (execution)  │◀───────│ (postgres,   │
 └────────────┘    └──────┬───────┘ via    │  docker, …)  │
                          │         registry└──────┬───────┘
        ┌─────────────────┼──────────────┐         │
        ▼                 ▼              ▼          ▼
  ┌──────────┐     ┌───────────┐  ┌───────────┐  ┌──────────────┐
  │ planner  │     │ discovery │  │  driver   │  │  transport   │
  │          │     │           │  │  (SDK/    │  │ (ssh/local)  │
  │          │     │           │  │  contract)│  │              │
  └────┬─────┘     └─────┬─────┘  └─────┬─────┘  └──────┬───────┘
       │                 │              │               │
       └────────┬────────┴──────┬───────┴───────┬───────┘
                ▼               ▼               ▼
          ┌───────────────────────────────────────────┐
          │  foundation:  result · ids · secrets ·     │
          │  log · errors · shellquote · types         │
          └───────────────────────────────────────────┘
```

Semua panah mengarah **ke bawah**. Tidak ada panah balik. `foundation` tak `import` apa pun internal. `engine` tak `import` `drivers/*` (hanya `driver` contract + `registry`). Ini di-enforce oleh arch-test (§7).

---

## 5. Package Layout Ideal (jika repo di-restart dari nol)

```
meshium/
├── cmd/
│   ├── meshiumd/            # server daemon (composition root)
│   │   └── main.go
│   └── meshium/             # CLI (thin gRPC client)
│       └── main.go
│
├── api/                     # API surface — thin, no business logic
│   ├── grpc/                # gRPC service impl (delegates to engine)
│   ├── rest/                # REST gateway (grpc-gateway atau manual)
│   └── ws/                  # WebSocket/SSE streaming (event fan-out)
│
├── internal/
│   ├── foundation/          # cross-cutting; import-able oleh SEMUA
│   │   ├── result/          # Result{stdout,stderr,exit,err} + .OK()  [P6]
│   │   ├── ids/             # typed IDs (RunID, StepID, WorkloadID)
│   │   ├── secret/          # Secret[T] wrapper; no plaintext in logs
│   │   ├── xerr/            # typed error kinds (Retryable, Fatal, …)
│   │   ├── shellquote/      # ShellQuote (satu-satunya jalan bikin cmd)
│   │   └── clock/           # injectable time (testability)
│   │
│   ├── transport/           # HOW to run a command remotely/locally
│   │   ├── transport.go     # interface Executor
│   │   ├── ssh/             # ssh impl → returns result.Result
│   │   └── local/           # local impl
│   │
│   ├── driver/              # Driver SDK / CONTRACT (no concrete driver)
│   │   ├── driver.go        # interface Driver + lifecycle
│   │   ├── capability.go    # Capability negotiation types
│   │   ├── registry.go      # Register/Lookup (populated di cmd/)
│   │   └── testkit/         # conformance test harness untuk driver
│   │
│   ├── drivers/             # CONCRETE drivers (satu package per workload)
│   │   ├── filesystem/      #   ← boleh import driver, transport, foundation
│   │   ├── rsync/
│   │   ├── docker/
│   │   ├── compose/
│   │   ├── postgres/
│   │   ├── mysql/
│   │   ├── redis/
│   │   ├── mongodb/
│   │   ├── rabbitmq/
│   │   ├── nginx/
│   │   ├── systemd/
│   │   └── …                #   ~35 driver, tiap satu terisolasi
│   │
│   ├── discovery/           # DISCOVERY context
│   │   ├── discovery.go     # orchestrates driver.Discover()
│   │   ├── inventory.go     # Inventory read-model
│   │   └── graph.go         # dependency DAG (Kahn topo — KEEP from current)
│   │
│   ├── planner/             # PLANNING context
│   │   ├── planner.go       # Inventory+Constraints → Plan
│   │   ├── constraint.go    # hard filters (downtime/RPO/RTO/arch)
│   │   ├── cost.go          # cost model (bytes/bandwidth/latency)
│   │   ├── score.go         # weighted scoring across candidate strategies
│   │   └── plan.go          # Plan (DAG) type
│   │
│   ├── engine/              # EXECUTION context — the referee (P7)
│   │   ├── engine.go        # Run lifecycle
│   │   ├── fsm.go           # per-Run state machine (typed states)
│   │   ├── dag.go           # DAG executor (parallel, dependency-aware)
│   │   ├── step.go          # Step contract + idempotency key
│   │   ├── checkpoint.go    # event log + materialized state  [ADR-5]
│   │   ├── saga.go          # compensation orchestration
│   │   └── cutover/         # cutover + rollback strategies (directional)
│   │
│   ├── observ/              # OBSERVABILITY context (consume-only)
│   │   ├── audit.go
│   │   ├── metrics.go       # prometheus
│   │   ├── trace.go         # otel
│   │   └── timeline.go      # execution timeline projection
│   │
│   └── store/               # PERSISTENCE (interfaces + sqlite/pg impl)
│       ├── store.go         # repo interfaces (RunRepo, EventRepo, …)
│       ├── sqlite/
│       └── migrations/
│
├── web/                     # SvelteKit (embedded via go:embed)
└── docs/rfc/                # dokumen ini
```

### 5.1 Alasan tiap package (ringkas)

| Package | Kenapa ada | Public API inti | Tak boleh import |
|---|---|---|---|
| `foundation/result` | Memaksa exit-code check [P6] | `Result`, `.OK() error` | apa pun internal |
| `foundation/secret` | Cegah plaintext bocor ke log | `Secret[T]`, `.Expose()` | log package |
| `transport` | Pisahkan *how to run* dari *what to run* | `Executor.Run(ctx,Cmd) Result` | driver, engine |
| `driver` | Kontrak plugin; jantung P7 | `Driver`, `Capability`, `Registry` | drivers, engine |
| `drivers/*` | Kecerdasan spesifik workload | implement `driver.Driver` | satu sama lain |
| `discovery` | Bangun Inventory | `Discover(ctx) Inventory` | planner, engine |
| `planner` | Pilih strategi | `Plan(Inventory,Constraints) Plan` | engine, drivers |
| `engine` | Orkestrasi (referee) | `Run(ctx,Plan) RunResult` | **drivers/\*** |
| `observ` | Jejak | consume `Event` | engine (sinkron) |
| `store` | Persist | repo interfaces | domain logic |

### 5.2 Package yang **haram** saling bergantung

- `engine` ✗→ `drivers/*` (harus lewat `driver.Registry`).
- `drivers/postgres` ✗→ `drivers/mysql` (driver terisolasi; shared code naik ke `driver` SDK atau `foundation`).
- `foundation/*` ✗→ apa pun di `internal/` selain `foundation`.
- `planner` ✗→ `engine` (planner menghasilkan Plan, tidak mengeksekusinya).
- `observ` ✗→ dipanggil sinkron oleh `engine` (hanya event async; blocking observ = blocking migrasi = pelanggaran P5 tujuannya).

---

## 6. Perbandingan dengan Struktur Saat Ini

| Aspek | Sekarang | Target | Kenapa berubah |
|---|---|---|---|
| Layout | `internal/mod/{migration,planner,discovery,ai,transfer,auth}` saling import | bounded-context + contract packages | cegah cyclic & core→driver |
| Eksekusi command | `ssh.ExecContext → (out,err,exit,error)`, nil error saat exit≠0 | `transport.Executor.Run → result.Result`, `.OK()` wajib | akar H1/H4 (exit code diabaikan) |
| Workload logic | `switch type` di `migration/*` & `ai/service.go` | satu package per driver, registry | P7; hapus god-file |
| Compat engine | dua engine (`discovery/compat.go`) | satu, di `drivers/*.Compatibility()` | hapus dead code |
| Cutover | `pipeline.go` nulis DB row = fake | `engine/cutover` directional | akar C2 |
| State | dual state/status non-atomik | satu FSM + derived string in-txn | P4, akar H2 |

---

## 7. Enforcement: Arch-Test (bukan sekadar konvensi)

Batas di atas tak berguna kalau hanya dokumentasi. Ditegakkan lewat test yang gagal di CI:

```go
// internal/arch/arch_test.go
package arch

import (
	"testing"
	"github.com/some/depguard-or-hand-rolled"
)

var rules = []Rule{
	{From: "internal/engine", MustNotImport: "internal/drivers/"},   // P7
	{From: "internal/foundation", MustNotImport: "internal/"},        // foundation pure
	{From: "internal/planner", MustNotImport: "internal/engine"},
	{From: "internal/drivers/", MustNotImportSibling: true},          // driver isolation
	{From: "internal/observ", MustNotImport: "internal/engine"},
}

func TestArchitectureBoundaries(t *testing.T) {
	for _, r := range rules {
		if viol := r.Check(); len(viol) > 0 {
			t.Errorf("arch violation %s: %v", r, viol)
		}
	}
}
```

> Implementasi `Check()` bisa pakai `golang.org/x/tools/go/packages` untuk membaca import graph — **tanpa dependency baru** kalau kita batasi ke stdlib `go/parser` + `go/build`. (Sesuai constraint: no new deps kalau bisa dihindari.)

Tambahan grep-based guard di CI (murah, cepat):

```
! grep -rn "fmt.Print" internal/engine internal/drivers    # P5
! grep -rn "StrictHostKeyChecking=accept-new" internal/      # H5 (blind TOFU)
! grep -rn "%s'" internal/drivers internal/transport        # cegah cmd injection non-ShellQuote
```

---

## 8. Keputusan yang Dikunci oleh Part Ini

1. **[ADR-11] Dependency direction** = acyclic, downward-only, di-enforce arch-test → semua part berikut wajib patuh.
2. **`foundation/result.Result` + `.OK()`** jadi tipe fondasi → Part 3 mendetailkannya (P6).
3. **`driver` contract package** terpisah dari `drivers/*` → Part 4 mendesain isinya.
4. **`engine/cutover` directional** → Part 5.
5. **Satu FSM per Run di `engine/fsm.go`** → Part 3 mendefinisikan seluruh state.

---

*Lanjut ke Part 3 — Core Engines, State Machine vs Workflow vs Saga vs Event Sourcing.*
