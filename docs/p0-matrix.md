# P0 Safety Fixes — Delivery Matrix

> Phase 1 P0 safety pass. 7 commits, 13 P0 findings closed. Spec:
> `docs/superpowers/specs/2026-07-11-p0-safety-fixes-design.md`.

## Commit order

| # | Commit | Findings |
|---|--------|----------|
| 1 | `013f96c` DB adapter correctness + volume path injection | P0-6,7,8,10,11 |
| 2 | `0929528` long-command streaming + bounded output | P0-4,5,9 |
| 3 | `175a3bd` rollback topology guard + honest terminal state | P0-1, P0-2 (honest) |
| 4 | `937569b` persist stage checkpoint before advance | P0-3 |
| 5 | `d8a56eb` AwaitingCutover/NeedsManualIntervention + commit guard | P0-2 (cutover) |
| 6 | `c2dd87b` ForceTransition audit — remove unsafe, restrict rest | P0-2 (force) |
| 7 | (this) regression + failure-path tests + docs + matrix | all |

## Findings → tests → acceptance

| Finding | Test file | Acceptance criterion |
|---|---|---|
| P0-6 | `internal/mod/migration/database_p0_test.go` | `mysqlMigrator.Detect` false when pgrep empty |
| P0-7 | `internal/mod/migration/database_p0_test.go` | redis restore chain has no `\|\| true`; health-gate present; bad PING → failure |
| P0-8 | `internal/mod/migration/database_p0_test.go` | no `-a`/password in any redis command; `REDISCLI_AUTH=` present (all sites) |
| P0-11 | `internal/mod/migration/database_p0_test.go` | `mongosh` preferred; `mongo` fallback only when mongosh absent + warning |
| P0-10 | `internal/mod/planner/bridge_p0_test.go` | `foo; rm -rf /` rejected; clean names shell-quoted |
| P0-5 | `internal/mod/ssh/boundedio_test.go`, `client_p0_test.go` | output > cap → `ErrOutputLimitExceeded`; small output verbatim |
| P0-9 | `internal/mod/ssh/client_p0_test.go` | ExecWithStdin outlasts 30s under steady flow; stall → `ErrInactivityTimeout` |
| P0-4 | `internal/mod/ssh/client_p0_test.go` | custom `FileTransfer` honored (not 30m) |
| P0-1 | `internal/mod/migration/topology_test.go`, `rollback_p0_test.go` | 5 topology cases (safe/both-writable/src-down/tgt-down/unknown) → correct fail-closed |
| P0-2 honest | `internal/mod/migration/rollback_p0_test.go` | partial → `rollback_degraded`; full → `rolled_back`; ambiguous → `needs_manual_intervention` |
| P0-2 cutover | `internal/mod/migration/cutover_p0_test.go` | `manual_required` stops at `awaiting_cutover`; survives restart; commit rejected; no `ForceTransition(Committed)` |
| P0-3 checkpoint | `internal/mod/migration/checkpoint_p0_test.go` | stage checkpoint persisted; write failure → no state advance; restart restores state |
| ForceTransition | `internal/mod/migration/force_transition_p0_test.go` | removed callers gone; restricted callers cannot reach `committed` |

## Verification

```
go build ./...       # green
go vet ./...         # green
go test ./...        # green (all packages)
```

- Baseline `go test ./...` was green before commit 1 and remains green after
  commit 7.
- All P0 test files listed above exist and pass.

## Residual limitations

See `docs/known-limitations.md`. Phase 2 scope (live replication, fencing,
automatic traffic switching, byte-level transfer resume) is explicitly
deferred and must not be implied by any product/API/UI wording.
