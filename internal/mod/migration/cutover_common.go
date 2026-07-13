package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

// Phase 2C: engine-agnostic fenced cutover primitives.
//
// The Phase 2A orchestrator (cutover_orchestrator.go) drives three primitives:
// CutoverPreflight (read-only, before any mutation), WaitForCatchUp
// (engine-dispatch, already existed), and CutoverPromote (the only data-plane
// write). All three are defined per-engine here so PostgreSQL, MySQL, and Redis
// share one orchestrator without coupling it to *ReplicationEngine's internals.
//
// Hard rules (carry the mandate's fail-closed contract):
//   - Every cutover mutation runs behind the orchestrator's AssertHolds fence.
//   - Preflight is READ-ONLY except it must block clearly-unsupported configs
//     with a concrete, actionable error before any CHANGE/START/RESET runs.
//   - Promote requires a role/writability check BEFORE and AFTER; a target that
//     did not become a primary → NeedsManualIntervention (never assumed).
//   - A source that could still accept writes after the target is promoted
//     (dual-writer window) → fail closed. The hard fence (lease) serializes the
//     cutover; the source freeze (read_only) removes the dual-writer window.

// CutoverPreflightResult is the engine-agnostic pre-cutover evidence. It is
// returned by CutoverPreflight and recorded on the checkpoint so
// NeedsManualIntervention carries concrete diagnostics, not a boolean.
//
// Field semantics are engine-neutral:
//   - TargetInRecovery = "target is in the expected pre-switch role": a read-only
//     standby for PostgreSQL/MySQL, a running replica for Redis. The orchestrator
//     requires this true at Seed and VerifyingTarget.
//   - SourceInRecovery = "source is in the expected pre-switch role": a writable
//     primary (false). A source that is itself a replica is rejected.
type CutoverPreflightResult struct {
	SourceVersionNum int    // source major*10000+minor (0 if unprobeable)
	TargetVersionNum int    // target major*10000+minor
	SourceInRecovery bool   // source is a replica/standby (must be false)
	TargetInRecovery bool   // target is a replica/standby (must be true)
	TargetDiskMB     int    // target free space (MB); -1 if unprobeable
	ReplicatorOK     bool   // target can reach source as the replicator
	Notes            string // human-readable diagnostics / actionable block reason
}

// ErrCutoverPreflight is returned by CutoverPreflight when a gate fails. It wraps
// the specific gate reason; callers fail closed to NeedsManualIntervention.
var ErrCutoverPreflight = fmt.Errorf("cutover preflight failed")

// CutoverPreflight runs the read-only pre-mutation gates for the engine named in
// config.DatabaseType. It issues NO destructive command. Returns (result, nil)
// on success, (result, ErrCutoverPreflight) on any gate failure with the reason
// in result.Notes. MongoDB returns an immediate fail-closed error (no replica-set
// lag measurement → no safe cutover).
//
// sourceFrozen indicates the FencingSource step has already run: the source is
// expected to be physically frozen (read_only=ON for MySQL). Pre-freeze the
// source must be a writable primary; post-freeze a still-frozen source is
// required and a source that became writable again means the freeze was lost
// out-of-band → fail closed. PostgreSQL's freeze uses default_transaction_read_only
// (which does not flip pg_is_in_recovery), so the param is inert there but kept
// uniform across engines.
func (e *ReplicationEngine) CutoverPreflight(ctx context.Context, config ReplicationConfig, sourceFrozen bool) (CutoverPreflightResult, error) {
	switch config.DatabaseType {
	case "postgres", "postgresql":
		return e.preflightPostgresCutover(ctx, config, sourceFrozen)
	case "mysql", "mariadb":
		return e.preflightMySQL(ctx, config, sourceFrozen)
	case "redis":
		return e.preflightRedis(ctx, config, sourceFrozen)
	case "mongodb":
		// MongoDB has no source-freeze primitive and its only cutover lever
		// (rs.stepDown / rs.remove) reconfigures the LIVE source replica set,
		// which is out-of-bounds for an automatic, fenced cutover (rules #3/#8).
		// Replica-set lag IS now measurable (mongoDBLag via rs.status() optimeDate),
		// but the missing freeze + destructive-only promotion path keeps Mongo
		// BLOCKED for automatic. The structured probe below still runs so the
		// operator gets an actionable verdict for a manual cutover; it does not
		// enable automatic. Fail closed HERE, before any mutation.
		return CutoverPreflightResult{}, fmt.Errorf("%w: mongodb automatic cutover is not supported (no source freeze; promotion would reconfigure the live source replica set)", ErrCutoverPreflight)
	default:
		return CutoverPreflightResult{}, fmt.Errorf("%w: unsupported engine %q", ErrCutoverPreflight, config.DatabaseType)
	}
}

