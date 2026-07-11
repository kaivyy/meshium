# Phase 1 — P0 Safety Fixes (Design, revised)

Status: design, not yet implemented. This revision makes scope deterministic
per the user's 7 mandatory changes. All findings verified against code
(see `docs/audit-2026-07-11.md`).

## Goal

Make the migration engine safe to run: no silent DB skip, no split-brain
rollback, no false success, no secret in argv, no OOM on large output, no 30s
kill on long restores, no auto-complete past a manual cutover, no in-memory
checkpoint presented as resumable. Every ambiguous state fails closed to
`StateNeedsManualIntervention`; every partial rollback ends in
`StateRollbackDegraded`.

## Non-goals (this pass)

- No automatic traffic switching, no live replication implementation, no
  distributed fencing/lease.
- No new DB engines.
- No mid-transfer byte-level resume (transfer-level resume is Phase 2;
  documented as a residual limitation).
- No large unrelated refactor.

---

## 1. Schema, state machine, and persistence (deterministic)

### 1.1 New states

Add to `MigrationState` enum (`internal/mod/migration/state.go`):

- `StateAwaitingCutover` — pipeline stopped after `manual_required`; only an
  explicit operator commit action may leave it. Survives backend restart.
- `StateNeedsManualIntervention` — fail-closed terminal-ish state for
  ambiguous topology, unsafe rollback, checkpoint-write failure, or commit
  rejection. Not `IsTerminal()` (operator can still act), but no automatic
  forward/rollback transition is legal from it.

### 1.2 State-transition table (additions only)

New rows in `transitionTable`:

```
StateObservation:           {StateAwaitingCutover, StateFailed, StateInterrupted, StatePaused, StateRollback}  // add StateAwaitingCutover
StateAwaitingCutover:       {StateCommitted, StateNeedsManualIntervention, StateRollback, StateFailed}
StateNeedsManualIntervention: {}  // no automatic transition out
StateRollback:              {StateRolledBack, StateRollbackDegraded, StateNeedsManualIntervention, StateFailed}  // add NeedsManualIntervention
```

`StateAwaitingCutover` and `StateNeedsManualIntervention` are added to:
- `String()` switch → `"awaiting_cutover"`, `"needs_manual_intervention"`
- `stateString` map → same strings (so `GetMigrationState` round-trips)
- `IsTerminal()`: `StateNeedsManualIntervention` is terminal-ish (treated as
  terminal for "should the run loop keep going?" = no).
- `IsRunning()`: neither is running.
- `CanResume()`: `StateAwaitingCutover` cannot resume via the interrupt/pause
  path — it resumes only via the explicit commit endpoint.

### 1.3 Persistence / schema changes

**No new tables, no new columns.** Verified the schema already has everything:

| Need | Existing column |
|---|---|
| migration state | `migrations.state` (ALTER at `migrations.go:496`) + `migrations.status` (legacy) |
| stage checkpoint | `migration_stages.checkpoint_data` (`migrations.go:263`) |
| cutover manual-required | `traffic_switch_config.switch_state` (`migrations.go:294`) + `cutover_history` rows |
| rollback step results | `migration_rollback_steps` (`migrations.go:217`) |

**Migration strategy:** none required. Migrations are idempotent
`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN` (SQLite tolerates re-add via
guard — confirm `state` ALTER is guarded; if not, wrap in
`PRAGMA table_info` check). New states are new string values in a TEXT
column — no DDL. `SetMigrationState` already writes both `state` and
`status` columns (`job.go:149`), so `awaiting_cutover` /
`needs_manual_intervention` persist + round-trip with zero schema work.

**API serialization:** the `String()` values are already what the API returns;
no frontend change required to *persist* the state (the pipeline view may add
labels in a later commit — out of this pass's safety scope, but a test
asserts the string is returned).

### 1.4 Checkpoint wiring (mandated option 1)

`pipeline_repo.go:148` `UpdateStageCheckpoint` has zero callers;
`NoopCheckpointStore` (`progress.go:207`) is the only `CheckpointStore` impl.

This pass **wires real checkpoint persistence**:

