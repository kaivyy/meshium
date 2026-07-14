# Part 5 (Resumable DB Dump/Restore Transfer) — Final Report

**2026-07-15** · Completion report for "lanjut Part 5 dan selesaikan semuanya".

This report covers the residue of the umbrella Phase 5B/Part work: making the
**file-path DB transfer (PostgreSQL, Redis) genuinely resumable** after a killed
multi-GB transfer, and confirming the surrounding Parts 7/8 (fenced cutover,
checkpoint-driven resume) are genuinely complete — not stubbed. It is the honest
complement to `phase-5-final-report.md` and `phase-5-transfer-engine.md` /
`phase-5-checkpoint-resume.md`.

**Standing constraint honored:** no fake-success, no silent skip, no unproven
capability claims. We integrated an already-built, already-tested engine
(`internal/mod/transfer`, previously orphaned) — we did not build new transfer
machinery, and we did not claim byte-resume for the download leg (see Risks).

---

## 1. The problem this round actually solved

Before this round, `DatabaseApplier.applyFile` (PG/Redis) did a one-shot
dump → SFTP → restore:

- The dump/restore commands were cap-free (Part 6), but the **SFTP legs went
  through `ssh.Upload`/`ssh.Download` with a 10m `FileTransfer` total-timeout**.
- The target dump path was `time.Now().UnixNano()`-based, so a killed upload left
  a **random, unfindable** partial → the next attempt restarted from zero.
- No checkpoint was persisted, so nothing could drive a resume decision.

Net: a killed multi-GB PG/Redis transfer restarted from zero (safe via idempotent
restore, but not resumable). This round makes it resume from the last byte offset.

---

## 2. What shipped (commit `956ac6f`)

| Change | File | Why |
|---|---|---|
| Import-cycle break | `transfer/step.go` | `transfer` imported `migration` (orphaned `FileTransferStep` adapter used `migration.StepContext`/`WSMessage`). `migration` now imports `transfer` → would have cycled. `transfer` now owns local `StepContext`/`WSMessage` (minimal subset). No live path constructs `FileTransferStep`, so no behavior change. |
| Cap-free SFTP (preserves Part 6) | `transfer/longtransfer.go` (new), `transfer/scp.go` | New `LongTransferExecuter` (`DownloadLong`/`UploadLong`), ctx-bounded, no 10m cap. `SCPStrategy` legs prefer it, fall back to capped `Download`/`Upload`. |
| Source fingerprint | `transfer/helpers.go` (`StatFile`) | size+mtime used to detect a source that changed after a checkpoint. |
| Deterministic dump path + resume | `migration/database.go` (`applyFile`) | Target dump path now `/tmp/meshium_dump_<migrationID>_<db>` → killed upload leaves a findable partial; `transfer.SCPStrategy.Transfer(Resume=true)` appends to it. Re-dump only when the source dump file is absent (keeps the source fingerprint stable). |
| Honest fail-closed resume | `migration/database.go` (`reconcileOrFail`) | Before upload, if a checkpoint exists, build `ReconcileEvidence` (live source snapshot + target partial size) and call `transfer.Reconcile`. Source moved / partial inconsistent → `VerdictManualIntervention` → `applyFile` returns `ErrTransferReconcileFailed` → stage fails to `NeedsManualIntervention`. Never a blind resume. |
| Checkpoint persistence | `migration/database.go`, `migration/pipeline.go`, `cmd/server/main.go` | `Pipeline` takes `transfer.CheckpointStore` (SQLite-backed from `*sql.DB`). `initialSyncStage` injects it into the applier via `SetCheckpointStore`. Checkpoint saved after upload; deleted after a verified restore (no phantom "partial" on next retry). |
| Test move | `migration/longcmd_test.go`, `transfer/longtransfer_test.go` (new) | Cap-free transfer-leg test moved to `transfer` (where the logic now lives); command-leg test kept. |

---

## 3. Parts 7 & 8 — verified, not stubbed

