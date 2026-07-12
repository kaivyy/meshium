# Phase 2B — Large-Transfer Correctness, Resumability & Operability (Investigation + Design)

> **Status:** Investigation complete; design decision locked. This document is
> the Phase 2B contract. Phase 1 and Phase 2A are **immutable baselines** —
> no Phase 1/2A invariant may regress. Phase 2B adds **no** zero-downtime
> claim; its scope is transfer correctness, resumability, and operability for
> large files/volumes via direct source-to-target transfer. Do **not** claim
> byte-level resume or resumable large transfer until interrupted-transfer
> tests prove continuation + data integrity.

## 1. Investigation summary

Verified by reading source (`internal/mod/migration`, `internal/mod/transfer`,
`internal/mod/ssh`) at HEAD `b71aee6`. Key facts:

### 1.1 There are TWO parallel, mostly-dead transfer implementations

1. **`internal/mod/transfer` package** — rich: `RsyncStrategy` (remote-to-remote
   direct push), `SCPStrategy` (offset-resumable upload/download), `ChecksumVerifier`
   (source/target md5), `TransferCheckpoint` + `CheckpointStore` interface,
   `DirectoryTransfer`/`FileTransferStep`. **Only imported by
   `planner/bridge.go`** (builds plan *steps*, never executed). No pipeline caller.
2. **`migration.SyncEngine` (`sync.go`)** — rsync `-avz --partial --append-verify`,
   `InitialSync`/`DeltaSync`/`ResumeSync`/`VerifyChecksums`. Runs rsync **on the
   source host** pushing to target → already source-to-target direct. **Has NO
   live caller** — `initialSyncStage.Execute` (`pipeline.go:1350`) explicitly
   does NOT call it (comment :1341-1359). It replays per-category
   `mod.Applier.Apply` instead.

### 1.2 The REAL execution path today

`initialSyncStage.Execute` → per category `mod.Applier.Apply(targetSSH, …)`.
- **File/config categories** (`configs.go`, `docker.go`, `nginx`): use
  `ssh.Download`/`ssh.Upload` — **Meshium relays the bytes** (Download into a
  `bytes.Buffer`, Upload to target). This is the path Phase 2B must enable
  *direct* rsync for, with a degraded fallback that stays visible.
- **`DatabaseApplier.Apply` (`database.go`)**: already has the right shape —
  `Streaming()==true` (MySQL/Mongo) pipes `ExecPipe`→`ExecWithStdin` (no Meshium
  buffering); `Streaming()==false` (PG/Redis) dumps to a remote path →
  `Download`→`Upload`→restore. Same relay concern for the file path.

### 1.3 Timeout / output-safety model (reuse, do not rebuild)

`ssh.Client.ExecWithStdin`/`ExecStreamLinesContextWithTimeout` already implement
the **inactivity-based** watchdog (no fixed wall-clock for streaming; resets on
any stdin/stdout/stderr activity; `ErrInactivityTimeout`). `ExecContext` caps
stdout 1 MiB / stderr 256 KiB and returns `ErrOutputLimitExceeded`. This is the
stream-safety baseline Phase 1/2A rely on — Phase 2B rsync/checksum must run
under caller ctx like `ExecWithStdin`, **not** buffered `ExecContext`.

### 1.4 Environment constraints (testbed)

- `rsync` is **NOT** installed on the host. Integration tests must run rsync
  inside containers (alpine with `apk add rsync`, or debian). `docker` works.
- The existing `transfer` package's `rsync remoteToRemote` shells out via
  `src.SSHClient.ExecContext` (buffered) — must not be used for large transfers;
  use a container-backed harness invoking `rsync` over ssh between two containers.

### 1.5 Compatibility / safety gaps found

- `transfer.RsyncStrategy.IsAvailable` returns false for local sources (no local
  exec) and only checks the *source* side — never the target/dest rsync, never
  SSH reachability source→target. For direct rsync, BOTH sides need rsync and the
  source must reach the target over SSH; current code does not verify any of this.
- `ChecksumVerifier` computes md5 of the *whole* file/tree — fine for small,
  **O(n) memory risk** if it ever buffers large files (verify it streams).
- `TransferCheckpoint` exists but is **never persisted** (only `NoopCheckpointStore`
  is ever constructed). No reconcile-after-restart exists anywhere.
- `buildRsyncCommand` defaults to `-z` (compress) for **all** workloads —
  violates the compression-policy requirement (off for already-compressed data).
- No typed errors distinguish strategy-unavailable / source-unreachable /
  target-unreachable / auth-failure / checksum-mismatch / reconcile-failure.

## 2. Design decisions (locked)

1. **Build on `internal/mod/transfer`, not `SyncEngine`.** `transfer` already has
   the checkpoint/checksum/strategy abstractions Phase 2B needs and is decoupled
   from the pipeline. `SyncEngine` stays dead (out of scope to wire it). We make
   `transfer` *usable* for real transfers: a persisted `CheckpointStore`,
   reconcile logic, and typed errors.
