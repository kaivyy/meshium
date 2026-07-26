package migration

import (
	"context"
	"strings"
	"testing"
)

// P0-6: MySQL Detect must be false when no mysqld/mariadbd is running. The
// prior command ended with "; echo yes" which always printed "yes" regardless of
// pgrep, so Detect could never return false.
func TestMySQLDetectFalseWhenAbsent(t *testing.T) {
	m := newMockSSH() // no output configured → pgrep finds nothing
	got := (mysqlMigrator{}).Detect(context.Background(), m)
	if got {
		t.Fatalf("mysql Detect must be false when no mysqld is running; got true")
	}
	var issued string
	for _, c := range m.commands {
		if strings.Contains(c, "pgrep -x mysqld") {
			issued = c
			break
		}
	}
	if strings.Contains(issued, "; echo yes") {
		t.Fatalf("detect command still uses unconditional '; echo yes': %s", issued)
	}
	if !strings.Contains(issued, "&& echo yes") {
		t.Fatalf("detect command must gate echo on pgrep via &&: %s", issued)
	}
}

// P0-6 counterpart: a present mysqld makes Detect true. The issued command
// only echoes "yes" when pgrep matches, so mock that exact command.
func TestMySQLDetectTrueWhenPresent(t *testing.T) {
	m := newMockSSH()
	m.execOutput["(pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1) && echo yes"] = "yes\n"
	if !(mysqlMigrator{}).Detect(context.Background(), m) {
		t.Fatal("mysql Detect must be true when mysqld is running")
	}
}

// P0-11: mongosh is preferred when present (no warning).
func TestMongoShellPrefersMongosh(t *testing.T) {
	m := newMockSSH()
	m.execOutput["command -v mongosh"] = "/usr/bin/mongosh\n"
	shell, warn := mongoShell(context.Background(), m)
	if shell != "mongosh" {
		t.Fatalf("expected mongosh, got %q", shell)
	}
	if warn != "" {
		t.Fatalf("no warning expected when mongosh present, got %q", warn)
	}
}

// P0-11: legacy mongo shell fallback when mongosh is absent, with a redacted
// warning (never silent).
func TestMongoShellFallback(t *testing.T) {
	m := newMockSSH() // command -v mongosh → empty
	shell, warn := mongoShell(context.Background(), m)
	if shell != "mongo" {
		t.Fatalf("expected fallback to mongo, got %q", shell)
	}
	if warn == "" {
		t.Fatal("expected a redacted warning when falling back to legacy mongo shell")
	}
}

// P0-11: ListDatabases uses the resolved shell (mongosh when present).
func TestMongoListDatabasesUsesMongosh(t *testing.T) {
	m := newMockSSH()
	m.execOutput["command -v mongosh"] = "/usr/bin/mongosh\n"
	// The eval prints plain name<TAB>MiB lines (shell-agnostic).
	m.execOutput["mongosh"] = "shop\t1\n"
	_, err := (mongoMigrator{}).ListDatabases(context.Background(), m,
		DBCredentials{Engine: "mongodb", Username: "u", Password: "p", Host: "127.0.0.1", Port: 27017})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsCommandPrefix(m.commands, "mongosh ") {
		t.Fatalf("expected mongosh to be used; commands=%v", m.commands)
	}
}

// P0-8: the password must never reach redis-cli's argv via -a. It rides the
// REDISCLI_AUTH env prefix (mirrors pgEnv/mysqlEnv). All four command sites are
// checked. The env assignment necessarily contains the literal value (as
// PGPASSWORD/MYSQL_PWD do); the requirement is "no -a on argv", not "no literal
// in the assignment".
func TestRedisNoPasswordInArgv(t *testing.T) {
	c := DBCredentials{Engine: "redis", Password: "s3cret-pw", Host: "127.0.0.1", Port: 6379}

	m := newMockSSH()
	_, _ = (redisMigrator{}).ListDatabases(context.Background(), m, c)
	cmds := []string{
		lastCmdContaining(m.commands, "redis-cli"),
		(redisMigrator{}).DumpCommand(c, "redis", "/tmp/dump.rdb"),
		(redisMigrator{}).RestoreCommand(c, "redis", "/tmp/dump.rdb"),
		(redisMigrator{}).DropDatabaseCommand(c, "redis"),
	}
	for i, cmd := range cmds {
		if strings.Contains(cmd, "-a ") {
			t.Fatalf("site %d: -a flag present on argv: %s", i, cmd)
		}
		if !strings.Contains(cmd, "REDISCLI_AUTH=") {
			t.Fatalf("site %d: REDISCLI_AUTH env missing: %s", i, cmd)
		}
	}
}

// P0-7: redis restore must not mask the restart/health chain with "|| true"
// and must health-gate via redis-cli PING (requiring PONG). The SHUTDOWN line
// is allowed to keep `|| true` (redis closes the connection → nonzero exit).
func TestRedisRestoreNoSilentFailure(t *testing.T) {
	c := DBCredentials{Engine: "redis", Password: "p", Host: "127.0.0.1", Port: 6379}
	cmd := (redisMigrator{}).RestoreCommand(c, "redis", "/tmp/dump.rdb")
	// Strip the legitimate SHUTDOWN-NOSAVE || true, then assert no other masking.
	masked := strings.Replace(cmd, "SHUTDOWN NOSAVE 2>/dev/null || true", "SHUTDOWN NOSAVE 2>/dev/null", 1)
	if strings.Contains(masked, "|| true") {
		t.Fatalf("restore command must not mask restart/health with || true: %s", cmd)
	}
	if !strings.Contains(cmd, "redis-cli") {
		t.Fatalf("restore must use redis-cli: %s", cmd)
	}
	if !strings.Contains(cmd, "PING") {
		t.Fatalf("restore must health-gate via redis-cli PING: %s", cmd)
	}
	if !(strings.Contains(cmd, "set -e") && strings.Contains(cmd, "PONG")) {
		t.Fatalf("restore must enforce the PONG check via set -e: %s", cmd)
	}
}

func lastCmdContaining(cmds []string, sub string) string {
	for i := len(cmds) - 1; i >= 0; i-- {
		if strings.Contains(cmds[i], sub) {
			return cmds[i]
		}
	}
	return ""
}
