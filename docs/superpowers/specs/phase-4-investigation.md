# Phase 4 — Investigation & Baseline Reality Check

> PART 1 of Phase 4 (production expansion & certification). Read-only: re-verify
> the actual supported matrix from **code + tests**, not prior claims, then
> produce the 4A→4F sequencing decision. Companion to
> `phase-3-final-report.md` and `phase-3-compatibility-matrix.md`.

## A. Baseline reality check (verified from code/tests, 2026-07-13)

### A.1 Engine × cutover dispatch (`cutoverEngineType`, pipeline.go:2199)
| Engine | Cutover supported? | Evidence |
|---|---|---|
| postgres / postgresql | **yes** (`automatic`, VERIFIED 3A) | 4 live integration tests |
| mysql / mariadb | **yes** (`automatic`, VERIFIED 3B) | 3 live integration tests |
| redis | **yes** (`degraded`, VERIFIED 3C) | 1 live integration test; `ErrNoSourceFreeze` |
| mongodb | **NO** (returns `false`) | `CutoverPreflight` + `CutoverPromote` fail closed; `preflightMongoDB` gives manual verdict only |

### A.2 Traffic-provider dispatch (`newTrafficSwitcher`, pipeline.go:2218)
| Provider | Real fenced switcher? | read-after-write ownership? | Status |
|---|---|---|---|
| nginx | **yes** | **yes** (`NginxSwitcher.verify`) | `automatic` for PG/MySQL |
| haproxy | **yes** | **yes** (`HAProxySwitcher`) | `automatic` for PG/MySQL |
| traefik | no | no | **selectable in API guardrail but fails closed at switch** ⚠️ |
| cloudflare | no | no | **selectable but fails closed** ⚠️ |
| caddy | no | no | **selectable but fails closed** ⚠️ |
| docker | no | no | **selectable but fails closed** ⚠️ |
| dns | no | no | **selectable but fails closed** ⚠️ |

**Finding (A.2‑1):** `supportedTrafficProviders` (pipeline_handler.go:634) lists 7
providers as selectable, but only nginx/haproxvy have fenced switchers. The other
5 are accepted by the API then rejected at `newTrafficSwitcher`. This is a
**documentation/UX honesty gap**, not a safety hole (it fails closed). Phase 4B
must either (a) implement one new provider with a real switcher, or (b) tighten
the API guardrail so unsupported providers are blocked at validation, not at
runtime. Default 4B choice: implement one locally-verifiable provider.

### A.3 Replication modes (`supportedReplicationModes`, pipeline_handler.go)
none / streaming / logical / replica / dump — all "supported" as a strategy set;
only streaming+replica are wired to a verified cutover (PG/MySQL). logical/dump
feed the database *category* (dump/restore), not cutover.

### A.4 What is still mock-only / unverified (must not be claimed in 4)
- The 5 non-nginx/haproxy providers have **no local integration proof** (fail
  closed). No provider has a live failure-injection test beyond the nginx/haproxy
  unit suite (timeouts, verify-mismatch, reload-fail).
- Bastion path: implemented in `ssh/client.go` (`dialBastion`, `BastionConfig`,
  `HasBastion`) but **never exercised by a cutover integration test** — topology
  identity/quoting through a jump host is unverified.
- WAN/impaired transfer: no `tc`/`netem` impairment test exists; resume is proven
  only by re-run, not by a killed-mid-stream process.
- Restart reconciliation: covered by orchestrator unit tests
  (`TestCutoverOrchestratorResumesAfterPartialFail`,
  `TestCutoverOrchestratorIdempotentReentry`) but **not** by a live
  kill-meshium-mid-cutover-and-resume integration test for any engine.
- Fencing/lease: unit-tested (`TestFence*`) but no live test that drops the lease
  mid-cutover.

### A.5 Immutable guards — confirmed present (do not regress)
`AutoCutoverDefault=false`; `AssertHolds` before every mutation; no
`ForceTransition(Committed)`; central redaction; idempotency keys; audit +
correlation IDs; ownership = read-after-write. (See phase-3-final-report.md §3.)

## B. Topology inventory

