//go:build integration

// Integration test for the Phase 4A Docker-Compose execution mode against a
// LIVE PostgreSQL primary + standby pair stood up with `docker compose`. It
// exercises the real ReplicationEngine primitives (preflight / WaitForCatchUp /
// PromotePG) and the real FencedCutoverOrchestrator through a composeExecuter
// (commands run via `docker compose exec -T <service>`), proving the Compose
// topology mode end-to-end:
//   - execution mode wrapping + compose service quoting,
//   - CertifyTopology records the compose mode + project + connectivity,
//   - the fenced cutover promotes the standby to primary,
//   - restart reconciliation re-validates the persisted topology evidence
//     (CertifyTopology re-run) before continuing.
//
// Run: go test -tags integration ./internal/mod/migration/ -run PGCompose -v
// Requires the `docker` CLI with compose v2 and the postgres:15 image.

package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// composeExecuter satisfies SSHExecuter by running commands inside a compose
// service container via `docker compose exec -T -u postgres <service>`. The
// production primitives prefix PG commands with `sudo -u postgres`; the
// compose service already runs postgres, so we strip that prefix like the
// container executer does.
type composeExecuter struct {
	project string
	service string
}

func (c composeExecuter) Exec(cmd string) (string, string, int, error) {
	return c.ExecContext(context.Background(), cmd)
}

