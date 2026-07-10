package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

// DatabaseMigrator is the per-engine adapter for dumping and restoring a single
// database. Phase 1 is dump/restore only — replication and cutover hooks live in
// ReplicationEngine (Phase 2). A new engine implements this interface and
// registers it in databaseMigrators below.
//
// Engines split into two transfer shapes, declared by Streaming():
//   - Streaming() == true  : StreamDumpCommand writes the dump to stdout and
//                            StreamRestoreCommand reads it from stdin, so the
//                            source dump pipes straight into the target restore
//                            (StreamExecuter/WriteExecuter) with no temp file.
//                            (MySQL, MongoDB)
//   - Streaming() == false : DumpCommand writes a file on the source and
//                            RestoreCommand reads a file on the target; the
//                            file is moved source→meshium→target via SFTP.
//                            (PostgreSQL — pg_restore needs a file; Redis — RDB
//                            replace + restart)
type DatabaseMigrator interface {
	// Engine returns the canonical engine name ("postgres"|"mysql"|"mongodb"|"redis").
	Engine() string

	// Detect reports whether this engine is running on the host.
	Detect(ctx context.Context, ssh SSHExecuter) bool

	// ListDatabases returns user databases + approximate size in MiB. System
	// databases (template0/1, postgres, mysql, sys, information_schema,
	// performance_schema, admin/local/config) are excluded.
	ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error)

	// Streaming reports whether the engine can stream dump→restore over a pipe
	// (true) or must go through a file (false).
	Streaming() bool

	// StreamDumpCommand returns the command that writes a dump to stdout.
	StreamDumpCommand(c DBCredentials, db string) string

	// StreamRestoreCommand returns the command that reads a dump from stdin and
	// restores it. Must be idempotent (drop/clean) so a retry does not duplicate.
	StreamRestoreCommand(c DBCredentials, db string) string

	// DumpCommand returns the command that writes a dump to remoteDumpPath.
	DumpCommand(c DBCredentials, db, remoteDumpPath string) string

	// RestoreCommand returns the command that restores from remoteDumpPath.
	// Must be idempotent.
	RestoreCommand(c DBCredentials, db, remoteDumpPath string) string

	// DropDatabaseCommand returns a command to drop the named database (rollback).
	DropDatabaseCommand(c DBCredentials, db string) string
}

// DBCredentials holds the connection details for one database engine. The
// Password is plaintext in memory and encrypted at rest (see
// pipeline_handler.go / pipeline.go Execute). Host is usually "localhost"
// because the dump runs over SSH on the server itself.
type DBCredentials struct {
	Engine   string `json:"engine"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// DatabaseConfig is the user-supplied plan/execute config carried on
// PlanRequest and MigrationConfig. DatabaseName empty means "all user DBs".
type DatabaseConfig struct {
	Engine      string `json:"engine"`
	DatabaseName string `json:"databaseName,omitempty"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"` // encrypted at rest
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
}

// ToCredentials builds in-memory creds with a localhost default.
func (d *DatabaseConfig) ToCredentials() DBCredentials {
	host := d.Host
	if host == "" {
		host = "localhost"
	}
	port := d.Port
	if port == 0 {
		port = defaultPort(d.Engine)
	}
	return DBCredentials{
		Engine:   d.Engine,
		Username: d.Username,
		Password: d.Password,
		Host:     host,
		Port:     port,
	}
}

// DBCatalogEntry is one database name + approximate size, stored as collect
// metadata (never row data).
type DBCatalogEntry struct {
	Name   string `json:"name"`
	SizeMB int64  `json:"sizeMb"`
}

// databaseMigrators registers every supported engine. Add an engine here.
var databaseMigrators = map[string]DatabaseMigrator{
	"postgres": &postgresMigrator{},
	"mysql":    &mysqlMigrator{},
	"mongodb":  &mongoMigrator{},
	"redis":    &redisMigrator{},
}

func getMigrator(engine string) (DatabaseMigrator, bool) {
	m, ok := databaseMigrators[strings.ToLower(strings.TrimSpace(engine))]
	return m, ok
}

func defaultPort(engine string) int {
	switch strings.ToLower(engine) {
	case "postgres", "postgresql":
		return 5432
	case "mysql", "mariadb":
		return 3306
	case "mongodb":
		return 27017
	case "redis":
		return 6379
	}
	return 0
}

