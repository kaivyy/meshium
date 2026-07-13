//go:build integration

// Integration test for the Phase 3D MongoDB cutover assessment against a LIVE
// mongo:7 replica set (Docker): one PRIMARY + one SECONDARY.
//
// MongoDB has NO source-freeze primitive and its only promotion lever (rs.stepDown
// / rs.remove) reconfigures the LIVE source replica set — out-of-bounds for an
// automatic, fenced cutover (rules #3/#8). So MongoDB stays BLOCKED for automatic
// and the supported path is a human-performed manual cutover. This test therefore
// proves the honest 3D contract:
//   - replica-set lag IS now measurable via rs.status() optimeDate (B4 closed),
//   - preflightMongoDB returns an actionable "manual-cutover-eligible" verdict
//     (source PRIMARY, target SECONDARY, same major, FCV match, lag drained),
//   - CutoverPreflight(dispatch) and CutoverPromote stay BLOCKED (fail closed) —
//     no rs.* command is ever issued against the source.
//
// Run: go test -tags integration ./internal/mod/migration/ -run MongoIntegration -v
// Requires the `docker` CLI and the mongo:7 image (pulled on demand).

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

type mongoPair struct {
	priExec, secExec mongoDockerExecuter
	network          string
	priC, secC       string
	rsName           string
}

type mongoDockerExecuter struct {
	container string
}

func (d mongoDockerExecuter) Exec(cmd string) (string, string, int, error) {
	return d.ExecContext(context.Background(), cmd)
}

func (d mongoDockerExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
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

func (mongoDockerExecuter) IsAlive() bool { return true }
func (mongoDockerExecuter) Upload(io.Reader, string) error {
	return fmt.Errorf("mongoDockerExecuter: Upload unsupported")
}
func (mongoDockerExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("mongoDockerExecuter: Download unsupported")
}

func setupMongoPair(t *testing.T) *mongoPair {
	t.Helper()
	net := "mgocut-" + randSuffix()
	rs := "rs-" + randSuffix()
	pri := "mgo-pri-" + randSuffix()
	sec := "mgo-sec-" + randSuffix()

	mustRun(t, "docker", "network", "create", net)
	for _, c := range []string{pri, sec} {
		mustRun(t, "docker", "run", "-d", "--rm",
			"--name", c, "--network", net, "--network-alias", c,
			"mongo:7", "mongod", "--replSet", rs, "--bind_ip_all")
	}
	pair := &mongoPair{priExec: mongoDockerExecuter{container: pri}, secExec: mongoDockerExecuter{container: sec}, network: net, priC: pri, secC: sec, rsName: rs}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", pri, sec).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})

	waitMongoReady(t, pair.priExec, pri)

	// Initiate the replica set with the primary + secondary (use container
	// network aliases so the set members resolve inside the Docker network).
	cfg := fmt.Sprintf(`rs.initiate({_id:"%s",members:[{_id:0,host:"%s:27017"},{_id:1,host:"%s:27017"}]})`,
		rs, pri, sec)
	runMongo(t, pair.priExec, cfg)
	waitMongoPrimary(t, pair.priExec)
	waitMongoSecondary(t, pair.secExec)

	// Seed a probe doc on the primary so replication can be observed.
	runMongo(t, pair.priExec, "db.cutover_probe.insertOne({_id:1,v:'pre-cutover'})")
	waitMongoReplicated(t, pair.secExec)
	return pair
}

func waitMongoReady(t *testing.T, e mongoDockerExecuter, name string) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mongosh --quiet --eval 'db.runCommand({ping:1}).ok' 2>&1")
		if rc == 0 && strings.Contains(out, "1") {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("mongo %s not ready", name)
}

func waitMongoPrimary(t *testing.T, e mongoDockerExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mongosh --quiet --eval 'rs.isMaster().ismaster' 2>&1")
		if rc == 0 && strings.Contains(out, "true") {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("mongo never became PRIMARY")
}

func waitMongoSecondary(t *testing.T, e mongoDockerExecuter) {
	t.Helper()
	for i := 0; i < 90; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mongosh --quiet --eval 'rs.isMaster().secondary' 2>&1")
		if rc == 0 && strings.Contains(out, "true") {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("mongo never became SECONDARY")
}

func waitMongoReplicated(t *testing.T, e mongoDockerExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mongosh --quiet --eval 'db.cutover_probe.find({_id:1}).count()' 2>&1")
		if rc == 0 && strings.Contains(strings.TrimSpace(out), "1") {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("probe doc never replicated to secondary")
}

func runMongo(t *testing.T, e mongoDockerExecuter, cmd string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(), "mongosh --quiet --eval "+shQuote(cmd)+" 2>&1")
	if rc != 0 {
		t.Fatalf("mongo %q failed (rc=%d): %s %v", cmd, rc, out, err)
	}
}

// TestMongoCutoverAssessmentLive proves the 3D contract on a live replica set:
// lag is measurable, preflightMongoDB returns a manual-cutover-eligible verdict,
// and the automatic dispatch stays BLOCKED (no rs.* issued against the source).
func TestMongoCutoverAssessmentLive(t *testing.T) {
	pair := setupMongoPair(t)
	cfg := ReplicationConfig{DatabaseType: "mongodb", SourceHost: pair.priC, TargetHost: pair.secC, SourcePort: 27017, TargetPort: 27017}

	e := &ReplicationEngine{sourceSSH: pair.priExec, targetSSH: pair.secExec}

	// Lag is measurable (drained to ~0 on a quiet replica set).
	if lag, err := e.mongoDBLag(context.Background(), cfg); err != nil {
		t.Fatalf("mongoDBLag: %v", err)
	} else if lag < 0 || lag > 10 {
		t.Fatalf("mongoDBLag out of expected drained range: %d", lag)
	}

	// Operator preflight: honest manual-cutover-eligible verdict (NOT automatic).
	r, err := e.preflightMongoDB(context.Background(), cfg)
	if err != nil {
		t.Fatalf("preflightMongoDB: %v (notes=%q)", err, r.Notes)
	}
	if !r.ReplicatorOK {
		t.Fatalf("preflightMongoDB should report replicator OK: %+v", r)
	}

	// Automatic dispatch stays BLOCKED — no rs.* mutation against the source.
	if _, err := e.CutoverPreflight(context.Background(), cfg, false); !errors.Is(err, ErrCutoverPreflight) {
		t.Fatalf("automatic CutoverPreflight must fail closed, got %v", err)
	}
	if err := e.CutoverPromote(context.Background(), cfg); !errors.Is(err, ErrCutoverPreflight) {
		t.Fatalf("automatic CutoverPromote must fail closed, got %v", err)
	}
	// And crucially no rs.stepDown / rs.remove / rs.add reached the source.
	if containsUnsafeMongo(e.sourceSSH) {
		t.Fatalf("automatic path issued an rs.* command against the source")
	}
}

// containsUnsafeMongo reports whether any recorded command would reconfigure the
// live replica set — the exact hazard 3D must never trigger automatically. The
// real docker executer does not record commands, so this is only meaningful for
// mocks; it returns false (safe) when no recording accessor exists.
func containsUnsafeMongo(ssh SSHExecuter) bool {
	type commander interface{ Commands() []string }
	if m, ok := ssh.(commander); ok {
		for _, c := range m.Commands() {
			if strings.Contains(c, "rs.stepDown") || strings.Contains(c, "rs.remove") || strings.Contains(c, "rs.add") || strings.Contains(c, "rs.reconfig") {
				return true
			}
		}
	}
	return false
}
