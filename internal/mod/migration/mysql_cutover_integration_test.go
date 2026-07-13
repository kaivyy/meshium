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
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type mysqlPair struct {
	srcExec, tgtExec mysqlDockerExecuter
	network          string
	srcC, tgtC       string
}

// mysqlDockerExecuter runs commands inside a MySQL container as root and injects
// MYSQL_PWD so the engine's bare `mysql -e` commands (which follow the repo
// convention of credential-via-env, not -p on argv) can authenticate. The shared
// dockerExecuter is Postgres-specific (-u postgres), which MySQL containers lack
// a user for, so MySQL integration tests need their own executer.
type mysqlDockerExecuter struct {
	container string
}

func (d mysqlDockerExecuter) Exec(cmd string) (string, string, int, error) {
	return d.ExecContext(context.Background(), cmd)
}

func (d mysqlDockerExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	c := exec.CommandContext(ctx, "docker", "exec", "-e", "MYSQL_PWD=rootpw", d.container, "bash", "-lc", cmd)
	out, err := c.CombinedOutput()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(out), "", exit, err
}

func (mysqlDockerExecuter) IsAlive() bool { return true }
func (mysqlDockerExecuter) Upload(io.Reader, string) error {
	return errors.New("mysqlDockerExecuter: Upload unsupported")
}
func (mysqlDockerExecuter) Download(string, io.Writer) error {
	return errors.New("mysqlDockerExecuter: Download unsupported")
}

func setupMySQLPair(t *testing.T) *mysqlPair {
	t.Helper()
	net := "mycut-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)
	src := "my-src-" + randSuffix()
	tgt := "my-tgt-" + randSuffix()
	// Start the SOURCE first and wait for it before launching the TARGET. Two
	// MySQL 8.4 containers bootstrapping InnoDB simultaneously on a constrained
	// host contend for I/O and can each exceed the readiness budget; serializing
	// startup keeps each bootstrap fast. MYSQL_INITDB_SKIP_TZINFO + a small
	// buffer pool shrink the footprint/startup so the pair fits on CI hosts.
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "my-src",
		"-e", "MYSQL_ROOT_PASSWORD=rootpw", "-e", "MYSQL_DATABASE=appdb",
		"-e", "MYSQL_INITDB_SKIP_TZINFO=1",
		"mysql:8", "--innodb-buffer-pool-size=128M", "--log-bin=mysql-bin", "--server-id=100")

	pair := &mysqlPair{
		srcExec: mysqlDockerExecuter{container: src},
		tgtExec: mysqlDockerExecuter{container: tgt},
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

	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "my-tgt",
		"-e", "MYSQL_ROOT_PASSWORD=rootpw", "-e", "MYSQL_INITDB_SKIP_TZINFO=1",
		"mysql:8", "--innodb-buffer-pool-size=128M", "--server-id=200")

	waitMySQLReady(t, pair.tgtExec, tgt)
	buildMySQLReplica(t, pair, src, tgt)
	waitMySQLReplica(t, pair.tgtExec)
	return pair
}

