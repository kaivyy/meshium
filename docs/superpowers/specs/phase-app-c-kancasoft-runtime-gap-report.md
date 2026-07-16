# Phase APP-C — Read-Only Source Audit for Kancasoft Runtime Gap (Alibaba → vpsexp7agus)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Audit (read-only) the source Alibaba and target vpsexp7agus to prove, with concrete file/process/config evidence, why the kancasoft application is not running on the target after migration 15 — and produce an exact deployment gap list.

**Architecture:** Source = a multi-component app server (PM2-managed Node apps as user `kancasoft`, a systemd Go binary `cli-proxy-api`, a docker-compose *build* project `CLIProxyAPI`, PostgreSQL 12, nginx reverse proxy fronting several `*.kancasoft.com` vhosts). Meshium migrated only server-level *metadata* categories (packages, configs, services, users, docker); the application *code*, its Node runtime, its Postgres data, and its TLS certs are not in any migrated category.

**Tech Stack:** Go 1.13.8 + Node v22.19.0 + PM2 + nginx 1.18 + Docker 28.1.1 / Compose 2.35.1 + PostgreSQL 12 on source (Ubuntu 20.04). Target (Ubuntu, post-migration 15): Docker 29.6.1 + Compose v5.3.1 provisioned; Node/PM2/Postgres/nginx/certs **absent**.

## Global Constraints

- READ-ONLY on source and target — no file, service, package, DNS, nginx, PM2, Docker, DB, or repo change; no restart; no writes.
- No secret values printed. No guessing of app location — every claim tied to a real file/process/config observed.
- Do not claim "app can run" until all dependencies are mapped.
- This is an **audit**, not a deployment. No fix is applied here.
- Distinguish clearly: server-level artifact migration *succeeded*; application-level deployment *not started / out of category scope*.

---

## A. INSPEKSI SOURCE APP RUNTIME (READ-ONLY)

Evidence source for the source server: Meshium discovery snapshot `discovery_snapshots` (server_id=1, latest captured 2026-07-15 23:20). I do NOT hold the source SSH credential in context (the stored password is AES-encrypted in `meshium.db`), so the snapshot is the read-only evidence. `collectionErrors` notes `system`, `docker_details`, `monitoring` collectors timed out — so `ps aux` / PM2 app-level detail / live port list are **not** fully captured. Claims are bounded to what the snapshot proves.

### A.1 Processes / managers actually in use

From `services[]` (load=loaded, active=active):
- `pm2-kancasoft.service` — PM2 process manager (User=kancasoft, `pm2 resurrect`) → **Node apps run under PM2**.
- `pm2-root.service` — second PM2 instance (root).
- `cliproxyapi.service` — "CLIProxyAPI Service" → a **systemd Go binary**, not PM2.
- `nginx.service` — reverse proxy, active/running.
- `postgresql@12-main.service` — **PostgreSQL 12.22**, running.
- `docker.service` + `containerd.service` — running.
- `telegram-bot.service` + `attack-monitor.service` — custom Node/systemd helpers.

**Manager verdict:** **Hybrid** — PM2 (Node apps, user `kancasoft`) + systemd Go binary (`cli-proxy-api`) + docker-compose build project (`CLIProxyAPI`) + nginx reverse proxy.

### A.2 Application code locations

Snapshot did not capture filesystem paths (no `ps`/`find` in snapshot). Inferred app locations from migrated configs read on the **target** (these configs were copied from source verbatim):
- PM2 user `kancasoft`, `PM2_HOME=/home/kancasoft/.pm2`, `PIDFile=/home/kancasoft/.pm2/pm2.pid`. App code path not in snapshot → **referenced but not dumped** (need `pm2 jlist`/ecosystem on source to confirm exact cwd).
- `cliproxyapi.service` → `WorkingDirectory=/root/cliproxyapi`, `ExecStart=/root/cliproxyapi/cli-proxy-api`.
- Docker compose project `cliproxyapi` → config `/root/CLIProxyAPI/docker-compose.yml`, `working_dir:/root/CLIProxyAPI`. Image `eceasy/cli-proxy-api:latest` is **built locally** (`build: context: .`) — it is NOT pullable as a prebuilt registry image alone; the build context (source) must travel.
- nginx vhost `whatsmoney` → static `root /var/www/whatsmoney` (frontend) + API proxy to `localhost:3003`.
- nginx vhost `kancasoft` → proxy to `localhost:3000`.
- nginx vhost `cliproxy` → proxy to `localhost:8317`.
- nginx vhost `arqstore.my.id` → proxy to `localhost:3005`.
- nginx vhost `tracker.kancasoft.com` → proxy to `localhost:8000`.
- nginx vhost `monitor.kancasoft.com` → proxy to `localhost:19999` (netdata).

