# P0 Safety Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the migration engine safe to run — no silent DB skip, no split-brain rollback, no false success, no secret in argv, no OOM on large output, no 30s kill on long restores, no auto-complete past a manual cutover, no in-memory checkpoint presented as resumable.

**Architecture:** Seven ordered commits against the existing Go backend. No schema changes (verified — all needed columns exist). New states `StateAwaitingCutover` + `StateNeedsManualIntervention` are new TEXT values in `migrations.state`, persisting via the existing dual-write `SetMigrationState`. Every ambiguous state fails closed to `StateNeedsManualIntervention`; every partial rollback ends in `StateRollbackDegraded`.

**Tech Stack:** Go 1.x, stdlib `testing`, SQLite, existing `shared.ShellQuote`.

## Global Constraints

- The revised spec (`docs/superpowers/specs/2026-07-11-p0-safety-fixes-design.md`) is the implementation contract. Do not reopen scope or substitute lighter fallbacks.
- No new DB engines, no live replication implementation, no distributed fencing/lease, no automatic traffic switching this pass.
- No byte-level/mid-transfer resume (Phase 2; documented as residual).
- Secrets never on argv/logs/errors/API/persisted data/audit. Redis uses `REDISCLI_AUTH`, never `redis-cli -a`.
- Fail-closed to `StateNeedsManualIntervention` on ambiguity; `StateRollbackDegraded` on partial rollback.
- Each commit: failing test → verify fail → minimal impl → verify pass → commit. `go build ./... && go vet ./... && go test ./...` green before each commit.
- No product/API/UI wording claims zero-downtime, automatic cutover, or resumable large transfer beyond Phase 1 reality.

---

## Commit 1 — DB adapter correctness/security (P0-6,7,8,11,10)

**Files:**
- Modify: `internal/mod/migration/database_adapter.go` (MySQL Detect :238, mongosh :309,341, redis env+health :360-413)
- Modify: `internal/mod/planner/bridge.go` (volume path injection :252,266,279,282-283,310 + import)
- Test: `internal/mod/migration/database_p0_test.go` (CREATE)
- Test: `internal/mod/planner/bridge_p0_test.go` (CREATE)

**Interfaces:**
- Consumes: `SSHExecuter.ExecContext` (4-tuple `(string,string,int,error)`), `shared.ShellQuote`, `mockSSH` in `mock_test.go`.
- Produces: `mysqlMigrator.Detect` correct; `mongoShell(ssh)` helper; `redisEnv(c)` replacing `redisAuth`; `validateVolName` + ShellQuoted volume paths.

### P0-6 — MySQL Detect always-true

- [ ] **Step 1: Write failing test** in `internal/mod/migration/database_p0_test.go`:

```go
package migration

import (
	"context"
	"strings"
	"testing"
)

func TestMySQLDetectFalseWhenAbsent(t *testing.T) {
	m := newMockSSH() // no output configured → simulates pgrep finding nothing
	got := (mysqlMigrator{}).Detect(context.Background(), m)
	if got {
		t.Fatalf("mysql Detect must be false when no mysqld is running; got true")
	}
	// Sanity: the issued command must be a guarded && chain, not a bare ; echo yes.
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/mod/migration/ -run TestMySQLDetectFalseWhenAbsent -v`
Expected: FAIL (current command is `... ; echo yes`, so `out` contains "yes" → `Detect` returns true).

- [ ] **Step 3: Fix `database_adapter.go:238`**

```go
func (mysqlMigrator) Detect(ctx context.Context, ssh SSHExecuter) bool {
	out, _, _, _ := ssh.ExecContext(ctx, "(pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1) && echo yes")
	return strings.TrimSpace(out) == "yes"
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/mod/migration/ -run TestMySQLDetectFalseWhenAbsent -v`
Expected: PASS.

### P0-11 — Mongo `mongo` → `mongosh` with fallback

- [ ] **Step 5: Write failing test** (append to `database_p0_test.go`):

```go
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

func TestMongoShellFallback(t *testing.T) {
	m := newMockSSH() // command -v mongosh returns nothing (exit 1, empty output)
	shell, warn := mongoShell(context.Background(), m)
	if shell != "mongo" {
		t.Fatalf("expected fallback to mongo, got %q", shell)
	}
	if warn == "" {
		t.Fatalf("expected a redacted warning when falling back to legacy mongo shell")
	}
}
```

Also assert the command builders use the resolved shell:

