# RFC-001 Part 7 — Roadmap dan Rekomendasi Jujur

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 7 dari 7
> **Baca dulu:** [README / index RFC](./README.md) · [Part 1](./01-vision-philosophy-boundary.md) · [Part 5](./05-pipeline-checkpoint-cutover-rollback.md)
> **Konvensi:** Prosa Bahasa Indonesia. Tipe, interface, tabel teknis dalam Bahasa Inggris.

---

## 1. Ringkasan Eksekutif

Part ini menutup RFC dengan hal yang paling sulit dan paling jujur: **urutan pengerjaan** dan **penilaian tanpa basa-basi** apakah semua ini layak dibangun. Roadmap di sini bukan daftar keinginan — ia adalah *urutan yang dipaksa oleh dependency arsitektural* yang sudah dikunci di Part 1–6. Anda tidak bisa membangun cutover (Part 5) sebelum state machine jujur (Part 3) ada; Anda tidak bisa membangun driver SDK (Part 4) sebelum kontrak `Result` exit-code-safe (Part 3, ADR-7) ada.

Satu kalimat pembuka yang harus jujur: **Meshium hari ini punya lebih banyak kode yang berpura-pura bekerja daripada kode yang benar-benar bekerja.** Audit menemukan cutover palsu (C2), rollback destruktif pasca-cutover (C1), replikasi MySQL tanpa base dump (C1), dan planner yang dead-code. Roadmap ini karena itu **bukan** "tambah fitur" — ia adalah "ganti fondasi yang retak, lalu bangun di atasnya".

---

## 2. Prinsip Penyusunan Roadmap

Tiga aturan yang mengikat urutan rilis:

1. **Trust sebelum breadth.** Tidak ada driver ke-6 sebelum 1 driver bisa migrasi + rollback + resume dengan benar end-to-end. Menambah cakupan di atas fondasi yang bocor hanya memperbanyak permukaan kegagalan.
2. **Setiap versi harus bisa dipakai di produksi untuk subset yang diklaimnya.** Tidak ada rilis "setengah jadi tapi banyak fitur". v0.5 yang hanya bisa Compose+Postgres tapi *benar* lebih berharga daripada v0.9 yang klaim 35 driver tapi semua rapuh.
3. **Breaking change hanya di batas versi mayor.** Setelah v1.0, format checkpoint/event-log (ADR-5) dan Driver SDK ABI (ADR-3) adalah kontrak. Perubahan mereka menunggu v2.0 dengan migration path eksplisit.

---

## 3. Roadmap v0.5 → v3.0

### 3.1 Peta jalan tingkat tinggi

```
 v0.5            v0.8            v1.0            v2.0             v3.0
 Foundation      Depth           GA / Trust      Platform         Ecosystem
 ────────        ─────           ──────────      ────────         ─────────
 State+Result    Planner         Driver SDK      Multi-target     Plugin
 honest core     cost-based      stable ABI      fleet            marketplace
 1 real path     5 drivers       15 drivers      policy engine    AI advisory GA
 checkpoint      observability   full obs        gRPC public      federation
 ────────        ─────           ──────────      ────────         ─────────
 "bisa dipercaya "bisa memilih   "orang lain     "banyak host,    "orang lain
  untuk 1 kasus"  strategi"       nulis driver"   satu kebijakan"  jualan driver"
```

### 3.2 v0.5 — Foundation (Trust untuk satu jalur)

**Tema:** Ganti fondasi yang retak. Satu jalur migrasi yang *benar-benar* bekerja, bisa di-rollback, bisa di-resume.

