# Phase 2D — Operability, Auditability, Observability, Release Readiness

> **Date:** 2026-07-13
> **Scope:** Make Phase 1, 2A, 2B, and 2C results operable, auditable, observable, operator-safe, and release-gate ready. **Hard out-of-scope:** new replication engines, new traffic-switch providers, changes to the proven safety contract, weakening single-tenant/fencing model, "zero downtime" claims.
> **Immutable baselines:** Phase 1, 2A, 2B, 2C. No regression of AwaitingCutover / NeedsManualIntervention, checkpoint persistence, topology fail-closed rollback, stream/output safety, fencing/lease enforcement, traffic ownership verification, transfer resume/reconcile/integrity, ForceTransition restrictions, or honest product wording.

## 1. Investigation findings (four inventories)

### 1.1 Observability inventory

**Existing:**
- `EventBus` (`event_bus.go`) already assigns a monotonic `Sequence` per migration and persists events; `handleGetEvents` (`pipeline_handler.go:1199`) serves `?after_seq=` replay. Good foundation.
- `MigrationEvent.CorrelationID` field **exists** (`event_bus.go:52`) and is persisted (`pipeline_repo.go:1388`) but is **never populated by any emitter** — correlation is dead today.
- A separate `Sequence` already exists on `WSMessageExtended` (`pipeline_models.go:486`) and the WS write loop increments it; reconnect streams persisted history (`streamPipelineHistory`, `pipeline_handler.go:210`). Replay exists but is **not sequenced against the durable events** end-to-end, and the WS `Sequence` namespace differs from `MigrationEvent.Sequence`.
- ~80 `log.Printf` sites across `engine.go`, `executor.go`, `handler.go`, `pipeline.go`, `pipeline_handler.go`, `recovery.go`, `sync.go`, `provision.go`, `pipeline_registry.go`, `file/service.go`. **All unstructured** (free-text `fmt`-style), none carry component/operation/correlation/migration_id, none redacted at source, and `shared.SanitizeLog` exists but is **used in only 1 place** (`shared/sanitizer.go:53`).
- SSH/remote-executor and DB command construction log via these `log.Printf` sites; some embed command lines (e.g. `engine.go:339` checkpoint failure, executor status failures). Commands themselves are built with `ShellQuote` so they are shell-safe, but **stderr/error payloads are not redacted** before logging.

**Gaps:**
- No structured logger abstraction (level/component/operation/correlation/migration_id/stage standard fields).
- `log.Printf` sites are noisy on retry loops (exponential backoff in `pipeline.go:335-360`) — unbounded volume risk.
- Correlation ID never generated or propagated.

### 1.2 Security & secret inventory

**Existing (good):**
- Central redaction boundary `shared.SanitizeString` + `shared.SanitizeJSONRawMessage` (`shared/sanitizer.go`) — JSON-aware, applied at: event `Emit` (`event_bus.go`), traffic switch `SanitizedConfig`/`SanitizedResult` (`nginx_switch.go`, `haproxy_switch.go`, `traffic_switch_common.go`), and `cutover_orchestrator.go` `sanitizeErr` + `CutoverOutcome` (persisted blob is sanitized).
- DB password redacted at API read (`redactDBConfig`, `pipeline_handler.go:109`) mirroring `server.Service.redactServer`; encrypted at rest via `shared.Encrypt` pattern.
- WS auth uses subprotocol token (`auth.WebSocketSubprotocolToken`, `pipeline_handler.go:74`) — auth validated on upgrade, including reconnects.

**Gaps:**
- `log.Printf` sites bypass redaction entirely — a failed command whose error string contains a password-shaped value would be logged raw. The production primitives use `ShellQuote` for args, but **error messages from remote execution are not guaranteed redacted**.
- `SanitizeLog` is effectively unused (1 call) — the redaction boundary is not the logger's boundary.
- No redaction unit tests for nested JSON, URLs, shell commands, wrapped errors, WS payloads, audit records, or exports.
- No secret scan in CI (only `govulncheck`, `ci.yml:130` — advisory; no `gosec`).
- Integration test files embed real-ish passwords in command strings (`mysql_cutover_integration_test.go`, `pg_cutover_integration_test.go`) — acceptable for tests but confirms the production code must never log those.

