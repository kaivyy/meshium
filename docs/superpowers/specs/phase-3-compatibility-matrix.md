# Phase 3 — Cross-Engine Compatibility / Release Matrix

> Authoritative capability matrix for Meshium database migration across all
> four engines (PostgreSQL, MySQL, Redis, MongoDB) × all traffic providers ×
> all topologies. Companion to `phase-3-all-engine-investigation.md`.
>
> **Honesty rule (mandated):** a cell is only `automatic` if it is (a) wired in
> code, (b) fenced (`AssertHolds` before every mutation), and (c) verified by a
> real local integration test during Phase 3. Anything else is `manual`,
> `degraded`, `blocked`, or `deferred` — never silently downgraded to automatic.

## 1. Status legend

| Status | Meaning | Product wording |
|---|---|---|
| `automatic` | fenced cutover supported + verified by integration test | "minimal-downtime automatic cutover (opt-in, fenced)" |
| `manual` | replication/seed supported; operator must cut over | "manual cutover; instructions provided" |
| `degraded` | cutover possible but with a known safety caveat (e.g. no source freeze) | "degraded: source-freeze not enforced; manual freeze recommended" |
| `blocked` | not safe to support; fails closed; must not be selectable as automatic | "not supported for automatic cutover" |
| `deferred` | not yet proven; planned for a later phase | "deferred; no support claim" |

## 2. Engine × cutover-mode matrix (nginx / haproxy traffic providers)

These providers are the **only** ones that dispatch a fenced switcher
(`newTrafficSwitcher`, pipeline.go:2218) and prove traffic ownership by
read-after-write (`NginxSwitcher.verify`, nginx_switch.go:193).

