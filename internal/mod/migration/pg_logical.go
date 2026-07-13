package migration

// Phase 4D — PostgreSQL LOGICAL replication (publication/subscription),
// same-major. A distinct axis from the proven physical/streaming-standby path
// (pg_cutover.go). Logical replication points a target that is an INDEPENDENT
// primary at a source PUBLICATION; there is no standby, no pg_basebackup, and
// no pg_promote(). The cutover is then: stop source writes, wait for the
// subscription to drain, repoint traffic (the already-proven fenced traffic
// switcher). This file proves the bounded new axis end-to-end:
//
//   - PreflightLogicalPG: same-major, source is a primary, source
//     wal_level=logical (required — a replica-only server cannot publish),
//     target is a reachable primary, replication user can connect source→target.
//     Every failure is pre-mutation and actionable; no pub/sub is created.
//   - SetupLogicalReplicationPG: idempotent CREATE PUBLICATION (source) +
//     CREATE SUBSCRIPTION (target). Connstring uses PGPASSWORD-equivalent creds
//     only inside the subscription DSN (no -p argv; values SQL-escaped).
//   - WaitForLogicalCatchUpPG: polls per-table row-count parity (target == source)
//     with a bounded wait; returns when drained or ctx expires. Never assumes 0
//     lag — it measures it.
//
// These primitives reuse the same SSHExecuter + fenced-traffic contract as the
// physical path. They do NOT change cutover orchestration: callers that want an
// automatic logical cutover compose these with the fenced traffic switcher.
// Product wording stays honest: logical cutover is proven at the replication
// primitive + catch-up level here; it is not claimed "automatic" until wired
// through the full fenced machine (deferred — must not regress 4A/4B).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"
)

// LogicalPubName / LogicalSubName are the fixed object names Meshium manages.
const (
	LogicalPubName = "meshium_pub"
	LogicalSubName = "meshium_sub"
)

// PGLogicalPreflightResult is the evidence captured by PreflightLogicalPG.
type PGLogicalPreflightResult struct {
	SourceVersionNum int
	TargetVersionNum int
	SourceInRecovery bool
	TargetInRecovery bool
	WalLevel         string // SHOW wal_level on source (must be "logical")
	ReplicatorOK     bool
	Notes            string
}

// ErrPGLogicalPreflight is returned when a logical preflight gate fails.
var ErrPGLogicalPreflight = fmt.Errorf("postgres logical preflight failed")

