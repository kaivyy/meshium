# Phase 4 — Production Expansion & Certification: Final Report

**Date:** 2026-07-14
**Scope:** Make Meshium production-grade for broader use across topology,
traffic provider, WAN/large-scale transfer, advanced database scenarios, and
enterprise policy — without weakening the Phase 1–3 safety contract.
**Method:** investigate → design decision → implement → test (real local Docker
or local rsync) → report. One compatibility axis per increment; one engine-
provider-topology expansion slice at a time; every support claim backed by real
integration evidence; every expansion fails closed on ambiguity; product wording
scoped and honest.
**Immutable baselines preserved:** `AwaitingCutover`, `NeedsManualIntervention`,
`RollbackDegraded`, durable persisted state/checkpoints, transfer checkpoint
resume/reconcile, restart reconciliation, topology verification fail-closed,
stream safety/bounded output, durable fencing authority, lease ownership/
generation validation, active token requirement, source fencing before switch,
target verification before write enable, traffic ownership verification after
switch, no auto-rollback after target writes, structured logging/correlation/
audit, central secret redaction, API/WS idempotency, accurate support status,
"minimal downtime" default wording, no universal zero-downtime claims.

---

## 1. Headline result

| Slice | Axis expanded | Verdict | Evidence |
|---|---|---|---|
| 4A | Topology: Docker Compose (PG) | **certified `automatic`** | `TestPGComposeCutoverLive` |
| 4B | Provider: Caddy | **certified `automatic`** + guardrail tightened | `TestCaddySwitcherLive` + `TestSupportedProviderGuardrail` |
| 4C | WAN/transfer: rsync resume | **proven** (no rsync replacement) | `TestSyncResumeAfterKillLive` |
| 4D | Advanced DB: PG logical pub/sub | **proven `degraded`** (primitives; not orchestrator-wired) | `TestPGLogicalReplicationLive` |
| 4E | Enterprise: policy engine | **shipped** (single guardrail surface) | `TestPolicy*` + `GET /api/pipeline/policy` |
| 4F | Certification | **shipped** (matrix + release matrix + runbooks + this report) | docs + drills |

**Product wording:** "minimal-downtime migration for supported configurations."
No universal zero-downtime claim. Every automatic cell is enumerated in
`phase-4-release-matrix.md`.

---

## 2. What was proven per slice

### 4A — Topology expansion (Docker Compose, PostgreSQL)
- `CertifyTopology` classifies + read-only probes the execution mode (host /
  container / compose / bastion); unknown/uncertified modes fail closed with
  `ErrUnsupportedMode`, no transport probe issued.
- `TestPGComposeCutoverLive` stands up a real primary + standby via `docker
  compose`, certifies the compose mode, then drives the real preflight /
  catch-up / promote primitives through a `composeExecuter` — target promoted to
  primary with seeded data intact, then re-certified. PostgreSQL Compose pair is
  now `automatic` (was `possible`/unverified).

### 4B — Traffic-provider expansion (Caddy)
- Third fenced switcher (`CaddySwitcher`) mirroring nginx/haproxy: idempotent
  full-replacement upload, `caddy validate`, `caddy reload --config … --adapter
  caddyfile`, read-after-write ownership verify, sanitized persistence.
- `TestCaddySwitcherLive` against a real `caddy:2-alpine` container proves the
  config swap + reload + a real GET showing the traffic marker moved source→target.
- **API guardrail tightened (finding A.2-1):** only nginx/haproxy/caddy are
  selectable as automatic switches; traefik/cloudflare/docker/dns are rejected at
  the API boundary (`validateConfigSupport`), not mid-cutover.

### 4C — WAN / large-scale transfer resilience
- `TestSyncResumeAfterKillLive` starts a throttled (`--bwlimit`) `InitialSync`,
  cancels its context to **kill rsync mid-transfer** (partial files remain),
  records the session as errored, then `ResumeSync` re-runs the same resumable
  command (`--partial --append-verify`) to completion; target proven
  **byte-identical** to source by per-file md5 (0 corrupted, 0 lost).
