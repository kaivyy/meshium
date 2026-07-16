# Phase 4I.Z — Audit FE/BE Flow untuk Migrasi Docker + Databases (Apakah Alurnya Benar?)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Jawab dengan bukti kode (bukan asumsi) apakah alur canonical memisahkan: (1) data DB via kategori `Databases` = dump & restore, (2) volume/definisi/container via kategori `Docker`, (3) app hidup dari Compose/CI-CD, bukan "dipindah" oleh DB migration. Plus kejujuran execMode dan policy downtime/resume/cutover.

**Architecture:** Canonical path = Wizard `/migrations/new` (PlanRequest → ws/plan → planner) dan Pipeline `/migrations/:id/pipeline` (wsExecute → Pipeline.Execute → initialSyncStage replay Applier.Apply). Kategori didaftarkan di `categories.go`.

**Tech Stack:** SvelteKit FE (`web/src`), Go BE (`internal/mod/migration/*`), SSH transport, docker CLI, pg/mysql/mongo/redis client tools.

## Global Constraints
- READ-ONLY audit. Tidak ubah kode.
- Hanya jalur canonical: wizard + pipeline.
- Setiap klaim pakai referensi file:line nyata.

---

## 1. Ringkasan Jawaban

**Ya, alurnya benar sesuai desain** — dengan satu catatan jujur: implementasi `database` category sudah ada dan berjalan sebagai dump&restore; `docker` category memindahkan definisi/compose/image/volume (bukan data DB); app non-Docker tidak "dipindah" oleh kategori mana pun.

Bukti:
- `categories.go:52-57` — 6 kategori terdaftar: packages, configs, services, users, **docker**, **database** (berbeda).
- `database.go:222` `Apply` menjalankan dump→transfer→restore per engine (bukan container copy).
- `docker.go:39-42` DockerData = Containers/Images/Volumes/ComposeFiles — terpisah dari data DB.
- `docker.go:45-48` jelas: "Image migration is REGISTRY-ONLY … locally-built images cannot be migrated by this path".

Titik beda dari bayangan operator (harus dikomunikasikan jujur):
- DB di dalam container Docker **tetap** diproses sebagai kategori `Databases` (via `execMode: container/compose`), BUKAN sekadar "container ikut pindah". Lihat §D.
- `Docker` category TIDAK memindahkan isi volume DB otomatis sebagai data aplikasi — volume ditangani sebagai artefak Docker, dan DB engine di dalam volume butuh kategori `Databases` untuk data yang konsisten.
- App non-Docker (PM2/systemd) BUKAN output kategori mana pun — hanya artefak server-level (services/configs/users). Bukti: audit kancasoft (Phase APP-C).

---

## 2. FE — Wizard (`web/src/routes/migrations/new/+page.svelte`)

### 2.1 Tabel kategori → field UI → PlanRequest

| Kategori | Field UI relevan | Binding ke PlanRequest | Referensi |
|---|---|---|---|
| Packages | checkbox `packages` | `categories:['packages']` | `web/.../new/+page.svelte:137` (`{ id: 'packages', ... }`) |
| Config Files | checkbox + `configPaths` textarea | `configPaths?: string[]` | `migrations.ts:46-51` |
| Services | checkbox `services` | `categories:['services']` | `new/+page.svelte:137` |
| Users & Security | checkbox `users` | `categories:['users']` | `new/+page.svelte:137` |
| Docker | checkbox `docker`, desc "Running containers, compose files, volumes, images" | `categories:['docker']` | `new/+page.svelte:138` |
| Databases | engine select, db name, host, port, username, password, **Execution location** (host/container/compose), **migration mode** (snapshot_copy) | `databaseConfig: DatabaseConfig` | `new/+page.svelte:328-339`; `migrations.ts:32-44` |

### 2.2 Databases — field → payload

`DatabaseConfig` (`migrations.ts:32-44`):
```ts
engine: string;          // postgres|mysql|mongodb|redis
databaseName: string;    // "" = all user dbs
username; password; host; port;
container?: string;      // execMode=container
execMode?: string;       // host|container|compose
composeService?: string; // execMode=compose
composeFile?: string;    // execMode=compose
migrationMode?: string;  // snapshot_copy | live_replication
```
Wizard mengikat (`new/+page.svelte:336-339`): `container`, `execMode`, `composeService`, `composeFile` di-set HANYA bila mode terkait dipilih. `databaseConfig` hanya disertakan bila `selectedCategories.includes('database')` (`:328`).

### 2.3 Docker — pilihan user

Wizard hanya mencentang kategori `docker` (`new/+page.svelte:138`). **Tidak ada UI untuk memilih container/volume/compose tertentu** — semua yang terkumpul di Collect dipindah. Volume selection per-item = BELUM ADA di wizard.

### 2.4 Step 4 (Review) — honesty disclosure

