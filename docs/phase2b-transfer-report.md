# Phase 2B — Large-Transfer Correctness: Final Report

**Date:** 2026-07-12
**Status:** COMPLETE (Commit 7 of 7, issue #53)
**Baseline guard:** Phase 1 P0 + Phase 2A cutover untouched. No zero-downtime
claim added.

---

## 1. Mandate (recap)

Make large-data transfer *really usable* for real migrations: direct
source→target transfer, durable checkpoint, integrity verification,
retry/reconcile after restart, honest fallback. Phase 2B deliberately did
**not** add a zero-downtime claim — the focus is transfer correctness,
resumability, and operability.

Fail-closed rules enforced throughout: if transfer state is ambiguous, the
checksum mismatches, restart reconciliation fails, the source/target path is
unverifiable, or remote cleanup is uncertain → fail closed to
`needs_manual_intervention` or a terminal degraded state. No silent downgrade
from rsync-direct to tar/SFTP.

---

## 2. What shipped

| Area | Deliverable | File |
|---|---|---|
| Persistence | `TransferCheckpoint` struct + SQLite store (UPSERT on transfer_id,file_name) | `internal/mod/transfer/checkpoint.go`, `internal/db/migrations.go` |
| Reconcile | `Reconcile(cp, ev)` → `Resume`/`FreshStart`/`ManualIntervention` (fail closed) | `internal/mod/transfer/checkpoint.go` |
| Strategy | `RsyncStrategy.Availability` (both-side rsync + SSH reachability gate), `Mode` | `internal/mod/transfer/availability.go`, `rsync.go` |
| Selection | `SelectWithFallback` — prefer rsync-direct; fail closed unless `AllowDegraded` | `internal/mod/transfer/strategy.go` |
| Errors | typed transfer errors mapped from rsync exit codes + context-cancel→inactivity | `internal/mod/transfer/errors.go`, `exec.go`, `rsync.go` |
| Verify | `ChecksumVerifier.VerifyInto` — sets `verified` ONLY on match | `internal/mod/transfer/checksum.go` |
| Terminal states | `TerminalStateForError` → ok/failed/degraded/needs_manual_intervention | `internal/mod/transfer/checksum.go` |
| Volume routing | `ClassifyVolumePath`, `ShouldCompress`, `PlanDockerVolume` (unknown→fail closed) | `internal/mod/transfer/volume.go` |
| Step wiring | `DockerVolumeMigrationStep`: classify → gate → operator-visible `DEGRADED` relay | `internal/mod/planner/bridge.go` |
| Integration | live rsync source→target push in alpine+rsync containers | `internal/mod/transfer/rsync_integration_test.go` |
| Docs | known-limitations §2B, runbook, this report | `docs/*` |

---

## 3. The one real product bug found

Integration testing surfaced a genuine defect that would have broken **every**
real direct transfer to a not-yet-existing target path:

```
rsync: mkdir "/data/app" failed: No such file or directory
```

rsync does not create the destination's *parent* directories. Fixed by adding
`--mkpath` to the rsync argv in `rsync.go` `transferOnce`. Without this, any
migration moving data to a fresh target mount fails outright. This is the kind
of bug only a live transfer exposes.

---

## 4. Fail-closed invariants (proven by tests)

1. **No success without verified checksum.** `VerifyInto` is the sole writer of
   `LastVerifiedPhase="verified"`; mismatch → `ErrTransferChecksumMismatch`
   (terminal, fail closed).
2. **Reconcile never blind-resumes.** Source changed / partial missing or
   mismatched / strategy invalid / unknown path → `ManualIntervention`.
3. **No silent downgrade.** Without `AllowDegraded`, `SelectWithFallback`
   returns `ErrTransferStrategyUnavailable` rather than falling back to the
   tar relay. The degraded path emits a `DEGRADED` warning first.
4. **Missing target rsync blocks direct.** The availability gate flips `Mode()`
   to `blocked` and surfaces `ErrTransferTargetUnreachable`.

---

## 5. Test results

- **Unit** (`go test ./internal/mod/transfer/ ./internal/mod/planner/ ./internal/mod/migration/ ./internal/mod/ssh/`): green.
- **Integration** (`make test-transfer-integration`, `//go:build integration`,
  alpine+rsync containers): 3 tests pass (~38s):
  - `TestLiveRsyncDirectTreeTransfer` — real rsync push + byte-identical
    checksum verify (raw + engine verifier).
  - `TestLiveRsyncReconcileTrustsPartial` — partial + unchanged source →
    `Resume`; changed source → `ManualIntervention`.
  - `TestLiveRsyncMissingRsyncBlocksDirect` — target rsync removed → `blocked`
    + `ErrTransferTargetUnreachable`.

---

## 6. Honest residual limitations (Phase 2C, not started)

1. **Volume step uses the degraded relay, not direct rsync.** The
   `DockerVolumeMigrationStep` lacks target-host topology (host/user/port) to
   build a true remote→remote rsync spec, so it routes to the operator-visible
   tar-over-SSH relay. Direct rsync source→target is exercised for plain
   file/directory transfers, not the volume path.
2. **Whole-file resume only.** rsync `--partial --append-verify` resumes a
   single file as a whole; many small files resume per-file via size+checksum
   skip. Byte-range resume of one huge file is Phase 2C.
3. **`FileTransfer` 30m interim SFTP/relay bound** still applies to the
   non-rsync path; direct rsync removes it for the rsync path.
4. **No zero-downtime claim.** 2B is transfer correctness/resumability, not
   liveness.

---

## 7. Release matrix

| Capability | Phase 1 | Phase 2A | **Phase 2B** |
|---|---|---|---|
| DB dump/restore | ✅ | ✅ | ✅ |
| Manual cutover gate | ✅ | ✅ | ✅ |
| Fenced PG cutover (opt-in) | — | ✅ | ✅ |
| Direct rsync source→target | — | — | ✅ (gate-guarded) |
| Durable transfer checkpoint | — | — | ✅ |
| Resume-after-restart (fail-closed) | — | — | ✅ (whole-file) |
| Checksum verify-then-persist | — | — | ✅ |
| Typed transfer errors / terminal states | — | — | ✅ |
| Degraded fallback (operator-visible) | — | — | ✅ (AllowDegraded) |
| Zero-downtime | ❌ | ❌ | ❌ |
| Volume direct rsync | — | — | ❌ (relay only) |
| Byte-range single-file resume | — | — | ❌ (Phase 2C) |

**Verdict:** GO for Phase 2B — direct rsync transfer is usable and honest.
Phase 1/2A baselines regressed nowhere. The one product defect (missing
`--mkpath`) is fixed and integration-tested.