- `TestSyncResumeAfterInterruptionMock` proves the error→resume→`VerifyChecksums`
  state machine.
- **No rsync replacement (finding D-1):** rsync already provides
  `--partial --append-verify`, `--checksum`, `--bwlimit`, `--parallel`; 4C proves
  the contract, it does not replace the tool.

### 4D — Advanced database scenario (PostgreSQL logical replication)
- `PreflightLogicalPG` (same-major, source primary, **wal_level=logical** hard
  gate, target independent primary, replica connectivity — all pre-mutation,
  fail-closed), `SetupLogicalReplicationPG` (idempotent publication/subscription;
  creds inside the DSN, SQL-escaped, never `-p` argv), `WaitForLogicalCatchUpPG`
  (per-table row-count parity, measures lag, never assumes 0).
- `TestPGLogicalReplicationLive` proves a source row replicates to the target and
  parity is reached on two real PostgreSQL 15 primaries.
- **Honest scope:** proven at the primitive level but NOT wired through the
  fenced orchestrator this increment (would risk regressing 4A/4B) → `degraded`,
  not claimed `automatic`.

### 4E — Enterprise operations / policy controls
- `PolicyEngine` (`policy.go`) unifies the four scattered guardrails
  (supportedTrafficProviders / supportedReplicationModes / supportedExecutionModes
  / cutoverEngineType) into ONE surface enforced at BOTH the API boundary
  (`validateConfigSupport`) and execution time (`CheckAutoCutover` in
  `runAutoCutover`, before lease acquire). A config that passes POST cannot reach
  a divergent decision at cutover.
- `GET /api/pipeline/policy` returns the full `PolicyMatrix` (supported engines /
  providers / modes + honest caveats) for operator audit.
- The fenced orchestrator, lease, idempotency, and fail-closed behavior are
  untouched — no regression.

### 4F — Production certification & release gates
- `phase-4-production-certification.md` (per-cell evidence + ship/no-ship gates),
  `phase-4-release-matrix.md` (exact supported combinations + legend),
  `phase-4-runbooks.md` (operator failure-response + cert drills), and this
  report. All committed.

---

## 3. Immutable guards — confirmed present, not regressed

- `AutoCutoverDefault = false`; `AssertHolds` before every mutating step.
- No `ForceTransition(Committed)`; fail-closed on ambiguous topology / fence /
  traffic-ownership.
- Ownership = read-after-write verification, never provider success alone.
- Unsupported engine/provider/topology NOT selectable as automatic.
- Central secret redaction; idempotency keys; audit + correlation IDs.
- No universal "zero downtime" — only "minimal downtime" per proven slice.

---

## 4. Known limitations carried into certification

- Redis: no source freeze → `degraded` (minimal, never zero, downtime); never
  automatic.
- MongoDB: no safe automatic cutover (no replica-set lag) → `blocked` automatic;
  manual verdict only.
- PostgreSQL logical: primitives proven; `degraded` until orchestrator wiring
  lands (follow-on).
- Bastion/jump-host cutover: `blocked` (no live test; `dialBastion` implemented).
- Cross-major cutover: `blocked` for every engine.
- External DNS/Cloudflare providers: `blocked` (no staging credentials here).

---

## 5. Commit chain (Phase 4)

```
a239b98 feat(migration): Phase 4E — server-side policy engine unifying support guardrails
8fc9e9d feat(migration): Phase 4D — PostgreSQL logical replication (pub/sub) primitives + live proof
38cc5f8 feat(migration): Phase 4C — rsync resume/integrity contract proven (killed-process + state-machine)
db4333c feat(migration): Phase 4B — Caddy fenced switcher + tighten provider guardrail
b88e0f9 feat(migration): Phase 4A — execution-mode certification + PostgreSQL Docker-Compose cutover
```

Companion docs: `phase-4-investigation.md`, `phase-4-production-certification.md`,
`phase-4-release-matrix.md`, `phase-4-runbooks.md`. Changelog: `[Unreleased]`
Phase 4 section (4A–4F).
