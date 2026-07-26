# Phase VPS-A — Meshium VPS Migration Readiness and Production Suitability Audit

**2026-07-26** · Evidence anchor: commit `9427118` (main). Method:
investigate → map → validate → threat-model → test → assess → report.
All claims below are anchored to code paths, SQLite schema, or live runtime
results from this audit session. Prior docs (phase-4i, phase-5e, phase-6a/6c,
ui-*) were treated as hypotheses and re-verified against the implementation;
several were found stale (noted inline).

**Evidence classes used:**
- `[LIVE]` — executed against real infrastructure during this audit session
  (real source VPS #1 → real target VPS #5, read-only paths only; local
  docker daemon; local shell semantics checks).
- `[CODE]` — verified by reading the shipped implementation (file:line).
- `[HIST]` — prior live evidence from phase-5e docs, not re-run here.
- `[PENDING]` — never live-verified anywhere; code-only.

---

## 1. Executive verdict

- **Meshium is NOT ready for production VPS migration.** The write path
  (apply → verify → cutover → rollback) has **never been executed end-to-end
  against a real source/target pair** — phase-5e's own live matrix records
  C1/C2/C5–C10 as PENDING; only transfer primitives passed (`[HIST]`
  phase-5e-live-matrix-results.md).
- The **read path is genuinely solid and live-verified**: plan/collect for
  packages+configs+services completes in ~10s against real VPSes, parity
  compare returns honest per-item states, and the parity summary reports
  `verificationConfidence: 0` for unexecuted migrations instead of faking
  green (`[LIVE]` this session).
- This audit session found and fixed **11 silent-corruption classes in the
  collectors themselves** (packages collected 0 packages, configs collected
  cert/font junk and zero real configs, docker env/labels always empty,
  container-mode DB commands could never execute, rollback baselines that
  meant "delete everything"). All fixed and live-verified — but their
  *existence until today* proves the test suite green-lights broken
  collectors. Production trust must be earned by a live execution matrix,
  not by unit tests.
- **The state machine and honesty layer are the strongest part**: traffic
  switch defaults to `manual_required` and never claims traffic moved
  (pipeline.go:2466-2472), commit is blocked until the operator confirms
  cutover (pipeline.go:666-676, `ErrCutoverNotConfirmed`), per-item
  execution/verification evidence is persisted (`migration_item_results`),
  and verification levels distinguish infra/runtime/app and never fabricate
  `app_verified` (pipeline.go:2373-2375).
- **Rollback is category-coarse and partially destructive by design**:
  restore-from-backup for configs/users, complement-removal for
  packages/databases (now guarded against empty baselines), `FLUSHALL` for
  Redis. There is **no rollback after cutover** and no per-item rollback.
- ~~No cross-migration target lock~~ — **RETRACTED 2026-07-26.** Re-verified
  during Gate 0: `Execute` claims source AND target through
  `tryAcquireResource` before any mutating work and fails closed when either is
  held (pipeline.go:224-235, `concurrency_resource_test.go`). The original
  finding was wrong; the guard exists.
- Credential handling is largely correct (AES-encrypted at rest, redacted in
  API, constant-time token compare as of `6a810ff`), but `meshium.db` is
  mode **0644** while `auth.key` is 0600 — the DB leaks encrypted blobs +
  full inventory to any local user.
- **Safe-use boundary today: lab/demo and disposable staging VPS
  unsupervised; low-risk non-critical VPS only with an operator watching and
  a tested restore path outside Meshium. Not production, not autonomous.**

---

## 2. Supported use-case matrix

Verdicts: `S` supported and safe · `SL` supported with explicit limitation ·
`SR` supported only with manual operator runbook · `E` experimental ·
`U` unsupported · `D` dangerous / should be blocked.