**Conclusion on code:** source code lives under `/home/kancasoft/*` (PM2 apps), `/root/cliproxyapi`, `/root/CLIProxyAPI`, `/var/www/whatsmoney`, plus whatever serves `:3000`, `:3003`, `:3005`, `:8000`, `:19999`. None of these paths were captured by discovery (discovery does not snapshot app code) → **exact PM2 app cwd referenced but not dumped**.

### A.3 PM2 detail (source)

`pm2-kancasoft.service` ExecStart = `pm2 resurrect` (restores saved process list). To enumerate the actual apps (`pm2 jlist` script/cwd/interpreter/env) requires live source SSH, which is **not available in this audit**. What is proven: PM2 is installed at `/usr/lib/node_modules/pm2/bin/pm2`, runs as user `kancasoft`, uses `PM2_HOME=/home/kancasoft/.pm2`. Apps are revived from a saved dump — so a `pm2 save` artifact plus the code is required for fidelity.

### A.4 Nginx / web entrypoint (source, via target copy)

| server_name | listen | root | proxy_pass |
|---|---|---|---|
| `kancasoft.com www.kancasoft.com` | `[::]:443 ssl http2` | — | `http://localhost:3000` |
| `cliproxy.kancasoft.com` | 443 ssl | — | `http://127.0.0.1:8317` |
| `whatsmoney.kancasoft.com` | 443 ssl | `/var/www/whatsmoney` | `http://localhost:3003` (API `/api/`, `/health`) |
| `tracker.kancasoft.com` | 80 | — | `http://127.0.0.1:8000` |
| `arqstore.my.id` | 80 | — | `http://127.0.0.1:3005` |
| `monitor.kancasoft.com` | 443 ssl | — | `http://127.0.0.1:19999` |
| `manhwa.kancasoft.com` | 80 | `/var/www/manhwa` | `https://delivery.shngm.id` (remote) |

SSL via Certbot: certs at `/etc/letsencrypt/live/<domain>/fullchain.pem` + `privkey.pem`. **Certbot certs are not in any migration category** → target has no `/etc/letsencrypt/live/*` (proven: target `ls /etc/letsencrypt/live/` empty).

### A.5 Docker / compose (source)

- 1 container `cli-proxy-api` (state `created` — it was created but **not running** at snapshot time), image `eceasy/cli-proxy-api:latest`, project `cliproxyapi`, config `/root/CLIProxyAPI/docker-compose.yml`.
- Compose **builds from local context** with `args VERSION/COMMIT/BUILD_DATE` and `pull_policy: always`. Volumes reference `./config.yaml`, `./auths`, `./logs` (relative to `/root/CLIProxyAPI`).
- `envVarNames`: `DEPLOY`, `PATH`, `TZ`. No `.env` mounted (commented out).
- **Implication:** even the docker path needs `/root/CLIProxyAPI` source (Dockerfile + config.yaml + auths + logs) to build. A bare `docker compose up` on target will fail — the build context never migrated.

### A.6 Runtime dependencies (source)