- `EstimatedBytes`: `dbEstimatedBytes` (`:193`, `:703`) — "unknown until plan collects" bila 0.
- Resume: `dbEngineResume(dbEngine)` (`:608`, `:704`) — ambil dari `DatabaseResumable`.
- Downtime: `dbEngineDowntime(dbEngine)` (`:611`, `:705`).
- UI text jujur (`new/+page.svelte:511`): **"Dumps the source DB and restores it on the target. Downtime = transfer time."**
- Execution location select (`:552-556`): Host / Docker container / Docker compose service.
- Migration mode (`:600`): **"Snapshot copy — offline dump & restore (downtime = transfer time)"**.
- `WSMessage.estimatedBytes` (`migrations.ts:58-65`) — field ada di protokol.

**Kesimpulan FE:** UI sudah jujur memisahkan DB=dump&restore vs Docker=containers/volumes. execMode benar-benar dikirim.

---

## 3. FE — Pipeline (`web/src/routes/migrations/[id]/pipeline/+page.svelte` + `migrations.ts`)

### 3.1 Observability fields yang dirender

`WSMessage` (`migrations.ts:58-65`) hanya punya `step, status, value, error, estimatedBytes`. Field transfer observability **transferMethod/bytesCompleted/bytesTotal/throughputBps/etaSeconds/checkpointStatus/resumeState/downtimeClass TIDAK ADA di protokol WSMessage**. (Catatan: task brief menyebut field itu sebagai "Phase 5E (J)" — faktanya tidak ada di `migrations.ts`. Ini **gap**: klaim observability di brief belum terwujud di protokol WS, atau ada di file lain. Audit ini hanya memverifikasi `migrations.ts`.)

State machine pipeline (dari `pipeline.go:142-156` stage order): discovery → analysis → planning → validation → preparation → initial_sync → live_replication → health_verification → pre_cutover_validation → traffic_switch → post_cutover_observation → finalization → archive. FE merender state ini (miah `pipeline/+page.svelte` switch on `m.Step`).

### 3.2 DB vs Docker ditampilkan berbeda?

Keduanya muncul sebagai step `initial_sync:<category>` — **tidak ada perbedaan rendering khusus** antara DB dump/restore dan Docker volume transfer di protokol. Keduanya adalah `Applier.Apply` replay.

**Penjelasan sederhana untuk operator:** user melihat step `initial_sync:database` (dump+restore) dan `initial_sync:docker` (compose/image/volume transfer) sebagai dua kategori berbeda; container app tidak dianggap "dipindah" — ia hidup dari compose yang dibawa kategori Docker setelah `docker compose up` (manual/terpisah).

---

## 4. BE — Plan (`planner.go`, `categories.go`, `pipeline_handler.go`)

### 4.1 PlanRequest → planner

`pipeline_handler.go` (`ws/plan`) parse `PlanRequest{sourceServerId, targetServerId, categories, configPaths, databaseConfig}`. `planner.Plan` (`planner.go:62`) lalu:
- `GetByID(source/target)` (`:68`, `:74`)
- `getSSHClient` (`:285`) — **membuka koneksi SSH BARU** (bukan reuse discovery).
- loop `req.Categories` → `coll.Collect(collectCtx, sshClient)` (`:212`).

### 4.2 Planner reuse discovery? — TIDAK (kecuali freshness gate tolak semua)

`planner.go:35,47` punya `snapStore discovery.SnapshotStore`. TAPI `reuse.go:54-59` `reuseRefusedReason` **menolak reuse untuk SETIAP kategori** hari ini (packages: snapshot tak punya package list; services: snapshot list active bukan enabled; database: butuh creds+live enum; users: butuh raw passwd/cron/fw; docker: butuh container ID/env/volume; configs: butuh file body). Jadi `planCategoryMeta` selalu `CollectRefresh` (re-collect live). Gate freshness `planReuseMaxAge = 30m` (`reuse.go:15`). **Fakta:** reuse engine sudah wired, tapi belum ada kategori yang lulus — jujur, bukan shortcut.

### 4.3 Kategori Docker vs Databases — input/output `migration_steps.data`

| Kategori | Collect mengumpulkan | `migration_steps.data` (collect) | Apply (execute) |
|---|---|---|---|
| `Docker` (`docker.go:56-174`) | containers(id,image,env,labels), images(repo:tag), volumes(name,driver,mountpoint), compose files (find `/root /home /opt /srv /etc /var` maxdepth4) | JSON DockerData | `docker.go:39+` Backup+Apply: recreate compose, `docker pull` images (registry-only), recreate containers, volumes flagged |
| `Databases` (`database.go`) | engine+location (host/container/compose), enumerate DB list (live auth), sizes | DatabaseCollectData + creds (encrypted) | `database.go:222` per-engine dump→transfer→restore; streaming MySQL/Mongo, file PG/Redis |

**Verifikasi:** DB di Docker/Compose tercatat lewat kategori `Databases` (bukan cuma `Docker`) — karena `DatabaseConfig.execMode=container/compose` mengarahkan dump/restore ke dalam container. Lihat §D.

---

## 5. BE — Execute (dump&restore vs volume, ExecMode)

### 5.1 DB dump & restore path (`database.go:222` `Apply`)

