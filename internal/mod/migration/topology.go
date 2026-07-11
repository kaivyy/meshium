package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsafeTopology is returned when a replication rollback cannot proceed
// safely: the source/target topology is ambiguous, unreachable, or in an
// unexpected role. Callers must fail closed to StateNeedsManualIntervention —
// no destructive rollback command may be issued.
var ErrUnsafeTopology = errors.New("unsafe replication topology for rollback")

// ErrAwaitingCutover is a sentinel returned by trafficSwitchStage when a
// manual_required cutover checkpoint has been recorded. The pipeline execute
// loop treats it as a clean stop: it transitions the migration to
// StateAwaitingCutover (persisted) and returns — it does NOT advance to
// Observing/Committed/Completed. Only an explicit operator commit may leave
// AwaitingCutover.
var ErrAwaitingCutover = errors.New("awaiting manual cutover")

// ErrCutoverNotConfirmed is returned by the commit endpoint when a migration in
// StateAwaitingCutover is committed before the operator has confirmed traffic
// moved to the target (switch_state still manual_required). No transition
// occurs; the caller surfaces a structured 409.
var ErrCutoverNotConfirmed = errors.New("cutover_not_confirmed")

// rollbackTerminalState decides the terminal state for a rollback run from its
// aggregate error. Any step failure → RollbackDegraded, never RolledBack. A
// failure that wraps ErrUnsafeTopology (ambiguous/unreachable topology) →
// NeedsManualIntervention. A nil error (every step succeeded) → RolledBack.
// This is the single decision point the pipeline consults, so partial-failure
// can never be misreported as a clean rollback.
func rollbackTerminalState(err error) MigrationState {
	if err == nil {
		return StateRolledBack
	}
	if errors.Is(err, ErrUnsafeTopology) {
		return StateNeedsManualIntervention
	}
	return StateRollbackDegraded
}

// topologyKind labels the resolved role of one side of a replication pair.
type topologyKind int

const (
	topoUnknown topologyKind = iota
	topoPrimary              // writable, not a replica
	topoReplica              // read-only / following a primary
)

// topologyProbe captures the resolved roles of source and target.
type topologyProbe struct {
	source     topologyKind
	target     topologyKind
	sourceUp   bool
	targetUp   bool
	detail     string
}

// safeForRepoint reports whether it is safe to re-point the target back to the
// source as a replica (the only rollback mutation permitted this pass, and only
// for Redis). Source must be a reachable primary, target a reachable replica.
// Anything else — including both writable — fails closed.
func (t topologyProbe) safeForRepoint() bool {
	return t.sourceUp && t.targetUp && t.source == topoPrimary && t.target == topoReplica
}

func (t topologyProbe) reason() string {
	if !t.sourceUp {
		return "source unreachable"
	}
	if !t.targetUp {
		return "target unreachable"
	}
	if t.source == topoReplica && t.target == topoPrimary {
		return "roles reversed: source is replica, target is primary"
	}
	if t.source == topoPrimary && t.target == topoPrimary {
		return "both writable: split-brain risk"
	}
	if t.source == topoUnknown || t.target == topoUnknown {
		return "unknown/unexpected topology"
	}
	return "topology does not permit safe rollback re-point"
}

// probeTopology resolves the replication roles of source and target for the
// given engine, WITHOUT issuing any mutating command. Each probe is independent;
// a failure on either side yields ErrUnsafeTopology. The probe commands are
// read-only (SHOW VARIABLES / pg_is_in_recovery / INFO replication).
func (e *ReplicationEngine) probeTopology(ctx context.Context, dbType string) (topologyProbe, error) {
	switch dbType {
	case "mysql", "mariadb":
		return e.probeMySQL(ctx)
	case "postgres", "postgresql":
		return e.probePostgres(ctx)
	case "redis":
		return e.probeRedis(ctx)
	case "mongodb":
		// MongoDB replica-set rollback is not implemented this pass (see
		// setupMongoDB's fail-closed note). Any rollback attempt fails closed.
		return topologyProbe{detail: "mongodb rollback not supported this pass"}, ErrUnsafeTopology
	default:
		return topologyProbe{detail: "unsupported engine"}, ErrUnsafeTopology
	}
}

