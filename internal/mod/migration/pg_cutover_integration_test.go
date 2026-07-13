//go:build integration

// Integration test for the Phase 2A PostgreSQL cutover primitives against a
// LIVE PostgreSQL primary + standby pair (Docker). It exercises the real
// ReplicationEngine primitives (Preflight / WaitForCatchUpPG / PromotePG) and
// the real FencedCutoverOrchestrator end-to-end — no fakes for the PG side.
//
// Traffic switching is faked (fakeTrafficSwitch): nginx is not present in the
// container pair and is already covered by unit tests with fakes. The
// integration value here is the unfakeable part: a real standby that must be
// PROVEN a standby before the switch, real replication lag draining to 0, and
// a real pg_promote() that flips the target to primary.
//
// Run: go test -tags integration ./internal/mod/migration/ -run PGIntegration -v
// Requires the `docker` CLI and the postgres:15 image (pulled on demand).

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

// ---------------------------------------------------------------------------
// docker-backed SSHExecuter
// ---------------------------------------------------------------------------

// dockerExecuter satisfies the migration.SSHExecuter interface by exec'ing
// commands inside a named container. The production primitives prefix PG
// commands with `sudo -u postgres`; the container runs postgres as the default
// user and has no sudo, so we strip that prefix.
type dockerExecuter struct {
	container string
}

func (d dockerExecuter) Exec(cmd string) (string, string, int, error) {
	return d.ExecContext(context.Background(), cmd)
}

func (d dockerExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	cmd = strings.ReplaceAll(cmd, "sudo -u postgres ", "")
	c := exec.CommandContext(ctx, "docker", "exec", "-u", "postgres", d.container, "bash", "-lc", cmd)
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

func (dockerExecuter) IsAlive() bool { return true }
func (dockerExecuter) Upload(io.Reader, string) error {
	return errors.New("dockerExecuter: Upload unsupported")
}
func (dockerExecuter) Download(string, io.Writer) error {
	return errors.New("dockerExecuter: Download unsupported")
}

// ---------------------------------------------------------------------------
// pair setup
// ---------------------------------------------------------------------------

type pgPair struct {
	srcExec, tgtExec dockerExecuter
	network          string
	srcC, tgtC       string
}

// setupPGPair builds a real primary (pg-src) + streaming standby (pg-tgt) and
// returns executers plus a cleanup func that tears the pair down.
func setupPGPair(t *testing.T) *pgPair {
	t.Helper()
	net := "pgcut-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)

	src := "pg-src-" + randSuffix()
	tgt := "pg-tgt-" + randSuffix()
	// Target starts with a no-op CMD so we can initialize it as a standby
	// ourselves (no postgres auto-start to fight).
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "pg-src",
		"-e", "POSTGRES_PASSWORD=secret", "-e", "POSTGRES_DB=appdb",
		"postgres:15")
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "pg-tgt",
		"-e", "POSTGRES_PASSWORD=secret",
		"postgres:15", "tail", "-f", "/dev/null")

	pair := &pgPair{
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

	waitReady(t, pair.srcExec, src)
	seedSource(t, pair.srcExec, src)
	waitReady(t, pair.srcExec, src) // ensure role + reload settled

	buildStandby(t, pair, src, tgt)
	waitReady(t, pair.tgtExec, tgt)
	waitStreaming(t, pair.srcExec)

	return pair
}

// seedSource creates the replicator role, opens pg_hba for replication, reloads,
// and writes a known data row so promotion can be verified end-to-end.
func seedSource(t *testing.T, src dockerExecuter, _ string) {
	t.Helper()
	runSQL(t, src, "CREATE ROLE replicator WITH REPLICATION PASSWORD 'rep' LOGIN;")
	runSQL(t, src, "CREATE TABLE IF NOT EXISTS cutover_probe (id int PRIMARY KEY, v text);")
	runSQL(t, src, "INSERT INTO cutover_probe VALUES (1, 'pre-cutover') ON CONFLICT DO NOTHING;")
	// Allow replication + a plain SELECT-1 probe from the replicator user.
	runSQL(t, src, "ALTER SYSTEM SET listen_addresses = '*';")
	appendHBA(t, src, "host replication replicator all trust")
	appendHBA(t, src, "host all replicator all trust")
	runSQL(t, src, "SELECT pg_reload_conf();")
}

