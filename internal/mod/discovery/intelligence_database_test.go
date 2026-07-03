package discovery

import (
	"context"
	"reflect"
	"testing"
)

func TestAnalyzeDatabaseIntelligenceMySQLPostgreSQLAndRedis(t *testing.T) {
	ssh := newMockSSH()

	ssh.setResponse(`mysql -e "SHOW VARIABLES LIKE 'version'"`, "version\t8.0.36\n")
	ssh.setResponse(`mysql -e "SHOW SLAVE STATUS\G"`, "Slave_IO_Running: Yes\nSlave_SQL_Running: Yes\n")
	ssh.setResponse(`mysql -e "SHOW VARIABLES LIKE 'gtid_mode'"`, "gtid_mode\tON\n")
	ssh.setResponse(`mysql -e "SHOW VARIABLES LIKE 'log_bin'"`, "log_bin\tON\n")
	ssh.setResponse(`mysql -e "SHOW VARIABLES LIKE 'read_only'"`, "read_only\tOFF\n")
	ssh.setResponse(`mysql -e "SHOW VARIABLES LIKE 'innodb_buffer_pool_size'"`, "innodb_buffer_pool_size\t134217728\n")
	ssh.setResponse(`mysql -e "SHOW DATABASES"`, "Database\napp\nanalytics\nmysql\nperformance_schema\n")
	ssh.setResponse(`mysql -e "SELECT ROUND(SUM(data_length + index_length) / 1024 / 1024) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')"`, "512\n")

	ssh.setResponse(`psql -c "SELECT version()"`, "                                                           version                                                           \n--------------------------------------------------------------------------------------------------------------------------------\n PostgreSQL 16.3 on x86_64-pc-linux-gnu\n(1 row)\n")
	ssh.setResponse(`psql -c "SELECT pg_is_in_recovery()"`, " pg_is_in_recovery \n-------------------\n t\n(1 row)\n")
	ssh.setResponse(`psql -c "SHOW wal_level"`, " wal_level \n-----------\n replica\n(1 row)\n")
	ssh.setResponse(`psql -c "SHOW max_connections"`, " max_connections \n-----------------\n 200\n(1 row)\n")
	ssh.setResponse(`psql -c "SELECT count(*) FROM pg_database WHERE datistemplate = false"`, " count \n-------\n 3\n(1 row)\n")
	ssh.setResponse(`psql -c "SELECT pg_database_size(current_database()) / 1024 / 1024"`, " pg_database_size \n-----------------\n 2048\n(1 row)\n")

	ssh.setResponse(`redis-cli -p 6379 INFO server`, "redis_version:7.2.4\n")
	ssh.setResponse(`redis-cli -p 6379 INFO memory`, "used_memory:1048576\nmaxmemory:2097152\n")
	ssh.setResponse(`redis-cli -p 6379 INFO persistence`, "rdb_bgsave_in_progress:0\naof_enabled:1\n")
	ssh.setResponse(`redis-cli -p 6379 INFO keyspace`, "db0:keys=120,expires=60,avg_ttl=3600000\n")
	ssh.setResponse(`redis-cli -p 6379 KEYS "bull:*"`, "bull:queue:wait\nbull:queue:active\n")
	ssh.setResponse(`redis-cli -p 6379 KEYS "session:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 KEYS "sess:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 KEYS "rate_limit:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 KEYS "ratelimit:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 KEYS "lock:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 KEYS "redis-lock:*"`, "")
	ssh.setResponse(`redis-cli -p 6379 PUBSUB CHANNELS`, "")

	snapshot := &ServerSnapshot{
		Databases: []DatabaseInfo{
			{Type: "mysql", Port: 3306, Running: true},
			{Type: "postgresql", Port: 5432, Running: true},
			{Type: "redis", Port: 6379, Running: true},
		},
	}

	intelligence := AnalyzeDatabaseIntelligence(context.Background(), ssh, snapshot)
	if intelligence == nil {
		t.Fatalf("expected database intelligence")
	}
	if intelligence.MySQL == nil || intelligence.PostgreSQL == nil || intelligence.Redis == nil {
		t.Fatalf("expected all database intelligence modules: %#v", intelligence)
	}
	if !intelligence.MySQL.HasReplication || intelligence.MySQL.ReplicaRole != "slave" {
		t.Fatalf("unexpected MySQL replication info: %#v", intelligence.MySQL)
	}
	if intelligence.MySQL.GTIDMode != "ON" || !intelligence.MySQL.BinaryLogEnabled || intelligence.MySQL.ReadOnly {
		t.Fatalf("unexpected MySQL variable analysis: %#v", intelligence.MySQL)
	}
	if intelligence.MySQL.DatabaseCount != 2 || intelligence.MySQL.TotalSizeMB != 512 {
		t.Fatalf("unexpected MySQL database sizing: %#v", intelligence.MySQL)
	}
	if intelligence.MySQL.MigrationStrategy == "" || intelligence.MySQL.VerificationQuery == "" {
		t.Fatalf("expected MySQL migration guidance: %#v", intelligence.MySQL)
	}

	if !intelligence.PostgreSQL.HasReplication || intelligence.PostgreSQL.ReplicaRole != "secondary" {
		t.Fatalf("unexpected PostgreSQL replication info: %#v", intelligence.PostgreSQL)
	}
	if intelligence.PostgreSQL.WALLevel != "replica" || intelligence.PostgreSQL.MaxConnections != 200 {
		t.Fatalf("unexpected PostgreSQL variables: %#v", intelligence.PostgreSQL)
	}
	if intelligence.PostgreSQL.DatabaseCount != 3 || intelligence.PostgreSQL.TotalSizeMB != 2048 {
		t.Fatalf("unexpected PostgreSQL sizing: %#v", intelligence.PostgreSQL)
	}
	if intelligence.PostgreSQL.MigrationStrategy == "" || intelligence.PostgreSQL.VerificationQuery == "" {
		t.Fatalf("expected PostgreSQL migration guidance: %#v", intelligence.PostgreSQL)
	}

	if intelligence.Redis.Version != "7.2.4" {
		t.Fatalf("unexpected redis version: %#v", intelligence.Redis)
	}
	if intelligence.Redis.UsageType != "bullmq" || !intelligence.Redis.BullMQDetected {
		t.Fatalf("expected bullmq usage: %#v", intelligence.Redis)
	}
	if intelligence.Redis.PersistenceType != "aof" || !intelligence.Redis.HasPersistence {
		t.Fatalf("unexpected redis persistence: %#v", intelligence.Redis)
	}
	if !reflect.DeepEqual(intelligence.Redis.QueueNames, []string{"queue"}) {
		t.Fatalf("expected queue name extraction, got %#v", intelligence.Redis.QueueNames)
	}
	if len(intelligence.Redis.BullMQMigrationWorkflow()) != 6 {
		t.Fatalf("expected bullmq migration workflow steps")
	}
}

