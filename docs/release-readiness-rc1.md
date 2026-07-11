# Release Readiness Report — v1.5.0-rc.1 (Phase 1 P0 safety baseline)

**Date:** 2026-07-11
**Tag:** `v1.5.0-rc.1` (annotated) → commit `0f8512c`
**Baseline HEAD:** `0f8512ca9f74befa8a3271cc1c7517ffd4183a23`

## Decision: **NO-GO**

The release candidate is **not** shippable as-is. One safety defect was found
during failure-injection validation that fits the explicit NO-GO trigger
("false-success behavior"). A focused remediation plan is below. No Phase 2
work is bundled. Phase 2 remains deferred pending re-validation after the fix.

---

## 1. Exact commit / tag / version

| Ref | Value |
|---|---|
| Tag | `v1.5.0-rc.1` |
| HEAD | `0f8512c` |
| P0 commit chain | `013f96c` `0929528` `175a3bd` `937569b` `d8a56eb` `c2dd87b` `0f8512c` |

---

## 2. Commands run and results

| Command | Result |
|---|---|
| `go build ./...` | PASS (exit 0) |
| `go vet ./...` | PASS (exit 0) |
| `go test -count=1 ./...` | PASS (all packages) |
| `go test -race ./internal/mod/migration/` | PASS (race-clean, 9.98s) |
| `go test -race ./internal/mod/ssh/` | PASS (race-clean, 44.3s) |
| `go test -race ./internal/mod/planner/` | PASS (race-clean, 1.16s) |
| `go test -race ./internal/jobengine/` | PASS (race-clean, 19.4s) |
| `cd web && npm run check` | PASS (0 errors, 0 warnings) |
| Static secret scan (grep across `internal/`) | PASS — no secrets on argv/logs/errors/API/events |

### Static secret scan detail
- `redis-cli -a` / `--pass` / `--password` on argv: **none**
- `PGPASSWORD=` / `MYSQL_PWD=` literals: **none** (only `${ENV}`-assignment via `shared.ShellQuote`)
- `log.Printf`/`Println` containing password/secret: **none**
- `DBCredentials.Password` persisted to step data: **no** (`DatabaseApplier.Apply` writes no creds)
- API `GET /config/:id`: password redacted to `"set"`; encrypted at rest
- Event bus sanitizes `Message`/`Source`/`Details` via `shared.SanitizeString`/`SanitizeJSONRawMessage`

---

## 3. Integration environment details

- **Host:** Linux 7.0.12-1-pve, single node. systemd `meshium.service`, port 9527.
- **Build toolchain:** Go (module `meshium`), SvelteKit (`web/`).
- **Test harness:** in-process sqlite (`newTestRepo`), mock SSH (`mockSSH`),
  and an in-process live SSH server harness (`internal/mod/ssh/*_test.go`).
- **Live database pairs: NOT available** in this environment. Scenarios
  requiring real MySQL/PostgreSQL/MongoDB/Redis pairs (or a live DB on the
  meshium host) are marked **ENV-BLOCKED** below.

---

## 4. Production-like integration / failure-injection matrix

