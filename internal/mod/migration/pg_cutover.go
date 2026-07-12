package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"
)

// Phase 2A-4: PostgreSQL physical-replication cutover primitives.
//
// This file adds the pre-mutation preflight, lag verification, and promotion
// primitives the fenced cutover orchestrator (Phase 2A-6) drives against a
// PostgreSQL same-major-version source→target pair. It reuses the existing
// setupPostgreSQL seed and adds the gates the spec (§6) requires:
//
//   - Preflight: same-major version, source not in recovery, target in recovery
//     (standby after seed), disk capacity, replicator connectivity. Every
//     failure is a pre-mutation actionable error — no destructive command runs.
//   - Lag verification: pg_stat_replication on the SOURCE → flush_lag/replay_lag
//     ≤ threshold with a bounded wait (RPO ≈ 0 once lag=0 + source fenced).
//   - Promotion: pg_promote() via SQL on the target (not pg_ctl promote shell),
//     with an SSH shell fallback only if the SQL path is unavailable.
//
// All commands are read-only except pg_promote. Credential handling follows the
// repo convention (PGPASSWORD env, shared.ShellQuote). The security constraint
// holds: no secret on argv; PGPASSWORD is an env prefix, never a -p flag.

// PGPreflightResult is the evidence captured by Preflight. It is returned to
// the caller so NeedsManualIntervention can record concrete diagnostics, not
// just a boolean.
type PGPreflightResult struct {
	SourceVersionNum int    // SHOW server_version_num on source (e.g. 150003)
	TargetVersionNum int    // SHOW server_version_num on target
	SourceInRecovery bool   // pg_is_in_recovery() on source (must be false)
	TargetInRecovery bool   // pg_is_in_recovery() on target (must be true)
	TargetDiskMB     int    // target $PGDATA free space (MB), -1 if unprobeable
	ReplicatorOK     bool   // replicator can connect from target to source
	Notes            string // human-readable diagnostics
}

// ErrPGPreflight is returned by Preflight when a gate fails. It wraps the
// specific gate reason; callers fail closed to NeedsManualIntervention. It is
// distinct from ErrUnsafeTopology (rollback probe) — preflight is pre-mutation.
var ErrPGPreflight = fmt.Errorf("postgres preflight failed")

// PG Cutover thresholds. Kept as vars (not consts) so tests can shrink the
// bounded wait without sleeping for the full default.
var (
	// PGCatchUpMaxLag is the default max replay lag (seconds) before promote.
	PGCatchUpMaxLag int64 = 1
	// PGCatchUpTimeout bounds the catch-up wait. Zero = unbounded; the
	// orchestrator's ctx deadline is the real ceiling.
	PGCatchUpTimeout = 5 * time.Minute
	// PGCatchUpPoll is the lag re-check interval.
	PGCatchUpPoll = 2 * time.Second
)

// pgServerVersionNum runs `SHOW server_version_num` over SSH and returns the
// integer version (e.g. 150003). A non-numeric or failed probe returns an error.
func pgServerVersionNum(ctx context.Context, ssh SSHExecuter) (int, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx,
		"sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1")
	if err != nil {
		return 0, fmt.Errorf("server_version_num: %w", err)
	}
	if exit != 0 {
		return 0, fmt.Errorf("server_version_num exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	v, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("server_version_num parse %q: %w", out, err)
	}
	return v, nil
}

// pgMajorVersion returns the major version number (e.g. 15 from 150003 or
// 160001). PostgreSQL's server_version_num encodes major as the first 2 (for
// <10) or 2 digits; for 10+ it is major*10000 + minor. We compare the
// major-only floor: version/10000 for >=100000, else version/10000 won't apply.
// For 9.x (e.g. 90623) the major is version/10000 = 9; for 10+ (e.g. 150003)
// version/10000 = 15. So major = version/10000 works for both 9.x and 10+.
func pgMajorVersion(versionNum int) int {
	return versionNum / 10000
}

