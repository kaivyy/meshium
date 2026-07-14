# Phase 5E — Live Validation Baseline (Pre-Flight Audit)

**2026-07-15** · Phase 5E pre-flight. This is the *verified current state* of
Part 5 before any new code changes. Every claim in the user's Phase 5E brief was
checked against real files/lines. Items not verifiable in this environment are
marked **UNVERIFIED (no live env)** and become live-matrix hypotheses in §9.

**Standing constraints honored:** no fake-success, no silent skip, no unproven
capability claims. `zeroDowntimeCapable` stays `false`. Credentials never in
logs/plaintext/step data.

---

## 1. Current Part 5 behavior — VERIFIED

### 1.1 Deterministic dump path
`internal/mod/migration/database.go:304-306` (`applyFile`):
```go
srcDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", a.migrationID, safe)
tgtDumpPath := fmt.Sprintf("/tmp/meshium_dump_%d_%s", a.migrationID, safe)
transferID := fmt.Sprintf("db:%d:%s", a.migrationID, safe)
```
`sanitizeName` (database.go:504) keeps DB name filename-safe. Path is NOT
time-based → a killed upload leaves a findable partial. **VERIFIED (code).**

### 1.2 Two transfer legs, different resume guarantees
`applyFile` runs 3 legs (database.go:314-378):
1. **Dump on source** (database.go:316-320) — re-dump ONLY if source dump file
   absent (`transfer.FileExists`). Reusing the surviving dump keeps the
   fingerprint stable so the upload leg can resume.
2. **Download source→local temp** (database.go:341-348) — `Resume:false`;
   `os.CreateTemp` (database.go:328) is **ephemeral** (deferred `os.Remove`,
   database.go:333). This leg is **never resumable** (D3 gap, real).
3. **Upload local→target** (database.go:368-378) — `Resume: a.checkpointStore != nil`.
   This is the only resumable leg.

### 1.3 SCP resume mechanism
`internal/mod/transfer/scp.go`:
- `SCPStrategy.CanResume()` → `true` (scp.go:33).
- `localToRemote` (scp.go:112-187): on `opts.Resume`, reads target partial size
  via `GetFileSize`; if `0 < dstSize < totalSize`, seeks local source to `offset`
  and calls `uploadAndAppend` (temp-file + `cat >>` append, scp.go:319-341).
  **VERIFIED: append, not overwrite — even on fresh attempt (attempt==0 branch,
  scp.go:135-145).** Retries (`opts.MaxRetries=3`) also resume from partial.
- `resumed` flag set honestly and returned in `TransferResult.Resumed`.

### 1.4 LongTransferExecuter selection (cap-free path)
`internal/mod/transfer/longtransfer.go`: `longDownload`/`longUpload` type-assert
`ssh.(LongTransferExecuter)` and call `DownloadLong`/`UploadLong` (ctx-bounded,
no SFTP wall-clock cap) when present; else fall back to capped
`ssh.Download`/`ssh.Upload`. `SCPStrategy` legs call `longUpload`/`longDownload`.
**VERIFIED (code + `TestLongDownloadPrefersCapFreePath`).**

### 1.5 Reconcile fail-closed
`internal/mod/migration/database.go:399-419` (`reconcileOrFail`):
- Loads persisted checkpoint; if absent → `VerdictFreshStart`.
- Builds `ReconcileEvidence` from **live** source snapshot (`sourceSnapshot` →
  `transfer.StatFile` size+mtime, database.go:444-450) and target partial state.
- Calls `transfer.Reconcile`.
`internal/mod/transfer/checkpoint.go:87-117` (`Reconcile`):
- `cp.SourceSnapshot != ev.CurrentSourceSnapshot` → `ManualIntervention` (checkpoint.go:96-99).
- Partial target missing after bytes transferred → `ManualIntervention` (checkpoint.go:115-116).
- `err != nil` in `reconcileOrFail` → `fail=true` (database.go:415-417).
- On `fail=true`, `applyFile` returns `ErrTransferReconcileFailed` → stage fails
  to `NeedsManualIntervention` (database.go:357-358). **VERIFIED (code +
  `TestDatabaseResumeRefusesWhenSourceMoved`).**

