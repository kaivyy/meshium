# Phase 2A — Minimal-Downtime PostgreSQL Migration (Investigation + Design)

> **Status:** Investigation complete; design decision locked. This document is
> the Phase 2A contract. Phase 1 is an **immutable baseline** — no Phase 1
> invariant may regress. Do not claim "zero-downtime" until the full Phase 2A
> acceptance suite passes; use **"minimal downtime"** until then.
>
> **For agentic workers:** implementation proceeds commit-by-commit per the
> structure in §9. Stop and report (do NOT continue) on any stop condition in §10.

## 1. Investigation summary

Inventory of existing Phase 2 infrastructure (verified by reading source +
two parallel Explore agents; all file:line confirmed against current HEAD):

### Dead code / stubs (zero production callers)
- **`CutoverEngine`** (`cutover.go:13-22`, `Execute` `:70-187`): fully
  implemented 9-step cutover (Freeze→FinalSync→WaitForCatchUp→DrainQueues→
  HealthVerify→TrafficSwitch→Promote→ResumeQueues→Verify). **Never instantiated**
  outside tests. Comment at `pipeline.go:1751` states it has no live caller.
- **`FreezeManager`** (`freeze.go`): implemented, **never called** in prod.
  PostgreSQL freeze = `ALTER SYSTEM SET default_transaction_read_only=on`
  (`freeze.go:146`) — **best-effort, NOT a hard fence** (a session can
  override). `FreezeResult` is an in-memory struct field (`cutover.go:20`),
  never persisted.
- **`TrafficSwitchEngine`** (`traffic.go`): 7 providers implemented
  (Cloudflare/Nginx/Traefik/HAProxy/Caddy/Docker/DNS), **never called** by the
  pipeline. `Verify` (`:121-142`) is a generic HTTP health check, **not a
  read-after-write that proves traffic ownership**. No idempotency key in the
  engine.
- **`ObservationEngine`** (`observation.go`): threshold-based auto-rollback
  implemented, **never called**. Pipeline's `postCutoverObservationStage`
  (`pipeline.go:1857`) runs an inline `echo ok` loop instead. **Write-detection
  on target does not exist anywhere.**

### Live wiring
- **`liveReplicationStage`** (`pipeline.go:1542`) is the **only** stage
  calling a real engine: `ReplicationEngine.SetupReplication` +
  `MonitorLag` + `WaitForCatchUp` (`:1596-1640`).
- **PostgreSQL replication** (`replication.go:348-407`): creates `replicator`
  user (REPLICATION), physical slot `meshium_slot`, `wal_level=replica`,
  `pg_hba.conf` entry, then **`pg_basebackup`** (`:386`) + `primary_conninfo`.
  This is **physical replication, same-major-version**. No explicit version
  gate today (pg_basebackup across majors fails at the PG level, not guarded).
- **Replication rollback = fully disabled (P0-1):** MySQL/PG/Mongo return
  `ErrUnsafeTopology`; Redis re-points only if `safeForRepoint()`.
- **`trafficSwitchStage`** (`pipeline.go:1756`) always records
  `manual_required` and returns `ErrAwaitingCutover` — no automatic switch.

### State machine
- 30 states, full `transitionTable` at `state.go:259-303`. Key Phase 1
  states: `StateAwaitingCutover` (28, non-terminal, edges to
  Committed/NeedsManualIntervention/Rollback/Failed), `StateNeedsManualIntervention`
  (29, terminal, no auto-exit). `IsTerminal` = Committed/RolledBack/
  RollbackDegraded/Cancelled/NeedsManualIntervention.
- 7 `ForceTransition` callers, all audited (failure/rollback-entry only,
  never Committed). **No `ForceTransition(Committed)` exists.**

### Persistence schema (`internal/db/migrations.go`)
- Tables present: `migration_stages` (live checkpoints, `:256`),
  `traffic_switch_config` (`:288`), `cutover_history` (`:317`),
  `migration_rollback_steps` (`:217`), `migration_freezes` (`:238`,
  **dead — never written**), `audit_trail` (`:420`), `replication_status`
  (`:272`).
- **No lease/fence/token/generation table or column exists anywhere.**
  Concurrency safety is a single in-process `map[int]bool` lock
  (`pipeline.go:967`). Two processes could run one migration concurrently.

### Commit / cutover endpoints (`pipeline_handler.go`)
- `POST …/actions/commit` → `Pipeline.Commit`. From `AwaitingCutover`:
  requires `switch_state != "manual_required"` or returns
  `409 cutover_notConfirmed` (`:471`).
