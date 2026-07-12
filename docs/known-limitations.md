# Known Limitations — Migration Engine (Phase 1 P0 + Phase 2A Cutover)

> Authoritative statement of what the migration engine does and does **not**
> deliver. Phase 1 P0 baseline is immutable (no regression). Phase 2A adds an
> **opt-in** fenced PostgreSQL cutover. Product copy, API responses, and the UI
> must not claim capabilities beyond this list.

## What Phase 1 delivers (immutable baseline)

- **Database dump/restore** (PostgreSQL, MySQL/MariaDB, MongoDB, Redis) via
  streamed pipe (MySQL/Mongo) or file path (PG/Redis). Downtime = transfer
  time. No live replication, no fencing, no automatic traffic switching.
- **Honest state terminal states.** Rollback ends in `rolled_back` only when
  every step succeeded; any step failure → `rollback_degraded`; an ambiguous
  or unsafe replication topology → `needs_manual_intervention`. Partial
  failure is never reported as a clean rollback.
- **Manual cutover gate.** A migration with `autoCutover=false` (default)
  stops at `awaiting_cutover` and survives a restart. Committing requires an
  operator-confirmed `switch_state`; an unconfirmed commit is rejected with
  `409 cutover_not_confirmed`. There is no automatic commit.
- **Stream/output safety.** Long-running commands run under the caller's
  context (no fixed 5m ceiling for streaming dump/restore). Output is
  bounded (stdout 1 MiB, stderr 256 KiB); exceeding the cap surfaces
  `ErrOutputLimitExceeded`. Inactivity (not a wall-clock ceiling) triggers
  `ErrInactivityTimeout`.
- **No secrets on argv / logs / errors / API responses / persisted data /
  audit export.** Redis uses `REDISCLI_AUTH`; PostgreSQL/MongoDB use scoped
  env or temp credential files cleaned up on success/failure/cancel.

## What Phase 2A adds (opt-in, `AutoCutover=true`)

- **Fenced PostgreSQL cutover** for a single same-major source→target pair.
  A durable fence lease (`FencingAuthority`) is acquired before any
  data-plane write; every mutating step re-asserts it. The orchestrator
  (`CutoverOrchestrator`) is a 12-step machine with checkpoint reconciliation
  and fail-closed on any primitive failure.
- **Real cutover primitives:** `ReplicationEngine.Preflight` (same-major,
  source-primary, target-standby, replicator connectivity),
  `WaitForCatchUpPG` (drains `pg_stat_replication` replay_lag to ≤threshold),
  `PromotePG` (SQL `pg_promote()`, verified by a post-promote recovery probe).
- **No-dual-writer invariant:** source accepts writes until fenced + caught
  up; target accepts writes only after promote. The target is a read-only
  standby between the switch and the promote.
- **Integration-tested:** `pg_cutover_integration_test.go` (`//go:build
  integration`) drives the real primitives + orchestrator against a live
  Docker PostgreSQL primary+standby pair. Three tests: live primitives,
  preflight-fail-when-target-not-standby, full orchestrated cutover.

## Residual limitations (Phase 2A)

1. **Source freeze is deferred.** The fence lease serializes the cutover but
   does not set a Postgres read-only GUC on the source. The no-dual-writer
   invariant holds on the happy path; a writer that ignores the lease could
   still write to the source during the switch→promote window. The lease is
   the hard fence; the source GUC is defense-in-depth, not yet wired.
2. **Single target, single nginx provider.** No multi-standby pinning, no
   multi-traffic-provider. `pg_stat_replication` first row is assumed to be
   the target.
3. **Cutover bounded by one lease lifetime** (`FenceTTL - 10s` ≈ 4m50s). No
   lease-renewal loop yet; a cutover that cannot finish in one lease lifetime
   fails closed.
4. **No automatic backout.** The orchestrator fails closed and persists the
   failure; the operator owns the backout (see `docs/cutover-runbook.md` §5).
