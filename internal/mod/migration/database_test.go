package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDatabaseMigratorCommandShapes asserts each engine's dump/restore/drop
// commands carry the idempotent flags and shell-quoting that make retry safe
// and prevent injection. Shape-only (no SSH) — the command builders are pure.
func TestDatabaseMigratorCommandShapes(t *testing.T) {
	creds := DBCredentials{Engine: "x", Username: "u", Password: "p'ass", Host: "h", Port: 1}

	cases := []struct {
		engine      string
		streaming   bool
		dumpWant    []string // substrings that must appear in the dump command
		restoreWant []string // substrings that must appear in the restore command
		dropWant    []string
	}{
		{"postgres", false,
			[]string{"pg_dump", "--clean", "--if-exists", "-Z 1"},
			[]string{"pg_restore", "--clean", "--if-exists"},
			[]string{"DROP DATABASE IF EXISTS"}},
		{"mysql", true,
			[]string{"mysqldump", "--single-transaction", "--add-drop-database"},
			[]string{"mysql"},
			[]string{"DROP DATABASE IF EXISTS"}},
		{"mongodb", true,
			[]string{"mongodump", "--archive", "--gzip"},
			[]string{"mongorestore", "--archive", "--gzip", "--drop"},
			[]string{"dropDatabase"}},
		{"redis", false,
			[]string{"redis-cli", "--rdb"},
			[]string{"CONFIG GET dir", "dump.rdb", "systemctl restart"},
			[]string{"FLUSHALL"}},
	}

	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			m, ok := getMigrator(tc.engine)
			if !ok {
				t.Fatalf("getMigrator: %s not registered", tc.engine)
			}
			if m.Streaming() != tc.streaming {
				t.Errorf("Streaming()=%v want %v", m.Streaming(), tc.streaming)
			}

			// File-path dump/restore (used by PG + Redis; MySQL/Mongo also define it).
			dump := m.DumpCommand(creds, "mydb", "/tmp/d.dump")
			for _, want := range tc.dumpWant {
				if !strings.Contains(dump, want) {
					t.Errorf("dump missing %q: %s", want, dump)
				}
			}
			restore := m.RestoreCommand(creds, "mydb", "/tmp/d.dump")
			for _, want := range tc.restoreWant {
				if !strings.Contains(restore, want) {
					t.Errorf("restore missing %q: %s", want, restore)
				}
			}
			drop := m.DropDatabaseCommand(creds, "mydb")
			for _, want := range tc.dropWant {
				if !strings.Contains(drop, want) {
					t.Errorf("drop missing %q: %s", want, drop)
				}
			}

			// Password with a single quote must be shell-quoted (never injected
			// raw) wherever it appears in dump/restore/drop.
			for name, cmd := range map[string]string{"dump": dump, "restore": restore, "drop": drop} {
				if strings.Contains(cmd, "p'ass") {
					t.Errorf("%s command leaked unquoted password: %s", name, cmd)
				}
			}
		})
	}
}

// TestDatabaseListExcludesSystemDBs verifies the catalog queries for PG and
// MySQL exclude system databases, via the actual command strings they emit.
func TestDatabaseListExcludesSystemDBs(t *testing.T) {
	ctx := context.Background()
	ssh := newMockSSH()

	pg, _ := getMigrator("postgres")
	// getMigrator returns (migrator, ok); ok ignored here — postgres is always registered.
	if _, err := pg.ListDatabases(ctx, ssh, DBCredentials{}); err != nil {
		t.Fatalf("pg list: %v", err)
	}
	pgCmd := ssh.commands[len(ssh.commands)-1]
	for _, sys := range []string{"template0", "template1", "'postgres'"} {
		if !strings.Contains(pgCmd, sys) {
			t.Errorf("pg list should exclude %s: %s", sys, pgCmd)
		}
	}

	ssh = newMockSSH()
	my, _ := getMigrator("mysql")
	if _, err := my.ListDatabases(ctx, ssh, DBCredentials{}); err != nil {
		t.Fatalf("mysql list: %v", err)
	}
	myCmd := ssh.commands[len(ssh.commands)-1]
	for _, sys := range []string{"'mysql'", "'sys'", "'information_schema'"} {
		if !strings.Contains(myCmd, sys) {
			t.Errorf("mysql list should exclude %s: %s", sys, myCmd)
		}
	}
}