### 1.3 Recovery & UX inventory

**Existing:**
- Full state machine: migration `Status*` (`model.go:17-25`), step `StepState*` (`job.go:15-24`), pipeline `StageState*` + `PipelineStageName` (`pipeline_models.go:11-75`), cutover `CutoverSubState` (`cutover_substate.go:17-27`) with a legal transition map and restart-reconciliation semantics.
- `AuditEntry` (`pipeline_models.go:322`) table + `CreateAuditEntry`/`GetAuditTrail` (`pipeline_repo.go:1093/1109`) already exist and are written from `pipeline.go` at 8 call sites (pause/resume/retry/cancel/rollback/cutover stages/commit). **Audit is partially wired but not comprehensive.**
- `buildSession` (`pipeline_handler.go`) assembles a rich `PipelineSession` with `Events`, `CutoverHistory`, `AuditTrail` (`pipeline_models.go:523`) — but **not exposed as a dedicated recovery endpoint**; only embedded in the WS session.
- WS correctly: validates auth on reconnect, streams persisted history, supports heartbeats.

**Gaps:**
- No dedicated REST "recovery status" endpoint that returns, for a blocked state: reason, persisted state, last success/failed step, topology status, traffic ownership status, lag/health, transfer verification, fence lease status, safe vs prohibited actions, runbook link, correlation + audit refs.
- `AwaitingCutover` / `NeedsManualIntervention` are labels, not actionable surfaces (no structured checklist/guidance payload).
- No external idempotency keys on mutating REST actions (only traffic switches have `IdempotencyKey`).
- `IdempotencyKey` is in-memory cache on switchers (rebuilt empty on restart) — not durable, so restart + reconnect could duplicate a switch.

### 1.4 Test & release inventory

**Existing:**
- 311 migration-package unit tests passing; 2 live Docker integration suites (`pg_cutover_integration_test.go`, `mysql_cutover_integration_test.go`, `//go:build integration`).
- Event bus tests, pipeline handler tests, replay tests, cutover orchestrator tests, engine-dispatch tests all present.
- CI runs `go build`, `go vet`, `go test`, `govulncheck` (`.github/workflows/ci.yml`).

**Gaps (missing fixtures per Phase2D mandate):**
- No restart/replay integration tests (simulate backend crash mid-state and verify resume).
- No concurrent-migration / conflicting-same-resource tests.
- No network-failure-injection tests (source/target unreachable).
- No fencing-authority-failure injection.
- No secret-redaction test suite (unit-level assertions across sinks).
- No idempotency conflict-rejection tests at the API boundary.
- No operator-recovery endpoint tests.
- No dedicated race-detector test target (race coverage not enforced).
- `gosec` not in CI; no automated secret scan of logs/test artifacts.

## 2. Design decisions (summary)

1. **Build on existing primitives; do not rewrite.** `EventBus.Sequence`, `MigrationEvent.CorrelationID`, `AuditEntry`, WS history replay, `shared.Sanitize*`, traffic `IdempotencyKey` already exist. Phase 2D closes the gaps: populate correlation, add a structured-log wrapper around `log`, apply `Sanitize*` as the logger boundary, broaden audit coverage, make idempotency durable, add a recovery endpoint, add tests. **Why:** minimizes regression risk to proven safety invariants; YAGNI over greenfield.

2. **One correlation ID at each external boundary; thread via `context.Context`.** New `ctxkey` package + `migration.WithCorrelation(ctx)` helper. Generated at: REST handler entry, WS command dispatch, migration creation, pipeline start/resume/retry, cutover action, rollback, background reconcile. **Distinct IDs** (not fresh per internal call): request ID (per HTTP/WS call), migration ID, pipeline ID, transfer ID, lease/fence generation, operator-action ID. Persisted on events + audit + structured logs. **Why:** needed for post-incident reconstruction without leaking secrets into context.