| # | Scenario | Status | Evidence |
|---|---|---|---|
| 1 | MySQL Detect false when mysqld absent | ✅ PASS | `TestMySQLDetectFalseWhenAbsent` |
| 2 | Redis restart failure → error, not false success | ✅ PASS | `TestRedisRestoreNoSilentFailure` |
| 3 | Redis password not on argv/logs/events/errors | ✅ PASS | `TestRedisNoPasswordInArgv` + static scan |
| 4 | Adversarial path inputs blocked | ✅ PASS | `bridge_p0_test.go` (`foo; rm -rf /` rejected; clean names shell-quoted) |
| 5 | Restore >30s completes when active | ⚠ GAP | No live test; `Inactivity=0` disables the bound, so a steady-flow restore is not bounded by 30s by design. `TestInactivityFieldConfigurable` confirms the field. **ENV-BLOCKED** for an end-to-end live proof. |
| 6 | Inactivity timeout kills a genuinely stalled restore | ⚠ GAP | `ErrInactivityTimeout` typed + field config tests pass; **no live test** that injects a real stall against a live SSH server. **ENV-BLOCKED** for end-to-end. |
| 7 | Output > cap → `ErrOutputLimitExceeded` + remote cleanup | ❌ **FAIL** | See Finding F-1 below. |
| 8 | Configured `FileTransfer` timeout honored | ✅ PASS | `TestFileTransferTimeoutConfigurable` / `TestFileTransferZeroFallsBackToDefault` |
| 9 | Rollback topology: 5 cases | ✅ PASS | `TestRollbackRedisSourcePrimaryTargetReplicaSafe`, `…BothWritableFailsClosed`, `…TargetUnreachableFailsClosed`, `TestRollbackMySQLAlwaysFailsClosed`, `…Postgres`, `…Mongo`, `…UnknownEngine` |
| 10 | No topology failure executes prohibited rollback mutations | ✅ PASS | MySQL/PG/Mongo always fail closed; Redis re-points only when `safeForRepoint()` |
| 11 | Rollback partial failure → degraded/manual, never rolled_back | ✅ PASS | `TestPartialRollbackTerminalDecision` (3 sub), `TestFullRollbackTerminalDecision`, `TestTopologyAmbiguousRollbackEndsManualIntervention`, `TestCancelMigrationMarksRollbackDegradedWhenSomeStagesFail` |
| 12 | Checkpoint write failure prevents state advance | ✅ PASS | `TestStageCheckpointWriteFailureFailsClosed` |
| 13 | Backend restart during AwaitingCutover restores AwaitingCutover | ✅ PASS | `TestAwaitingCutoverRoundTripsThroughStateColumn` (state persisted via dual-write; restart reads it back) |
| 14 | manual_required cannot auto-advance to Observing/Committed/Completed | ✅ PASS | `TestTrafficSwitchStageStopsAtAwaitingCutover`, `TestAwaitingCutoverNotTerminalManualInterventionIsTerminal` |
| 15 | Commit endpoint rejects while manual_required/AwaitingCutover | ✅ PASS | `TestCommitRejectedWhileManualRequired`; handler emits structured `409 cutover_not_confirmed` |
| 16 | ForceTransition cannot bypass cutover safety | ✅ PASS | `TestForceTransitionCannotBypassCutover`, `TestRemovedUnsafeForceTransitionsGone`; 3 unsafe callers removed, 7 restricted |
| 17 | MySQL/Redis/Mongo command + auth behavior | ⚠ PARTIAL | Command-shape tests pass (`TestMongoShellPrefersMongosh`, `TestRedisLag*`). **ENV-BLOCKED**: no live DB pairs to exercise real auth handshake + restart. |
| 18 | Output-cap breach cleans up the remote command | ❌ **FAIL** | Same root cause as #7 — Finding F-1. |

---

## 5. Finding F-1 (NO-GO blocker)

**Title:** Output-cap breach does NOT terminate the remote command — bounded
capture is a memory guard only, not a safety limit; the remote process keeps
running and the caller is blocked until it ends naturally.

**Location:** `internal/mod/ssh/client.go` `ExecContextWithTimeout`
(lines ~367-382) + `internal/mod/ssh/boundedio.go` `boundedWriter.Write`.

**What the code does:**
- `boundedWriter.Write` always returns `(n=len(p), nil)` even when overflowed
  — it never errors the SSH stdout capture, so the ssh session sees a healthy
  writer.
- `ExecContextWithTimeout` calls `session.Run(cmd)` which **blocks until the
  remote command exits naturally**. Only after `Run` returns does it check
  `Overflowed()` and return `ErrOutputLimitExceeded`.
- The `closeSession` goroutine only fires on `execCtx.Done()` (timeout/ctx
  cancel) — **not** on overflow. A command that floods output past the cap
  keeps running on the remote host, with its output discarded, while
  `session.Run` blocks indefinitely (or until the ctx/timeout fires).