// TestDatabaseCollectAbsentErrors: when the engine is absent on the source,
// Collect MUST fail clearly (Phase 5B P0-1). A silent "0 databases" success
// would falsely reassure the operator. A server without the engine selected is
// a hard planning error, not a no-op.
func TestDatabaseCollectAbsentErrors(t *testing.T) {
	ssh := newMockSSH() // no "yes" outputs → Detect returns false
	coll := &DatabaseCollector{Creds: &DatabaseConfig{Engine: "postgres"}, DatabaseName: ""}
	if _, err := coll.Collect(context.Background(), ssh); err == nil {
		t.Fatalf("collect on absent engine should error clearly, got nil")
	}
}

// TestDatabaseCollectNamedDBMissingErrors: a user-named database that the
// engine does not expose is a hard error, never a quiet drop.
func TestDatabaseCollectNamedDBMissingErrors(t *testing.T) {
	ssh := newMockSSH()
	// Detect → "yes"; ListDatabases returns only "appdb".
	ssh.addOutput("pgrep -x postgres", "yes")
	ssh.addOutput("psql", "appdb\t10\notherdb\t20\n")
	coll := &DatabaseCollector{Creds: &DatabaseConfig{Engine: "postgres", Username: "u", Password: "p"}, DatabaseName: "ghostdb"}
	if _, err := coll.Collect(context.Background(), ssh); err == nil {
		t.Fatalf("collect with missing named DB should error, got nil")
	}
}

// TestDatabaseCollectAllUserDBs: empty DatabaseName enumerates every user DB
// (system DBs excluded by the engine query) and is flagged AllUserDBs.
func TestDatabaseCollectAllUserDBs(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("pgrep -x postgres", "yes")
	ssh.addOutput("psql", "appdb\t10\notherdb\t20\n")
	coll := &DatabaseCollector{Creds: &DatabaseConfig{Engine: "postgres", Username: "u", Password: "p"}, DatabaseName: ""}
	data, err := coll.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("collect all user dbs: %v", err)
	}
	var cd DatabaseCollectData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cd.Databases) != 2 {
		t.Fatalf("want 2 user dbs, got %d: %+v", len(cd.Databases), cd.Databases)
	}
	if !cd.AllUserDBs {
		t.Errorf("AllUserDBs should be true when DatabaseName empty")
	}
}

// TestDockerWrap: commands run inside a container are prefixed with a
// docker-exec, args shell-quoted; empty container is a pass-through so the
// host-mode path is unchanged.
func TestDockerWrap(t *testing.T) {
	if got := dockerWrap("", "pg_dump -d x"); got != "pg_dump -d x" {
		t.Errorf("empty container should pass through, got %q", got)
	}
	if got := dockerWrap("db-1", "pg_dump -d x"); got != "docker exec -i 'db-1' -- pg_dump -d x" {
		t.Errorf("container should be prefixed, got %q", got)
	}
	// container names with spaces/specials must be quoted, not split
	if got := dockerWrap("my db", "redis-cli"); got != "docker exec -i 'my db' -- redis-cli" {
		t.Errorf("container name must be quoted, got %q", got)
	}
}