| Aspek | Isi |
|---|---|
| **Fitur** | (1) Tipe `Result` exit-code-safe [ADR-7]; (2) State machine transaksional tunggal [ADR-6, P4]; (3) Event log + checkpoint materialized [ADR-5]; (4) Satu jalur nyata: Compose + Postgres + Nginx discovery→plan→migrate→verify→cutover→rollback; (5) PONR eksplisit + rollback directional [ADR-6] |
| **Prioritas** | P0 semua — ini bukan fitur, ini perbaikan cacat data-loss |
| **Rationale** | Audit C1/C2 membuat versi sekarang berbahaya. Tidak boleh ada rilis publik sebelum ini beres |
| **Dependencies** | Tidak ada (ini akar). Semua part lain bergantung ke sini |
| **Kompleksitas** | Tinggi — menyentuh pipeline, replication, cutover, rollback sekaligus |
| **Risiko** | Menyentuh kode live; butuh test harness E2E dengan container nyata sebelum menyentuh apa pun |
| **Breaking change** | Ya — format state DB berubah dari dual-field ke event-sourced. Tidak ada migration path (pre-1.0, data migrasi lama dibuang dengan sengaja) |
| **Technical debt dihapus** | Cutover palsu (C2), rollback destruktif (C1), dual-state non-atomik (H2), `if err != nil`-only (H1/H4 sistemik) |

**Definition of done v0.5:** Bisa memigrasi stack Compose+Postgres+Nginx nyata dari host A ke host B, memverifikasi kesetaraan, lalu me-rollback-nya di *setiap* stage sebelum PONR tanpa kehilangan satu byte pun — dibuktikan oleh test E2E yang mematikan proses di tengah tiap stage dan me-resume.

### 3.3 v0.8 — Depth (Planner + Observability + 5 driver)

**Tema:** Bisa *memilih* strategi, bisa *diamati*.

| Aspek | Isi |
|---|---|
| **Fitur** | (1) Planner hybrid: constraint-filter → cost-model → scoring [ADR-4] dengan input nyata (bandwidth/latency/CPU/RAM/RPO/RTO); (2) Estimator berbasis pengukuran nyata (ganti fabricated sizes C3); (3) Observability penuh: OTel trace, Prometheus metrics, audit log [P5]; (4) 5 driver tervalidasi: Filesystem/Rsync, Docker Compose, Postgres, Redis, Systemd |
| **Prioritas** | P0: planner nyata + estimator; P1: observability; P1: 5 driver |
| **Rationale** | v0.5 membuktikan *satu* jalur; v0.8 membuktikan *pemilihan* jalur dan *pengamatan*-nya. Estimator fabricated (C3) membuat planner tak berguna tanpa ini |
| **Dependencies** | v0.5 (Result, state, checkpoint). Driver SDK draft [ADR-3] |
| **Kompleksitas** | Sedang-tinggi — cost model butuh kalibrasi empiris |
| **Risiko** | Cost model bisa salah estimasi; mitigasi: planner *menolak* (P1) saat confidence rendah, bukan menebak |
| **Breaking change** | Minor — `StrategyInput` bertambah field; planner API belum publik |
| **Technical debt dihapus** | Dead-code strategy selector (C1 planner), fabricated estimator (C3), rsync TOFU host-key (H5) |

### 3.4 v1.0 — GA / Trust (Driver SDK stabil, 15 driver)

**Tema:** Orang lain bisa menulis driver tanpa fork.

| Aspek | Isi |
|---|---|
| **Fitur** | (1) Driver SDK ABI stabil [ADR-3] dengan capability negotiation; (2) 15 driver GA (tambah: MySQL/MariaDB, MongoDB, SQLite, Podman, Docker, Nginx/Apache/Caddy, Cron, Users/Groups, Firewall, SFTP/SCP); (3) Security model lengkap [ADR-10]: RBAC, secrets zero-knowledge, host verification, session token per-client [lihat memory WS auth]; (4) CLI + REST + WebSocket setara di atas API sama [ADR-8]; (5) SBOM + binary signing (SLSA) |
| **Prioritas** | P0: SDK ABI freeze, security; P1: 15 driver; P1: CLI |
| **Rationale** | v1.0 = janji stabilitas. Setelah ini, SDK ABI dan format checkpoint adalah kontrak sampai v2.0 |
| **Dependencies** | v0.8 (planner, observability, 5 driver sebagai bukti SDK) |
| **Kompleksitas** | Tinggi — 10 driver baru, tiap driver 14 aspek (Part 4) |
| **Risiko** | Membekukan ABI terlalu dini → utang jangka panjang. Mitigasi: 5 driver v0.8 harus meng-exercise *setiap* method SDK sebelum freeze |
| **Breaking change** | Ini garis batas. Sesudah v1.0, breaking hanya di v2.0 |
| **Technical debt dihapus** | Global non-expiring session token, spoofable WS origin (Part 6 security), god-file `ai/service.go` dipecah, dua compat engine (H3) disatukan |

