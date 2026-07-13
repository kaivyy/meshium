# Phase 4 — Release Matrix & Supported Configurations

> Authoritative list of what Meshium supports for production, and the exact
> supported combinations an operator may select. Companion to
> `phase-4-production-certification.md` and `phase-4-runbooks.md`.

## Product wording (fixed)

**"Meshium performs a minimal-downtime migration for supported configurations."**

Supported configurations are enumerated explicitly below. Anything not listed
is `manual`, `degraded`, `blocked`, or `deferred` — and must never be selected
as an automatic cutover.

## Supported automatic combinations

An **automatic** cutover requires ALL of: a supported engine in `automatic`
status, a fenced traffic provider (nginx/haproxy/caddy), and a certified
execution mode. The matrix:

| Engine | Execution mode | Traffic provider | Automatic? |
|---|---|---|---|
| PostgreSQL | host / container / compose | nginx / haproxy / caddy | **YES** (minimal downtime) |
| MySQL | host / container | nginx / haproxy / caddy | **YES** (minimal downtime) |
| Redis | host / container | nginx / haproxy / caddy | **NO — `degraded`** (no source freeze; manual freeze required) |
| MongoDB | any | any | **NO — `blocked`** (no automatic cutover contract) |
| PostgreSQL logical (pub/sub) | host / compose | nginx / haproxy / caddy | **NO — `degraded`** (primitives proven; not wired to orchestrator) |

## Honest status legend

| Status | Meaning | Operators may… |
|---|---|---|
| `automatic` | fenced cutover, verified live, opt-in via `autoCutover=true` | select it; traffic switches + target promoted automatically |
| `degraded` | cutover possible but with a known safety caveat | select it AND perform the required manual step (e.g. freeze Redis writes) |
| `blocked` | not safe; fails closed | select it ONLY as a manual switch; automatic is rejected at API + execution |
| `deferred` | not yet proven | not selectable as automatic |
| `manual` | replication/seed supported; operator performs cutover | use the manual path (`autoCutover=false`, default) |

## What is certified vs. not (one-glance)

- ✅ PostgreSQL automatic on host / container / **compose (4A)**.
- ✅ MySQL automatic on host / container.
- ✅ Traffic providers nginx / haproxy / **caddy (4B)** — all fenced + read-after-write.
- ✅ WAN/large transfer resume + integrity (4C).
- ✅ PostgreSQL logical replication primitives (4D) — available, scoped `degraded`.
- ✅ Single server-side policy engine (4E) gates engine/provider at API + execution.
- ⛔ Bastion/jump-host automatic cutover — `blocked` (no live test).
- ⛔ Cross-major version cutover — `blocked` for all engines.
- ⛔ MongoDB automatic — `blocked`.
- ⛔ External DNS/Cloudflare providers — `blocked` (no staging creds).
- ⛔ Redis automatic — `degraded` (no source freeze; dual-writer window bounded, not zero).

## How to introspect at runtime

`GET /api/pipeline/policy` returns the live `PolicyMatrix`: supported engines,
traffic providers, replication modes, execution modes, `autoCutoverDefault`
(false), and honest caveats. Use it to verify a target server's policy before
planning a migration — the response is the same source of truth enforced at
execution time.