**Why it is a NO-GO trigger:**
- The doc comment promises "the remote command is terminated where possible"
  (`boundedio.go:12-13`) and the cap is documented as a guard against a
  "multi-GB dump misrouted through ExecContext" (`client.go:365-366`). In
  reality the cap bounds only meshium's memory; the remote process is not
  terminated on breach — the host resources the cap was meant to protect keep
  being consumed, and the caller is blocked (not promptly told). This is a
  false-success / unmet-safety-contract behavior, which the release rules
  flag as a stop condition.

**Repro (conceptual):** a remote command emitting >1 MiB on stdout while the
ctx has no deadline. Expected: prompt `ErrOutputLimitExceeded` + remote
killed. Actual: `session.Run` blocks until the remote exits on its own; the
caller receives the error only then, and the remote was never killed.

**Severity:** safety/availability. Not data-corrupting, but the cap is
advertised as a safety boundary it does not enforce.

---

## 6. Remediation plan (focused, before re-tagging)

Scoped to F-1 only. No feature work, no Phase 2, no new engines.

1. **Signal/cancel on overflow.** When `boundedWriter` flips to overflowed,
   have `ExecContextWithTimeout` observe it (e.g. an overflow channel or a
   polling goroutine) and call `closeSession()` + `cancel()` promptly —
   same pattern already used for `execCtx.Done()`. This terminates the remote
   session.
2. **Prompt error return.** After closing, return
   `&outputLimitError{stream}` immediately rather than waiting for `Run`.
3. **TDD.** Add a live-server test: a remote command that emits beyond the
   cap; assert `ErrOutputLimitExceeded` is returned within a small bound
   (e.g. <2s) and the remote session is closed (no goroutine leak —
   `TestNoGoroutineLeakAfterClose` pattern).
4. Re-run the full matrix (especially rows 5, 6, 7, 18) + `go test -race`.
5. Re-tag `v1.5.0-rc.2` and re-evaluate GO/NO-GO.

**Estimated size:** one method change in `client.go` + one test file. No
interface changes, no mock breakage (the change is internal to
`ExecContextWithTimeout`).

---

## 7. Tests not run and why

| Scenario | Why not run |
|---|---|
| Live MySQL/PostgreSQL/MongoDB/Redis dump→restore end-to-end | No live DB pairs in this environment. Command shapes + auth/env verified by unit tests; end-to-end deferred to a host with the engine pairs. |
| Restore >30s steady-flow end-to-end | Needs a live SSH host + large dataset. Field-config and disable-by-default verified; live proof ENV-BLOCKED. |
| Genuine inactivity-stall end-to-end | Needs a live SSH host that can stall. Typed error + field verified; live proof ENV-BLOCKED. |
| Backend *process* restart mid-AwaitingCutover | State persistence verified by round-trip test (`TestAwaitingCutoverRoundTripsThroughStateColumn`); a real `systemctl restart` re-read was not executed in this environment. |

---

## 8. Residual risks (after F-1 remediation)

1. **No live end-to-end DB engine validation.** All command/behavior coverage
   is unit-level against mocks. A production-like host with all four engines
   must run the matrix rows marked ENV-BLOCKED before ship.
2. **Operator guidance UI is tooltip-only.** `MigrationHeader` shows an ⚠ icon
   with a `title` tooltip for `awaiting_cutover`/`needs_manual_intervention`/
   `rollback_degraded`; a visible inline banner is recommended before ship so
   the operator sees the action without hovering.
3. **`FileTransfer` 30m** remains an interim bound; large PG/Redis SFTP dumps
   may exceed it. Documented, not solved.
4. **Mongo `mongo` fallback** retained for 4.x — behavior on a 4.x host is
   unit-tested but not live-validated here.
5. **Race coverage** is limited to the four packages with concurrency; the
   full `-race` sweep was not run on `internal/mod/auth`/`server` (long
   durations) — not P0-relevant but noted.

---

## 9. Recommendation

**NO-GO for v1.5.0-rc.1.** Apply the F-1 remediation, add the live-server
output-cap test, re-run the matrix + race, re-tag rc.2, and re-evaluate.
Phase 2 must not begin until rc.2 is GO and the report is reviewed and
explicitly approved.