// Preflight runs the pre-mutation PG gates. It issues NO destructive command.
// Returns (result, nil) on success, (result, ErrPGPreflight) on any gate
// failure with the reason in result.Notes. ctx cancellation aborts promptly.
//
// Gates (spec §6):
//  1. same-major: source and target major versions match.
//  2. source is a primary: pg_is_in_recovery() == false.
//  3. target is a standby: pg_is_in_recovery() == true (seeded).
//  4. disk capacity: target $PGDATA has free space (best-effort; unprobeable
//     does not block — recorded in Notes — because a locked-down target may
//     deny df; the seed already consumed the bulk of the space).
//  5. replicator connectivity: the target can reach the source as replicator.
func (e *ReplicationEngine) Preflight(ctx context.Context, config ReplicationConfig) (PGPreflightResult, error) {
	var r PGPreflightResult

	// Gate 1: same-major.
	srcV, err := pgServerVersionNum(ctx, e.sourceSSH)
	if err != nil {
		r.Notes = "source version probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}
	r.SourceVersionNum = srcV
	tgtV, err := pgServerVersionNum(ctx, e.targetSSH)
	if err != nil {
		r.Notes = "target version probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}
	r.TargetVersionNum = tgtV
	if pgMajorVersion(srcV) != pgMajorVersion(tgtV) {
		r.Notes = fmt.Sprintf("cross-major: source=%d target=%d (same-major required)", srcV, tgtV)
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}

	// Gate 2: source not in recovery (primary).
	srcInRecovery, ok, err := pgInRecovery(ctx, e.sourceSSH)
	if err != nil || !ok {
		r.Notes = "source pg_is_in_recovery probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}
	r.SourceInRecovery = srcInRecovery
	if srcInRecovery {
		r.Notes = "source is in recovery (standby) — source must be primary"
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}

	// Gate 3: target in recovery (standby after seed).
	tgtInRecovery, ok2, err := pgInRecovery(ctx, e.targetSSH)
	if err != nil || !ok2 {
		r.Notes = "target pg_is_in_recovery probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}
	r.TargetInRecovery = tgtInRecovery
	if !tgtInRecovery {
		r.Notes = "target is NOT in recovery — target must be a standby after seed"
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}

	// Gate 4: disk capacity (best-effort). A denied df on a locked target is
	// not fatal; record it and continue. The seed already proved space exists.
	mb, diskNote := pgTargetFreeMB(ctx, e.targetSSH)
	r.TargetDiskMB = mb
	if diskNote != "" {
		r.Notes = diskNote
	}

	// Gate 5: replicator connectivity from target to source.
	replOK, replNote := pgReplicatorConnectivity(ctx, e.targetSSH, config)
	r.ReplicatorOK = replOK
	if !replOK {
		// Do not overwrite a more specific disk note; append.
		if r.Notes != "" {
			r.Notes += "; "
		}
		r.Notes += "replicator connectivity: " + replNote
		return r, fmt.Errorf("%w: %s", ErrPGPreflight, r.Notes)
	}
	return r, nil
}

// pgTargetFreeMB probes free space on the target's $PGDATA. Best-effort: a
// locked-down target may deny df. Returns (-1, note) when unprobeable so the
// caller records it without blocking the preflight.
func pgTargetFreeMB(ctx context.Context, ssh SSHExecuter) (int, string) {
	out, stderr, exit, err := ssh.ExecContext(ctx,
		"df -m --output=avail $(sudo -u postgres psql -tAc 'SHOW data_directory;') 2>&1 | tail -1")
	if err != nil || exit != 0 {
		return -1, "target disk probe unavailable: " + strings.TrimSpace(stderr)
	}
	mb, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1, "target disk probe parse: " + strings.TrimSpace(out)
	}
	return mb, ""
}

// pgReplicatorConnectivity checks the target can reach the source as the
// replicator user. Uses the source host from config (falling back to "source")
// and the configured port (default 5432). Returns (true, "") on success.
func pgReplicatorConnectivity(ctx context.Context, targetSSH SSHExecuter, config ReplicationConfig) (bool, string) {
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}
	port := config.SourcePort
	if port == 0 {
		port = 5432
	}
	replUser := config.ReplicationUser
	if replUser == "" {
		replUser = "replicator"
	}
	// A read-only probe: SELECT 1 via the replication connection. PGPASSWORD via
	// env (never -p on argv). No secret in the command string. Connect to the
	// `postgres` DB explicitly — without -d psql defaults dbname to the
	// username, which for a replication-only role does not exist.
	cmd := fmt.Sprintf("PGPASSWORD=%s psql -h %s -p %d -U %s -d postgres -tAc 'SELECT 1;' 2>&1",
		shared.ShellQuote(config.ReplicationPass), shared.ShellQuote(sourceHost), port, shared.ShellQuote(replUser))
	out, _, exit, err := targetSSH.ExecContext(ctx, cmd)
	if err != nil {
		return false, "psql probe error: " + err.Error()
	}
	if exit != 0 {
		return false, "psql probe exit " + strconv.Itoa(exit) + ": " + strings.TrimSpace(out)
	}
	if strings.TrimSpace(out) != "1" {
		return false, "unexpected probe output: " + strings.TrimSpace(out)
	}
	return true, ""
}

// pgStatReplicationLag queries the SOURCE for the replication lag of its
// connected standby (the target). Returns replay_lag in seconds (0 when caught
// up). A missing standby row or unparseable lag is an error — never 0, because
// 0 would let the orchestrator promote on un-replicated data.
//
// The query returns the replay_lag of the FIRST pg_stat_replication row. For a
// Phase 2A single-target pair there is exactly one standby. If multiple
// standbys exist, the operator must pin the target (deferred; recorded in
// known-limitations). The row's application_name or client_addr could
// disambiguate, but for the single-target slice the first row is the target.
func pgStatReplicationLag(ctx context.Context, sourceSSH SSHExecuter) (int64, error) {
	out, stderr, exit, err := sourceSSH.ExecContext(ctx,
		"sudo -u postgres psql -tAc "+
			shared.ShellQuote("SELECT COALESCE(EXTRACT(EPOCH FROM replay_lag)::bigint, 0) FROM pg_stat_replication LIMIT 1;")+
			" 2>&1")
	if err != nil {
		return -1, fmt.Errorf("pg_stat_replication: %w", err)
	}
	if exit != 0 {
		return -1, fmt.Errorf("pg_stat_replication exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	v := strings.TrimSpace(out)
	if v == "" {
		// No standby row → target not connected to source. Cannot verify lag.
		return -1, fmt.Errorf("pg_stat_replication: no standby row (target not connected)")
	}
	lag, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("pg_stat_replication parse %q: %w", v, err)
	}
	return lag, nil
}

// WaitForCatchUpPG bounds the catch-up wait on pg_stat_replication replay_lag.
// Returns nil when lag <= maxLagSeconds, ctx.Err() on cancellation. A lag probe
// error (no standby, unparseable) is returned immediately — it does NOT count
// as caught-up.
func (e *ReplicationEngine) WaitForCatchUpPG(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error {
	if maxLagSeconds < 0 {
		maxLagSeconds = PGCatchUpMaxLag
	}
	deadline := time.Now().Add(PGCatchUpTimeout)
	if PGCatchUpTimeout == 0 {
		deadline = time.Time{} // zero = rely on ctx
	}
	poll := PGCatchUpPoll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		lag, err := pgStatReplicationLag(ctx, e.sourceSSH)
		if err != nil {
			return fmt.Errorf("pg catch-up lag: %w", err)
		}
		if lag <= maxLagSeconds {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf("pg catch-up timeout: lag=%ds > %ds", lag, maxLagSeconds)
		}
	}
}

// PromotePG promotes the target standby to primary. SQL-first: `SELECT
// pg_promote()` over SSH (the spec mandates pg_promote() SQL, not pg_ctl
// promote shell). The shell pg_ctl promote is a FALLBACK only if the SQL path
// is unavailable (psql missing/broken on target). Returns nil only after a
// post-promote probe confirms the target is NO LONGER in recovery — promotion
// is not assumed from the command's exit code alone.
func (e *ReplicationEngine) PromotePG(ctx context.Context, config ReplicationConfig) error {
	out, stderr, exit, err := e.targetSSH.ExecContext(ctx,
		"sudo -u postgres psql -tAc 'SELECT pg_promote();' 2>&1")
	sqlOK := err == nil && exit == 0 && strings.TrimSpace(out) == "t"
	if !sqlOK {
		// Fallback: pg_ctl promote shell. Only when the SQL path failed.
		shellErr := e.promotePostgreSQLShell(ctx, config, out, stderr, exit, err)
		if shellErr != nil {
			return fmt.Errorf("pg_promote SQL (%s) and shell fallback (%s) both failed",
				pgPromoteDiag(out, stderr, exit, err), shellErr)
		}
	}
	// Post-promote verification: target must be out of recovery. This is the
	// ownership proof — the target is now writable. A failure here is
	// NeedsManualIntervention (promotion may have partially succeeded).
	inRecovery, ok, err := pgInRecovery(ctx, e.targetSSH)
	if err != nil || !ok {
		return fmt.Errorf("post-promote probe: %v", err)
	}
	if inRecovery {
		return fmt.Errorf("post-promote: target still in recovery (promote did not take effect)")
	}
	return nil
}

// promotePostgreSQLShell is the SSH pg_ctl promote fallback. It is only reached
// when the SQL pg_promote() path is unavailable. Returns nil on exit 0.
func (e *ReplicationEngine) promotePostgreSQLShell(ctx context.Context, config ReplicationConfig, sqlOut, sqlStderr string, sqlExit int, sqlErr error) error {
	cmd := "sudo -u postgres pg_ctl promote -D /var/lib/postgresql/*/main 2>&1"
	out, stderr, exit, err := e.targetSSH.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("shell promote: %w (out=%s)", err, strings.TrimSpace(out))
	}
	if exit != 0 {
		return fmt.Errorf("shell promote exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return nil
}

func pgPromoteDiag(out, stderr string, exit int, err error) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(out)
	}
	if err != nil {
		if detail != "" {
			return fmt.Sprintf("%v: %s", err, detail)
		}
		return err.Error()
	}
	return fmt.Sprintf("exit %d: %s", exit, detail)
}

// skipped: multi-standby target pinning (use application_name/client_addr), add
// when a Phase 2A test exercises >1 standby. For the single-target slice the
// first pg_stat_replication row is the target.