// buildStandby wipes the target data dir and base-backups from the source,
// writing standby.signal + primary_conninfo, then starts it streaming.
func buildStandby(t *testing.T, pair *pgPair, src, tgt string) {
	t.Helper()
	// .pgpass so the wal receiver can authenticate to the primary.
	mustRun(t, "docker", "exec", tgt, "bash", "-lc",
		"find /var/lib/postgresql/data -mindepth 1 -delete && "+
			"echo 'pg-src:5432:replication:replicator:rep' > /var/lib/postgresql/.pgpass && "+
			"chmod 600 /var/lib/postgresql/.pgpass && "+
			"chown postgres:postgres /var/lib/postgresql/.pgpass")
	// pg_basebackup with -R writes standby.signal + primary_conninfo.
	baseOut, _, rc, _ := pair.tgtExec.ExecContext(context.Background(),
		"PGPASSWORD=rep pg_basebackup -h pg-src -p 5432 -U replicator "+
			"-D /var/lib/postgresql/data -Fp -Xs -P -R")
	if rc != 0 {
		t.Fatalf("pg_basebackup failed (rc=%d): %s", rc, baseOut)
	}
	ctlOut, _, rc, _ := pair.tgtExec.ExecContext(context.Background(),
		"chmod 700 /var/lib/postgresql/data && "+
			"/usr/lib/postgresql/15/bin/pg_ctl -D /var/lib/postgresql/data -l /tmp/standby.log -o \"-c listen_addresses='*'\" start")
	if rc != 0 {
		t.Fatalf("standby start failed (rc=%d): %s", rc, ctlOut)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("host cmd %v: %v\n%s", args, err, out)
	}
}

func runSQL(t *testing.T, e dockerExecuter, sql string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(),
		fmt.Sprintf("psql -v ON_ERROR_STOP=1 -tAc %s", shQuote(sql)))
	_ = err
	if rc != 0 {
		t.Fatalf("psql %q failed (rc=%d): %s", sql, rc, out)
	}
}

func appendHBA(t *testing.T, e dockerExecuter, line string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(),
		fmt.Sprintf("echo %s >> /var/lib/postgresql/data/pg_hba.conf", shQuote(line)))
	if rc != 0 {
		t.Fatalf("appendHBA %q failed (rc=%d): %s %v", line, rc, out, err)
	}
}

func waitReady(t *testing.T, e dockerExecuter, name string) {
	t.Helper()
	const consecutive = 2 // the image runs a transient init server then restarts
	good := 0
	for i := 0; i < 120; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "psql -tAc 'SELECT 1;'")
		if rc == 0 && strings.TrimSpace(out) == "1" {
			good++
			if good >= consecutive {
				return
			}
		} else {
			good = 0
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("postgres %s not ready", name)
}

// waitStreaming polls pg_stat_replication on the source until a standby row
// appears (the target is connected and streaming).
func waitStreaming(t *testing.T, src dockerExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := src.ExecContext(context.Background(),
			"psql -tAc \"SELECT count(*) FROM pg_stat_replication;\"")
		if rc == 0 && strings.TrimSpace(out) != "0" && strings.TrimSpace(out) != "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("target never appeared as a streaming standby on source")
}

func randSuffix() string {
	// No Date.now/Math.random in workflow scripts, but this is a normal test
	// binary — time is fine here.
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// shQuote returns s wrapped in single quotes, safely escaping any embedded
// single quotes using the bash '\” idiom (close, escaped quote, reopen). Use
// this for strings embedded in `bash -lc '...'` (e.g. SQL with its own quotes).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func pgReplConfig() ReplicationConfig {
	return ReplicationConfig{
		DatabaseType:    "postgres",
		SourceHost:      "pg-src",
		SourcePort:      5432,
		TargetHost:      "pg-tgt",
		TargetPort:      5432,
		ReplicationUser: "replicator",
		ReplicationPass: "rep",
		MigrationID:     1,
	}
}

