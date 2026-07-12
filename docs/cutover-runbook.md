# Operator Runbook — Fenced PostgreSQL Cutover (Phase 2A)

**Applies to:** meshium commit `05dd990` and later (Phase 2A-2 → 2A-6).
**Scope:** opt-in PostgreSQL source→target cutover with durable fencing.
**Engagement:** read this before triggering any `AutoCutover=true` migration.

---

## 1. What this does

A `AutoCutover=true` migration runs a **12-step fenced cutover** driven by
`CutoverOrchestrator` (`cutover_orchestrator.go`). Before any data-plane write,
it acquires a durable **fence lease** (`FencingAuthority`). Every mutating step
re-asserts the lease, so an interrupted or stale cutover cannot resume blindly
and cannot double-write.

Hard invariants enforced by code (not by the operator):

- **No dual writer.** The source accepts writes until the lease is held and
  catch-up is verified; the target accepts writes only after `pg_promote()`.
  Between the traffic switch and the promote, the target is a read-only
  standby. There is never a window where both accept writes.
- **Fenced.** A missing/expired/conflicting/stale lease fails closed →
  `needs_manual_intervention`.
- **Fail closed.** Any primitive failure persists the failure and stops.
  No automatic rollback is assumed (the operator owns the backout).

---

## 2. Pre-conditions (the migration must already satisfy these)

| # | Condition | Why |
|---|---|---|
| 1 | Source is a PostgreSQL **primary** (same major as target) | Preflight gate 2 |
| 2 | Target is a **streaming standby** seeded from the source | Preflight gate 3; the source must already show the target in `pg_stat_replication` |
| 3 | Replicator role can connect source←target | Preflight gate 5 (read-only `SELECT 1`) |
| 4 | `AutoCutover=true` set on `MigrationConfig` | otherwise the pipeline stops at `awaiting_cutover` (manual path) |
| 5 | Nginx provider reachable + health URL returns 200 | the switch verifies a read-after-write marker |

If any pre-condition is false, **do not** set `AutoCutover=true`. Use the
manual path (`awaiting_cutover` → operator-confirmed commit), which is unchanged.

---

## 3. Triggering

Set `autoCutover: true` on the migration config and start the pipeline. The
cutover runs automatically inside the `trafficSwitch` stage. There is **no**
separate "press the button" step once the pipeline reaches that stage.

Expected console timeline (abridged):

```
preflight → seed → replicating → verifying → awaiting → fencing →
catching-up → verifying-target → switching → promoting → observing
```

Each step is checkpointed on the fence lease. The stage ends in
`switched` (success) or `needs_manual_intervention` (failure).

---

## 4. Failure handling

### 4.1 Cutover fails closed (most common)

`trafficSwitch` stage → `needs_manual_intervention`. The persisted
`CutoverOutcome.Failure` is sanitized (no secrets). Open it:

```
GET /api/migrations/:id/state   # inspect stage + checkpoint + failure blob
```

The checkpoint records exactly which sub-state it stopped at and which steps
are marked done. **Do NOT re-run `AutoCutover` from a partial checkpoint unless**
the failure is clearly safe to resume (see 4.3).

### 4.2 Stale lease

If a prior run was interrupted (process kill, network split) the lease may
still be held or expired-but-unreleased. `Acquire` returns
`ErrFenceLeaseStale`; the orchestrator surfaces `needs_manual_intervention`
with the stale evidence. Resolve:

1. Confirm no cutover is in flight (check the target: is it still a standby?).
2. If the target is still a standby and the source is still primary and
   replication is healthy, the lease can be released manually:
   `UPDATE fence_leases SET released_at = <now> WHERE migration_id = :id;`
3. Re-run from `awaiting_cutover`.

### 4.3 Deciding resume vs. fresh run

- Stopped **before** `switching` (e.g. `verifying-target`): safe to resume —
  no data-plane write happened. Re-run.
- Stopped **at or after** `switching` but **before** `promoting`: the switch
  may or may not have taken effect. Verify the Nginx upstream actually points
  at the target (read-only). If it does, **do not** let it serve writes —
  revert the Nginx config to the source manually, then re-run.
- Stopped **at or after** `promoting`: the target is now primary. This is a
  completed-promote failure — treat the target as the new primary, point
  traffic at it, and **do not** re-run the orchestrator (it will fail the
  "target must be a standby" gate, by design).

---

## 5. Backout (operator-owned)

The orchestrator does **not** auto-rollback. If you must back out a cutover
that has promoted the target:

1. Quiesce writes.
2. Re-point application/Nginx traffic to the source (still primary, or
   re-promote the source if it was fenced).
3. Re-establish replication source←target if the target took writes, or
   accept data loss on the target and rebuild it as a fresh standby.

This is the same manual backout as the Phase 1 manual cutover. The fence lease
existed only to serialize the cutover; releasing it does not unfreeze the
source (source freeze is deferred — see known-limitations).

---

## 6. Verification commands (manual, on the hosts)

```bash
# source still primary?
sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();'   # expect f
# target standby vs primary?
sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();'   # expect t before / f after promote
# replication healthy?
sudo -u postgres psql -tAc 'SELECT count(*) FROM pg_stat_replication;'  # expect >=1
# last lease state
sqlite3 <repo> 'SELECT * FROM fence_leases WHERE migration_id=:id;'
```

---

## 7. Known limitations (Phase 2A)

- **Source freeze is deferred.** The fence lease serializes the cutover but
  does not set a Postgres read-only GUC on the source. The no-dual-writer
  invariant holds in the happy path; a writer that ignores the lease could
  still write to the source during the switch→promote window. (Hard fence =
  lease; source read-only GUC is defense-in-depth, not yet wired.)
- **Single target, single nginx provider.** No multi-standby pinning, no
  multi-traffic-provider.
- **Cross-major / MySQL / Mongo / Redis** cutover is **not** implemented. Only
  PostgreSQL same-major, one source→one target.
- **Cutover bounded by `FenceTTL - 10s` (~4m50s).** No lease-renewal loop yet;
  a cutover that cannot finish in one lease lifetime fails closed.
- **No automatic backout.** Operator-owned (§5).

See `docs/known-limitations.md` for the authoritative list.
