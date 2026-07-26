# Phase VPS-A — Acceptance Checklist

**2026-07-26** · Evidence anchor `9427118`. Status values:
passed · failed · partial · needs live verification · unsupported · out of scope.

| # | Requirement | Evidence | Status | Sev | Owner |
|---|---|---|---|---|---|
| 1 | Source/target add + connection test | servers API, pool dial `[LIVE]` | passed | — | backend |
| 2 | Credentials encrypted at rest | server/service.go:88+, auth.key 0600 | passed | — | security |
| 3 | Credentials redacted in API/WS | redactServer live-verified; WS stderr passthrough | partial | Med | security |
| 4 | DB file permissions | meshium.db 0644 | failed | High | infrastructure |
| 5 | Host-key verification deny-until-trusted, mismatch fail | knownhosts.go:137-167 | passed | — | security |
| 6 | Session token constant-time compare | 6a810ff | passed | — | security |
| 7 | Command injection hygiene | ShellQuote sweep; pqIdent/backtickIdent; wrapExec live | passed | — | security |
| 8 | Inventory collects real data (packages) | 900 pkgs live (was 0) | passed | — | backend |
| 9 | Inventory collects real data (configs) | 2043 files incl nginx/sshd live | passed | — | backend |
| 10 | Inventory freshness surfaced | stale-snapshot warning live | passed | — | backend |
| 11 | Config ownership/perms/symlinks captured+applied | configs.go content-only | failed | High | backend |
| 12 | Docker env/labels collected | 1624a5a + live template proof | passed | — | backend |
| 13 | Docker volume data migration | no code path | unsupported | High | product |
| 14 | Docker recreate fidelity (ports/nets/volumes) | docker.go:392-404 name/env/label/image only | failed | High | backend |
| 15 | DB engines dump/restore commands correct incl. container mode | fbac33a + container round-trip live | needs live verification | High | backend |
| 16 | Streaming DB transfer functional | ExecPipe panic fixed df0ff1b; no live pair run | needs live verification | High | backend |
| 17 | Compare distinguishes missing/different/same/unsupported/manual | parity.go states + live 149-item run | passed | — | backend |
| 18 | Item identity stable across reruns | phase-6c contract + UNIQUE(migration_id,item_key) | passed | — | backend |
| 19 | Selections honored by executor (catMixed) | filterCategoryToApplySet pipeline.go:1950-1955 | passed (code), needs live verification | High | backend |
| 20 | keep_target/skip/review_manual never applied | apply-set exclusion + unresolved verify state | passed (code) | — | backend |
| 21 | applied ≠ verified enforced in data model | item_results states; parity axes live | passed | — | backend |
| 22 | Verification levels infra/runtime/app honest | attestVerification never writes app | passed | — | backend |
| 23 | Per-item post-apply probes (pkg exists/config hash/service active) | absent except docker Up | failed | High | backend |
| 24 | Cutover default cannot claim traffic moved | manual_required + note | passed | — | backend |
| 25 | Commit blocked until cutover confirmed | ErrCutoverNotConfirmed path | passed | — | backend |
| 26 | Auto-cutover fenced + whitelisted | pipeline.go:2563-2763 | needs live verification | High | backend |
| 27 | Rollback scoped to actually-applied categories | pipeline.go:2102-2117 | passed (code) | — | backend |
| 28 | Per-item rollback evidence (backup_ref populated) | schema only, never written | failed | High | backend |
| 29 | Rollback cannot wipe target from empty baseline | Enumerated + zero-package guards + tests | passed | — | backend |
| 30 | Rollback after cutover | not implemented | unsupported | High | product |
| 31 | Per-migration concurrent-run lock | tryAcquire | passed | — | backend |
| 32 | Per-target cross-migration lock | absent | failed | High | backend |
| 33 | source==target blocked | pre-flight critical check | passed | — | backend |
| 34 | SQLite pragmas (FK, WAL, busy_timeout) per connection | db.go:22 + test | passed | — | infrastructure |
| 35 | Schema: item_results/selection_history/decision fields | live schema dump | passed | — | backend |
| 36 | DB growth/retention policy | 429MB, none | failed | Med | infrastructure |
| 37 | Stale plan hard-blocked before apply | warning only | partial | Med | backend |
| 38 | Live e2e apply→verify→rollback matrix | phase-5e mostly PENDING; not run here | needs live verification | **Critical gate** | infrastructure |
| 39 | UI grades category maturity (experimental flags) | absent | failed | Med | frontend+product |
| 40 | Sensitive step data at rest (creds in steps) | mongo/db collect metadata-only | passed | — | security |
| 41 | Root/sudo policy explicit | root silently assumed | partial | Med | product |
| 42 | Disk-full / reboot-mid-migration behavior | untested anywhere | needs live verification | High | infrastructure |
| 43 | Documentation of safe-use boundary | this audit + CHANGELOG known-limits | passed | — | documentation |