// TestPGCutoverPrimitivesLive drives the real ReplicationEngine primitives
// against a live pair: preflight passes, lag drains to 0, promote flips the
// target to primary, and the seeded row is readable + writable on the target.
func TestPGCutoverPrimitivesLive(t *testing.T) {
	pair := setupPGPair(t)
	eng := NewReplicationEngine(pair.srcExec, pair.tgtExec, nil)
	cfg := pgReplConfig()
	ctx := context.Background()

	// Preflight: all gates green.
	res, err := eng.Preflight(ctx, cfg)
	if err != nil {
		t.Fatalf("preflight: %v (notes=%s)", err, res.Notes)
	}
	if res.SourceInRecovery {
		t.Fatalf("source must be primary (in recovery=false)")
	}
	if !res.TargetInRecovery {
		t.Fatalf("target must be a standby before cutover")
	}
	if !res.ReplicatorOK {
		t.Fatalf("replicator connectivity must hold")
	}

	// Write a row on the source, wait for it to replicate + lag to drain.
	runSQL(t, pair.srcExec, "INSERT INTO cutover_probe VALUES (2, 'replicated') ON CONFLICT DO NOTHING;")
	if err := eng.WaitForCatchUpPG(ctx, cfg, 1); err != nil {
		t.Fatalf("catch-up: %v", err)
	}

	// Promote the target.
	if err := eng.PromotePG(ctx, cfg); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Post-promote: target is primary and serves the replicated row.
	r, _, _, _ := pair.tgtExec.ExecContext(ctx,
		"psql -tAc \"SELECT v FROM cutover_probe WHERE id=2;\"")
	if strings.TrimSpace(r) != "replicated" {
		t.Fatalf("target missing replicated row after promote: got %q", strings.TrimSpace(r))
	}
	// Target now accepts writes (was read-only standby).
	out, _, rc, _ := pair.tgtExec.ExecContext(ctx,
		"psql -tAc \"INSERT INTO cutover_probe VALUES (3, 'writable') ON CONFLICT DO NOTHING; SELECT 'ok';\"")
	if rc != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("target not writable after promote (rc=%d): %s", rc, out)
	}
}

// TestPGPreflightFailsWhenTargetNotStandby injects a failure: the target is
// promoted out-of-band, then Preflight must fail the gate 3 (target not a
// standby) — proving the pre-switch safety boundary is real.
func TestPGPreflightFailsWhenTargetNotStandby(t *testing.T) {
	pair := setupPGPair(t)
	eng := NewReplicationEngine(pair.srcExec, pair.tgtExec, nil)
	cfg := pgReplConfig()
	ctx := context.Background()

	if err := eng.PromotePG(ctx, cfg); err != nil {
		t.Fatalf("baseline promote: %v", err)
	}
	res, err := eng.Preflight(ctx, cfg)
	if err == nil {
		t.Fatalf("preflight should FAIL when target is no longer a standby (got %+v)", res)
	}
	if res.TargetInRecovery {
		t.Fatalf("target incorrectly reported as standby after promote")
	}
	if !errors.Is(err, ErrPGPreflight) {
		t.Fatalf("expected ErrPGPreflight, got %v", err)
	}
}