1. **Remove `NoopCheckpointStore`** from production wiring. It may remain as a
   test stub only (move to a `_test.go`-adjacent helper or delete and let tests
   inline a no-op). No production path constructs it.
2. **Wire `UpdateStageCheckpoint`** in the pipeline execute loop: after each
   stage handler `Execute` succeeds, persist
   `{stage, state:"completed", bytesDone, bytesTotal, attempt}` as JSON into
   `migration_stages.checkpoint_data` for that stage row (already created by
   `CreateStage`). For `trafficSwitchStage`, also persist the
   `manual_required` cutover checkpoint into `traffic_switch_config` + a
   `cutover_history` row (already done today — keep).
3. **Transactional, fail-closed:** the state advance (`SetMigrationStateContext`
   → `awaiting_cutover` / `completed`) and its checkpoint write happen in a
   single `db.BeginTx` transaction where both touch the DB. If the checkpoint
   write fails, the state is **not** advanced; the pipeline returns the
   checkpoint error and the migration stays in its pre-advance state (or goes
   `needs_manual_intervention` if it cannot safely stay). No "pause looks
   successful but checkpoint is lost."
4. **Order:** persist checkpoint **before** emitting the corresponding
   state/event. Concretely: `UpdateStageCheckpoint` (and cutover record) first;
   only on success `SetMigrationStateContext` + `OnProgress`.
5. **Resume:** on resume, `GetMigrationState` reads `awaiting_cutover` /
   `needs_manual_intervention` from the `state` column directly — these states
   are *persisted*, so a backend restart restores the exact state. No
   in-memory reconstruction. A migration in `awaiting_cutover` after restart
   stays `awaiting_cutover` until the operator commits.

**Residual limitation (documented):** byte-level / mid-transfer resume is not
implemented. Resume resumes at the **last fully-checkpointed stage boundary**;
an interrupted transfer re-runs from the stage's start (idempotent restore
flags + `StepStatusApplied` skip guard make this safe). Transfer-level
resume (rsync `--partial`) is Phase 2.

---

## 2. Per-finding fixes (exact files + acceptance)

### P0-6 — MySQL Detect always-true
- **File:** `internal/mod/migration/database_adapter.go:238`
- **Fix:** `pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1; echo yes`
  → `(pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1) && echo yes`.
  Match `postgresMigrator.Detect` semantics (line 182).
- **Test:** `TestMySQLDetectFalseWhenAbsent` — mock SSH returns empty output → `Detect` returns `false`.

### P0-11 — Mongo legacy `mongo` → `mongosh` + fallback
- **File:** `database_adapter.go:309,341`
- **Fix:** add `mongoShell(ssh)` helper that runs `command -v mongosh` and
  returns `"mongosh"` if present else `"mongo"` (with a redacted warning event,
  never silent). Cache per-client for the run. Use it in `ListDatabases` and
  `DropDatabaseCommand`.
- **Test:** `TestMongoShellPrefersMongosh` (mock returns mongosh present →
  command uses `mongosh`), `TestMongoShellFallback` (absent → uses `mongo`,
  warning emitted). Cross-check: `replication.go:531,539` already use
  `mongosh` — consistent.

### P0-7 — Redis `|| true` mask + health fail-closed
- **File:** `database_adapter.go:394`
- **Fix:** remove `|| true` from the restart chain. After restart, run
  `redis-cli PING`; require `PONG`. Else return error (restore failed). No
  silent success.
- **Test:** `TestRedisRestoreFailsOnBadHealth` — mock PING returns `""` →
  `RestoreCommand` chain is structured so a failed PING surfaces failure
  (assert the command no longer ends in `|| true`; assert health-gate present).

### P0-8 — Redis password argv → `REDISCLI_AUTH` env
- **File:** `database_adapter.go:408-413` (+ call sites 360,382,390,402)
- **Fix:** delete `redisAuth()` `-a` builder. Add `redisEnv(c)` prefix
  `REDISCLI_AUTH=<quoted>` (mirrors `pgEnv`/`mysqlEnv`). Replace all
  `redis-cli ... <redisAuth>` sites with `redisEnv(c) redis-cli ...`. Password
  leaves argv.