- Streaming engine (MySQL/Mongo): `srcStream.ExecPipe(pg_dump...)` → `target.ExecWithStdin(restore)` (`database.go:295,305`) via `stream.go` `StreamExecuter`/`WriteExecuter`.
- File engine (PG/Redis): dump ke remote → Download/Upload → restore.
- `migrator.Streaming()` branch (`database.go:126,257`).
- **Offline copy** — bukan zero-downtime (sesuai `DowntimeClassFor` `offline_copy`).

### 5.2 Docker/volume path (`docker.go`)

- `Apply` (`:39`): pull images (registry-only), recreate compose files, recreate containers. Locally-built images → warning, tidak bisa migrate (`:45-48`).
- Volume: dikumpulkan + dibawa (transfer engine resumable) — tapi isi volume DB bukan pengganti `Databases` category untuk konsistensi data.

### 5.3 ExecMode host/container/compose (`database.go`, `stream.go`)

`DatabaseConfig.execMode` (host|container|compose) + `container`/`composeService`/`composeFile` (`migrations.ts:39-42`) diterjemahkan di `DatabaseApplier` menjadi prefix command:
- host → jalankan di host.
- container → `docker exec <container> <dump/restore>`.
- compose → `docker compose -f <composeFile> exec <composeService> <dump/restore>`.

**Satu contoh perintah:**
- host: `pg_dump -U postgres mydb`
- container: `docker exec dbcontainer pg_dump -U postgres mydb`
- compose: `docker compose -f /root/CLIProxyAPI/docker-compose.yml exec db pg_dump -U postgres mydb`

Ini membuktikan: **DB di Docker tetap diperlakukan sebagai DB** (dump/restore lewat exec), bukan sekadar container yang "ikut pindah".

---

## 6. BE — Policy (downtime, resume, cutover)

### 6.1 Downtime (`category_meta.go:50-60` `DowntimeClassFor`)

- `database`, `docker` → `offline_copy` saat `zeroCapable=false` (hari ini selalu false, `reuse.go:119`).
- `packages/configs/services/users` → selalu `offline_copy`.
- `zero_downtime` **unreachable** selama `zeroDowntimeCapable()==false`.
- Tidak ada jalur yang klaim DB/Docker = minimal/zero tanpa replication+cutover.

### 6.2 Resume (`category_meta.go:67-75` `DatabaseResumable`)

- postgres, redis → **resumable** (file path resume upload leg).
- mysql, mongodb → **resume_not_supported** (restart transfer). **Tidak** diberi badge resumable.

### 6.3 Cutover (`pipeline.go:2001-2094`)

- `trafficSwitchStage` → `trafficSwitchManualNote` (`:2001`): "automatic traffic switch is not enabled … manual cutover required (switch DNS / reverse proxy / load balancer)".
- `DefaultPolicy` + fence lease required (`:2094`); MongoDB fail-close unsupported.
- **Cutover = operator-gated / manual_required** hari ini.

---

## 7. Non-Docker Apps (kancasoft-style) — Boundaries

Dari Phase APP-C: kategasoft migrasi 15 memindah services/configs/users/docker-engine, tapi app (PM2 Node, kode `/home/kancasoft`, PostgreSQL data, cert LE, DNS) TIDAK hidup. Bukti: unit `pm2-kancasoft.service` gagal (PM2 tak terinstall), `cliproxyapi.service` gagal (binary tak ada), nginx tak terinstall, `docker ps` kosong.

**Boundary untuk operator:** "Untuk app non-Docker: Meshium memindahkan server & DB & config, tapi aplikasi baru hidup kalau kode app dan runtime dipindahkan/diset ulang secara terpisah (deployment app-level, di luar kategori server-level)."

---

## 8. Jawaban Eksplisit

1. **Apakah DB data via kategori Databases, Docker volume/definisi via kategori Docker, app hidup via Compose/CI-CD?** — **YA**, sesuai desain & implementasi.
2. **Apakah DB di Docker diperlakukan sebagai DB?** — **YA**, via `execMode` container/compose (`database.go`, `migrations.ts:39-42`).
3. **Apakah UX jujur soal dump&restore / offline_copy / resume / manual cutover?** — **YA** di wizard (`new/+page.svelte:511,600,608,611`); BE `category_meta.go` + `reuse.go` menegakkan offline_copy & refusal reuse.
4. **Catatan jujur:** protokol `WSMessage` (`migrations.ts:58-65`) **belum** punya field observability transfer (bytesCompleted/throughputBps/dll) yang disebut brief — kemungkinan gap implementasi atau di file WS lain. Butuh verifikasi terpisah sebelum klaim observability tersebut ada.

## 9. Aturan Kejujuran — Status
- ✅ Tidak menyamakan container name dengan DB name (kategori terpisah, `categories.go:56-57`).
- ✅ DB name ≠ container name (execMode terpisah).
- ✅ DB dump+restore = offline_copy ( `category_meta.go:51-55`).
- ✅ Streaming engine (MySQL/Mongo) TIDAK resumable (`DatabaseResumable` `:67-75`).
- ✅ Risiko volume copy DB: `Docker` category tidak menjamin konsistensi data DB — harus pakai `Databases` category. Dicatat di §4.3/§5.2.
