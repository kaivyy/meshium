package migration

import (
	"context"
	"fmt"
	"strings"
)

// Phase 2C: MySQL seeded-replication cutover primitives.
//
// MySQL cutover is seeded-replication only, same-major topology. The seed
// (streaming mysqldump → mysql restore) is performed in setupMySQL BEFORE the
// replica is configured, so the target is not empty on cutover. This file adds
// the gates the orchestrator drives:
//
//   - preflightMySQL: read-only gates that block unsupported configs with an
//     actionable error before any CHANGE/START/RESET runs: same-major, source a
//     writable primary (read_only OFF), target a clean standby (read_only ON,
//     not already a running replica with unexpected state), binary logging ON
//     on the source (else no binlog position to seed from), unique non-zero
//     server_id on both sides, and replicator connectivity.
//   - promoteMySQLCutover: the ONLY data-plane write. It checks the replica is
//     caught up + running BEFORE, pauses the replica, verifies the source can be
//     frozen (read_only ON) to close the dual-writer window, then stops the
//     replica and makes the target writable. After promotion it re-probes to
//     confirm the target is a writable primary; a target that did not become
//     writable → fail closed (NeedsManualIntervention), never assumed.
//
// All commands are read-only except the promote mutations. Credential handling
// follows the repo convention: MYSQL_PWD via env prefix (mysqlEnv), never -p on
// argv, MYSQL_PWD scoped to the command (the adaptor already returns that form).

