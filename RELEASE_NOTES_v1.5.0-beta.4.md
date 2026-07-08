# Meshium v1.5.0-beta.4

**Pre-release / beta — migration-correctness candidate. Not a stable release.**

Fourth beta on the v1.5.0 line. This is a **migration-correctness release**: it
does not add migration features. It makes the migration paths stop claiming or
reporting work the live pipeline did not actually perform, and fails closed on
operations that cannot be verified as safe. Every change is a safety or honesty
fix grounded in the Migration Functional Audit.

Meshium is still **not** production-ready or enterprise-grade at this tag, and
this release makes no such claim. The migration paths are safer and more honest
here — not complete.

## Why this release exists

An audit of the migration paths found several places where the code reported
success for work it had not performed, or continued past an operation it could
not verify as safe:

- a traffic-switch cutover that could record success without switching;
- replication lag measurements that returned "caught up" on their error paths,
  which could promote a target on un-replicated data;
- a MongoDB cutover that reconfigured the live source's replica set before an
  inevitable abort;
- an "initial sync" stage whose name implied an rsync file transfer it does not
  do;
- Docker image-pull failures silently downgraded to warnings while the migration
  still reported success;
- a missing step handler that crashed the process with a nil dereference.

This release addresses each of these by doing the safe thing or refusing with a
clear error, and by making the capability descriptions match what the code
actually does.

## What changed since v1.5.0-beta.3

### False-success and crash safety
- **Traffic switch no longer reports a success it did not achieve.** The cutover
  traffic-switch path could record a successful switch even when the switch was
  not performed. (`internal/mod/migration`)
- **Missing step handlers no longer crash the migration.** A pipeline step with
  no registered handler dereferenced a nil handler and took the process down with
  a SIGSEGV; missing handlers are now guarded and surfaced as an error.

### Fail closed on unverifiable operations
- **Unknown distributions no longer fall back to `apt`.** An unrecognized source
  distro (e.g. Amazon Linux) previously fell through to a Debian-oriented `apt`
  path; the package path now refuses an unknown distro rather than running an
  unsafe fallback.
- **Alpine service migration is gated without OpenRC support.** systemd-oriented
  service migration is no longer issued against an Alpine/OpenRC host that does
  not use `systemctl`.
- **Replication lag paths fail closed.** PostgreSQL, MongoDB, and Redis lag
  measurements returned `0` ("caught up") on error paths, which could let a
  cutover promote on un-replicated data. They now return `-1` plus an error, and
  `WaitForCatchUp` propagates that error instead of treating a failed measurement
  as caught-up. (`internal/mod/migration/replication.go`)
- **MongoDB replication cutover is refused up front.** There is no real
  replica-set lag measurement, so catch-up cannot be verified before promotion.
  `setupMongoDB` now fails closed **before** issuing `rs.add()`, so a cutover that
  cannot complete safely never reconfigures the live source's replica set.

### Truthful capability reporting
- **"Initial sync" stage describes what it actually does.** It does not perform a
  file-level rsync; it replays the data collected during the collect phase via
  each category's `Applier.Apply`. The stage name, progress messages, and
  comments now say so, and the unused `SyncEngine`/`InitialSync` path is
  documented as not driven by the live pipeline. (`internal/mod/migration/pipeline.go`)
- **Docker image migration documented as registry-only.** Image transfer is
  `docker pull` only; images built locally and never pushed to a reachable
  registry cannot be migrated. Pull failures are now counted and reflected in the
  completion summary instead of being silently downgraded while success is still
  reported. (`internal/mod/migration/docker.go`, `dryrun.go`)
- **Assisted-cutover limitations clarified** in the documentation — no implication
  of a fully automated zero-downtime cutover where the path is assisted/manual.

### Architecture & validation documentation
- **Migration orchestrator consolidation plan** (`docs/orchestrator-consolidation.md`)
  records that three orchestrators (Job Engine `Engine`, `Executor`/
  `CompositeRunner`, and the `Pipeline`) each drive their own
  backup→apply→rollback loop, why that divergence is a correctness risk, and the
  decision to designate the Pipeline as the single go-forward path with guard
  rails while the others are frozen.
- **Migration validation matrix and non-destructive scripts**
  (`docs/validation/migration-matrix.md`, `scripts/validate-fresh-clone.sh`,
  `scripts/validate-migration-lab.sh`) — a manual E2E test matrix, a local mirror
  of CI's fresh-clone build guard, and a lab bring-up/health-check script. Both
  scripts are non-destructive and never trigger a migration themselves
  (migrations remain UI/API-driven).

### CI
- **Fresh-clone build guard** — CI builds and tests from a `git archive` of `HEAD`
  (tracked files only) and fails if a tracked source file is swallowed by a
  `.gitignore` rule — the regression that broke the beta.1/beta.2 tags.

## Known limitations
- **MongoDB replication cutover is unsupported** — refused until real replica-set
  lag measurement is implemented.
- **Locally-built Docker images cannot be migrated** — image transfer is
  registry-pull only; only images present in a registry the target can reach
  migrate.
- **Three migration orchestrators still coexist** — consolidation onto the
  Pipeline is planned (see `docs/orchestrator-consolidation.md`), not yet
  performed. A fix to one apply loop does not automatically propagate to the
  others.
- **"Initial sync" is a data replay, not a file-level rsync** — the `SyncEngine`
  is not driven by the live pipeline.
- All limitations carried from v1.5.0-beta.3 still apply (pipeline path not
  instrumented for observability, `Engine.Resume` not instrumented,
  staticcheck/govulncheck advisory backlog, bounded idempotency, no SBOM / build
  provenance / signing).

## Validation checklist (run before trusting a real migration)
- [ ] `make build` succeeds **from a fresh clone** (builds frontend, then Go), or
      run `scripts/validate-fresh-clone.sh`.
- [ ] `go build ./...` succeeds (after the frontend has been built once).
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes.
- [ ] `go vet ./...` is clean.
- [ ] `npm run check` (svelte-check) is clean and `npm run build` succeeds.
- [ ] Manual migration validation matrix on your own hosts — see
      `docs/validation/migration-matrix.md` — across your source→target distro
      pair, plus at least one interrupt-and-resume cycle and one rollback.

## Upgrade notes
- No schema migration is required by this release.
- No breaking API changes versus v1.5.0-beta.3; no WebSocket or REST contract
  consumed by the frontend changed.
- Behavior change to be aware of: operations that previously reported success and
  continued (unverifiable replication catch-up, MongoDB cutover, unknown-distro
  package install, Alpine service migration) now fail closed with a clear error.
  This is intentional — a refusal replaces a false success.

## Follow-up before a stable release
- Execute the orchestrator consolidation plan: extract a shared
  backup→apply→rollback core and route the Executor and Job Engine paths onto it.
- Implement real MongoDB replica-set lag measurement, then re-enable the MongoDB
  cutover path.
- Instrument the pipeline path and `Engine.Resume` for observability.
- Clear or explicitly suppress the staticcheck backlog, then promote staticcheck
  to a blocking CI gate.
- Add SBOM generation and build provenance/signing.
