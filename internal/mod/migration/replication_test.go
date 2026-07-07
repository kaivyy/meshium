package migration

import (
	"context"
	"testing"
)

// TestRedisLagFailsClosedWhenNotReplica proves redisLag returns an error (not 0)
// when `redis-cli INFO replication` has no master_last_io_seconds_ago line — i.e.
// the target is not acting as a replica. Returning 0 would let WaitForCatchUp
// treat replication as instantly caught up and promote on un-replicated data.
func TestRedisLagFailsClosedWhenNotReplica(t *testing.T) {
	target := newMockSSH()
	// A master (not a replica) reports role:master and no master_last_io_seconds_ago.
	target.execOutput["redis-cli INFO replication 2>&1"] = "role:master\nconnected_slaves:0\n"

	e := NewReplicationEngine(nil, target, nil)
	lag, err := e.redisLag(context.Background(), ReplicationConfig{DatabaseType: "redis"})
	if err == nil {
		t.Fatalf("redisLag returned no error for a non-replica target (lag=%d); it must fail closed", lag)
	}
	if lag == 0 {
		t.Fatalf("redisLag returned lag 0 on failure; callers treat 0 as caught-up and would promote on un-replicated data")
	}
}

// TestRedisLagReportsSecondsWhenReplica confirms the happy path still parses the
// lag value when the target is a properly configured replica.
func TestRedisLagReportsSecondsWhenReplica(t *testing.T) {
	target := newMockSSH()
	target.execOutput["redis-cli INFO replication 2>&1"] =
		"role:slave\nmaster_link_status:up\nmaster_last_io_seconds_ago:3\n"

	e := NewReplicationEngine(nil, target, nil)
	lag, err := e.redisLag(context.Background(), ReplicationConfig{DatabaseType: "redis"})
	if err != nil {
		t.Fatalf("redisLag error on healthy replica: %v", err)
	}
	if lag != 3 {
		t.Fatalf("expected lag 3, got %d", lag)
	}
}

// TestRedisLagFailsClosedWhenLinkDown confirms a down master link is reported as
// an error rather than a low/zero lag.
func TestRedisLagFailsClosedWhenLinkDown(t *testing.T) {
	target := newMockSSH()
	target.execOutput["redis-cli INFO replication 2>&1"] =
		"role:slave\nmaster_link_status:down\n"

	e := NewReplicationEngine(nil, target, nil)
	if _, err := e.redisLag(context.Background(), ReplicationConfig{DatabaseType: "redis"}); err == nil {
		t.Fatal("redisLag returned no error while master link is down; it must fail closed")
	}
}

// TestSetupMongoDBFailsClosedWithoutTouchingSource proves MongoDB replication
// setup refuses up front and never issues rs.add() (or any command) against the
// live source. mongoDBLag cannot measure replica-set lag, so a cutover could
// never verify catch-up; reconfiguring the source's replica set for a migration
// that cannot safely complete would leave the source altered for nothing.
func TestSetupMongoDBFailsClosedWithoutTouchingSource(t *testing.T) {
	source := newMockSSH()
	target := newMockSSH()

	e := NewReplicationEngine(source, target, nil)
	err := e.setupMongoDB(context.Background(), ReplicationConfig{
		DatabaseType: "mongodb",
		SourceHost:   "src",
		TargetHost:   "dst",
		SourcePort:   27017,
	})
	if err == nil {
		t.Fatal("setupMongoDB returned no error; MongoDB cutover must fail closed without lag checks")
	}
	if len(source.commands) != 0 {
		t.Fatalf("setupMongoDB issued %d command(s) against the source before failing closed: %v", len(source.commands), source.commands)
	}
}