// probeMySQL: source must be read_only=OFF and not a replica; target must be a
// replica (SHOW REPLICA STATUS / SHOW SLAVE STATUS). read-only target with a
// running receiver = replica.
func (e *ReplicationEngine) probeMySQL(ctx context.Context) (topologyProbe, error) {
	t := topologyProbe{}
	srcRO, ok, err := mysqlReadOnly(ctx, e.sourceSSH)
	if err != nil {
		t.sourceUp = false
		return t, fmt.Errorf("%w: source read_only probe: %v", ErrUnsafeTopology, err)
	}
	t.sourceUp = ok
	t.source = mysqlRoleFromReadOnly(srcRO) // OFF → primary, ON → replica-ish

	tgtRO, ok2, err := mysqlReadOnly(ctx, e.targetSSH)
	if err != nil {
		t.targetUp = false
		return t, fmt.Errorf("%w: target read_only probe: %v", ErrUnsafeTopology, err)
	}
	t.targetUp = ok2
	t.target = mysqlRoleFromReadOnly(tgtRO)
	return t, nil
}

func mysqlRoleFromReadOnly(ro string) topologyKind {
	switch strings.ToLower(strings.TrimSpace(ro)) {
	case "off", "":
		return topoPrimary
	case "on":
		return topoReplica
	default:
		return topoUnknown
	}
}

// mysqlReadOnly runs `SHOW VARIABLES LIKE 'read_only'` and returns the value.
// The second return is false when the probe could not reach the server.
func mysqlReadOnly(ctx context.Context, ssh SSHExecuter) (string, bool, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "mysql -NBe \"SHOW VARIABLES LIKE 'read_only'\" 2>&1")
	if err != nil {
		return "", false, err
	}
	if exit != 0 {
		return "", false, fmt.Errorf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	// Output: "read_only\tOFF" — take the second field.
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 {
		return "", true, fmt.Errorf("unexpected read_only output: %q", out)
	}
	return fields[1], true, nil
}

// probePostgres: target must be in recovery (replica); source must not be.
func (e *ReplicationEngine) probePostgres(ctx context.Context) (topologyProbe, error) {
	t := topologyProbe{}
	srcInRecovery, ok, err := pgInRecovery(ctx, e.sourceSSH)
	if err != nil {
		t.sourceUp = false
		return t, fmt.Errorf("%w: source pg_is_in_recovery probe: %v", ErrUnsafeTopology, err)
	}
	t.sourceUp = ok
	if srcInRecovery {
		t.source = topoReplica
	} else {
		t.source = topoPrimary
	}

	tgtInRecovery, ok2, err := pgInRecovery(ctx, e.targetSSH)
	if err != nil {
		t.targetUp = false
		return t, fmt.Errorf("%w: target pg_is_in_recovery probe: %v", ErrUnsafeTopology, err)
	}
	t.targetUp = ok2
	if tgtInRecovery {
		t.target = topoReplica
	} else {
		t.target = topoPrimary
	}
	return t, nil
}

func pgInRecovery(ctx context.Context, ssh SSHExecuter) (bool, bool, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1")
	if err != nil {
		return false, false, err
	}
	if exit != 0 {
		return false, false, fmt.Errorf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	v := strings.TrimSpace(out)
	return v == "t", true, nil
}

// probeRedis: source role:master, target role:slave/replica.
func (e *ReplicationEngine) probeRedis(ctx context.Context) (topologyProbe, error) {
	t := topologyProbe{}
	srcRole, ok, err := redisRole(ctx, e.sourceSSH)
	if err != nil {
		t.sourceUp = false
		return t, fmt.Errorf("%w: source redis role probe: %v", ErrUnsafeTopology, err)
	}
	t.sourceUp = ok
	t.source = redisRoleKind(srcRole)

	tgtRole, ok2, err := redisRole(ctx, e.targetSSH)
	if err != nil {
		t.targetUp = false
		return t, fmt.Errorf("%w: target redis role probe: %v", ErrUnsafeTopology, err)
	}
	t.targetUp = ok2
	t.target = redisRoleKind(tgtRole)
	return t, nil
}

func redisRole(ctx context.Context, ssh SSHExecuter) (string, bool, error) {
	out, stderr, exit, err := ssh.ExecContext(ctx, "redis-cli INFO replication 2>&1")
	if err != nil {
		return "", false, err
	}
	if exit != 0 {
		return "", false, fmt.Errorf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "role:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "role:")), true, nil
		}
	}
	return "", true, fmt.Errorf("no role: line in INFO replication")
}

func redisRoleKind(role string) topologyKind {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "master":
		return topoPrimary
	case "slave", "replica":
		return topoReplica
	default:
		return topoUnknown
	}
}