- **Part 7 (fenced cutover):** reachable. `trafficSwitchStage` calls `runAutoCutover`
  when `pc.Config.AutoCutover` is true (`pipeline.go:1977`), which is gated by
  `CheckAutoCutover` (engine/provider support, shared with the API boundary).
  `CutoverOrchestrator.Run` drives a 12-step machine with a `FencingAuthority`
  lease asserted before **every** mutating step; stale/missing/conflicting lease
  fails closed to `NeedsManualIntervention`; idempotent re-entry returns
  `ErrCutoverReentryDone`. PostgreSQL/MySQL/Redis supported; MongoDB fails closed
  (no safe cutover contract).
- **Part 8 (checkpoint resume):** `engine.Resume` loads `GetVerifiedCheckpoints`
  and **skips already-verified steps** (does not re-apply them). `transfer_checkpoints`
  table + `NewSQLiteCheckpointStore` exist. The per-step `StepStatusApplied` skip
  guard in `initialSyncStage` covers already-applied *categories*.

Both were already wired; this round did not need to build them. No fake-success.

---

## 4. Tests

| Test | File | Proves |
|---|---|---|
| `TestDatabaseResumeRefusesWhenSourceMoved` | `migration/database_resume_test.go` (new) | Source snapshot changed after checkpoint → `ManualIntervention`, fail-closed (the honesty-critical path). |
| `TestDatabaseResumeAllowsWhenSourceStable` | `migration/database_resume_test.go` (new) | Source unchanged + partial present → `VerdictResume`. |
| `TestLongDownloadPrefersCapFreePath` | `transfer/longtransfer_test.go` (new) | SFTP legs take `DownloadLong`/`UploadLong` when available; capped fallback otherwise. |
| `TestExecLongOrContextPrefersLongPath` | `migration/longcmd_test.go` (kept) | Dump/restore commands take cap-free path. |

Full module `go test ./...` green; `go build ./...` clean; `go vet` clean on the
touched packages.

---

## 5. Capability matrix (database category)

| Engine | Collect | Dump/Restore | Transfer path | Cap-free | Resumable |
|---|---|---|---|---|---|
| PostgreSQL | ✓ | ✓ | file (SFTP) | ✓ (Part 6 + this round) | ✓ (upload leg) |
| Redis | ✓ | ✓ | file (SFTP) | ✓ | ✓ (upload leg) |
| MySQL | ✓ | ✓ | stream (pipe) | ✓ | n/a (no temp file) |
| MongoDB | ✓ | ✓ | stream (pipe) | ✓ | n/a (no temp file) |

---

## 6. Remaining risks (honest, not faked)

1. **Download leg not resumable.** Only the slow **WAN upload** leg resumes. The
   *local* temp dump is re-downloaded on retry (meshium's temp is ephemeral). A
   killed transfer still re-downloads the source dump, then appends the upload.
   ponytail: persist the local dump to also resume the download leg (~2× dump disk
   on meshium).
2. **`zeroDowntimeCapable` still `false`.** The live-replication chain (Parts 4–8
   of the downtime gate) is not yet verified end-to-end. Rollback is already
   split-brain-safe (LIFO, backup-gated, fail-closed). Honest: no "zero downtime"
   claim.
3. **No live multi-GB integration run executed.** The resume logic is unit-tested
   (fail-closed decision + cap-free path), but the actual "kill a 50 GB transfer
   mid-flight and watch it append" needs a real PG/Redis pair (the umbrella
   `phase-5-final-report.md` Blocking item E — the live matrix — remains the only
   non-code gate).
4. **Reconcile fingerprint is size+mtime**, not a content hash. A same-size,
   same-mtime source rewrite would be missed. Acceptable fail-closed posture
   (restart-from-partial is bounded by idempotent restore); a content hash would
   be stronger but costs a full source read before every resume.

---

## 7. Sub-report updates

- `phase-5-transfer-engine.md` §B3: byte-level resume for the **DB file path** is
  now **DONE** (deterministic path + `SCPStrategy.Resume` + checkpoint). rsync-based
  volume transfer (B2) and download-leg resume remain open (Risks 1).
- `phase-5-checkpoint-resume.md` §D2: checkpoint **consumption for resume** on the
  DB file path is now **DONE**; the fail-closed `Reconcile` is the audit surface.

**Recommendation:** ship Part 5; the only remaining gate is the live test matrix
(Workstream E) — same as the umbrella report. Until E runs, the resume behavior is
code-grounded and unit-tested, not live-measured.
