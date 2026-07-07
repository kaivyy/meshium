# Migration Orchestrator Consolidation Plan

**Status:** Proposed (decision record). No code is changed by this document.
**Scope:** The three migration orchestration paths in `internal/mod/migration`
and `internal/jobengine`. This is a plan, not an implementation.

This document is code-grounded. Every path and call site named below was
verified against the tree at the time of writing; where this document and the
code disagree, the code wins and this document is the bug. It complements
[`architecture.md`](architecture.md), which describes the paths as they exist;
this document records the decision about how they should converge.

## The problem: three orchestrators, three apply loops

`architecture.md` documents two migration *paths* (Jalur A / Job Engine and
Jalur B / Pipeline). At the orchestration layer there are in fact **three**
distinct drivers of the backup → apply → rollback sequence, each with its own
copy of that loop:

| # | Orchestrator | Constructed in | User-facing entry | Apply/backup/rollback site |
|---|--------------|----------------|-------------------|----------------------------|
| 1 | `Engine` (`engine.go`, state machine) | `main.go` `NewEngine` (also `recovery.go`) | Job Engine jobs (Jalur A) | `step.go:116` Backup, `:128` Apply, `:166` Rollback |
| 2 | `Executor` (`executor.go`), wrapped by `CompositeRunner` | `main.go` `NewExecutor` / `NewCompositeRunner` | `migrationHandler` → `/ws/migrate/`, `/api/migrations` | `executor.go:313` Backup, `:389` Apply, `:490` Rollback |
| 3 | `Pipeline` (`pipeline*.go`, staged) | `main.go` `NewPipeline` | `pipelineHandler` → `/ws/pipeline/`, `/api/pipeline/*` | `pipeline.go` `initialSyncStage` + live replication/cutover stages |

All three are registered in `cmd/server/main.go` (`migrationHandler.RegisterRoutes`,
`pipelineHandler.RegisterRoutes`) and all three resolve category modules through
the same `CategoryRegistry` (`categories.go`). The frontend calls **two** of
them directly: `web/src/lib/api/migrations.ts` drives `/ws/migrate/` (Executor)
and `web/src/lib/api/pipeline.ts` drives `/ws/pipeline/` (Pipeline). The Job
Engine `Engine` is driven server-side by queued jobs rather than a dedicated WS
client.

### Why this is a correctness risk, not just untidiness

The three apply loops are separate implementations of the same safety-critical
sequence (backup must precede apply; apply failure must roll back applied
categories in LIFO order). A fix applied to one loop does **not** propagate to
the others. Several of the migration-correctness fixes in this beta cycle live
in shared category code (e.g. `services.go`, `replication.go`, `docker.go`,
`distro.go`), so they are seen by all three paths — but any fix made *in an
orchestrator's own loop* (progress semantics, checkpoint ordering, rollback
triggering, false-success guards) has to be duplicated three times or it
silently regresses on the paths that were missed. Divergence between the loops
is the underlying hazard behind the "two parallel worlds" findings in the audit.

## Decision

**Designate the Pipeline (path 3) as the single go-forward orchestrator for new
migration work, and freeze the Engine and Executor loops against new feature
work while they are progressively retired.** No path is deleted in this beta;
this records the direction and the guard rails, not a cutover.

Rationale, grounded in the code:

- The Pipeline is the only path with the staged live-replication and cutover
  machinery (`liveReplicationStage`, `CutoverEngine`) that the honest-downtime
  model in `architecture.md` depends on. The other two paths have no cutover
  stage.
- The Pipeline is the path the newer frontend surfaces build against
  (`/api/pipeline/*`: risk, compatibility, health, replication, traffic,
  metrics, audit) — it already carries the richest user-facing contract.
- Consolidating onto the path with the most capability is lower-risk than
  back-porting cutover into the two simpler loops.

## Guard rails until consolidation completes

These are the rules that keep the three loops from diverging further while they
coexist. They are behavioral commitments, not code in this document:

1. **No new migration capability lands in `Engine` or `Executor` loops.** New
   orchestration behavior goes into the Pipeline. Bug fixes to the frozen loops
   are allowed only to preserve safety (fail-closed, backup-before-apply).
2. **Safety-critical fixes go in shared category code where possible.** A fix in
   an `Applier` (`categories.go` modules) is seen by all three paths. Prefer
   that over editing an orchestrator's private loop.
3. **When a fix must touch one orchestrator loop, check the other two.** If the
   same defect exists in `step.go`, `executor.go`, and `pipeline.go`, fix all
   three in the same change or file a tracked follow-up naming the unpatched
   sites.
4. **No path may report success it did not perform.** This is the standing
   migration-correctness target for the beta and applies identically to all
   three loops.

## Consolidation steps (future work, not this beta)

Ordered, each independently reviewable; none are performed by this document:

1. **Inventory parity.** Enumerate every behavioral difference between the three
   apply loops (checkpoint granularity, rollback trigger conditions, progress
   message contract, concurrency guard). Record as a parity table.
2. **Extract a shared apply/rollback core.** Factor the backup → apply →
   checkpoint → rollback sequence (currently triplicated at the sites in the
   table above) into one internal helper the orchestrators call, so there is a
   single implementation of the safety sequence.
3. **Route the Executor path to the Pipeline.** Make `/ws/migrate/` and
   `/api/migrations` execution delegate to the Pipeline (or a thin shim over the
   shared core), keeping the existing request/response and WS message contract
   so the frontend (`migrations.ts`) is unaffected.
4. **Fold the Job Engine `Engine` onto the shared core.** The Job Engine keeps
   its queue/worker/shutdown responsibilities; only its step-application inner
   loop moves to the shared core.
5. **Delete the retired loops** once no route or job reaches them, and update
   `architecture.md` to describe a single orchestrator.

## Non-goals

- Deleting any orchestrator in this beta.
- Changing any WebSocket or REST contract the frontend depends on.
- Introducing zero-downtime claims — the honest-downtime model in
  `architecture.md` is unchanged.

## Verification for each future step

Each consolidation step above must, before it is committed: build
(`go build ./...`), vet (`go vet ./...`), pass the migration package tests
(`go test ./internal/mod/migration/...`) including under `-race`, and preserve
the frontend contract (no change to the messages `migrations.ts` /
`pipeline.ts` consume). A step that cannot meet all of these is not ready.
