# Meshium v1.5.0-beta.2

**Pre-release / beta — hardening candidate. Not a stable release.**

Second beta on the v1.5.0 line. It builds on v1.5.0-beta.1 by fixing two
connection-breaking backend bugs and landing a full frontend redesign. It is
still **not recommended for production migrations without a manual validation
pass** on your own source/target pair (see the Validation Checklist below).
Meshium is **not** production-ready or enterprise-grade at this tag, and this
release makes no such claim.

## What's new since v1.5.0-beta.1

### Connection fixes
- **WebSocket origin check is now proxy-aware.** WebSocket upgrades were
  rejected with `403 Forbidden` whenever Meshium was reached through a reverse
  proxy (Tailscale serve, nginx) on a public hostname — the origin check
  compared the browser's `Origin` against the internal `Host`, so Test
  Connection / Terminal / Monitoring failed while plain REST kept working. The
  origin hostname is now compared against both `r.Host` and a trusted
  `X-Forwarded-Host`; genuine cross-origin requests are still rejected. All 7
  WebSocket upgraders were consolidated onto the single shared check (several
  previously had no origin protection at all).
- **SSH key-management routes are now registered.** `/api/servers/{id}/test-auth`
  and 7 sibling routes (`install-key`, `verify-key`, `rotate-key`, `clear-key`,
  `auth-status`, `history`, `metrics`) returned `404 NOT_FOUND` because the key
  handler was never wired into the router. It is now constructed and registered.

### Frontend redesign (light / dark / system)
- **Semantic CSS-variable token system** — colors, surfaces, borders, and
  accents are driven by semantic tokens defined once and themed per mode,
  replacing hard-coded palette values scattered across pages.
- **Light / dark / system theme toggle** — a persisted theme store with a
  three-state top-bar toggle (light / dark / follow-system).
- **Shared UI primitives restyled on the token layer** — Button, Card,
  DataTable, DropdownMenu, plus card shadows and focus states.
- **All pages swept onto the token system** — existing page layouts were
  preserved; only the styling layer changed.
- **WCAG-oriented touches** — 44px minimum tap targets on mobile and global
  `focus-visible` handling; server-detail tab row and mobile terminal overflow
  fixes.

## Inherited from v1.5.0-beta.1

- Concurrency and shutdown correctness across both migration paths (Job Engine
  and Pipeline); startup recovery for interrupted migrations.
- Fail-closed SSH host-key handling on the credentialed connect paths
  (including the AI assistant path).
- Idempotent traffic-switch cutover record.
- Pipeline registry + bounded `GracefulDrain` on shutdown; resources closed
  strictly after graceful shutdown completes.
- State-machine engine `Run` emits lifecycle observability events.
- Honest, code-grounded capability descriptors (Driver SDK, planner strategy
  selection, observability seam) with maturity labels rather than overstated
  claims.
- CI runs vet, `-race`, staticcheck, and govulncheck (staticcheck/govulncheck
  advisory until the backlog is cleared).

## Known limitations

- **Pipeline path is not instrumented for observability** — obs events are
  emitted from the state-machine engine's `Run` only.
- **`Engine.Resume` is not instrumented** — no observability events on resume.
- **Static-analysis backlog** — advisory staticcheck findings remain
  (unused-symbol backlog and a handful of style findings); non-blocking.
- **Some future API is intentionally unused** — extension points and strategy
  descriptors are declared ahead of being wired; deliberate, not dead code.
- **Full idempotency is not guaranteed for every step** — recovery is bounded
  by checkpointing; work applied but not yet checkpointed before a crash may be
  re-attempted.
- **Real DNS/LB cutover depends on the available strategy** — only implemented
  strategies execute; descriptor/future strategies are not orchestrated.
- **Credential-less SSH wizard probe** — the connection wizard's initial probe
  dials with host-key verification disabled to read the server banner and
  algorithms only; it sends no credentials. Credentialed connect paths (wizard
  and AI assistant) fail closed.
- **Enterprise-grade features are incomplete** — no SBOM, no build provenance /
  signing.

## Validation checklist (run before trusting a real migration)
- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes (CI race job runs the full suite unbounded).
- [ ] `go vet ./...` is clean.
- [ ] `npm run check` (svelte-check) is clean and `npm run build` succeeds.
- [ ] Manual migration validation matrix on your own hosts:
      packages / configs / services / users / docker across your source→target
      distro pair, plus at least one interrupt-and-resume cycle and one rollback.

## Upgrade notes
- No schema migration is required by this release.
- No breaking API changes versus v1.4.x / v1.5.0-beta.1 in the committed set.
- The proxy-aware WebSocket origin fix **is** in this tag (it was uncommitted
  WIP at beta.1). If you deploy behind a reverse proxy, this is the release that
  fixes the `403` handshake failures.

## Follow-up before a stable release
- Instrument the pipeline path and `Engine.Resume` for observability.
- Review the no-credential SSH wizard probe and decide whether to keep it as-is,
  document it explicitly, or add optional key capture/compare behavior.
- Clear or explicitly suppress the staticcheck backlog, then promote staticcheck
  to a blocking CI gate.
- Extend idempotency guarantees across all pipeline stages.
- Add SBOM generation and build provenance/signing.