// preflightPostgresCutover adapts the Phase 2A PostgreSQL Preflight into the
// engine-agnostic result. The PG Preflight already enforces same-major,
// source-primary, target-standby, disk, and replicator gates.
func (e *ReplicationEngine) preflightPostgresCutover(ctx context.Context, config ReplicationConfig, sourceFrozen bool) (CutoverPreflightResult, error) {
	r, err := e.Preflight(ctx, config)
	if err != nil {
		// err already wraps ErrPGPreflight with Notes inside r.Notes.
		return CutoverPreflightResult{
			SourceVersionNum: r.SourceVersionNum,
			TargetVersionNum: r.TargetVersionNum,
			SourceInRecovery: r.SourceInRecovery,
			TargetInRecovery: r.TargetInRecovery,
			TargetDiskMB:     r.TargetDiskMB,
			ReplicatorOK:     r.ReplicatorOK,
			Notes:            r.Notes,
		}, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	return CutoverPreflightResult{
		SourceVersionNum: r.SourceVersionNum,
		TargetVersionNum: r.TargetVersionNum,
		SourceInRecovery: r.SourceInRecovery,
		TargetInRecovery: r.TargetInRecovery,
		TargetDiskMB:     r.TargetDiskMB,
		ReplicatorOK:     r.ReplicatorOK,
		Notes:            r.Notes,
	}, nil
}

// CutoverPromote promotes the target to primary for the engine named in
// config.DatabaseType. It is the ONLY data-plane write in the cutover; the
// orchestrator has already asserted the fence and verified the target role.
// Each engine's implementation checks the source/target role before AND after
// and freezes the source to close the dual-writer window.
func (e *ReplicationEngine) CutoverPromote(ctx context.Context, config ReplicationConfig) error {
	switch config.DatabaseType {
	case "postgres", "postgresql":
		return e.PromotePG(ctx, config)
	case "mysql", "mariadb":
		return e.promoteMySQLCutover(ctx, config)
	case "redis":
		return e.promoteRedisCutover(ctx, config)
	case "mongodb":
		return fmt.Errorf("%w: mongodb replication cutover is not supported (replica-set lag unverifiable)", ErrCutoverPreflight)
	default:
		return fmt.Errorf("unsupported database type for cutover: %s", config.DatabaseType)
	}
}

// ErrNoSourceFreeze is returned by CutoverFreezeSource for engines that cannot
// physically enforce a write-freeze on the source (Redis today). It is
// NOT fatal: the orchestrator records it as a degraded-but-continuable condition
// (manual freeze required) so it never silently claims RPO=0 for an engine whose
// source can still accept writes during the cutover window.
var ErrNoSourceFreeze = fmt.Errorf("source write-freeze not supported for engine")

// CutoverFreezeSource closes the dual-writer window by freezing writes on the
// SOURCE before the target is promoted. It is the physical complement to the
// lease (which serializes the cutover): the lease forbids proceeding without a
// held fence; the freeze physically stops app writes on the source. It runs
// behind the orchestrator's AssertHolds gate. Engines that cannot enforce a
// freeze return ErrNoSourceFreeze (honest degraded, not fatal). MongoDB has no
// cutover contract and fails closed.
func (e *ReplicationEngine) CutoverFreezeSource(ctx context.Context, config ReplicationConfig) error {
	switch config.DatabaseType {
	case "postgres", "postgresql":
		return e.freezePostgresSource(ctx, config)
	case "mysql", "mariadb":
		// Held read_only=ON + super_read_only=ON on the source. This is stronger
		// and more durable than the legacy FinalSync FLUSH TABLES WITH READ LOCK
		// (session-scoped, released on process exit — see replication.go B2 note),
		// so the fenced cutover path supersedes that fragile lock with a real
		// freeze. Non-SUPER connections are rejected on write; the lease-holder
		// (SUPER) can still drive the cutover.
		return e.freezeMySQLSource(ctx, config)
	case "redis":
		// No source freeze exists for Redis yet (B3). Report it honestly so the
		// orchestrator can downgrade to degraded rather than claim a closed
		// dual-writer window.
		return ErrNoSourceFreeze
	case "mongodb":
		return fmt.Errorf("%w: mongodb cutover not supported", ErrCutoverPreflight)
	default:
		return fmt.Errorf("unsupported engine for source freeze: %s", config.DatabaseType)
	}
}

// freezeMySQLSource freezes the source by setting read_only=ON and
// super_read_only=ON (held, survives the client session) and verifies it took
// effect. Non-SUPER users can no longer write, closing the dual-writer window
// for application traffic.
func (e *ReplicationEngine) freezeMySQLSource(ctx context.Context, config ReplicationConfig) error {
	if err := mysqlSetReadOnly(ctx, e.sourceSSH, true); err != nil {
		return fmt.Errorf("freeze source: set read_only: %w", err)
	}
	ro, ok, err := mysqlReadOnly(ctx, e.sourceSSH)
	if err != nil || !ok {
		return fmt.Errorf("freeze source: verify read_only: %v", err)
	}
	if !strings.EqualFold(ro, "ON") {
		return fmt.Errorf("freeze source: read_only=%q, expected ON", ro)
	}
	return nil
}

// freezePostgresSource sets default_transaction_read_only=on on the source via
// ALTER SYSTEM + reload, then verifies the GUC is on. This rejects writes from
// non-superuser app roles (the lease already serialized the cutover). Superusers
// bypass the GUC, which the live integration test documents explicitly.
func (e *ReplicationEngine) freezePostgresSource(ctx context.Context, config ReplicationConfig) error {
	set := `sudo -u postgres psql -c "ALTER SYSTEM SET default_transaction_read_only = on;" 2>&1`
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, set); err != nil {
		return fmt.Errorf("freeze source: set read_only: %w", err)
	}
	reload := `sudo -u postgres psql -c "SELECT pg_reload_conf();" 2>&1`
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, reload); err != nil {
		return fmt.Errorf("freeze source: reload conf: %w", err)
	}
	out, _, _, err := e.sourceSSH.ExecContext(ctx, `sudo -u postgres psql -tAc "SHOW default_transaction_read_only;" 2>&1`)
	if err != nil {
		return fmt.Errorf("freeze source: verify read_only: %w", err)
	}
	if strings.TrimSpace(out) != "on" {
		return fmt.Errorf("freeze source: default_transaction_read_only=%q, expected on", strings.TrimSpace(out))
	}
	return nil
}