### 3.5 v2.0 — Platform (Multi-target, fleet, policy)

**Tema:** Banyak host, satu kebijakan.

| Aspek | Isi |
|---|---|
| **Fitur** | (1) Multi-target: satu plan → N target (fan-out); (2) Fleet view: ratusan migrasi terpantau; (3) Policy engine: "tolak migrasi tanpa backup terverifikasi", "wajib approval untuk PONR di prod"; (4) gRPC API publik [ADR-8]; (5) 25+ driver (tambah: Kafka, RabbitMQ, Elasticsearch/OpenSearch, MinIO, Traefik, Kubernetes-as-target) |
| **Prioritas** | P1: policy engine, fleet; P2: sisa driver |
| **Rationale** | Persona MSP/platform-engineer (Part 1 §2.4) butuh skala + kebijakan. Ini titik monetisasi |
| **Dependencies** | v1.0 (SDK stabil, security, API) |
| **Kompleksitas** | Sangat tinggi — fleet state, distributed execution |
| **Risiko** | Scope creep menuju "PaaS". Mitigasi: product boundary §4.2 sebagai gerbang |
| **Breaking change** | Ya — API v1→v2 dengan deprecation window 2 minor version; checkpoint format v1 dibaca (forward-compat), ditulis v2 |
| **Technical debt dihapus** | Single-node assumption di executor; sinkron-only WS (Part 6) |

### 3.6 v3.0 — Ecosystem (Plugin marketplace, AI advisory GA, federation)

**Tema:** Orang lain bisa jualan driver; AI membantu keputusan.

| Aspek | Isi |
|---|---|
| **Fitur** | (1) Plugin marketplace: driver pihak-ketiga tersigni + terverifikasi provenance; (2) AI advisory GA [ADR-9]: discovery-assist, compat-analysis, risk-narrative, rollback-recommendation — semua *advisory*, human-in-the-loop; (3) Federation: kontrol-plane lintas region; (4) 35 driver penuh |
| **Prioritas** | P2 semua — ini ekspansi, bukan fondasi |
| **Rationale** | Ekosistem hanya bermakna setelah SDK terbukti (v1.0) dan skala terbukti (v2.0) |
| **Dependencies** | v2.0 (API publik, policy, fleet) |
| **Kompleksitas** | Tinggi — supply-chain security untuk plugin pihak-ketiga |
| **Risiko** | AI memberi saran salah yang dipercaya buta. Mitigasi: ADR-9 — AI tidak pernah jadi jalur eksekusi tanpa persetujuan manusia; setiap saran AI menyertakan alasan deterministik yang bisa diaudit |
| **Breaking change** | Tidak diharapkan — v3 additive di atas v2 API |
| **Technical debt dihapus** | Rule-based keyword router di `ai/service.go` diganti advisory nyata |

### 3.7 Ringkasan dependency antar-versi

```
v0.5 (Result, state, checkpoint, 1 path)
  │  wajib sebelum
  ▼
v0.8 (planner, estimator, obs, 5 driver) ──── membuktikan SDK
  │  wajib sebelum
  ▼
v1.0 (SDK freeze, security, 15 driver, CLI) ── garis kontrak
  │  wajib sebelum
  ▼
v2.0 (multi-target, fleet, policy, gRPC, 25 driver)
  │  wajib sebelum
  ▼
v3.0 (marketplace, AI advisory, federation, 35 driver)
```

Tidak ada jalan pintas. Setiap panah adalah dependency arsitektural keras, bukan preferensi.

---

## 4. Rekomendasi Jujur

### 4.1 Apakah Meshium layak dikembangkan jadi proyek OSS besar?