| Use case | Verdict | Basis |
|---|---|---|
| Simple single-server Linux VPS (packages+services+configs) | **SL** | Collect/plan/compare `[LIVE]`; apply/rollback `[PENDING]` live. Limitation: apply never e2e-live-verified. |
| Standard packages & services | **SL** | apt/dnf/pacman/apk/zypper adapters fixed+tested this session (`e88469e`); apply is `[CODE]` only. |
| Custom configs | **SL** | Collector fixed+live-verified (`2201305`, 2043 real files). Excluded-list protects OS-critical files. Apply restores no ownership/perm/symlink metadata → limitation (configs.go Apply writes content only). |
| Docker containers | **E** | Env/labels collection fixed (`1624a5a`) but recreate is `docker run` reconstruction (ports/volumes/networks NOT reconstructed — docker.go:392-404 builds name/env/label/image only). |
| Docker Compose | **SL** | Compose files collected + `compose up -d` on target `[CODE]`; image pulls registry-only (locally built images explicitly not migrated, docker.go:277). |
| Docker volumes / persistent data | **U** | Volume *names* collected; **no volume data transfer path exists** (no code path copies volume contents). |
| Databases (PG/MySQL/Mongo/Redis dump-restore) | **E** | Adapter commands unit-tested; container-mode fixed today (`fbac33a`); streaming path panicked until today (`df0ff1b`) → has never worked in the field. No live pair test. |
| Reverse proxy | **SR** | Configs category can carry nginx configs `[LIVE collect]`; no semantic handling; traffic switch manual. |
| SSL certificates | **U** | `/etc/ssl` deliberately pruned from collection (configs.go hugeConfigDirs); letsencrypt dir IS collected `[LIVE]` but renewal state/permissions not handled. |
| Application runtime | **U** | No runtime category; honest — nothing pretends otherwise. |
| Cron / systemd timers | **SL** | User crontabs collected+applied (users.go); systemd timers only as enabled services. Backup parser fixed today (`5b37430`). |
| Multiple users & SSH config | **SL** | users/groups/passwd applied; `/etc/passwd`, `shadow` excluded from configs by design; UID/GID collision handling not implemented. |
| Firewall | **SL** | iptables-save restore only; ufw-status capture now correctly refused (`5b37430`); nftables-native and ufw rule semantics unsupported. |
| DNS / domain dependency | **U** | No DNS integration; cutover note says so honestly. |
| External/object storage | **U** | Out of scope, not represented. |
| Managed database | **U** | Adapters assume shell access to the engine host. |
| Production VPS with live traffic | **D — block** | No live-verified write path, no target lock, no post-cutover rollback. |
| Zero/minimal-downtime | **E** | Fenced auto-cutover exists in code (pg/mysql/redis × nginx/haproxy/caddy, pipeline.go:2563-2763) but is opt-in and `[PENDING]` live; default is honest manual_required. |
| Compliance / audit / recovery requirement | **SR** | audit_trail + item results + selection history exist `[CODE+schema]`; recovery depends on category rollback limits below. |

---

## 3. End-to-end journey map (actual)

| Stage | Route/API | Backend | Persistence | Reality class |
|---|---|---|---|---|
| Add source/target | `/servers/new` → `POST /api/servers` | server.Service.Create, creds AES-encrypted (service.go:88+) | `servers` | `[LIVE]` (servers 1,5 exist) |
| Test connection | `POST /api/servers/:id/test` | pool dial + host-key callback | `known_hosts` | `[LIVE]` |
| Inventory scan | discovery module | ~13 collectors (memory: MaxSessions risk) | `discovery_snapshots` | `[LIVE]`, freshness warning surfaces in plan (`[LIVE]` "snapshot is 1475 min old — collected live") |
| Plan creation | `WS /ws/plan` | Planner → category Collect on source | `migrations`, `migration_steps` (status `completed`) | `[LIVE]` ~10s, 3 categories |
| Compare | `GET parity`, `parity-summary` | ParityEngine: live target collect vs plan snapshot; 15s result cache (`2eba2c7`) | none (cache in-memory) | `[LIVE]` 11s cold / ms warm |
| Item selection | `PUT /api/migrations/:id/selections` | handler + InvalidateParity | `migration_selections` (+decision_reason, risk_acknowledged, manual_followup), `migration_selection_history` | `[CODE+schema]` verified present |
| Execution | `POST pipeline start` → Execute | 13 stages (pipeline.go:141-157); per-migration lock only | `migration_stages`, `migration_steps→applied`, `migration_item_results` | `[PENDING]` live e2e |
| initial_sync | — | Applier per category over **apply-set only** (filterCategoryToApplySet, pipeline.go:1950-1955) | item results ExecApplied/ExecFailed | `[CODE]`; catMixed bug fixed per 6C |
| live_replication | — | Only if `ReplicationEnabled`; MySQL replica unseeded (design doc known gap) | `replication_status` | `[PENDING]`, honest skip default |
| health_verification | — | `echo ok` (infra) + docker `ps` upgrade to runtime; **never fabricates app_verified** (pipeline.go:2313-2375) | `migration_item_results.verification_*` | `[CODE]` |
| traffic_switch | — | Default: records `manual_required`, never "switched" (pipeline.go:2466-2472). Opt-in fenced autoCutover with lease + engine/provider whitelist | `traffic_switch_config`, `migration_fence_leases`, `cutover_history` | manual: `[CODE]` honest; auto: `[PENDING]` |
| Commit | `POST commit` | Blocked while switch_state=manual_required (`ErrCutoverNotConfirmed`); validated state transition, fails closed to NeedsManualIntervention (pipeline.go:666-690) | `migrations.status`, audit entry | `[CODE]` strong |
| Rollback | stage failure → rollbackApplied; manual endpoint | Category Applier.Rollback from `migration_backups`, scoped to categories with ≥1 ExecApplied item (pipeline.go:2102-2117) | `migration_rollback_steps`, `rollback_history` | `[CODE]`; live `[PENDING]` |
| Audit/history | tabs + endpoints | audit_trail, migration_events | tables exist | `[CODE]` |

