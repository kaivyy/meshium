# Phase 5 — Workstream D: Checkpoint & Resume per Stage

**2026-07-14** · Phase 5.

Audit demanded: `UpdateStageCheckpoint` had zero callers and `checkpoint_data` was
always NULL (no mid-stage resume); persist checkpoint per stage (file/byte
position, stage status, processed DB subset) and resume from a sensible point
after a crash.

> Stale finding: `UpdateStageCheckpoint` **now has a caller** at `pipeline.go:455`,
> and `sqliteRepo.UpdateStageCheckpoint` (`pipeline_repo.go:154`) persists JSON.

---

## D1. Persist checkpoint per stage (current reality)

- `pipeline.go:455` calls `p.UpdateStageCheckpoint(stageID, checkpointJSON)` during
  execution — the audit's "zero caller" is fixed.
- The `StepStatusApplied` checkpoint marker (`StepStatusApplied`, model.go:33) lets
  a resumed run skip already-applied *categories* (the `initialSyncStage` skip
  guard).
- `TransferCheckpoint` concept exists in the audit; the live code persists a
  generic checkpoint JSON but **does not yet store byte offsets for large-file
  transfers** (see Workstream B3).

### Remaining gap (genuine, P1-2)
- The checkpoint JSON is written but **not consumed to resume mid-stage work**:
  a crashed 50 GB rsync re-runs from zero; a crashed DB dump re-runs from zero.
  Phase 5 must make the transfer layer (B3) and DB dump (A) read the checkpoint and
  resume.

## D2. Resume semantics (design)

- On pipeline crash/interruption → `StateInterrupted` → `Resume`
  (`state.go:284`, `pipeline.go` resume path).
- Resume must:
  1. load the stage checkpoint (`GetStageCheckpoint`),
  2. for a large-file transfer: re-invoke rsync with `--partial --append-verify`
     from the partial file (B3),
  3. for a DB dump/restore: re-run only unprocessed databases (the `database`
     category can compute the processed subset from `backup`/`applied` records),
  4. never re-apply an `already-applied` category (existing `StepStatusApplied`
     guard).
- Idempotent restore flags already present (`--clean --if-exists` PG,
  `--add-drop-database` MySQL, `--drop` Mongo) make resume safe.

### Phase 5 net-new (D2)
- Implement checkpoint **read + resume** in the transfer and DB-apply paths; add a
  unit test that kills a transfer mid-flight and asserts resume continues (not
  restart-from-zero). This is the same work as B3 — D and B overlap; the single
  implementation lives in the transfer/DB layer and is certified by the E-matrix
  failure-injection test.

---

## Phase 5 net-new work for Workstream D

| Item | Status | Action |
|---|---|---|
| `UpdateStageCheckpoint` caller (P1-2) | done (`pipeline.go:455`) | — |
| `StepStatusApplied` category skip | done | — |
| Byte-level resume for large files (P1-2) | **DB FILE PATH DONE (2026-07-15, `956ac6f`)** | `DatabaseApplier.applyFile` delegates to `transfer.SCPStrategy` with `Resume=true` + deterministic dump path + persisted `transfer.Checkpoint`; `transfer.Reconcile` fails closed. See `part5-resumable-db-transfer-report.md`. rsync volume (B2) + download-leg resume still open. |
| DB-subset resume | **not present** | implement in A (re-run unprocessed DBs) |
| Crash-resume test | not executed | unit-tested the fail-closed decision (`TestDatabaseResumeRefusesWhenSourceMoved`/`...AllowsWhenSourceStable`) + cap-free path; **live** failure-injection still in Workstream E |

**Honest claim:** checkpoint *persistence* exists; checkpoint *consumption for
resume* on the DB file path is now done and fail-closed. The live failure-injection
test remains the certification gate (Workstream E).
