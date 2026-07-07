# RFC-001 Part 1 — Visi, Filosofi, Product Boundary, dan Positioning

> **Status:** Draft · **Penulis:** Chief Software Architect · **Bagian:** 1 dari 7
> **Baca dulu:** [README / index RFC](./README.md)
> **Konvensi:** Prosa Bahasa Indonesia. Istilah teknis, nama tipe, interface, dan tabel dibiarkan dalam Bahasa Inggris agar langsung dapat dipakai di kode.

---

## 1. Ringkasan Eksekutif

Meshium hari ini adalah *inventory + SSH-automation dashboard* dengan pipeline migrasi eksperimental. Dokumen ini bukan tambalan atas keadaan itu — ini adalah **fondasi 10 tahun**: mendefinisikan apa yang Meshium *adalah*, apa yang *bukan*, dan bentuk arsitektur yang membuatnya bisa menjadi orchestrator migrasi workload Linux kelas enterprise yang **aman, idempotent, observable, dan reversible**.

Tiga kalimat yang harus benar sebelum apa pun yang lain:

1. **Sebuah tool migrasi yang bisa kehilangan data punya nilai kepercayaan negatif.** Keamanan data adalah fitur nomor nol, bukan nomor satu.
2. **Cutover yang "melaporkan sukses" tanpa benar-benar memindahkan trafik lebih berbahaya daripada cutover yang gagal dengan jujur.** Kejujuran status adalah invariant arsitektur, bukan detail implementasi.
3. **Setiap operasi harus bisa diulang tanpa efek samping.** Idempotency bukan optimisasi; ia adalah prasyarat recovery.

---

## 2. Visi Produk Jangka Panjang

### 2.1 Pernyataan visi

> **Meshium adalah control plane migrasi yang memindahkan seluruh workload Linux — stateless maupun stateful — dari satu host/lingkungan ke lingkungan lain dengan downtime terukur, jaminan RPO/RTO eksplisit, dan kemampuan rollback penuh, tanpa mengharuskan operator menjadi ahli di setiap datastore yang mereka jalankan.**

### 2.2 Bintang utara (North Star)

Satu metrik yang mengukur keberhasilan produk: **Successful Reversible Cutover Rate (SRCR)** —

```
SRCR = (migrasi yang mencapai cutover terverifikasi
        DAN masih bisa di-rollback pada titik itu)
       / (total migrasi yang mencapai fase cutover)
```

Kenapa metrik ini dan bukan "jumlah migrasi selesai": ia menghukum dua kegagalan yang paling merusak kepercayaan — cutover palsu (numerator turun karena "terverifikasi") dan titik-tak-bisa-balik yang tersembunyi (numerator turun karena "masih bisa rollback"). Sebuah tool bisa punya 100% completion rate dan tetap tidak layak dipercaya; SRCR tidak bisa dipalsukan dengan cara itu.

### 2.3 Tiga horizon

| Horizon | Rentang | Fokus | Definisi "selesai" |
|---|---|---|---|
| **H1 — Trust** | 0–12 bln | Tidak pernah kehilangan data; status selalu jujur; rollback selalu bekerja | Bisa memigrasi Compose+Postgres+Nginx nyata dan me-rollback-nya di titik mana pun sebelum PONR |
| **H2 — Breadth** | 12–30 bln | Driver SDK, cakupan datastore luas, planner cost-based, observability penuh | Komunitas bisa menulis driver pihak ketiga tanpa fork |
| **H3 — Platform** | 30 bln+ | Multi-target, fleet, kebijakan, AI advisory, marketplace plugin | Meshium jadi bahan pembicaraan default untuk "pindahin server ini" |

### 2.4 Persona pengguna utama

1. **Solo operator / small-shop sysadmin** — punya 1–20 VPS, tidak punya SRE. Ini persona *inti*; semua keputusan default dioptimalkan untuknya.
2. **Platform engineer di scale-up** — memindahkan puluhan layanan antar-cloud, butuh repeatability + audit. Persona *pertumbuhan*.
3. **MSP / konsultan migrasi** — melakukan migrasi untuk klien, butuh multi-tenant + reporting. Persona *monetisasi*.