// seedMySQLSource creates the replicator user and writes a probe row so
// promotion can be verified end-to-end. binlog + server_id are enabled via
// container args (--log-bin / --server-id) at startup, because log_bin and
// server_id are read-only GLOBALs that cannot be SET at runtime.
func seedMySQLSource(t *testing.T, src SSHExecuter) {
	t.Helper()
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

	// Seed the target from the source (streamed dump → restore) BEFORE replica setup.
	// MYSQL_PWD is injected here (as mysqlDockerExecuter does) because this pipe
	// runs bare `docker exec`, outside the executer, and MySQL 8.4 root@localhost
	// requires the password (auth_socket applies only to the container root OS user,
	// not to the mysql client's TCP/socket login).
	dump := fmt.Sprintf("mysqldump -uroot --single-transaction --routines --triggers --add-drop-database --databases cutover_probe")
	restore := fmt.Sprintf("mysql -uroot")
	cmd := fmt.Sprintf("docker exec -e MYSQL_PWD=rootpw %s bash -lc %s | docker exec -i -e MYSQL_PWD=rootpw %s bash -lc %s",
		src, shQuote(dump), tgt, shQuote(restore))
	if out, err := exec.Command("bash", "-lc", cmd).CombinedOutput(); err != nil {
		t.Fatalf("seed dump|restore failed: %v\n%s", err, out)
	}

	// Read the source binlog position and configure the replica from it.
	status, _, _, _ := mysqlShowMasterStatus(context.Background(), pair.srcExec)
	file, pos := parseMySQLMasterStatus(status)
	if file == "" {
		t.Fatalf("source has no binlog file (log_bin off?):\n%s", status)
	}
	chg := fmt.Sprintf(
		"CHANGE REPLICATION SOURCE TO SOURCE_HOST='my-src', SOURCE_USER='repl', "+
			"SOURCE_PASSWORD='rep', SOURCE_LOG_FILE=%s, SOURCE_LOG_POS=%s, "+
			"GET_SOURCE_PUBLIC_KEY=1;", shQuote(file), pos)
	runMySQL(t, pair.tgtExec, chg)
	runMySQL(t, pair.tgtExec, "START REPLICA;")
	// A real MySQL standby is read-only until promote. preflightMySQL requires the
	// target read_only=ON (the same-role contract as the Pg standby), so set it
	// here to mirror a genuine seeded-replica topology.
	runMySQL(t, pair.tgtExec, "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;")
}

func waitMySQLReady(t *testing.T, e SSHExecuter, name string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "mysql -uroot -NBe 'SELECT 1;' 2>&1")
		if rc == 0 && strings.TrimSpace(out) == "1" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("mysql %s not ready", name)
}