func (c composeExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	cmd = strings.ReplaceAll(cmd, "sudo -u postgres ", "")
	// Run docker directly (no outer login shell) so the host's Proxmox motd
	// banner is never prepended to the container's output — mirror dockerExecuter.
	cargs := []string{"compose", "-p", c.project, "exec", "-T", "-u", "postgres", c.service, "bash", "-lc", cmd}
	out, err := exec.CommandContext(ctx, "docker", cargs...).CombinedOutput()
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

func (composeExecuter) IsAlive() bool { return true }
func (composeExecuter) Upload(io.Reader, string) error {
	return fmt.Errorf("composeExecuter: Upload unsupported")
}
func (composeExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("composeExecuter: Download unsupported")
}

// hostExecuter runs commands on the local machine (where docker / docker
// compose live). CertifyTopology's transport-tool probe must run here, not
// inside the PG container — the container has no docker available.
type hostExecuter struct{}

func (hostExecuter) Exec(cmd string) (string, string, int, error) {
	return hostExecuter{}.ExecContext(context.Background(), cmd)
}
func (hostExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	out, err := exec.CommandContext(ctx, "bash", "-lc", cmd).CombinedOutput()
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
func (hostExecuter) IsAlive() bool { return true }
func (hostExecuter) Upload(io.Reader, string) error {
	return fmt.Errorf("hostExecuter: Upload unsupported")
}
func (hostExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("hostExecuter: Download unsupported")
}

type pgComposePair struct {
	srcExec, tgtExec composeExecuter
	project          string
	dir              string
	srcSvc, tgtSvc   string
}

func setupPGComposePair(t *testing.T) *pgComposePair {
	t.Helper()
	project := "pgcomp-" + randSuffix()
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	// Target starts with a no-op CMD so we initialize it as a standby ourselves.
	composeYML := fmt.Sprintf(`services:
  %[1]s:
    image: postgres:15
    environment:
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: appdb
    networks: [pgnet]
  %[2]s:
    image: postgres:15
    command: ["tail", "-f", "/dev/null"]
    environment:
      POSTGRES_PASSWORD: secret
    networks: [pgnet]
networks:
  pgnet:
`, "pg-src", "pg-tgt")
	if err := os.WriteFile(composePath, []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	mustRun(t, "bash", "-lc", fmt.Sprintf("cd %s && docker compose -p %s up -d", shQuote(dir), project))

	pair := &pgComposePair{
		srcExec: composeExecuter{project: project, service: "pg-src"},
		tgtExec: composeExecuter{project: project, service: "pg-tgt"},
		project: project, dir: dir, srcSvc: "pg-src", tgtSvc: "pg-tgt",
	}
	t.Cleanup(func() {
		_ = exec.Command("bash", "-lc", fmt.Sprintf("cd %s && docker compose -p %s down -v --remove-orphans", shQuote(dir), project)).Run()
	})

	// Wait for source ready (reuse the container-mode helper semantics).
	waitReadyCompose(t, pair.srcExec, "pg-src")
	seedSourceCompose(t, pair.srcExec)
	waitReadyCompose(t, pair.srcExec, "pg-src")

	buildStandbyCompose(t, pair)
	waitReadyCompose(t, pair.tgtExec, "pg-tgt")
	waitStreamingCompose(t, pair.srcExec)
	return pair
}

// lastResultLine returns the last non-empty line of out with ANSI escapes
// stripped. The community-scripts Debian image prints a motd banner on every
// `bash -lc` login, so psql output is prefixed with banner lines; the actual
// result is the final line.
func lastResultLine(out string) string {
	noAnsi := strings.NewReplacer("\x1b[1m", "", "\x1b[m", "", "\x1b[33m", "", "\x1b[1;92m", "", "\x1b[92m", "").Replace(out)
	lines := strings.Split(noAnsi, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func waitReadyCompose(t *testing.T, e composeExecuter, name string) {
	t.Helper()
	var last string
	for i := 0; i < 120; i++ {
		out, _, rc, _ := e.ExecContext(context.Background(), "psql -tAc 'SELECT 1;'")
		last = fmt.Sprintf("rc=%d out=%q", rc, lastResultLine(out))
		if rc == 0 && lastResultLine(out) == "1" {
			// The postgres entrypoint reports "ready to accept connections"
			// during its initdb phase, then shuts down and restarts. Require a
			// second successful read after a settle so we don't seed into the
			// transient window.
			time.Sleep(2 * time.Second)
			out2, _, rc2, _ := e.ExecContext(context.Background(), "psql -tAc 'SELECT 1;'")
			if rc2 == 0 && lastResultLine(out2) == "1" {
				return
			}
			last = fmt.Sprintf("settled rc=%d out=%q", rc2, lastResultLine(out2))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("pg compose %s not ready (%s)", name, last)
}

func seedSourceCompose(t *testing.T, src composeExecuter) {
	t.Helper()
	runSQLCompose(t, src, "CREATE ROLE replicator WITH REPLICATION PASSWORD 'rep' LOGIN;")
	runSQLCompose(t, src, "CREATE TABLE IF NOT EXISTS cutover_probe (id int PRIMARY KEY, v text);")
	runSQLCompose(t, src, "INSERT INTO cutover_probe VALUES (1, 'pre-cutover') ON CONFLICT DO NOTHING;")
	runSQLCompose(t, src, "ALTER SYSTEM SET listen_addresses = '*';")
	appendHBACompose(t, src, "host replication replicator all trust")
	appendHBACompose(t, src, "host all replicator all trust")
	runSQLCompose(t, src, "SELECT pg_reload_conf();")
}

func buildStandbyCompose(t *testing.T, pair *pgComposePair) {
	t.Helper()
	tgt := pair.tgtExec
	mustRun(t, "bash", "-lc", fmt.Sprintf("docker compose -p %s exec -T %s bash -lc %s",
		pair.project, pair.tgtSvc,
		shQuote("find /var/lib/postgresql/data -mindepth 1 -delete && "+
			"echo 'pg-src:5432:replication:replicator:rep' > /var/lib/postgresql/.pgpass && "+
			"chmod 600 /var/lib/postgresql/.pgpass && "+
			"chown postgres:postgres /var/lib/postgresql/.pgpass")))
	baseOut, _, rc, _ := tgt.ExecContext(context.Background(),
		"PGPASSWORD=rep pg_basebackup -h pg-src -p 5432 -U replicator "+
			"-D /var/lib/postgresql/data -Fp -Xs -P -R")
	if rc != 0 {
		t.Fatalf("pg_basebackup failed (rc=%d): %s", rc, baseOut)
	}
	ctlOut, _, rc, _ := tgt.ExecContext(context.Background(),
		"chmod 700 /var/lib/postgresql/data && "+
			"/usr/lib/postgresql/15/bin/pg_ctl -D /var/lib/postgresql/data -l /tmp/standby.log -o \"-c listen_addresses='*'\" start")
	if rc != 0 {
		t.Fatalf("standby start failed (rc=%d): %s", rc, ctlOut)
	}
}

func waitStreamingCompose(t *testing.T, src composeExecuter) {
	t.Helper()
	for i := 0; i < 60; i++ {
		out, _, rc, _ := src.ExecContext(context.Background(),
			"psql -tAc \"SELECT count(*) FROM pg_stat_replication;\"")
		if rc == 0 && lastResultLine(out) != "0" && lastResultLine(out) != "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("compose target never appeared as a streaming standby")
}

func runSQLCompose(t *testing.T, e composeExecuter, sql string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(), fmt.Sprintf("psql -v ON_ERROR_STOP=1 -tAc %s", shQuote(sql)))
	if rc != 0 {
		t.Fatalf("psql %q failed (rc=%d): %s %v", sql, rc, out, err)
	}
}

// appendHBACompose prepends a rule to the top of pg_hba.conf. postgres:15.18
// appends a trailing `host all all all scram-sha-256` line, so trust rules for
// the replicator must be FIRST-match (above it), not appended — otherwise
// replication hits the scram line and fails with "no encryption".
func appendHBACompose(t *testing.T, e composeExecuter, line string) {
	t.Helper()
	out, _, rc, err := e.ExecContext(context.Background(),
		fmt.Sprintf("{ echo %s; cat /var/lib/postgresql/data/pg_hba.conf; } > /tmp/pg_hba.new && "+
			"mv /tmp/pg_hba.new /var/lib/postgresql/data/pg_hba.conf",
			shQuote(line)))
	if rc != 0 {
		t.Fatalf("appendHBA %q failed (rc=%d): %s %v", line, rc, out, err)
	}
}

// TestPGComposeCutoverLive proves the Docker-Compose execution mode end-to-end:
// topology certification, fenced preflight/catch-up/promote through the compose
// executer, and restart reconciliation via a re-validated topology evidence.
func TestPGComposeCutoverLive(t *testing.T) {
	pair := setupPGComposePair(t)
	cfg := ReplicationConfig{DatabaseType: "postgres", SourceHost: "pg-src", SourcePort: 5432}

	// 1) Certify the compose topology mode (persisted evidence, no mutation).
	//    The transport-tool probe runs on the HOST where docker compose lives,
	//    not inside the PG container.
	ev, err := CertifyTopology(context.Background(), hostExecuter{}, "compose", "pg-src", "pg-tgt", pair.project, "")
	if err != nil {
		t.Fatalf("CertifyTopology: %v", err)
	}
	if ev.Mode != ModeCompose || ev.ComposeProject != pair.project || ev.SupportStatus != "automatic" {
		t.Fatalf("compose evidence wrong: %+v", ev)
	}

	e := &ReplicationEngine{sourceSSH: pair.srcExec, targetSSH: pair.tgtExec}

	// 2) Preflight + catch-up on the live standby.
	r, err := e.preflightPostgresCutover(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("preflight: %v (notes=%q)", err, r.Notes)
	}
	if !r.TargetInRecovery || r.SourceInRecovery {
		t.Fatalf("role flags wrong: %+v", r)
	}
	if err := e.WaitForCatchUp(context.Background(), cfg, 0); err != nil {
		t.Fatalf("WaitForCatchUp: %v", err)
	}

	// 3) Promote the standby through the compose executer.
	if err := e.PromotePG(context.Background(), cfg); err != nil {
		t.Fatalf("PromotePG: %v", err)
	}
	out, _, rc, err := pair.tgtExec.ExecContext(context.Background(), "psql -tAc 'SELECT pg_is_in_recovery();'")
	if rc != 0 || err != nil {
		t.Fatalf("post-promote probe failed: %s %v", out, err)
	}
	if lastResultLine(out) != "f" {
		t.Fatalf("compose target did not become primary (pg_is_in_recovery=%s)", lastResultLine(out))
	}
	// Seeded data survived promotion.
	if k, _, _, _ := pair.tgtExec.ExecContext(context.Background(), "psql -tAc 'SELECT v FROM cutover_probe WHERE id=1;'"); !strings.Contains(k, "pre-cutover") {
		t.Fatalf("promoted compose target lost seeded data: %q", k)
	}

	// 4) Restart reconciliation: re-validate the persisted topology evidence
	//    with a fresh probe before "continuing" — must still certify.
	ev2, err := CertifyTopology(context.Background(), hostExecuter{}, string(ev.Mode), ev.SourceID, ev.TargetID, ev.ComposeProject, ev.BastionID)
	if err != nil || ev2.SupportStatus != "automatic" {
		t.Fatalf("restart reconciliation: topology re-certification failed: %v %+v", err, ev2)
	}
}
