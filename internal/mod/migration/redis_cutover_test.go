package migration

import (
	"context"
	"testing"
)

// Phase 2C-6: Redis fenced-cutover primitives.
//
// Redis preflight must confirm the source is a master and the target a replica
// pointing at this source with its link up; promoteRedisCutover must confirm the
// target is a replica (link up) BEFORE REPLICAOF NO ONE, then confirm it became
// a master AFTER. Redis has no freeze, so the dual-writer window is bounded by
// the async repl gap — documented in known-limitations, not eliminated.

func redisReplMaster(src string) string {
	return "role:" + src + "\nconnected_slaves:1\n"
}

func redisReplReplicaOf(masterHost string) string {
	return "role:slave\nmaster_host:" + masterHost + "\nmaster_link_status:up\n"
}

func TestPreflightRedisHappyPath(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput[`redis-cli INFO replication 2>&1`] = redisReplMaster("master")
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = redisReplReplicaOf("source")

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "source"})
	if err != nil {
		t.Fatalf("preflight: %v (notes=%q)", err, r.Notes)
	}
	if !r.TargetInRecovery || !r.ReplicatorOK {
		t.Fatalf("flags: %+v", r)
	}
}

func TestPreflightRedisSourceNotMaster(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput[`redis-cli INFO replication 2>&1`] = redisReplMaster("slave")
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = redisReplReplicaOf("source")

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "source"})
	if err == nil || r.Notes == "" {
		t.Fatal("source not master must fail preflight")
	}
}

func TestPreflightRedisTargetWrongMaster(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput[`redis-cli INFO replication 2>&1`] = redisReplMaster("master")
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = redisReplReplicaOf("other-host")

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "source"})
	if err == nil || r.Notes == "" {
		t.Fatal("target replica pointing at wrong master must fail preflight")
	}
}

func TestPreflightRedisLinkDown(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	src.execOutput[`redis-cli INFO replication 2>&1`] = redisReplMaster("master")
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = "role:slave\nmaster_host:source\nmaster_link_status:down\n"

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	r, err := e.preflightRedis(context.Background(), ReplicationConfig{DatabaseType: "redis", SourceHost: "source"})
	if err == nil || r.Notes == "" {
		t.Fatal("link down must fail preflight")
	}
}

func TestPromoteRedisCutoverHappyPath(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// BEFORE: target is a replica, link up. AFTER: target became master.
	tgt.execOutputSeq[`redis-cli INFO replication 2>&1`] = []string{
		redisReplReplicaOf("source"),
		redisReplMaster("master"),
	}

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteRedisCutover(context.Background(), ReplicationConfig{DatabaseType: "redis"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !containsCommand(tgt.commands, "redis-cli REPLICAOF NO ONE 2>&1") {
		t.Fatalf("REPLICAOF NO ONE not issued: %v", tgt.commands)
	}
}

func TestPromoteRedisCutoverNoReplicaFailsClosed(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// Target already a master → BEFORE gate fails; never promote.
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = redisReplMaster("master")

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteRedisCutover(context.Background(), ReplicationConfig{DatabaseType: "redis"}); err == nil {
		t.Fatal("promote must fail when target is not a replica")
	}
	for _, c := range tgt.commands {
		if c == "redis-cli REPLICAOF NO ONE 2>&1" {
			t.Fatal("REPLICAOF NO ONE issued for a non-replica target")
		}
	}
}

func TestPromoteRedisCutoverNotMasterAfterFailsClosed(t *testing.T) {
	src := newMockSSH()
	tgt := newMockSSH()
	// BEFORE replica; AFTER still a replica → promote did not take effect.
	tgt.execOutput[`redis-cli INFO replication 2>&1`] = redisReplReplicaOf("source")

	e := &ReplicationEngine{sourceSSH: src, targetSSH: tgt}
	if err := e.promoteRedisCutover(context.Background(), ReplicationConfig{DatabaseType: "redis"}); err == nil {
		t.Fatal("promote must fail when target does not become a master")
	}
}