// mysqlVersionProbe returns the source and target @@version numeric (major*10000
// + minor) for same-major comparison. A non-numeric/unprobeable side returns 0
// and a note; the caller decides whether 0 blocks (it does for cross-major).
func mysqlVersionProbePair(ctx context.Context, src, tgt SSHExecuter) (int, int, string) {
	srcN, srcNote := mysqlVersionNum(ctx, src)
	tgtN, tgtNote := mysqlVersionNum(ctx, tgt)
	note := ""
	if srcNote != "" {
		note = "source: " + srcNote
	}
	if tgtNote != "" {
		if note != "" {
			note += "; "
		}
		note += "target: " + tgtNote
	}
	return srcN, tgtN, note
}

// mysqlVersionNum runs `SELECT @@version` over SSH and extracts the numeric
// major version prefix (e.g. "8.0.36" → 8). Returns (major, ""), or (0, note)
// on probe failure. We compare major only (same-major requirement).
func mysqlVersionNum(ctx context.Context, ssh SSHExecuter) (int, string) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "mysql -NBe 'SELECT @@version;' 2>&1")
	if err != nil {
		return 0, err.Error()
	}
	if exit != 0 {
		return 0, fmt.Sprintf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	// "@@version" looks like "8.0.36" or "10.11.4-MariaDB". Take the leading
	// numeric major component before any non-digit/dot.
	v := strings.TrimSpace(out)
	major := 0
	seen := false
	for _, r := range v {
		if r >= '0' && r <= '9' {
			major = major*10 + int(r-'0')
			seen = true
		} else if r == '.' {
			if seen {
				break
			}
			continue
		} else {
			break
		}
	}
	if !seen {
		return 0, "unparseable version: " + v
	}
	return major, ""
}