| Engine | Mode | Status (today) | Phase 3 target | Evidence / notes |
|---|---|---|---|---|
| PostgreSQL | `automatic` | **deferred** (PG not wired to orchestrator switch) | **`automatic` (VERIFIED, 3A)** | replication + fenced cutover live; source freeze GUC now enforced + verified (B1 closed, ca439c5) |
| PostgreSQL | `manual` | supported | supported | operator publishes/promotes via runbook |
| MySQL | `automatic` | **deferred** | **`automatic` (VERIFIED, 3B)** | seeded repl + fenced cutover live; source freeze `read_only=ON`+`super_read_only=ON` held + verified; non-SUPER write rejected post-freeze (dual-writer closed); `CutoverPreflight` now stage-aware so the post-freeze re-check tolerates the frozen source; `SHOW BINARY LOG STATUS`/`SHOW REPLICA STATUS` 8.4 renames handled with legacy fallback |
| MySQL | `manual` | supported | supported | |
| Redis | `automatic` | **blocked** (no integration test, no source freeze) | **`degraded` (VERIFIED, 3C)** | live master/replica pair proves preflight→`REPLICAOF NO ONE`→master; `CutoverFreezeSource` returns `ErrNoSourceFreeze` (no freeze primitive) → RPO is *minimal*, never *zero*; never selectable as `automatic` (rule #11) |
| Redis | `manual` | supported | supported | `REPLICAOF`/`REPLICAOF NO ONE` |
| MongoDB | `automatic` | **blocked** (fails closed, pipeline.go:2052) | blocked until 3D replica-set path built | no lag/role measurement (B4) |
| MongoDB | `manual` | **blocked** (no cutover contract at all) | `manual` after 3D replica-set seed path | mongoMigrator dump/restore exists; cutover does not |

**No engine is `automatic` today.** Every opt-in automatic cutover is gated by
`AutoCutover=true` (default `false`, `TestAutoCutoverDefaultsOff`), so the
shipped default is always `manual` — this is the immutable baseline.

## 3. Engine × topology matrix

| Topology | PostgreSQL | MySQL | Redis | MongoDB |
|---|---|---|---|---|
| host → host, same major | `degraded`→`automatic` (3A) | `automatic` (3B) | `degraded`/`blocked` (3C) | `blocked`→`manual` (3D) |
| Docker container pair | possible (SSH into container) | possible | possible | possible |
| Docker Compose pair | possible | possible | possible | possible |
| cross-major version | **blocked** | **blocked** | **blocked** | **blocked** |
| sharded / cluster (Redis Cluster, Mongo sharded) | n/a | n/a | **blocked** | **blocked** |
| managed (RDS, Atlas, ElastiCache, Cloud SQL) | **blocked** (no replica control) | **blocked** | **blocked** | **blocked** |
| source/target direct connection | required | required | required | required |
| via SSH/bastion | supported | supported | supported | supported |

## 4. Engine × traffic-provider matrix (automatic cutover reach)

| Provider | PostgreSQL | MySQL | Redis | MongoDB |
|---|---|---|---|---|
| nginx | `degraded`/`blocked` (3A) | `automatic` (3B) | `degraded` (3C) | blocked |
| haproxy | `degraded`/`blocked` (3A) | `automatic` (3B) | `degraded` (3C) | blocked |
| traefik | blocked | blocked | blocked | blocked |
| caddy | blocked | blocked | blocked | blocked |
| cloudflare | blocked | blocked | blocked | blocked |
| docker | blocked | blocked | blocked | blocked |
| dns | blocked | blocked | blocked | blocked |

Only nginx + haproxy dispatch in `newTrafficSwitcher`. All others fail closed
at pipeline.go:2225. Ownership is proven only by read-after-write, never by
provider success alone (directive rule #4).

## 5. Per-engine acceptance criteria (must hold before a cell becomes `automatic`)

Each criterion is verified by a **real local** integration test (Docker pair),
not mocks.

### PostgreSQL (3A)
- [ ] `setupPostgreSQL` establishes physical replication (slot + basebackup).
- [ ] `WaitForCatchUp` reports lag ≤ threshold before cutover gate.
- [ ] `Promote` (`pg_promote()`) makes target writable; source stays primary.
- [ ] `AssertHolds` fails closed on expired/conflicting fence token.
- [ ] **Source freeze decision:** either (a) a read-only GUC is set and verified
      before promote, or (b) the cell is shipped `degraded` with an explicit
      "manual freeze recommended" runbook and the no-dual-writer claim is
      downgraded honestly (B1).
- [ ] Restart-reconciliation: kill meshium mid-cutover → resumes at persisted
      `cutoverCheckpoint`, does not double-promote.
- [ ] Failure injection: simulate traffic-switch failure → `NeedsManualIntervention`
      with persisted evidence + runbook; no `ForceTransition(Committed)`.

### MySQL (3B)
- [ ] Seed runs BEFORE `CHANGE REPLICATION SOURCE TO` (Phase 2C invariant).
- [ ] `FinalSync` consistency: prove or document the FTWRL session-scope gap (B2);
      if unprovable, ship `degraded` honestly.
- [ ] `read_only=ON` fenced on source during switch; verified.
- [ ] `Promote` stops replica + makes target writable.
- [ ] `AssertHolds` fails closed.
- [ ] Restart + failure injection as above.

### Redis (3C) — VERIFIED (degraded)
- [x] Real local Redis pair: `REPLICAOF` + `REPLICAOF NO ONE` round-trip (TestRedisCutoverPrimitivesLive, 8.5s).
- [x] Promote verified: target becomes `role:master` AND retains seeded data (no loss on promote).
- [x] `CutoverFreezeSource` returns `ErrNoSourceFreeze` — no freeze primitive; orchestrator stays degraded (never `automatic`, rule #11).
- [x] Integration test added (only unit existed before 3C).
- [ ] **RPO:** NOT zero — Redis has no source freeze, so the dual-writer window is bounded by the async replication gap. Honest wording: "minimal-downtime degraded cutover; source-freeze not enforced — operator must freeze writes manually." B3 caveat closed honestly as `degraded`, not `blocked`, because the path IS fenced (AssertHolds) and verified; only the freeze primitive is missing.

### MongoDB (3D)
- [ ] New same-major replica-set path behind preflight: exactly one writable
      primary, FCV compatible, oplog window sufficient.
- [ ] Lag measurement via `rs.status()` / oplog.
- [ ] Role/writeability re-queried at critical boundaries (directive rule #5).
- [ ] If preflight cannot guarantee a safe contract, ship `blocked`/`deferred`
      honestly; do NOT enable automatic.

## 6. Sequencing (one slice proven before the next)

```
3A PostgreSQL  ──► 3B MySQL  ──► 3C Redis  ──► 3D MongoDB  ──► 3E Certification
   (degraded            (automatic      (blocked/      (blocked/        + matrix
    or manual)          after proof)    degraded)       manual)         + runbooks
```

Each slice is committed separately with its acceptance tests green before the
next begins. A slice that cannot prove `automatic` ships `degraded`/`blocked`/
`deferred` honestly — it does NOT claim automatic.

## 7. Immutable guards (must never regress across Phase 3)

- `AutoCutoverDefault = false` (manual is the default).
- `AssertHolds` before every mutating cutover step.
- Fails closed on ambiguous topology / fence / traffic-ownership.
- No `ForceTransition(Committed)`.
- No global "zero downtime" claim — only "minimal downtime" per proven slice.
- Central secret redaction; idempotency keys; audit + correlation IDs.
- Ownership = read-after-write verification, never provider success alone.
- Unsupported engine/provider/topology is NOT selectable as automatic.