**"applied" semantics (validated):** step `applied` = category Apply()
returned nil = remote commands exited zero. Per-item `ExecApplied` inherits
this coarse result (all apply-set items flip together,
pipeline.go:1979-1991). It does **not** mean expected target state exists —
that is verification's job, and verification tops out at runtime level.
The system itself never conflates them (parity summary showed
`executionCompletion: 0`, `verificationConfidence: 0` `[LIVE]`).

---

## 4. Source/target capability matrix

| Capability | Status |
|---|---|
| OS/distro/version detection | Detected (`/etc/os-release`, distro.go) `[LIVE]` |
| Package manager detection | Detected, 5 families `[LIVE apt]` |
| Architecture / CPU / RAM / disk checks | Detected in compatibility engine; exit-code gating fixed today (`5b37430`) |
| Init system | **Assumed systemd** (rc-update fallback for services list only) |
| Non-standard SSH port / password / key / encrypted key / bastion | Supported `[CODE]` (client.go, bastion at :274) |
| Host-key verification | **Deny-until-trusted**; explicit TOFU via TrustHostKey; mismatch = hard error (knownhosts.go:137-167) — strong |
| sudo/root | **Assumed root**; no sudo password handling for collectors (replication uses `sudo -u postgres` assuming NOPASSWD) |
| IPv6 | Untested; `net.JoinHostPort` used so likely functional, `[PENDING]` |
| Time sync check | Timezone only (fixed today); no NTP/skew check |
| Hostname/port/service/data conflict checks | Port listing collected; **no conflict blocking before apply** — apply is last-writer-wins with backups |
| Target re-scan before destructive action | Parity re-collects target live (≤15s cache) — yes for compare; pipeline apply does **not** re-verify staleness at execute time (stale plan = warning surface in FE, not a hard block) |

---

## 5. Discovery/inventory coverage matrix

| Category | Coverage after this session's fixes |
|---|---|
| Packages | **Collected reliably** `[LIVE 900]` (was: 0, silently) |
| Services | Collected (enabled units) `[LIVE]`; zero-guard added |
| Configs | **Collected reliably** `[LIVE 2043 files incl. nginx/sshd]` (was: junk); ownership/perms/symlinks NOT captured |
| Users/groups/crontabs | Collected; crontab backup parser fixed |
| Docker containers/images/volumes/compose | Collected incl. env+labels (fixed); volume **data** not collected |
| Databases | Enumerated w/ sizes; mongosh fixed; creds never persisted in steps `[CODE]` |
| Open ports / firewall | Collected; ufw text no longer stored as restorable |
| Mounts/swap/kernel/certs/env-files | **Not collected** (env files deliberately — secret hygiene) |
| Freshness | Plan warns when onboarding snapshot stale and re-collects live `[LIVE]`; parity cache 15s TTL |

---

## 6. Category-by-category migration matrix

