package migration

import (
	"context"
	"errors"
	"testing"
)

// topoSsh is a mock SSHExecuter pre-seeded with per-engine probe outputs.
// It reuses the package's mockSSH (execOutput keyed by exact or prefix cmd).
func newTopoMock() *mockSSH { return newMockSSH() }

// --- MySQL topology probe ---

func TestProbeMySQLSourcePrimaryTargetReplica(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tOFF",
	})
	e.targetSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tON",
	})
	probe, err := e.probeMySQL(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.source != topoPrimary || probe.target != topoReplica {
		t.Fatalf("want source=primary target=replica; got %v/%v", probe.source, probe.target)
	}
	if !probe.safeForRepoint() {
		t.Fatal("source-primary/target-replica must be safe for repoint")
	}
}

func TestProbeMySQLBothWritableFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tOFF",
	})
	e.targetSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tOFF",
	})
	probe, _ := e.probeMySQL(context.Background())
	if probe.safeForRepoint() {
		t.Fatal("both-writable must NOT be safe for repoint")
	}
}

func TestProbeMySQLSourceUnreachable(t *testing.T) {
	e := &ReplicationEngine{}
	src := newMockSSH() // no output → exit 0, empty; mysqlReadOnly sees exit 0 + empty output → error
	src.execExit[`mysql`] = 2 // nonzero exit on source
	e.sourceSSH = src
	e.targetSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tON",
	})
	_, err := e.probeMySQL(context.Background())
	if err == nil {
		t.Fatal("source unreachable must fail closed")
	}
	if !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("must be ErrUnsafeTopology, got %v", err)
	}
}

// --- PostgreSQL topology probe ---

func TestProbePostgresSourcePrimaryTargetReplica(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "f",
	})
	e.targetSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "t",
	})
	probe, err := e.probePostgres(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.source != topoPrimary || probe.target != topoReplica {
		t.Fatalf("want source=primary target=replica; got %v/%v", probe.source, probe.target)
	}
}

func TestProbePostgresBothWritableFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "f",
	})
	e.targetSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "f",
	})
	probe, _ := e.probePostgres(context.Background())
	if probe.safeForRepoint() {
		t.Fatal("both-writable postgres must NOT be safe for repoint")
	}
}

// --- Redis topology probe + rollback ---

func TestProbeRedisSourceMasterTargetReplica(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:master\n",
	})
	e.targetSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:slave\n",
	})
	probe, err := e.probeRedis(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.source != topoPrimary || probe.target != topoReplica {
		t.Fatalf("want source=primary target=replica; got %v/%v", probe.source, probe.target)
	}
}

func TestRollbackRedisSourcePrimaryTargetReplicaSafe(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:master\n",
	})
	e.targetSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:slave\n",
	})
	if err := e.rollbackRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "src", SourcePort: 6379}); err != nil {
		t.Fatalf("safe redis repoint must succeed: %v", err)
	}
}

func TestRollbackRedisBothWritableFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:master\n",
	})
	e.targetSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:master\n",
	})
	err := e.rollbackRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "src", SourcePort: 6379})
	if err == nil || !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("both-writable must fail closed ErrUnsafeTopology; got %v", err)
	}
}

func TestRollbackRedisTargetUnreachableFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`redis-cli INFO replication 2>&1`: "role:master\n",
	})
	tgt := newMockSSH()
	tgt.execExit["redis-cli"] = 1 // target redis-cli exits nonzero
	e.targetSSH = tgt
	err := e.rollbackRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "src", SourcePort: 6379})
	if err == nil || !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("target unreachable must fail closed; got %v", err)
	}
}

// --- MySQL/PG/Mongo rollback always fails closed this pass ---

func TestRollbackMySQLAlwaysFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tOFF",
	})
	e.targetSSH = mockWith(map[string]string{
		`mysql -NBe "SHOW VARIABLES LIKE 'read_only'" 2>&1`: "read_only\tON",
	})
	err := e.rollbackMySQL(context.Background(), ReplicationConfig{DatabaseType: "mysql"})
	if err == nil || !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("mysql rollback must fail closed this pass; got %v", err)
	}
}

func TestRollbackPostgresAlwaysFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	e.sourceSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "f",
	})
	e.targetSSH = mockWith(map[string]string{
		`sudo -u postgres psql -tAc 'SELECT pg_is_in_recovery();' 2>&1`: "t",
	})
	err := e.rollbackPostgreSQL(context.Background(), ReplicationConfig{DatabaseType: "postgres"})
	if err == nil || !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("postgres rollback must fail closed this pass; got %v", err)
	}
}

func TestRollbackMongoAlwaysFailsClosed(t *testing.T) {
	e := &ReplicationEngine{sourceSSH: newMockSSH(), targetSSH: newMockSSH()}
	err := e.rollbackMongoDB(context.Background(), ReplicationConfig{DatabaseType: "mongodb"})
	if err == nil || !errors.Is(err, ErrUnsafeTopology) {
		t.Fatalf("mongo rollback must fail closed; got %v", err)
	}
}

func TestRollbackUnknownEngineFailsClosed(t *testing.T) {
	e := &ReplicationEngine{sourceSSH: newMockSSH(), targetSSH: newMockSSH()}
	err := e.Rollback(context.Background(), 1, ReplicationConfig{DatabaseType: "oracle"})
	if err == nil {
		t.Fatal("unknown engine must error")
	}
}

// mockWith builds a mockSSH pre-seeded with exact/prefix command outputs.
func mockWith(outputs map[string]string) *mockSSH {
	m := newMockSSH()
	for k, v := range outputs {
		m.execOutput[k] = v
	}
	return m
}
