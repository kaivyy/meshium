package migration

import (
	"context"
	"errors"
	"testing"
)

type recordingSSH struct {
	*mockSSH
	commands []string
}

func newRecordingSSH() *recordingSSH {
	return &recordingSSH{mockSSH: newMockSSH()}
}

func (r *recordingSSH) Exec(cmd string) (string, string, int, error) {
	r.commands = append(r.commands, cmd)
	return r.mockSSH.Exec(cmd)
}

func (r *recordingSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	r.commands = append(r.commands, cmd)
	return r.mockSSH.Exec(cmd)
}

func containsRecordedCommand(commands []string, want string) bool {
	for _, cmd := range commands {
		if cmd == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestFreezeManager_FreezeMySQL(t *testing.T) {
	source := newRecordingSSH()
	source.execOutput[`mysql -e "SHOW VARIABLES LIKE 'read_only'" -s -N 2>&1`] = "read_only\tON\n"

	mgr := NewFreezeManager(source, nil)
	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{{Type: "mysql", Port: 3306}})
	if err != nil {
		t.Fatalf("FreezeWrites() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("FreezeWrites() result = %+v, want success", result)
	}
	if result.Method != "database_readonly" {
		t.Fatalf("FreezeWrites() method = %q, want database_readonly", result.Method)
	}
	if !containsRecordedCommand(source.commands, `mysql -e "FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only = ON;" 2>&1`) {
		t.Fatalf("mysql freeze command not recorded: %#v", source.commands)
	}
	if !containsRecordedCommand(source.commands, `mysql -e "SHOW VARIABLES LIKE 'read_only'" -s -N 2>&1`) {
		t.Fatalf("mysql verification command not recorded: %#v", source.commands)
	}
}

func TestFreezeManager_FreezePostgreSQL(t *testing.T) {
	source := newRecordingSSH()
	source.execOutput[`sudo -u postgres psql -t -c "SHOW default_transaction_read_only;" 2>&1`] = "on\n"
	mgr := NewFreezeManager(source, nil)

	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{{Type: "postgresql", Port: 5432}})
	if err != nil {
		t.Fatalf("FreezeWrites() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("FreezeWrites() result = %+v, want success", result)
	}
	if !containsRecordedCommand(source.commands, `sudo -u postgres psql -c "ALTER SYSTEM SET default_transaction_read_only = on;" -c "SELECT pg_reload_conf();" 2>&1`) {
		t.Fatalf("postgres freeze command not recorded: %#v", source.commands)
	}
	if !containsRecordedCommand(source.commands, `sudo -u postgres psql -t -c "SHOW default_transaction_read_only;" 2>&1`) {
		t.Fatalf("postgres freeze verification command not recorded: %#v", source.commands)
	}
}

func TestFreezeManager_UnfreezeReverses(t *testing.T) {
	source := newRecordingSSH()
	source.execOutput[`mysql -e "SHOW VARIABLES LIKE 'read_only'" -s -N 2>&1`] = "read_only\tON\n"

	mgr := NewFreezeManager(source, nil)
	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{{Type: "mysql", Port: 3306}})
	if err != nil {
		t.Fatalf("FreezeWrites() error = %v", err)
	}
	if err := mgr.UnfreezeWrites(context.Background(), result); err != nil {
		t.Fatalf("UnfreezeWrites() error = %v", err)
	}
	if !containsRecordedCommand(source.commands, `mysql -e "SET GLOBAL read_only = OFF; UNLOCK TABLES;" 2>&1`) {
		t.Fatalf("mysql unfreeze command not recorded: %#v", source.commands)
	}
}

func TestFreezeManager_DetectRedisBullMQ(t *testing.T) {
	source := newRecordingSSH()
	source.execOutput[`redis-cli -p 6379 SCAN 0 MATCH "bull:*" COUNT 10 2>&1`] = "1\nbull:123\n"

	mgr := NewFreezeManager(source, nil)
	usage := mgr.detectRedisUsage(context.Background(), DatabaseInfo{Type: "redis", Port: 6379})
	if usage != RedisUsageBullMQ {
		t.Fatalf("detectRedisUsage() = %q, want %q", usage, RedisUsageBullMQ)
	}

	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{{Type: "redis", Port: 6379}})
	if err != nil {
		t.Fatalf("FreezeWrites() error = %v", err)
	}
	if !containsString(result.FrozenDBs, "redis:bullmq:6379") {
		t.Fatalf("FreezeWrites() FrozenDBs = %#v, want redis:bullmq:6379", result.FrozenDBs)
	}
}

func TestFreezeManager_DetectRedisCache(t *testing.T) {
	source := newRecordingSSH()
	mgr := NewFreezeManager(source, nil)

	usage := mgr.detectRedisUsage(context.Background(), DatabaseInfo{Type: "redis", Port: 6379})
	if usage != RedisUsageCache {
		t.Fatalf("detectRedisUsage() = %q, want %q", usage, RedisUsageCache)
	}

	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{{Type: "redis", Port: 6379}})
	if err != nil {
		t.Fatalf("FreezeWrites() error = %v", err)
	}
	if len(result.FrozenDBs) != 0 {
		t.Fatalf("FreezeWrites() FrozenDBs = %#v, want none for cache Redis", result.FrozenDBs)
	}
}

func TestFreezeManager_FreezeFailure_Unfreezes(t *testing.T) {
	source := newRecordingSSH()
	source.execOutput[`mysql -e "SHOW VARIABLES LIKE 'read_only'" -s -N 2>&1`] = "read_only\tON\n"
	source.execOutput[`mongosh --eval "db.fsyncLock()"`] = ""
	source.execErr[`mongosh --eval "db.fsyncLock()"`] = errors.New("fsyncLock failed")

	mgr := NewFreezeManager(source, nil)
	result, err := mgr.FreezeWrites(context.Background(), []DatabaseInfo{
		{Type: "mysql", Port: 3306},
		{Type: "mongodb", Port: 27017},
	})
	if err == nil {
		t.Fatal("FreezeWrites() error = nil, want failure")
	}
	if result == nil {
		t.Fatal("FreezeWrites() result = nil, want partial freeze state")
	}
	if !containsCommand(source.commands, `mysql -e "SET GLOBAL read_only = OFF; UNLOCK TABLES;" 2>&1`) {
		t.Fatalf("mysql unfreeze command not recorded after partial failure: %#v", source.commands)
	}
}
