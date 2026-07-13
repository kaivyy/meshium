# Phase 2D — Release Report: Operability, Auditability, Observability, Release-Readiness

**Scope.** Make the Phase 1 / 2A / 2B / 2C results operable, auditable,
observable, operator-safe, and release-gate ready. Hard out-of-scope: new
replication engines, new traffic-switch providers, weakening the
single-tenant/fencing model, or any "zero downtime" claim. Immutable baselines
(Phase 1 / 2A / 2B / 2C) are preserved — no regression to the safety contract.

**Pattern.** Investigate → design decision → implement → test → report, slice by
slice, with one commit per slice.

---

## Slice → Commit map

| Slice | Commit | Summary |
|-------|--------|---------|
| 1 — Investigation + design | `f509658` | Gap matrix + design doc (`docs/superpowers/specs/2026-07-13-phase2d-*`) |
| 2 — Correlation IDs + event/audit identity | `9a34d9c` | `trace.go` propagation + `AuditEntry` evidence fields; 6 tests |
| 3 — Structured logging + central redaction | `6430c60` | `redactWriter`, `shared.LogCtx`, source sanitization; 5+ tests |
| 4 — Durable audit trail + diagnostic bundle | `c414f8a` | `BuildDiagnosticBundle` + `/api/pipeline/diagnostics`; 3 tests |
| 5 — REST idempotency + WS replay | `def2f22` | `Idempotency-Key` dedup; 2 tests |
| 6 — Operator recovery API/UX + runbooks | `b6c1bec` | `recoveryGuidance` + `/api/pipeline/recovery` + runbook; 2 tests |
| 7 — Concurrency caps + transfer-limit wiring | `3247d45` | global sem + per-server `(source,target)` resource lock (atomic claim) + bandwidth honesty warn; 8 tests |
| 8 — Dead-code cleanup + feature gating + docs | `0ebed66` | removed orphan `parseCategories`; API guardrail map for unsupported provider/replication; gate test + docs |
| 9 — Release suite + secret-leak gate | `HEAD` | audit-boundary sanitization + JSON key-aware redaction; consolidated release-gate secret test; this report |

## Tests per slice (migration package, non-integration)

| Slice | Test file(s) | #funcs | Key guards |
|-------|--------------|--------|------------|
| 2 | `trace_test.go` | 6 | correlation/request id propagation, audit inheritance |
| 3 | `logger_test.go`, `replication_test.go` | 5+ | message/attr redaction, cross-sink secret scan |
| 4 | `diagnostic_test.go` | 3 | aggregates durable state, redacts config, no fabrication |
| 5 | `idempotency_test.go` | 2 | key round-trips, audit inherits idempotency key |
| 6 | `recovery_guidance_test.go` | 2 | fail-closed NOT safe-to-resume; every state answered |
| 7 | `concurrency_test.go`, `concurrency_resource_test.go` | 8 | global cap blocks at N+1; resource lock excludes shared host; rsync `--parallel` |
| 8 | `feature_gate_test.go`, `guardrail_test.go` | 2 | `AutoCutoverDefault == false`; unsupported provider/mode rejected at API |
| 9 | `release_gate_test.go` | 1 | consolidated no-secret-in-any-sink gate (log + audit + bundle) |

Total: 58 test files in `internal/mod/migration/`; 2 integration-tagged
(`pg_cutover`, `mysql_cutover`, `//go:build integration`, require live Docker).

## Full release gate (all green)

- `go build ./...` → exit 0
- `go vet ./...` → exit 0
- `go test ./internal/...` → 15 packages `ok`, 0 FAIL
- `cd web && npm run check` (svelte-check) → 0 errors, 0 warnings

## Support matrix (authoritative, unchanged)

The honest capability boundary is `docs/known-limitations.md`. Phase 2D added no
new claims. Specifically preserved:

- Manual cutover (`autoCutover=false`) is the **default**; automatic commit is
  rejected (`409 cutover_not_confirmed`). Fenced cutover is **opt-in only**
  (`AutoCutoverDefault = false`, asserted by test).
- Fail-closed states (`needs_manual_intervention`, `awaiting_cutover`) are
  **never** auto-forwarded or auto-rolled-back; recovery guidance marks them
  `safeToResume: false`.
- No secret reaches logs / events / API / audit / exports. `#9` closed two
  concrete leaks the consolidated release-gate test surfaced: (a) `CreateAuditEntry`
  persisted raw free-text fields (`event_data`, `actor`, evidence summaries)
  without sanitizing — now sanitized at the persistence boundary; (b) a bare
  password value under a credential key (e.g. `databaseConfig.password`) leaked
  through `SanitizeJSONRawMessage` because the string patterns only matched
  `key=value`/`-p`/base64 shapes — now key-aware: any value under a secret-key
  name is redacted wholesale. Both verified by `TestReleaseGateNoSecretInAnySink`.
- No global "zero downtime" claim anywhere.

## Residual risks (carried, not introduced)

1. **Active path does not drive rsync.** The applier-replay path (`initialSyncStage`)
   replays collected metadata and never invokes the rsync-backed `SyncEngine`.
   `BandwidthLimit`/`ParallelTransfers` therefore do **not** throttle the live
   path; a runtime warning surfaces this so operators are not misled. The
   `--bwlimit`/`--parallel` flags are wired and tested for when that engine is
   driven.
2. **Transfer-limit wiring is config-only today.** Same as above — the flags
   are honored by `buildRsyncCommand` but the stage that would consume
   `syncConfigFromPipelineContext` is not yet live (documented `ponytail` in
   `pipeline.go`).
3. **Source freeze deferred** (pre-existing, Phase 2A residual): the fence
   lease serializes cutover but does not set a Postgres read-only GUC on the
   source. No-dual-writer holds on the happy path; a writer ignoring the lease
   could still write during the switch→promote window.
4. **Single lease lifetime** bounds a cutover (`FenceTTL - 10s` ≈ 4m50s); no
   renewal loop yet.

## Product wording (verified honest)

- UI/API/docs state downtime = observed cutover + switch window. No "zero
  downtime" headline.
- Recovery runbook (`docs/runbooks/operator-recovery.md`) carries an explicit
  **Not Supported** section mirroring `known-limitations.md`.
- All Phase2D commit messages describe only what was built; no capability
  inflation.

**Conclusion.** Phase 2D meets its brief: the migration engine is now
correlation-traceable, audit-durable, centrally redacted, idempotent,
recovery-guided, concurrency-bounded, and release-gate green — without
touching any immutable safety baseline.

## Ship / no-ship recommendation

**SHIP** Phase 2D for the documented scope. All release gates are green
(`go build ./...`, `go vet ./...`, full `./internal/...` test suite, migration
`-race` suite, and `svelte-check`). No immutable Phase 1/2A/2B/2C baseline
regressed; the safety contract (manual cutover default, fail-closed states,
fencing, traffic-ownership verification, integrity-checked transfer, restricted
`ForceTransition`, honest product wording) is intact. The two secret-leak
findings in slice 9 were **closed before ship**, not deferred.

Carry, do not block: residual risks #1–#4 (rsync not driven on the active
path, source-freeze deferred, single fence lifetime) are pre-existing Phase
2A/2B limitations, honestly documented in `known-limitations.md`, and outside
this slice's scope.