Non-persona (jangan dioptimalkan untuk mereka di H1–H2): tim yang butuh migrasi database petabyte dengan tim DBA sendiri; mereka sudah punya tooling khusus dan Meshium tidak akan menang di sana.

---

## 3. Core Philosophy

Tujuh prinsip. Ini bukan slogan — masing-masing punya konsekuensi arsitektural yang mengikat, dan setiap PR harus bisa diuji terhadapnya.

### P1 — Safety over completeness

Lebih baik menolak melakukan migrasi yang tidak bisa dijamin aman daripada melakukannya dan berharap. Konsekuensi: setiap driver **wajib** mengimplementasikan `DryRun` dan `Verify`; planner **wajib** menolak (bukan menebak) ketika input tidak cukup untuk mengestimasi risiko.

### P2 — Everything is reversible until the Point of No Return (PONR)

Ada tepat satu batas eksplisit dalam setiap migrasi: PONR. Sebelum PONR, semua operasi harus bisa dibatalkan tanpa kehilangan data. Setelah PONR, rollback berarti **forward-recovery** (kembalikan trafik ke source), *bukan* memutar ulang backup di atas data yang sudah live. Konsekuensi: rollback engine harus *directional* dan sadar-PONR. (Ini secara langsung memperbaiki cacat C1 pipeline pada audit — rollback destruktif pasca-cutover.)

### P3 — Idempotency by construction

Setiap langkah harus aman dijalankan ulang. Ini dicapai lewat *desain*, bukan disiplin: setiap operasi punya kunci idempoten yang persist, dan efek samping dijaga oleh marker "sudah dilakukan". Konsekuensi: tidak ada langkah yang boleh "ambil backup lalu apply" tanpa marker; retry tidak boleh meracuni checkpoint.

### P4 — Honest state, single source of truth

Satu representasi status kanonik (typed state machine), dan semua turunan (string status untuk UI) diturunkan darinya dalam transaksi yang sama. Tidak boleh ada dua field yang bisa saling bertentangan. Konsekuensi: memperbaiki cacat H2 pipeline; semua tulisan status bersifat transaksional.

### P5 — Observability is not optional

Sebuah operasi yang tidak bisa diamati tidak bisa dipercaya. Setiap langkah memancarkan log terstruktur, metrik, span trace, dan entri audit — sebagai bagian dari kontrak langkah, bukan tambahan. Konsekuensi: signature langkah membawa `context.Context` yang men-carry trace; tidak ada `fmt.Printf` di jalur eksekusi.

### P6 — Exit codes are truth

Kontrak eksekusi remote tidak pernah menyembunyikan kegagalan. Sebuah perintah yang berjalan tapi keluar non-zero **adalah** kegagalan. Konsekuensi: tipe hasil eksekusi memaksa pemanggil menangani exit code (lihat Part 3 §`Result`); pola `if err != nil`-saja dibuat *tidak mungkin* dikompilasi untuk jalur yang salah. (Memperbaiki akar masalah sistemik H1/H4.)

### P7 — Drivers are the product, the core is a referee

Kecerdasan spesifik-datastore hidup di driver; core hanya mengorkestrasi kontrak. Core tidak boleh tahu apa itu "Postgres". Konsekuensi: core tidak pernah `import` paket driver konkret; ekstensi terjadi lewat registri + interface, bukan `switch` di core.

### Ringkasan konsekuensi yang dapat diuji

| Prinsip | Aturan lint/review yang mengikat |
|---|---|
| P1 | Driver tanpa `DryRun`/`Verify` gagal compile (interface wajib) |
| P2 | `rollback` menolak kategori destruktif bila `completedStage >= PONR` |
| P3 | Setiap `Apply` menerima `IdempotencyKey`; backup dijaga marker |
| P4 | Tidak ada `UpdateStatus` di luar transaksi yang juga menulis `state` |
| P5 | Grep CI: `fmt.Print` di `internal/engine/**` → gagal |
| P6 | Tipe `Result` tanpa `.OK()` check → `go vet` custom analyzer gagal |
| P7 | `core` meng-`import` `drivers/*` → gagal (arch-test) |