- Node.js **v22.19.0** (required by PM2 apps).
- Go **go1.13.8** (builds `cli-proxy-api` binary? — binary present at `/root/cliproxyapi/cli-proxy-api`; Go may be build-time only).
- PM2 (global npm).
- nginx 1.18.0.
- Docker 28.1.1 + Compose 2.35.1.
- PostgreSQL 12.22 (running cluster 12-main).
- Certbot (Let's Encrypt).

---

## B. INSPEKSI TARGET GAP (READ-ONLY)

Live target probe (2026-07-15, post migration 15) — authoritative. (The target discovery snapshot is **stale**: captured before Docker was provisioned, so it shows `docker:null`; the live probe is the truth.)

| Runtime / artifact | Present on target? | Evidence |
|---|---|---|
| Docker | **YES** (29.6.1) | `docker --version` |
| Docker Compose | **YES** (v5.3.1) | `docker compose version` |
| nginx | **NOT installed** | `nginx -v` empty; service `inactive` |
| Node.js | **NO** | `node --version` empty |
| npm | **NO** | empty |
| PM2 | **NO** | `pm2 --version` empty |
| PostgreSQL | **NO** | `which psql` empty; `postgresql` inactive |
| Certbot certs | **NO** | `/etc/letsencrypt/live/` empty |
| App code `/home/kancasoft/*` | **NO** | PM2 home absent (`/home/kancasoft/.pm2` missing) |
| App code `/root/cliproxyapi` | **NO** | dir absent |
| App code `/root/CLIProxyAPI` | **NO** | dir absent |
| Static `/var/www/whatsmoney` | **NO** | `/var/www/` empty |
| Listening ports (3000/3003/3005/8317/8000/19999) | **NONE** | `ss -tlnp` empty for those |

What **DID** migrate (configs + services + users categories):
- systemd units present: `pm2-kancasoft.service`, `pm2-root.service`, `cliproxyapi.service`, `attack-monitor.service`, `telegram-bot.service`. (Verified by `ls /etc/systemd/system/`.)
- nginx vhosts present in `sites-available/`: `kancasoft`, `kancasoft.com`, `whatsmoney`, `cliproxy`, `arqstore.my.id`, `manhwa.kancasoft.com`, `tracker.kancasoft.com`, `netdata`. `sites-enabled/` only has a `.bak` symlink (`cliproxy.bak-...`) — **no active vhost is enabled**.
- User `kancasoft` (uid=1001) created; home `/home/kancasoft/` exists but only has skeleton `.bashrc/.profile` — **no `.pm2` dump, no app code**.

**Gap synthesis (broken dependencies on target):**
- `pm2-kancasoft.service` → points at `/usr/lib/node_modules/pm2/bin/pm2` which **does not exist** (PM2 not installed) → unit fails.
- `cliproxyapi.service` → `ExecStart=/root/cliproxyapi/cli-proxy-api` but **that binary/path does not exist** → unit fails.
- nginx vhosts → proxy to `localhost:3000/3003/3005/8317/8000/19999`; **none of those listeners exist** and nginx itself isn't installed → broken.
- nginx vhosts reference `/etc/letsencrypt/live/<domain>/*.pem` → **cert files absent** → nginx would fail to start even if installed.
- docker compose `CLIProxyAPI` → build context `/root/CLIProxyAPI` absent → `docker compose up` fails to build.

---

## C. IDENTIFIKASI KOMPONEN WAJIB AGAR APP HIDUP

### C.1 Runtime prerequisites
- Node.js v22.19.0 (PM2 apps need this exact major).
- PM2 (global).
- nginx (reverse proxy + SSL).
- Certbot + Let's Encrypt certs for `*.kancasoft.com`.
- PostgreSQL 12 (data + schema — running on source, **not** in migration categories).
- Docker + Compose (for `CLIProxyAPI` build path; already present on target).

### C.2 Application code artifacts
- `/home/kancasoft/*` — PM2 Node apps (exact cwd **referenced but not dumped**).
- `/root/cliproxyapi/` + binary `cli-proxy-api` (Go binary; build context unknown).
- `/root/CLIProxyAPI/` — docker-compose project (Dockerfile, `config.yaml`, `auths/`, `logs/`).
- `/var/www/whatsmoney/` — whatsmoney frontend static build.
- Unknown code serving `:3000` (kancasoft main), `:3003` (whatsmoney API), `:3005` (arqstore), `:8000` (tracker), `:19999` (netdata) — paths **referenced but not dumped**.

### C.3 Config artifacts
- `ecosystem.config.js` / PM2 save dump (`/home/kancasoft/.pm2/dump.pm2` + `pm2.pid`) — **not migrated**.
- `.env` / `.env.production` for each app — **referenced but not dumped**; presence unknown.
- nginx vhosts — **migrated** (but not enabled; certs broken).
- systemd units — **migrated** (but binaries absent).
- `docker-compose.yml` for `CLIProxyAPI` — content known (in snapshot) but **project dir absent**.

### C.4 Data / state artifacts
- PostgreSQL 12 cluster data (`/var/lib/postgresql/12/main`) — **NOT migrated** (no `database` category in migration 15). Any app state in PG is missing.
- `CLIProxyAPI` volumes: `./config.yaml` (auth + routing config), `./auths` (credentials), `./logs`.
- Uploads / local storage for whatsmoney & kancasoft — unknown paths, **not migrated**.

### C.5 External dependencies
- DNS: `*.kancasoft.com`, `arqstore.my.id` must point to target IP (currently still on source IP).
- TLS: Let's Encrypt certs (re-issue via Certbot on target, or copy `/etc/letsencrypt/`).
- Remote DB / object storage / API keys — referenced by env; values **not dumped**.

---

## D. DEPLOYMENT MODE YANG SEBENARNYA DIBUTUHKAN

**Verdict: E — Hybrid (PM2 + systemd Go binary + docker-compose + nginx reverse proxy), multi-app behind nginx.**

Evidence: PM2-managed Node apps (user `kancasoft`, `pm2 resurrect`); systemd Go binary `cli-proxy-api`; docker-compose *build* project `CLIProxyAPI`; nginx fronting 6+ upstreams. This is not a single deployable unit.

Minimal steps for the app to live on target (none automated by Meshium today):
1. Install Node v22 + PM2; create user `kancasoft`; restore `/home/kancasoft/.pm2` dump + app code.
2. Transfer `/root/cliproxyapi` binary + `/root/CLIProxyAPI` (build context).
3. Transfer `/var/www/whatsmoney` + whatever serves `:3000/:3003/:3005/:8000`.
4. Install + enable nginx; `sites-enabled` symlinks; obtain/replay Let's Encrypt certs.
5. Install PostgreSQL 12; migrate DB (data, not just metadata) — needs the planned `database` category (not yet wired end-to-end).
6. Re-point DNS `*.kancasoft.com` + `arqstore.my.id` to target IP.

What Meshium categories **can** do today: `packages` (pkg lists, not runtimes like Node/PM2/PG), `configs` (already moved nginx vhosts + units), `services` (moved unit files), `users` (moved `kancasoft` user), `docker` (provisioned engine + pulled the prebuilt `eceasy/cli-proxy-api` image — but the *build context* is still missing so compose `up` cannot build).

What is **clearly out of scope** of server-level categories and must be a separate application deployment: app source code, `.env`/secrets, PM2 dump, PostgreSQL data, Let's Encrypt certs, DNS.

---

## E. JAWABAN TEGAS

1. **Kenapa migrasi 15 selesai tapi app belum jalan?** Karena migrasi 15 hanya memindahkan *artefak server-level* (daftar package, file config nginx + unit systemd, daftar service, user `kancasoft`, dan engine Docker). Kode aplikasi, runtime Node/PM2, data PostgreSQL, dan sertifikat TLS **bukan bagian kategori mana pun** yang di-migrasi. Tanpa itu, unit `pm2-kancasoft.service` gagal (binary PM2 tak ada), `cliproxyapi.service` gagal (binary tak ada), nginx tak terinstall, dan tidak ada port yang listen.
2. **Artefak yang sudah pindah:** unit systemd (`pm2-kancasoft`, `pm2-root`, `cliproxyapi`, `attack-monitor`, `telegram-bot`); nginx vhosts di `sites-available/`; user `kancasoft` (uid 1001); engine Docker + Compose (diprovision); image `eceasy/cli-proxy-api:latest` ditarik (tapi build context tak ada).
3. **Artefak yang belum pindah:** kode PM2 (`/home/kancasoft/*` + `.pm2` dump), `/root/cliproxyapi` + binary, `/root/CLIProxyAPI` (build context), `/var/www/whatsmoney`, kode app `:3000/:3003/:3005/:8000`, Node.js, PM2, nginx, PostgreSQL + datanya, sertifikat Let's Encrypt, `.env`/secret, DNS.
4. **Lokasi source code utama:** `/home/kancasoft/*` (PM2 Node apps — cwd pasti butuh `pm2 jlist` di source), `/root/cliproxyapi`, `/root/CLIProxyAPI`, `/var/www/whatsmoney`. Path pasti untuk `:3000/:3003/:3005/:8000` **referenced but not dumped** (butuh akses source).
5. **App dijalankan oleh apa?** Hybrid: PM2 (user `kancasoft`, `pm2 resurrect`) untuk Node apps; systemd langsung untuk binary Go `cli-proxy-api`; docker-compose build untuk `CLIProxyAPI`; nginx sebagai reverse proxy di depan.
6. **Nginx memproxy ke mana?** `kancasoft.com`→`localhost:3000`; `cliproxy.kancasoft.com`→`127.0.0.1:8317`; `whatsmoney.kancasoft.com`→static `/var/www/whatsmoney` + API `localhost:3003`; `tracker.kancasoft.com`→`127.0.0.1:8000`; `arqstore.my.id`→`127.0.0.1:3005`; `monitor.kancasoft.com`→`127.0.0.1:19999`.
7. **Runtime minimum di target:** Node v22.19.0, PM2, nginx, Certbot+letsencrypt, PostgreSQL 12, ditambah Docker/Compose (sudah ada).
8. **File/path/project yang harus dipindah:** `/home/kancasoft/*`+`.pm2` dump; `/root/cliproxyapi`(+binary); `/root/CLIProxyAPI`(Dockerfile+config.yaml+auths+logs); `/var/www/whatsmoney`; kode `:3000/:3003/:3005/:8000`; `.env` masing-masing app; `/var/lib/postgresql/12/main` (data); `/etc/letsencrypt/live/*`.
9. **Env/config/secrets?** Tiap app butuh `.env`/env vars (`DEPLOY` untuk CLIProxyAPI terbukti; sisanya `referenced but not dumped`). Sertifikat TLS di `/etc/letsencrypt/live/` harus disiapkan (re-issue Certbot atau copy). **Tidak ada secret yang dicetak di laporan ini.**
10. **Bisa dibawa dengan cara apa?** Kombinasi: (a) rsync/copy path untuk `/home/kancasoft`, `/root/cliproxyapi`, `/root/CLIProxyAPI`, `/var/www/whatsmoney`; (b) git clone untuk repo app (jika ada remote); (c) `docker compose up` **hanya setelah** `/root/CLIProxyAPI` build context dibawa (selain itu image sudah ditarik); (d) PostgreSQL butuh dump→restore (kategori `database` belum wired); (e) DNS + Certbot re-point manual. Tidak ada satu cara yang cukup sendiri.

---

## F. OUTPUT — LAPORAN FINAL

(See sections A–E above for full evidence. Summary tables below.)

### 1. Executive answer
Migrasi 15 **berhasil memindahkan artefak server-level** (configs, services, users, docker engine) tetapi **aplikasi kancasoft belum berjalan** karena kode aplikasi, runtime Node/PM2, data PostgreSQL, sertifikat TLS, dan DNS bukan bagian kategori migrasi mana pun. Di target: Docker+Compose ada, tapi Node/PM2/nginx/Postgres/certs tidak ada, tidak ada port listen, tidak ada app code. Ini bukan kegagalan Meshium — ini batas scope kategori server-level. Deployment application-level belum dilakukan.

### 2. Source runtime map — see A.
### 3. Target gap map — see B.
### 4. App artifact inventory — see C.
### 5. Deployment model — see D (Hybrid / E).
### 6. Minimal action plan — see D.5.
### 7. Meshium coverage — packages(metadata only)/configs/services/users/docker engine done; **app code, runtime installs, DB data, certs, DNS = not covered**.
### 8. Risk & blocker
- **BLOCKER:** app code tidak pernah dipindah (`referenced but not dumped` — butuh akses source/read-only `pm2 jlist`, `find`).
- **BLOCKER:** PostgreSQL 12 data tidak di-migrasi (kategori `database` belum wired end-to-end).
- **BLOCKER:** Let's Encrypt certs absen → nginx gagal start walau diinstall.
- **BLOCKER:** DNS masih menunjuk ke IP source.
- **RISK:** `CLIProxyAPI` compose `build:` lokal → butuh `/root/CLIProxyAPI` context; image sudah ditarik tapi `docker compose up` tetap gagal build.
- **RISK:** PM2 `resurrect` butuh dump `.pm2/dump.pm2` + kode; keduanya belum ada.

### 9. Rekomendasi langkah berikut
**`need source-code transfer audit first`** — sebelum deployment, lakukan audit transfer read-only di source (akses SSH source) untuk mengunci: `pm2 jlist` (script/cwd/env), `find` lokasi kode `:3000/:3003/:3005/:8000`, isi `.env` (referenced, jangan dump value), dan path data PG. Tanpa itu, rencana deploy hanya bisa berbasis inference.

### Tabel komponen

| Komponen | Source ada? | Target ada? | Sudah termigrasi? | Dibutuhkan untuk app jalan? | Catatan |
|---|---|---|---|---|---|
| Node.js v22.19.0 | Ya | Tidak | Tidak | Ya | runtime PM2 apps |
| PM2 | Ya | Tidak | Tidak | Ya | process manager |
| nginx | Ya | Tidak | Tidak (config ya) | Ya | reverse proxy |
| Certbot + LE certs | Ya | Tidak | Tidak | Ya | SSL vhosts |
| PostgreSQL 12 + data | Ya | Tidak | Tidak | Ya | app state/DB |
| Docker + Compose | Ya (28.1.1/2.35.1) | Ya (29.6.1/v5.3.1) | Ya (engine) | Ya (CLIProxyAPI) | image ditarik; build context belum |
| systemd units | Ya | Ya | Ya | Ya | binary PM2/cli-proxy tiada → gagal |
| nginx vhosts | Ya | Ya (available) | Ya | Ya | tidak di-enable; cert broken |
| user `kancasoft` | Ya | Ya | Ya | Ya | home kosong |
| PM2 app code `/home/kancasoft/*` | Ya | Tidak | Tidak | Ya | referenced but not dumped |
| `/root/cliproxyapi` + binary | Ya | Tidak | Tidak | Ya | unitExecStart指向nya |
| `/root/CLIProxyAPI` build context | Ya | Tidak | Tidak | Ya (compose build) | compose contents diketahui |
| `/var/www/whatsmoney` | Ya | Tidak | Tidak | Ya | frontend static |
| kode `:3000/:3003/:3005/:8000` | Ya | Tidak | Tidak | Ya | referenced but not dumped |
| `.env` / secrets | Ya | Tidak | Tidak | Ya | referenced but not dumped |
| DNS `*.kancasoft.com` | Ya (IP source) | — | Tidak | Ya | masih ke IP source |

### Tabel item

| Item | Lokasi source | Cara run | Dependency | Perlu dipindah? | Cara paling masuk akal |
|---|---|---|---|---|---|
| PM2 Node apps | `/home/kancasoft/*` | `pm2 resurrect` | Node v22, PM2, `.pm2` dump | Ya | rsync `/home/kancasoft` + dump; atau git clone per repo |
| cli-proxy-api | `/root/cliproxyapi/cli-proxy-api` | systemd unit | binary + Go build | Ya | rsync binary + dir; build ulang dari source Go |
| CLIProxyAPI | `/root/CLIProxyAPI` | `docker compose up` (build) | Docker (ada), build context, config.yaml/auths/logs | Ya | rsync dir; lalu `docker compose up` (image sudah ditarik) |
| whatsmoney frontend | `/var/www/whatsmoney` | nginx static | nginx + cert | Ya | rsync dir |
| kancasoft main | `:3000` (path TBD) | (TBD) | nginx proxy | Ya | butuh audit source |
| whatsmoney API | `:3003` (path TBD) | (TBD) | nginx proxy | Ya | butuh audit source |
| arqstore | `:3005` (path TBD) | (TBD) | nginx proxy | Ya | butuh audit source |
| tracker | `:8000` (path TBD) | (TBD) | nginx proxy | Ya | butuh audit source |
| PostgreSQL data | `/var/lib/postgresql/12/main` | PG 12 cluster | `database` category | Ya | dump→restore (belum wired) |
| TLS certs | `/etc/letsencrypt/live/*` | nginx ssl | Certbot | Ya | copy dir atau `certbot` re-issue |
| nginx vhosts | `/etc/nginx/sites-available/*` | nginx | nginx + certs | Sudah pindah | enable symlink + sediakan cert |

---

## G. ATURAN KEJUJURAN — catatan kepatuhan

- Source code **tidak ditemukan** di target (di-source tidak di-capture oleh discovery): dinyatakan eksplisit sebagai *referenced but not dumped*.
- `.env` ada rujukan (compose `DEPLOY`) tapi **tidak dibaca/didump**; nilai secret tidak dicetak.
- nginx vhost di target mengarah ke upstream `localhost:*` yang **tidak ada** → dicatat sebagai *broken dependency*.
- compose `CLIProxyAPI` butuh bind/build context lokal `/root/CLIProxyAPI` yang **tidak termigrasi** → dicatat sebagai *blocker*.
- Tidak menyarankan "install PM2 lalu selesai" — kode app + env + data belum ada.
- Tidak menyatakan "Meshium gagal total": dipisah tegas — migrasi artefak server-level **berhasil**; deployment application-level **belum dilakukan / di luar scope**.