- **Test:** `TestRedisNoPasswordInArgv` — generated command contains no `-a`
  and no password literal; contains `REDISCLI_AUTH=`. Also assert across all
  four call sites (List/Dump/Restore/Drop).

### P0-10 — Volume path injection
- **File:** `internal/mod/planner/bridge.go:266,279,282-283,310`
- **Fix:** add `validateVolName(string) error` rejecting anything outside
  `[A-Za-z0-9_.:/-]`; call before the loop. Wrap `vol`, `sourcePath`,
  `destPath`, `ContainerName` in `shared.ShellQuote` in every shell
  `fmt.Sprintf` (tar/mkdir/rm/test). Defense-in-depth: ShellQuote closes
  injection; validator makes the failure loud.
- **Test:** `TestVolumeNameInjectionRejected` — `foo; rm -rf /` → error.
  `TestVolumePathShellQuoted` — clean name produces quoted args.

### P0-9 — ExecWithStdin 30s kill (stream-safety)
- **File:** `internal/mod/ssh/client.go:642`
- **Fix:** `ExecWithStdin` must **not** apply `timeouts.Command` as a hard
  wall-clock ceiling. Use only the parent `ctx` for lifetime + a new
  **configurable inactivity timeout** (`TimeoutConfig.Inactivity`, default 0 =
  disabled). **Inactivity reset signal:** any successful stdin write, any
  stdout/stderr bytes, or a remote progress marker resets the inactivity
  timer. On inactivity timeout → close session, return typed
  `ErrInactivityTimeout`. Long restores with steady flow never trip it.
- **Test:** `TestExecWithStdinOutlastsThirtySeconds` (mock steady stdin flow;
  assert no 30s ceiling applied — the parent ctx, not `Command`, bounds life).
  `TestExecWithStdinInactivityTimeout` (stall → `ErrInactivityTimeout`).

### P0-4 — SFTP 5-min hard timeout
- **File:** `internal/mod/ssh/model.go:29` (+ `client.go:713,758`)
- **Fix:** raise `DefaultTimeouts.FileTransfer` `5 * time.Minute` →
  `30 * time.Minute` as an **interim compatibility value**, not "large file
  solved." Keep configurable. Document: this remains relay/SFTP behavior;
  streaming (Phase 2) removes the bound.
- **Test:** `TestFileTransferTimeoutConfigurable` — client with
  `TimeoutConfig.FileTransfer = 2h` honors it (not the 30m default).

### P0-5 — ExecContext OOM (bounded capture)
- **File:** `internal/mod/ssh/client.go:361`
- **Fix:** new `internal/mod/ssh/boundedio.go` — capped writer: stdout cap
  1 MiB, stderr cap 256 KiB. On overflow, set a truncation flag and return a
  **typed `ErrOutputLimitExceeded`**, and **terminate/clean up the remote
  command** (close session) where possible — do not silently truncate output a
  parser relies on. `ExecContextWithTimeout` uses the bounded writers instead
  of raw `bytes.Buffer`. Metadata commands (intended use) stay tiny; the cap
  is a guard against a multi-GB dump misrouted through `ExecContext`.
- **Test:** `TestExecContextBoundedOutput` — output > cap →
  `ErrOutputLimitExceeded`, no unbounded growth.
  `TestExecContextSmallOutputUnaffected` — small output returned verbatim.

### P0-1 — Split-brain replication rollback (fail-closed, exact rules)
- **Files:** `internal/mod/migration/replication.go:319,424,495`; new
  `internal/mod/migration/topology.go`
- **Rules (non-negotiable):**
  - Topology probe runs **before any mutating rollback command**.
  - **Prohibited this pass:** `RESET SLAVE`/`RESET REPLICA ALL`,
    `standby.signal` removal, automatic promotion, automatic demotion.
  - **Permitted only after role+writability checks pass:** Redis `REPLICAOF`
    re-point (target must be confirmed `slave`/`replica`, source confirmed
    `master`).
  - **Probe failure / source unreachable / target unreachable / unexpected
    role / both writable → `StateNeedsManualIntervention`.** No destructive
    command issued.
