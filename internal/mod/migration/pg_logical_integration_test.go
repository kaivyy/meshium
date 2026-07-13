//go:build integration

// Phase 4D — PostgreSQL LOGICAL replication (publication/subscription) proven
// against a LIVE pair of PostgreSQL 15 primaries. The source runs
// wal_level=logical; the target is an independent primary. It exercises the
// real 4D primitives — PreflightLogicalPG, SetupLogicalReplicationPG,
// WaitForLogicalCatchUpPG — end-to-end, and proves a row written on the source
// replicates to the target and that parity is reached and measurable (no
// assumed-zero lag).
//
// Same-host reliability note: physical cutover tests (pg_cutover_integration_test)
// use a standby; logical uses two primaries, so the source must be started with
// wal_level=logical (ALTER SYSTEM + restart) before the publication can be
// created. This file manages that itself.
//
// Run: go test -tags integration ./internal/mod/migration/ -run PgLogical -v

package migration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// setupPGLogicalPair builds two PostgreSQL 15 primaries (pg-src, pg-tgt) on a
// private docker network. The source is reconfigured to wal_level=logical and
// restarted; a replicator role + hba rule are added. Returns executers + names.
func setupPGLogicalPair(t *testing.T) (dockerExecuter, dockerExecuter) {
	t.Helper()
	net := "pglog-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)
	src := "pg-log-src-" + randSuffix()
	tgt := "pg-log-tgt-" + randSuffix()
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "pg-src",
		"-e", "POSTGRES_PASSWORD=secret", "-e", "POSTGRES_DB=appdb",
		"postgres:15")
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "pg-tgt",
		"-e", "POSTGRES_PASSWORD=secret", "-e", "POSTGRES_DB=appdb",
		"postgres:15")

	srcE := dockerExecuter{container: src}
	tgtE := dockerExecuter{container: tgt}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", src, tgt).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})

	waitReady(t, srcE, src)
	waitReady(t, tgtE, tgt)

	// Source: enable logical replication (requires restart) + replicator role.
	runSQL(t, srcE, "ALTER SYSTEM SET wal_level = logical;")
	mustRun(t, "docker", "restart", src)
	waitReady(t, srcE, src)
	runSQL(t, srcE, "CREATE ROLE replicator WITH REPLICATION PASSWORD 'rep' LOGIN;")
	runSQL(t, srcE, "ALTER SYSTEM SET listen_addresses = '*';")
	appendHBA(t, srcE, "host replication replicator all trust")
	appendHBA(t, srcE, "host all replicator all trust")
	runSQL(t, srcE, "SELECT pg_reload_conf();")

	// Seed a table on the source so the subscription has data to copy.
	runSQL(t, srcE, "CREATE TABLE app_probe (id int PRIMARY KEY, v text);")
	runSQL(t, srcE, "INSERT INTO app_probe VALUES (1, 'seed');")
	// Logical replication (FOR ALL TABLES) does NOT create the target schema —
	// the matching table must exist on the target before CREATE SUBSCRIPTION.
	// This models the operator/seeder having created the target schema first.
	runSQL(t, tgtE, "CREATE TABLE app_probe (id int PRIMARY KEY, v text);")
	// The walsender on the source checks the subscriber's `replicator` role for
	// SELECT on the published tables; grant it so the initial copy can read.
	runSQL(t, srcE, "GRANT SELECT ON ALL TABLES IN SCHEMA public TO replicator;")
	return srcE, tgtE
}

func TestPGLogicalReplicationLive(t *testing.T) {
	srcE, tgtE := setupPGLogicalPair(t)
	eng := NewReplicationEngine(srcE, tgtE, nil)
	cfg := ReplicationConfig{
		DatabaseType: "postgres",
		SourceHost: "pg-src", SourcePort: 5432,
		TargetHost: "pg-tgt", TargetPort: 5432,
		ReplicationUser: "replicator", ReplicationPass: "rep", MigrationID: 1,
	}
	ctx := context.Background()

	// Preflight: wal_level=logical gate + same-major + target primary must pass.
	// DatabaseName left empty → the publication/subscription operate in the
	// default `postgres` database (where the seed table lives) and the db-exists
	// gate is skipped (it is only an extra safety check when a DB is named).
	res, err := eng.PreflightLogicalPG(ctx, cfg)
	if err != nil {
		t.Fatalf("logical preflight: %v (notes=%s)", err, res.Notes)
	}
	if res.WalLevel != "logical" {
		t.Fatalf("wal_level must be logical, got %q", res.WalLevel)
	}
	if !res.ReplicatorOK {
		t.Fatalf("replicator connectivity must hold")
	}
	if res.TargetInRecovery {
		t.Fatalf("logical target must NOT be a standby")
	}

	// Setup publication (source) + subscription (target).
	if err := eng.SetupLogicalReplicationPG(ctx, cfg); err != nil {
		t.Fatalf("setup logical: %v", err)
	}

	// The publication must exist on the source.
	pubOut, _, _, _ := srcE.ExecContext(ctx,
		"psql -tAc \"SELECT count(*) FROM pg_publication WHERE pubname='meshium_pub';\"")
	if strings.TrimSpace(pubOut) != "1" {
		t.Fatalf("publication not created on source: %q", strings.TrimSpace(pubOut))
	}
	// The subscription must exist on the target.
	subOut, _, _, _ := tgtE.ExecContext(ctx,
		"psql -tAc \"SELECT count(*) FROM pg_subscription WHERE subname='meshium_sub';\"")
	if strings.TrimSpace(subOut) != "1" {
		t.Fatalf("subscription not created on target: %q", strings.TrimSpace(subOut))
	}

	// Write a row on the source; it must replicate to the target.
	runSQL(t, srcE, "INSERT INTO app_probe VALUES (2, 'replicated') ON CONFLICT DO NOTHING;")

	// Catch-up: wait for target row count to equal source for app_probe.
	if err := eng.WaitForLogicalCatchUpPG(ctx, cfg, []string{"app_probe"}, 60*time.Second); err != nil {
		t.Fatalf("logical catch-up: %v", err)
	}

	// The replicated row must now be readable on the target (default `postgres` db).
	r, _, rc, _ := tgtE.ExecContext(ctx,
		"psql -tAc \"SELECT v FROM app_probe WHERE id=2;\"")
	if rc != 0 || strings.TrimSpace(r) != "replicated" {
		t.Fatalf("target missing replicated row: rc=%d got %q", rc, strings.TrimSpace(r))
	}

	// Parity proven: seed + replicated = 2 rows on both sides.
	sc, _ := LogicalRowCount(ctx, srcE, "postgres", "app_probe")
	tc, _ := LogicalRowCount(ctx, tgtE, "postgres", "app_probe")
	if sc != 2 || tc != 2 {
		t.Fatalf("row-count parity broken: source=%d target=%d", sc, tc)
	}
}