// pgEnv builds a PGPASSWORD env prefix so pg_dump/pg_restore don't need a TTY
// for the password. The password is shell-quoted to prevent injection.
func pgEnv(c DBCredentials) string {
	return fmt.Sprintf("PGPASSWORD=%s", shared.ShellQuote(c.Password))
}

// pgConnArgs are the -U/-h/-p flags shared by pg_dump/pg_restore.
func pgConnArgs(c DBCredentials) string {
	return fmt.Sprintf("-U %s -h %s -p %d", shared.ShellQuote(c.Username), shared.ShellQuote(c.Host), c.Port)
}

// mysqlEnv builds the MYSQL_PWD env prefix (avoids exposing the password in the
// process list via -p<pass>).
func mysqlEnv(c DBCredentials) string {
	return fmt.Sprintf("MYSQL_PWD=%s", shared.ShellQuote(c.Password))
}

func mysqlConnArgs(c DBCredentials) string {
	return fmt.Sprintf("-u %s -h %s -P %d", shared.ShellQuote(c.Username), shared.ShellQuote(c.Host), c.Port)
}

// mongoConnArgs returns the --uri for mongodump/mongorestore. Credentials are
// URL-escaped into the URI so they don't leak via the process list.
func mongoConnArgs(c DBCredentials) string {
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d",
		mongoEscape(c.Username), mongoEscape(c.Password), c.Host, c.Port)
	return fmt.Sprintf("--uri %s", shared.ShellQuote(uri))
}

// mongoEscape percent-escapes a credential for a MongoDB URI.
func mongoEscape(s string) string {
	r := strings.NewReplacer(
		"%", "%25", " ", "%20", "@", "%40", ":", "%3A", "/", "%2F",
	)
	return r.Replace(s)
}

// --- PostgreSQL ------------------------------------------------------------

type postgresMigrator struct{}

func (postgresMigrator) Engine() string { return "postgres" }

func (postgresMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "pgrep -x postgres >/dev/null 2>&1 && echo yes")
	return strings.TrimSpace(out) == "yes"
}

func (postgresMigrator) Streaming() bool { return false }

func (postgresMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// datname + size in MiB, excluding system DBs. Output: "name<TAB>size" lines.
	q := "SELECT datname, pg_database_size(datname)/1048576 FROM pg_database " +
		"WHERE datname NOT IN ('template0','template1','postgres') ORDER BY datname;"
	cmd := fmt.Sprintf("%s psql %s -t -A -F '\t' -c %s 2>/dev/null",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(q))
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("postgres list databases: %v", err)
	}
	return parseCatalog(out, "\t")
}

func (postgresMigrator) DumpCommand(c DBCredentials, db, path string) string {
	// -Fc custom format + -Z 1 gzip; --clean --if-exists makes restore idempotent.
	return fmt.Sprintf("%s pg_dump %s -Fc --no-owner --clean --if-exists -Z 1 -f %s %s 2>/dev/null",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(path), shared.ShellQuote(db))
}

func (postgresMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	return fmt.Sprintf("%s pg_restore %s --no-owner --clean --if-exists -d %s %s 2>&1",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(db), shared.ShellQuote(path))
}

func (postgresMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// PG custom format can't stream to a restore stdin cleanly; Streaming()==false.
	return ""
}

func (postgresMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	return ""
}

func (postgresMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	q := fmt.Sprintf("DROP DATABASE IF EXISTS %s;", pqIdent(db))
	return fmt.Sprintf("%s psql %s -c %s 2>/dev/null", pgEnv(c), pgConnArgs(c), shared.ShellQuote(q))
}

// pqIdent double-quotes a Postgres identifier with embedded quotes doubled.
func pqIdent(name string) string {
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

// --- MySQL / MariaDB -------------------------------------------------------

type mysqlMigrator struct{}

func (mysqlMigrator) Engine() string { return "mysql" }

func (mysqlMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1; echo yes")
	// pgrep returns non-zero if nothing matched; the trailing echo confirms exec ran.
	return strings.Contains(out, "yes")
}

func (mysqlMigrator) Streaming() bool { return true }

func (mysqlMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	q := "SELECT table_schema, ROUND(SUM(data_length+index_length)/1048576) " +
		"FROM information_schema.tables WHERE table_schema " +
		"NOT IN ('mysql','sys','information_schema','performance_schema') " +
		"GROUP BY table_schema ORDER BY table_schema;"
	cmd := fmt.Sprintf("%s mysql %s -N -B -e %s 2>/dev/null",
		mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(q))
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("mysql list databases: %v", err)
	}
	return parseCatalog(out, "\t")
}