### 1.6 Checkpoint persistence + cleanup
`sqliteCheckpointStore` (`checkpoint.go:121-204`) writes to table
`transfer_checkpoints` (server-side SQLite `*sql.DB`, injected via
`NewSQLiteCheckpointStore(db)` in `cmd/server/main.go`). Survives backend
restart (durable, not memory-only). **VERIFIED (code).**
- Save: `persistTransferCheckpoint` after upload (database.go:375-377, 425-440)
  with `LastVerifiedPhase:"transferred"`.
- Delete: only after **restore succeeds** (database.go:384-391,
  `DeleteCheckpoints(transferID)`). Restore failure path does NOT delete →
  **checkpoint survives a failed restore (C7 requirement). VERIFIED (code).**

### 1.7 Engine.Resume skips verified steps
`internal/mod/migration/engine.go:374-490`: `Resume` loads
`GetVerifiedCheckpoints`, builds `skipSet`, and **skips already-verified steps**
(engine.go:467-476). The per-category `StepStatusApplied` guard
(pipeline.go:2408-2413, 1613) prevents re-applying a completed category.
**VERIFIED (code).**

### 1.8 Cutover reachability + real side effects
- `trafficSwitchStage.Execute` calls `runAutoCutover` only when
  `pc.Config.AutoCutover` (pipeline.go:1977-1978).
- `runAutoCutover` (pipeline.go:2057+) gates via `CheckAutoCutover`
  (policy.go:150, engine+provider support) and `AutoCutoverDefault=false`
  (cutover_orchestrator.go:49).
- `CutoverOrchestrator.Run` (cutover_orchestrator.go:136+) drives a 12-step
  machine with `FencingAuthority` lease asserted before every mutating step;
  stale/missing/conflicting lease fails closed to `NeedsManualIntervention`;
  idempotent re-entry returns `ErrCutoverReentryDone`.
- Real drivers wired: `newTrafficSwitcher` (pipeline.go:2254) returns real
  `NginxSwitcher`/`HAProxySwitcher`/`CaddySwitcher` — **genuine traffic-switch
  side effect, not event-only. VERIFIED (code).** MongoDB not in cutover path
  (fails closed).
- **UNVERIFIED in this env:** end-to-end live cutover against real hosts (C10).

### 1.9 Progress / event payloads (current shape)
`WSMessage` (`model.go:46-51`) = `{step, status, value, error}`. `applyFile`
emits `Value` strings with `% leg db: %.1f%% — %d/%d bytes — %d B/s` via
`dbTransferProgress` (database.go:454-465) → **bytes + speed present, but no
normalized `transfer_id`/`resume_status`/`downtime_class`/etc. fields (D1/D2
gap).** No `bytes_total`/`estimatedBytes` structured field for the FE size
display requirement (J).

### 1.10 DB execution modes (host/container/compose)
`database_adapter.go:110-130` (`execPrefix`) wraps engine commands:
- host → no wrap
- container → `docker exec -i <ShellQuote(container)> --`
- compose → `docker compose [-f <ShellQuote(file)>] exec <ShellQuote(svc)> --`
All identifiers `ShellQuote`-safe. **VERIFIED (code).** `Apply` does not yet
consume `ExecMode` from `DatabaseConfig` at execution (only `Collect`/`planner`
populate it: database.go:104). **GAP: container/compose exec not yet plumbed
into `Apply` run-time (needs D5/exec-mode plumbing).**

---

## 2. File / function / line mapping