- **Probe logic** (`topology.go`):
  - MySQL: `SHOW VARIABLES LIKE 'read_only'` on both; target
    `SHOW REPLICA STATUS`/`SHOW SLAVE STATUS` (must be replica); source
    `read_only=OFF` and not a replica.
  - PostgreSQL: `SELECT pg_is_in_recovery()` — target `t`, source `f`.
  - Redis: `INFO replication` — target role `slave`/`replica`, source `master`.
- **`rollback*` rewrite:** call probe; if not safe → return
  `ErrUnsafeTopology`; caller sets `StateNeedsManualIntervention` + runbook
  note. If safe (Redis only): re-point target; others → still
  `NeedsManualIntervention` (destructive shortcuts prohibited this pass).
- **Tests:**
  - `TestRollbackSourcePrimaryTargetReplicaSafe` (Redis re-point allowed)
  - `TestRollbackBothWritableFailsClosed` (→ NeedsManualIntervention)
  - `TestRollbackSourceUnreachableFailsClosed`
  - `TestRollbackTargetUnreachableFailsClosed`
  - `TestRollbackUnknownTopologyFailsClosed`

### P0-2 / regression-1 — Honest rollback terminal state
- **Known bug:** automatic rollback could reach `StateRolledBack` even when a
  rollback step failed. The current `pipeline.go:776-783` path *does* set
  `RollbackDegraded` and return early on errors — verify it is the only
  rollback completion path and that `recovery.go:277-297` `finalState` honors
  it.
- **Fix:** aggregate rollback step results explicitly. If **any** rollback
  step fails → `StateRollbackDegraded` or `StateNeedsManualIntervention`,
  **never `StateRolledBack`**. The success-path `Transition(StateRolledBack)`
  at `pipeline.go:786` runs only when `len(rollbackErrors)==0`.
- **Tests:** `TestPartialRollbackEndsDegraded` (one step fails →
  `RollbackDegraded`), `TestFullRollbackEndsRolledBack` (all succeed →
  `RolledBack`), `TestTopologyAmbiguousRollbackEndsManualIntervention`.

### P0-2 / regression-2 — ForceTransition audit (exact inventory)
All 11 callers (excl. definition):

| Caller | File:line | Verdict |
|---|---|---|
| `ForceTransition(StateCommitted)` | `pipeline.go:414` | **REMOVE.** Replace with validated `Transition` gated on a verified cutover record; cutover path must not auto-complete. |
| `ForceTransition(StateRolledBack)` | `pipeline.go:787` | **REMOVE.** Replace: if `Transition` fails, the from-state was unexpected → `SetMigrationStateContext(NeedsManualIntervention)`, never force. |
| `ForceTransition(StateRolledBack)` | `pipeline.go:1006` | **REMOVE** (same as above). |
| `ForceTransition(StateRolledBack)` | `engine.go:627` | **REMOVE** (same). |
| `ForceTransition(StateRollback)` | `pipeline.go:932` | **RESTRICT + document.** Recovery-correction (moving a stuck migration into rollback). Keep, add comment + test proving it cannot bypass cutover rules (it only enters rollback, never `Committed`). |
| `ForceTransition(StateRollback)` | `engine.go:579` | **RESTRICT + document.** Same. |
| `ForceTransition(StateFailed)` | `pipeline.go:897,925` | **RESTRICT + document.** Failure-marking; acceptable. Add audit comment. |
| `ForceTransition(StateFailed)` | `engine.go:561` | **RESTRICT + document.** Same. |
| `ForceTransition(StateInterrupted)` | `pipeline.go:910` | **RESTRICT + document.** Recovery only. |
| `ForceTransition(StateInterrupted)` | `engine.go:644` | **RESTRICT + document.** Same. |

- **Rule:** removed callers → validated `Transition` or explicit
  `SetMigrationStateContext(NeedsManualIntervention)`. Remaining callers →
  audit comment marking recovery/admin-only + a test
  (`TestForceTransitionCannotBypassCutover`) proving the restricted callers
  cannot reach `Committed` or skip `AwaitingCutover`.

