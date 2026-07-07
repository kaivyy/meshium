# Meshium v1.5.0-beta.1

**Pre-release / beta — hardening candidate. Not a stable release.**

This is a beta hardening release focused on runtime safety, crash recovery,
lifecycle correctness, and honest capability documentation. It is **not
recommended for production migrations without a manual validation pass** on
your own source/target pair (see the Validation Checklist below). Meshium is
**not** production-ready or enterprise-grade at this tag, and this release makes
no such claim.

## Highlights

Concurrency and shutdown correctness across both migration paths (Job Engine and
Pipeline), startup recovery for interrupted migrations, fail-closed SSH host-key
handling, an idempotent traffic-switch cutover record, and a set of honest,
code-grounded capability descriptors (Driver SDK, planner strategy selection,
observability seam) whose maturity is labeled rather than overstated.

## Runtime safety
- Job Engine in-memory queue is now concurrency-safe (data race removed).
- Discovery network-step callbacks are serialized (no concurrent callback race).
- SSH stream callback is serialized to prevent a concurrent `onOutput` race.
- Security middleware wired; LiveMonitor sockets gated; remote-to-remote rsync
  path repaired.

## Recovery
- Migrations crashed mid-state-machine (Jalur A) are detected and marked
  interrupted on startup, so they become resumable.
- Interrupted pipeline migrations (Jalur B) left in a running state are
  recovered on startup.

## Pipeline lifecycle
- Active pipelines are drained during shutdown via a pipeline registry and a
  bounded `GracefulDrain` window before the process exits.
- The migration engine is drained before the root context is cancelled.
- Server resources are closed strictly after graceful shutdown completes, so
  in-flight steps keep their connections while finishing.

## Security
- SSH host-key verification fails closed when no known-hosts store is available
  (removes silent insecure-ignore fallbacks on the credentialed connect paths,
  including the AI assistant path).
- Path-safety guard for remote command paths (rejects NUL/control characters,
  opt-in traversal containment).
- CSP updated to allow the embedded SvelteKit inline scripts via sha256 hashes
  rather than loosening the policy.

## Observability
- Vendor-neutral observability abstraction (structured events, metrics,
  per-migration timeline, pluggable sink; ships Nop and in-memory sinks — no
  external backend mandated).
- The state-machine engine's `Run` path emits lifecycle events
  (started / stage-completed / stage-started / stage-failed / interrupted /
  completed).

## Idempotency
- Traffic-switch cutover is idempotent: on resume after a crash between applying
  the switch and writing its checkpoint, the config and cutover records are not
  duplicated.

## Driver SDK
- Standalone, stdlib-only workload driver foundation.
- Honest database-strategy catalog with explicit maturity labels
  (implemented / descriptor / future); dump/restore strategies are descriptors,
  not executed paths.

## Planner
- Capability-grounded, deterministic strategy selector that derives feasibility
  from the driver catalog: never selects a non-executable strategy and never
  labels a result zero-downtime.

## CI / release readiness
- CI expanded to run vet, `-race`, staticcheck, and govulncheck; staticcheck and
  govulncheck run advisory (non-blocking) until the backlog is cleared.
- Safe staticcheck findings cleaned up; two unused internal helpers removed.
- `RELEASE.md` release checklist added.

## Documentation
- README claims synced to the code; new `docs/architecture.md` is the honest,
  code-grounded reference (descriptor vs implemented, host-key exceptions,
  static-analysis backlog).

## Known limitations
- **Pipeline path is not instrumented for observability** — the observ events
  are emitted from the state-machine engine's `Run` only.
- **`Engine.Resume` is not instrumented** — no observability events on the
  resume path yet.
- **Static-analysis backlog** — 35 advisory staticcheck findings remain
  (27×U1000 unused, 2×SA1019, 2×S1039, 2×S1040, 2×S1016). No new or critical
  finding; expected and non-blocking at this tag.
- **Some future API is intentionally unused** — extension points and strategy
  descriptors are declared ahead of being wired; these are deliberate, not dead
  code.
- **Full idempotency is not guaranteed for every step** — recovery is bounded by
  checkpointing; work applied but not yet checkpointed before a crash may be
  re-attempted. Steps aim to be idempotent where feasible.
- **Real DNS/LB cutover depends on the available strategy** — only implemented
  strategies execute; descriptor/future strategies are not orchestrated.
- **Credential-less SSH wizard probe** — the connection wizard's initial probe
  dials with host-key verification disabled to read the server banner and
  algorithms only; it sends no credentials. Credentialed connect paths (wizard
  and AI assistant) fail closed.
- **Enterprise-grade features are incomplete** — no SBOM, no build provenance/
  signing.

## Validation checklist (run before trusting a real migration)
- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes (CI race job runs the full suite unbounded).
- [ ] `go vet ./...` is clean.
- [ ] `staticcheck ./...` reviewed — advisory backlog only, no new/critical.
- [ ] Manual migration validation matrix on your own hosts:
      packages / configs / services / users / docker across your source→target
      distro pair, plus at least one interrupt-and-resume cycle and one rollback.

## Upgrade notes
- No schema migration is required by this release.
- No breaking API changes versus v1.4.x in the committed set.
- If you deploy behind a reverse proxy, the proxy-aware WebSocket origin fix is
  **not** in this tag (it is uncommitted WIP) — plan for it in a later release.

## Follow-up before a stable release
- Instrument the pipeline path and `Engine.Resume` for observability.
- Review the no-credential SSH wizard probe and decide whether to keep it as-is,
  document it explicitly, or add optional key capture/compare behavior.
- Clear or explicitly suppress the staticcheck U1000 backlog, then promote
  staticcheck to a blocking CI gate.
- Extend idempotency guarantees across all pipeline stages.
- Add SBOM generation and build provenance/signing.