2. **Direct rsync is the primary strategy; tar-over-SSH / SFTP are explicit
   DEGRADED fallbacks** (operator-visible), never silent. A `TransferStrategy`
   reports `Mode()` ∈ {direct, degraded, blocked}. Selection fails closed: if the
   configured primary is unavailable and no fallback is acceptable, → blocked.
3. **Meshium is the control plane, not the data relay.** When direct rsync is
   selected, bytes move source→target over SSH; Meshium only issues the command
   and observes progress/checksum. The Download/Upload relay path is the
   degraded fallback and must be labeled.
4. **Durable `TransferCheckpoint`** persisted before + during transfer; the
   schema from `transfer.TransferCheckpoint` is extended with the fields the
   spec lists (transferID, category, resumable flag, source/target metadata
   snapshot, last-verified-phase, timestamps). A real `sqliteCheckpointStore`
   is added (the repo already has sqlite migration infra).
5. **Reconcile before resume.** On restart, the orchestrator loads the checkpoint
   and verifies: partial target state exists & matches the checkpoint; source
   object/path still equals the snapshot; rsync partial file is consistent;
   strategy still valid. If reconcile fails → fail closed to `NeedsManualIntervention`
   (or a terminal degraded state), never blind-resume.
6. **Integrity = verify, then advance.** Post-transfer checksum compare (or
   count/size secondary signal) persisted *before* the step is marked applied.
   Mismatch → failed/degraded, never success, never auto-delete evidence.
7. **Compression policy** by data type: text/config/log/db-dump compressible →
   `-z` allowed; binary/image/video/docker-layer/volume → off by default.
8. **Typed errors** (`ErrTransferStrategyUnavailable`, `ErrTransferSourceUnreachable`,
   `ErrTransferTargetUnreachable`, `ErrTransferAuth`, `ErrTransferOutputCap`,
   `ErrTransferInactivity`, `ErrTransferChecksumMismatch`, `ErrTransferReconcileFailed`,
   `ErrTransferTopologyUnsupported`) so the pipeline can map to fail-closed.
9. **No bastion claim.** The transport abstraction may support `-J` jump-host in
   the rsync `-e` ssh string, but bastion end-to-end safety is deferred; if the
   source cannot reach the target directly, the topology is *blocked* (or uses
   the relay fallback, labeled degraded) — not silently claimed safe.

## 3. Compatibility matrix (Phase 2B)

| Resource / topology | Status | Notes |
|---|---|---|
| Regular file tree, source SSH→target SSH reachable, rsync both sides | **supported (direct)** | primary path |
| Single large file, direct rsync | **supported (direct)** | `--partial --append-verify` |
| Docker named-volume path (materialized), host path verified | **supported (direct)** | path-discovery must be verified, not guessed |
| Docker bind-mount path | **supported (direct)** | host path is authoritative |
| Source cannot reach target directly, relay fallback acceptable | **degraded (relay)** | operator-visible warning; Meshium relays |
| rsync missing on source or target | **degraded→blocked** | fall back to tar-over-SSH if available, else blocked |
| Compression policy on binary/volume | **supported (off)** | no `-z` default |
| rsync partial-file resume after interruption | **supported (verify first)** | reconcile gate before resume |
| Ambiguous volume path / unverifiable host path | **blocked** | actionable error, no guess |
| Bastion/jump-host end-to-end | **deferred** | documented limit; not claimed safe |
| Chunked parallel WAN resumability | **deferred** | not smaller/safer than rsync direct resume |

## 4. Smallest safe implementation order

1. Investigation report + matrix (this doc) + commit.
2. Extend `TransferCheckpoint` + persisted `sqliteCheckpointStore` + `Reconcile()`
   primitive + unit tests.
3. `RsyncStrategy` direct orchestration: availability checks BOTH sides +
   source→target SSH reachability; quoting/validation; idempotent invocation;
   strategy `Mode()` reporting.
4. Progress/timeout/output-safety wiring to the inactivity model; typed errors.
5. `ChecksumVerifier` integrity + degraded/failed terminal states + persisted
   verify outcome.
6. Degraded fallback (tar-over-SSH / SFTP) gating + Docker-volume discovery +
   wiring.
7. Integration/failure-injection (container pair) + runbook + known-limitations +
   release matrix.

## 5. Baseline invariants preserved (must not regress)

`AwaitingCutover`, `NeedsManualIntervention`, checkpoint persistence,
topology fail-closed rollback, stream safety (inactivity/OutputLimit),
fencing/lease rules, traffic ownership verification, `ForceTransition`
restrictions, honest product wording. Existing Phase 1 + 2A tests stay green.

## 6. Stop conditions

- Direct source→target connectivity cannot be represented safely in the current
  transport model → stop & report.
- Resume/reconcile cannot prove target partial-state safety after interruption
  → stop.
- Any transfer path can still false-success after checksum mismatch /
  incomplete verification → stop.
- Any path requires unbounded output buffering or Meshium relay that defeats the
  architecture → stop.
- Phase 1 or 2A tests regress → stop.
