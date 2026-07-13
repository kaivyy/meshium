package migration

import (
	"context"
	"testing"
)

// Phase 2C-3/4: MySQL seeded-replication cutover primitives.
//
// These test the fail-closed gates: preflightMySQL must reject
// cross-major / log_bin-off / shared-server_id / source-read_only / target-not-read_only
// configs and accept a same-major seeded pair; promoteMySQLCutover must freeze the
// source (read_only ON) BEFORE stopping the replica and making the target writable,
// and fail closed if the target does not become writable.

// mysqlPreflightMocks wires two mocks (source, target) with the happy-path
// responses a same-major (8.x), log_bin-on, unique-server_id, source-primary /
// target-standby, replicator-connected pair produces.
func mysqlPreflightMocks() (src, tgt *mockSSH) {
	src = newMockSSH()
	tgt = newMockSSH()

	src.execOutput[`mysql -NBe 'SELECT @@version;' 2>&1`] = "8.0.36"
	src.execOutput[`mysql -NBe 'SELECT @@log_bin;' 2>&1`] = "1"
	src.execOutput[`mysql -NBe 'SELECT @@server_id;' 2>&1`] = "100"
	src.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tOFF"

	tgt.execOutput[`mysql -NBe 'SELECT @@version;' 2>&1`] = "8.0.36"
	tgt.execOutput[`mysql -NBe 'SELECT @@server_id;' 2>&1`] = "200"
	tgt.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tON"
	// Replicator connectivity: target probes source as the repl user.
	tgt.execOutput[`MYSQL_PWD='replpass' mysql -u 'meshium_repl' -h 'source' -P 3306 -NBe 'SELECT 1;' 2>&1`] = "1"
	return src, tgt
}

func mysqlReplConfig() ReplicationConfig {
	return ReplicationConfig{
		DatabaseType:   "mysql",
		SourceHost:     "source",
		SourcePort:     3306,
		ReplicationUser: "meshium_repl",
		ReplicationPass: "replpass",
	}
}

func TestPreflightMySQLHappyPath(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err != nil {
		t.Fatalf("preflight: %v (notes=%q)", err, r.Notes)
	}
	if r.SourceVersionNum != 8 || r.TargetVersionNum != 8 {
		t.Fatalf("version mismatch: src=%d tgt=%d", r.SourceVersionNum, r.TargetVersionNum)
	}
	if r.SourceInRecovery {
		t.Fatal("source must be a writable primary (read_only OFF)")
	}
	if !r.TargetInRecovery {
		t.Fatal("target must be a read-only standby")
	}
	if !r.ReplicatorOK {
		t.Fatal("replicator connectivity should pass")
	}
}

func TestPreflightMySQLCrossMajor(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	tgt.execOutput[`mysql -NBe 'SELECT @@version;' 2>&1`] = "5.7.40"
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err == nil {
		t.Fatal("cross-major must fail preflight")
	}
	if r.SourceVersionNum != 8 || r.TargetVersionNum != 5 {
		t.Fatalf("version parse: src=%d tgt=%d", r.SourceVersionNum, r.TargetVersionNum)
	}
}

func TestPreflightMySQLLogBinOff(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	src.execOutput[`mysql -NBe 'SELECT @@log_bin;' 2>&1`] = "0"
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err == nil || r.Notes == "" {
		t.Fatal("log_bin off must fail preflight with a note")
	}
}

func TestPreflightMySQLSharedServerID(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	tgt.execOutput[`mysql -NBe 'SELECT @@server_id;' 2>&1`] = "100"
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err == nil || r.Notes == "" {
		t.Fatal("shared server_id must fail preflight")
	}
}

func TestPreflightMySQLSourceReadOnly(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	src.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tON"
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err == nil || r.Notes == "" {
		t.Fatal("source read_only=ON must fail preflight")
	}
}

func TestPreflightMySQLTargetNotReadOnly(t *testing.T) {
	src, tgt := mysqlPreflightMocks()
	tgt.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tOFF"
	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightMySQL(context.Background(), mysqlReplConfig(), false)
	if err == nil || r.Notes == "" {
		t.Fatal("target not read_only must fail preflight")
	}
}

