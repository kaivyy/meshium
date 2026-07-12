# Phase 2B Implementation Plan — Large-Transfer Correctness

> Companion to `docs/superpowers/specs/2026-07-12-phase2b-large-transfer.md`.
> Builds on `internal/mod/transfer`. Immutable baselines: Phase 1 + Phase 2A.
> Execute commit-by-commit; run tests after each; stop on any baseline regression.

## Commit 1 — Investigation + design + matrix  ✅ `5cb58da`
Spec + compatibility matrix committed.

## Commit 2 — Durable TransferCheckpoint persistence + reconcile primitive
- Extend `transfer.TransferCheckpoint` with spec fields: `TransferID`,
  `Category`, `MigrationID`, `Resumable bool`, `SourceSnapshot`,
  `TargetPartialState`, `ChecksumSource`, `ChecksumTarget`, `LastVerifiedPhase`,
  `StartedAt`, `UpdatedAt`. Keep `CheckpointStore` interface; add a real
  `sqliteCheckpointStore` backed by a new `transfer_checkpoints` table in
  `internal/db/migrations.go`.
- Add `Reconcile(ctx, cp) (ReconcileVerdict, error)` where verdict ∈
  {Resume, FreshStart, ManualIntervention}. Verdict checks: partial target
  exists & matches; source object/path == snapshot; strategy still valid.
- Unit tests: persist→get round-trip; reconcile matrix (partial-match=resume,
  source-changed=manual, missing-partial=fresh, strategy-invalid=manual).

## Commit 3 — rsync direct orchestration + strategy selection + validation
- `RsyncStrategy`: `IsAvailable` checks BOTH source AND target rsync
  (`SSHClient` on each `TransferTarget`) + source→target SSH reachability probe.
  Add `Mode()` returning `direct`/`degraded`/`blocked`.
- `StrategySelector.Select` returns primary (rsync direct) with typed
  unavailability; add `SelectWithFallback` that returns degraded fallback only
  when explicitly acceptable.
- Path validation + shell-quoting via `shared.ShellQuote` (already used).
- Idempotent invocation (`--partial --append-verify`).
- Unit tests: availability both-side, reachability gate, mode reporting, quoting.

## Commit 4 — Progress, timeout, output-safety, typed errors for large transfer
- Wire rsync execution to the inactivity model (run under caller ctx like
  `ExecWithStdin`); do NOT use buffered `ExecContext` for large transfers.
- Add typed errors: `ErrTransferStrategyUnavailable`, `ErrTransferSourceUnreachable`,
  `ErrTransferTargetUnreachable`, `ErrTransferAuth`, `ErrTransferOutputCap`,
  `ErrTransferInactivity`, `ErrTransferChecksumMismatch`,
  `ErrTransferReconcileFailed`, `ErrTransferTopologyUnsupported`.
- Map rsync exit codes (23/24 partial, 12 protocol, etc.) to typed errors.
- Unit tests: error classification from exit codes + context.

## Commit 5 — Integrity verification + degraded/failed terminal states
- `ChecksumVerifier`: ensure streaming (no full-file buffer); compare
  source/target checksums; persist verify outcome BEFORE marking applied.
- Mismatch → `failed`/`degraded` terminal, never success, never auto-delete.
- Secondary signal: count/size/time metadata as secondary, not primary.
- Unit tests: match→ok; mismatch→failed; verify-persisted-before-advance order.

## Commit 6 — Docker volume/file transfer + degraded fallback gating
- Volume path discovery: bind-mount (host path authoritative), named-volume
  materialized path, compose-managed path. Unverifiable → blocked (actionable).
- Degraded fallback: tar-over-SSH / SFTP only as explicit, operator-visible
  `degraded` mode; never silent downgrade.
- Compression policy by data type (off for binary/volume by default).
- Unit tests: volume path classification; fallback gating; compression policy.

## Commit 7 — Integration/failure-injection + runbook + known-limitations + matrix
- `//go:build integration` tests with two alpine containers (rsync installed):
  direct tree transfer; interrupted+restart+resume; target partial exists;
  source changed after checkpoint; source unreachable; target unreachable;
  rsync missing → degraded/blocked; checksum mismatch; volume transfer.
- `docs/transfer-runbook.md`, update `docs/known-limitations.md` (Phase 2B
  section), release matrix, final report `docs/phase2b-transfer-report.md`.
- `make test-transfer-integration` target.

## Acceptance (all commits)
- No success result without persisted verification outcome.
- Interrupted supported transfers resume/reconcile safely after restart.
- No silent downgrade direct→weaker fallback.
- Checksum mismatch never = success.
- `go build ./...`, `go vet ./...`, `go test ./...` green; Phase 1+2A tests green.
