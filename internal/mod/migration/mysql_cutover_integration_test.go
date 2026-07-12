//go:build integration

// Integration test for the Phase 2C MySQL seeded-replication cutover
// primitives against a LIVE MySQL 8 primary + replica pair (Docker). It
// exercises the real ReplicationEngine: seed (mysqldump → mysql restore) BEFORE
// the replica is configured, preflight gates, lag drain, and the fenced promote
// (source freeze → STOP REPLICA → target writable). Traffic switching is faked
// (nginx/haproxy are not in the container pair and are covered by unit tests).
//
// Run: go test -tags integration ./internal/mod/migration/ -run MySQLIntegration -v
// Requires the `docker` CLI and the mysql:8 image (pulled on demand).

package migration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type mysqlPair struct {
	srcExec, tgtExec dockerExecuter
	network          string
	srcC, tgtC       string
}

func setupMySQLPair(t *testing.T) *mysqlPair {
	t.Helper()
	net := "mycut-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)
	src := "my-src-" + randSuffix()
	tgt := "my-tgt-" + randSuffix()
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "my-src",
		"-e", "MYSQL_ROOT_PASSWORD=rootpw", "-e", "MYSQL_DATABASE=appdb",
		"mysql:8")
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "my-tgt",
		"-e", "MYSQL_ROOT_PASSWORD=rootpw",
		"mysql:8")

	pair := &mysqlPair{
		srcExec: dockerExecuter{container: src},
		tgtExec: dockerExecuter{container: tgt},
		network: net,
		srcC:    src,
		tgtC:    tgt,
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", src, tgt).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})

	waitMySQLReady(t, pair.srcExec, src)
	seedMySQLSource(t, pair.srcExec)
	waitMySQLReady(t, pair.tgtExec, tgt)
	buildMySQLReplica(t, pair, src, tgt)
	waitMySQLReplica(t, pair.tgtExec)
	return pair
}

// seedMySQLSource enables binlog + unique server_id, creates the replicator
// user, and writes a probe row so promotion can be verified end-to-end.
func seedMySQLSource(t *testing.T, src dockerExecuter) {
	t.Helper()
	runMySQL(t, src, "SET GLOBAL server_id = 100;")
	runMySQL(t, src, "SET GLOBAL log_bin = ON;")
	runMySQL(t, src, "CREATE USER IF NOT EXISTS 'repl'@'%' IDENTIFIED BY 'rep';")
	runMySQL(t, src, "GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';")
	runMySQL(t, src, "CREATE DATABASE IF NOT EXISTS cutover_probe;")
	runMySQL(t, src, "CREATE TABLE IF NOT EXISTS cutover_probe.t (id INT PRIMARY KEY, v VARCHAR(64));")
	runMySQL(t, src, "INSERT INTO cutover_probe.t VALUES (1,'pre-cutover') ON DUPLICATE KEY UPDATE v=VALUES(v);")
}

// buildMySQLReplica seeds the target from the source (the Phase 2C unseeded-defect
// fix) and configures it as a replica from the seeded binlog position.
func buildMySQLReplica(t *testing.T, pair *mysqlPair, src, tgt string) {
	t.Helper()
	runMySQL(t, pair.tgtExec, "SET GLOBAL server_id = 200;")

	// Seed the target from the source (streamed dump → restore) BEFORE replica setup.
	dump := fmt.Sprintf("mysqldump -uroot -prootpw --single-transaction --routines --triggers --add-drop-database --databases cutover_probe")
	restore := fmt.Sprintf("mysql -uroot -prootpw")
	cmd := fmt.Sprintf("docker exec %s bash -lc %s | docker exec -i %s bash -lc %s",
		src, shQuote(dump), tgt, shQuote(restore))
	if out, err := exec.Command("bash", "-lc", cmd).CombinedOutput(); err != nil {
		t.Fatalf("seed dump|restore failed: %v\n%s", err, out)
	}

	// Read the source binlog position and configure the replica from it.
	status, _, _, _ := pair.srcExec.ExecContext(context.Background(), "mysql -uroot -prootpw -NBe 'SHOW MASTER STATUS\\G'")
	file, pos := parseMySQLMasterStatus(status)
	if file == "" {
		t.Fatalf("source has no binlog file (log_bin off?):\n%s", status)
	}
	chg := fmt.Sprintf(
		"CHANGE REPLICATION SOURCE TO SOURCE_HOST='my-src', SOURCE_USER='repl', "+
			"SOURCE_PASSWORD='rep', SOURCE_LOG_FILE=%s, SOURCE_LOG_POS=%s, "+
			"GET_SOURCE_PUBLIC_KEY=1;", shQuote(file), shQuote(pos))
	runMySQL(t, pair.tgtExec, chg)
	runMySQL(t, pair.tgtExec, "START REPLICA;")
}

