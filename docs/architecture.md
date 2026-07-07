# Meshium Architecture

This document is the honest, code-grounded reference for how Meshium is
built. Where the README summarizes features, this document states what is
actually implemented, what is a descriptor (a command string that exists but
is not yet wired into an orchestrated path), and what is deliberately out of
scope. It is kept in sync with the code; when a claim here and the code
disagree, the code wins and this document is the bug.

## Contents

- [Architecture Overview](#architecture-overview)
- [Migration Paths](#migration-paths)
- [State Machine & Lifecycle](#state-machine--lifecycle)
- [Graceful Shutdown](#graceful-shutdown)
- [Recovery Model](#recovery-model)
- [Cutover & Downtime Honesty](#cutover--downtime-honesty)
- [Driver SDK](#driver-sdk)
- [Planner Strategy Selection](#planner-strategy-selection)
- [Observability](#observability)
- [Security Model](#security-model)
- [Known Limitations](#known-limitations)

## Architecture Overview

Meshium is a single Go binary with an embedded SvelteKit SPA. It manages
Linux-to-Linux server migration entirely over SSH — there is no agent or
daemon on the target machines. The backend is organized into modules under
`internal/mod/` (auth, ssh, server, discovery, planner, transfer, migration,
jobengine, file, and supporting feature modules), with cross-cutting helpers
in `internal/shared/`.

The high-level flow is: **discover** a server into a `ServerSnapshot` →
**plan** a migration from source and target snapshots → **execute** the plan
through a checkpointed state machine → **verify**, and on failure **roll
back**. Discovery, planning, and execution are each independently invocable
and independently persisted to SQLite.

## Migration Paths

There are two execution paths and they are not the same code:

- **Jalur A — Job Engine (`internal/jobengine`).** The top-level orchestrator.
  It queues Migration / Discovery / CompatCheck jobs in a SQLite-backed FIFO
  queue, runs them through a worker pool (default 1 worker — migrations are
  heavy and are intentionally not parallelized), and broadcasts progress over a
  non-blocking pub/sub. A Migration job loads a planner plan, builds
  `[]MigrationStep` via the bridge, and drives the migration `Engine`.

- **Jalur B — Pipeline (`internal/mod/migration/pipeline*.go`).** A separate,
  staged pipeline used by the newer migration flow, including the live
  replication path for databases.

Both paths ultimately execute typed steps over an SSH `Executer`. When reading
the code, keep the two paths distinct: a change to the job-engine executor does
not automatically change pipeline behavior and vice versa.

## State Machine & Lifecycle

The migration `Engine` (`internal/mod/migration/state.go`, `engine.go`) runs a
validated state machine. The forward path is:

```
created → planning → backup → snapshot → transferring → applying → verifying → committed
```

with failure and repair transitions:

```
… → failed → rollback → restored
… → interrupted → resuming → (resumes forward path)
```

Transitions are validated — an arbitrary state change is rejected — and each
verified step is checkpointed so a resume can skip already-completed work.

## Graceful Shutdown

The job engine's shutdown manager (`internal/jobengine/shutdown.go`) enforces a
30-second timeout on shutdown: running jobs are allowed to finish their current
step, and if they do not finish within the window they are force-cancelled. Job
state is persisted so it can be resumed on the next start. The server wires
resource cleanup to run after graceful shutdown completes, not before, so
in-flight steps still have their connections while finishing.

## Recovery Model

On restart the `RecoveryManager` (`internal/mod/migration/recovery.go`) finds
migrations left in an interrupted state and offers two resolutions:

- **Resume** — continue from the last verified checkpoint, skipping
  already-verified steps.
- **Cancel** — roll back all applied steps in LIFO order and mark the
  migration `restored`.

The pipeline path additionally recovers migrations that crashed mid-state and
mid-pipeline on startup (see the recent recovery fixes in the migration
package). Recovery is best-effort and bounded by what was checkpointed: work
that was applied but never checkpointed before a crash may be re-attempted, so
steps are written to be idempotent where feasible.

## Cutover & Downtime Honesty

Meshium deliberately does **not** advertise "zero-downtime" migration. Downtime
is classified honestly (`internal/mod/driver/dbstrategy.go`):

- **full** — a dump/restore strategy requires the workload to be down for the
  whole transfer.
- **brief** — a replication strategy narrows downtime to a short cutover
  window while the replica catches up and is promoted. This is a brief window,
  **not** zero.

There is no "zero" downtime class in the type system, by design. A strategy is
only treated as low-downtime when it is both **implemented** and does not
require downtime; an unbuilt (future) strategy can never be reported as
zero-downtime.

## Driver SDK

The driver package (`internal/mod/driver`) is a standalone, stdlib-only
workload abstraction. It carries an honest maturity label on every database
strategy:

- **implemented** — there is an orchestrated, executed path (e.g. MySQL binlog
  replication, PostgreSQL streaming replication, Redis replica handoff).
- **descriptor** — the command strings exist (e.g. as planner output) but there
  is no orchestrated execution path yet (e.g. logical dump/restore).
- **future** — declared for planning purposes, not built.

`SelectDBStrategy` prefers the lowest-downtime **implemented** strategy for a
workload and never returns a descriptor or future strategy as executable. The
package has no importers inside the running pipeline beyond the planner's
one-way dependency (planner → driver), so it cannot destabilize execution.

## Planner Strategy Selection

Strategy selection is deterministic and rule-plus-integer-scoring based
(`internal/mod/planner/strategy_scoring.go`). `SelectStrategyScored` derives
feasibility from `driver.SelectDBStrategy`: it never selects a non-executable
strategy, never labels a result zero-downtime, and when the downtime budget is
below the replication cutover window it rejects all candidates rather than
pretending a strategy fits. Scoring is stable across runs (verified by a
determinism test) so a given set of constraints always yields the same plan.

## Observability

The observability abstraction (`internal/mod/observ`) is a vendor-neutral seam:
a structured `Event` model (lifecycle `Phase`, audit-vs-log `Kind`, `Severity`,
free-form string fields), a `Metric` type, a per-migration execution
`Timeline`, and a pluggable `Sink` interface. Meshium ships a `NopSink` and an
in-memory `MemorySink`; a real backend (Prometheus, OpenTelemetry, a database)
is a `Sink` adapter — no such dependency is mandated. The package is
stdlib-only and standalone, so it does not affect the running pipeline; the
existing `EventBus` and observation metrics can be wired into it later.

## Security Model

- **Credentials at rest** — AES-256-GCM with an Argon2id-derived key; legacy
  PBKDF2 verified for backward compatibility.
- **Remote command construction** — untrusted values are single-quoted with
  `shared.ShellQuote`/`ShellQuoteArgs`, and arbitrary file content is written
  via base64 decode rather than interpolated into a shell line. A path-safety
  guard (`shared.ValidateRemotePath`, `shared.IsPathWithinRoot`) rejects NUL and
  control characters and provides opt-in traversal containment.
- **Host key verification** — see [Known Limitations](#known-limitations) for
  the exact trust model and its exceptions.
- **Secret redaction in logs** — `shared.SanitizeString`/`SanitizeLog` redact
  passwords, tokens, bearer/authorization headers, private-key blocks, and long
  base64 blobs before they reach the log.

## Known Limitations

This section is intentionally blunt. These are real gaps, not marketing
softeners.

### Host key verification has two unverified paths

Production connections verify the remote host key against a SQLite-backed
known-hosts store (`internal/mod/ssh/knownhosts.go`): an unknown or
not-yet-trusted key is **rejected** (`ErrHostKeyNotTrusted`), a key becomes
trusted only through an explicit `TrustHostKey` action, and a later key change
is rejected as `ErrHostKeyMismatch`. However, two paths connect **without**
verification:

- the connection wizard's initial probe (`internal/mod/ssh/wizard.go`), which
  dials with `InsecureIgnoreHostKey` to read the banner and algorithms, and
- the optional AI assistant's fallback (`internal/mod/ai/service.go`), whose
  `hostKeyCallback` returns `InsecureIgnoreHostKey` when no store is available.

These are trust-on-first-use style exposures and should be closed before any
"secure host key verification everywhere" claim is made.

### Descriptor-only database strategies

Dump/restore database strategies are descriptors (command strings), not
orchestrated executed paths. Only the replication strategies listed under
[Driver SDK](#driver-sdk) are `implemented`.

### Recovery is bounded by checkpointing

Work applied but not yet checkpointed before a crash may be re-attempted on
resume. Steps aim to be idempotent, but this is not universally guaranteed.

### Static analysis backlog

The codebase carries pre-existing `staticcheck` findings (mostly unused
symbols and minor style nits). CI runs `staticcheck` and `govulncheck` in
advisory (non-blocking) mode until that backlog is cleared; see
`.github/workflows/ci.yml`.