3. **Structured logging = thin wrapper over `log/slog` with mandatory fields + `SanitizeLog` as the emit boundary.** Keep `log.Printf` call sites working but route through `slog` with `component`/`operation`/`correlation_id`/`migration_id`/`stage`/`engine`/`provider`/`transfer_id`/`attempt`/`state`/`error_category`/`error_summary`. Severity mapping INFO/WARN/ERROR/DEBUG. High-frequency progress (transfer bytes/ETA loops) sampled/rate-limited. **Redaction is mandatory at emit**: the wrapper calls `shared.SanitizeString` / `SanitizeJSONRawMessage` before writing — so no sink ever receives a raw secret. **Why:** satisfies §C without scattering redaction calls across 80 sites.

4. **Durable idempotency on all mutating REST actions.** Add `IdempotencyKey` to the mutating request bodies + a `idempotency_keys` table (migration_id, key, request_hash, result_summary, created_at). Duplicate key + identical request hash → return prior outcome (no re-execution). Different request hash → 409 conflict. Key generated server-side if absent (so it stays optional for callers). Switchers' in-memory cache migrates to this table. **Why:** prevents duplicate cutover/promote/rollback on reconnect/retry.

5. **Recovery endpoint + actionable blocked-states.** New `GET /api/pipeline/{id}/recovery` returning a `RecoveryStatus` struct assembled from existing persisted state (state, stage history, checkpoints, fence lease gen, topology obs, lag, traffic verify, transfer verify, safe/prohibited actions, runbook slug, correlation+audit refs). UI renders distinct states; "retry" only offered when safe (never past AwaitingCutover without explicit confirm). **Why:** makes manual-intervention actionable, not a dead label.

6. **Concurrency/resource locks + parallel/bandwidth wiring.** `ParallelTransfers` (default 4) and `BandwidthLimit` already parsed (`pipeline_handler.go:543-544`, `sync.go:39-40`) and applied to rsync (`--bwlimit`, `sync.go:350`). Add: a global migration resource-lock keyed by `(source_id, target_id)` to reject two migrations mutating the same resource concurrently; bounded worker pool; cancellation propagation; document that bandwidth is honored only for rsync-backed transfers and **explicitly rejected/warned** for transfer strategies that can't honor it. **No new concurrency that could corrupt checkpoints.** **Why:** honors config honesty requirement; prevents dual-mutation.

7. **Dead-code + gating cleanup.** Inventory and quarantine: unused `CutoverEngine`/`FreezeManager`/`TrafficSwitchEngine` wiring, duplicate command builders, raw logger paths, stale claims. Unsupported engines/providers already fail closed (Phase 2C) — add a runtime guardrail map (`supportedEngines`/`supportedProviders`) consulted at API validation so the UI/API cannot select them. Move any needed-but-unused code behind `// experimental` boundaries. Update `known-limitations.md`.

8. **Health/readiness + diagnostic bundle.** Extend the existing `/api/pipeline/health/` with a readiness gate: fail readiness if persistence (repo) or fencing authority is unavailable (would cause false success). Add `GET /api/pipeline/{id}/diagnostics` returning a sanitized bundle (build/version, feature flags, support matrix, config-presence-without-secrets, recent sanitized events/audit, health, test metadata). No secrets/keys/raw output. **Why:** support bundle for incident review without leaking.

9. **Release suite (test pyramid).** Unit tests for correlation/propagation, log fields, redaction across sinks, audit ordering/immutability, event replay/gap detection, idempotency, unsafe-action rejection, state/permission gating, concurrency/lock, bandwidth config, unsupported-blocked. Integration (Docker) for restart/replay, duplicate mutations, WS disconnect/reconnect, audit across restart, correlation trace, concurrent distinct + conflicting same-resource, parallel-limit, support-matrix enforcement, health/readiness. Failure-injection: persistence/event/fencing/timeout/checksum/lag/traffic/restart/redaction-attempt/seq-gap/stale-idempotency/conflicting-cutover/disk. Security: secret scan of logs/API/WS/DB/exports/args, injection tests, authz checks, `govulncheck` + add `gosec`. E2E scenarios per the mandate.

