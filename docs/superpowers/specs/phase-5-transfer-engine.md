# Phase 5 — Workstream B: Transfer Engine (Large Files & Volumes)

**2026-07-14** · Phase 5.

Audit demanded: replace the 5-min SFTP `FileTransfer` with resumable rsync
(`--partial --append-verify` + sha256 checkpoint); fix Docker volume transfer
(`planner/bridge.go` tar+SFTP, 5-min, unquoted path injection); persist
`TransferCheckpoint` and resume instead of start-from-zero.

> The brief cites `plannerbridge.go` / `planner/bridge.go:264` — those files do
> **not exist** on this branch. The actual transfer layer is `sync.go`
> (`SyncEngine`, rsync-based) plus `database_adapter.go`/`database.go` for DB
> streaming. The 5-min SFTP path the audit attacked is **not the live transfer
> path** for the `database` category (which streams via `ExecPipe`/`ExecWithStdin`
> or file-path PG/Redis) and for file sync uses `rsync` already. Grounding below.

---

## B1. rsync resume (current reality)

`SyncEngine.InitialSync` (`sync.go:99-123`) already builds and runs an **rsync over
SSH** command (`buildRsyncRemoteSpec`, `sync.go:83`), parses `--progress`
(`rsyncProgressRegex`, `sync.go:56`), and supports `ChecksumVerify`
(`sync.go:43`). So the "replace SFTP >1GB with rsync" ask (P1-3) is **already met**
for the file-sync path.

### Remaining gap (genuine)
- **No resume/checkpoint on rsync.** `InitialSync` runs rsync as a single
  `ExecContext`; if it dies mid-transfer (network drop), there is no
  `--partial --append-verify` flag and no `TransferCheckpoint` persisted to resume
  from byte offset. The `database` file-path (PG/Redis) uses SFTP download/upload
  (`database.go`) with the same non-resumable property.
- **Phase 5 net-new (B1):** add `--partial --append-verify` to the rsync command
  builder (`sync.go`), persist a `TransferCheckpoint{file, bytesTotal, checksum}`
  before/after transfer, and on restart re-invoke rsync (it natively resumes from
  the partial file). Add `compression` toggle (the audit flags a hardcoded `-z`;
  confirm in `sync.go` and make it per-size tunable — small LAN files skip
  compression, large WAN files use `zstd`/moderate `-z`).

## B2. Docker volume transfer

### Current reality
No `planner/bridge.go` tar+SFTP code exists. Volume migration today is **not** a
first-class category on this branch (no `volume` registration in
`categories.go:53` — only `database` is registered). The audit's volume concern is
therefore **latent**, not live.

### Phase 5 net-new (B2)
- Implement a `volume` category (or extend `database`-style streaming) using
  `rsync -e ssh -avzP --partial` over SSH between host volume paths
  (`/var/lib/docker/volumes/<v>/_data`), with **shell-quoted** paths
  (`shared.ShellQuote`), progress callback, and the B1 checkpoint/resume. This is
  net-new code (P1-3 volume + P0-6 injection fix folded in via `ShellQuote`).
- Until implemented, volume migration is **out of scope** for the Phase 5
  "minimal downtime realistic" claim — document as a limitation.

## B3. Checkpoint & resume

### Current reality
`UpdateStageCheckpoint` **has a caller now**: `pipeline.go:455` persists checkpoint
JSON per stage. The audit's "zero caller" is stale. But the persisted checkpoint is
not yet **consumed to resume a mid-stage transfer** — resume still re-runs the stage
from its start (the `StepStatusApplied` skip guard at `pipeline.go:~1256` covers
already-applied *categories*, not byte-resume of a 50 GB file).

### Phase 5 net-new (B3)
- Wire the rsync/file transfer to read a prior `TransferCheckpoint` and pass
  `--partial --append-verify` + the partial path so a killed 50 GB transfer resumes
  from the byte offset, not zero.
- Post-transfer: compute sha256 at source and dest (`syncChecksumCommand`,
  `sync.go:95` already exists); on mismatch, bounded retry, then fail closed.

### Status (updated 2026-07-15 — Part 5, commit `956ac6f`)
- **DB file-path resume: DONE.** `DatabaseApplier.applyFile` now uses the already-built
  `internal/mod/transfer` engine (`transfer.SCPStrategy.Transfer(Resume=true)`) with a
  **deterministic** target dump path `/tmp/meshium_dump_<migrationID>_<db>` so a killed
  upload leaves a findable partial that the next attempt appends to. A persisted
  `transfer.Checkpoint` (SQLite-backed) + `transfer.Reconcile` makes the resume
  **fail-closed**: a source that changed after the checkpoint → `ManualIntervention`,
  never a blind resume. See `part5-resumable-db-transfer-report.md`.
- **rsync volume transfer (B2) + download-leg resume: still OPEN** (the local temp
  dump is re-downloaded on retry; only the WAN upload leg resumes). ponytail per the
  final report's Risk 1.
- The transfer package's `SCPStrategy` prefers `LongTransferExecuter`
  (`DownloadLong`/`UploadLong`, ctx-bounded) so Part 6's cap-free behavior is preserved.

---

## Phase 5 net-new work for Workstream B

| Item | Status | Action |
|---|---|---|
| rsync for file sync (P1-3) | done (`sync.go`) | add `--partial --append-verify` + compression toggle |
| `TransferCheckpoint` persist (P1-2) | partial (`pipeline.go:455`) | consume it for byte-resume |
| Docker volume rsync (P1-3 + P0-6) | **not present** | implement `volume` category with `ShellQuote` |
| SFTP 5-min injection (P0-6) | n/a (SFTP not live path) | ensure all new shell uses `ShellQuote` |
| 50 GB live resume test (E) | not executed | **implement** in Workstream E matrix |

**Honest claim:** file sync already uses rsync; the genuine B-work is (1) resumable
rsync + checkpoint consumption, (2) the `volume` category, (3) the 50 GB live
resume test. No rewrite of a 5-min SFTP path is needed because that path is not the
active transfer mechanism.