func (mysqlMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// --single-transaction for a consistent snapshot without locking;
	// --add-drop-database makes the restore idempotent. Output to stdout.
	return fmt.Sprintf("%s mysqldump %s --single-transaction --routines --triggers --add-drop-database --databases %s 2>/dev/null",
		mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(db))
}

func (mysqlMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	// mysql reads the dump from stdin. The dump already contains
	// CREATE/DROP DATABASE statements (--add-drop-database), so db here is the
	// connection target, not a filter.
	return fmt.Sprintf("%s mysql %s 2>&1", mysqlEnv(c), mysqlConnArgs(c))
}

func (mysqlMigrator) DumpCommand(c DBCredentials, db, path string) string {
	return mysqlMigrator{}.StreamDumpCommand(c, db) + " | gzip > " + shared.ShellQuote(path)
}

func (mysqlMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	return fmt.Sprintf("gunzip -c %s | %s 2>&1", shared.ShellQuote(path), mysqlMigrator{}.StreamRestoreCommand(c, db))
}

func (mysqlMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	q := fmt.Sprintf("DROP DATABASE IF EXISTS %s;", backtickIdent(db))
	return fmt.Sprintf("%s mysql %s -e %s 2>/dev/null", mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(q))
}

// backtickIdent wraps a MySQL identifier in backticks with embedded backticks
// doubled.
func backtickIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// --- MongoDB ---------------------------------------------------------------

type mongoMigrator struct{}

func (mongoMigrator) Engine() string { return "mongodb" }

func (mongoMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "pgrep -x mongod >/dev/null 2>&1 && echo yes")
	return strings.TrimSpace(out) == "yes"
}

func (mongoMigrator) Streaming() bool { return true }

func (mongoMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// listDatabases via the mongo shell; parseMongoCatalog pulls name +
	// sizeOnDisk (bytes→MiB) and excludes admin/config/local.
	shell := "db.adminCommand({listDatabases:1})"
	listCmd := fmt.Sprintf("mongo %s --quiet --eval %s 2>/dev/null", mongoConnArgs(c), shared.ShellQuote(shell))
	out, _, exit, err := ssh.ExecContext(ctx, listCmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("mongo list databases: %v", err)
	}
	return parseMongoCatalog(out)
}

func (mongoMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// --archive streams a single archive to stdout; --gzip compresses in flight.
	return fmt.Sprintf("mongodump %s --db %s --archive --gzip 2>/dev/null",
		mongoConnArgs(c), shared.ShellQuote(db))
}

func (mongoMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	// --drop makes restore idempotent. --nsInclude targets the streamed db.
	return fmt.Sprintf("mongorestore %s --archive --gzip --drop --nsInclude %s.* 2>&1",
		mongoConnArgs(c), shared.ShellQuote(db))
}

func (mongoMigrator) DumpCommand(c DBCredentials, db, path string) string {
	return mongoMigrator{}.StreamDumpCommand(c, db) + " > " + shared.ShellQuote(path)
}

func (mongoMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	return fmt.Sprintf("mongorestore %s --archive %s --gzip --drop --nsInclude %s.* 2>&1",
		mongoConnArgs(c), shared.ShellQuote(path), shared.ShellQuote(db))
}

func (mongoMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	// Drop via the shell; db name is the eval target.
	drop := fmt.Sprintf("db.getSiblingDB(%s).dropDatabase()", shared.ShellQuote(db))
	return fmt.Sprintf("mongo %s --quiet --eval %s 2>/dev/null", mongoConnArgs(c), shared.ShellQuote(drop))
}

// --- Redis -----------------------------------------------------------------

type redisMigrator struct{}

func (redisMigrator) Engine() string { return "redis" }

func (redisMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "pgrep -x redis-server >/dev/null 2>&1 && echo yes")
	return strings.TrimSpace(out) == "yes"
}

func (redisMigrator) Streaming() bool { return false }