---

## 4. Product Boundary

### 4.1 Yang **menjadi** tanggung jawab Meshium

1. **Discovery** — menemukan workload, dependensi, dan konfigurasi pada host source.
2. **Planning** — memilih strategi migrasi per-workload berdasarkan pengukuran nyata dan constraint (downtime/RPO/RTO).
3. **Orchestration** — menjalankan rencana sebagai DAG langkah yang idempotent, dengan checkpoint.
4. **Cutover** — memindahkan trafik/promosi secara terkendali dengan verifikasi.
5. **Rollback & Recovery** — mengembalikan ke keadaan aman, sadar-PONR, tahan-crash.
6. **Verification** — membuktikan target setara source (data + perilaku).
7. **Observability & Audit** — jejak lengkap yang bisa diaudit.

### 4.2 Yang **bukan** tanggung jawab Meshium

Ini sama pentingnya — batas yang jelas mencegah produk membengkak menjadi "semuanya, buruk di segalanya".

| Bukan tanggung jawab | Alasan | Delegasikan ke |
|---|---|---|
| **Provisioning infrastruktur** (bikin VM, jaringan, DNS zone) | Domain deklaratif yang sudah matang | Terraform/OpenTofu |
| **Configuration management berkelanjutan** | Meshium adalah operasi *sekali-pindah*, bukan *desired-state loop* | Ansible/Salt |
| **Backup terjadwal jangka panjang** | Meshium bikin backup *taktis* untuk rollback, bukan retensi | Restic/Borg/Velero |
| **Container runtime / scheduler** | Meshium memindahkan workload, tidak menjalankannya jangka panjang | Docker/K8s |
| **Monitoring produksi berkelanjutan** | Meshium observability adalah *tentang migrasi*, berakhir saat migrasi selesai | Prometheus/Grafana |
| **Block-level disk cloning** | Level abstraksi salah untuk workload hidup | Clonezilla/dd |
| **Menjadi PaaS / app platform** | Bukan control loop; tidak menjalankan app | Fly/Render/K8s |

### 4.3 Garis batas yang eksplisit

> Meshium **berhenti** ketika target terverifikasi setara source dan trafik sudah pindah (atau di-rollback). Meshium **tidak** menjadi tempat tinggal workload. Ia adalah *jembatan*, bukan *rumah*.

Aturan praktis untuk keputusan fitur di masa depan: *"Apakah fitur ini membantu memindahkan workload dengan aman, atau membantu menjalankannya setelah pindah?"* Jika yang kedua — tolak, delegasikan.

---

## 5. Positioning Terhadap Tool Lain

Meshium bukan pesaing langsung mayoritas tool di bawah — ia *mengorkestrasi sebagian dari mereka* dan mengisi celah yang tidak satu pun tempati.

### 5.1 Tabel perbandingan