**Jawaban jujur: Ya, tapi hanya jika v0.5 dikerjakan dengan disiplin brutal — dan tidak, jika v0.5 dilewati.**

Alasan **ya**:
- Kuadran "workload-level DAN transaksional/reversible" (Part 1 §5.2) benar-benar kosong kecuali Velero (yang terkunci di K8s). Ada ruang produk nyata.
- Persona inti (solo operator/small-shop) tidak terlayani. rsync terlalu rendah, Ansible tidak transaksional, Velero K8s-only. Ada pengguna nyata.
- Fondasi baik sudah ada: dependency DAG dengan topo-sort nyata (`discovery/graph.go`), kripto benar (AES-256-GCM, Argon2id, PBKDF2 600k), SQL parameterized.

Alasan **hati-hati**:
- Kode saat ini punya cacat data-loss kelas C1/C2. Merilis ini sebagai OSS *sekarang* akan menghancurkan kepercayaan permanen pada rilis pertama. Reputasi tool migrasi hancur oleh satu insiden kehilangan data.
- Cakupan ambisi (35 driver) vs realitas (0 driver yang benar end-to-end) adalah jurang. OSS yang menjanjikan 35 dan bisa 2 lebih dipercaya daripada yang klaim 35 dan rapuh.

**Rekomendasi:** Jangan umumkan sebagai OSS sampai v0.5 DoD tercapai. Bangun di publik (repo terbuka) boleh, tapi jangan *memasarkan* sampai satu jalur benar-benar aman.

### 4.2 Tiga keputusan arsitektur terpenting

1. **`Result` exit-code-safe yang memaksa pengecekan [ADR-7].** Ini akar dari cacat sistemik H1/H4. Membuat `if err != nil`-saja *tidak bisa dikompilasi* untuk jalur yang salah adalah keputusan tunggal dengan ROI keamanan tertinggi. Semua kebenaran eksekusi remote bergantung padanya.

2. **State jujur transaksional tunggal + PONR eksplisit [ADR-6, P4].** Menghapus kemungkinan "melaporkan sukses tanpa benar-benar sukses" (C2) dan "rollback destruktif pasca-cutover" (C1). North Star (SRCR) tidak bisa diukur tanpa ini.

3. **Driver SDK dengan capability negotiation [ADR-3, P7].** Menentukan apakah Meshium bisa tumbuh tanpa fork. `core` yang tidak pernah `import drivers/*` adalah pembeda antara "produk platform" dan "monolit yang membengkak". Keputusan ini harus dibuat *sebelum* driver ke-2 ditulis, karena mengubahnya nanti berarti menulis ulang semua driver.

### 4.3 Lima kesalahan desain yang harus dihindari sejak awal

1. **Menambah driver di atas fondasi yang belum aman.** Setiap driver baru di atas cutover palsu = satu permukaan kehilangan data baru. Fondasi dulu, selalu.
2. **`switch workloadType` di core.** Momen pertama core tahu apa itu "Postgres", P7 mati dan Meshium jadi monolit. Tangkap ini di arch-test CI sejak commit pertama.
3. **Dua sumber kebenaran status.** Dual-field state/status (H2) adalah bug yang menunggu. Satu state kanonik, turunan dalam transaksi sama — sejak awal, bukan retrofit.
4. **Optimasi untuk persona yang salah.** Membangun untuk "DBA tim besar dengan DB petabyte" (non-persona, Part 1 §2.4) akan menambah kompleksitas yang mengusir persona inti. Setiap default dioptimalkan untuk solo operator.
5. **AI sebagai jalur eksekusi.** Membiarkan AI *memutuskan* (bukan *menyarankan*) melanggar ADR-9 dan menghancurkan determinisme yang jadi inti kepercayaan. AI advisory, selamanya human-in-the-loop.

### 4.4 Sepuluh fitur dengan ROI teknis tertinggi

Diurutkan by (dampak-kepercayaan ÷ biaya-implementasi):

