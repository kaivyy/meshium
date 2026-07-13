package migration

import (
	"context"
	"strings"
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

// TestExecCommandErrorRedactsRemoteOutput proves the diagnostic built from
// remote command stdout/stderr (which can contain connection strings,
// -pPASSWORD, or tokens) is redacted before it becomes an error string
// that flows into logs, events, and API responses. This is the highest-risk
// secret sink in the replication path.
func TestExecCommandErrorRedactsRemoteOutput(t *testing.T) {
	const raw = "mysql -uroot -pS3cretPass! -h10.0.0.5 'FLUSH TABLES WITH READ LOCK' exited 1: Access denied"
	got := execCommandError(nil, 1, raw, "")

	if got == "" {
		t.Fatal("empty diagnostic")
	}
	if strings.Contains(got, "S3cretPass!") {
		t.Fatalf("secret leaked into command-error diagnostic: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker in diagnostic: %s", got)
	}
}


// TestPreflightMongoDBEligible verifies the operator preflight reports an
// actionable manual-cutover-eligible verdict for a same-major PRIMARY/SECONDARY
// replica set with matching FCV and drained lag. This path is NOT wired into the
// automatic CutoverPreflight dispatch (MongoDB stays BLOCKED for automatic) but
// the structured probe must still resolve correctly for the manual runbook.
func TestPreflightMongoDBEligible(t *testing.T) {
	source := newMockSSH()
	target := newMockSSH()
	roleEval := `mongosh --quiet --eval 'var m=rs.status();var self=m.members.find(function(x){return x.self})||m.members[0];var ss=db.serverStatus();var fcv=db.getMongo().getDB("admin").runCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version;JSON.stringify({ok:1,kind:self.stateStr,primary:(self.stateStr==="PRIMARY"),replica:(self.stateStr==="SECONDARY"||self.stateStr==="PRIMARY"),sharded:!!(ss.sharding&&(ss.sharding.configsvr||ss.sharding.clusterRole)),ver:ss.version||"",fcv:fcv||""})'`
	optimeEval := `mongosh --quiet --eval 'var s=rs.status();var m=s.members.find(function(x){return x.name===s.primary})||s.members[0];JSON.stringify({ok:1,optime:(m&&m.optimeDate)?new Date(m.optimeDate).getTime():null})'`
	source.execOutput[roleEval] = `{"ok":1,"kind":"PRIMARY","primary":true,"replica":true,"sharded":false,"ver":"7.0.37","fcv":"7.0"}`
	target.execOutput[roleEval] = `{"ok":1,"kind":"SECONDARY","primary":false,"replica":true,"sharded":false,"ver":"7.0.37","fcv":"7.0"}`
	// optime probes (source frontier ~= target applied → drained lag)
	source.execOutput[optimeEval] = `{"ok":1,"optime":1700000000000}`
	target.execOutput[optimeEval] = `{"ok":1,"optime":1700000000000}`

	e := NewReplicationEngine(source, target, nil)
	r, err := e.preflightMongoDB(context.Background(), ReplicationConfig{DatabaseType: "mongodb"})
	if err != nil {
		t.Fatalf("preflightMongoDB: %v (notes=%q)", err, r.Notes)
	}
	if !r.ReplicatorOK {
		t.Fatalf("expected ReplicatorOK=true: %+v", r)
	}
	if !strings.Contains(r.Notes, "manual-cutover-eligible") {
		t.Fatalf("expected manual-cutover-eligible verdict, got notes=%q", r.Notes)
	}
}

// TestPreflightMongoDBNotPrimary confirms the probe fails closed when the source
// is not a PRIMARY (e.g. already stepped down or a secondary).
func TestPreflightMongoDBNotPrimary(t *testing.T) {
	source := newMockSSH()
	target := newMockSSH()
	roleEval := `mongosh --quiet --eval 'var m=rs.status();var self=m.members.find(function(x){return x.self})||m.members[0];var ss=db.serverStatus();var fcv=db.getMongo().getDB("admin").runCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version;JSON.stringify({ok:1,kind:self.stateStr,primary:(self.stateStr==="PRIMARY"),replica:(self.stateStr==="SECONDARY"||self.stateStr==="PRIMARY"),sharded:!!(ss.sharding&&(ss.sharding.configsvr||ss.sharding.clusterRole)),ver:ss.version||"",fcv:fcv||""})'`
	source.execOutput[roleEval] = `{"ok":1,"kind":"SECONDARY","primary":false,"replica":true,"sharded":false,"ver":"7.0.37","fcv":"7.0"}`
	target.execOutput[roleEval] = `{"ok":1,"kind":"SECONDARY","primary":false,"replica":true,"sharded":false,"ver":"7.0.37","fcv":"7.0"}`

	e := NewReplicationEngine(source, target, nil)
	r, err := e.preflightMongoDB(context.Background(), ReplicationConfig{DatabaseType: "mongodb"})
	if err == nil || r.Notes == "" {
		t.Fatalf("non-primary source must fail preflight: %+v", r)
	}
}

// TestMongoDBLagParsesDrained verifies mongoDBLag computes a non-negative,
// near-zero lag when source frontier and target applied optime match.
func TestMongoDBLagParsesDrained(t *testing.T) {
	source := newMockSSH()
	target := newMockSSH()
	optimeEval := `mongosh --quiet --eval 'var s=rs.status();var m=s.members.find(function(x){return x.name===s.primary})||s.members[0];JSON.stringify({ok:1,optime:(m&&m.optimeDate)?new Date(m.optimeDate).getTime():null})'`
	source.execOutput[optimeEval] = `{"ok":1,"optime":1700000000000}`
	target.execOutput[optimeEval] = `{"ok":1,"optime":1700000000000}`

	e := NewReplicationEngine(source, target, nil)
	lag, err := e.mongoDBLag(context.Background(), ReplicationConfig{DatabaseType: "mongodb"})
	if err != nil {
		t.Fatalf("mongoDBLag: %v", err)
	}
	if lag < 0 || lag > 1 {
		t.Fatalf("expected drained lag in [0,1], got %d", lag)
	}
}