```go
func TestMongoListDatabasesUsesMongosh(t *testing.T) {
	m := newMockSSH()
	m.execOutput["command -v mongosh"] = "/usr/bin/mongosh\n"
	// mongosh prefix-match: ListDatabases issues "<shell> --uri ... --quiet --eval ..."
	m.execOutput["mongosh"] = `{"databases":[{"name":"shop","sizeOnDisk":1048576}]}`
	_, err := (mongoMigrator{}).ListDatabases(context.Background(), m, DBCredentials{Engine: "mongodb", Username: "u", Password: "p", Host: "127.0.0.1", Port: 27017})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsCommandPrefix(m.commands, "mongosh ") {
		t.Fatalf("expected mongosh to be used; commands=%v", m.commands)
	}
}
```

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/mod/migration/ -run 'TestMongoShell|TestMongoListDatabasesUsesMongosh' -v`
Expected: FAIL (`mongoShell` undefined; commands still use literal `mongo`).

- [ ] **Step 7: Add `mongoShell` helper + use it** in `database_adapter.go`. Add near the mongo section:

```go
// mongoShell resolves the MongoDB shell binary, preferring mongosh (Mongo 5+)
// and falling back to the legacy mongo shell with a redacted warning event.
// The result is cached per-client by the caller via the returned shell string.
func mongoShell(ctx context.Context, ssh SSHExecuter) (shell string, warning string) {
	out, _, _, _ := ssh.ExecContext(ctx, "command -v mongosh 2>/dev/null")
	if strings.TrimSpace(out) != "" {
		return "mongosh", ""
	}
	return "mongo", "mongosh not found; falling back to legacy 'mongo' shell (MongoDB 4.x compat). Install mongosh when possible."
}
```

Then change `ListDatabases` (:308-309) and `DropDatabaseCommand` (:340-341) to resolve the shell once and use it:

```go
func (mongoMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	shell, _ := mongoShell(ctx, ssh)
	eval := "db.adminCommand({listDatabases:1})"
	listCmd := fmt.Sprintf("%s %s --quiet --eval %s 2>/dev/null", shell, mongoConnArgs(c), shared.ShellQuote(eval))
	out, _, exit, err := ssh.ExecContext(ctx, listCmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("mongo list databases: %v", err)
	}
	return parseMongoCatalog(out)
}

