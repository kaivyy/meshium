# Phase 6A — Source vs Target Compare + Selective Apply in Pipeline (Audit)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Audit (read-only) fondasi Meshium untuk meningkatkan flow dari "create plan → execute" menjadi "discover → **compare** → pilih item → apply terpilih → **verify parity**". Semua klaim berbasis file/route/handler/state/data model nyata.

**Architecture:** Canonical path = Wizard `/migrations/new` + Pipeline `/migrations/:id/pipeline`. Existing compare primitives: `DiffService` (`diff.go`), `DryRun` (`dryrun.go`), `compatibility.go`, `reuse.go` freshness gate, `category_meta.go` honesty contract.

**Tech Stack:** SvelteKit + Go `internal/mod/migration/*`.

## Global Constraints (non-negotiable)
- Mulai dari AUDIT. Tidak implementasi di sini.
- Hormati canonical path wizard + pipeline. Jangan bikin flow baru kalau muat di jalur ada.
- Klaim harus berbasis bukti file:line.
- Bila tak terbukti tanpa live server, tulis **gap**.
- Jangan oversell "one-click parity".

---

## A. FONDASI (dari audit sebelumnya)

1. Planner TIDAK reuse discovery hari ini — `reuse.go:54-59` menolak SETIAP kategori (snapshot shape tidak faithful). Gate `planReuseMaxAge=30m` (`reuse.go:15`). Engine reuse sudah wired, belum ada kategori lulus.
2. Pipeline canonical: `pipeline.go:142-156` 14 stage; `initialSyncStage` (`pipeline.go:1560`) replay `Applier.Apply` per step; `StepStatusApplied` guard (`pipeline.go:1642`) cegah re-apply.
3. FE jujur sudah ada: `category_meta.go` (`DowntimeClassFor` offline_copy, `DatabaseResumable`), wizard text dump&restore/offline (`new/+page.svelte:511,600,608,611`).
4. Audit kancasoft (Phase APP-C): configs/services/users/docker metadata bisa pindah; app tetap mati bila runtime/code/DB/cert/DNS tak ikut.

---

## B. AUDIT FLOW SAAT INI — DIMANA COMPARE COCOK?

### B.1 Tabel route/file/handler

| Layer | Route / File | Handler | Peran |
|---|---|---|---|
| FE onboarding | `/servers/new`, `/servers/[id]` | — | add server, test conn |
| BE onboarding | `POST /api/servers`, `RunConnectionTest` | server handler | creds + test |
| BE discovery | `POST /api/servers/{id}/discover`, `GET /api/servers/{id}/snapshot`, `GET /api/compat?source=&target=` | discovery handler | snapshot + compat |
| FE migration | `/migrations/new` | wizard | source/target/category/config |
| BE plan | `ws/plan` | `pipeline_handler.go` → `planner.Plan` | collect per category → `migration_steps` |
| **FE compare** | `/migrations/[id]/diff/+page.svelte`, `/servers/compare/+page.svelte`, `/drift/+page.svelte` | — | **READ-ONLY compare sudah ada** |
| **BE compare** | `POST /api/diff`, `/ws/diff` | `handler.go:54-55` → `DiffService.Diff` (`diff.go:57`) | live source-vs-target per category |
| BE dryrun | `ws/dryrun/`, `GET /api/migrations/{id}/dryrun` | `handler.go:53,414` → `Executor.DryRun` (`dryrun.go:25`) | preview changes target-side |
| FE pipeline | `/migrations/:id/pipeline` | pipeline UI | execute + state machine |
| BE execute | `ws/execute` / `ws/migrate/{id}` | `Pipeline.Execute` (`pipeline.go:185`) / `executor.go` | replay Apply |

### B.2 Titik yang SUDAH "mirip compare"
- `DiffService.Diff` (`diff.go:57`) — live collect source+target, `computeDiff` → `OnlyInSource/OnlyInTarget/Different/Same` per kategori (`diff.go:22-28`).
- `DryRun` (`dryrun.go`) — bandingkan source-collected data vs target state, hasilkan `DryRunChange{add,modify,remove}`; **persist** ke `migration_steps action='dryrun'` (`dryrun.go` persist block) agar survive reload.
- `compatibility.go` — bandingkan kapasitas (disk, dll).

### B.3 Titik yang HANYA collect/execute
- `planner.Plan` → collect only (no target compare beyond dryrun).
- `initialSyncStage` → Apply only (no re-compare).