- `GET …/traffic` only — **no HTTP endpoint flips `switch_state`**. Today
  every pipeline-reached AwaitingCutover commit is a permanent dead-end.
  `UpdateTrafficSwitchState`/`UpdateTrafficSwitchConfig` exist but are only
  called by the uncalled `TrafficSwitchEngine`.

### Phase 1 invariants confirmed present
`StateAwaitingCutover`, `StateNeedsManualIntervention`, `rollbackTerminalState`
(`topology.go:30`), `ErrUnsafeTopology`/`ErrAwaitingCutover`/`ErrCutoverNotConfirmed`
(`topology.go`), `boundedWriter`/`ErrOutputLimitExceeded` (`ssh/boundedio.go`),
7 audited ForceTransition callers. **These must not regress.**

### Environment for live tests
- Docker daemon live; `postgres:16` image accessible from registry.
- **No `psql`/`pg_basebackup`/`initdb` on the host** — use a postgres:16
  container pair (server bins live inside the container). Live PG
  replication + nginx traffic switch verifiable via local containers.

## 2. Phase 2A slice decision

**Locked:**
- **Database:** PostgreSQL **same-major-version only** (physical replication
  via the existing `pg_basebackup` path).
- **Replication mode:** **physical replication** (existing `setupPostgreSQL`
  is already usable + wired; logical replication would be net-new and
  unverified). One mode only.
- **Topology:** local **Docker container pair** (two postgres:16 containers +
  one nginx container), host-to-host over the docker bridge network.
- **Traffic provider:** **Nginx reverse-proxy** (extend existing
  `switchNginx`/`rollbackNginx`). Verifiable end-to-end with distinct
  source/target health endpoints. No external creds.
- **Fencing:** **durable fencing authority implemented before any auto-switch.**

**Rationale vs alternatives:**
- Physical > logical: existing code, wired, real lag slot; logical is net-new.
- Nginx > Docker-compose: docker provider restarts the target service (not a
  traffic shift) — weak ownership proof. Nginx config flip is a real,
  reversible traffic move with a verifiable health endpoint.
- Nginx > Cloudflare: no external network/creds; local-container-verifiable.

## 3. Compatibility matrix

| Capability | Phase 2A | Status |
|---|---|---|
| PostgreSQL same-major physical replication | supported | implemented + tested |
| PostgreSQL cross-major (e.g. 15→16) | **blocked** | preflight rejects; needs pg_upgrade (Phase 2B) |
| MySQL/MariaDB automatic cutover | **deferred** | rollback remains fail-closed (P0-1) |
| MongoDB automatic cutover | **deferred** | replication unimplemented (`setupMongoDB` errors) |
| Redis automatic cutover | **deferred** | manual cutover only; rollback re-point only if `safeForRepoint` |
| Nginx reverse-proxy traffic switch | supported | extended + tested |
| Cloudflare/Traefik/HAProxy/Caddy/Docker/DNS providers | **deferred** | not wired this increment |
| Byte-level / mid-transfer resume (rsync) | **deferred** | Phase 2B |
| Cross-engine conversion | **deferred** | never |

## 4. Fencing design

A **durable fencing authority** (`fencing_authority.go` + `migration_fences`
extension), not in-memory.

### Lease record (persisted)
New table `migration_fence_leases` (added via a migration in
`internal/db/migrations.go`):
```
id INTEGER PK AUTOINCREMENT,
migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
holder TEXT NOT NULL,                 -- agent/process instance id (uuid)
fence_token INTEGER NOT NULL,        -- monotonic generation counter
state TEXT NOT NULL,                 -- current cutover sub-state
acquired_at DATETIME NOT NULL,
expires_at DATETIME NOT NULL,
renewed_at DATETIME,
released_at DATETIME,
UNIQUE(migration_id)                 -- one active lease per migration
```

### Authority semantics (fail-closed throughout)
- **Acquire:** INSERT a row with `holder`, `fence_token` (max+1), `state`,
  `expires_at = now + TTL`. If a row exists with a non-expired, non-released
  lease held by another holder → conflict → **NeedsManualIntervention**.
- **Renew:** UPDATE `renewed_at`/`expires_at` only if `holder` + `fence_token`
  match AND `expires_at > now`. Renewal failure (expired/owner mismatch/conflict)
  → **NeedsManualIntervention**.
- **Release:** UPDATE `released_at` if holder+token match. Release is best-effort
  cleanup; does NOT auto-unfreeze source (unfreeze is an explicit fenced step).
- **Crash recovery:** on startup / before any mutating action, the orchestrator
  loads the lease. If `expires_at <= now` and not released → the lease is stale;
  any pending mutating action (freeze/promote/switch/unfreeze/rollback) is
  rejected → **NeedsManualIntervention** with evidence (the stale lease row).
  Re-acquire requires an explicit new token.