// promoteMySQLCutover: the source must be frozen (read_only ON) BEFORE the target
// replica is stopped and made writable. We assert the command order and that a
// source that resists freezing fails closed (no promote → no dual-writer).
func TestPromoteMySQLCutoverHappyPath(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// Target replica must be running for the BEFORE gate.
	tgt.execOutput[`mysql -e 'SHOW REPLICA STATUS\G' 2>&1`] = "Replica_IO_Running: Yes\nReplica_SQL_Running: Yes\nSource_Host: source"
	// After promote the target must report read_only=OFF.
	tgt.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tOFF"

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteMySQLCutover(context.Background(), mysqlReplConfig()); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Order: source frozen (SET GLOBAL read_only ON) BEFORE STOP REPLICA and
	// before the target is made writable.
	srcFrozen := containsCommandPrefix(src.commands, `mysql -e 'SET GLOBAL read_only=ON`)
	if !srcFrozen {
		t.Fatal("source was not frozen (read_only ON) during promote")
	}
	stopIdx := indexOfPrefix(tgt.commands, `mysql -e 'STOP REPLICA;'`)
	srcFreezeIdx := indexOfPrefix(src.commands, `mysql -e 'SET GLOBAL read_only=ON`)
	if srcFreezeIdx < 0 || stopIdx < 0 {
		t.Fatal("expected both source freeze and target STOP REPLICA")
	}
	if srcFreezeIdx > stopIdx {
		t.Fatal("source must be frozen before the target replica is stopped (dual-writer window)")
	}
	// Target made writable.
	if !containsCommandPrefix(tgt.commands, `mysql -e 'SET GLOBAL read_only=OFF`) {
		t.Fatal("target was not made writable (read_only OFF)")
	}
}

func TestPromoteMySQLCutoverNoReplicaFailsClosed(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// Replica not running → BEFORE gate fails; source must never be touched.
	tgt.execOutput[`mysql -e 'SHOW REPLICA STATUS\G' 2>&1`] = "Replica_IO_Running: No\nReplica_SQL_Running: No\nSource_Host: source"

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteMySQLCutover(context.Background(), mysqlReplConfig()); err == nil {
		t.Fatal("promote must fail when replica is not running")
	}
	if len(src.commands) != 0 {
		t.Fatalf("source must not be touched when replica is not running (dual-writer risk): %v", src.commands)
	}
}

func TestPromoteMySQLCutoverSourceFreezeFailsClosed(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	tgt.execOutput[`mysql -e 'SHOW REPLICA STATUS\G' 2>&1`] = "Replica_IO_Running: Yes\nReplica_SQL_Running: Yes\nSource_Host: source"
	// Make the source freeze fail: the shell-quoted SET GLOBAL command errors.
	// The mock only honors execErr when the command also matches execOutput, so
	// register it there too.
	freezeCmd := `mysql -e 'SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;' 2>&1`
	src.execOutput[freezeCmd] = ""
	src.execErr[freezeCmd] = context.DeadlineExceeded

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	err := e.promoteMySQLCutover(context.Background(), mysqlReplConfig())
	if err == nil {
		t.Fatal("promote must fail when source cannot be frozen (would create split-brain)")
	}
	// Target replica must NOT have been stopped if the source didn't freeze.
	if containsCommandPrefix(tgt.commands, `mysql -e 'STOP REPLICA;'`) {
		t.Fatal("target replica stopped before source was frozen — split-brain window")
	}
}

func TestPromoteMySQLCutoverTargetStaysReadOnlyFailsClosed(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	tgt.execOutput[`mysql -e 'SHOW REPLICA STATUS\G' 2>&1`] = "Replica_IO_Running: Yes\nReplica_SQL_Running: Yes\nSource_Host: source"
	// Post-promote probe still reports read_only=ON → target did not become a primary.
	tgt.execOutput[`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`] = "read_only\tON"

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteMySQLCutover(context.Background(), mysqlReplConfig()); err == nil {
		t.Fatal("promote must fail when target does not become writable")
	}
}

func indexOfPrefix(cmds []string, prefix string) int {
	for i, c := range cmds {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			return i
		}
	}
	return -1
}
