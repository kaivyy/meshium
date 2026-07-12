# MongoDB Cutover Runbook (Manual — Automated Cutover NOT Supported)

> **Why this runbook exists.** Phase 2C automated fenced cutover supports
> PostgreSQL, MySQL, and Redis. **MongoDB is explicitly excluded** from
> automated cutover: this codebase has no replica-set lag measurement, so
> catch-up cannot be verified before promotion. Promoting blind would risk a
> split-brain / data-loss. Every MongoDB cutover primitive fails closed
> (`setupMongoDB`, `preflightMongoDB`, `promoteMongoDB`, `rollbackMongoDB`
> all return errors before mutating state), and the pipeline dispatch layer
> (`autoCutover` + engine `mongodb`) refuses immediately.
>
> Therefore MongoDB migrations use the **Phase 1 manual cutover** path
> (`autoCutover=false` → stops at `awaiting_cutover`, survives restart,
> requires operator-confirmed `switch_state`; unconfirmed commit is rejected
> with `409 cutover_not_confirmed`). This runbook covers that manual path plus
> the data safety checks an operator must do by hand.

## 1. Prerequisites

- Source is a MongoDB replica set (or standalone → replica set) with a
  healthy primary.
- Target is a running `mongod` (or replica set) reachable over SSH from
  Meshium.
- `mongodump`/`mongorestore` (or `mongo`/`mongosh`) available on both hosts.
- Backup of the source taken **before** any transfer (see §4).

## 2. Data transfer (manual, Phase 1 mechanics)

Meshium's `database` category dumps and restores MongoDB via streamed pipe
(`mongodump --archive --gzip` → `mongorestore --archive --gzip --drop`).
This is a **downtime = transfer time** operation, not live replication.

1. Plan the migration with the `database` category and engine `mongodb`.
2. Run the pipeline. The transfer stage dumps the source and restores to the
   target. Monitor `DBSIZE`/collection counts on both sides.
3. Verify row/collection parity **before** any cutover (see §3).

## 3. Pre-cutover verification (operator-owned)

Automated cutover would have checked these; you must do them by hand:

- [ ] Source `rs.status()` shows a healthy primary and the data is current.
- [ ] Target restore is byte/collection-complete: `db.getCollectionNames()`
      on source and target match; document counts reconcile.
- [ ] No application writes are in flight that are not yet on the target
      (there is no lag measurement — quiesce writers or accept the gap).
- [ ] DNS / connection strings for clients are ready to repoint.

## 4. Cutover (manual)

1. **Quiesce writers** to the source (stop the app or set the source
   read-only / secondary). This is the manual equivalent of the freeze the
   automated path cannot safely perform for Mongo.
2. Re-run a final incremental `mongodump`/`mongorestore` if any writes landed
   after §2, and re-verify parity.
3. Repoint application traffic to the target (DNS, connection string, or the
   traffic provider — for Mongo there is no Meshium auto-switch; do it at the
   client/DNS layer).
4. Confirm the target is serving reads/writes correctly.
5. In Meshium, confirm the cutover (`switch_state`) so the migration records
   the commit. Until confirmed, a commit attempt returns `409
   cutover_not_confirmed`.

## 5. Failure / rollback

- If §2/§3 verification fails: **do not cut over.** Re-run the transfer; the
  target is dropped-and-restored (`--drop`), so a retry is idempotent.
- If cutover already happened but the target is bad: the only safe backout is
  to repoint clients back to the source (assuming the source was not
  destroyed) and re-sync. There is **no automated MongoDB rollback** — never
  run a script that reconfigures the source replica set automatically.
- Any ambiguity → `needs_manual_intervention`. Partial failure is never
  reported as a clean rollback.

## 6. What NOT to do

- Do **not** enable `autoCutover` for a MongoDB migration — it is refused, but
  if that guard is ever weakened, the result would be a blind promote.
- Do **not** run `rs.reconfig` / `replSetStepDown` via automation scripts
  expecting Meshium to manage topology — it does not.