func (redisMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// Redis has one logical DB namespace; report DBSIZE as a rough "size" (key
	// count, not MiB — Redis exposes no per-DB size without SCAN+DEBUG).
	cmd := fmt.Sprintf("redis-cli -h %s -p %d %s DBSIZE 2>/dev/null",
		shared.ShellQuote(c.Host), c.Port, redisAuth(c))
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("redis dbsize: %v", err)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return []DBCatalogEntry{{Name: "redis", SizeMB: n}}, nil
}

func (redisMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// Redis can't restore from a stdin pipe; Streaming()==false. The RDB is
	// written to a file via DumpCommand and restored by replacing dump.rdb.
	return ""
}

func (redisMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	return ""
}

func (redisMigrator) DumpCommand(c DBCredentials, db, path string) string {
	// redis-cli --rdb streams the RDB to stdout; redirect to the remote file.
	return fmt.Sprintf("redis-cli -h %s -p %d %s --rdb %s 2>/dev/null",
		shared.ShellQuote(c.Host), c.Port, redisAuth(c), shared.ShellQuote(path))
}

func (redisMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// Restore = copy the RDB into the Redis data dir and restart so it loads.
	// This is the standard Redis RDB restore path; there is no online restore.
	return fmt.Sprintf(
		"rdir=$(redis-cli -h %s -p %d %s CONFIG GET dir 2>/dev/null | tail -1); "+
			"[ -n \"$rdir\" ] || rdir=/var/lib/redis; "+
			"cp %s \"$rdir/dump.rdb\" && "+
			"redis-cli -h %s -p %d %s SHUTDOWN NOSAVE 2>/dev/null; "+
			"systemctl restart redis redis-server 2>/dev/null || service redis-server restart 2>/dev/null || true",
		shared.ShellQuote(c.Host), c.Port, redisAuth(c),
		shared.ShellQuote(path),
		shared.ShellQuote(c.Host), c.Port, redisAuth(c))
}

func (redisMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	// FLUSHALL empties the dataset (rollback = undo the restore).
	return fmt.Sprintf("redis-cli -h %s -p %d %s FLUSHALL 2>/dev/null",
		shared.ShellQuote(c.Host), c.Port, redisAuth(c))
}

// redisAuth returns the -a flag with the password, or empty if none. The
// password is shell-quoted.
func redisAuth(c DBCredentials) string {
	if c.Password == "" {
		return ""
	}
	return fmt.Sprintf("-a %s --no-auth-warning", shared.ShellQuote(c.Password))
}

// --- shared helpers --------------------------------------------------------

// parseCatalog parses "name<sep>size" lines into entries. Blank/short lines and
// non-numeric sizes are skipped.
func parseCatalog(out, sep string) ([]DBCatalogEntry, error) {
	var entries []DBCatalogEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || name == "" {
			continue
		}
		entries = append(entries, DBCatalogEntry{Name: name, SizeMB: size})
	}
	return entries, nil
}

// parseMongoCatalog extracts database names + sizes (MiB) from a
// listDatabases JSON-ish response. Mongo's JS shell output is loose JSON, so we
// scan for "name": and "sizeOnDisk": pairs defensively rather than strict-decode.
func parseMongoCatalog(out string) ([]DBCatalogEntry, error) {
	var entries []DBCatalogEntry
	// Naive scan: each db block has "name" then "sizeOnDisk".
	lines := strings.Split(out, "\n")
	var lastName string
	for _, ln := range lines {
		if n := extractJSONString(ln, "name"); n != "" {
			lastName = n
		}
		if s := extractJSONNumber(ln, "sizeOnDisk"); s != "" && lastName != "" {
			size, _ := strconv.ParseInt(s, 10, 64)
			size /= 1048576
			if lastName != "admin" && lastName != "local" && lastName != "config" {
				entries = append(entries, DBCatalogEntry{Name: lastName, SizeMB: size})
			}
			lastName = ""
		}
	}
	return entries, nil
}

func extractJSONString(line, key string) string {
	idx := strings.Index(line, "\""+key+"\"")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key)+2:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if !strings.HasPrefix(rest, "\"") {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func extractJSONNumber(line, key string) string {
	idx := strings.Index(line, "\""+key+"\"")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key)+2:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	var num strings.Builder
	for _, r := range rest {
		if (r >= '0' && r <= '9') || r == '-' {
			num.WriteRune(r)
		} else {
			break
		}
	}
	return num.String()
}
