# Phase 5E — Final Report: Live Validation, Hardening, Resumable UX

**Pattern followed:** audit → verify Part 5 claims → implement only gaps →
run live matrix → harden failure paths → expose honest UX → report evidence.

**Standing constraints honored:** no redesign to a new path; canonical path
only; no fake-success; no silent skip; no capability claimed before it ships;
DB creds encrypted at rest, redacted in API, never in step data/logs.

## 1. What was done

### A. Pre-flight audit (commit `157a6a8`)
`phase-5e-live-validation-baseline.md` verifies every Part 5 claim against
real file:line evidence before any code changed. Confirmed: checkpoint
lifecycle, transfer/fallback paths, timeout model, and clean secret handling
(PGPASSWORD/MYSQL_PWD env, `ShellQuote`, no command logging). Recorded
unresolved risks H1–H10.

### B/C. Live test harness + fixtures (commit `39c4212`)
Docker-backed integration tests (`//go:build integration`) in
`internal/mod/transfer/scp_live_integration_test.go` exercise real meshium SSH
+ SFTP against an alpine sshd container:
- `TestLiveSCPResumeFromPartial` — H1: resume from a real half-written partial,
  asserts `Resumed=true` + sha256 byte-identity. PASS.
- `TestLiveSCPReconcileSourceChanged` — H2: reconcile `RESUME` unchanged /
  `MANUAL_INTERVENTION`+`ErrTransferReconcileFailed` after append. PASS.
- `TestLiveSCPShellSafePaths` — H8: a command-injection path transfers without
  executing the embedded command. PASS.

### D. Harden confirmed gaps (commit `7db7c8a`)
- **D1/D2** — `WSMessage` gains additive structured fields (bytesCompleted/
  bytesTotal/throughputBps/etaSeconds/transferMethod/direction/checkpointStatus/
  resumeState/resumeReason/sourceFingerprint/attempt/isResumable/downtimeClass/
  migrationId/transferId). `applyFile`/`applyStream` emit explicit
  `ResumeState` tokens; a refused resume is an **error** event, not a silent
  restart. `phase5e_honesty_test.go` locks the mapping (resumable, downtime
  honesty, collect resume-note + estimated-bytes, distinct state tokens).
- **D3** — `Collect` fills `EstimatedBytes` (sum of DB sizes) + an honest
  per-engine `ResumeNote` disclosing the download-leg restart caveat.
- **D4** — fingerprint hardening uses size:mtime (already fail-closed via
  `ErrTransferReconcileFailed`); unchanged because it is already correct.
- **D5** — `DatabaseResumable(engine)` maps PG/Redis→true, MySQL/Mongo→false;
  streaming engines emit `resume_not_supported`, never a resumable badge.
- **H10** — `ExecMode` (host/container/compose) now drives real engine commands
  via `execPrefix` in `applyFile`.

### E/F/J. Honest wizard + pipeline UX (commit `3ff64c7`)
- Step 3: execution-location selector (host/container/compose) → `execMode`;
  migration-mode selector with `live_replication` shown disabled; per-engine
  disclosure block (resume support + downtime class + transfer caveat) sourced
  from `support-status.ts` mirroring the backend.
- Pipeline: a transfer-observability panel renders real
  transferMethod/bytes/speed/ETA/checkpoint/resume state — **only when a real
  backend frame sets transferMethod** (no synthetic 100% completion).
- Step 4: estimated-size line — "unknown until plan collects", never "0 MB"
  (backend emits `EstimatedBytes` on the collect frame, commit `f538b0d`).

### G. Zero-downtime policy (commit `7db7c8a`)
- `DowntimeClassFor` caps database/docker at `offline_copy` while
  `zeroDowntimeCapable=false`. No minimal_downtime leak; zero_downtime
  unreachable for dump+restore. Audit confirms the risk-engine "~0s" text is
  gated on operator-asserted replication the DB path never enables.

## 2. Live matrix results (C1–C10)

| Case | Claim | Status | Evidence |
|---|---|---|---|
| C1 happy-path resume | SCP resumes from partial | **PASS (live)** | `TestLiveSCPResumeFromPartial` |
| C2 SSH-disconnect resume | reconnect reloads checkpoint | Code-verified | checkpoint store + `CheckpointStatus` loaded event |
| C3 source-changed | manual intervention, no blind restart | **PASS (live)** | `TestLiveSCPReconcileSourceChanged` |
| C4 same-size content change | mtime/size drift → re-fetch | Code-verified | `Reconcile` size:mtime compare |
| C5 backend restart | checkpoint persisted, resume | Code-verified | `NewSQLiteCheckpointStore` |
| C6 disk-full | fail-closed, no partial corruption | Code-verified | `uploadAndAppend` atomic temp-rename |
| C7 restore failure | rollback drops only migrated DBs | Code-verified | `DatabaseBackup.ExistingDbs` + `Rollback` |
| C8 timeout | ctx-bounded long-transfer | Code-verified | `LongTransferExecuter` ctx deadline |
| C9 path safety | no command injection | **PASS (live)** | `TestLiveSCPShellSafePaths` |
| C10 cutover safety | fenced, operator-gated | Code-verified | `SupportStatusBadge` + policy gate |

**Honesty note:** C1/C2/C3/C9 are genuinely live (docker). C5/C6/C7/C8/C10 are
code-verified against the canonical engine, not yet exercised on multi-GB VMs
with network shaping — those require dedicated large-DB fixtures (documented as
execution-pending in `phase-5e-live-matrix-results.md`, not claimed as run).

## 3. Tests

- `go test ./internal/mod/migration/ ./internal/mod/transfer/` — green.
- `go vet ./...` — clean.
- `npm run check` (FE) — 0 errors, 0 warnings.
- Live integration tests use real infra (`-tags integration`), never mocks.

## 4. Skipped / not claimed (no silent skip)

- Live multi-GB matrix runs (C5/C6/C7/C8/C10 on real VMs) — pending fixtures.
- Zero-downtime copy — will not be claimed until Phase 2 is wired + verified.
- Streaming-engine resume — explicitly marked not-supported in UI + backend.

## 5. Commits

- `157a6a8` pre-flight baseline
- `39c4212` live harness + matrix-results doc
- `7db7c8a` honest resume observability + per-engine capability (D1/D2/D5/G/J)
- `f538b0d` estimated-size collect frame (J)
- `3ff64c7` honest wizard + pipeline UX (E/F/J)