func waitMySQLReplica(t *testing.T, tgt SSHExecuter) {
	t.Helper()
	var last string
	for i := 0; i < 180; i++ {
		// Use `-e` (NOT `-NBe`): with `-B` batch mode the `\G` vertical format
		// strips the `Replica_IO_Running: ` labels, leaving bare `Yes`/`No` values
		// that the readiness grep can never match — the replica would be healthy
		// yet the check times out. `-e` keeps the labels.
		out, _, rc, _ := tgt.ExecContext(context.Background(), "mysql -uroot -e 'SHOW REPLICA STATUS\\G' 2>&1")
		last = fmt.Sprintf("rc=%d out=[%s]", rc, out)
		if rc == 0 && strings.Contains(out, "Replica_IO_Running: Yes") && strings.Contains(out, "Replica_SQL_Running: Yes") {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("target never became a running replica; last status: %s", last)
}

func runMySQL(t *testing.T, e SSHExecuter, sql string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(), fmt.Sprintf("mysql -uroot -NBe %s 2>&1", shQuote(sql)))
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

	res, err := eng.preflightMySQL(ctx, cfg, false)
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
	r, _, _, _ := pair.tgtExec.ExecContext(ctx, "mysql -uroot -NBe \"SELECT count(*) FROM cutover_probe.t;\" 2>&1")
	if strings.TrimSpace(r) != "2" {
		t.Fatalf("target row count = %q, want 2 (seed + replicated)", strings.TrimSpace(r))
	}
	out, _, rc, _ := pair.tgtExec.ExecContext(ctx, "mysql -uroot -NBe \"INSERT INTO cutover_probe.t VALUES (3,'writable') ON DUPLICATE KEY UPDATE v=VALUES(v); SELECT 'ok';\" 2>&1")
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
	res, err := eng.preflightMySQL(context.Background(), mysqlIntegrationConfig(), false)
	if err == nil {
		t.Fatalf("preflight should FAIL when source is read_only (got %+v)", res)
	}
	if !strings.Contains(res.Notes, "read_only") {
		t.Fatalf("expected read_only note, got %q", res.Notes)
	}
}

// TestMySQLSourceFreezeClosesWrites proves Phase 3B: the fenced cutover now
// physically freezes the MySQL source (read_only=ON + super_read_only=ON, held)
// before promote, with no Degraded downgrade, against a LIVE seeded pair. A
// non-SUPER application user must be rejected on write after the freeze — the
// dual-writer window is closed for application traffic. The lease-holder (SUPER)
// can still drive the cutover. Traffic switching is faked.
func TestMySQLSourceFreezeClosesWrites(t *testing.T) {
	pair := setupMySQLPair(t)

	// Application user (NON-SUPER) with write access to the probe table.
	runMySQL(t, pair.srcExec, "CREATE USER IF NOT EXISTS 'appuser'@'%' IDENTIFIED BY 'app';")
	runMySQL(t, pair.srcExec, "GRANT SELECT, INSERT, UPDATE, DELETE ON cutover_probe.* TO 'appuser'@'%';")
	runMySQL(t, pair.srcExec, "FLUSH PRIVILEGES;")

	// Baseline: appuser CAN write before the cutover fence is applied.
	appWrite := func() (string, int) {
		out, _, rc, _ := pair.srcExec.ExecContext(context.Background(),
			"mysql -uappuser -papp -NBe " + shQuote("INSERT INTO cutover_probe.t VALUES (100,'pre-freeze') ON DUPLICATE KEY UPDATE v=VALUES(v); SELECT 'ok';"))
		return out, rc
	}
	if out, rc := appWrite(); rc != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("appuser should be able to write before freeze (rc=%d): %s", rc, out)
	}

	database, _ := newTestDB(t)
	repo := NewRepo(database).(*sqliteRepo)
	for _, sid := range []int{1, 2} {
		if _, err := database.Exec(
			`INSERT INTO servers (id, name, host, port, username) VALUES (?, ?, ?, ?, ?)`,
			sid, "srv", "127.0.0.1", 22, "u"); err != nil {
			t.Fatalf("seed server %d: %v", sid, err)
		}
	}
	if _, err := database.Exec(
		`INSERT INTO migrations (id, source_id, target_id, categories, status) VALUES (1, 1, 2, ?, ?)`,
		"mysql", "planned"); err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	if _, err := repo.CreateStage(context.Background(), 1, string(StageTrafficSwitch), 0); err != nil {
		t.Fatalf("create stage: %v", err)
	}

	auth := &FencingAuthority{repo: repo, clock: realClock{}, ttl: FenceTTL}
	machine := newCutoverMachine(repo, auth, 1)
	eng := NewReplicationEngine(pair.srcExec, pair.tgtExec, repo)
	o := NewCutoverOrchestrator(machine, auth, eng, &fakeTrafficSwitch{})

	out, err := o.Run(context.Background(), CutoverRequest{
		MigrationID:   1,
		Holder:        "meshium-freeze",
		Replication:    mysqlIntegrationConfig(),
		TrafficRequest: nginxSwitchReq("http://verify/health"),
		MaxLagSeconds:  5,
		ObserveFor:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("orchestrator run: %v (out=%+v)", err, out)
	}
	if !out.Completed {
		t.Fatalf("cutover not completed: %+v", out)
	}
	// MySQL freeze is now supported — no degraded downgrade expected.
	for _, d := range out.Degraded {
		if strings.HasPrefix(d, "source_freeze_not_supported:") {
			t.Fatalf("mysql freeze unexpectedly degraded: %v", out.Degraded)
		}
	}

	// The source read_only must now be ON (the freeze primitive verified it).
	ro, _, err := mysqlReadOnly(context.Background(), pair.srcExec)
	if err != nil {
		t.Fatalf("read_only probe: %v", err)
	}
	if !strings.EqualFold(ro, "ON") {
		t.Fatalf("source read_only=%q, expected ON after freeze", ro)
	}

	// appuser (non-SUPER) must now be REJECTED on write — dual-writer closed.
	wo, wrc := appWrite()
	if wrc == 0 {
		t.Fatalf("appuser still able to write after freeze (dual-writer window open): %s", wo)
	}
	if !strings.Contains(wo, "read-only") {
		t.Fatalf("appuser write not rejected as read-only (rc=%d): %s", wrc, wo)
	}
}
