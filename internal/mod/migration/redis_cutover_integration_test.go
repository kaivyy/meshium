//go:build integration

// Integration test for the Phase 3C Redis cutover primitives against a LIVE
// Redis 7 source(master) + target(replica) pair (Docker).
//
// Redis has NO source freeze primitive, so this path is certified as DEGRADED,
// never AUTOMATIC: the dual-writer window is bounded by the async replication
// gap, not eliminated. The test therefore proves the honest contract:
//   - preflight gates on source=master / target=replica(link up),
//   - promoteRedisCutover issues REPLICAOF NO ONE and the target becomes master,
//   - CutoverFreezeSource returns ErrNoSourceFreeze (degraded, not fatal).
//
// Run: go test -tags integration ./internal/mod/migration/ -run RedisIntegration -v
// Requires the `docker` CLI and the redis:7 image (pulled on demand).

package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type redisPair struct {
	srcExec, tgtExec redisDockerExecuter
	network          string
	srcC, tgtC       string
}

// redisDockerExecuter runs redis-cli inside a redis container. redis:7 ships no
// auth in this test, so a plain `redis-cli` works (mirrors the production path
// where creds are injected by the SSHExecuter wrapper).
type redisDockerExecuter struct {
	container string
}

func (d redisDockerExecuter) Exec(cmd string) (string, string, int, error) {
	return d.ExecContext(context.Background(), cmd)
}

func (d redisDockerExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	c := exec.CommandContext(ctx, "docker", "exec", d.container, "bash", "-lc", cmd)
	out, err := c.CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(out), "", exit, err
}

func (redisDockerExecuter) IsAlive() bool { return true }
func (redisDockerExecuter) Upload(io.Reader, string) error {
	return fmt.Errorf("redisDockerExecuter: Upload unsupported")
}
func (redisDockerExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("redisDockerExecuter: Download unsupported")
}

func setupRedisPair(t *testing.T) *redisPair {
	t.Helper()
	net := "redcut-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)
	src := "red-src-" + randSuffix()
	tgt := "red-tgt-" + randSuffix()

	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "red-src",
		"redis:7", "redis-server", "--save", "", "--appendonly", "no")

	pair := &redisPair{
		srcExec: redisDockerExecuter{container: src},
		tgtExec: redisDockerExecuter{container: tgt},
		network: net,
		srcC:    src,
		tgtC:    tgt,
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", src, tgt).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})

	waitRedisReady(t, pair.srcExec, src)
	seedRedisSource(t, pair.srcExec)

	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "red-tgt",
		"redis:7", "redis-server", "--save", "", "--appendonly", "no")

	waitRedisReady(t, pair.tgtExec, tgt)
	// Point the target at the source as a replica (REPLICAOF).
	runRedis(t, pair.tgtExec, "REPLICAOF red-src 6379")
	waitRedisReplica(t, pair.tgtExec)
	return pair
}

func seedRedisSource(t *testing.T, src redisDockerExecuter) {
	t.Helper()
	// A probe key lets us verify writes reached the replica pre-promote.
	runRedis(t, src, "SET cutover_probe pre-cutover")
	runRedis(t, src, "SET akey avalue")
}

func waitRedisReady(t *testing.T, e redisDockerExecuter, name string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "redis-cli PING")
		if rc == 0 && strings.TrimSpace(out) == "PONG" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("redis %s not ready", name)
}

func waitRedisReplica(t *testing.T, e redisDockerExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "redis-cli INFO replication 2>&1")
		if rc == 0 {
			role := ""
			link := ""
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "role:") {
					role = strings.TrimPrefix(line, "role:")
				}
				if strings.HasPrefix(line, "master_link_status:") {
					link = strings.TrimPrefix(line, "master_link_status:")
				}
			}
			if strings.TrimSpace(role) == "slave" && strings.TrimSpace(link) == "up" {
				// Confirm the seeded key replicated.
				if k, _, _, _ := e.ExecContext(context.Background(), "redis-cli GET cutover_probe"); strings.Contains(k, "pre-cutover") {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("target never became a linked replica with seeded data")
}

func runRedis(t *testing.T, e redisDockerExecuter, cmd string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(), "redis-cli "+cmd)
	if rc != 0 {
		t.Fatalf("redis %q failed (rc=%d): %s %v", cmd, rc, out, err)
	}
}

// TestRedisCutoverPrimitivesLive proves the DEGRADED Redis cutover contract on a
// live master/replica pair: preflight gates, promote runs REPLICAOF NO ONE and
// the target becomes master, and no source freeze exists (ErrNoSourceFreeze).
func TestRedisCutoverPrimitivesLive(t *testing.T) {
	pair := setupRedisPair(t)
	cfg := ReplicationConfig{DatabaseType: "redis", SourceHost: "red-src", SourcePort: 6379}

	// Preflight: source master + target replica(link up).
	r, err := (&ReplicationEngine{sourceSSH: pair.srcExec, targetSSH: pair.tgtExec}).
		preflightRedis(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("preflight: %v (notes=%q)", err, r.Notes)
	}
	if !r.ReplicatorOK || !r.TargetInRecovery {
		t.Fatalf("preflight flags wrong: %+v", r)
	}

	// Source freeze does NOT exist for Redis → degraded, not fatal.
	ferr := (&ReplicationEngine{sourceSSH: pair.srcExec, targetSSH: pair.tgtExec}).
		CutoverFreezeSource(context.Background(), cfg)
	if !errors.Is(ferr, ErrNoSourceFreeze) {
		t.Fatalf("expected ErrNoSourceFreeze, got %v", ferr)
	}

	// Promote: target becomes master (REPLICAOF NO ONE).
	if err := (&ReplicationEngine{sourceSSH: pair.srcExec, targetSSH: pair.tgtExec}).
		promoteRedisCutover(context.Background(), cfg); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Post-promote: target is now role:master.
	out, _, rc, err := pair.tgtExec.ExecContext(context.Background(), "redis-cli INFO replication 2>&1")
	if rc != 0 || err != nil {
		t.Fatalf("post-promote probe failed: %s %v", out, err)
	}
	role := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "role:") {
			role = strings.TrimPrefix(line, "role:")
		}
	}
	if strings.TrimSpace(role) != "master" {
		t.Fatalf("target did not become master (role=%s)", role)
	}

	// The promoted target still holds the seeded data (no data loss on promote).
	if k, _, _, _ := pair.tgtExec.ExecContext(context.Background(), "redis-cli GET cutover_probe"); !strings.Contains(k, "pre-cutover") {
		t.Fatalf("promoted target lost seeded data: %q", k)
	}
}
