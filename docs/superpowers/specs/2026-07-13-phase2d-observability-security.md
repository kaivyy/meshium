# Phase 2D — Operationalization, Auditability, Observability, Recovery UX, Release Readiness

> **Date:** 2026-07-13
> **Scope:** Make Phase 1 / 2A / 2B / 2C operationally ready: traceable, auditable,
> observable, operator-safe, and release-gated. **No new replication engines, no new
> traffic providers, no changes to the proven safety contract.** Hardening only.
> **Baselines (immutable):** Phase 1, 2A, 2B, 2C. Do NOT regress `AwaitingCutover`,
> `NeedsManualIntervention`, checkpoint persistence, topology fail-closed rollback,
> stream/output safety, fencing/lease enforcement, traffic-ownership verification,
> transfer resume/reconcile/integrity, `ForceTransition` restrictions, honest wording.
> **Spec/contract:** this document.

## 0. Executive summary (investigation findings)

The codebase already provides the *primitives* Phase 2D needs. The gaps are
**wiring and completeness**, not greenfield construction:

| Capability | Status | Gap |
|---|---|---|
| Secret redaction | **Present** — `shared.SanitizeString` + `SanitizeJSONRawMessage` | Not applied to raw `log.Printf` sites, command stdout/stderr persistence, error serializers uniformly |
| Correlation ID | **Field exists** — `MigrationEvent.CorrelationID` | Never populated at any request/WS/operator boundary; no request vs migration vs pipeline vs operator-action distinction |
| Event sequencing + replay | **Present** — monotonic `Sequence`, `ReplayEvents` | WS handler replay path not verified end-to-end; client seq handshake undefined |
| Audit trail | **Present** — `AuditEntry` + `CreateAuditEntry` (8 sites) | Missing fields: correlation_id, idempotency_key, fence generation, topology summary, traffic-verify summary, result enum, actor-type; no export/diagnostic bundle |
| Idempotency | **Partial** — nginx/haproxy switchers use `IdempotencyKey` | REST mutating actions (`cutover/commit/rollback/pause/resume/cancel/retry`) have **none** |
| Structured logging | **Absent** — ~80 `log.Printf` sites, unstructured, no severity/field discipline, no sampling | No standard fields; many sites could carry command lines |
| Concurrency/bandwidth | **Half-wired** — `ParallelTransfers` default 4; `BandwidthLimit` field exists | `BandwidthLimit` has **no consumer** (dead); parallel wiring into `TransferOptions` unverified |
| Operator recovery UX | **Partial** — states exist, `handlePipelineMigrationByID`/`handleGetStages` exist | AwaitingCutover/NeedsManualIntervention lack actionable surface (reason, safe/prohibited actions, runbook link, audit ref) |

Evidence: `internal/shared/sanitizer.go`, `internal/mod/migration/event_bus.go`,
`internal/mod/migration/pipeline_models.go` (`AuditEntry`), `internal/mod/migration/pipeline_handler.go`
(`handlePipelineAction` dispatcher, `handlePipelineWS`), `internal/mod/migration/pipeline.go`
(`CreateAuditEntry` call sites), `internal/mod/migration/nginx_switch.go` (`IdempotencyKey`).

## 1. Slice order (risk reduction)

