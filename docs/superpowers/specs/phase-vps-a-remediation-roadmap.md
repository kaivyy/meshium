# Phase VPS-A — Remediation Roadmap

**2026-07-26** · Companion to
`phase-vps-a-meshium-vps-migration-readiness-audit.md` (evidence anchor
`9427118`). Ordered by gate. Nothing in a later gate should be implemented
before its prerequisites — building autonomy on an unverified write path
just automates the blast radius.

---

## Gate 0 → Staging use (disposable VPS pairs) — **COMPLETE 2026-07-26**

Status: B1 ✅ (10/10 live) · B2 ✅ (already existed, finding retracted) ·
B3 ✅ (0600 + retention, live 429MB→130MB) · B4 ✅ (mode/owner fidelity, live).
Gate 1 items below are the next tranche.

Blockers before Meshium may be pointed at any VPS someone would mind losing:

1. **VPS-B1 — Live execution matrix on disposable pairs.** Stand up two
   throwaway VPSes (or 2 privileged containers with systemd) and run the
   full matrix: apply each category, verify, induced failure mid-apply,
   rollback, retry-after-partial, cancel-during-execution, SSH drop, target
   reboot. Record DB evidence per run. This is the single largest gap —
   every apply/rollback path is `[CODE]`-only today. *Area:*
   test harness + `docs/superpowers/specs/` results doc. *Depends on:* none.
2. ~~**VPS-B2 — Per-target migration lock.**~~ **ALREADY IMPLEMENTED** —
   verified 2026-07-26: `tryAcquireResource(SourceID, TargetID)` claims both
   hosts atomically and fails closed (pipeline.go:224-235,
   `concurrency_resource_test.go`). The VPS-A finding was wrong and is
   retracted. No work needed.
3. **VPS-B3 — DB file permissions + retention.** Chmod 0600 on create;
   retention/pruning for step payloads (DB is 429MB). *Area:* `internal/db`,
   repo cleanup job.
4. **VPS-B4 — Config metadata fidelity.** Capture and restore owner, mode
   (at minimum) for configs; today content-only writes can silently flip
   root:600 secrets to default umask. *Area:* configs.go (tar already
   carries mode bits — parse and apply them).

## Gate 1 → Low-risk non-critical VPS with supervision

5. **VPS-B5 — Post-apply per-item probes.** Verification depth: package
   exists (`dpkg-query -W`), config sha256 match, service `is-active`,
   db row/table counts. Wire into healthVerificationStage so runtime/app
   levels are earned per category, not just docker. *Depends:* B1 harness.
6. **VPS-B6 — Populate `backup_ref` per item** and surface rollback
   feasibility per item in compare/pipeline UI; classify mixed-category
   rollback honestly in the UI. *Area:* executor/appliers + FE.
7. **VPS-B7 — Stale-plan hard gate.** Block execute (not just warn) when
   target inventory changed after plan beyond a threshold; force re-compare.
   *Area:* pipeline Execute precondition + parity engine.
8. **VPS-B8 — Session watchdogs around `NewSession`/`sftp.NewClient`** so a
   wedged transport cannot hang a stage until ctx timeout. *Area:*
   ssh/client.go.
9. **VPS-B9 — Category maturity labels in UI.** docker/database marked
   experimental at selection time; volume-data explicitly "not migrated".

## Gate 2 → Production with mandatory runbook (supervised)

10. **VPS-B10 — Docker recreate fidelity or honest refusal.** Recreate with
    ports/volumes/networks/restart-policy from inspect, or refuse
    non-compose containers with manual_required. Volume data transfer
    (rsync of mountpoints) or explicit unsupported flag per container.
11. **VPS-B11 — Database live verification.** Row counts/checksums
    post-restore; refuse cutover on divergence. MySQL replica seeding for
    the replication path (known gap since phase-5 design).
12. **VPS-B12 — Rollback drills in CI.** The B1 harness runs
    rollback-after-partial per category on every release; failures block.
13. **VPS-B13 — sudo-password path** or explicit root-only enforcement at
    server add time (today root is silently assumed).
14. **VPS-B14 — Redaction sweep of WS/step logs** (stderr passthrough) with
    a shared sanitizer at the WSMessage boundary.
15. **VPS-B15 — Pool refcount/session-cap** (known limitations noted in
    CHANGELOG Phase 6D): eviction under live use, MaxSessions ceiling.

## Gate 3 → Autonomous production (do not start until Gate 2 complete)

16. **VPS-B16 — Fenced autoCutover live certification** per engine×provider
    on the B1 harness, incl. fence-lease contention tests.
17. **VPS-B17 — Automatic rollback policy** with health-based triggers and
    post-cutover reverse-switch (currently impossible).
18. **VPS-B18 — Divergence monitoring** between source and target during
    observation window.

## Explicit "do not build yet"

- No autonomous cutover UI affordances until VPS-B16.
- No "migrate volume data" quick-fix inside the current `docker run`
  reconstruction (belongs to B10's design).
- No schema work for item results/selection history — **already exists**;
  the earlier plan drafts proposing to add these tables are stale and must
  not be re-implemented.
