# Known Limitations — Migration Engine (Phase 1 P0 Safety Fixes)

> Status of the migration engine after the Phase 1 P0 safety pass (commits
> `013f96c` → `c2dd87b`). This document is the authoritative statement of what
> Phase 1 does and does **not** deliver. Product copy, API responses, and the UI
> must not claim capabilities beyond this list.

## What Phase 1 delivers

- **Database dump/restore** (PostgreSQL, MySQL/MariaDB, MongoDB, Redis) via
  streamed pipe (MySQL/Mongo) or file path (PG/Redis). Downtime = transfer
  time. No live replication, no fencing, no automatic traffic switching.
- **Honest state terminal states.** Rollback ends in `rolled_back` only when
  every step succeeded; any step failure → `rollback_degraded`; an ambiguous
  or unsafe replication topology → `needs_manual_intervention`. Partial
  failure is never reported as a clean rollback.
- **Manual cutover gate.** A migration with a `manual_required` traffic
  switch stops at `awaiting_cutover` and survives a restart. Committing
  requires an operator-confirmed `switch_state`; an unconfirmed commit is
  rejected with `409 cutover_not_confirmed`. There is no automatic commit.
- **Stream/output safety.** Long-running commands run under the caller's
  context (no fixed 5m ceiling for streaming dump/restore). Output is
  bounded (stdout 1 MiB, stderr 256 KiB); exceeding the cap surfaces
  `ErrOutputLimitExceeded`. Inactivity (not a wall-clock ceiling) triggers
  `ErrInactivityTimeout`.
- **No secrets on argv / logs / errors / API responses / persisted data /
  audit export.** Redis uses `REDISCLI_AUTH`; PostgreSQL/MongoDB use scoped
  env or temp credential files cleaned up on success/failure/cancel.

## Residual limitations (Phase 2)

1. **No byte-level / mid-transfer resume.** Resume restarts at the last
   fully-checkpointed stage boundary; an interrupted transfer re-runs from
   the stage start (safe via idempotent restore flags + `StepStatusApplied`
   skip). Transfer-level resume (e.g. rsync `--partial`) is Phase 2. Do not
   describe Phase 1 transfers as "resumable large transfers."
2. **No live replication / fencing / automatic traffic switching.** Zero
   downtime is **not** claimed. Replication rollback re-points only confirmed
   Redis replicas (source primary + target replica); MySQL/PG destructive
   rollback shortcuts remain prohibited (Phase 2 fencing). Do not describe
   cutover as "automatic" or "zero-downtime."
3. **`FileTransfer` 30m is an interim SFTP/relay bound**, configurable, not
   "large file solved." Streaming (Phase 2) removes it.
4. **Mongo `mongo` fallback** retained for Mongo 4.x; dropped when 5.x is
   the floor.
5. **`awaiting_cutover` commit** requires an operator-confirmed
   `switch_state`; no automatic commit exists or is planned this pass.

## Wording rules

API responses, UI text, and docs must use these exact terms and must **not**
use: "zero-downtime", "automatic cutover", "automatic commit",
"resumable large transfer", or any phrase implying replication-backed
continuous sync in Phase 1.

| Concept | Allowed wording | Not allowed |
|---|---|---|
| Cutover | "manual cutover required" / "awaiting operator cutover" | "automatic cutover", "zero-downtime cutover" |
| Transfer resume | "resumes from the last completed stage" | "resumable large transfer", "mid-transfer resume" |
| Rollback result | "rolled back" / "rollback degraded" / "needs manual intervention" | "rolled back" when steps failed |
| Commit | "operator-confirmed commit" | "automatic commit" |