// TestFencedCutoverWithRealPG runs the full 11-step orchestrator against a live
// pair with a real FencingAuthority (sqlite-backed) and a real ReplicationEngine.
// Only traffic switching is faked. Asserts the cutover reaches SubCompleted and
// the target is promoted to primary.
func TestFencedCutoverWithRealPG(t *testing.T) {
	pair := setupPGPair(t)
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
		"postgres", "planned"); err != nil {
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
		Holder:        "meshium-integration",
		Replication:    pgReplConfig(),
		TrafficRequest: nginxSwitchReq("http://verify/health"),
		MaxLagSeconds:  1,
		ObserveFor:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("orchestrator run: %v (out=%+v)", err, out)
	}
	if !out.Completed || out.FinalState != SubCompleted {
		t.Fatalf("cutover not completed: %+v", out)
	}
	// Target must now be primary.
	r, _, _, _ := pair.tgtExec.ExecContext(context.Background(),
		"psql -tAc 'SELECT pg_is_in_recovery();'")
	if strings.TrimSpace(r) != "f" {
		t.Fatalf("target not promoted after orchestrated cutover: recovery=%q", strings.TrimSpace(r))
	}
}

// TestPGSourceFreezeClosesWrites proves Phase 3A / B1: the fenced cutover now
// physically freezes the PostgreSQL source (default_transaction_read_only=on,
// verified) and an application (non-superuser) role can no longer write. The
// Superuser bypass is documented explicitly — the freeze targets app writes,
// not operators holding the lease.
//
// It drives the real orchestrator (real FencingAuthority + ReplicationEngine)
// against a live pair; traffic switching is faked. After the run, it asserts the
// source read_only GUC is on and that a non-superuser INSERT is rejected.
func TestPGSourceFreezeClosesWrites(t *testing.T) {
	pair := setupPGPair(t)

	// Create an application role that is NOT a superuser and grant it write
	// access to the probe table. This models a real app connection. The seed
	// table cutover_probe lives in the postgres superuser's default database
	// (runSQL uses no -d), so we target that DB. We connect appuser over TCP
	// with a trust rule (mirroring how seedSource opens the replicator).
	runSQL(t, pair.srcExec, "CREATE ROLE appuser LOGIN PASSWORD 'app' CONNECTION LIMIT 5;")
	runSQL(t, pair.srcExec, "GRANT CONNECT ON DATABASE postgres TO appuser;")
	runSQL(t, pair.srcExec, "GRANT USAGE ON SCHEMA public TO appuser;")
	runSQL(t, pair.srcExec, "GRANT SELECT, INSERT, UPDATE, DELETE ON cutover_probe TO appuser;")
	appendHBA(t, pair.srcExec, "host all appuser all trust")
	runSQL(t, pair.srcExec, "SELECT pg_reload_conf();")

	// Baseline: appuser CAN write before the cutover fence is applied.
	appWrite := func() (string, int) {
		out, _, rc, _ := pair.srcExec.ExecContext(context.Background(),
			"psql -h 127.0.0.1 -U appuser -d postgres -tAc " +
				shQuote("INSERT INTO cutover_probe VALUES (100, 'pre-freeze') ON CONFLICT DO NOTHING; SELECT 'ok';"))
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
		"postgres", "planned"); err != nil {
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
		Replication:    pgReplConfig(),
		TrafficRequest: nginxSwitchReq("http://verify/health"),
		MaxLagSeconds:  1,
		ObserveFor:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("orchestrator run: %v (out=%+v)", err, out)
	}
	if !out.Completed {
		t.Fatalf("cutover not completed: %+v", out)
	}

	// The source read_only GUC must now be ON (the freeze primitive verified it).
	guc, _, _, _ := pair.srcExec.ExecContext(context.Background(),
		"psql -tAc 'SHOW default_transaction_read_only;'")
	if strings.TrimSpace(guc) != "on" {
		t.Fatalf("source default_transaction_read_only=%q, expected on after freeze", strings.TrimSpace(guc))
	}

	// appuser (non-superuser) must now be REJECTED on write — the dual-writer
	// window is closed for application traffic.
	wo, wrc := appWrite()
	if wrc == 0 {
		t.Fatalf("appuser still able to write after freeze (dual-writer window open): %s", wo)
	}
	if !strings.Contains(wo, "cannot execute") && !strings.Contains(wo, "read-only") {
		t.Fatalf("appuser write not rejected as read-only (rc=%d): %s", wrc, wo)
	}
}