| Mode | Transport | PG | MySQL | Redis | Mongo | Blocker / note |
|---|---|---|---|---|---|---|
| host→host (SSH) | direct SSH | proven (3A) | proven (3B) | proven (3C) | proven (3D) | none for host mode itself |
| Docker container pair | `docker exec` | unit only | **live (3B)** | **live (3C)** | **live (3D)** | PG container pair has no live test yet |
| Docker Compose pair | `docker compose exec -T` | **automatic (VERIFIED 4A)** | unverified | unverified | unverified | `compose exec` quoting + service-discovery now verified for PG via `TestPGComposeCutoverLive` |
| bastion/jump-host | `ssh -J` / `dialBastion` | implemented, unverified | implemented, unverified | implemented, unverified | implemented, unverified | **4A follow-on:** `dialBastion` transport implemented; no live cutover integration test through a jump host yet (per-host executer via multi-hop ProxyJump not wired to cutover primitives) |

**Assumptions common to all modes:** source→target direct connectivity (or via
SSH/bastion); DB client binaries present on the execution surface; credentials
via env / `-e` (never `-p` on argv); `StrictHostKeyChecking=accept-new` for rsync
transport.

**Finding (B‑1):** the only *live* topology proofs were host→host (PG/MySQL/Redis/
Mongo) and Docker-container (MySQL/Redis/Mongo). **Docker Compose and bastion
had zero live cutover proof.** Phase 4A's first increment closed the
lowest-risk gap: **PostgreSQL Docker-Compose pair** verified live via
`TestPGComposeCutoverLive` (4A), certifying the compose execution mode through
`CertifyTopology` + the real preflight/catch-up/promote primitives. Next 4A
increment: **bastion** (transport safety through `ssh -J` / `dialBastion`).

## C. Provider inventory

| Provider | Credential model | Local testable? | Idempotency | Ownership proof | Class |
|---|---|---|---|---|---|
| nginx | config + reload + verify endpoint | **yes** (local) | yes | read-after-write HTTP | automatic candidate |
| haproxy | config + reload + verify | **yes** (local) | yes | read-after-write | automatic candidate |
| application-config switch | file write + reload + health | **yes** (local) | yes | process/health read | local-verifiable candidate |
| reverse-proxy/LB adapter | config + reload | partial | yes | backend-health read | manual/staging candidate |
| floating IP | API/CLI | env-dependent | yes | ARP/route read | staging candidate |
| Cloudflare DNS | API token | **no local** | yes (idempotent PUT) | propagation delay → uncertain | **staging-only / blocked without creds** |
| traefik/caddy/docker/dns | n/a today | n/a | n/a | n/a | **fail closed (blocked)** |

**Finding (C‑1):** the safest 4B first increment is a **locally-verifiable
provider** (application-config switch or a second reverse-proxy adapter) so
ownership proof can run end-to-end without external credentials. External
providers (Cloudflare/DNS) stay blocked until a staging harness with real
credentials + propagation verification exists.

## D. Scale / WAN inventory (current = `SyncEngine`, sync.go)

| Capability | Status | Notes |
|---|---|---|
| rsync direct | **yes** | `rsync -avz --progress --stats` over SSH |
| partial resume | **yes** (`--partial --append-verify`) | resume = full re-run of same command |
| append-verify | **yes** | `--append-verify` |
| checksum verification | **yes** (`--checksum`, `VerifyChecksums`) | whole-tree `md5sum` compare (O(N) remote) |
| compression | **yes** (`-z`, toggleable) | |
| bandwidth limit | **yes** (`--bwlimit`) | wired to `BandwidthLimit` |
| parallel transfers | **yes** (`--parallel`) | wired to `ParallelTransfers` |
| very large trees | unverified | no chunk/sub-transfer state; one rsync process |
| long-running remote checksum | unverified | `find -exec md5sum` can exceed timeouts on huge trees |
| SSH drop & recovery | partial | resume re-runs; no byte-level resume proof |
| tc/netem impairment | **none** | no impairment test exists |
| sub-transfer chunking | **none** | no chunk state machine |
| backpressure/fairness | none | no fairness between concurrent migrations |
| operator visibility | partial | `--progress` parsed; no per-chunk ETA persisted |

**Finding (D‑1):** the rsync baseline already supports resume (`--partial
--append-verify`), checksum, bandwidth, parallel. The honest 4C increment is to
**prove** the resume/integrity contract under a killed-process + SSH-drop + a
large synthetic tree, and add **operator-visible progress persistence** — NOT to
replace rsync with a new chunking engine (rule: never replace a proven default
unless at least as safe). A chunked engine is a later/deferred option.

## E. Advanced database inventory