| Category | Collect | Apply | Verify | Rollback | Net class |
|---|---|---|---|---|---|
| packages | `[LIVE]` | install missing via batches, cap-free since `f67f211`; no version pinning, no repo/key migration | infra only (exists-check absent — verify is target-reachable + item attestation) | remove complement of baseline; refuses empty baseline (`5b37430`) | **partially implemented** |
| configs | `[LIVE]` | upload content, mkdir -p; **no owner/mode/symlink restore**; excluded-list enforced (absolute-key fix `1624a5a`) | infra | restore backed-up files | **partially implemented** |
| services | `[LIVE]` | enable+start | runtime-ish via docker only; services not probed per-unit post-apply | re-disable? (coarse) | **partially implemented** |
| users | `[CODE]` | useradd/groups/crontabs/firewall | infra | restore passwd/group/shadow files — **blunt** (whole-file restore) | **partially implemented; rollback risky** |
| docker | `[CODE]` | pull registry images (honest fail list), compose up, or `docker run` name/env/label/image only — **no ports/volumes/networks** | runtime (Up check) | remove created containers | **experimental** |
| database | `[CODE]` | dump/restore per engine; streaming fixed today; container-mode fixed today | none beyond infra (no row/checksum verify) | drop complement, Enumerated-guarded; Redis = FLUSHALL | **experimental; never live e2e** |

---

## 7. Execution safety findings

- Shell quoting: `shared.ShellQuote` used pervasively; injection spot-checks
  (docker names, config paths, db names via pqIdent/backtickIdent, crontab
  users) clean. Docker `idList` interpolated unquoted into `docker inspect`
  on the **source** — IDs originate from the source's own docker daemon, so
  no privilege boundary is crossed (low).
- Timeouts: pooled clients freeze the first dialer's profile; long ops
  (installs, pulls, compose up) moved to cap-free ctx-bound exec (`f67f211`).
  `NewSession`/`sftp.NewClient` remain outside the per-command watchdog — a
  wedged transport can hang a stage until ctx cancel (**medium, open**).
- Concurrency: per-migration lock, global slot cap, **and a per-server resource
  lock** — `tryAcquireResource(SourceID, TargetID)` is atomic and fails closed,
  so two distinct migrations cannot fork the same host (pipeline.go:224-235).
  Same-server (source==target) is a critical pre-flight check
  (pipeline_handler.go:1000-1005). *(Corrected: an earlier draft of this audit
  claimed the per-target lock was missing.)*
- Cancellation: ctx propagated through stages; checkpoint rows persist
  completed stages; resume skips completed stages (pipeline.go:355-368).
- Partial failure: category apply error marks apply-set items ExecFailed and
  triggers rollback of previously applied categories (pipeline.go:1965-1977).
- Disk-full/reboot-during-migration: **untested anywhere** `[PENDING]`.

## 8. Verification credibility findings

- Levels exist and are enforced in data: `not_verified / infra / runtime /
  app / verify_failed`; attestation only ever raises infra→runtime and never
  writes app (pipeline.go:2373-2375). Items outside the apply-set stay
  unresolved — no false green (pipeline.go:2316-2319).
- BUT the probes are thin: infra = `echo ok` on target; runtime = docker `ps`
  Up substring. Packages/configs/services/db get **no per-item existence
  probe** post-apply. Verification honesty is structural, depth is minimal.
- `applied ≠ verified` holds throughout the schema and parity scoring
  (`[LIVE]` summary shows independent axes).

## 9. Cutover / commit / rollback findings

- Default cutover is **manual and honestly labeled**; the persisted note
  states traffic still points at source (pipeline.go:2470-2472).
- Opt-in fenced autoCutover exists (leases via `migration_fence_leases`,
  engine whitelist pg/mysql/redis — mongodb explicitly refused as unsafe;
  provider whitelist nginx/haproxy/caddy) — `[PENDING]` live.
- Commit cannot occur while manual_required; failed transitions fail closed
  to NeedsManualIntervention (pipeline.go:676-683). Commit does **not**
  touch the source (no decommission) — reversible at the infra level.
- Rollback scope is **category-coarse from `migration_backups`**, gated on
  per-item ExecApplied existence (pipeline.go:2102-2117). `backup_ref` on
  item rows exists in schema but is **never populated** → per-item rollback
  evidence absent → **rollback must be classified limited for
  mixed-category migrations** (the prompt's critical test: FAILS the
  per-item-evidence bar, passes the "scoped to actually-applied categories"
  bar).
- No rollback after cutover; no source-changed detection before rollback.
- Destructive baselines hardened this session: database Enumerated guard,
  packages zero-baseline refusal — both prevent "rollback = wipe target".

## 10. SQLite persistence findings

- File: `/root/.meshium/meshium.db`, **mode 0644** (finding: should be 0600;
  `auth.key`/`auth.token` are correctly 0600). Size **429MB** — no retention
  policy; legacy steps carry multi-MB payloads (finding: growth/retention).