func TestDetectRedisUsagePatterns(t *testing.T) {
	cases := []struct {
		name string
		keys string
		want string
	}{
		{name: "bullmq", keys: "bull:jobs:1\n", want: "bullmq"},
		{name: "session", keys: "session:user:1\n", want: "session"},
		{name: "rate limiter", keys: "rate_limit:api:1\n", want: "rate_limiter"},
		{name: "lock", keys: "redis-lock:resource\n", want: "lock"},
		{name: "default unknown", keys: "", want: "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ssh := newMockSSH()
			ssh.setResponse(`redis-cli -p 6379 KEYS "bull:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "session:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "sess:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "rate_limit:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "ratelimit:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "lock:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 KEYS "redis-lock:*"`, "")
			ssh.setResponse(`redis-cli -p 6379 PUBSUB CHANNELS`, "")

			if tc.keys != "" {
				switch tc.want {
				case "bullmq":
					ssh.setResponse(`redis-cli -p 6379 KEYS "bull:*"`, tc.keys)
				case "session":
					ssh.setResponse(`redis-cli -p 6379 KEYS "session:*"`, tc.keys)
				case "rate_limiter":
					ssh.setResponse(`redis-cli -p 6379 KEYS "rate_limit:*"`, tc.keys)
				case "lock":
					ssh.setResponse(`redis-cli -p 6379 KEYS "redis-lock:*"`, tc.keys)
				}
			}

			got := detectRedisUsage(context.Background(), ssh, 6379)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
