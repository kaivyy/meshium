package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// pgPreflightConfig returns a ReplicationConfig for the PG preflight tests with
// a replicator user/pass that the connectivity mock recognizes.
func pgPreflightConfig() ReplicationConfig {
	return ReplicationConfig{
		DatabaseType:    "postgres",
		SourceHost:      "source",
		SourcePort:      5432,
		TargetHost:      "target",
		TargetPort:      5432,
		ReplicationUser: "replicator",
		ReplicationPass: "secret'pass", // contains a quote to prove ShellQuote is used
		MigrationID:     1,
	}
}

// pgVersionMock returns a mockSSH that answers SHOW server_version_num with v.
func pgVersionMock(v string) *mockSSH {
	m := newMockSSH()
	m.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = v
	return m
}

// setRecovery configures the pg_is_in_recovery() answer on a mock.
func setRecovery(m *mockSSH, inRecovery bool) {
	v := "f"
	if inRecovery {
		v = "t"
	}
	m.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = v
}

// setReplicatorOK configures the target mock to answer the replicator probe.
// The probe command is built with shared.ShellQuote on user/host, so we prefix-
// match on the stable leading fragment "PGPASSWORD=".
func setReplicatorOK(m *mockSSH, ok bool) {
	if ok {
		m.execOutput["PGPASSWORD="] = "1"
		return
	}
	m.execExit["PGPASSWORD="] = 2
	m.execOutput["PGPASSWORD="] = "psql: connection refused"
}

// TestPGPreflightSameMajorPass: a healthy same-major pair passes preflight. The
// replicator password contains a single quote to prove it is shell-quoted (an
// unquoted quote would break the command and the probe would not return "1").
func TestPGPreflightSameMajorPass(t *testing.T) {
	source := pgVersionMock("150003")
	setRecovery(source, false) // primary
	target := pgVersionMock("150001")
	setRecovery(target, true) // standby
	target.execOutput["df -m --output=avail"] = "8192" // best-effort disk
	setReplicatorOK(target, true)

	e := NewReplicationEngine(source, target, nil)
	r, err := e.Preflight(context.Background(), pgPreflightConfig())
	if err != nil {
		t.Fatalf("preflight: %v (notes=%q)", err, r.Notes)
	}
	if pgMajorVersion(r.SourceVersionNum) != pgMajorVersion(r.TargetVersionNum) {
		t.Fatalf("majors differ: src=%d tgt=%d", r.SourceVersionNum, r.TargetVersionNum)
	}
	if r.SourceInRecovery {
		t.Fatal("source reported in recovery")
	}
	if !r.TargetInRecovery {
		t.Fatal("target not reported in recovery")
	}
	if !r.ReplicatorOK {
		t.Fatalf("replicator not ok: %s", r.Notes)
	}
}

// TestPGPreflightCrossMajorBlock: cross-major is rejected pre-mutation.
func TestPGPreflightCrossMajorBlock(t *testing.T) {
	source := pgVersionMock("150003")
	setRecovery(source, false)
	target := pgVersionMock("160001") // major 16 vs 15
	setRecovery(target, true)
	setReplicatorOK(target, true)

	e := NewReplicationEngine(source, target, nil)
	_, err := e.Preflight(context.Background(), pgPreflightConfig())
	if !errors.Is(err, ErrPGPreflight) {
		t.Fatalf("cross-major: err=%v want ErrPGPreflight", err)
	}
	if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("cross-major msg missing: %v", err)
	}
}

// TestPGPreflightSourceInRecoveryBlock: a standby source is rejected.
func TestPGPreflightSourceInRecoveryBlock(t *testing.T) {
	source := pgVersionMock("150003")
	setRecovery(source, true) // source is a standby — not a primary
	target := pgVersionMock("150001")
	setRecovery(target, true)
	setReplicatorOK(target, true)

	e := NewReplicationEngine(source, target, nil)
	_, err := e.Preflight(context.Background(), pgPreflightConfig())
	if !errors.Is(err, ErrPGPreflight) {
		t.Fatalf("source-in-recovery: err=%v want ErrPGPreflight", err)
	}
	if !strings.Contains(err.Error(), "source is in recovery") {
		t.Fatalf("source-in-recovery msg missing: %v", err)
	}
}

// TestPGPreflightTargetNotStandbyBlock: a target that is already a primary is
// rejected — promoting an already-promoted target is not a cutover.
func TestPGPreflightTargetNotStandbyBlock(t *testing.T) {
	source := pgVersionMock("150003")
	setRecovery(source, false)
	target := pgVersionMock("150001")
	setRecovery(target, false) // target is a primary, NOT a standby
	setReplicatorOK(target, true)

	e := NewReplicationEngine(source, target, nil)
	_, err := e.Preflight(context.Background(), pgPreflightConfig())
	if !errors.Is(err, ErrPGPreflight) {
		t.Fatalf("target-not-standby: err=%v want ErrPGPreflight", err)
	}
	if !strings.Contains(err.Error(), "NOT in recovery") {
		t.Fatalf("target-not-standby msg missing: %v", err)
	}
}