// TestContainerCredentials: when ExecMode=container, connections target the
// container's loopback + engine default port (host/port fields are meaningless
// inside a container), and the container/execMode flow into DBCredentials.
func TestContainerCredentials(t *testing.T) {
	in := &DatabaseConfig{Engine: "postgres", Username: "u", Password: "p", Host: "10.0.0.5", Port: 6543, ExecMode: "container", Container: "pg"}
	c := in.ToCredentials()
	if c.ExecMode != "container" || c.Container != "pg" {
		t.Errorf("execMode/container not propagated: mode=%q container=%q", c.ExecMode, c.Container)
	}
	if c.Host != "127.0.0.1" || c.Port != 5432 {
		t.Errorf("container should force loopback+default port, got %s:%d", c.Host, c.Port)
	}
	// compose mode also forces loopback
	compose := &DatabaseConfig{Engine: "postgres", Username: "u", Host: "10.0.0.5", Port: 6543, ExecMode: "compose", ComposeService: "pg", ComposeFile: "/opt/stack/docker-compose.yml"}
	cc := compose.ToCredentials()
	if cc.Host != "127.0.0.1" || cc.Port != 5432 || cc.ComposeService != "pg" {
		t.Errorf("compose should force loopback+default port, got %s:%d svc=%q", cc.Host, cc.Port, cc.ComposeService)
	}
	// host mode keeps user host/port
	hostMode := &DatabaseConfig{Engine: "mysql", Username: "u", Host: "10.0.0.5", Port: 3307}
	hc := hostMode.ToCredentials()
	if hc.ExecMode != "host" || hc.Container != "" || hc.Host != "10.0.0.5" || hc.Port != 3307 {
		t.Errorf("host mode should preserve host/port, got %s:%d mode=%q container=%q", hc.Host, hc.Port, hc.ExecMode, hc.Container)
	}
}

// TestContainerCommandPrefix: every engine's command builders wrap in the
// execution context (docker exec for container mode, docker compose exec for
// compose mode) when ExecMode is set — proving no engine silently skips a
// containerized/compose-managed DB.
func TestContainerCommandPrefix(t *testing.T) {
	pg := DBCredentials{Engine: "postgres", Username: "u", Password: "p", Host: "127.0.0.1", Port: 5432, ExecMode: "container", Container: "pg"}
	my := DBCredentials{Engine: "mysql", Username: "u", Password: "p", Host: "127.0.0.1", Port: 3306, ExecMode: "container", Container: "my"}
	mg := DBCredentials{Engine: "mongodb", Username: "u", Password: "p", Host: "127.0.0.1", Port: 27017, ExecMode: "container", Container: "mg"}
	rd := DBCredentials{Engine: "redis", Username: "u", Password: "p", Host: "127.0.0.1", Port: 6379, ExecMode: "container", Container: "rd"}
	cp := DBCredentials{Engine: "postgres", Username: "u", Password: "p", Host: "127.0.0.1", Port: 5432, ExecMode: "compose", ComposeService: "pg", ComposeFile: "/opt/stack/docker-compose.yml"}

	for _, c := range []DBCredentials{pg, my, mg, rd, cp} {
		var cmd string
		switch c.Engine {
		case "postgres":
			cmd = postgresMigrator{}.DumpCommand(c, "db", "/tmp/db.dump")
		case "mysql":
			cmd = mysqlMigrator{}.DumpCommand(c, "db", "/tmp/db.dump")
		case "mongodb":
			cmd = mongoMigrator{}.DumpCommand(c, "db", "/tmp/db.dump")
		case "redis":
			cmd = redisMigrator{}.DumpCommand(c, "db", "/tmp/db.rdb")
		}
		switch c.ExecMode {
		case "container":
			if !strings.HasPrefix(cmd, "docker exec -i ") {
				t.Errorf("%s: container command not prefixed: %q", c.Engine, cmd)
			}
		case "compose":
			if !strings.HasPrefix(cmd, "docker compose -f ") || !strings.Contains(cmd, "exec 'pg' -- ") {
				t.Errorf("%s: compose command not prefixed: %q", c.Engine, cmd)
			}
		default:
			if strings.HasPrefix(cmd, "docker") {
				t.Errorf("%s: host command should not be docker-prefixed: %q", c.Engine, cmd)
			}
		}
	}
}