### B.4 Analisis placement
- **Wizard**: sudah punya summary capability (Step 4 estimatedBytes/downtime/resume). Cocok untuk **ringkasan compare + initial category choice** (bukan item-level, karena item belum di-collect hingga plan).
- **Pipeline**: punya stage machine + observability slot. Cocok untuk **item-level compare matrix + selection + apply + verify** (karena di sini `migration_steps` sudah ada per item).
- **Kesimpulan**: COMPARE penuh di pipeline; wizard cukup summary + re-use `DiffService` untuk preview. **Hybrid** adalah placement paling masuk akal (lihat E).

---

## C. AUDIT SUMBER DATA — SOURCE OF TRUTH UNTUK COMPARE

### C.1 Kandidat
1. `server_info` — hasil `RunConnectionTest` (host profile). **Tidak cukup** untuk runtime compare.
2. `discovery_snapshots` — Services, Packages(?), Docker, Databases(detection only), Users(summary), Cron, SSL, Monitoring. **Cukup sebagian**; tapi `reuse.go:75-111` buktikan shape tidak faithful untuk apply (packages kosong, services=active-bukan-enabled, db=detection-no-creds, docker=no-ID/env, users=summary, configs=no-body).
3. `migration_steps.data` — hasil collect per kategori (source). **Cukup untuk source side**, tapi tidak punya target side & tidak punya delta.
4. `MigrationSession`/migration row — `MigrationConfig` (`pipeline_models.go:425`) punya `Categories`, `DatabaseConfig`. **Tidak ada** field selection/compare/verification (`grep` konfirmasi: tidak ada `UserSelection/CompareResult/ParityItem`).
5. FE types — `MigrationStep`, `DatabaseConfig`, `PlanRequest` (`migrations.ts`). Tidak ada compare matrix type.

### C.2 Source of truth terbaik SAAT INI
**Gabungan, tapi tidak normalized:** source side dari `migration_steps.data` (collect), target side dari live `DiffService.Diff` (collect target). Delta dihitung dua kali (plan-time collect vs diff-time collect) — **redundan & bisa stale**.

### C.3 Butuh normalized model?
**YA** — untuk compare yang jujur & survive reload, butuh `NormalizedInventory` (source+target per item) + `ParityItem` (delta + status + applyLevel + deps). Tanpa itu, compare hanya bisa berupa list string (`onlyInSource`) seperti `DiffService` hari ini — **tidak cukup untuk selective apply** (tidak ada per-item value, action, dependency).

### C.4 Risiko patch UI di atas model lama
`DiffService` menghasilkan `OnlyInSource []string` — tidak ada nilai, tidak ada "apply_from_source" semantics, tidak ada dependency. Bila FE cuma nambah checkbox di atas `DiffResult`, user bisa centang item tanpa tahu dependency (mis. centang `pm2-kancasoft.service` tanpa Node/PM2/code). **Harus** model parity baru, bukan patch UI.

---

## D. FEASIBILITY COMPARE PER KATEGORI

Status: `discoverable` (bisa dikumpulkan?), `comparable` (bisa dibandingkan?), `auto-applicable` (bisa apply otomatis?), `guarded` (perlu konfirmasi?), `manual-only`, `out-of-scope`.