| Engine | Next axis | Exact blocker | Belongs in |
|---|---|---|---|
| PostgreSQL | logical replication (pub/sub) | new preflight (publication exists, no conflicting slots) + role model | 4D |
| PostgreSQL | cross-major | version-skew replication risks; needs dual-version pair | 4D (deferred unless paired) |
| PostgreSQL | subset/table-scoped | `pg_publication` scoping + seed subset | 4D |
| MySQL | GTID vs non-GTID | auto-position vs file/pos; preflight must detect | 4D |
| MySQL | more version range | 8.0/8.4/9.x matrix; 8.4 rename already handled | 4D |
| Redis | Sentinel | client redirect model; `ROLE` differs (sentinel-aware) | 4D (later) |
| Redis | Cluster | hash-slot reshard; no single primary; out-of-scope for `automatic` | 4D (deferred) |
| MongoDB | sharded cluster | mongos topology; `rs.status` insufficient; reconfig risk | 4D (deferred) |
| MongoDB | managed/Atlas | no replica control; blocked | blocked |

**Finding (E‑1):** the lowest-risk 4D increment is **PostgreSQL logical
replication (pub/sub) for same-major**, because physical replication is already
proven and logical adds a well-bounded new axis (publication/subscription) with
a clear preflight. Redis Cluster / Mongo sharded remain **deferred** (cannot
prevent dual-writer ambiguity safely).

## F. Enterprise operations inventory

| Control | Status | Gap |
|---|---|---|
| Audit trail | **present** (correlation id, `AuditEntry`, export) | retention/export policy not formalized |
| RBAC / approval workflow | **absent** | single operator model; no separation of duties |
| Policy engine / feature gating | partial (`supportedTrafficProviders`, `supportedReplicationModes` maps) | no "automatic cutover allowed?" / "which topology?" policy |
| External secret manager | **absent** | creds in app AES key only; no Vault/SOPS/cloud KMS |
| Key rotation / recovery | partial (AES key decrypt at execute) | no rotation test |
| Incident package / diagnostic bundle | partial (`BuildDiagnosticBundle` redacts) | not wired to a cutover incident flow |
| Metrics / health / alerting | partial (`HealthEngine`, `RiskEngine`) | no SLO/readiness gating on cutover |
| Multi-tenant isolation | absent | single-tenant assumption |

**Finding (F‑1):** the safest 4E increment is a **server-side policy engine**
gating which engine/provider/topology/automatic-cutover is allowed (reusing the
existing `supported*` maps as enforcement, not just validation), because it
directly extends the honesty contract without touching the fence/state machine.
RBAC/external-secret-manager are larger and can follow.

## G. Test environment inventory

| Environment | Available? | Can prove |
|---|---|---|
| local Docker daemon | **yes** | PG/MySQL/Redis/Mongo container + compose pairs |
| Docker Compose | **yes** | compose-exec topology (unverified today) |
| testcontainers | not used | n/a |
| CI container | n/a here | n/a |
| staging w/ provider creds | **no** | external providers (Cloudflare/DNS) stay blocked |
| bastion simulation | **yes** (ssh client supports it) | bastion routing, but no live cutover test yet |
| network impairment (tc/netem) | host supports tc | WAN impairment test possible |
| cloud sandbox | **no** | managed-topology claims stay blocked |

## H. Phase 4 sequencing decision

Default order confirmed, with the lowest-risk first increment per sub-phase
chosen from the findings above:

1. **4A topology** — first increment: **PostgreSQL Docker-Compose pair**
   (VERIFIED 4A, `TestPGComposeCutoverLive`). Next: bastion (via
   `ssh -J` / `dialBastion`), then expand the container pair to MySQL/Redis/Mongo.
   One execution mode at a time.
2. **4B provider** — first increment: **one locally-verifiable provider**
   (application-config switch or second reverse-proxy adapter) with
   read-after-write ownership; ALSO tighten the API guardrail so the 5
   unsupported providers are blocked at validation (finding A.2‑1). External
   providers stay blocked without staging creds.
3. **4C WAN/transfer** — first increment: **prove resume/integrity under
   killed-process + SSH-drop + large synthetic tree**, add persisted progress;
   do NOT replace rsync.
4. **4D advanced DB** — first increment: **PostgreSQL logical replication
   (pub/sub) same-major**.
5. **4E enterprise** — first increment: **server-side policy engine** gating
   engine/provider/topology/automatic-cutover.
6. **4F certification** — final matrix + runbooks + drills + ship/no-ship.

No reordering needed; investigation confirms the default order is the safest.
