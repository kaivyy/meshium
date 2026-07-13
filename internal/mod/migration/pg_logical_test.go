package migration

import (
	"context"
	"strings"
	"testing"
)

// TestLogicalConnStringEscapesCredentials proves the subscription DSN escapes
// the password so an embedded single quote cannot break out of the quoted DSN,
// and that no -p argv password flag is used.
func TestLogicalConnStringEscapesCredentials(t *testing.T) {
	cfg := ReplicationConfig{
		SourceHost:      "pg-src",
		SourcePort:      5432,
		DatabaseName:    "appdb",
		ReplicationUser: "replicator",
		ReplicationPass: "p'ass'word", // hostile single quote
	}
	conn := logicalConnString(cfg)
	if strings.Contains(conn, "-p") {
		t.Fatalf("connstring must not use -p flag: %q", conn)
	}
	if !strings.Contains(conn, "password=p''ass''word") {
		t.Fatalf("password not SQL-escaped in DSN: %q", conn)
	}
	if !strings.Contains(conn, "host=pg-src port=5432 dbname=appdb user=replicator") {
		t.Fatalf("connstring missing expected fields: %q", conn)
	}
}

// TestLogicalConnStringDefaults proves safe defaults when fields are empty.
func TestLogicalConnStringDefaults(t *testing.T) {
	conn := logicalConnString(ReplicationConfig{ReplicationPass: "rep"})
	if !strings.Contains(conn, "host=source port=5432 dbname=postgres user=replicator password=rep") {
		t.Fatalf("defaults wrong: %q", conn)
	}
}

// TestPreflightLogicalPGWalLevelNotLogicalFails proves the hard gate: a source
// with wal_level != 'logical' fails closed (no publication/subscription created).
func TestPreflightLogicalPGWalLevelNotLogicalFails(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// source: same major 15, primary, wal_level=replica (wrong), db exists.
	src.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "150003"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"
	src.execOutput["sudo -u postgres psql -tAc 'SHOW wal_level;' 2>&1"] = "replica"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT 1 FROM pg_database WHERE datname = ''appdb'';' 2>&1"] = "1"
	// replicator connectivity probe (exact key).
	src.execOutput["sudo -u postgres psql -tAc 'SELECT 1 FROM pg_database WHERE datname = ''appdb'';' 2>&1"] = "1"
	tgt.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "150003"
	tgt.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"
	tgt.execOutput["PGPASSWORD='rep' psql -h 'pg-src' -p 5432 -U 'replicator' -d postgres -tAc 'SELECT 1;' 2>&1"] = "1"

	eng := NewReplicationEngine(src, tgt, nil)
	res, err := eng.PreflightLogicalPG(context.Background(), ReplicationConfig{
		DatabaseType: "postgres", DatabaseName: "appdb",
		ReplicationUser: "replicator", ReplicationPass: "rep", MigrationID: 1,
	})
	if err == nil {
		t.Fatalf("preflight must fail when wal_level != logical (got %+v)", res)
	}
	if res.WalLevel != "replica" {
		t.Fatalf("wal level captured = %q, want replica", res.WalLevel)
	}
	// No CREATE PUBLICATION / CREATE SUBSCRIPTION must have been issued.
	for _, c := range src.commands {
		if strings.Contains(c, "CREATE PUBLICATION") || strings.Contains(c, "CREATE SUBSCRIPTION") {
			t.Fatalf("preflight must not create replication objects; saw: %s", c)
		}
	}
}

// TestPreflightLogicalPGCrossMajorFails proves same-major is enforced.
func TestPreflightLogicalPGCrossMajorFails(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "150003"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"
	src.execOutput["sudo -u postgres psql -tAc 'SHOW wal_level;' 2>&1"] = "logical"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT 1 FROM pg_database WHERE datname = ''appdb'';' 2>&1"] = "1"
	tgt.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "160001" // different major
	tgt.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"
	tgt.execOutput["PGPASSWORD='rep' psql -h 'pg-src' -p 5432 -U 'replicator' -d postgres -tAc 'SELECT 1;' 2>&1"] = "1"

	eng := NewReplicationEngine(src, tgt, nil)
	res, err := eng.PreflightLogicalPG(context.Background(), ReplicationConfig{
		DatabaseType: "postgres", DatabaseName: "appdb",
		ReplicationUser: "replicator", ReplicationPass: "rep", MigrationID: 1,
	})
	if err == nil {
		t.Fatalf("preflight must fail on cross-major (got %+v)", res)
	}
}

// TestPreflightLogicalPGTargetMustBePrimary proves a standby target is rejected
// for logical replication (the target must be an independent primary).
func TestPreflightLogicalPGTargetMustBePrimary(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "150003"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "f"
	src.execOutput["sudo -u postgres psql -tAc 'SHOW wal_level;' 2>&1"] = "logical"
	src.execOutput["sudo -u postgres psql -tAc 'SELECT 1 FROM pg_database WHERE datname = ''appdb'';' 2>&1"] = "1"
	tgt.execOutput["sudo -u postgres psql -tAc 'SHOW server_version_num;' 2>&1"] = "150003"
	tgt.execOutput["sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1"] = "t" // standby — wrong for logical
	tgt.execOutput["PGPASSWORD='rep' psql -h 'pg-src' -p 5432 -U 'replicator' -d postgres -tAc 'SELECT 1;' 2>&1"] = "1"

	eng := NewReplicationEngine(src, tgt, nil)
	res, err := eng.PreflightLogicalPG(context.Background(), ReplicationConfig{
		DatabaseType: "postgres", DatabaseName: "appdb",
		ReplicationUser: "replicator", ReplicationPass: "rep", MigrationID: 1,
	})
	if err == nil {
		t.Fatalf("preflight must fail when target is a standby (got %+v)", res)
	}
	if !res.TargetInRecovery {
		t.Fatalf("target recovery state must be reported true")
	}
}

// TestSetupLogicalReplicationIdempotent proves a re-run on an already-configured
// pair does not error (handles the "already exists" path from an interrupted run).
func TestSetupLogicalReplicationIdempotent(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// Both CREATE statements already exist on re-run.
	src.execOutput["sudo -u postgres psql -tAc 'CREATE PUBLICATION meshium_pub FOR ALL TABLES;' 2>&1"] =
		"ERROR:  publication \"meshium_pub\" already exists"
	// The subscription key varies only by the connection string; prefix-match on
	// the stable CREATE SUBSCRIPTION prefix is enough.
	tgt.execOutput["sudo -u postgres psql -tAc 'CREATE SUBSCRIPTION meshium_sub"] =
		"ERROR:  subscription \"meshium_sub\" already exists"

	eng := NewReplicationEngine(src, tgt, nil)
	if err := eng.SetupLogicalReplicationPG(context.Background(), ReplicationConfig{
		DatabaseType: "postgres", DatabaseName: "appdb",
		SourceHost: "pg-src", ReplicationUser: "replicator", ReplicationPass: "rep", MigrationID: 1,
	}); err != nil {
		t.Fatalf("idempotent setup: %v", err)
	}
}
