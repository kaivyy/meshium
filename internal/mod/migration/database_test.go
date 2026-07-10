package migration

import (
	"context"
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

// TestDatabaseCollectAbsentNotError: when the engine is absent on the source,
// Collect returns empty metadata (not an error) — a server without that DB
// engine is a no-op, not a failed plan.
func TestDatabaseCollectAbsentNotError(t *testing.T) {
	ssh := newMockSSH() // no "yes" outputs → Detect returns false
	coll := &DatabaseCollector{Creds: &DatabaseConfig{Engine: "postgres"}, DatabaseName: ""}
	data, err := coll.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("collect on absent engine should not error: %v", err)
	}
	if data.Type != "database" {
		t.Fatalf("collect should return database CategoryData, got %+v", data)
	}
}
