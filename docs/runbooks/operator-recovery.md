# Operator Recovery Runbook (Meshium)

This runbook covers recovery from every fail-closed / interrupted state the
migration pipeline can reach. It is derived from the **actual** state machine
and cutover-orchestrator semantics — it never recommends an automatic recovery
that the code does not perform. When a state is fail-closed, the only safe
paths are an explicit operator action (resume / retry / rollback) or manual
intervention. There is **no** silent auto-forward, auto-rollback, or "zero
downtime" guarantee.

## Quick reference

| State | Safe to auto-resume? | Operator action |
|-------|----------------------|-----------------|
| `failed` | yes (retry) | diagnostics → retry or rollback |
| `interrupted` | yes | `POST /{id}/resume` |
| `paused` | yes | `POST /{id}/resume` |
| `awaiting_cutover` | no | confirm traffic, then `commit` or `rollback` |
| `needs_manual_intervention` | no | diagnostics → resolve gate → rollback if abandoning |
| `rolled_back` / `rollback_degraded` | no | review, plan re-run |
| `cancelled` | no | plan fresh migration |
| `committed` | n/a | none |

## API surfaces

- `GET /api/pipeline/recovery/{id}` — returns the operator guidance for the
  migration's current state (summary + concrete next actions + `safeToResume`).
- `GET /api/pipeline/diagnostics/{id}` — redacted snapshot (config, stages,
  audit, events, fence, traffic, cutover, rollback, transfers) for incident
  analysis. Secrets are masked.
- `GET /api/pipeline/audit/{id}` — full audit trail with correlation + idempotency
  identifiers for tracing one operation end-to-end.
- Mutating actions accept an `Idempotency-Key` header; a retried request with
  the same key is answered from the original audit outcome instead of
  re-executing.

## Per-state procedure

### `failed`
1. `GET /api/pipeline/diagnostics/{id}`; read the failing stage's `error` and
   its audit entry (correlation id ties logs ↔ events ↔ audit).
2. If the error is transient (network blip, source briefly unreachable):
   `POST /api/pipeline/{id}/retry` resumes from the last checkpoint.
3. If permanent: `POST /api/pipeline/{id}/rollback` restores source/target to
   pre-migration state.

### `interrupted`
Crashed or disconnected mid-run; resumable from the last completed stage.
`POST /api/pipeline/{id}/resume`. If resume repeatedly fails, treat as `failed`.

### `paused`
Operator-paused; nothing in flight. `POST /api/pipeline/{id}/resume`.

### `awaiting_cutover`
Pipeline stopped after a `manual_required` traffic switch. Only an explicit
`commit` may finish it (and it survives restart).
1. Confirm traffic is actually on the target (health checks, DNS propagation).
2. `POST /api/pipeline/{id}/commit` to finalize, or `/rollback` if verification
   failed.

### `needs_manual_intervention` (fail-closed)
Ambiguous topology, unsafe rollback, checkpoint-write failure, or a rejected
commit. **No automatic forward/rollback transition is legal.** Read the failing
gate's reason in the diagnostic bundle (`fenceStatus`, `topologySummary`,
`trafficVerifySummary`). Resolve the underlying condition, then either retry
(after the gate clears) or `POST /api/pipeline/{id}/rollback` to abandon.
Do **not** retry while the gate reason is present — it will fail-closed again.

### `rolled_back` / `rollback_degraded`
Target/source restored. `rollback_degraded` means some stage rollback had
warnings — review the diagnostic bundle before trusting integrity. Plan a
corrected re-run as a fresh migration.

## What is NOT supported (honest scope)

- No automatic cross-major PostgreSQL cutover; no MongoDB replica-set automatic
  cutover; no new traffic-switch provider beyond the tested ones
  (nginx, haproxy, traefik, cloudflare, caddy, docker, dns).
- No global "zero downtime" claim — downtime = observed cutover + switch window.
- Distributed fencing redesign and k8s/multi-region/multi-tenant are out of
  scope.