| # | Fitur | Kenapa ROI tinggi |
|---|---|---|
| 1 | Tipe `Result` exit-code-safe | Satu tipe menutup seluruh kelas cacat sistemik (H1/H4). Biaya rendah, dampak maksimal |
| 2 | PONR eksplisit + rollback directional | Menutup C1 (rollback destruktif). Mengubah "berbahaya" jadi "aman" |
| 3 | State transaksional tunggal | Menutup C2 (cutover palsu) + H2. Prasyarat SRCR |
| 4 | Event log + checkpoint resume | Crash-recovery gratis begitu event-sourced. Fondasi idempotency (P3) |
| 5 | `DryRun` wajib per-driver | Pengguna bisa lihat apa yang akan terjadi sebelum terjadi. Kepercayaan besar, biaya per-driver kecil |
| 6 | Estimator berbasis pengukuran nyata | Menutup C3 (fabricated sizes). Membuat planner berguna |
| 7 | Verify (kesetaraan source↔target) | Membuktikan migrasi benar, bukan sekadar "selesai". Inti nilai produk |
| 8 | Audit log lengkap | Persona MSP butuh ini; biaya rendah bila jadi bagian kontrak langkah (P5) |
| 9 | Host-key verification benar (ganti TOFU) | Menutup H5 (MITM pada bulk data). Satu perbaikan, seluruh transfer aman |
| 10 | Capability negotiation di SDK | Membuat driver bisa jujur soal "aku tidak bisa zero-downtime". Mencegah janji palsu |

### 4.5 Lima fitur yang TIDAK boleh ditambahkan

Dari product boundary (Part 1 §4.2), fitur-fitur ini menambah kompleksitas tanpa manfaat proporsional dan mengaburkan identitas produk:

| Fitur anti | Kenapa jangan | Delegasikan ke |
|---|---|---|
| **Desired-state reconciliation loop** | Meshium adalah operasi *sekali-pindah*, bukan control loop. Menambah ini = jadi Ansible/K8s yang buruk | Ansible/Salt/K8s |
| **Provisioning infrastruktur** (bikin VM/network/DNS zone) | Domain deklaratif matang; Meshium isi & pindahkan, bukan bikin | Terraform/OpenTofu |
| **Backup terjadwal jangka panjang + retensi** | Backup Meshium *taktis* untuk rollback. Retensi = produk berbeda | Restic/Borg/Velero |
| **Monitoring produksi berkelanjutan** | Observability Meshium berakhir saat migrasi selesai. Menjadi Grafana = kehilangan fokus | Prometheus/Grafana |
| **Menjadi tempat tinggal workload (PaaS)** | "Jembatan, bukan rumah" (§4.3). Menjalankan app jangka panjang bukan domain Meshium | Fly/Render/K8s |

Aturan gerbang untuk setiap usulan fitur masa depan: *"Apakah ini membantu memindahkan workload dengan aman, atau membantu menjalankannya setelah pindah?"* Jika yang kedua — tolak.

---

## 5. Penutup RFC

RFC ini mendefinisikan Meshium sebagai **migration orchestrator** yang aman, idempotent, observable, dan reversible — dengan 10 ADR yang saling mengunci dari visi (Part 1) sampai roadmap (Part 7). Benang merah tunggal: **kepercayaan adalah fitur nomor nol.** Setiap keputusan — `Result` yang memaksa exit-code check, PONR yang membuat rollback directional, state tunggal yang jujur, driver SDK yang menjaga core tetap bodoh — melayani satu tujuan: sebuah tool migrasi yang tidak pernah kehilangan data dan tidak pernah berbohong soal statusnya.

Urutan pengerjaan tidak fleksibel: **fondasi jujur dulu (v0.5), baru cakupan (v0.8+).** Melewati v0.5 berarti membangun gedung di atas retakan yang sudah diketahui.

---

*Selesai. RFC-001 lengkap: [Part 1](./01-vision-philosophy-boundary.md) · [Part 2](./02-domain-and-layout.md) · [Part 3](./03-engines-and-state.md) · [Part 4](./04-planner-and-drivers.md) · [Part 5](./05-pipeline-checkpoint-cutover-rollback.md) · [Part 6](./06-observability-security-plugins-persistence-api-ai.md) · Part 7 (dokumen ini).*