## 3. Observability / security / recovery gap matrix

| Concern | Today | Gap | Phase 2D action | Risk |
|---|---|---|---|---|
| Correlation ID | field exists, unused | never generated/persisted/propagated | context-thread + generate at boundaries + persist on events/audit/logs | high (no reconstruction) |
| Structured logging | 80 `log.Printf`, 1 `SanitizeLog` | no levels/fields/redaction-at-emit | `slog` wrapper + mandatory `Sanitize*` emit boundary | high (secret leak via logs) |
| Redaction boundary | `shared.Sanitize*` at events/traffic/cutover | not the logger boundary; no tests | logger calls sanitizer; redaction test suite | high |
| Audit trail | `AuditEntry` + 8 writes | not comprehensive; no actor/result/fence-gen/conflict-key | broaden coverage + structured fields + immutable append | med |
| Event replay | WS history + `after_seq` | WS seq ≠ durable seq; no gap detection | align WS seq to durable; gap → REST re-fetch | med |
| API idempotency | only traffic switches (in-memory) | not durable; not on REST mutators | durable `idempotency_keys` table + REST coverage | high (dup mutation) |
| WS reconnect/replay | auth-validated, history streamed | no monotonic guarantee vs durable; no gap response | sequence-aligned replay + explicit gap response | med |
| Operator recovery | `PipelineSession` has data, not surfaced | no REST recovery endpoint; blocked states not actionable | `GET /recovery` + actionable blocked-state payload | med |
| Concurrency/resource lock | none | two migrations can touch same resource | `(source,target)` lock + bounded workers | med |
| Parallel/bandwidth | parsed + rsync `--bwlimit` | not documented; un-honorable paths silent | document + explicit reject/warn | low |
| Health/readiness | `/health` per pipeline | no readiness gate on deps | fail-closed readiness + diagnostic bundle | med |
| Dead code / gating | some unwired paths | no runtime guardrail for unsupported select | guardrail map + cleanup + docs | low |
| Secret scan in CI | govulncheck only | no gosec / log scan | add gosec + redaction test suite | med |

## 4. Phase 2D slice order (by risk reduction)

1. **Correlation IDs + event/audit identity** (commit #2) — unblocks audit/reconstruction; low risk, high leverage.
2. **Structured logging + central redaction + secret-scan tests** (commit #3) — closes the highest-risk gap (secret leak via logs).
3. **Durable audit trail + diagnostic bundle + restart tests** (commit #4).
4. **API idempotency + WS reconnect/replay/gap** (commit #5) — closes duplicate-mutation risk.
5. **Operator recovery endpoint/UX + runbooks** (commit #6).
6. **Concurrency/resource locks + parallel/bandwidth** (commit #7).
7. **Dead-code cleanup + feature gating + docs** (commit #8).
8. **Release suite + report** (commit #9).

Each commit: build + vet + unit + relevant integration + redaction scan. Stop conditions from the mandate are enforced at every step (any secret in a sink → stop; any audit/event persistence failure enabling reported success → stop; any replay dup/loss/reorder → stop; any idempotency/concurrency dup of cutover/promote/rollback/switch → stop).

## 5. Acceptance criteria (carried from mandate)

- Every operator-visible op has correlation ID + durable audit trail.
- Structured logs contain required context, no secrets in tested sinks.
- API/WS/logs/audit/checkpoints/reports/diagnostic bundles pass redaction tests.
- Every supported op can reconnect/replay safely or explicitly instruct REST recovery; no silent event loss claimed complete.
- Duplicate mutation requests cannot cause duplicate unsafe actions.
- Conflicting concurrent migrations/actions blocked before unsafe mutation.
- Parallel/bandwidth works as documented or is explicitly rejected/marked unsupported.
- Unsupported engine/provider/topology blocked early + visible in compatibility matrix.
- UI/API never show false-complete for degraded/manual/ambiguous state.
- All Phase 1/2A/2B/2C suites green; `go build ./...`, `go vet ./...`, `go test ./...` pass; race coverage on concurrency packages.
- Integration/failure-injection results recorded; env-blocked tests explicitly listed.