| Item | discover | compare | auto-apply | guarded | manual | out-of-scope | Catatan |
|---|---|---|---|---|---|---|---|
| Packages (list) | ✅ | ✅ | ✅ L1 | — | — | — | install via pkg mgr; fails clear if incompatible (`category_meta.go:114`) |
| Config files (body) | ✅ | ✅ (checksum) | ✅ L1/L2 | overwrite L3 | — | — | `configs.go` mkdir parent (fix Phase 5E) |
| Services (unit file) | ✅ | ✅ | ⚠️ L2 | enable/restart L3 | — | — | **false-equiv**: unit hadir ≠ runtime hadir (kancasoft) |
| Users & Security | ✅ | ✅ | ✅ L1 | destructive L3 | — | — | butuh raw passwd/cron/fw (`reuse.go:95`) |
| Docker: images (registry) | ✅ | ✅ | ✅ L2 | — | locally-built L4 | — | `docker pull` only (`docker.go:45`) |
| Docker: compose def | ✅ | ✅ | ✅ L2 | — | build-context L4 | — | compose file hadir ≠ build context hadir |
| Docker: volumes | ✅ | ✅ | ⚠️ L2 | overwrite L3 | — | — | resumable transfer |
| Docker: containers | ✅ | ✅ | ⚠️ L2 | — | — | — | recreate from def+image |
| **Runtime: Node** | ⚠️ (runtimes) | ⚠️ | ❌ | — | L4 | — | provision component? belum ada nodejs+PM2 otomatis |
| **Runtime: PM2** | ❌ | ❌ | ❌ | — | L4 | — | tidak di-collect; butuh install+code+.pm2 dump |
| **Runtime: nginx** | ✅ (vhost) | ✅ | ⚠️ | cert L3 | — | — | vhost hadir ≠ serve (cert/upstream) |
| **Runtime: PostgreSQL** | ✅ (detection) | ⚠️ | data L3 | — | — | — | engine discovered ≠ data synced |
| **DB data (dump/restore)** | ✅ | ✅ | ⚠️ L3 | guarded | — | — | `database.go`; offline_copy; MySQL/Mongo not resumable |
| **App code (PM2/systemd)** | ❌ | ❌ | ❌ | — | L4 | — | **referenced but not dumped** (kancasoft) |
| **TLS cert (LE)** | ❌ | ❌ | ❌ | — | L4 | — | `/etc/letsencrypt` tidak di-collect |
| **DNS/cutover** | ❌ (external) | ❌ | ❌ | — | L4 | — | manual_required (`pipeline.go:2001`) |
| **Verification: ports/health** | ⚠️ | ⚠️ | n/a | — | — | — | `healthVerificationStage` dangkal (`pipeline.go:1912`) |

**Kasus kancasoft (false-equivalence yang dihindari):**
- service file hadir ≠ runtime hadir ✅ terbukti (pm2-kancasoft failed).
- nginx config hadir ≠ site bisa serve ✅ (cert/upstream absen).
- docker compose project hadir ≠ build context hadir ✅ (CLIProxyAPI build context tak migrate).
- db engine discovered ≠ db data synced ✅ (PG 12 data tak di-migrasi).
- PM2 unit hadir ≠ app code hadir ✅ (PM2 tak terinstall).

---

## E. COMPARE UX — WIZARD vs PIPELINE vs HYBRID

| Opsi | Produk | Implementasi | State consistency |
|---|---|---|---|
| A — penuh di wizard | lemah (item belum di-collect) | sedang | lemah (reload butuh re-plan) |
| B — penuh di pipeline | kuat (step per item ada) | berat (stage baru) | kuat (persist di migration_steps) |
| **C — Hybrid** | **kuat** | **ringan** | **kuat** |

**Rekomendasi: C (Hybrid).**
- **Wizard** = ringkasan compare (reuse `DiffService` di Step 3/4) + initial category choice + honesty disclosure (sudah ada pattern `category_meta.go`).
- **Pipeline** = compare matrix tab (item-level), dependency drawer, selection, apply (reuse `initialSyncStage` + per-step Apply), verification/parity tab (reuse `healthVerificationStage` + `VerificationResult`).

Alasan: item-level data hanya ada SETELAH plan (migration_steps). Pipeline sudah punya persistence + stage machine + observability slot. Wizard cuma perlu summary agar user tahu sebelum execute.

---

## F. DESIGN TARGET UX — COMPARE MATRIX (sketsa, bukan implementasi)

Minimum (konsisten dengan `DiffService` + `category_meta` yang ada):
1. **Matrix** kolom: Category | Item | Source value | Target value | Status | Suggested action | Risk/Dep badge.
   - Status: `same | missing_on_target | different | stale | unknown | manual_required | unsupported`.
2. **Item action**: `apply_from_source | keep_target | skip | review_manual`.
3. **Bulk**: select-all-safe / select-all-missing / select-category / clear.
4. **Dependency drawer**: prerequisite, side-effects, restart risk, overwrite risk, downtime class, resumability, manual follow-up.
5. **Risk badges**: safe-auto / auto-with-warning / guarded-manual / manual-only (mapping ke `category_meta.go` + apply-level model §H).
6. **Verification result**: matched / failed / unresolved-drift / manual-next / parity-score.

Placement: wizard = summary + category choice; pipeline = matrix + deps + apply + verify (post-apply only).

---

## G. DEPENDENCY GRAPH — USER TIDAK BOLEH CENTANG TANPA KONSEKUENSI

### G.1 Apakah sudah ada?
**Tidak** sebagai graph eksplisit. Hanya terserak: `category_meta.go` (PlanBehavior/ExecuteBehavior teks), `reuse.go` (reuseRefusedReason menyebut dependency implicitly: "services need package+config prerequisites"), `docker.go` (image before container). Tidak ada struktur `DependencyRef`.

