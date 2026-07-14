# Phase 5 — Workstream E: Testing End-to-End Matrix

**2026-07-14** · Phase 5.

This is the **one genuinely missing deliverable** in Phase 5. The audit (§E) notes
"belum ada live test matrix" — confirmed: the repo has rich **unit/integration**
tests (`database_test.go`, `database_p0_test.go`, `replication_test.go`,
`cutover_p0_test.go`, `caddy_switch_integration_test.go`, `freeze_test.go`,
`observation_test.go`, `concurrency_resource_test.go`) but **no executed live
multi-engine cutover/rollback matrix** with measured results.

Pattern: design from audit → implement → test live → certify. This doc defines the
matrix; the final report records the results.

---

## E1. Matrix — host / container / compose per engine

| Engine | host→host | host→Docker | Compose→host.container | Notes |
|---|---|---|---|---|
| PostgreSQL | ✅ plan | ✅ plan | ✅ plan | physical `pg_basebackup` seed; container adapter (P1-1) needed for Docker targets |
| MySQL | ✅ plan | ✅ plan | ✅ plan | seeded binlog; `Seconds_Behind_Master` lag |
| MongoDB | ✅ plan | ✅ plan (mongosh) | ✅ plan | dump/restore only; **no fenced auto-cutover** (policy) |
| Redis | ✅ plan | ✅ plan | ✅ plan | `REPLICAOF`; **fenced auto-cutover supported** but verify manual-fenced path |

Each cell exercised as: **plan → execute → (cutover manual or fenced-auto) →
observation → commit**, plus a **rollback** variant and a **degraded/needs_manual**
variant.

## E2. Test kinds (per cell)

1. **Unit** (already green): command shapes, error classification, state
   transitions — keep green.
2. **Integration** (Docker Compose pairs): `pgbench`/`sysbench`/`mongorestore`/
   `redis-cli DBSIZE` row/collection counts source == target.
3. **Failure injection**: kill restore mid-flight → assert resume (D/B3) or clean
   manual fallback; kill source during observation → assert `NeedsManualIntervention`
   (stop-on-write guard, `pipeline.go:892`).
4. **Large-file**: dummy 50 GB on a loop device; rsync `--partial` resume after a
   forced kill; sha256 source==dest.
5. **Network interruption**: `tc netem` loss/jitter + drop SSH mid-transfer →
   assert resume (not from-zero).
6. **DB consistency**: table/collection checksum source vs target.
7. **Security**: `ps`/`logs` grep — assert **no password in argv/log**; redaction
   works (credentials redacted in `GET /config` and audit).
8. **E2E**: full plan→execute→cutover→observation→rollback; record BE state
   (`session.state`) + FE state (`currentState`, rollback banner) — proves the
   4F.X/4F.Y sync holds under real flows.

## E3. Evidence capture

- Scripts in `web/../test/phase5/` (or `internal/mod/migration/testdata/phase5/`).
- Results + key logs + UI screenshots (SafetyStatePanel + rollback banner) saved
  under `docs/superpowers/test-evidence/phase-5-*.md`.
- Each matrix cell: PASS/FAIL + measured downtime (the honest "minimal downtime
  realistic" number: seconds for small DBs on LAN, minutes for 50 GB+).

## Acceptable Phase 5 scope

The full matrix across every cell is large. **Phase 5 acceptance** requires at
minimum: one green cell per engine (host→host or host→Docker) for
execute→cutover→commit, plus a rollback cell per engine, plus the 50 GB resume
test, plus the stop-on-write → `NeedsManualIntervention` test. Remaining cells can
be scheduled post-Phase-5 but must be listed as open in the final report.

**Honest claim:** no live matrix has been executed yet. This is the core Phase 5
execution deliverable and the basis for the "minimal downtime realistic" claim.
