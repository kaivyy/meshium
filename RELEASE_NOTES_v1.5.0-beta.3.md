# Meshium v1.5.0-beta.3

**Pre-release / beta — hardening candidate. Not a stable release.**

Third beta on the v1.5.0 line. This is a **build-fix release**: it makes the
tagged source compile from a fresh clone. It carries the same features as
v1.5.0-beta.2 (proxy-aware WebSocket origin check, registered SSH key-management
routes, and the light/dark/system frontend redesign) — nothing new was added.
Meshium is still **not** production-ready or enterprise-grade at this tag, and
this release makes no such claim.

## Why this release exists

**v1.5.0-beta.1 and v1.5.0-beta.2 do not build from a fresh clone.** Two source
files under `internal/mod/server/` were silently excluded from every commit by
an over-broad `.gitignore` rule, so the tagged trees are missing the
implementation of several `sqliteRepo` methods. A clean checkout of either tag
fails to compile:

```
internal/mod/server/repo.go: *sqliteRepo does not implement Repo
(missing method CreateConnectionProfile)
```

The local development tree always compiled because the files exist on disk —
they just never reached git. This tag fixes that.

## What changed since v1.5.0-beta.2

### Fresh-clone build fix (the whole point of this release)
- **`.gitignore` binary patterns anchored to the repo root.** The bare `server`
  pattern (intended for the compiled root binary) also matched the
  `internal/mod/server/` package directory, so any new file created there was
  ignored. `server`, `bin/`, and `meshium-server` are now anchored (`/server`,
  `/bin/`, `/meshium-server`) so they only match root-level build artifacts and
  never a package directory.
- **Missing server source files are now tracked.**
  - `internal/mod/server/repo_enterprise.go` — the `sqliteRepo` implementation
    of `CreateConnectionProfile` and 23 other server-key / connection-profile /
    known-host / credential-audit / SSH-agent methods required by the `Repo`
    interface.
  - `internal/mod/server/socket_test.go` — its accompanying test file.
- **README build/test instructions corrected.** The docs now state that
  `cmd/server/web/build` is generated (not committed), that `make build` builds
  the frontend before compiling Go, and that a raw `go build ./...` /
  `go test ./...` requires the embedded frontend to have been built once
  (otherwise it fails with `pattern all:web/build: no matching files found`).

## Features carried over from v1.5.0-beta.2 (unchanged)

### Connection fixes
- **Proxy-aware WebSocket origin check** — WebSocket upgrades no longer return
  `403 Forbidden` behind a reverse proxy; the origin hostname is compared against
  both `r.Host` and a trusted `X-Forwarded-Host`, and genuine cross-origin
  requests are still rejected. All 7 WebSocket upgraders share the single check.
- **SSH key-management routes registered** — `/api/servers/{id}/test-auth` and 7
  sibling routes no longer return `404 NOT_FOUND`.

### Frontend redesign (light / dark / system)
- Semantic CSS-variable token system; light/dark/system theme toggle; shared UI
  primitives restyled on the token layer; all pages swept onto the token system
  with layouts preserved; WCAG-oriented tap-target and `focus-visible` handling.

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

- **v1.5.0-beta.1 and v1.5.0-beta.2 tags are non-building** — use this tag or
  later. The earlier tags are left in place for history but should not be cloned.
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
- [ ] `make build` succeeds **from a fresh clone** (builds frontend, then Go).
- [ ] `go build ./...` succeeds (after the frontend has been built once).
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes (CI race job runs the full suite unbounded).
- [ ] `go vet ./...` is clean.
- [ ] `npm run check` (svelte-check) is clean and `npm run build` succeeds.
- [ ] Manual migration validation matrix on your own hosts:
      packages / configs / services / users / docker across your source→target
      distro pair, plus at least one interrupt-and-resume cycle and one rollback.

## Upgrade notes
- No schema migration is required by this release.
- No breaking API changes versus v1.4.x / earlier v1.5.0 betas in the committed
  set.
- If you cloned v1.5.0-beta.1 or v1.5.0-beta.2 and could not build, this tag is
  the fix — no code changes to features, only the missing files and ignore rule.

## Follow-up before a stable release
- Add a CI job that builds and tests **from a clean checkout** (not the dev
  tree) so a missing-tracked-file regression fails the pipeline, not the user.
- Instrument the pipeline path and `Engine.Resume` for observability.
- Review the no-credential SSH wizard probe and decide whether to keep it as-is,
  document it explicitly, or add optional key capture/compare behavior.
- Clear or explicitly suppress the staticcheck backlog, then promote staticcheck
  to a blocking CI gate.
- Extend idempotency guarantees across all pipeline stages.
- Add SBOM generation and build provenance/signing.