### G.2 Model proposal (`DependencyRef`)
```go
type DependencyRef struct {
  ItemKey     string // e.g. "runtime:node@22"
  Kind        string // hard | recommended | verify_post
  SatisfiedBy string // how target meets it (installed / present / manual)
  Note        string
}
```
Contoh wajib:
- `pm2-kancasoft.service` → hard: Node v22, PM2 installed, app code path, `.pm2/dump.pm2`, env/secrets, expected port/health.
- nginx vhost → hard: nginx installed; hard: cert TLS; recommended: upstream app healthy; post: DNS/cutover.
- docker-compose project → hard: compose file; hard: Dockerfile/build context; recommended: env/config; hard: volumes; verify_post: image/build.
- database → hard: engine on target; hard: db creds; hard: execMode; recommended: dump/restore path; post: downtime class; post: rollback behavior.

### G.3 Status dependency
- **hard**: blokir apply bila tidak terpenuhi.
- **recommended**: tampil di drawer, tidak blokir.
- **verify_post**: cek setelah apply (port/health/container).

---

## H. APPLY MATURITY MODEL

| Level | Item | Bukti/Alasan |
|---|---|---|
| **L1 safe-auto** | user creation, copy config aman, package install, mkdir simple | `category_meta.go` users/configs/packages; `configs.go` mkdir fix |
| **L2 auto-with-warning** | enable/restart service, activate nginx site (runtime+cert ready), pull image / sync compose metadata | `category_meta.go:121` "Enable/start/restart only after prereqs"; `docker.go` pull |
| **L3 guarded-manual** | DB dump/restore, overwrite config, restart prod service, service cutover, volume overwrite | `database.go` offline_copy; `category_meta.go` services "explicit confirmation"; `pipeline.go:2001` manual cutover |
| **L4 manual/out-of-scope** | private repo / unknown app code, secret tanpa restore path, DNS unsupported, cert issuance (challenge), unknown hybrid dep | kancasoft: PM2 code/cert/DNS tidak di-collect |

**Audit per kategori ke level:** lihat tabel §D. Kesimpulan: L1/L2 aman otomatis dulu; DB/docker-volume/cutover = L3 guarded; app-code/runtime-install/cert/DNS = L4 manual.

---

## I. ENHANCEMENT BACKEND (audit/proposal — lihat enhancement-plan.md untuk detail)

Minimum agar compare-in-pipeline nyata:
1. **Normalized inventory model** — `ParityItem`, `DependencyRef`, `VerificationResult` (sudah ada `VerificationResult` `pipeline_models.go:280`). Map dari `migration_steps.data` (source) + live target collect (reuse `DiffService` collect).
2. **Compare engine** — input source+target inventory → delta `ParityItem[]` (status/suggestedAction/applyLevel/deps/warnings/freshness). Bisa extend `DiffService.computeDiff` jadi richer `ComputeParity`.
3. **Selection persistence** — `migration_steps` action=`selection` (mirip `dryrun` persist `dryrun.go`) atau tabel baru `migration_selections{migration_id, item_key, action}`. Harus survive reload (pattern dryrun sudah ada).
4. **Apply translation** — selected `ParityItem` diterjemahkan ke step Apply. **Jangan** bikin execution path ke-2; extend `initialSyncStage` agar per-step Apply bisa di-skip bila `action=keep_target/skip`, atau dijalankan bila `apply_from_source`. `StepStatusApplied` guard (`pipeline.go:1642`) tetap berlaku.
5. **Verification engine** — setelah apply, `healthVerificationStage` (`pipeline.go:1912`) diperdalam: cek service active, package/runtime installed, port listening, container running, DB exists, nginx upstream healthy, cert valid, drift sisa. Bisa pakai refresh snapshot + collector live.
6. **Freshness policy** — reuse `planReuseMaxAge` (`reuse.go:15`); compare result invalid bila snapshot >30m atau target berubah sejak compare → warning "source/target may have changed".
7. **Secrets/cert/DNS** — compare HANYA menandai `missing`/`manual_required`. Tidak ada secret di compare/apply result. `.env`/`letsencrypt`/`DNS` = L4 manual (referenced but not dumped).

---

## J. ENHANCEMENT FRONTEND (audit/proposal — lihat enhancement-plan.md)

