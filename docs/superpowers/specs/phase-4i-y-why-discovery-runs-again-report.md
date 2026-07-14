# Phase 4I.Y — Why Does Discovery Run Again? (Report)

> READ-ONLY audit. No code changed. The companion map
> (`phase-4i-y-onboarding-to-pipeline-map.md`) holds the full inventory; this
> document answers the direct question and grades severity.

## Direct answer

**When you create a migration plan, the backend re-runs discovery/collection from
scratch. It does not reuse the inventory you already scanned when adding the
server.**

Proof (all real):

- Onboarding discovery persists to `discovery_snapshots` via
  `DiscoveryJobHandler.Execute` → `snapshotStore.SaveSnapshot`
  (`internal/jobengine/handlers.go:64-72`) and host info to `server_info` via
  `Service.RunConnectionTest` → `repo.SaveServerInfo`
  (`internal/mod/discovery/service.go:371`).
- The migration planner `planner.Plan` (`internal/mod/migration/planner.go:63`)
  fetches the server **record** (`GetByID`, planner.go:63/69), opens a **fresh**
  SSH client (`getSSHClient`, planner.go:78), and for each category calls
  `coll.Collect(collectCtx, sshClient)` (planner.go:191-209).
- A grep of `planner.go` for `GetServerInfo | LoadSnapshot | SnapshotStore |
  GetSnapshot | server_info` returns **zero matches**. The planner never reads
  onboarding output.
- The `Source`/`Target discovery.SystemInfo` fields declared in
  `model.go:80-81` are **never assigned** by the planner (grep: no assignment in
  `planner.go`/`pipeline_models.go`). They are dead — a sign of an *intended*
  reuse that was never wired.

So the second "discovery" is a **full re-collection**, not a cache hit. Whether
that is *correct* is the graded question below.

---

## Verdict: C — Partially overlapping (valid re-run for some data, redundant for others)

This is **not** purely "by design" and **not** purely "broken". It is a mix:

- **VALID to re-run:** connection/auth must be re-verified at plan time (creds
  may have rotated; the source/target *pairing* and the chosen categories are
  plan-scoped, not server-scoped). Onboarding discovery is per-server; a
  migration is per-source→target-pair and only for the *selected* categories.
- **REDUNDANT:** services, packages, databases, and docker inventory are
  re-collected identically. Onboarding already ran `systemctl list-unit-files`,
  the distro package list, and the pg/mysql/mongo/redis probes (see duplicate
  logic section). If the onboarding scan is fresh, re-scanning is wasted SSH
  work and can even diverge from what the user saw on the server page.

Architecturally the two systems are **separate modules with no shared read and
no shared cache**, so the duplication is structural, not incidental.

---

## Evidence: the duplicate logic

| Inventory | Onboarding (discovery) | Migration (categories) |
|---|---|---|
| Services | `CollectorRunner.Run` → `snapshot.Services` (`collector_runner.go:191`), via `systemctl list-unit-files` in discovery collectors | `ServicesCollector.Collect` (`services.go:28`): `systemctl list-unit-files --type=service --state=enabled` |
| Packages | discovery package collector | `PackagesCollector.Collect` (`packages.go:29`): `adapter.ListPackages()` |
| Databases | `AnalyzeDatabaseIntelligence` (`intelligence_database.go:76`: pg `pg_database`, mysql `information_schema`, mongo `mongod`, redis `INFO`) | `DatabaseCollector.Collect` (`database.go:38`): `getMigrator(...).ListDatabases` (same pg/mysql/mongo/redis probes) |
| Docker | `snapshot.Docker` (`collector_runner.go:140`) | `DockerCollector.Collect` (`docker.go:57`) |

Both run over the **same SSH transport** (`transport.SSHExecuter`) and the **same
remote commands**. Two implementations, two tables, zero cross-read.

---

## Severity of each finding

| # | Finding | Severity | Why |
|---|---|---|---|
| F1 | Plan collect re-runs services/packages/db/docker already scanned at onboarding; ignores both `server_info` and `discovery_snapshots`. | **performance inefficiency** (medium) + **workflow confusion** (medium) | Wasted SSH round-trips; user sees "scan" twice and assumes reuse. |
| F2 | No TTL / no invalidation on `server_info` or `discovery_snapshots`; changing creds/host never invalidates. | **correctness risk** (low-medium, latent) | A stale onboarding snapshot could be trusted if reuse is ever wired without a freshness gate. |
| F3 | Duplicate DB-detection logic (`intelligence_database.go` vs `database.go`). | **code duplication** (low) | Two sources of truth for "what DBs run here"; drift risk. |
| F4 | `Source`/`Target SystemInfo` field declared but never populated. | **cosmetic / dead field** (low) | Misleads readers into thinking reuse exists. |
| F5 | UI has no label connecting plan-time collection to prior server scans. | **workflow confusion** (medium) | Root cause of the user's "kok discovery lagi?" feeling. |
| F6 | `operationId` idempotency dedupes *submit*, not *discovery results*. | **none / by design** | Correct: it prevents duplicate migration rows, not duplicate scans. Noted to preempt misinterpretation. |

No **blocking correctness** defect found: the pipeline still produces correct
results (it re-collects rather than trusting stale data, which is safe). The
cost is inefficiency + confusion, not wrong output.

---

## Architectural recommendations (no implementation)

1. **Make the boundary explicit in the UI.** Label plan-time collection as
   "collecting for migration" and, when an unstale onboarding snapshot exists,
   show "reusing server scan" vs "re-scanning". Kills F5 confusion without code
   changes to the engine.
2. **Add a freshness gate before any reuse.** If reuse is introduced (R3), gate
   it on `last_checked`/`captured_at` age and on a server-config hash; invalidate
   on cred/host change (fixes F2). Without this, reuse is unsafe.
3. **Single-source the DB detection.** Extract one `ListDatabases(ssh, engine)`
   used by both `intelligence_database.go` and `migration/database.go` (fixes F3).
   Reduces drift between "what discovery shows" and "what the plan migrates".
4. **Either populate or delete `Source`/`Target SystemInfo`** (F4). If reuse is
   the intended path, planner should read `GetServerInfo`/`LoadSnapshot` into
   these fields and pass them to collectors as a cache; if not, drop the field.
5. **Optional future optimization (out of scope for audit):** let
   `planner.Plan` accept an optional `snapshotID`/`serverInfo` from onboarding and
   skip re-collecting categories whose remote state is unchanged. Only after R2's
   freshness gate exists.

---

## Follow-up audit steps before any implementation

- [ ] **Live timing:** with a real source+target, measure SSH round-trips of an
      onboarding scan vs a plan collect for the same categories, to quantify F1.
- [ ] **Config-hash prototype:** confirm what server fields, if changed, should
      invalidate `server_info`/`discovery_snapshots` (F2).
- [ ] **Field-usage grep:** confirm `Source`/`Target SystemInfo` is unread
      anywhere else (F4) before deleting.
- [ ] **FE wizard trace:** capture a real `/migrations/new` session to confirm
      no hidden `/discover` pre-call (the wizard sends only `PlanRequest`;
      structurally confirmed, but a live capture closes the gap noted in the map).