5. **Only PostgreSQL same-major.** MySQL/Mongo/Redis and cross-major PG
   cutover are **not** implemented in Phase 2A.

## What Phase 2B adds (large-transfer correctness)

- **Direct rsync source→target over SSH.** `RsyncStrategy` pushes the
  data stream straight from the source host to the target host (Meshium
  only relays the rsync control channel, never the bytes). Selected only
  when **all three** hold: rsync present on BOTH sides, and the source
  can reach the target over SSH without a password prompt (BatchMode probe).
- **Durable, fail-closed reconcile.** `TransferCheckpoint` persists
  source snapshot + target partial fingerprint; after a restart
  `Reconcile` returns `Resume` (partial matches + source unchanged),
  `FreshStart` (nothing transferred), or `ManualIntervention`
  (source changed / partial mismatched / missing / strategy invalid).
  Any ambiguity → ManualIntervention. Never blind-resumes.
- **Integrity verify-then-persist.** `ChecksumVerifier.VerifyInto`
  records both checksums and sets `LastVerifiedPhase="verified"` ONLY
  on a match. A checkpoint is never persisted as complete without a
  proven verification. Mismatch → `ErrTransferChecksumMismatch`
  (terminal, fail-closed).
- **Typed transfer errors + honest terminal states.**
  `TerminalStateForError` maps every typed error to
  `ok` / `failed` / `degraded` / `needs_manual_intervention`.
  Checksum mismatch and reconcile failure are fail-closed, never
  silently retried or reported as success.
- **No silent downgrade.** The tar-over-SSH/SFTP relay is reachable ONLY
  as an explicit, operator-visible `degraded` fallback
  (`AllowDegraded=true` + a `DEGRADED` warning). Without it,
  `SelectWithFallback` returns `ErrTransferStrategyUnavailable`
  (fail closed) rather than downgrading. An unverifiable volume path
  routes to `manual_intervention`.
- **Integration-tested.** `rsync_integration_test.go`
  (`//go:build integration`) drives a real rsync source→target push
  between two alpine+rsync containers: byte-identical checksum verify,
  reconcile-trusts-partial, and missing-target-rsync-blocks-direct.

## Residual limitations (Phase 2C — not yet started)

1. **Topology is host-to-host only.** The Docker-volume step has no
   target-host topology (host/user/port) to form a true remote→remote
   rsync spec, so it uses the explicit tar-over-SSH relay (degraded,
   operator-visible) — NOT direct rsync. Direct rsync source→target
   is exercised for plain file/directory transfers, not the volume path.
2. **No chunked parallel WAN resumability yet.** Resume is whole-file
   (rsync `--partial --append-verify`). Many small files resume per-file
   via size+checksum skip. Large single-file byte-range resume is Phase 2C.
3. **`FileTransfer` 30m is an interim SFTP/relay bound**, configurable, not
   "large file solved." Direct rsync removes it for the rsync path.
4. **Mongo `mongo` fallback** retained for Mongo 4.x; dropped when 5.x is
   the floor.
5. **No zero-downtime claim.** Phase 2B is about transfer
   correctness/resumability, not liveness. Downtime still = transfer time.

## What Phase 2C adds (cutover engine/provider extension)

> **Status: implemented.** Extends the Phase 2A fenced PostgreSQL cutover to
> additional engines and one more traffic provider, behind the same fail-closed
> contract. PostgreSQL, MySQL (seeded), and Redis are supported; MongoDB is
> **not** (fails closed at dispatch). See `docs/superpowers/specs/2026-07-13-
> phase2c-engine-providers.md` for the full contract and compatibility matrix.

### Supported engines

- **PostgreSQL** (Phase 2A): same-major only, fenced, source-freeze (read-only
  GUC) deferred as defense-in-depth. Verified by `pg_cutover_integration_test.go`.