1. Compare surface: wizard summary (reuse DiffService) + pipeline compare tab.
2. Matrix table: source vs target, status badge, suggested action, apply level, dependency badge.
3. Selection UX: per-item toggle, per-category bulk, safe-only, reset.
4. Dependency panel: saat item dipilih.
5. Apply handoff: selection → pipeline execute (persist).
6. Verification UI: parity score, matched/failed/unresolved, manual-next, refresh.
7. Honesty rules: no fake 100%, no synthetic success, no hidden manual-only, no zero-downtime claim for offline_copy, no "service ready" dari file presence hanya.

---

## K. PRODUK DECISION

1. **Seberapa dekat ke "bandingkan, centang, apply, target mirip source"?** — Hari ini ~40%: compare ada (DiffService), tapi selection+selective-apply+parity BELUM. Setelah 6A2–6A6, ~75% untuk L1/L2 item; L3/L4 tetap guarded/manual.
2. **Paling realistis otomatis dulu:** packages, configs (aman), users, docker images (registry), compose def, volumes.
3. **Harus tetap manual:** app code, PM2 dump, runtime install (Node/PM2/nginx), cert, DNS, DB data (guarded L3).
4. **Syarat minimum agar tidak menyesatkan:** dependency drawer wajib muncul; L4 item explicit "manual_required"; parity score jujur (unresolved drift tidak disembunyikan).
5. **TIDAK boleh diklaim sampai implementasi selesai:** one-click parity penuh; app hidup otomatis; zero-downtime; "service ready" dari file presence.

---

## L. ROADMAP (singkat — detail di enhancement-plan.md)

- **6A1** Audit complete (dokumen ini) — placement=Hybrid, source-of-truth=gabungan+mormalisasi, parity boundary=L1/L2 auto, L3 guarded, L4 manual.
- **6A2** Normalized inventory + compare engine (extend `DiffService` → `ComputeParity`).
- **6A3** FE compare summary (wizard) + compare matrix (pipeline tab).
- **6A4** Selection persistence + safe selective apply (L1/L2).
- **6A5** Verification/parity result (deepen `healthVerificationStage`).
- **6A6** Guarded categories (DB/docker/service/runtime dependency enforcement).
- **6A7** App/runtime/manual-required handling (L4 honest workflow).

---

## M. RISKS / NON-NEGOTIABLES / HONESTY

- **RISK**: double-collect (plan + diff) bisa stale → freshness policy wajib.
- **RISK**: user centang item tanpa dependency → dependency drawer + hard-dep block wajib.
- **NON-NEGOTIABLE**: jangan bikin flow baru di luar wizard/pipeline.
- **NON-NEGOTIABLE**: `StepStatusApplied` guard tetap; jangan bypass replay.
- **HONESTY**: service file ≠ runnable; config copied ≠ runtime ready; image present ≠ build context present; db discovered ≠ db data synced; execute succeeded ≠ app healthy. (Semua konsisten Phase 5E + APP-C.)

## N. JAWABAN FORMAT (tl;dr)

- **Compare paling bagus di pipeline?** Ya untuk item-level; wizard untuk summary → **Hybrid** (C).
- **Data sudah cukup?** Sebagian: `migration_steps.data` (source) + `DiffService` (target live). Belum normalized per-item.
- **Data belum ada?** `ParityItem` model, `DependencyRef`, selection persistence, target-side per-item value, runtime/app-code/cert/DNS collectors.
- **Kategori sudah selectable apply?** packages, configs, users, docker (images/compose/volumes) — L1/L2.
- **Harus manual/guarded?** DB (L3), services restart (L3), app code/runtime/cert/DNS (L4).
- **Dependency wajib sebelum apply?** Node/PM2/code untuk PM2 service; nginx+cert+upstream untuk vhost; engine+creds untuk DB; compose+build-context untuk compose.
- **Refactor backend minimum penting?** `DiffService.computeDiff` → `ComputeParity` (tambah value/action/applyLevel/deps); selection persist (pattern dryrun); `initialSyncStage` skip/apply-by-selection.
- **FE surface minimum penting?** pipeline compare tab (matrix + dep drawer + selection); wizard summary reuse DiffService.
- **UX "klik klik pasang" jujur?** Hybrid + dependency block + honesty badges + parity score jujur + L4 explicit manual.
- **Roadmap paling aman?** 6A1→6A2 (model) →6A3 (UI) →6A4 (safe apply) →6A5 (verify) →6A6 (guarded) →6A7 (manual).
