# Transfer Runbook (Phase 2B — Large-Transfer Correctness)

Operator-facing guide for Meshium's direct rsync transfer path, its durable
checkpoint/reconcile, and the honest `degraded` fallback. Read this before
triggering a large transfer or acting on a transfer failure.

Scope: this covers the **transfer engine** (`internal/mod/transfer`). It does
not cover cutover/fencing (that is `docs/cutover-runbook.md`) and it does **not**
claim zero-downtime. Downtime is still the transfer time.

---

## 1. What actually happens

For a plain file/directory step, Meshium now prefers **direct rsync
source→target over SSH**:

- rsync runs **on the source host** and pushes bytes **straight to the target
  host**; Meshium only relays the rsync control channel (file list, progress),
  never the data. The bytes never land on Meshium's disk.
- Selected only when **all three** hold:
  1. rsync is installed on the **source**,
  2. rsync is installed on the **target**,
  3. the source can reach the target over SSH with no password prompt
     (BatchMode probe).
- Transfer args: `rsync -avz --partial --append-verify --mkpath`. `--partial`
  keeps a half-written file; `--append-verify` resumes it with a checksum;
  `--mkpath` creates the target's parent dirs.

If any of the three conditions fails **and** the step is *not* marked
`AllowDegraded`, Meshium **fails closed** (`ErrTransferStrategyUnavailable`) —
it does **not** silently downgrade to a tar-over-SSH relay.

If the step *is* `AllowDegraded=true`, Meshium emits a **`DEGRADED` warning**
to the operator channel and uses the explicit tar-over-SSH relay fallback. This
is an intentional, operator-visible downgrade — never silent.

---

## 2. Pre-conditions (verify before triggering)

- rsync present on **both** ends: `command -v rsync` returns a path.
- SSH from source → target works non-interactively:
  - key-based auth, `StrictHostKeyChecking accept-new` or pre-populated
    `known_hosts`,
  - no `sudo`/`password` prompt.
- Target parent path writable by the SSH user (rsync will `--mkpath` the
  immediate parent, but the mount must exist).
- For Docker volumes: the path must be classifiable. A **bare token**
  (`mydata`) or a `/var/lib/docker/volumes/.../_data` path is `named`; an
  absolute `/...` path is `bind`. Anything unverifiable (`VolumeUnknown`) routes
  to **manual intervention** — Meshium will not guess.

---

## 3. Triggering

The transfer step is created by the planner. Two operator-relevant flags:

- `AllowDegraded` (bool) — permits the operator-visible `degraded` relay when
  direct rsync is unavailable. Default **false**.
- `Category` (string) — `configs`, `docker-volume`, etc. Drives compression
  policy (configs/text compressed; docker-volume never compressed).

If direct rsync is unavailable and `AllowDegraded=false`, the step errors with
`ErrTransferStrategyUnavailable` and the migration stops at the transfer step
for operator decision. This is correct, honest behavior — do not treat it as a
bug.

---

## 4. Failure handling — resume vs fresh vs manual

After any restart or interruption, Meshium reconciles the durable
`TransferCheckpoint` and returns one of three verdicts:

| Verdict | Meaning | Action taken |
|---|---|---|
| `Resume` | a partial target exists **and** the source snapshot is unchanged | resume via rsync `--partial --append-verify` |
| `FreshStart` | nothing durable was transferred | start the transfer from scratch |
| `ManualIntervention` | source changed / partial missing or mismatched / strategy invalid / path unverifiable | stop; route to `needs_manual_intervention` |

**Decision tree for the operator on a transfer failure:**

1. Read the terminal state in the migration status:
   - `failed` — terminal, safe to retry the step.
   - `degraded` — the relay fallback ran; verify the target manually (§6).
   - `needs_manual_intervention` — do NOT blindly retry.
2. If `needs_manual_intervention`:
   - Check whether the **source changed** after the partial transfer. If it
     did, the partial target is untrustworthy → **delete the partial on the
     target** and start `FreshStart`.
   - If the source is unchanged but the partial looks corrupt, delete it and
     resume.
   - If you cannot confirm either, leave it for manual verification — do not
     let Meshium resume blindly.
3. On `failed`, retry the step. `VerifyInto` runs after the transfer; a checksum
   mismatch flips the step to `needs_manual_intervention` (fail closed). No
   success is recorded without a verified checksum.

**Key invariant:** a checkpoint is persisted as complete
(`LastVerifiedPhase="verified"`) **only** when `VerifyInto` proves the checksum
matches. A mismatch is never shrugged off.

---

## 5. Backout

- Transfers are point-to-point copies; there is no built-in "undo". To back out,
  delete the copied tree on the target.
- For Docker volumes the `degraded` relay is a copy too — backout = remove the
  target copy.
- Rolling back a migration that contains a transfer step ends in
  `rollback_degraded` if the transfer already completed (the target now holds
  real data); see `docs/known-limitations.md` wording rules. Partial transfer
  failure is never reported as a clean rollback.

---

## 6. Manual verification commands

Run on the source and target hosts over SSH, compare the digests.

```sh
# File or directory tree — compare recursive sha256 of the whole tree:
source$ cd /path/to/src && find . -type f -exec sha256sum {} \; | sort > /tmp/src.sums
target$ cd /path/to/dst && find . -type f -exec sha256sum {} \; | sort > /tmp/dst.sums
# diff, ignoring leading path differences:
diff <(awk '{print $1}' /tmp/src.sums) <(awk '{print $1}' /tmp/dst.sums) && echo IDENTICAL

# Single large file:
sha256sum /path/to/blob.bin   # run on both; compare the first field

# Confirm rsync availability on both ends:
command -v rsync || echo "rsync MISSING"
ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new user@target 'command -v rsync' \
  || echo "target unreachable / rsync missing"

# Inspect a partial that rsync left behind:
ls -l /path/to/dst/blob.bin.partial   # rsync --partial names vary; check the dir
```

If the source snapshot changed, treat the target partial as poisoned:
remove it and re-run the step (`FreshStart`).

---

## 7. Honest status summary (what the UI/API may say)

- "direct rsync source→target" / "resumable (rsync --partial)" /
  "verify-then-advance" / "degraded fallback (operator-visible)".
- Terminal states reported truthfully: `ok`, `failed`, `degraded`,
  `needs_manual_intervention`.
- **Never** claim "zero-downtime", "automatic resume", or "resumable large
  transfer" for Phase 1. The direct rsync path is resumable-after-restart with
  fail-closed reconcile; it is not continuous replication.