// TestPGPreflightReplicatorFailBlock: replicator connectivity failure blocks
// preflight — the target cannot receive replication without it.
func TestPGPreflightReplicatorFailBlock(t *testing.T) {
	source := pgVersionMock("150003")
	setRecovery(source, false)
	target := pgVersionMock("150001")
	setRecovery(target, true)
	setReplicatorOK(target, false)

	e := NewReplicationEngine(source, target, nil)
	_, err := e.Preflight(context.Background(), pgPreflightConfig())
	if !errors.Is(err, ErrPGPreflight) {
		t.Fatalf("replicator-fail: err=%v want ErrPGPreflight", err)
	}
	if !strings.Contains(err.Error(), "replicator connectivity") {
		t.Fatalf("replicator msg missing: %v", err)
	}
}

// TestPGStatReplicationLagParses: a numeric replay_lag is parsed.
func TestPGStatReplicationLagParses(t *testing.T) {
	source := newMockSSH()
	source.execOutput["sudo -u postgres psql -tAc "] = "4"
	e := NewReplicationEngine(source, nil, nil)
	lag, err := pgStatReplicationLag(context.Background(), e.sourceSSH)
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	if lag != 4 {
		t.Fatalf("lag=%d want 4", lag)
	}
}

// TestPGStatReplicationLagNoStandbyFailsClosed: an empty result (no standby
// row) is an error, never 0 — 0 would let the orchestrator promote on
// un-replicated data.
func TestPGStatReplicationLagNoStandbyFailsClosed(t *testing.T) {
	source := newMockSSH()
	source.execOutput["sudo -u postgres psql -tAc "] = "" // no row
	e := NewReplicationEngine(source, nil, nil)
	lag, err := pgStatReplicationLag(context.Background(), e.sourceSSH)
	if err == nil {
		t.Fatalf("no-standby returned no error (lag=%d); must fail closed", lag)
	}
	if lag != -1 {
		t.Fatalf("no-standby lag=%d want -1", lag)
	}
}

// TestWaitForCatchUpPGReturnsNilWhenWithinThreshold: lag at/below the threshold
// returns nil without sleeping.
func TestWaitForCatchUpPGReturnsNilWhenWithinThreshold(t *testing.T) {
	source := newMockSSH()
	source.execOutput["sudo -u postgres psql -tAc "] = "0" // caught up
	e := NewReplicationEngine(source, nil, nil)
	// Shrink the poll so a bug that loops would surface quickly.
	PGCatchUpPoll = 5 * time.Millisecond
	defer func() { PGCatchUpPoll = 2 * time.Second }()
	if err := e.WaitForCatchUpPG(context.Background(), pgPreflightConfig(), 1); err != nil {
		t.Fatalf("catchup: %v", err)
	}
}

// TestWaitForCatchUpPGFailsOnNoStandby: a lag probe error (no standby) is
// returned immediately, not swallowed as caught-up.
func TestWaitForCatchUpPGFailsOnNoStandby(t *testing.T) {
	source := newMockSSH()
	source.execOutput["sudo -u postgres psql -tAc "] = "" // no standby row
	e := NewReplicationEngine(source, nil, nil)
	if err := e.WaitForCatchUpPG(context.Background(), pgPreflightConfig(), 1); err == nil {
		t.Fatal("catchup returned no error with no standby; must fail closed")
	}
}

// TestPromotePGSQLSuccess: pg_promote() returns "t" and the post-promote probe
// shows the target out of recovery → nil error. No shell fallback is reached.
func TestPromotePGSQLSuccess(t *testing.T) {
	target := newMockSSH()
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_promote();' 2>&1"] = "t"
	// Post-promote probe: NOT in recovery (primary now).
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"

	e := NewReplicationEngine(nil, target, nil)
	if err := e.PromotePG(context.Background(), pgPreflightConfig()); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The shell fallback command must NOT have run.
	for _, c := range target.commands {
		if strings.Contains(c, "pg_ctl promote") {
			t.Fatalf("shell fallback ran: %s", c)
		}
	}
}

// TestPromotePGShellFallback: when the SQL path fails, the shell pg_ctl promote
// fallback is used and the post-promote probe confirms ownership.
func TestPromotePGShellFallback(t *testing.T) {
	target := newMockSSH()
	// SQL path fails (nonzero exit, non-"t" output).
	target.execExit["sudo -u postgres psql -tAc 'SELECT pg_promote();' 2>&1"] = 1
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_promote();' 2>&1"] = ""
	// Shell fallback succeeds (exit 0 by default).
	target.execOutput["sudo -u postgres pg_ctl promote -D /var/lib/postgresql/*/main 2>&1"] = "server promoted"
	// Post-promote probe: out of recovery.
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"

	e := NewReplicationEngine(nil, target, nil)
	if err := e.PromotePG(context.Background(), pgPreflightConfig()); err != nil {
		t.Fatalf("promote shell fallback: %v", err)
	}
}

// TestPromotePGPostProbeFailsClosed: pg_promote() returns "t" but the target is
// STILL in recovery afterward → promotion did not take effect → error (not
// assumed-success from the command exit code alone).
func TestPromotePGPostProbeFailsClosed(t *testing.T) {
	target := newMockSSH()
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_promote();' 2>&1"] = "t"
	// Post-promote probe: STILL in recovery — promote did not take effect.
	target.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "t"

	e := NewReplicationEngine(nil, target, nil)
	err := e.PromotePG(context.Background(), pgPreflightConfig())
	if err == nil {
		t.Fatal("promote returned no error while target still in recovery; must fail closed")
	}
	if !strings.Contains(err.Error(), "still in recovery") {
		t.Fatalf("post-probe msg missing: %v", err)
	}
}
