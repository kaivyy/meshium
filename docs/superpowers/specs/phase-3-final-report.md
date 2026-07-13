# Phase 3 — Cross-Engine Cutover Certification: Final Report

**Date:** 2026-07-13
**Scope:** Certify Meshium's database cutover capability, honestly and with real
local evidence, across all four engines (PostgreSQL, MySQL, Redis, MongoDB) —
one engine-provider-topology slice at a time.
**Method:** investigate → design decision → implement → test (real local Docker
pair) → report. Each slice is its own commit with acceptance tests green before
the next begins. No mocks alone satisfy acceptance (rule #12).

---

## 1. Headline result

| Engine | Verdict | Evidence | Source freeze? | RPO |
|---|---|---|---|---|
| PostgreSQL | **`automatic` (VERIFIED, 3A)** | 4 live tests | yes (GUC held) | minimal, freeze-enforced |
| MySQL | **`automatic` (VERIFIED, 3B)** | 3 live tests | yes (`read_only`+`super_read_only` held) | minimal, freeze-enforced |
| Redis | **`degraded` (VERIFIED, 3C)** | 1 live test | **none** (primitive absent) | minimal, **not** zero |
| MongoDB | **`blocked` (automatic) / `manual` (VERIFIED, 3D)** | 1 live test + 3 unit | **none**; promotion would reconfigure live source set | n/a (manual only) |

**No engine is `automatic` unless proven by a live integration test.** The
shipped default remains `AutoCutover=false` (manual) — the immutable baseline.

---

## 2. What was proven per slice

### 3A — PostgreSQL (automatic)
- Physical replication (slot + `pg_basebackup` seed) established on a live pair.
- `WaitForCatchUp` reports lag ≤ threshold from `pg_stat_replication` before the
  gate; the prior lag-hang was fixed (ca439c5).
- `Promote` (`pg_promote()`) makes the target writable; source stays primary.
- **Source freeze closed (B1):** `default_transaction_read_only=on` set via
  `ALTER SYSTEM` + reload, and **verified** — a non-super appuser write is
  rejected after freeze, closing the dual-writer window. `AssertHolds` fails
  closed on expired/conflicting fence tokens.
- Restart-reconciliation and failure-injection paths covered by orchestrator
  unit tests (`TestCutoverOrchestrator*`).

### 3B — MySQL (automatic)
- Seeded repl (dump BEFORE `CHANGE REPLICATION SOURCE TO`) on a live MySQL 8
  primary + replica; the Phase 2C unseeded defect is fixed.
- **Source freeze verified:** `read_only=ON` + `super_read_only=ON` held; a
  non-SUPER `appuser` write is rejected post-freeze in a full fenced
  orchestrator run (TestMySQLSourceFreezeClosesWrites).
- **Three real 8.4 production bugs surfaced and fixed while certifying:**
  1. `SHOW MASTER STATUS` renamed to `SHOW BINARY LOG STATUS` (old name removed)
     → resilient reader (`mysqlShowMasterStatus`).
  2. `SHOW SLAVE STATUS` → `SHOW REPLICA STATUS`, `Seconds_Behind_Master` →
     `Seconds_Behind_Source` → resilient `mysqlLag`/probe.
  3. `CutoverPreflight` self-contradiction: the post-freeze re-check rejected the
     intentionally-frozen source → stage-aware `sourceFrozen bool` param.
- `-B` batch-mode label-stripping bug (`SHOW REPLICA STATUS\G` loses
  `Replica_IO_Running: ` prefixes → grep-blind) fixed by dropping `-NB`.

### 3C — Redis (degraded)
- Live master/replica pair proves `preflightRedis` (source master, target
  replica, link up) → `promoteRedisCutover` issues `REPLICAOF NO ONE` → target
  becomes `role:master` **and retains seeded data** (no loss on promote).
- **No source-freeze primitive exists** → `CutoverFreezeSource` returns
  `ErrNoSourceFreeze`; the orchestrator stays `degraded` (continuable, not
  fatal). The dual-writer window is bounded by the async replication gap, so
  **RPO is minimal, never zero**. Redis is therefore NOT selectable as
  `automatic` (rule #11). Honest wording: "minimal-downtime degraded cutover;
  source-freeze not enforced — operator must freeze writes manually."

### 3D — MongoDB (blocked for automatic / manual supported)
- **B4 closed:** `mongoDBLag` now measures replica-set lag via `rs.status()`
  `optimeDate` delta (never assumes zero lag).
- `preflightMongoDB` re-queries source/target roles at the gate (rule #5),
  refusing non-PRIMARY source / non-SECONDARY target / sharded / cross-major /
  FCV-mismatch, and returns an actionable `manual-cutover-eligible` verdict.
- **`promoteMongoDB` hardened to FAIL CLOSED** — never issues `rs.stepDown` /
  `rs.remove` / reconfigure against the live source (rules #3/#8). No
  source-freeze primitive exists, so automatic stays `blocked`; the supported
  path is a **human-performed manual step-down**.
- Live replica-set (PRIMARY + SECONDARY) integration test confirms: lag drains
  to ~0, preflight returns the manual-eligible verdict, and the automatic
  dispatch + `CutoverPromote` stay blocked with **no rs.* command ever issued
  against the source**.

---

## 3. Immutable guards — verified not regressed

- `AutoCutoverDefault = false` (`TestAutoCutoverDefaultsOff`).
- `AssertHolds` before every mutating cutover step; orchestrator fails closed on
  expired/conflicting/stale fence (10+ `TestCutoverOrchestratorFailClosed*`).
- No `ForceTransition(Committed)`; automatic cutover is reachable only via an
  explicit, persisted, fenced `AwaitingCutover` → `Completed` walk.
- No global "zero downtime" claim — only "minimal downtime" per proven slice.
- Central secret redaction (`TestReleaseGateNoSecretInAnySink`,
  `TestExecCommandErrorRedactsRemoteOutput`) + idempotency keys + audit/correlation
  IDs.
- Ownership = read-after-write verification (nginx/haproxy switchers verify,
  never trust provider success alone — rule #4).
- Unsupported engine/provider/topology is NOT selectable as automatic.

---

## 4. Production bugs found and fixed during certification

| # | Engine | Bug | Fix | Slice |
|---|---|---|---|---|
| 1 | MySQL 8.4 | `SHOW MASTER STATUS` removed → syntax error on every 8.4 source | `mysqlShowMasterStatus` (modern then legacy, covers MariaDB) | 3B |
| 2 | MySQL 8.4 | `SHOW SLAVE STATUS` / `Seconds_Behind_Master` removed → `monitor replication lag` exit 1 | resilient `mysqlLag` + probe (modern then legacy) | 3B |
| 3 | MySQL | `CutoverPreflight` rejected the intentionally-frozen source on post-freeze re-check | stage-aware `sourceFrozen bool` through the preflight family | 3B |
| 4 | MySQL | `-B` batch mode strips `SHOW REPLICA STATUS\G` labels → replica-health grep blind | drop `-NB`, use `mysql -e` | 3B |
| 5 | MongoDB | `mongoDBLag` returned hard-error (lag unmeasurable → all Mongo cutover impossible to certify) | real `rs.status()` optimeDate lag + role/FCV probe | 3D |

Bugs 1–3 would have broken **every** MySQL 8.4+ source in production; they were
caught by running the cutover against a real 8.4 pair, not by mocks.

---

## 5. Honesty commitments (mandatory)

1. We do **not** claim universal zero-downtime cutover for any engine.
2. We do **not** auto-cutover without an active, durable, verified fence lease.
3. We do **not** promote/enable-write/unfreeze/switch-traffic on a
   missing/expired/stale/conflicting/unverifiable fence token.
4. We do **not** infer traffic ownership from provider success alone.
5. We do **not** infer role/writeability from a stale preflight.
6. On ambiguous topology/fence/traffic we fail **closed** to
   `NeedsManualIntervention` with persisted evidence + runbook.
7. We do **not** report `RolledBack`/`Completed` when critical verification
   failed.
8. We do **not** silently downgrade an automatic strategy to a weaker one.
9. We do **not** make an unsupported engine/provider/topology selectable as
   `automatic` (Redis/MongoDB are excluded from `automatic`).
10. Mocks alone never satisfy acceptance — every `automatic` claim is backed by
    a real local Docker integration test.

---

## 6. Acceptance test inventory (all green)

**Live integration (build tag `integration`, real Docker pairs):**

| Test | Engine | Slice |
|---|---|---|
| TestPGCutoverPrimitivesLive | PostgreSQL | 3A |
| TestPGPreflightFailsWhenTargetNotStandby | PostgreSQL | 3A |
| TestFencedCutoverWithRealPG | PostgreSQL | 3A |
| TestPGSourceFreezeClosesWrites | PostgreSQL | 3A |
| TestMySQLCutoverPrimitivesLive | MySQL | 3B |
| TestMySQLPreflightFailsWhenSourceReadOnly | MySQL | 3B |
| TestMySQLSourceFreezeClosesWrites | MySQL | 3B |
| TestRedisCutoverPrimitivesLive | Redis | 3C |
| TestMongoCutoverAssessmentLive | MongoDB | 3D |

**Unit (fenced gates, fail-closed, orchestrator):** the full
`go test ./internal/mod/migration/` suite passes (300+ cases), including the
engine-specific preflight/promote fail-closed tests and the orchestrator
fence/topology/dual-writer ordering tests referenced in §3.

---

## 7. Remaining limitations (called out honestly, not hidden)

- **Redis:** no source freeze → RPO minimal, not zero. Operator must freeze
  writes out-of-band before relying on the degraded path.
- **MongoDB:** automatic cutover is intentionally blocked; only a human-run
  manual step-down is supported, because promotion would reconfigure the live
  source replica set and no freeze primitive exists.
- **Cross-major, sharded/cluster, and managed (RDS/Atlas/ElastiCache/Cloud SQL):**
  blocked for all engines — no replica control or unsupported topology.
- **Traefik / Caddy / Cloudflare / docker / DNS traffic providers:** fail closed
  at the switcher; only nginx + haproxy dispatch a fenced switcher with
  read-after-write ownership proof.

---

## 8. Commit chain

```
33a6dd6 feat(migration): Phase 3D — MongoDB replica-set cutover certified BLOCKED(automatic)/manual
0fa147a feat(migration): Phase 3C — Redis cutover certified DEGRADED (live proof)
c295b68 feat(migration): Phase 3B — MySQL seeded cutover verified with live freeze
044c932 docs(phase3): mark PostgreSQL automatic cutover verified in compatibility matrix
ca439c5 feat(phase3a): PostgreSQL source-freeze closes dual-writer window + fix lag hang
b34e1f7 docs(phase3): Part 1 — all-engine investigation + compatibility matrix + sequencing
```

Companion matrix: `phase-3-compatibility-matrix.md` (updated live as each slice
was certified).