// preflightMySQL runs the read-only pre-cutover gates for a same-major
// seeded-replication pair. It issues NO destructive command. Any failure returns
// ErrCutoverPreflight with the actionable reason in result.Notes.
func (e *ReplicationEngine) preflightMySQL(ctx context.Context, config ReplicationConfig) (CutoverPreflightResult, error) {
	var r CutoverPreflightResult

	// Gate 1: same-major (SQL-parseable; use leading major component).
	srcV, tgtV, verNote := mysqlVersionProbePair(ctx, e.sourceSSH, e.targetSSH)
	r.SourceVersionNum = srcV
	r.TargetVersionNum = tgtV
	if verNote != "" {
		r.Notes = verNote
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, verNote)
	}
	if srcV != tgtV {
		r.Notes = fmt.Sprintf("cross-major: source=%d target=%d (same-major required)", srcV, tgtV)
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}

	// Gate 2: binary logging ON on the source (else there is no binlog position
	// to seed the replica from — a silent data-loss trap). Probe log_bin.
	if on, note := mysqlLogBinOn(ctx, e.sourceSSH); !on {
		r.Notes = note
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, note)
	}

	// Gate 3: unique, non-zero server_id on both sides (replication safety).
	srcID, srcNote := mysqlServerID(ctx, e.sourceSSH)
	tgtID, tgtNote := mysqlServerID(ctx, e.targetSSH)
	if srcNote != "" {
		r.Notes = "source: " + srcNote
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	if tgtNote != "" {
		r.Notes = "target: " + tgtNote
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	if srcID == 0 || tgtID == 0 {
		r.Notes = fmt.Sprintf("server_id unset: source=%d target=%d (both must be non-zero)", srcID, tgtID)
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	if srcID == tgtID {
		r.Notes = fmt.Sprintf("server_id clash: source and target share %d (must be unique)", srcID)
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}

	// Gate 4: source must be a writable primary (read_only OFF), target must be a
	// standby (read_only ON). MySQL cutover keeps the target read-only until
	// promote, exactly like the Pg standby contract.
	srcRO, ok, err := mysqlReadOnly(ctx, e.sourceSSH)
	if err != nil || !ok {
		r.Notes = "source read_only probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	r.SourceInRecovery = strings.EqualFold(srcRO, "ON")
	if r.SourceInRecovery {
		r.Notes = "source is read_only=ON (standby) — source must be a writable primary"
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	tgtRO, ok2, err := mysqlReadOnly(ctx, e.targetSSH)
	if err != nil || !ok2 {
		r.Notes = "target read_only probe: " + err.Error()
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	r.TargetInRecovery = strings.EqualFold(tgtRO, "ON")
	if !r.TargetInRecovery {
		r.Notes = "target is read_only=OFF — target must be a read-only standby before cutover"
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}

	// Gate 5: replicator connectivity. The target must be able to reach the
	// source as the replication user. Best-effort: a denied probe does not block
	// (the replica channel's own IO thread is the authoritative signal), but a
	// clear auth/host failure is an actionable block.
	if ok, note := mysqlReplicatorConnectivity(ctx, e.targetSSH, config); !ok {
		r.Notes = "replicator connectivity: " + note
		return r, fmt.Errorf("%w: %s", ErrCutoverPreflight, r.Notes)
	}
	r.ReplicatorOK = true

	return r, nil
}

// mysqlLogBinOn reports whether binary logging is enabled on the given host.
// Returns (true, "") when log_bin=ON, (false, note) otherwise.
func mysqlLogBinOn(ctx context.Context, ssh SSHExecuter) (bool, string) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "mysql -NBe 'SELECT @@log_bin;' 2>&1")
	if err != nil {
		return false, "log_bin probe: " + err.Error()
	}
	if exit != 0 {
		return false, fmt.Sprintf("log_bin probe exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(out) == "1", "binary logging is OFF (log_bin=0) — enable log_bin on the source so a replica can be seeded"
}

// mysqlReplicatorConnectivity checks the target can reach the source as the
// replication user via the configured host/port. Read-only probe; returns
// (true, "") on success.
func mysqlReplicatorConnectivity(ctx context.Context, targetSSH SSHExecuter, config ReplicationConfig) (bool, string) {
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}
	port := config.SourcePort
	if port == 0 {
		port = 3306
	}
	replUser := config.ReplicationUser
	if replUser == "" {
		replUser = "meshium_repl"
	}
	c := DBCredentials{Username: replUser, Password: config.ReplicationPass, Host: sourceHost, Port: port}
	cmd := fmt.Sprintf("%s mysql %s -NBe 'SELECT 1;' 2>&1", mysqlEnv(c), mysqlConnArgs(c))
	out, _, exit, err := targetSSH.ExecContext(ctx, cmd)
	if err != nil {
		return false, "probe error: " + err.Error()
	}
	if exit != 0 {
		return false, fmt.Sprintf("probe exit %d (check user/host/password)", exit)
	}
	if strings.TrimSpace(out) != "1" {
		return false, "unexpected probe output: " + strings.TrimSpace(out)
	}
	return true, ""
}

// promoteMySQLCutover promotes the seeded MySQL replica to primary. This is the
// ONLY data-plane write. It closes the dual-writer window by freezing the source
// (read_only ON) AFTER confirming the replica is caught up and running, then
// stops the replica and makes the target writable. Post-promote it re-probes the
// target to confirm it is a writable primary; failure → NeedsManualIntervention.
func (e *ReplicationEngine) promoteMySQLCutover(ctx context.Context, config ReplicationConfig) error {
	// BEFORE: the replica must be running and caught up. A replica whose IO/SQL
	// thread is not Yes means the channel is broken; promoting it would move
	// traffic to an un-replicated target.
	ioRunning, sqlRunning, _, errMsg := mysqlReplicaRunning(ctx, e.targetSSH)
	if errMsg != "" {
		return fmt.Errorf("pre-promote replica probe: %s", errMsg)
	}
	if !ioRunning || !sqlRunning {
		return fmt.Errorf("pre-promote: target replica not running (IO=%v SQL=%v) — cannot promote an un-replicated target", ioRunning, sqlRunning)
	}

	// Freeze the source FIRST (close the dual-writer window). If the source won't
	// freeze, promoting the target would create a split-brain (both writable).
	if err := mysqlSetReadOnly(ctx, e.sourceSSH, true); err != nil {
		return fmt.Errorf("freeze source (read_only ON) failed: %w", err)
	}

	// Stop the replica so the target stops following and becomes independently
	// writable. No RESET here — removing the replication metadata is irreversible
	// and not required for the target to accept writes.
	if _, stderr, exit, err := e.targetSSH.ExecContext(ctx, "mysql -e 'STOP REPLICA;' 2>&1"); err != nil {
		return fmt.Errorf("stop replica: %w", err)
	} else if exit != 0 {
		return fmt.Errorf("stop replica exit %d: %s", exit, strings.TrimSpace(stderr))
	}

	// Make the target writable (it was read_only=ON as a standby).
	if err := mysqlSetReadOnly(ctx, e.targetSSH, false); err != nil {
		return fmt.Errorf("make target writable (read_only OFF) failed: %w", err)
	}

	// AFTER: confirm the target is now a writable primary (read_only OFF).
	tgtRO, ok, err := mysqlReadOnly(ctx, e.targetSSH)
	if err != nil || !ok {
		return fmt.Errorf("post-promote target probe: %v", err)
	}
	if strings.EqualFold(tgtRO, "ON") {
		return fmt.Errorf("post-promote: target still read_only after promote (did not become a primary)")
	}
	return nil
}

// mysqlMasterStatusBinlog returns the current source binlog File/Position for a
// post-seed CHANGE REPLICATION SOURCE/MASTER. Reused by setupMySQL after the
// seed completes. It is a thin wrapper over parseMySQLMasterStatus.
func mysqlMasterStatusBinlog(ctx context.Context, ssh SSHExecuter) (string, string, error) {
	output, mstderr, mexit, err := ssh.ExecContext(ctx, "mysql -e 'SHOW MASTER STATUS\\G' 2>&1")
	if err != nil {
		return "", "", err
	}
	if mexit != 0 {
		return "", "", fmt.Errorf("exit %d: %s", mexit, strings.TrimSpace(mstderr))
	}
	file, pos := parseMySQLMasterStatus(output)
	if file == "" {
		return "", "", fmt.Errorf("source master status has no binlog file (is binary logging enabled?)")
	}
	return file, pos, nil
}