| Concern | File | Lines |
|---|---|---|
| Deterministic dump path, 3-leg transfer, resume upload | `migration/database.go` | 294-393 |
| reconcileOrFail (fail-closed) | `migration/database.go` | 399-419 |
| persistTransferCheckpoint + delete-after-restore | `migration/database.go` | 421-440, 389-391 |
| sourceSnapshot (size+mtime) | `migration/database.go` | 444-450 |
| dbTransferProgress / WSMessage adapter | `migration/database.go` | 454-465 |
| SetCheckpointStore injection | `migration/database.go` | 164-167 |
| Pipeline injects checkpoint store | `migration/pipeline.go` | 1596 |
| StepStatusApplied skip guard | `migration/pipeline.go` | 1613, 2408-2413 |
| runAutoCutover gate | `migration/pipeline.go` | 1977-1978, 2057+ |
| newTrafficSwitcher (real drivers) | `migration/pipeline.go` | 2254-2261 |
| engine.Resume skip-verified | `migration/engine.go` | 374-490 |
| Reconcile + SQLite store | `transfer/checkpoint.go` | 87-204 |
| SCP resume append | `transfer/scp.go` | 33, 112-187, 319-341 |
| LongTransferExecuter selection | `transfer/longtransfer.go` | whole file |
| StatFile (source fingerprint) | `transfer/helpers.go` | 89 |
| ExecMode wrap (host/container/compose) | `migration/database_adapter.go` | 110-130 |
| Credential env (no process-list leak) | `migration/database_adapter.go` | 238-271 |
| DowntimeClassFor gate | `migration/category_meta.go` | 45-57 |
| zeroDowntimeCapable=false | `migration/reuse.go` | 119 |

---

## 3. Checkpoint lifecycle

```
applyFile(db):
  dump on source ──(if srcDump absent)──> remote /tmp/meshium_dump_<id>_<db>
  download src → local temp (ephemeral, non-resumable)
  reconcileOrFail:
     checkpoint? ─ no ─> FreshStart (full upload)
                  ─ yes ─> live snapshot vs cp.SourceSnapshot
                       mismatch ─> ManualIntervention (fail, stage→needs_manual)
                       match + partial ─> Resume (append upload)
  upload (Resume = store!=nil):
     SCPStrategy appends to target /tmp/meshium_dump_<id>_<db>
     ──save checkpoint (phase="transferred") ──
  restore (cap-free, idempotent):
     success ──delete checkpoint (delete-after-verified) ──> done
     failure ──checkpoint KEPT ──> stage failed (retry safe)
```

---

## 4. Transfer / fallback paths

- Engine file-path (PG, Redis): `applyFile` → dump→download→upload→restore.
- Engine stream (MySQL, Mongo): `applyStream` (database.go:259-289) →
  `ExecPipe`→`io.Pipe`→`ExecWithStdin`. No temp file. **No resumable
  checkpoint path exists for streams (D5 gap): a killed stream restarts.**
- Fallback: `applyStream` falls back to `applyFile` if either side lacks
  `StreamExecuter`/`WriteExecuter` (database.go:266-272).
- `LongTransferExecuter` absent (test mocks) → capped `Download`/`Upload`
  fallback (longtransfer.go).

---

## 5. Timeout model

- Dump/restore: `execLongOrContext` (cap-free, ctx-bounded) — Part 6.
- SFTP upload/download: `LongTransferExecuter` (cap-free) when the concrete
  `*ssh.Client` implements it; else capped `Upload`/`Download` with
  `FileTransfer`-style total-timeout.
- SCP `MaxRetries=3`, per-attempt backoff (scp.go:49-91).
- **C8 risk:** if the concrete client does NOT implement `LongTransferExecuter`,
  large transfers hit the capped path. Must confirm the production client does
  implement it (live test C8).

---

## 6. Secret handling — VERIFIED clean

- Password stored encrypted (`shared.Encrypt`) at plan-save
  (pipeline_handler.go:63-83); decrypted in-memory at execute only
  (pipeline_handler.go:85-105); API responses redacted to `"set"`
  (pipeline_handler.go:107-115).
- Password passed to engine commands via **env vars** (`PGPASSWORD=`,
  `MYSQL_PWD=`, mongo `--uri` with URL-escaped creds) → not in argv/process
  list (database_adapter.go:238-271). `ShellQuote` on every identifier.