func waitMySQLReady(t *testing.T, e dockerExecuter, name string) {
	t.Helper()
	for i := 0; i < 120; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mysql -uroot -prootpw -NBe 'SELECT 1;' 2>&1")
		if rc == 0 && strings.TrimSpace(out) == "1" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("mysql %s not ready", name)
}

func waitMySQLReplica(t *testing.T, tgt dockerExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := tgt.ExecContext(context.Background(), "mysql -uroot -prootpw -NBe 'SHOW REPLICA STATUS\\G' 2>&1")
		if rc == 0 && strings.Contains(out, "Replica_IO_Running: Yes") && strings.Contains(out, "Replica_SQL_Running: Yes") {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("target never became a running replica")
}

func runMySQL(t *testing.T, e dockerExecuter, sql string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(), fmt.Sprintf("mysql -uroot -prootpw -NBe %s 2>&1", shQuote(sql)))
	if rc != 0 {
		t.Fatalf("mysql %q failed (rc=%d): %s %v", sql, rc, out, err)
	}
}

func mysqlIntegrationConfig() ReplicationConfig {
	return ReplicationConfig{
		DatabaseType:    "mysql",
		SourceHost:      "my-src",
		SourcePort:      3306,
		TargetHost:      "my-tgt",
		TargetPort:      3306,
		ReplicationUser: "repl",
		ReplicationPass: "rep",
		MigrationID:     1,
	}
}

// TestMySQLCutoverPrimitivesLive drives the real primitives against a live
// seeded pair: preflight passes, a new row replicates, lag drains, promote
// freezes the source and makes the target writable, and the seeded probe row is
// present on the target.
func TestMySQLCutoverPrimitivesLive(t *testing.T) {
	pair := setupMySQLPair(t)
	eng := NewReplicationEngine(pair.srcExec, pair.tgtExec, nil)
	cfg := mysqlIntegrationConfig()
	ctx := context.Background()

	res, err := eng.preflightMySQL(ctx, cfg)
	if err != nil {
		t.Fatalf("preflight: %v (notes=%s)", err, res.Notes)
	}
	if res.SourceInRecovery {
		t.Fatal("source must be primary (read_only OFF)")
	}
	if !res.TargetInRecovery {
		t.Fatal("target must be read-only standby before cutover")
	}

	// Write a row on the source; it must replicate to the seeded target.
	runMySQL(t, pair.srcExec, "INSERT INTO cutover_probe.t VALUES (2,'replicated') ON DUPLICATE KEY UPDATE v=VALUES(v);")
	if err := eng.WaitForCatchUp(ctx, cfg, 5); err != nil {
		t.Fatalf("catch-up: %v", err)
	}

	if err := eng.promoteMySQLCutover(ctx, cfg); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Seeded row + replicated row both present; target now writable.
	r, _, _, _ := pair.tgtExec.ExecContext(ctx, "mysql -uroot -prootpw -NBe \"SELECT count(*) FROM cutover_probe.t;\" 2>&1")
	if strings.TrimSpace(r) != "2" {
		t.Fatalf("target row count = %q, want 2 (seed + replicated)", strings.TrimSpace(r))
	}
	out, _, rc, _ := pair.tgtExec.ExecContext(ctx, "mysql -uroot -prootpw -NBe \"INSERT INTO cutover_probe.t VALUES (3,'writable') ON DUPLICATE KEY UPDATE v=VALUES(v); SELECT 'ok';\" 2>&1")
	if rc != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("target not writable after promote (rc=%d): %s", rc, out)
	}
}

// TestMySQLPreflightFailsWhenSourceReadOnly injects the failure the unit tests
// mock: flip the source to read_only=ON, then preflight must refuse (source is
// no longer a writable primary).
func TestMySQLPreflightFailsWhenSourceReadOnly(t *testing.T) {
	pair := setupMySQLPair(t)
	eng := NewReplicationEngine(pair.srcExec, pair.tgtExec, nil)
	runMySQL(t, pair.srcExec, "SET GLOBAL read_only = ON; SET GLOBAL super_read_only = ON;")
	res, err := eng.preflightMySQL(context.Background(), mysqlIntegrationConfig())
	if err == nil {
		t.Fatalf("preflight should FAIL when source is read_only (got %+v)", res)
	}
	if !strings.Contains(res.Notes, "read_only") {
		t.Fatalf("expected read_only note, got %q", res.Notes)
	}
}
