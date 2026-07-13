# Phase 4 — Production Certification

> PART 6 of Phase 4 (production expansion & certification). This is the
> authoritative certification record: every support claim made by Meshium, the
> evidence that backs it, and the ship / no-ship gates that must pass before a
> release is tagged. It supersedes the Phase 3 matrix for the cells Phase 4
> expanded. Companion to `phase-4-release-matrix.md`, `phase-4-runbooks.md`, and
> `phase-4-final-report.md`.

## 0. Certification contract (immutable)

A capability cell is marked `automatic` ONLY when all of the following hold,
proven by a **real local integration test** (Docker pair) during Phase 4 — never
by a mock, never by an adapter existing:

1. Wired in code (the real cutover/replication/sync primitive is driven).
2. Fenced: `AssertHolds` runs before every mutating step; no
   `ForceTransition(Committed)`; fail-closed on ambiguity.
3. Ownership proven by read-after-write, never by provider success alone.
4. Verified by a live integration test that asserts the end state (target
   promoted/switched, data intact, parity reached).

Anything not meeting all four is `manual`, `degraded`, `blocked`, or `deferred`
— honestly, with the reason. **No universal "zero downtime" claim exists.**
Only "minimal-downtime migration for supported configurations."

## 1. Engine × cutover-mode certification (authoritative)

| Engine | Mode | Status | Phase proven | Evidence (live test) |
|---|---|---|---|---|
| PostgreSQL | host→host | `automatic` | 3A | `TestPGCutoverPrimitivesLive` |
| PostgreSQL | Docker container pair | `automatic` | 3A | `TestPGCutoverPrimitivesLive` (container path) |
| PostgreSQL | Docker Compose pair | `automatic` | **4A** | `TestPGComposeCutoverLive` |
| PostgreSQL | bastion | `blocked` (follow-on) | — | `dialBastion` implemented, no live cutover test yet |
| MySQL | host→host | `automatic` | 3B | `TestMySQLCutoverPrimitivesLive` |
| MySQL | Docker container pair | `automatic` | 3B | `TestMySQLCutoverPrimitivesLive` |
| MySQL | Docker Compose pair | `possible` | — | no compose cutover test yet (4A follow-on) |
| Redis | host→host | `degraded` | 3C | `TestRedisCutoverPrimitivesLive` (no source freeze) |
| Redis | Docker container pair | `degraded` | 3C | `TestRedisCutoverPrimitivesLive` |
| MongoDB | — | `blocked` for automatic | 3D | `TestMongoCutoverAssessmentLive` (manual verdict only) |
| PostgreSQL **logical** (pub/sub) | host / compose | `degraded` (scoped) | **4D** | `TestPGLogicalReplicationLive` (primitives; not wired to orchestrator) |

## 2. Traffic-provider certification

| Provider | Status | Owner-proof | Evidence |
|---|---|---|---|
| nginx | `automatic` | read-after-write HTTP marker | `TestNginxSwitchVerifyEndpointLive` + unit suite |
| haproxy | `automatic` | read-after-write | HAProxy switcher unit suite |
| caddy | `automatic` | read-after-write HTTP marker | `TestCaddySwitcherLive` + unit suite |
| traefik / cloudflare / docker / dns | `blocked` (manual only) | none | rejected at API boundary (`TestSupportedProviderGuardrail`) |

Only nginx, haproxy, caddy dispatch a fenced switcher with read-after-write
verification. The other four remain selectable only as **manual** switches
(legacy `TrafficSwitchEngine`); they are NOT selectable as automatic switches
(finding A.2-1, resolved in 4B).

## 3. WAN / large-scale transfer certification

| Capability | Status | Evidence |
|---|---|---|
| rsync resume after killed process | **proven** | `TestSyncResumeAfterKillLive` (throttled InitialSync killed mid-transfer → `ResumeSync` → target byte-identical by per-file md5) |
| rsync interruption → resume state machine | **proven** | `TestSyncResumeAfterInterruptionMock` (error session → `completed` + `VerifyChecksums`) |
| partial + append-verify | shipped | `--partial --append-verify` in `SyncEngine` |
| checksum verify | shipped | `--checksum`, `VerifyChecksums` |
| bandwidth / parallel limits | shipped | `--bwlimit`, `--parallel` |
| rsync replacement (chunked engine) | **deferred** | not needed; rsync proven sufficient (finding D-1) |

The WAN-impaired contract (killed process / SSH drop → partial tree → resume →
byte-identical target) is proven with REAL local rsync, not a mock.

## 4. Enterprise policy certification (4E)

| Control | Status | Evidence |
|---|---|---|
| Single support-guardrail surface | **shipped** | `PolicyEngine` (`policy.go`); `TestPolicyEngineUnifiesGuardrails` |
| Engine/provider gate at execution time | **shipped** | `CheckAutoCutover` in `runAutoCutover`; `TestPolicyEngineAutoCutover*` |
| API boundary == execution decision | **shipped** | `validateConfigSupport` delegates to `DefaultPolicy`; `TestPolicyEngineConfigSupportMirrorsValidate` |
| Operator introspection | **shipped** | `GET /api/pipeline/policy`; `TestPolicyMatrixIntrospection` |

## 5. Ship / no-ship gates (release checklist)

A release may be tagged `production` only when every gate below is green on the
release commit:

- [x] **Unit suite green:** `go test ./internal/mod/migration/` — no regression to Phase 1–3.
- [x] **Integration suite green** for the cells Phase 4 expanded (4A compose, 4B
      caddy, 4C rsync, 4D logical) — live Docker proof on the release commit.
- [x] **Guardrails tight:** `TestSupportedProviderGuardrail`,
      `TestPolicyEngine*` — only fenced providers selectable as automatic.
- [x] **Fail-closed unchanged:** no `ForceTransition(Committed)`; `AssertHolds`
      before every mutation; ownership = read-after-write.
- [x] **Honesty:** product wording = "minimal-downtime migration for supported
      configurations"; no universal zero-downtime; deferred cells (bastion,
      logical-wired, Redis source-freeze, MongoDB automatic) explicitly NOT
      claimed automatic.
- [x] **Secrets:** `SanitizeString` on persisted cmd output / policy matrix; no
      password on argv; DSN creds SQL-escaped (logical), PGPASSWORD via env.
- [x] **Docs:** matrix, release matrix, runbooks, final report updated and
      committed.

## 6. Known limitations carried into certification (must stay in release notes)

- Redis cutover has **no source freeze** → `degraded` (minimal, never zero,
  downtime). Never automatic.
- MongoDB has **no safe automatic cutover** (no replica-set lag measurement) →
  `blocked` for automatic; manual verdict only.
- PostgreSQL logical replication is proven at the primitive level but **NOT wired
  through the fenced orchestrator** → `degraded`/scoped, not automatic.
- Bastion/jump-host cutover has no live integration test → `blocked` for
  automatic until a `dialBastion` cutover test exists.
- Cross-major version cutover is `blocked` for every engine.
- No external provider (Cloudflare/DNS) is certified — no staging credentials in
  this environment.