- **Token check before every mutation:** `freeze`, `promoteTarget`,
  `demoteSource`, `switchTraffic`, `unfreezeSource`, `rollbackMutation` each
  call `authority.AssertHolds(ctx, migrationID, token, expectedState)`. Missing /
  expired / conflicting / stale token → **NeedsManualIntervention**, no mutation.

### Hard fence for PostgreSQL
Replace the best-effort `default_transaction_read_only=on` with a hard fence:
**superuser `ALTER SYSTEM SET` is insufficient** (sessions can override). Phase 2A
uses **`pg_is_in_recovery()`-based promotion** as the real fence: the source is
kept writable, the target is a standby (read-only by PG, enforced by the server
itself — a standby **cannot** accept writes). The "freeze" step additionally sets
`default_transaction_read_only=on` on the source as defense-in-depth + records
it, but the **source of truth for "no dual-writer" is the standby role**, not the
GUC. Promotion flips the target to primary; **the source is NOT demoted
automatically** — it stays primary but fenced (read-only GUC) until an operator
confirms. This guarantees: **at no point does the test prove two writable
primaries.**

## 5. Persisted cutover flow

New Phase 2A sub-states (persisted, idempotent/reconcilable). These are
**checkpoint sub-states stored on the fence lease + stage checkpoint**, not new
top-level `MigrationState` enum values (to avoid churning the Phase 1 state
machine). The top-level state during the fenced cutover is `StateTrafficSwitch`
(existing). The sub-states drive the stage's internal step machine:

```
Preflight → Seed → Replicating → Verifying → AwaitingCutover
→ FencingSource → CatchingUp → VerifyingTarget → SwitchingTraffic
→ PromotingTarget → Observing → Completed
```

Rules (each must hold before advancing; persist checkpoint/topology/fence-token/
traffic-result BEFORE state advance):
1. **Preflight** — PG version match (same major), connectivity, disk capacity,
   WAL/slot prerequisites, `replicator` creds, source writable + not in
   recovery, target reachable. Unsupported → actionable error, no mutation.
2. **Seed** — `pg_basebackup` (existing path). Persist slot name + LSN.
3. **Replicating** — standby streaming; persist `replication_status`.
4. **Verifying** — lag polling reaches a configured threshold; persist last LSN.
5. **AwaitingCutover** — operator confirms ready (existing gate; stays manual
   until the fenced auto-path is explicitly enabled per-migration via config flag
   `autoCutover`, default **off**).
6. **FencingSource** — acquire fence lease; set source read-only GUC
   (defense-in-depth); persist lease token + freeze result.
7. **CatchingUp** — wait lag → 0 (bounded); persist final LSN.
8. **VerifyingTarget** — target health + standby role verified; persist.
9. **SwitchingTraffic** — `AssertHolds`; run Nginx switch (idempotent); persist
   sanitized before/after provider config + result + idempotency key.
10. **PromotingTarget** — `AssertHolds`; `pg_promote()` target; persist new role.
11. **Observing** — health + write-detection on target; persist results.
    Write detected on target during observation → **NeedsManualIntervention**
    (no auto-rollback; target may have accepted writes).
12. **Completed** — release lease; persist `completed_at`.

**Restart reconciliation:** each sub-state is idempotent. On backend restart
mid-cutover, the orchestrator loads the lease + last sub-state from the stage
checkpoint and re-enters at that step. A step that already completed (verified by
persisted result) is skipped. If the lease is stale/expired → NeedsManualIntervention.

**No-dual-writer invariant:** source is fenced (read-only GUC + lease) before
target promotion; promotion makes target primary while source is read-only. If
traffic switched but post-switch verify fails → **NeedsManualIntervention**
(rollback is NOT assumed safe — target may have served writes). No automatic
rollback when topology or ownership is ambiguous.

## 6. PostgreSQL Phase 2A specifics
- Reuse `setupPostgreSQL` for seed/slot; add a **preflight** that enforces
  same-major (`SHOW server_version_num` on both, compare major), `pg_is_in_recovery()`
  on source (must be false) and target (must be true after seed), disk capacity
  check, WAL/ slot prerequisites, `replicator` connectivity. All failures are
  **pre-mutation** actionable errors.
- Lag verification: `pg_stat_replication` on source → `flush_lag`/`replay_lag`
  ≤ threshold (default 1s) with a bounded wait.
- Promotion: `pg_promote()` on target (not `pg_ctl promote` shell — use SQL when
  reachable; SSH fallback only if SQL path unavailable).
