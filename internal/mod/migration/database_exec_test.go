package migration

import (
	"strings"
	"testing"
)

// Container/compose-mode commands were built as `docker exec -i <c> -- ENV=x tool`.
// Verified against a live daemon, that form is broken twice over:
//
//	$ docker exec -i <c> -- echo hi
//	OCI runtime exec failed: exec: "--": executable file not found in $PATH
//	$ docker exec -i <c> TESTVAR=1 echo hi
//	OCI runtime exec failed: exec: "TESTVAR=1": executable file not found
//
// docker exec treats `--` as the binary to run, and runs no shell, so env
// assignments (PGPASSWORD=…, MYSQL_PWD=…, REDISCLI_AUTH=…) and multi-statement
// scripts (redis restore's `set -e; …`) can never work. The working form is
// `docker exec -i <c> sh -c '<full command>'` — which the repo's own
// integration tests (caddy_switch, mysql_cutover) already use.
func TestContainerCommandsUseShWrapper(t *testing.T) {
	creds := map[string]DBCredentials{
		"postgres": {Engine: "postgres", Username: "u", Password: "p", Host: "127.0.0.1", Port: 5432, ExecMode: "container", Container: "pg"},
		"mysql":    {Engine: "mysql", Username: "u", Password: "p", Host: "127.0.0.1", Port: 3306, ExecMode: "container", Container: "my"},
		"mongodb":  {Engine: "mongodb", Username: "u", Password: "p", Host: "127.0.0.1", Port: 27017, ExecMode: "container", Container: "mg"},
		"redis":    {Engine: "redis", Username: "u", Password: "p", Host: "127.0.0.1", Port: 6379, ExecMode: "container", Container: "rd"},
	}

	for engine, c := range creds {
		m, ok := getMigrator(engine)
		if !ok {
			t.Fatalf("no migrator for %s", engine)
		}
		cmds := map[string]string{
			"dump":    m.DumpCommand(c, "appdb", "/tmp/appdb.dump"),
			"restore": m.RestoreCommand(c, "appdb", "/tmp/appdb.dump"),
			"drop":    m.DropDatabaseCommand(c, "appdb"),
		}
		if m.Streaming() {
			cmds["streamdump"] = m.StreamDumpCommand(c, "appdb")
			cmds["streamrestore"] = m.StreamRestoreCommand(c, "appdb")
		}
		for kind, cmd := range cmds {
			if cmd == "" {
				continue
			}
			if !strings.Contains(cmd, "docker exec") {
				t.Errorf("%s %s: container mode not wrapped: %q", engine, kind, cmd)
				continue
			}
			if strings.Contains(cmd, " -- ") {
				t.Errorf("%s %s: uses `docker exec ... --`, which docker treats as the binary: %q", engine, kind, cmd)
			}
			if !strings.Contains(cmd, "sh -c") {
				t.Errorf("%s %s: no shell inside the container — env assignments and pipes cannot work: %q", engine, kind, cmd)
			}
			if n := strings.Count(cmd, "docker exec"); n > 1 {
				t.Errorf("%s %s: docker exec prefix doubled (%d occurrences): %q", engine, kind, n, cmd)
			}
		}
	}
}

// The dump file must land on the HOST so SFTP can move it: the redirect /
// `cat` half of the command must sit OUTSIDE the docker exec wrapper.
func TestContainerDumpFileStaysOnHost(t *testing.T) {
	c := DBCredentials{Engine: "postgres", Username: "u", Password: "p", Host: "127.0.0.1", Port: 5432, ExecMode: "container", Container: "pg"}

	dump := postgresMigrator{}.DumpCommand(c, "appdb", "/tmp/appdb.dump")
	// The redirect must be the host-side tail of the command, after the sh -c
	// payload — `-f <path>` inside the wrapper would write into the container.
	if !strings.HasSuffix(dump, "> '/tmp/appdb.dump'") {
		t.Errorf("pg dump does not end in a host-side redirect: %q", dump)
	}
	if strings.Contains(dump, "-f ") {
		t.Errorf("pg dump writes via -f inside the container where SFTP cannot reach it: %q", dump)
	}

	restore := postgresMigrator{}.RestoreCommand(c, "appdb", "/tmp/appdb.dump")
	if !strings.Contains(restore, "cat '/tmp/appdb.dump' |") {
		t.Errorf("pg restore reads the dump inside the container where the upload never landed: %q", restore)
	}
}

// Host mode must be completely untouched by the wrapper.
func TestHostModeCommandsHaveNoDocker(t *testing.T) {
	c := DBCredentials{Engine: "mysql", Username: "u", Password: "p", Host: "127.0.0.1", Port: 3306}
	for _, cmd := range []string{
		mysqlMigrator{}.DumpCommand(c, "appdb", "/tmp/a.gz"),
		mysqlMigrator{}.RestoreCommand(c, "appdb", "/tmp/a.gz"),
		mysqlMigrator{}.StreamDumpCommand(c, "appdb"),
	} {
		if strings.Contains(cmd, "docker") {
			t.Errorf("host-mode command docker-wrapped: %q", cmd)
		}
	}
}

// The MySQL catalog query summed table sizes with no IFNULL: a schema holding
// only views yields SUM(...) = NULL, `mysql -N -B` prints the literal string
// "NULL", ParseInt fails, and parseCatalog silently drops the schema — so a
// view-only database vanished from the migration with no warning. (A schema
// with zero tables was likewise absent, because it has no rows in
// information_schema.tables at all.)
func TestMySQLCatalogQueryHandlesViewOnlySchemas(t *testing.T) {
	c := DBCredentials{Engine: "mysql", Username: "u", Password: "p", Host: "127.0.0.1", Port: 3306}
	// Reconstruct the query from the command the migrator actually issues.
	cmd := mysqlListQuery(c)
	if !strings.Contains(cmd, "IFNULL") {
		t.Errorf("catalog query lets NULL sizes through, dropping view-only schemas: %s", cmd)
	}
	if !strings.Contains(cmd, "LEFT JOIN") {
		t.Errorf("catalog query misses schemas with zero tables: %s", cmd)
	}
}

// parseCatalog must skip a literal NULL rather than storing garbage — the
// query fix makes this unreachable, but old-shaped output must stay safe.
func TestParseCatalogSkipsNullSize(t *testing.T) {
	entries, err := parseCatalog("views_only\tNULL\nrealdb\t42\n", "\t")
	if err != nil {
		t.Fatalf("parseCatalog: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "realdb" {
		t.Errorf("entries = %v, want just realdb", entries)
	}
}

// The mongo listing eval must emit plain name<TAB>MiB lines. The old parser
// (parseMongoCatalog) required double-quoted JSON keys, which only the legacy
// `mongo` shell prints — mongosh (MongoDB 5+) emits Node inspect format with
// unquoted keys and Long('…') wrappers, so on any modern host the parse
// produced zero entries and collect failed with a misleading "no user
// databases enumerated".
func TestMongoListEvalEmitsTabSeparatedLines(t *testing.T) {
	c := DBCredentials{Engine: "mongodb", Username: "u", Password: "p", Host: "127.0.0.1", Port: 27017}
	eval := mongoListEval()
	for _, want := range []string{`\t`, "listDatabases", "map"} {
		if !strings.Contains(eval, want) {
			t.Errorf("mongo list eval missing %q: %s", want, eval)
		}
	}
	_ = c
}