### P0-2 — Cutover fail-closed (AwaitingCutover, required outcome)
- **Files:** `pipeline.go:1647` (`trafficSwitchStage`), `pipeline.go:414`,
  `pipeline_handler.go` (cutover commit endpoint), `state.go`.
- **Behavior:**
  1. After `trafficSwitchStage` records `manual_required`, the pipeline
     transitions to `StateAwaitingCutover` (persisted) and returns a sentinel
     `ErrAwaitingCutover`. The execute loop treats this as a **clean stop**,
     not a failure. It does **not** advance to Observing/Committed/Completed.
  2. `StateAwaitingCutover` survives backend restart (persisted in `state`
     column; `GetMigrationState` reads it directly).
  3. Only an explicit operator action may leave `AwaitingCutover`:
     `POST /api/migrations/:id/cutover/commit`.
  4. **Commit endpoint behavior:** verify the cutover record's `switch_state`
     is no longer `manual_required` (operator must flip it via the existing
     traffic-switch confirm path, or a new minimal confirm flag). While no
     safe commit action exists, the endpoint **rejects** commit with a
     **structured error** (`{error:"cutover_not_confirmed", ...}`) and
     transitions to `StateNeedsManualIntervention` if the operator aborts.
     It **never** uses `ForceTransition(StateCommitted)`.
  5. If the current architecture prevents a safe commit, **block earlier**:
     transition to `StateNeedsManualIntervention` instead of leaving
     auto-advance in place. No silent auto-advance to `Committed` survives
     this pass.
- **Tests:**
  - `TestManualRequiredStopsAtAwaitingCutover` (no advance to
    Observing/Committed)
  - `TestAwaitingCutoverSurvivesRestart` (persist + reload → same state)
  - `TestCommitRejectedWhileManualRequired` (structured error, no
    `Committed`)
  - `TestCommitRejectedDoesNotForceTransition`

---

## 3. Component / file changes per commit

### Commit 1 — DB adapter correctness/security
- `internal/mod/migration/database_adapter.go` (MySQL detect, mongosh, redis
  env+health, redisAuth removed)
- `internal/mod/planner/bridge.go` (volume path injection — grouped here
  per user commit order item 1: "quoted volume paths")
- Tests: `internal/mod/migration/database_p0_test.go`,
  `internal/mod/planner/bridge_p0_test.go`

### Commit 2 — Long-command streaming + bounded output
- `internal/mod/ssh/client.go` (ExecWithStdin inactivity, ExecContext bounded)
- `internal/mod/ssh/model.go` (FileTransfer 30m, Inactivity field)
- `internal/mod/ssh/boundedio.go` (new)
- Tests: `internal/mod/ssh/boundedio_test.go`, `client_p0_test.go`

### Commit 3 — Rollback topology guard + honest terminal state
- `internal/mod/migration/topology.go` (new)
- `internal/mod/migration/replication.go` (rollback* fail-closed)
- `internal/mod/migration/pipeline.go` (partial-rollback aggregation,
  honest terminal state)
- Tests: `internal/mod/migration/topology_test.go`,
  `internal/mod/migration/rollback_p0_test.go`

### Commit 4 — Persisted checkpoint wiring
- `internal/mod/migration/pipeline.go` (loop: `UpdateStageCheckpoint` after
  stage success)
- `internal/mod/migration/pipeline_repo.go` (wire existing method; tx)
- `internal/mod/migration/progress.go` (remove `NoopCheckpointStore` from
  production; test-only stub)
- Tests: `internal/mod/migration/checkpoint_p0_test.go`

### Commit 5 — AwaitingCutover / NeedsManualIntervention + endpoint guard
- `internal/mod/migration/state.go` (new states, transitions, persistence)
- `internal/mod/migration/pipeline.go` (trafficSwitchStage → AwaitingCutover)
- `internal/mod/migration/pipeline_handler.go` (commit endpoint reject)
- `internal/mod/migration/api.go` (or equivalent — structured error)
- Tests: `internal/mod/migration/cutover_p0_test.go`

### Commit 6 — ForceTransition audit/removal/restriction
- `internal/mod/migration/pipeline.go`, `engine.go` (remove 4, restrict +
  comment 7)