- RPO/RTO boundary stated honestly: **RPO ≈ 0** once lag=0 + source fenced;
  **RTO = freeze + catch-up + switch + promote** (seconds on a local pair; not a
  guaranteed bound under load — "minimal downtime", not "zero").

## 7. Traffic switching Phase 2A specifics (Nginx)
- Extend `switchNginx`: SSH to the nginx host, swap upstream to target, `nginx -t`,
  `nginx -s reload`. **Add an idempotency key** (stored on `traffic_switch_config`):
  a second switch with the same key is a no-op (return cached result).
- **Timeout + retry:** bounded switch timeout (default 30s); one retry on
  reload failure; on total failure → NeedsManualIntervention.
- **Post-switch read-after-write verification (the ownership proof):** after
  reload, issue a request to the nginx health endpoint that the **target serves
  with a distinct marker** (e.g. a `X-Meshium-Target: target` header or a
  target-specific health body). The verify step reads that marker; if it
  reflects the source (or absent) → **verification failed → NeedsManualIntervention**
  (traffic did NOT move; do NOT assume rollback safe). This replaces the generic
  health-check `Verify`.
- **Reconciliation after restart:** `traffic_switch_config.switch_state` +
  idempotency key + last result persisted; on restart the step re-reads and
  reconciles (no duplicate switch).
- **Sanitization:** provider request/result stored with secrets redacted
  (reuse `shared.SanitizeJSONRawMessage`); nginx config stored minus any
  credentialed upstream lines.
- **Automatic switch only with:** valid active fence token + target verification
  success + `autoCutover` config flag on. Otherwise stays at AwaitingCutover.

## 8. Required tests

### Unit
- Lease acquire/renew/release/expiry/stale-token/conflict (table-driven).
- All state sub-transitions legal/illegal.
- Fenced mutation rejects missing/expired/conflicting/stale token.
- Idempotency + restart reconciliation (re-enter each sub-state).
- PostgreSQL preflight compatibility (same-major pass, cross-major block,
  source-in-recovery block, target-not-standby block).
- Nginx provider command/request behavior + sanitization + idempotency key.

### Integration / failure-injection (live container pair)
- PG source→target seed + replication lag reaches target (real `pg_stat_replication`).
- Backend restart at every Phase 2A cutover sub-state → reconciles safely.
- Source unreachable / target unreachable / both writable / unexpected role.
- Fencing authority unavailable (DB locked / error) → NeedsManualIntervention.
- Lease expiry + renewal failure → NeedsManualIntervention.
- Stale token attempting mutation → rejected → NeedsManualIntervention.
- Network partition around freeze/switch/promote → NeedsManualIntervention.
- Traffic switch success but post-switch verification failure → NeedsManualIntervention.
- Traffic partial failure / timeout → NeedsManualIntervention.
- Write detected on target during observation → NeedsManualIntervention.
- Rollback attempt after target write → blocked, NeedsManualIntervention.
- Data integrity check after successful migration (row counts / checksum).

### Acceptance gate (no ship without all green)
- No tested failure path creates two writable PG primaries.
- No mutation executes with missing/expired/conflicting/stale fence token.
- Pipeline resumes/reconciles after restart in every persisted sub-state.
- Traffic switched automatically only after source fencing + lag/health verify +
  valid lease + post-switch verify.
- Ambiguity always → NeedsManualIntervention with evidence + operator runbook.
- Integration tests use **real** PG replication + nginx (mocks insufficient).
- `go build ./... && go vet ./... && go test ./...` pass throughout.

## 9. Commit structure (small per-concern commits)
1. Investigation report + Phase 2A decision + compatibility matrix (this doc).
2. Durable fencing/lease persistence (`migration_fence_leases` + authority) + tests.
3. State sub-machine + persisted checkpoint/restart reconciliation + tests.
4. PostgreSQL physical replication: preflight (version/role/disk/creds), seed,
   setup, lag verification (reuse `setupPostgreSQL`, add preflight + gates).
5. Nginx traffic-switch provider: idempotent switch + post-switch read-after-write
   verification + sanitization.
6. Fenced cutover orchestration: freeze → catch-up → switch → promote → observe,
   gated on fence token + `autoCutover`.
7. Failure-injection / integration tests + operator runbook + known-limitations
   update + release matrix.

## 10. Stop conditions (stop + report before further implementation)
- No durable fencing authority implementable safely in current deployment model.
- Chosen PG replication mode cannot demonstrate end-to-end seed/replicate/cutover.
- Traffic ownership cannot be verified after a provider switch.
- Any path could promote/unfreeze two writers.
- Any Phase 1 test regresses (run `go test ./...` after every commit).