- Pragmas per connection via DSN: `busy_timeout(5000)`, `foreign_keys(1)`,
  `WAL`, `synchronous(NORMAL)` (db.go:22) — FK enforcement + concurrency
  posture correct.
- Schema readiness the brief asked about is **already implemented**:
  `migration_item_results` with `UNIQUE(migration_id,item_key)`, index,
  FK CASCADE, verification fields, `backup_ref`; `migration_selection_history`;
  `migration_selections.decision_reason / risk_acknowledged /
  manual_followup`. Upsert path in repo.go:540-560. Old migrations without
  item rows degrade to legacy whole-category behavior (loadItemDecisions
  returns empty map ⇒ apply-all).
- Secrets at rest: server creds AES-encrypted; DB config password encrypted
  on MigrationConfig; step data verified to carry metadata only (mongo list
  never persists creds). Legacy risk: none observed in sampled rows.

## 11. API / UI truthfulness findings

- Server API: password/sshKey/passphrase redacted on every read path
  (service.go:77-85,143-149) — `[LIVE]` verified absent from JSON.
- Parity summary axes keep execution/verification/decision separate
  `[LIVE]`.
- Known misleads (open): "applied" chip can read as done-done to operators
  (depth documented above); compare page items for categories whose apply is
  experimental (docker/db) are selectable like mature ones — UI does not
  grade category maturity; stale plan surfaces as warning, not block.
- Fixed this session: compare "Nothing to compare" on fresh plans
  (`11cc8fb`), Live Monitor idle blank, rollback banner stale after reload
  (`ca5804c`), modals clipping.

## 12. Security findings (severity-ranked)

| Sev | Finding | Status |
|---|---|---|
| ~~High~~ | ~~No per-target concurrency lock~~ | **retracted** — guard exists (pipeline.go:224-235) |
| High→fixed | `meshium.db` mode 0644 with full inventory + encrypted creds | fixed `eb1d2b9`; live 429MB→130MB, all files 0600 |
| High→fixed | Configs applied content only → 0600 secrets widened on target | fixed `eb1d2b9`; live: TLS privkeys + gshadow keep modes |
| High→fixed | Cancelled configs collect returned success with zero files | fixed `7d478f0` (found by live matrix C6) |
| High→fixed | Session token `==` compare (timing) | fixed `6a810ff` |
| Critical→fixed | Container-mode DB commands unrunnable / env cred assignments dead | fixed `fbac33a` |
| Critical→fixed | Rollback wipe-target baselines (db/packages) | fixed `1624a5a`,`5b37430` |
| Medium | Root assumed; no sudo-password path; replication assumes NOPASSWD sudo | open |
| Medium | WS/step logs carry raw stderr (SanitizeString only in packages apply) | open |
| Low | docker inspect idList unquoted (same-host origin) | open |
| Strong | Host-key deny-until-trusted + mismatch hard-fail; explicit TOFU | — |

## 13. Live test matrix (this session, safe/read-only) + gaps

| Test | Result |
|---|---|
| Plan 3 categories real source→target | **PASS** 10s, real data (900 pkgs / 2043 configs) |
| Parity compare cold/warm | **PASS** 11.2s / ms; honest zero exec/verify scores |
| Collector regression suite (5 distro families, mocks pinned to real tool output) | **PASS** |
| wrapExec round-trip in real container (env survival) | **PASS** |
| dpkg/find/prune/tar/docker-template semantics on live shell/daemon | **PASS** (7 empirical proofs) |
| Apply / verify / rollback on disposable pair (VPS-B1) | **RUN 2026-07-26** — 10/10 PASS. C1 packages apply+rollback, C2 config mode/owner, C3 excluded files, C4 services, C5 apply failure, C6 cancellation, C7 SSH drop, C8 users, C9 idempotent re-apply, C10 configs rollback restores content+mode. See `vpsb1_live_matrix_test.go` (tag `integration`). |
| Cutover / commit on disposable pair | **NOT RUN** — Gate 2/3 scope |
| phase-5e matrix C1–C10 | mostly **PENDING** per its own record `[HIST]` |

## 14. Go / no-go

**Release verdict tier: "Suitable only for disposable staging VPS; low-risk
non-critical VPS with operator supervision."** Production (supervised or
autonomous): **NO-GO** until the remediation roadmap's staging→production
gates pass, chiefly a live execution matrix on disposable pairs.
