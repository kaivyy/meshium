# RFC-001 — Meshium Architecture Review

> **Status:** Draft · **Type:** Architecture RFC · **Scope:** Foundation for the next 10 years
> **Author:** Chief Software Architect (design authority for this RFC)
> **Date:** 2026-07-07

Dokumen ini adalah *architecture review* dan *target architecture* resmi untuk Meshium.
Ia **tidak terikat** pada implementasi repository saat ini. Bila sebuah subsistem harus
dirombak total, RFC ini mengatakannya secara eksplisit dan memberikan desain penggantinya.

RFC ini dipecah menjadi beberapa part agar tiap bagian padat, dapat direview sendiri, dan
dapat langsung diturunkan menjadi backlog pengembangan.

---

## Daftar Part

| Part | File | Isi |
|------|------|-----|
| 0 | `README.md` (dokumen ini) | Index, konvensi, ringkasan keputusan, glosarium |
| 1 | `part-1-vision-and-positioning.md` | Visi produk, core philosophy, product boundary, positioning vs tool lain |
| 2 | `part-2-domain-and-layout.md` | Domain, bounded context, module, dependency flow, package layout ideal |
| 3 | `part-3-engines-and-state.md` | Core engine, state machine vs workflow vs saga vs event sourcing, seluruh state & transisi |
| 4 | `part-4-planner-and-drivers.md` | Algoritma planner, Driver SDK, kontrak & lifecycle driver, capability negotiation |
| 5 | `part-5-pipeline-checkpoint-cutover-rollback.md` | Migration pipeline, checkpoint system, cutover engine, rollback engine |
| 6 | `part-6-observability-security-plugins-persistence-api-ai.md` | Observability, security model, plugin system, persistence, CLI/API, AI integration |
| 7 | `part-7-roadmap-and-recommendations.md` | Roadmap v0.5→v3.0, rekomendasi jujur, keputusan arsitektur kunci, anti-fitur |

---

## Konvensi Penulisan

- **Prosa** dalam Bahasa Indonesia. **Kode, nama interface, nama package, tabel teknis, diagram** dalam English.
- Pseudocode Go menggunakan sintaks Go yang valid tetapi boleh menyederhanakan (elipsis `…` untuk body yang tidak relevan).
- Diagram menggunakan ASCII agar diff-able dan tidak butuh tooling.
- Setiap keputusan besar diberi label **[ADR-n]** (Architecture Decision Record) agar dapat dirujuk lintas part.

---

## Ringkasan Keputusan Arsitektur (ADR Index)

| ADR | Keputusan | Part |
|-----|-----------|------|
| ADR-1 | Meshium adalah **migration orchestrator**, bukan backup/config-mgmt/provisioning tool | 1 |
| ADR-2 | **Durable workflow engine + FSM per-entity + saga untuk kompensasi** (hybrid), bukan salah satu saja | 3 |
| ADR-3 | Semua kerja workload dilakukan lewat **Driver SDK** dengan capability negotiation; core tidak tahu detail workload | 4 |
| ADR-4 | Planner adalah **hybrid: constraint-filter → cost-model → scoring**, bukan rule-based if-else | 4 |
| ADR-5 | **Checkpoint = event log + materialized state**; resume dan crash-recovery dibangun di atas event sourcing pada layer eksekusi | 3, 5 |
| ADR-6 | **Point-of-no-return** eksplisit pada pipeline; rollback bersifat directional & stage-scoped (tidak pernah menimpa data live pasca-cutover) | 5 |
| ADR-7 | **Semua efek samping ke remote host wajib idempotent** dan lolos exit-code check yang benar (bukan `if err != nil` saja) | 3, 5 |
| ADR-8 | **gRPC (internal) + REST (thin gateway) + WebSocket/SSE (streaming)**; CLI dan Web adalah klien setara di atas API yang sama | 6 |
| ADR-9 | **AI bersifat advisory & deterministik-by-default**; AI tidak pernah menjadi jalur eksekusi tanpa persetujuan manusia | 6 |
| ADR-10 | **Secrets zero-knowledge di server**; RBAC berbasis peran + scope per-target; least privilege by default | 6 |

---

## Glosarium

- **Workload** — unit yang dimigrasi: sebuah database, container, volume, reverse-proxy config, systemd unit, dsb.
- **Driver** — plugin yang tahu cara men-*discover/plan/migrate/verify/rollback* satu jenis workload.
- **Plan** — DAG langkah migrasi yang dihasilkan Planner dari sebuah Inventory + Strategy.
- **Run (Execution)** — satu eksekusi Plan; punya event log, checkpoint, dan state machine sendiri.
- **Cutover** — titik peralihan trafik dari source ke target.
- **Point of No Return (PoNR)** — batas setelah cutover berhasil; sesudahnya rollback = *revert traffic*, bukan *restore backup*.
- **Capability** — fitur yang didukung sebuah driver (mis. `SupportsLiveReplication`, `SupportsResume`).
- **Checkpoint** — snapshot state eksekusi yang cukup untuk resume secara idempotent.