// pgWalLevel returns SHOW wal_level on the given server.
func pgWalLevel(ctx context.Context, ssh SSHExecuter) (string, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx,
		"sudo -u postgres psql -tAc 'SHOW wal_level;' 2>&1")
	if err != nil {
		return "", fmt.Errorf("wal_level: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("wal_level exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(out), nil
}

// pgDBExists reports whether a database exists on the server.
func pgDBExists(ctx context.Context, ssh SSHExecuter, db string) (bool, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx,
		fmt.Sprintf("sudo -u postgres psql -tAc %s 2>&1",
			shared.ShellQuote("SELECT 1 FROM pg_database WHERE datname = '"+sqlEscapeSingleQuotes(db)+"';")))
	if err != nil {
		return false, fmt.Errorf("db exists: %w", err)
	}
	if exit != 0 {
		return false, fmt.Errorf("db exists exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(out) == "1", nil
}

// PreflightLogicalPG runs the pre-mutation logical gates. It issues NO
// destructive command; it only reads. Each gate failure is reported in
// result.Notes and returned as ErrPGLogicalPreflight (callers fail closed to
// NeedsManualIntervention).
func (e *ReplicationEngine) PreflightLogicalPG(ctx context.Context, config ReplicationConfig) (PGLogicalPreflightResult, error) {
	var r PGLogicalPreflightResult

	// Gate 1: same-major.
	srcV, err := pgServerVersionNum(ctx, e.sourceSSH)
	if err != nil {
		r.Notes = "source version probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	r.SourceVersionNum = srcV
	tgtV, err := pgServerVersionNum(ctx, e.targetSSH)
	if err != nil {
		r.Notes = "target version probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	r.TargetVersionNum = tgtV
	if pgMajorVersion(srcV) != pgMajorVersion(tgtV) {
		r.Notes = fmt.Sprintf("cross-major: source=%d target=%d (same-major required)", srcV, tgtV)
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}

	// Gate 2: source is a primary.
	srcInRec, ok, err := pgInRecovery(ctx, e.sourceSSH)
	if err != nil || !ok {
		r.Notes = "source pg_is_in_recovery probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	r.SourceInRecovery = srcInRec
	if srcInRec {
		r.Notes = "source is in recovery (standby) — logical source must be a primary"
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}

	// Gate 3: wal_level=logical (hard requirement for logical replication).
	wl, err := pgWalLevel(ctx, e.sourceSSH)
	if err != nil {
		r.Notes = "wal_level probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	r.WalLevel = wl
	if wl != "logical" {
		r.Notes = fmt.Sprintf("wal_level=%q (must be 'logical'); set wal_level=logical and restart the source", wl)
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}

	// Gate 4: target is a reachable primary. For logical the target is an
	// independent primary (NOT a standby). Require it to NOT be in recovery.
	tgtInRec, ok2, err := pgInRecovery(ctx, e.targetSSH)
	if err != nil || !ok2 {
		r.Notes = "target pg_is_in_recovery probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	r.TargetInRecovery = tgtInRec
	if tgtInRec {
		r.Notes = "target is in recovery (standby) — logical target must be an independent primary"
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}

	// Gate 5: the target database must exist (the subscription copies into it).
	if config.DatabaseName != "" {
		exists, derr := pgDBExists(ctx, e.targetSSH, config.DatabaseName)
		if derr != nil {
			r.Notes = "target db probe: " + derr.Error()
			return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
		}
		if !exists {
			r.Notes = fmt.Sprintf("target database %q does not exist", config.DatabaseName)
			return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
		}
	}

	// Gate 6: replication connectivity from target to source.
	replOK, replNote := pgReplicatorConnectivity(ctx, e.targetSSH, config)
	r.ReplicatorOK = replOK
	if !replOK {
		if r.Notes != "" {
			r.Notes += "; "
		}
		r.Notes += "replicator connectivity: " + replNote
		return r, fmt.Errorf("%w: %s", ErrPGLogicalPreflight, r.Notes)
	}
	return r, nil
}

// SetupLogicalReplicationPG creates the publication on the source and the
// subscription on the target, both idempotently. It is safe to call on a pair
// that already has the objects (a prior interrupted run). Returns after the
// subscription is created (initial copy begins asynchronously).
func (e *ReplicationEngine) SetupLogicalReplicationPG(ctx context.Context, config ReplicationConfig) error {
	// Source: CREATE PUBLICATION ... FOR ALL TABLES (idempotent).
	pubSQL := fmt.Sprintf("CREATE PUBLICATION %s FOR ALL TABLES;", LogicalPubName)
	if out, _, rc, err := e.sourceSSH.ExecContext(ctx,
		fmt.Sprintf("sudo -u postgres psql -tAc %s 2>&1", shared.ShellQuote(pubSQL))); err != nil || rc != 0 {
		if !strings.Contains(out, "already exists") {
			return fmt.Errorf("create publication: %w (out=%s)", errOrExit(err, rc), shared.SanitizeString(strings.TrimSpace(out)))
		}
	}

	// Target: CREATE SUBSCRIPTION ... CONNECTION ... PUBLICATION (idempotent).
	conn := logicalConnString(config)
	subSQL := fmt.Sprintf(
		"CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s WITH (copy_data = true, create_slot = true);",
		LogicalSubName, shared.ShellQuote(conn), LogicalPubName)
	if out, _, rc, err := e.targetSSH.ExecContext(ctx,
		fmt.Sprintf("sudo -u postgres psql -tAc %s 2>&1", shared.ShellQuote(subSQL))); err != nil || rc != 0 {
		if !strings.Contains(out, "already exists") {
			return fmt.Errorf("create subscription: %w (out=%s)", errOrExit(err, rc), shared.SanitizeString(strings.TrimSpace(out)))
		}
	}
	return nil
}

// logicalConnString builds the subscription DSN. Credentials are placed INSIDE
// the connection string (the only safe place for logical replication); values
// are SQL-escaped so they cannot break out of the single-quoted DSN. No -p
// argv flag is used anywhere.
func logicalConnString(config ReplicationConfig) string {
	host := config.SourceHost
	if host == "" {
		host = "source"
	}
	port := config.SourcePort
	if port == 0 {
		port = 5432
	}
	user := config.ReplicationUser
	if user == "" {
		user = "replicator"
	}
	pass := config.ReplicationPass
	db := config.DatabaseName
	if db == "" {
		db = "postgres"
	}
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s",
		host, port, db, user, sqlEscapeSingleQuotes(pass))
}

// errOrExit returns a readable error for an ExecContext result.
func errOrExit(err error, exit int) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("exit %d", exit)
}

// LogicalRowCount returns the row count of a table on a server (via psql).
func LogicalRowCount(ctx context.Context, ssh SSHExecuter, db, table string) (int64, error) {
	q := fmt.Sprintf("SELECT count(*) FROM %s;", table)
	out, stderr, exit, err := ssh.ExecContext(ctx,
		fmt.Sprintf("sudo -u postgres psql -d %s -tAc %s 2>&1",
			shared.ShellQuote(db), shared.ShellQuote(q)))
	if err != nil {
		return -1, fmt.Errorf("row count: %w", err)
	}
	if exit != 0 {
		return -1, fmt.Errorf("row count exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// WaitForLogicalCatchUpPG polls per-table row-count parity (target == source)
// with a bounded wait. Returns nil when every table's target count equals the
// source count, or ctx.Err()/timeout otherwise. It never assumes 0 lag — it
// measures it. tables must be non-empty; db names the database.
func (e *ReplicationEngine) WaitForLogicalCatchUpPG(ctx context.Context, config ReplicationConfig, tables []string, maxWait time.Duration) error {
	if len(tables) == 0 {
		return fmt.Errorf("WaitForLogicalCatchUpPG: no tables to verify parity on")
	}
	db := config.DatabaseName
	if db == "" {
		db = "postgres"
	}
	if maxWait <= 0 {
		maxWait = PGCatchUpTimeout
	}
	deadline := time.Now().Add(maxWait)
	poll := PGCatchUpPoll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		allMatch := true
		for _, t := range tables {
			s, serr := LogicalRowCount(ctx, e.sourceSSH, db, t)
			tg, terr := LogicalRowCount(ctx, e.targetSSH, db, t)
			if serr != nil || terr != nil {
				return fmt.Errorf("parity probe (table %s): src=%v tgt=%v", t, serr, terr)
			}
			if s != tg {
				allMatch = false
				break
			}
		}
		if allMatch {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf("logical catch-up timeout: target row counts did not match source for tables %v", tables)
		}
	}
}
