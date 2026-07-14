# Phase 5 — Workstream F: Observability & Hardening

**2026-07-14** · Phase 5.

Audit P3: correlation ID dead code (all events `correlationId: ""`), no structured
logging, `log.Printf` unsanitized. Demanded: populate correlationId on all events,
structured logging (JSON/consistent), `shared.LogPrintf` redaction helper; minimal
log viewer UI (per-migration, filter, client-side redaction fallback); metrics
(throughput, lag, error counts, per-stage status) exposed in API + FE dashboard.

---

## F1. Correlation ID + structured logging (current reality)

- **Phase2D-2 already threaded correlation IDs** through the REST handlers:
  `withRequestID` wraps every pipeline handler (`pipeline_handler.go:124-135`),
  honors client `X-Request-ID`, and a `WithCorrelation` boundary id is derived
  (`pipeline_handler.go` comment block). So the audit's "dead code" is largely
  addressed at the **request** boundary.
- **Gap (genuine, P3-1):** events emitted on the WS/audit path still may not stamp
  `correlationId` end-to-end (the audit's specific complaint). Verify every
  `WSMessageExtended` / audit row carries the migration+request correlation id.
- **Gap (P3-2):** logging still uses `log.Printf` in places (e.g.
  `pipeline.go:909` `log.Printf("warning: failed to persist needs_manual…")`).
  Introduce `shared.LogPrintf` (redacting) and route engine logs through it;
  emit JSON-structured lines for pipeline stages so the log viewer can parse them.

### Phase 5 net-new (F1)
- Ensure `extendWSMessage` (`pipeline_handler.go:1623`) and audit writes stamp
  `correlationId`; replace remaining raw `log.Printf` in the migration package
  with a redacting structured logger.

## F2. Log viewer UI (gap)

- **Not present.** No per-migration log viewer component exists on this branch.
- **Phase 5 net-new (F2):** minimal `MigrationLogAudit` already renders audit rows
  (`MigrationLogAudit.svelte`, used in the pipeline page) — extend it with
  level/keyword filter and a client-side redaction fallback (mask anything matching
  a credential pattern if the server row isn't already redacted). This is a small
  FE addition; backend redaction already exists for config/password.

## F3. Metrics (current reality + gap)

- `GET /api/pipeline/migrations/:id/metrics` (`pipeline_handler.go:134`) exists and
  the WS `WSMessageExtended` already carries `bytesDone, bytesTotal, speedBytes,
  eta, replicationLag, healthScore, riskScore` (`pipeline.ts:322`). The FE pipeline
  dashboard consumes these.
- **Gap (P3):** no explicit **error-count** or **throughput time-series** endpoint;
  lag/throughput are point-in-time from the WS, not aggregated. Add a lightweight
  metrics aggregation (error counts per stage, throughput samples) to the metrics
  handler + show in FE.

### Phase 5 net-new (F3)
- Add error-count + throughput aggregation to the metrics response; surface in the
  FE pipeline dashboard (no new panel needed — extend existing metrics view).

---

## Phase 5 net-new work for Workstream F

| Item | Status | Action |
|---|---|---|
| Request correlation ID (P3-1) | done (withRequestID) | — |
| Event/audit correlationId | **done** | stamped by `extendWSMessage` (`pipeline_handler.go:1623`) |
| Structured redacting logger (P3-2) | **done** | `redactWriter` routes every `log.Printf` via `SanitizeString` (`shared/logger.go`) |
| Log viewer filter/redaction (F2) | partial (`MigrationLogAudit`) | add level/keyword filter + client redaction |
| Metrics error-count/throughput (F3) | partial (point-in-time) | aggregate + FE surface |

**Honest claim:** request-level correlation and metrics plumbing exist; the genuine
F-work is event/audit correlation stamping, a redacting structured logger, and the
small log-viewer/metrics-aggregation additions.