| Tool | Kategori | Level abstraksi | Stateful-aware? | Cutover trafik? | Rollback? | Overlap dengan Meshium |
|---|---|---|---|---|---|---|
| **rsync** | File transfer | File | Tidak | Tidak | Manual | Meshium *memakai* rsync sebagai driver filesystem |
| **SCP/SFTP** | File transfer | File | Tidak | Tidak | Tidak | Driver alternatif filesystem |
| **Ansible** | Config mgmt | Task/playbook | Sebagian (manual) | Tidak | Tidak (tidak transaksional) | Tumpang tindih pada "jalankan langkah via SSH"; beda pada state/rollback/planning |
| **Terraform/OpenTofu** | Provisioning | Deklaratif/resource | N/A | Tidak | Via state (destroy) | Komplementer — TF bikin target, Meshium isi & pindahkan |
| **Restic/Borg** | Backup | Snapshot/dedup | Ya (as-data) | Tidak | Restore penuh | Meshium bisa *memakai* mereka sebagai driver backup rollback |
| **Velero** | K8s DR | Cluster object+PV | Ya (K8s only) | Tidak | Restore | Overlap hanya di ranah K8s; Meshium lebih luas (bare-metal/VPS/Compose) |
| **Docker Compose** | Runtime | Service | Volume-level | Tidak | Tidak | Meshium *membaca* Compose untuk discovery, memindahkannya |
| **Kubernetes** | Orchestrator | Cluster | Ya (via CSI) | Ya (Service) | Deklaratif | Beda domain — K8s menjalankan; Meshium memindahkan *ke/dari* K8s |
| **Clonezilla** | Disk clone | Block | Tidak (offline) | Tidak | Image restore | Beda level — offline block vs live workload |
| **Migrate2Rocky** | OS conversion | In-place OS | N/A | Tidak | Tidak | Tidak overlap |
| **Carbon Copy Cloner** | Disk clone (Mac) | File/block | Tidak | Tidak | Image | Tidak overlap |
| **Meshium** | **Migration orchestrator** | **Workload** | **Ya** | **Ya** | **Ya, sadar-PONR** | — |

### 5.2 Peta posisi (dua sumbu)

```
                 Sadar-state / transaksional (rollback nyata)
                              ▲
                              │
                  Velero ●    │    ● MESHIUM
              (K8s only)      │      (workload-level, lintas-lingkungan)
                              │
   Restic/Borg ●              │
   (data saja)                │
──────────────────────────────┼──────────────────────────────▶
 Level rendah                 │                    Level tinggi
 (file/block)                 │                    (workload/service)
                              │
        rsync ●   ● Clonezilla│   ● Ansible
        SCP       (block)     │   (task, tak-transaksional)
                              │
                              │   ● Terraform/OpenTofu
                              │     (provisioning, desired-state)
                              ▼
                 Stateless / fire-and-forget
```

Kuadran kanan-atas — **workload-level DAN transaksional/reversible** — hampir kosong kecuali Velero (yang terkunci di K8s). Itulah wilayah Meshium.

### 5.3 Kalimat positioning kanonik

> **"Terraform membuat server-nya. Ansible mengonfigurasinya. Meshium memindahkan apa yang sudah berjalan ke sana — dengan aman, terverifikasi, dan bisa dibatalkan."**

### 5.4 Kapan pengguna **tidak** boleh memakai Meshium (kejujuran positioning)

- Hanya butuh menyalin file sekali → pakai `rsync`, jangan pasang Meshium.
- Butuh desired-state loop berkelanjutan → pakai Ansible/K8s.
- Migrasi DB tunggal petabyte dengan tim DBA → pakai tooling native DB.
- Sudah 100% di K8s dan pindah antar-cluster → Velero lebih pas.

Mendokumentasikan ini di README bukan kelemahan — ia membangun kepercayaan yang justru memenangkan persona inti.

---

## 6. Keputusan yang Dikunci oleh Part Ini

Ini menjadi input wajib untuk part-part selanjutnya:

1. **North Star = SRCR** → Part 5 (pipeline/cutover) harus punya definisi "verified cutover" dan "PONR" yang tegas.
2. **P2 (PONR) + P4 (honest state)** → Part 3 mendesain state machine dengan PONR sebagai state eksplisit; Part 5 mendesain rollback directional.
3. **P6 (exit codes)** → Part 3 mendefinisikan tipe `Result` yang memaksa pengecekan exit code.
4. **P7 (drivers are the product)** → Part 2 melarang `core → drivers`; Part 4 mendesain Driver SDK.
5. **Product boundary §4.2** → Part 7 memakai ini untuk daftar "lima fitur yang tidak boleh ditambahkan".

---

*Lanjut ke Part 2 — Domain, Bounded Context, dan Package Layout ideal.*