- Tests: `internal/mod/migration/force_transition_p0_test.go`

### Commit 7 — Full regression suite, docs, matrix
- `docs/known-limitations.md` (transfer-level resume = Phase 2;
  FileTransfer 30m interim; no live replication/fencing)
- `docs/p0-matrix.md` (finding → fix → files → test → status → residual risk)
- Run full suite before + after; record results.

---

## 4. Endpoint behavior (AwaitingCutover + commit rejection)

- `POST /api/migrations/:id/cutover/commit`:
  - Load `GetMigrationState`. Must be `StateAwaitingCutover`; else 409
    `{"error":"not_awaiting_cutover"}`.
  - Load `traffic_switch_config.switch_state`. If `manual_required` →
    **reject** 409 `{"error":"cutover_not_confirmed","detail":"operator must confirm traffic moved to target"}`.
    Do **not** transition. Do **not** `ForceTransition`.
  - Only if `switch_state != manual_required` (operator confirmed):
    `Transition(StateCommitted)` (validated). On transition failure →
    `StateNeedsManualIntervention`.
- `GET /api/migrations/:id` returns `state` string
  (`awaiting_cutover`/`needs_manual_intervention`) — test asserts the string.

---

## 5. Exact tests + acceptance criteria per finding

| Finding | Test file | Acceptance |
|---|---|---|
| P0-6 | `database_p0_test.go` | `mysqlMigrator.Detect` false when pgrep empty |
| P0-7 | `database_p0_test.go` | redis restore chain has no `\|\| true`; health-gate present; bad PING → failure |
| P0-8 | `database_p0_test.go` | no `-a`/password in any redis command; `REDISCLI_AUTH=` present (all 4 sites) |
| P0-11 | `database_p0_test.go` | `mongosh` preferred; `mongo` fallback only when mongosh absent + warning |
| P0-10 | `bridge_p0_test.go` | `foo; rm -rf /` rejected; clean names shell-quoted |
| P0-5 | `boundedio_test.go`/`client_p0_test.go` | output > cap → `ErrOutputLimitExceeded`; small output verbatim |
| P0-9 | `client_p0_test.go` | ExecWithStdin outlasts 30s under steady flow; stall → `ErrInactivityTimeout` |
| P0-4 | `client_p0_test.go` | custom `FileTransfer` honored (not 30m) |
| P0-1 | `topology_test.go`/`rollback_p0_test.go` | 5 topology cases (safe/both-writable/src-down/tgt-down/unknown) → correct fail-closed |
| P0-2 honest | `rollback_p0_test.go` | partial → `RollbackDegraded`; full → `RolledBack`; ambiguous → `NeedsManualIntervention` |
| P0-2 cutover | `cutover_p0_test.go` | manual_required stops at `AwaitingCutover`; survives restart; commit rejected; no ForceTransition(Committed) |
| P0-3 checkpoint | `checkpoint_p0_test.go` | stage checkpoint persisted; write failure → no state advance; restart restores state |
| ForceTransition | `force_transition_p0_test.go` | removed callers gone; restricted callers cannot reach `Committed` |

Full suite (`go test ./...`) green before commit 1 baseline and after commit 7.

---

## 6. Residual limitations (stated)

1. **No byte-level / mid-transfer resume.** Resume restarts at the last
   fully-checkpointed stage boundary; interrupted transfers re-run from stage
   start (safe via idempotent flags + `StepStatusApplied`). Transfer-level
   resume (rsync `--partial`) = Phase 2.
2. **No live replication / fencing / automatic traffic switching.** Zero
   downtime is not claimed. Replication rollback re-points only confirmed
   Redis replicas; MySQL/PG destructive rollback shortcuts remain prohibited
   (Phase 2 fencing).
3. **`FileTransfer` 30m is an interim SFTP/relay bound**, configurable, not
   "large file solved." Streaming (Phase 2) removes it.
4. **Mongo `mongo` fallback** retained for Mongo 4.x; dropped when 5.x is the
   floor.
5. **`AwaitingCutover` commit** requires an operator-confirmed
   `switch_state`; no automatic commit exists or is planned this pass.