- **MySQL / MariaDB** (seeded replication): same-major only. The target is
  **seeded** (streamed `mysqldump` → `mysql` restore) *before* the replica is
  configured, so the target is not empty on cutover (fixes the unseeded
  Phase-1 defect). Preflight gates: same-major, `log_bin` ON, unique non-zero
  `server_id`, source `read_only=OFF`, target `read_only=ON`, replicator
  connectivity. Promote freezes the source (`read_only=ON`) **before** stopping
  the replica and making the target writable; post-promote probe must show the
  target is now a primary or it fails closed.
- **Redis** (fenced): source must be `role:master`, target a `role:slave`
  pointing at this source with `master_link_status:up`. Promote runs
  `REPLICAOF NO ONE` and confirms the target became `role:master`. Redis has
  **no freeze** — the dual-writer window is bounded by the async replication
  gap, not eliminated. This is the one accepted residual risk; do not claim a
  hard no-dual-writer guarantee for Redis.

### Supported traffic providers

- **nginx**: `NginxSwitcher` — idempotent (idempotency key + cache), bounded,
  read-after-write verify, sanitized persist. (Phase 2A.)
- **haproxy**: `HAProxySwitcher` — identical contract to nginx (`haproxy -c`
  config test, `systemctl reload`, same verify/sanitize). Second supported
  auto-switch provider (Phase 2C).

### MongoDB — NOT supported, fails closed

MongoDB replication cutover is **refused** at the pipeline dispatch layer
(`autoCutover` with engine `mongodb` → immediate `failAutoCutover`) and at every
primitive (`setupMongoDB`, `preflightMongoDB`, `promoteMongoDB`,
`rollbackMongoDB` all return errors before issuing a mutating command). The
blocker is that replica-set lag has no measurement in this codebase, so
catch-up cannot be verified before promotion — promoting would be blind. Use
manual cutover (the Phase 1 baseline) for MongoDB. Runbook:
`docs/mongodb-cutover-runbook.md`.

## Residual limitations (Phase 2C cutover)

1. **MySQL source freeze uses `read_only` GUC**, not a PG-style hard freeze; a
   writer that bypasses the lease could still touch the source during the
   switch→promote window. The fence lease is the hard serializing fence; the
   `read_only` flip closes the dual-writer window on the happy path.
2. **Redis has no source freeze.** The dual-writer window is bounded by async
   repl lag. Accept the gap or use manual cutover for strict no-dual-writer
   requirements.
3. **Single target, single traffic provider per cutover.** No multi-standby
   pinning, no simultaneous multi-provider switch.
4. **Cutover bounded by one lease lifetime** (~4m50s). No lease-renewal loop; a
   cutover that cannot finish in one lease fails closed.
5. **No automatic backout.** The orchestrator fails closed and persists the
   failure; the operator owns the backout (see `docs/cutover-runbook.md` §5 and
   `docs/mongodb-cutover-runbook.md`).
6. **MongoDB cutover is manual only.** No automated Mongo cutover this pass.

## Wording rules

API responses, UI text, and docs must use these exact terms and must **not**
use: "zero-downtime", "automatic cutover", "automatic commit",
"resumable large transfer" (for Phase 1 only), or any phrase implying
replication-backed continuous sync in Phase 1.

| Concept | Allowed wording | Not allowed |
|---|---|---|
| Cutover | "manual cutover required" / "awaiting operator cutover" / "fenced PG cutover (opt-in)" | "automatic cutover", "zero-downtime cutover" |
| Transfer (Phase 2B) | "direct rsync source→target" / "resumable (rsync --partial)" / "verify-then-advance" / "degraded fallback (operator-visible)" | "zero-downtime transfer", "automatic resume" |
| Transfer resume | "resumes from the last completed stage" OR (Phase 2B) "resumes the partial via rsync --partial" | "resumable large transfer" for Phase 1, "mid-transfer resume" (unspecified) |
| Transfer failure | "failed" / "degraded" / "needs manual intervention" | silent downgrade, success on checksum mismatch |
| Rollback result | "rolled back" / "rollback degraded" / "needs manual intervention" | "rolled back" when steps failed |
| Commit | "operator-confirmed commit" | "automatic commit" |

