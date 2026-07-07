# Migration Validation Matrix

**Status:** Validation plan for the next beta. This document defines *what must
be validated* and *how a human validates it*; it does not claim any of these
scenarios currently passes. Rows are marked with an explicit status so the
matrix cannot itself become an unproven capability claim.

This matrix is code-grounded. It reflects how Meshium actually runs at the time
of writing:

- The server is configured **only** through environment variables
  (`MESHium_PORT`, default `9527`; `MESHium_DATA_DIR`; `MESHium_LOG_LEVEL`;
  `MESHium_TLS_CERT` / `MESHium_TLS_KEY`). See `internal/shared/config.go`.
- There is **no CLI flag that triggers a migration.** A migration is started
  only through the web UI or its HTTP/WebSocket API
  (`/api/migrations`, `/api/pipeline/migrations`, `/ws/migrate/`,
  `/ws/pipeline/`, `/ws/dryrun/`). See `internal/mod/migration/handler.go` and
  `internal/mod/migration/pipeline_handler.go`.
- Health is reported at `GET /api/health` → `{"status":"ok"}`
  (`cmd/server/main.go`).

Because of this, the scripts in `scripts/` **cannot** perform an end-to-end
migration unattended — driving one requires an authenticated API/WS session
against real source and target hosts. The scripts instead prepare and verify the
preconditions (build integrity, lab bring-up, health) and default to
non-destructive behavior. The migration step itself is a documented **manual**
action in each scenario below.

## How to read the status column

| Status | Meaning |
|--------|---------|
| `planned` | Scenario defined here; not yet executed against a real lab. No claim of success. |
| `manual` | The step must be performed by a human through the UI/API; scripts cannot do it unattended. |
| `automated` | A script in `scripts/` performs and checks this without a live migration. |

No row is marked "passing" in this document. Passing status belongs in a test
run record, not in the plan.

## Preconditions (automated where possible)

| # | Check | How | Status |
|---|-------|-----|--------|
| P1 | Tree builds from tracked files only (fresh-clone guard) | `scripts/validate-fresh-clone.sh` | automated |
| P2 | Go vet + unit tests + race pass | `make test` then `go test -race ./...` | automated |
| P3 | Lab source/target hosts reachable and healthy | `scripts/validate-migration-lab.sh` | automated (bring-up + health only) |
| P4 | Meshium server healthy | `GET /api/health` → `{"status":"ok"}` | automated |

## Migration scenarios (manual through UI/API)

Each row is one migration a validator runs by hand against a source→target pair,
then records the result out-of-band (not in this file). "Dry-run first" means:
use `/ws/dryrun/` (or the UI dry-run) and confirm the plan before any apply.

| # | Scenario | Source → Target | What it exercises | Status |
|---|----------|-----------------|-------------------|--------|
| M1 | Same-distro baseline | Ubuntu → Ubuntu (same major) | Happy path: packages, services, files | planned / manual |
| M2 | Same-distro Debian | Debian → Debian (same major) | apt path on a non-Ubuntu Debian | planned / manual |
| M3 | Cross-distro family | Ubuntu → Rocky/Alma | apt→dnf translation; must fail closed where unsupported (see `distro.go`) | planned / manual |
| M4 | Node web app | Next.js app + reverse proxy | App files, node runtime, service unit | planned / manual |
| M5 | Docker Compose stack | Compose project | **Registry-only image pull** (locally-built unpushed images cannot migrate — see `docker.go`) | planned / manual |
| M6 | MySQL | MySQL → MySQL | DB dump/restore or replication path | planned / manual |
| M7 | PostgreSQL | PostgreSQL → PostgreSQL | Replication lag must be verified before promotion (`replication.go`) | planned / manual |
| M8 | Redis / BullMQ | Redis → Redis | `redisLag` must fail closed on a non-replica target before cutover | planned / manual |
| M9 | MongoDB (negative) | MongoDB → MongoDB | Cutover **must refuse**: `setupMongoDB` fails closed without touching the source (`replication.go`) | planned / manual |

## Failure-injection scenarios (manual)

These prove the correctness fixes from this beta cycle behave under fault, not
just on the happy path.

| # | Inject | Expected honest behavior | Status |
|---|--------|--------------------------|--------|
| F1 | Unknown/unsupported distro | Refuse with a clear error; no unsafe apt fallback (`distro.go`) | planned / manual |
| F2 | Alpine target without OpenRC support wired | Service migration gated, not silently skipped (`services.go`) | planned / manual |
| F3 | Missing step handler | Guarded — no SIGSEGV; migration reports the gap (`step.go` handler guard) | planned / manual |
| F4 | Replication never catches up (lag > threshold) | No promotion; `WaitForCatchUp` does not treat error as caught-up | planned / manual |
| F5 | Image only built locally (never pushed) | Pull reported as failure and counted; completion summary says it was NOT migrated (`docker.go`) | planned / manual |
| F6 | Traffic switch on a target that is not ready | No false-success traffic switch (B1 fix) | planned / manual |

## Rollback scenarios (manual)

| # | Scenario | Expected | Status |
|---|----------|----------|--------|
| R1 | Apply fails mid-run | Applied categories roll back in LIFO order; backup restored | planned / manual |
| R2 | Operator-initiated rollback after apply | Documented rollback path executes and reports honestly | planned / manual |
| R3 | Process crash mid-state-machine | Recovery on restart resumes or safely aborts (`recovery.go`) | planned / manual |

## What "ready for the next beta" requires

The beta-correctness target is: *Meshium must not claim or report a migration
capability the live pipeline did not perform.* For that target, the minimum
gate before tagging the next beta is:

1. P1–P4 automated checks pass locally and in CI.
2. The negative scenarios (M9, F1–F6) are executed at least once and behave as
   the "expected honest behavior" column states — these directly guard the
   no-false-claims target.
3. At least one positive same-distro scenario (M1) is executed end-to-end so the
   happy path is not purely theoretical.

Positive cross-distro and database scenarios (M3, M6–M8) are desirable but are
**not** gates for a *beta* prerelease, provided the code fails closed where they
are not yet validated. They are gates for any future non-beta release.
