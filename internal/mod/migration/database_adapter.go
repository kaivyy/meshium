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

	// SupportsLiveReplication reports whether a genuine, end-to-end live
	// replication path exists for this engine (seed + catch-up + verified lag +
	// fencing + traffic switch). It is intentionally conservative: today no
	// engine has a fully wired live path, so it returns false and the planner
	// honestly downgrades live_replication requests to snapshot_copy. Flip this
	// per-engine only when the replication chain is real and tested (Phase 5B
	// P3).
	SupportsLiveReplication() bool

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
//
// Container is the name (or id) of the Docker container running the engine when
// the database is NOT reachable on the host network (P1-1). When non-empty, every
// engine command is wrapped in `docker exec -i <Container> -- …` so the dump/
// restore/list/drop run inside the container instead of on the host. Empty means
// run on the host directly. All values are shell-quoted by the wrap helper.
type DBCredentials struct {
	Engine        string `json:"engine"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Container     string `json:"container,omitempty"`      // Docker container running the engine (ExecMode=container)
	ExecMode      string `json:"execMode,omitempty"`       // host | container | compose
	ComposeService string `json:"composeService,omitempty"` // compose service name (ExecMode=compose)
	ComposeFile   string `json:"composeFile,omitempty"`    // docker-compose.yml path (ExecMode=compose)
}

// dockerWrap runs cmd inside the named container via `docker exec -i`. The
// container name is shell-quoted; cmd is emitted verbatim because it is already
// fully quoted by the engine command builders (pgEnv/pgConnArgs/…). When
// container is empty the command is returned unchanged (host-mode path).
func dockerWrap(container, cmd string) string {
	if container == "" {
		return cmd
	}
	return fmt.Sprintf("docker exec -i %s sh -c %s", shared.ShellQuote(container), shared.ShellQuote(cmd))
}

// wrapExec runs cmd in the engine's execution context (host / container /
// compose). Host mode returns cmd unchanged.
//
// Container/compose mode wraps the WHOLE command in `sh -c '<cmd>'`. The old
// prefix form `docker exec -i <c> -- ENV=x tool` was broken two ways, verified
// against a live daemon: docker exec treats `--` as the binary to run
// (`exec: "--": executable file not found`), and it runs no shell, so env
// assignments and multi-statement scripts can never execute. Only a shell
// inside the container can interpret them.
//
// Callers that combine a container command with host-side file plumbing
// (`> path`, `cat path |`, `| gzip > path`) must keep that plumbing OUTSIDE
// the wrapper so the dump file lands on the host where SFTP can reach it.
func wrapExec(c DBCredentials, cmd string) string {
	switch c.ExecMode {
	case "container":
		if c.Container == "" {
			return cmd // misconfigured: caller should have failed earlier
		}
		return fmt.Sprintf("docker exec -i %s sh -c %s", shared.ShellQuote(c.Container), shared.ShellQuote(cmd))
	case "compose":
		svc := c.ComposeService
		if svc == "" {
			return cmd
		}
		fileArg := ""
		if c.ComposeFile != "" {
			fileArg = fmt.Sprintf("-f %s ", shared.ShellQuote(c.ComposeFile))
		}
		// -T: no TTY — these run over SSH exec, and stdin must stay a pipe.
		return fmt.Sprintf("docker compose %sexec -T %s sh -c %s", fileArg, shared.ShellQuote(svc), shared.ShellQuote(cmd))
	default: // host
		return cmd
	}
}

// composeRunWrap is unused at execution time (migrations exec into a running
// service, not `run` a new container) but documents the intended boundary.

// resolveExecMode normalizes the empty/missing ExecMode to "host".
func resolveExecMode(m string) string {
	if m == "" {
		return "host"
	}
	return m
}

// DatabaseConfig is the user-supplied plan/execute config carried on
// PlanRequest and MigrationConfig. DatabaseName empty means "all user DBs".
//
// MigrationMode selects how the DB is moved. SnapshotCopy is dump→transfer→
// restore (honest downtime = transfer time). LiveReplication is only valid when
// the engine + topology + privileges + target prep genuinely support it; the
// planner disables it (and records why) otherwise. We never claim
// live_replication for a path that is not wired end-to-end.
//
// ExecMode selects WHERE the engine runs: on the host directly, inside a Docker
// container, or via a compose service. When ExecMode != host, ContainerName /
// ComposeService / ComposeFile identify the execution context and every engine
// command is wrapped to run there (docker exec / docker compose exec).
type DatabaseConfig struct {
	Engine        string `json:"engine"`
	MigrationMode string `json:"migrationMode,omitempty"` // snapshot_copy | live_replication (empty defaults to snapshot_copy)
	ExecMode      string `json:"execMode,omitempty"`      // host | container | compose (empty defaults to host)
	DatabaseName  string `json:"databaseName,omitempty"`  // empty = all user DBs
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"` // encrypted at rest
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Container     string `json:"container,omitempty"`    // Docker container name/id running the engine (ExecMode=container)
	ComposeService string `json:"composeService,omitempty"` // compose service name (ExecMode=compose)
	ComposeFile   string `json:"composeFile,omitempty"`    // path to docker-compose.yml (ExecMode=compose)
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
	execMode := resolveExecMode(d.ExecMode)
	container := d.Container
	// For container/compose execution the engine is reached on the container's
	// own loopback, so reset host/port to the engine default regardless of the
	// user-supplied host (which is usually empty for in-container engines).
	if execMode != "host" {
		host = "127.0.0.1"
		port = defaultPort(d.Engine)
	}
	return DBCredentials{
		Engine:        d.Engine,
		Username:      d.Username,
		Password:      d.Password,
		Host:          host,
		Port:          port,
		Container:     container,
		ExecMode:      execMode,
		ComposeService: d.ComposeService,
		ComposeFile:   d.ComposeFile,
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

func (postgresMigrator) SupportsLiveReplication() bool { return false }

func (postgresMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// datname + size in MiB, excluding system DBs. Output: "name<TAB>size" lines.
	q := "SELECT datname, pg_database_size(datname)/1048576 FROM pg_database " +
		"WHERE datname NOT IN ('template0','template1','postgres') ORDER BY datname;"
	cmd := wrapExec(c, fmt.Sprintf("%s psql %s -t -A -F '\t' -c %s 2>/dev/null",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(q)))
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("postgres list databases: %v", err)
	}
	return parseCatalog(out, "\t")
}

func (postgresMigrator) DumpCommand(c DBCredentials, db, path string) string {
	// -Fc custom format + -Z 1 gzip; --clean --if-exists makes restore
	// idempotent. The dump goes to stdout with the redirect OUTSIDE wrapExec:
	// with `-f <path>` in container mode the file landed inside the container,
	// where the SFTP download (which reads host paths) could never find it.
	return wrapExec(c, fmt.Sprintf("%s pg_dump %s -Fc --no-owner --clean --if-exists -Z 1 %s 2>/dev/null",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(db))) + " > " + shared.ShellQuote(path)
}

func (postgresMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// The dump file lives on the host (SFTP upload), so cat feeds it through
	// stdin — pg_restore reads a custom-format archive from stdin fine when
	// not using -j. A path argument would resolve inside the container.
	return "cat " + shared.ShellQuote(path) + " | " + wrapExec(c, fmt.Sprintf(
		"%s pg_restore %s --no-owner --clean --if-exists -d %s 2>&1",
		pgEnv(c), pgConnArgs(c), shared.ShellQuote(db)))
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
	return wrapExec(c, fmt.Sprintf("%s psql %s -c %s 2>/dev/null", pgEnv(c), pgConnArgs(c), shared.ShellQuote(q)))
}

// pqIdent double-quotes a Postgres identifier with embedded quotes doubled.
func pqIdent(name string) string {
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

// --- MySQL / MariaDB -------------------------------------------------------

type mysqlMigrator struct{}

func (mysqlMigrator) Engine() string { return "mysql" }

func (mysqlMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	// pgrep returns non-zero if nothing matched; gate echo on the OR result so an
	// absent mysqld+ mariadbd prints nothing (not "yes"). Matches postgresMigrator.
	out, _, _, _ := ssh.ExecContext(ctx, "(pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1) && echo yes")
	return strings.TrimSpace(out) == "yes"
}

func (mysqlMigrator) Streaming() bool { return true }

func (mysqlMigrator) SupportsLiveReplication() bool { return false }

func (mysqlMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	cmd := wrapExec(c, fmt.Sprintf("%s mysql %s -N -B -e %s 2>/dev/null",
		mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(mysqlListQuery(c))))
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("mysql list databases: %v", err)
	}
	return parseCatalog(out, "\t")
}

// mysqlListQuery builds the catalog query. IFNULL matters: a schema holding
// only views has SUM(data_length+index_length) = NULL, `mysql -N -B` prints
// the literal string "NULL", and parseCatalog drops the row — so a view-only
// database silently vanished from the migration. The LEFT JOIN from schemata
// likewise keeps schemas with zero tables, which have no rows in
// information_schema.tables at all.
func mysqlListQuery(_ DBCredentials) string {
	return "SELECT s.schema_name, IFNULL(ROUND(SUM(t.data_length+t.index_length)/1048576),0) " +
		"FROM information_schema.schemata s " +
		"LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name " +
		"WHERE s.schema_name NOT IN ('mysql','sys','information_schema','performance_schema') " +
		"GROUP BY s.schema_name ORDER BY s.schema_name;"
}

func (mysqlMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// --single-transaction for a consistent snapshot without locking;
	// --add-drop-database makes the restore idempotent. Output to stdout.
	return wrapExec(c, fmt.Sprintf("%s mysqldump %s --single-transaction --routines --triggers --add-drop-database --databases %s 2>/dev/null",
		mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(db)))
}

func (mysqlMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	// mysql reads the dump from stdin. The dump already contains
	// CREATE/DROP DATABASE statements (--add-drop-database), so db here is the
	// connection target, not a filter.
	return wrapExec(c, fmt.Sprintf("%s mysql %s 2>&1", mysqlEnv(c), mysqlConnArgs(c)))
}

func (mysqlMigrator) DumpCommand(c DBCredentials, db, path string) string {
	// gzip + redirect stay on the host so the file is SFTP-reachable; the
	// stream command already carries the exec-context wrapper (the old form
	// prefixed it again, producing docker-exec-inside-docker-exec).
	return mysqlMigrator{}.StreamDumpCommand(c, db) + " | gzip > " + shared.ShellQuote(path)
}

func (mysqlMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// gunzip runs on the host where the uploaded dump lives; the wrapped mysql
	// reads it over stdin (docker exec -i forwards stdin into the container).
	return fmt.Sprintf("gunzip -c %s | %s", shared.ShellQuote(path), mysqlMigrator{}.StreamRestoreCommand(c, db))
}

func (mysqlMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	q := fmt.Sprintf("DROP DATABASE IF EXISTS %s;", backtickIdent(db))
	return wrapExec(c, fmt.Sprintf("%s mysql %s -e %s 2>/dev/null", mysqlEnv(c), mysqlConnArgs(c), shared.ShellQuote(q)))
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

func (mongoMigrator) SupportsLiveReplication() bool { return false }

func (mongoMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// The eval renders plain "name<TAB>MiB" lines itself, so the output is
	// identical on mongosh and the legacy mongo shell. The previous approach
	// printed the raw command result and scanned it for double-quoted JSON
	// keys — but only the LEGACY shell prints strict JSON; mongosh (MongoDB
	// 5+) emits Node inspect format (`name: 'admin', sizeOnDisk: Long('…')`),
	// so on any modern host zero entries parsed and collect failed with a
	// misleading "no user databases enumerated".
	shell, _ := mongoShell(ctx, ssh)
	listCmd := wrapExec(c, fmt.Sprintf("%s %s --quiet --eval %s 2>/dev/null",
		shell, mongoConnArgs(c), shared.ShellQuote(mongoListEval())))
	out, _, exit, err := ssh.ExecContext(ctx, listCmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("mongo list databases: %v", err)
	}
	return parseCatalog(out, "\t")
}

// mongoListEval builds the shell-agnostic listing expression. String
// concatenation coerces Long to decimal digits on both shells, and the
// filter drops system databases before they ever reach the wire.
func mongoListEval() string {
	return `db.adminCommand({listDatabases:1}).databases` +
		`.filter(function(d){return ['admin','local','config'].indexOf(d.name)===-1})` +
		`.map(function(d){return d.name+"\t"+Math.round(d.sizeOnDisk/1048576)})` +
		`.join("\n")`
}

// mongoShell resolves the MongoDB shell binary, preferring mongosh (MongoDB 5+)
// and falling back to the legacy mongo shell with a redacted warning. mongosh is
// the modern default (replication.go:531,539 already assume it); the fallback
// keeps MongoDB 4.x hosts working without silently downgrading.
func mongoShell(ctx context.Context, ssh SSHExecuter) (shell string, warning string) {
	out, _, _, _ := ssh.ExecContext(ctx, "command -v mongosh 2>/dev/null")
	if strings.TrimSpace(out) != "" {
		return "mongosh", ""
	}
	return "mongo", "mongosh not found; falling back to legacy 'mongo' shell (MongoDB 4.x compat). Install mongosh when possible."
}

func (mongoMigrator) StreamDumpCommand(c DBCredentials, db string) string {
	// --archive streams a single archive to stdout; --gzip compresses in flight.
	return wrapExec(c, fmt.Sprintf("mongodump %s --db %s --archive --gzip 2>/dev/null",
		mongoConnArgs(c), shared.ShellQuote(db)))
}

func (mongoMigrator) StreamRestoreCommand(c DBCredentials, db string) string {
	// --drop makes restore idempotent. --nsInclude targets the streamed db.
	return wrapExec(c, fmt.Sprintf("mongorestore %s --archive --gzip --drop --nsInclude %s.* 2>&1",
		mongoConnArgs(c), shared.ShellQuote(db)))
}

func (mongoMigrator) DumpCommand(c DBCredentials, db, path string) string {
	// Redirect on the host side; the stream command already carries the
	// exec-context wrapper (the old form prefixed it a second time).
	return mongoMigrator{}.StreamDumpCommand(c, db) + " > " + shared.ShellQuote(path)
}

func (mongoMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// cat on the host feeds the uploaded archive through stdin — a path
	// argument would resolve inside the container where the file never landed.
	return "cat " + shared.ShellQuote(path) + " | " + mongoMigrator{}.StreamRestoreCommand(c, db)
}

func (mongoMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	// Drop via the shell; db name is the eval target. Command builders are pure
	// (no ctx/ssh), so mongosh is used unconditionally — replication.go already
	// assumes mongosh. The legacy fallback only applies to ListDatabases.
	drop := fmt.Sprintf("db.getSiblingDB(%s).dropDatabase()", shared.ShellQuote(db))
	return wrapExec(c, fmt.Sprintf("mongosh %s --quiet --eval %s 2>/dev/null", mongoConnArgs(c), shared.ShellQuote(drop)))
}

// --- Redis -----------------------------------------------------------------

type redisMigrator struct{}

func (redisMigrator) Engine() string { return "redis" }

func (redisMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "pgrep -x redis-server >/dev/null 2>&1 && echo yes")
	return strings.TrimSpace(out) == "yes"
}

func (redisMigrator) Streaming() bool { return false }

func (redisMigrator) SupportsLiveReplication() bool { return false }

func (redisMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	// Redis has one logical DB namespace; report DBSIZE as a rough "size" (key
	// count, not MiB — Redis exposes no per-DB size without SCAN+DEBUG).
	cmd := wrapExec(c, fmt.Sprintf("%s redis-cli -h %s -p %d DBSIZE 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port))
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
	// Password rides REDISCLI_AUTH (never -a on argv).
	return wrapExec(c, fmt.Sprintf("%s redis-cli -h %s -p %d --rdb %s 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port, shared.ShellQuote(path)))
}

func (redisMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// Restore = copy the RDB into the Redis data dir, restart, and require a PONG
	// health check. A failed restart must surface as an error — no || true masking
	// the chain. set -e makes any step abort before the PING gate; the SHUTDOWN's
	// expected non-zero is the only allowed non-zero (redis closes the conn).
	return wrapExec(c, fmt.Sprintf(
		"set -e; "+
			"[ -s %s ] || exit 1; "+ // refuse to restore a missing/empty RDB
			"rdir=$(redis-cli -h %s -p %d %s CONFIG GET dir 2>/dev/null | tail -1); "+
			"[ -n \"$rdir\" ] || rdir=/var/lib/redis; "+
			"cp %s \"$rdir/dump.rdb\"; "+
			"redis-cli -h %s -p %d %s SHUTDOWN NOSAVE 2>/dev/null || true; "+
			"(systemctl restart redis redis-server 2>/dev/null || service redis-server restart 2>/dev/null || /etc/init.d/redis-server restart 2>/dev/null); "+
			"pong=$(redis-cli -h %s -p %d %s PING 2>/dev/null); "+
			"[ \"$pong\" = \"PONG\" ] || exit 2",
		shared.ShellQuote(path),
		shared.ShellQuote(c.Host), c.Port, redisEnv(c),
		shared.ShellQuote(path),
		shared.ShellQuote(c.Host), c.Port, redisEnv(c),
		shared.ShellQuote(c.Host), c.Port, redisEnv(c)))
}

func (redisMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	// FLUSHALL empties the dataset (rollback = undo the restore). Connects through
	// the engine's execution context (host/container/compose) and auth env.
	return wrapExec(c, fmt.Sprintf("%s redis-cli -h %s -p %d FLUSHALL 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port))
}

// redisEnv returns the REDISCLI_AUTH env prefix so the password never appears on
// the command line (process list / ps audit). Empty if no password. Mirrors the
// pgEnv / mysqlEnv convention. Redis rejects -a as of 7.2 in any case.
func redisEnv(c DBCredentials) string {
	if c.Password == "" {
		return ""
	}
	return fmt.Sprintf("REDISCLI_AUTH=%s", shared.ShellQuote(c.Password))
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