- No `log.`/`fmt.Print` of command strings found in `transport/client.go`
  Exec* methods → no command/secret leakage in logs.
- Credentials never persisted into `migration_steps.data` (planner substitutes
  in-memory only).
- **Residual risk (not a finding, a guardrail):** `redactDBConfig` only redacts
  when `Password != ""`. If a config is stored decrypted-by-design elsewhere,
  re-audit. No such path found.

---

## 7. Resume-state surface — CURRENT (insufficient, needs D2)

Today the FE only sees a generic `status` (running/success/failed) and a free
`value` string. There is no structured distinction between:
`fresh_transfer` / `resuming_upload` / `restarting_download` /
`resume_refused_source_changed` / `resume_refused_partial_invalid` /
`resume_not_supported` / `manual_intervention_required` /
`verification_in_progress` / `verified_complete`. **This is the core D1/D2 UX
gap.**

---

## 8. Unresolved risks (from Part 5, carried forward)

1. **Download leg not resumable (D3).** Local temp is ephemeral; a killed
   transfer re-downloads the source dump on retry, then appends the upload.
2. **Resume protects the WAN upload leg only.** Source→local and source dump are
   restart-prone.
3. **Fingerprint = size+mtime (D4).** A same-size/same-mtime content rewrite is
   missed (C4).
4. **MySQL/Mongo stream has no resumable artifact path (D5).** Must surface
   `resume_not_supported`, not a generic resumable badge.
5. **`zeroDowntimeCapable` = false (G).** No engine `SupportsLiveReplication()`
   → `DowntimeClassFor` returns `minimal_downtime`/`offline_copy`. Honest.
6. **No live multi-GB run executed (Workstream E).** All resume logic is
   unit-tested, not live-measured.
7. **ExecMode (container/compose) not plumbed into `Apply` run-time** — Collect
   populates it; execution still runs on host path. Needs plumbing for the
   container/compose live-matrix scenarios (C-host/container/compose).

---

## 9. Live-test hypotheses (must be proven, not assumed)

| # | Hypothesis | Scenario | Gates |
|---|---|---|---|
| H1 | Upload resumes from partial after SSH disconnect at 10/50/90% | C2 | scp.go append |
| H2 | Source size+mtime change → ManualIntervention | C3 | checkpoint.go:96 |
| H3 | Same-size content change is NOT caught by size+mtime fingerprint | C4 | checkpoint.go (doc only) |
| H4 | Backend restart preserves stage+checkpoint, resume deterministic | C5 | engine.go:374 |
| H5 | Disk-full fails explicit, no false `completed` | C6 | scp.go error path |
| H6 | Restore failure keeps checkpoint | C7 | database.go:389 |
| H7 | Production `*ssh.Client` implements LongTransferExecuter (no cap kill) | C8 | longtransfer.go |
| H8 | Shell-sensitive filenames safe (no injection) | C9 | ShellQuote |
| H9 | Cutover reaches real orchestrator + fencing | C10 | pipeline.go:2254 |
| H10 | ExecMode container/compose actually execs in-container | C-host/container/compose | database_adapter.go:110 |

---

## 10. Honest status before Phase 5E coding

- All 4 Part 5 unit tests green; `go test ./...` green (cached this session).
- The implementation is **code-complete and fail-closed** but **live-unproven**.
- The genuine engineering gaps for Phase 5E are: **D1/D2 (observability +
  explicit resume states), D3 (local-dump persistence decision), D4 (fingerprint
  hardening decision), D5 (stream non-resumable honesty + ExecMode plumbing), G
  (zero-downtime policy enforcement in UI/API), and J (size display).**
- The **live matrix C1–C10 cannot be executed in this environment** (no
  source/target VMs, no network shaping). Phase 5E will (a) build the harness +
  fixtures as runnable artifacts, (b) execute whatever is locally runnable
  (unit/integration), and (c) document C1–C10 as **execution-pending with exact
  commands** — never marked passed against mocks.