// mysqlServerID returns @@server_id. 0 means unset (replication unsafe).
func mysqlServerID(ctx context.Context, ssh SSHExecuter) (int64, string) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "mysql -NBe 'SELECT @@server_id;' 2>&1")
	if err != nil {
		return 0, err.Error()
	}
	if exit != 0 {
		return 0, fmt.Sprintf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	id, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if perr != nil {
		return 0, "unparseable server_id: " + strings.TrimSpace(out)
	}
	return id, ""
}

// mysqlReplicaRunning reports whether the target is a running replica
// (Replica_IO_Running=Yes AND Replica_SQL_Running=Yes). MySQL 8.0.22+ uses
// "Replica_*"; older/legacy uses "Slave_*". Both forms are accepted.
func mysqlReplicaRunning(ctx context.Context, ssh SSHExecuter) (bool, bool, string, string) {
	// Use `-e` (NOT `-NBe`): under `-B` batch mode the `\G` vertical format
	// strips the `Replica_IO_Running: ` labels, leaving bare Yes/No values that
	// parseReplicaStatus cannot match — it would report the replica as not running
	// even though it is healthy. `-e` keeps the labels.
	out, _, _, err := ssh.ExecContext(ctx, "mysql -e 'SHOW REPLICA STATUS\\G' 2>&1")
	if err != nil {
		// Fall back to legacy Slave spelling.
		out, _, _, err = ssh.ExecContext(ctx, "mysql -e 'SHOW SLAVE STATUS\\G' 2>&1")
		if err != nil {
			return false, false, "", err.Error()
		}
	}
	ioRunning, sqlRunning, sourceHost := parseReplicaStatus(out)
	return ioRunning, sqlRunning, sourceHost, ""
}

// parseReplicaStatus extracts Replica_IO_Running / Replica_SQL_Running (or
// Slave_* legacy) and Source_Host (or Master_Host) from SHOW ... STATUS\G.
func parseReplicaStatus(out string) (ioRunning, sqlRunning bool, sourceHost string) {
	ioVal := ""
	sqlVal := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Replica_IO_Running:") || strings.HasPrefix(line, "Slave_IO_Running:"):
			ioVal = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Replica_IO_Running:"), "Slave_IO_Running:"))
		case strings.HasPrefix(line, "Replica_SQL_Running:") || strings.HasPrefix(line, "Slave_SQL_Running:"):
			sqlVal = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Replica_SQL_Running:"), "Slave_SQL_Running:"))
		case strings.HasPrefix(line, "Source_Host:") || strings.HasPrefix(line, "Master_Host:"):
			sourceHost = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "Source_Host:"), "Master_Host:"))
		}
	}
	ioRunning = strings.EqualFold(ioVal, "Yes")
	sqlRunning = strings.EqualFold(sqlVal, "Yes")
	return ioRunning, sqlRunning, sourceHost
}

// mysqlSetReadOnly sets the global read_only (and super_read_only) flag on the
// given host. Used to freeze the source after promote (close the dual-writer
// window) and to make the target writable on promote. A failure returns an
// error — callers must fail closed rather than assume the role changed.
func mysqlSetReadOnly(ctx context.Context, ssh SSHExecuter, readOnly bool) error {
	val := "OFF"
	if readOnly {
		val = "ON"
	}
	q := fmt.Sprintf("SET GLOBAL read_only=%s; SET GLOBAL super_read_only=%s;", val, val)
	// Shell-quote the -e argument; no user input is interpolated here.
	cmd := fmt.Sprintf("mysql -e %s 2>&1", shared.ShellQuote(q))
	if _, stderr, exit, err := ssh.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("set read_only=%s: %w", val, err)
	} else if exit != 0 {
		return fmt.Errorf("set read_only=%s: exit %d: %s", val, exit, strings.TrimSpace(stderr))
	}
	return nil
}