1. **Correlation IDs + event/audit identity** (#2) — highest leverage; unlocks traceability for every later slice.
2. **Structured logging + central redaction** (#3) — builds on the existing `shared.SanitizeString`; closes the largest secret-surface gap.
3. **Durable audit trail + diagnostic bundle** (#4) — completes the `AuditEntry` contract + export.
4. **API idempotency + WS reconnect/replay** (#5) — single injection point `handlePipelineAction`; event bus replay already exists.
5. **Operator recovery API/UX + runbooks** (#6) — makes blocked states actionable.
6. **Concurrency/resource locks + parallel/bandwidth** (#7) — wire `ParallelTransfers`; mark `BandwidthLimit` explicit (unsupported if un-honorable).
7. **Dead-code cleanup + feature gating + docs** (#8) — quarantine dead provider rollback helpers, confirm feature guards.
8. **Release suite + report** (#9) — pyramid + final report.

## 2. Design decisions

### A. Correlation ID propagation
- New `internal/mod/migration/trace.go`:
  - `type RequestID string` (externally initiated: REST req, WS command, migration create, pipeline start/retry/resume, cutover, rollback, background reconcile).
  - `type MigrationID int`, `PipelineID int`, `TransferID string`, `LeaseGen int`, `OperatorActionID string`.
  - `contextKey` + `WithRequestID(ctx, id)`, `RequestIDFrom(ctx)`, `NewRequestID()` (crypto/rand hex).
- Boundaries populate it: every `handle*` HTTP handler wraps `r.Context()` via `WithRequestID`; WS command dispatch (`handlePipelineWS`) extracts a client-supplied `clientSeq` + echoes a server `requestId`; `Pipeline.Run`/`Executor` carry it through; `Emit`/`CreateAuditEntry`/`LogRecord` read it from context.
- Distinction is by **field**, not by generating new IDs per call: one request → one `RequestID`; it flows into `MigrationEvent.CorrelationID` and `AuditEntry.CorrelationID`.
- Persisted for post-incident reconstruction: `correlation_id` column already exists on the events table (`internal/db/migrations.go:214`); add to `audit_entries`.
- **Never** expose raw secrets through trace context.

### B. Structured logging
- New `internal/mod/migration/log.go` — `LogRecord` struct with mandatory fields:
  `timestamp, level, message, component, operation, correlation_id, migration_id, pipeline_id, stage, engine, provider, transfer_id, attempt, state, error_category, sanitized_error`.
- `Logger` interface (`Log(ctx, record)`) + `defaultLogger` writing JSON to `log` (via `shared.LogPrintf` for sanitization). Severity `DEBUG/INFO/WARN/ERROR`.
- Replace the highest-risk `log.Printf` sites (SSH command exec, fencing, traffic, replication, pipeline state transitions, transfer) with `Log(ctx, ...)`. Do NOT churn all 80 at once — convert by concern as each slice lands. Each converted site passes `ctx` so correlation_id + sanitized error flow.
- Sampling: a `rateLimited` wrapper for high-frequency progress (transfer byte ticks) — preserve milestone events, cap volume.
- All messages pass through `shared.SanitizeString` (the `defaultLogger` sanitizes the message + error before emit).

### C. Mandatory redaction (central)
- Single boundary already exists: `shared.SanitizeString` / `SanitizeJSONRawMessage` / `SanitizeLog`.
- Apply it at every sink:
  - logger (`defaultLogger`)
  - API error serializer (`shared.WriteError`)
  - WS event serializer (`EventBus.Emit` already does; verify `handlePipelineWS` write path)
  - audit persistence (`CreateAuditEntry` — sanitize `EventData`)
  - command-result persistence (replication/traffic stdout/stderr — sanitize before store)
  - export/report generation (`handlePipelineExport`)
  - UI log viewer (sanitize server-side before send)
- Redacts: passwords, tokens, API keys, credential-bearing URIs, Redis auth, cloud creds, SSH private key material, authorization headers, env secret values, command-line secret args, sensitive request bodies.
- Existing test coverage: `internal/shared/sanitizer_test.go` (password/bearer/ssh-key/api-key/base64). Extend with: nested JSON, query strings, shell commands, multi-line, wrapped errors, structured fields, WebSocket payloads, audit records, exports.
- Emergency diagnostic mode: **out of scope for #3**; documented as a future access-controlled, time-limited, audited mode. (No secret-logging path is added now.)

### D. Audit trail + evidence
- Extend `AuditEntry` (backward-compatible, additive columns):
  `correlation_id`, `idempotency_key`, `actor_type` (operator|api_client|system_reconciler),
  `fence_generation`, `fence_status`, `topology_summary`, `traffic_verify_summary`,
  `approval_ref`, `result` (success|rejected|failed|degraded|manual_intervention).
- Persist **before** reporting a safety-critical mutation complete (pipeline.go sites already call `CreateAuditEntry`; verify ordering for `cutover/commit/rollback` — those currently route through `handlePipelineActionResult` and may not audit; add explicit audit there).
- Append-only: no delete API. `handleAudit`/`handleAuditByID` already exist.
- Sanitized export: `handlePipelineExport` emits a JSON bundle with audit + events + config-presence (no secrets) — the basis of the diagnostic bundle (slice #4).

### E. API idempotency + WS reconnect/replay
- `handlePipelineAction`: read `Idempotency-Key` header (or JSON body field). New `idempotencyStore` (in-repo table `idempotency_keys{key, action, migration_id, result_payload, created_at}`).
  - Same key + same action → return prior result (200/409 as originally).
  - Same key + **different** action/migration → `409 conflict`.
  - No key → generate one (preserve current behavior; key optional for human operators, mandatory for API clients — documented).
- WS `handlePipelineWS`: client sends `lastSeq` on connect; server calls `EventBus.ReplayEvents(ctx, migrationID, lastSeq, publish)` then streams live. If `lastSeq` > server max (gap impossible) or history truncated → respond with explicit `RECOVER_VIA_REST` control frame; client re-fetches `/api/pipelines/:id/events`. No fabricated continuous stream.
- Auth validated on WS reconnect (existing upgrade path). Reconnect must not re-invoke a mutating action.

### F. Operator recovery UX/API
- New `handleGetRecovery(ctx, id)` (or extend `handlePipelineMigrationByID`) returning a structured block for blocked/manual states:
  `reason, current_state, last_success_stage, last_failed_action, topology_status, traffic_ownership_status, lag_health, transfer_verify_status, fence_status, safe_actions[], prohibited_actions[], runbook_ref, correlation_id, audit_ref`.
- UI (`web/src`) distinguishes: success / in-progress / awaiting-cutover / degraded / rollback-degraded / failed / manual-intervention. No "retry" button unless the server confirms the action is safety-legal for the current state.
- Confirmation maps to a persisted+audited+idempotent server action only.

### G. Concurrency / rate / bandwidth
- `ParallelTransfers` (default 4, in `normalizeMigrationConfig`) — verify it reaches `TransferOptions` and the transfer engine honors it (bounded worker pool, no unbounded goroutines). Add a guard: per-migration cap ≤ global cap.
- `BandwidthLimit` — **currently has no consumer**. Decision: either wire `rsync --bwlimit` (explicit, observable) or mark it `unsupported` in the matrix + reject at config validation. Given rsync availability gating already exists, wire `--bwlimit` only when rsync is selected; otherwise reject/annotate (never pretend applied).
- Resource-identity lock: prevent two migrations mutating the same source/target concurrently. Add a lightweight in-memory `resourceLock` keyed by `sourceHost:sourcePort`+`targetHost:targetPort` claimed at pipeline start, released at terminal state. Conflicting claim → reject before any mutating step.
- Preserve integrity verification under concurrency.

### H. Dead-code / feature gating
- `TrafficSwitchEngine.rollback{Cloudflare,Caddy,Docker,DNS,Traefik}` — providers that fail closed at dispatch. Keep the methods (referenced defensively) but mark them with a doc comment "unsupported provider — fails closed; no live caller". Do not delete (they are the fail-closed tail of `rollbackTraffic`).
- Confirm `CutoverEngine`/`FreezeManager` have real callers or are quarantined behind an `// Experimental` note.
- `ForceTransition` legacy callers — already removed per `force_transition_p0_test.go`; verify none remain.
- Update `docs/known-limitations.md` + compatibility matrix for any deferred/unsupported path.
- Runtime guardrail: `cutoverEngineType` / `newTrafficSwitcher` already block unsupported at dispatch — keep; add a UI-side support matrix fetch so unsupported engines/providers cannot be selected in the wizard.

### I. Health / readiness / diagnostics
- `handleHealth` / `handleHealthByID` exist. Add readiness dependencies: persistence (DB ping), fencing authority (lease table reachable), event/audit persistence (write probe). Readiness FAILS if a required dependency would cause false success.
- Diagnostic bundle (`/api/diagnostics` or part of export): build/version, feature flags, support matrix, config-presence (no secret values), recent sanitized events/audit, health/readiness, test/release metadata. Never includes secrets/keys/raw command output.

### J. Documentation / runbooks
- Update: support matrix, known-limitations, preflight requirements, cutover checklist, manual-intervention runbook, rollback/degraded runbook, fence/lease failure runbook, transfer resume/reconcile runbook, event replay/reconnect troubleshooting, redaction/diagnostic-bundle policy, release-validation checklist, incident template.

## 3. Acceptance criteria (carried from directive)
- Every operator-visible operation has a correlation ID + durable audit trail.
- Structured logs contain required context, no secrets in tested sinks.
- API/WS/logs/audit/checkpoints/reports/diagnostic bundles pass redaction tests.
- Every supported operation reconnects/replays safely or instructs REST re-fetch; no silent event loss claimed complete.
- Duplicate mutation requests cannot cause duplicate unsafe actions.
- Conflicting concurrent migrations/actions blocked before unsafe mutation.
- Parallel/bandwidth config works as documented or is explicitly rejected/unsupported.
- Unsupported engine/provider/topology blocked early + visible in matrix.
- UI/API never show false-complete for degraded/manual/ambiguous state.
- Phase 1/2A/2B/2C test suites remain green; `go build ./...`, `go vet ./...`, `go test ./...` pass; race coverage on concurrency-sensitive packages.
- Integration/failure-injection results recorded; env-blocked tests explicitly listed.

## 4. Commit structure
1. Investigation + design + gap matrix (this doc).
2. Correlation ID propagation + event/audit identity + unit tests.
3. Structured logging abstraction + central redaction + secret-scan tests.
4. Durable audit trail (extend `AuditEntry`) + sanitized export/diagnostic bundle + restart tests.
5. API idempotency + WS reconnect/replay/gap + integration tests.
6. Operator recovery API/UX + runbooks.
7. Concurrency/resource locks + parallel/bandwidth wiring + tests.
8. Dead-code cleanup + feature gating + docs.
9. Release suite + report.

## 5. Stop conditions (unchanged from directive)
Secret in any log/event/API/audit/checkpoint/export → stop. Correlation/audit/event
persistence failure still reporting critical success → stop. Reconnect/replay can
duplicate/lose/reorder safety actions → stop. Idempotency/concurrency can run
duplicate cutover/promotion/rollback/traffic-switch → stop. Observability requires
unredacted command output/credentials → stop. Cleanup weakens Phase 1–2C invariants
→ stop. Any supported pair loses its passing acceptance suite → stop.