func (mongoMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	// Dropping does not run in a ctx-bearing call (command-builder contract); use
	// mongosh unconditionally — it is the modern default and replication.go:531
	// already assumes mongosh. The legacy fallback applies only to listDatabases.
	drop := fmt.Sprintf("db.getSiblingDB(%s).dropDatabase()", shared.ShellQuote(db))
	return fmt.Sprintf("mongosh %s --quiet --eval %s 2>/dev/null", mongoConnArgs(c), shared.ShellQuote(drop))
}
```

- [ ] **Step 8: Run to verify pass**

Run: `go test ./internal/mod/migration/ -run 'TestMongoShell|TestMongoListDatabasesUsesMongosh' -v`
Expected: PASS.

### P0-8 — Redis `redis-cli -a` → `REDISCLI_AUTH` env

- [ ] **Step 9: Write failing test** (append):

```go
func TestRedisNoPasswordInArgv(t *testing.T) {
	c := DBCredentials{Engine: "redis", Password: "s3cret-pw", Host: "127.0.0.1", Port: 6379}
	cases := map[string]string{
		"ListDatabases": func() string {
			m := newMockSSH()
			(mongoMigrator{}).Detect(context.Background(), m) // noop; just to keep import usage stable
			_, _ = (redisMigrator{}).ListDatabases(context.Background(), m, c)
			return lastCmdContaining(m.commands, "redis-cli")
		}(),
	}
	_ = cases
	// Direct command-builder assertions cover all 4 sites without running SSH:
	cmds := []string{
		(redisMigrator{}).DumpCommand(c, "redis", "/tmp/dump.rdb"),
		(redisMigrator{}).RestoreCommand(c, "redis", "/tmp/dump.rdb"),
		(redisMigrator{}).DropDatabaseCommand(c, "redis"),
	}
	// ListDatabases runs via SSH; capture its command too.
	m := newMockSSH()
	_, _ = (redisMigrator{}).ListDatabases(context.Background(), m, c)
	cmds = append(cmds, lastCmdContaining(m.commands, "redis-cli"))
	for i, cmd := range cmds {
		if strings.Contains(cmd, "-a ") {
			t.Fatalf("site %d: -a flag present in command: %s", i, cmd)
		}
		if strings.Contains(cmd, "s3cret-pw") {
			t.Fatalf("site %d: password literal present in command: %s", i, cmd)
		}
		if !strings.Contains(cmd, "REDISCLI_AUTH=") {
			t.Fatalf("site %d: REDISCLI_AUTH env missing: %s", i, cmd)
		}
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
```

- [ ] **Step 10: Run to verify failure**

Run: `go test ./internal/mod/migration/ -run TestRedisNoPasswordInArgv -v`
Expected: FAIL (current `redisAuth()` emits `-a '<pw>' --no-auth-warning`).

- [ ] **Step 11: Replace `redisAuth` with `redisEnv`** in `database_adapter.go`. Delete the `redisAuth` function (:408-413) and replace all 4 call sites (`:360-361`, `:382-383`, `:390-397`, `:402-403`). New helper:

```go
// redisEnv returns the REDISCLI_AUTH env prefix so the password never appears on
// the command line (process list / ps audit). Empty if no password.
func redisEnv(c DBCredentials) string {
	if c.Password == "" {
		return ""
	}
	return fmt.Sprintf("REDISCLI_AUTH=%s", shared.ShellQuote(c.Password))
}
```

Rewrite the 4 sites so the env prefix leads the command and no `-a` remains:

```go
func (redisMigrator) ListDatabases(ctx context.Context, ssh SSHExecuter, c DBCredentials) ([]DBCatalogEntry, error) {
	cmd := fmt.Sprintf("%s redis-cli -h %s -p %d DBSIZE 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port)
	out, _, exit, err := ssh.ExecContext(ctx, cmd)
	if err != nil || exit != 0 {
		return nil, fmt.Errorf("redis dbsize: %v", err)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return []DBCatalogEntry{{Name: "redis", SizeMB: n}}, nil
}

func (redisMigrator) DumpCommand(c DBCredentials, db, path string) string {
	return fmt.Sprintf("%s redis-cli -h %s -p %d --rdb %s 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port, shared.ShellQuote(path))
}

// RestoreCommand is restructured below in P0-7 to drop || true and add a PING
// health gate. The env prefix is applied there.
```

(See Step 13 for the full `RestoreCommand` rewrite; `DropDatabaseCommand` below.)

```go
func (redisMigrator) DropDatabaseCommand(c DBCredentials, db string) string {
	return fmt.Sprintf("%s redis-cli -h %s -p %d FLUSHALL 2>/dev/null",
		redisEnv(c), shared.ShellQuote(c.Host), c.Port)
}
```

- [ ] **Step 12: Run to verify pass** (expect partial — RestoreCommand still has `|| true`; the no-`-a` assertions pass but P0-7 test below will drive the rest).

Run: `go test ./internal/mod/migration/ -run TestRedisNoPasswordInArgv -v`
Expected: PASS.

### P0-7 — Redis `|| true` mask + health fail-closed

- [ ] **Step 13: Write failing test** (append):

```go
func TestRedisRestoreNoSilentFailure(t *testing.T) {
	c := DBCredentials{Engine: "redis", Password: "p", Host: "127.0.0.1", Port: 6379}
	cmd := (redisMigrator{}).RestoreCommand(c, "redis", "/tmp/dump.rdb")
	if strings.Contains(cmd, "|| true") {
		t.Fatalf("restore command must not mask failure with || true: %s", cmd)
	}
	if !strings.Contains(cmd, "redis-cli") || !strings.Contains(cmd, "PING") {
		t.Fatalf("restore command must health-gate via redis-cli PING: %s", cmd)
	}
	// The PING check must be enforced (set -e or explicit &&), not optional.
	if !strings.Contains(cmd, "set -e") && !strings.Contains(cmd, "PONG") {
		t.Fatalf("restore must require PONG (set -e or explicit PONG check): %s", cmd)
	}
}
```

- [ ] **Step 14: Run to verify failure**

Run: `go test ./internal/mod/migration/ -run TestRedisRestoreNoSilentFailure -v`
Expected: FAIL (current `RestoreCommand` ends with `... || true` and has no PING gate).

- [ ] **Step 15: Rewrite `RestoreCommand`** in `database_adapter.go:386-398`:

```go
func (redisMigrator) RestoreCommand(c DBCredentials, db, path string) string {
	// Restore = copy the RDB into the Redis data dir, restart, and require a PONG
	// health check. A failed restart must surface as an error (no || true).
	// set -e makes any step failure abort the chain before PING.
	return fmt.Sprintf(
		"set -e; "+
			"rdir=$(redis-cli -h %s -p %d %s CONFIG GET dir 2>/dev/null | tail -1); "+
			"[ -n \"$rdir\" ] || rdir=/var/lib/redis; "+
			"cp %s \"$rdir/dump.rdb\"; "+
			"redis-cli -h %s -p %d %s SHUTDOWN NOSAVE 2>/dev/null || true; "+
			"(systemctl restart redis redis-server 2>/dev/null || service redis-server restart 2>/dev/null || /etc/init.d/redis-server restart 2>/dev/null); "+
			"pong=$(redis-cli -h %s -p %d %s PING 2>/dev/null); "+
			"[ \"$pong\" = \"PONG\" ]",
		shared.ShellQuote(c.Host), c.Port, redisEnv(c),
		shared.ShellQuote(path),
		shared.ShellQuote(c.Host), c.Port, redisEnv(c),
		shared.ShellQuote(c.Host), c.Port, redisEnv(c))
}
```

Note: `SHUTDOWN NOSAVE || true` is retained because redis-cli returns non-zero when the server closes the connection on shutdown — that is expected, not a failure. The final PING is the real gate under `set -e`.

- [ ] **Step 16: Run to verify pass**

Run: `go test ./internal/mod/migration/ -run TestRedisRestoreNoSilentFailure -v`
Expected: PASS.

### P0-10 — Volume path injection (planner/bridge.go)

- [ ] **Step 17: Write failing test** `internal/mod/planner/bridge_p0_test.go`:

```go
package planner

import (
	"context"
	"strings"
	"testing"

	"meshium/internal/mod/migration"
	"meshium/internal/mod/transport"
)

type stubSSH struct {
	commands []string
}

func (s *stubSSH) Exec(cmd string) (string, string, int, error) {
	s.commands = append(s.commands, cmd)
	return "", "", 0, nil
}
func (s *stubSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	s.commands = append(s.commands, cmd)
	return "", "", 0, nil
}
func (s *stubSSH) IsAlive() bool             { return true }
func (s *stubSSH) Upload(src interface{ Read(p []byte) (int, error) }, remotePath string) error {
	return nil
}
func (s *stubSSH) Download(remotePath string, dst interface{ Write(p []byte) (int, error) }) error {
	return nil
}

func TestValidateVolNameRejectsInjection(t *testing.T) {
	bad := []string{"foo; rm -rf /", "foo$(reboot)", "foo`x`", "foo|x", "foo && echo", "  ", ""}
	for _, name := range bad {
		if err := validateVolName(name); err == nil {
			t.Fatalf("expected rejection for %q", name)
		}
	}
	good := []string{"data", "pg_data-1", "vol.a:b", "/var/lib/x", "C0nfig_2"}
	for _, name := range good {
		if err := validateVolName(name); err != nil {
			t.Fatalf("expected accept for %q: %v", name, err)
		}
	}
}
```

(If `stubSSH` above conflicts with existing test helpers in the planner package, adapt to use the existing stub; the assertion on `validateVolName` is the required part.)

- [ ] **Step 18: Run to verify failure**

Run: `go test ./internal/mod/planner/ -run TestValidateVolNameRejectsInjection -v`
Expected: FAIL (`validateVolName` undefined).

- [ ] **Step 19: Add `validateVolName` + ShellQuote all volume paths** in `internal/mod/planner/bridge.go`. Add import `"meshium/internal/shared"` to the import block (after `"io"`). Add the validator near the top of the file (after `BuildSteps`):

```go
// validateVolName rejects volume/container names containing characters outside
// a safe shell-and-path set. It is a loud failure layer on top of ShellQuote
// (which already neutralizes injection). Empty names are rejected.
func validateVolName(name string) error {
	if name == "" {
		return fmt.Errorf("empty volume name")
	}
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.:/-"
	for _, r := range name {
		if !strings.ContainsRune(allowed, r) {
			return fmt.Errorf("invalid character %q in volume name", r)
		}
	}
	return nil
}
```

Then in `DockerVolumeMigrationStep.Execute`, validate before the loop and quote every shell interpolation. Replace lines :252-283 region:

```go
	// Stop the container on the source
	... (progress unchanged) ...
	if err := validateVolName(s.ContainerName); err != nil {
		return "", fmt.Errorf("container name: %w", err)
	}
	s.SourceSSH.ExecContext(ctx, fmt.Sprintf("docker stop %s", shared.ShellQuote(s.ContainerName)))

	for _, vol := range s.Volumes {
		if err := validateVolName(vol); err != nil {
			return "", fmt.Errorf("volume name: %w", err)
		}
		... (progress unchanged) ...

		sourcePath := fmt.Sprintf("/tmp/meshium-vol-%s.tar", s.ContainerName)
		s.SourceSSH.ExecContext(ctx, fmt.Sprintf("tar cf %s -C %s .", shared.ShellQuote(sourcePath), shared.ShellQuote(vol)))

		destPath := fmt.Sprintf("/tmp/meshium-vol-%s.tar", s.ContainerName)
		pipeReader, pipeWriter := newPipe()
		go func() {
			defer pipeWriter.Close()
			s.SourceSSH.Download(sourcePath, pipeWriter)
		}()
		s.TargetSSH.Upload(pipeReader, destPath)

		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("mkdir -p %s && tar xf %s -C %s", shared.ShellQuote(vol), shared.ShellQuote(destPath), shared.ShellQuote(vol)))

		s.SourceSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", shared.ShellQuote(sourcePath)))
		s.TargetSSH.ExecContext(ctx, fmt.Sprintf("rm -f %s", shared.ShellQuote(destPath)))
	}
```

And in `Verify` (:310):

```go
		for _, vol := range s.Volumes {
			if err := validateVolName(vol); err != nil {
				return "", fmt.Errorf("volume name: %w", err)
			}
			_, stderr, exitCode, err := s.TargetSSH.ExecContext(ctx, fmt.Sprintf("test -d %s", shared.ShellQuote(vol)))
			if err != nil || exitCode != 0 {
				return "", fmt.Errorf("volume %s not found on target: %s", vol, stderr)
			}
		}
```

Note: the `stubSSH` in Step 17's test must satisfy `transport.SSHExecuter` (the real interface uses `io.Reader`/`io.Writer`, not anonymous interfaces). Correct the stub signatures to match `transport.go`:

```go
func (s *stubSSH) Upload(src io.Reader, remotePath string) error       { return nil }
func (s *stubSSH) Download(remotePath string, dst io.Writer) error     { return nil }
```

- [ ] **Step 20: Run to verify pass**

Run: `go test ./internal/mod/planner/ -run TestValidateVolNameRejectsInjection -v`
Expected: PASS.

- [ ] **Step 21: Add injection-through-quoting test** (append to `bridge_p0_test.go`):

```go
func TestVolumePathShellQuoted(t *testing.T) {
	src := &stubSSH{}
	dst := &stubSSH{}
	step := &DockerVolumeMigrationStep{
		StepName:      "vol",
		ContainerName: "web",
		Volumes:       []string{"data"},
		SourceSSH:     src,
		TargetSSH:     dst,
	}
	_, err := step.Execute(migration.StepContext{Ctx: context.Background()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, c := range src.commands {
		if strings.Contains(c, "tar cf") && !strings.Contains(c, "'/tmp/meshium-vol-web.tar'") {
			t.Fatalf("tar source path not quoted: %s", c)
		}
	}
}
```

- [ ] **Step 22: Run full package + vet, then commit**

Run:
```bash
go build ./... && go vet ./internal/mod/migration/ ./internal/mod/planner/ && go test ./internal/mod/migration/ ./internal/mod/planner/ -v
```
Expected: all green.

```bash
git add internal/mod/migration/database_adapter.go internal/mod/planner/bridge.go internal/mod/migration/database_p0_test.go internal/mod/planner/bridge_p0_test.go
git commit -m "fix(migration): P0 DB adapter correctness + volume path injection (P0-6,7,8,10,11)

MySQL Detect false when mysqld absent (&& gate, not ; echo yes).
Redis: REDISCLI_AUTH env replaces -a argv; restart PONG health gate, no || true.
Mongo: mongosh preferred, legacy mongo fallback + warning.
Volumes: validateVolName + ShellQuote all remote path interpolations."
```

---

## Commits 2–7

(Outlines below; full bite-sized TDD steps are filled in identically to Commit 1 when each commit is reached. The spec section §2 + §3 are the per-step source of truth. The lazy-execution principle: do not pre-write Commit 2 steps until Commit 1 is green and committed.)

### Commit 2 — Long-command streaming + bounded output (P0-4,5,9)
- `internal/mod/ssh/boundedio.go` (CREATE): `boundedWriter` (cap, overflow flag, `ErrOutputLimitExceeded`).
- `internal/mod/ssh/model.go:29`: `FileTransfer 5m→30m` interim; add `Inactivity time.Duration` field.
- `internal/mod/ssh/client.go:361`: `ExecContextWithTimeout` uses bounded writers.
- `internal/mod/ssh/client.go:642`: `ExecWithStdin` drops `Command` ceiling; uses `ctx` + configurable `Inactivity`; resets on stdin write / stdout/stderr activity; `ErrInactivityTimeout` on stall.
- Tests: `boundedio_test.go`, `client_p0_test.go` (outlasts 30s, inactivity stall, output cap, custom FileTransfer).

### Commit 3 — Rollback topology guard + honest terminal state (P0-1, P0-2 honest)
- `internal/mod/migration/topology.go` (CREATE): `ProbeTopology` per engine (MySQL `read_only`+replica status; PG `pg_is_in_recovery`; Redis `INFO replication`); `ErrUnsafeTopology`.
- `internal/mod/migration/replication.go:319,424,495`: rollback* call probe first; fail-closed `ErrUnsafeTopology`; Redis re-point only when roles verified.
- `internal/mod/migration/pipeline.go:776-787`: aggregate rollback errors; `RolledBack` only when `len(rollbackErrors)==0`; else `RollbackDegraded`/`NeedsManualIntervention`.
- Tests: `topology_test.go`, `rollback_p0_test.go` (5 topology cases + partial/full/ambiguous terminal state).

### Commit 4 — Persisted checkpoint wiring (P0-3)
- `internal/mod/migration/progress.go`: remove `NoopCheckpointStore` from production wiring (test stub only).
- `internal/mod/migration/pipeline_repo.go:148`: wire `UpdateStageCheckpoint` in a tx with the state advance.
- `internal/mod/migration/pipeline.go` execute loop: persist stage checkpoint after success, **before** state advance/event; checkpoint write failure → no advance, fail closed.
- Test: `checkpoint_p0_test.go` (persisted, write-failure-no-advance, restart restores state).

### Commit 5 — AwaitingCutover / NeedsManualIntervention + endpoint guard (P0-2 cutover)
- `internal/mod/migration/state.go`: add `StateAwaitingCutover`, `StateNeedsManualIntervention` (enum, String, stateString, IsTerminal/IsRunning/CanResume, transitionTable rows).
- `internal/mod/migration/pipeline.go:1647`: `trafficSwitchStage` → `StateAwaitingCutover` + `ErrAwaitingCutover` (clean stop); remove auto-advance.
- `internal/mod/migration/pipeline_handler.go`: commit endpoint — 409 `not_awaiting_cutover` / `cutover_not_confirmed`; validated `Transition(StateCommitted)` only when confirmed; `NeedsManualIntervention` on failure; never `ForceTransition`.
- Test: `cutover_p0_test.go` (manual_required stops, survives restart, commit rejected, no ForceTransition).

### Commit 6 — ForceTransition audit/removal/restriction (P0-2 regression-2)
- Remove 4 callers: `pipeline.go:414` (Committed), `:787,1006`, `engine.go:627` (RolledBack) → validated `Transition` or `SetMigrationStateContext(NeedsManualIntervention)`.
- Restrict + comment 7: `pipeline.go:932,897,925,910`, `engine.go:579,561,644`.
- Test: `force_transition_p0_test.go` (removed gone; restricted cannot reach Committed or skip AwaitingCutover).

### Commit 7 — Full regression suite, docs, matrix
- `docs/known-limitations.md`, `docs/p0-matrix.md` (the required delivery matrix from the guardrails).
- Run `go test ./...` before + after; record results.

---

## Self-review

- **Spec coverage:** §2 per-finding fixes → Commits 1-6 tasks. §1.4 checkpoint → Commit 4. §4 endpoint → Commit 5. §5 test matrix → per-commit tests. §6 residual → Commit 7 docs. The required final delivery matrix → Commit 7. All 7 mandatory changes covered.
- **Placeholder scan:** Commit 1 steps are complete code. Commits 2-7 outlines will be expanded to identical bite-sized TDD when reached — stated explicitly, not hidden as "TODO".
- **Type consistency:** `redisEnv`/`mongoShell`/`validateVolName` names used consistently. `ErrUnsafeTopology`/`ErrAwaitingCutover`/`ErrInactivityTimeout`/`ErrOutputLimitExceeded` named consistently across commits.

→ skipped: pre-writing Commits 2-7 bite-sized steps before Commit 1 is green, add when each commit is reached. Keeps the plan honest and avoids stale code if Commit 1 surfaces an interface detail.
