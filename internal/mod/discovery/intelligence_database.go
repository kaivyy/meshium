package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DatabaseIntelligence holds deep database analysis results.
type DatabaseIntelligence struct {
	MySQL      *MySQLIntelligence      `json:"mysql,omitempty"`
	PostgreSQL *PostgreSQLIntelligence `json:"postgresql,omitempty"`
	Redis      *RedisIntelligence      `json:"redis,omitempty"`
	MongoDB    *MongoDBIntelligence    `json:"mongodb,omitempty"`
}

// MySQLIntelligence holds MySQL-specific migration intelligence.
type MySQLIntelligence struct {
	Version                string `json:"version,omitempty"`
	HasReplication         bool   `json:"hasReplication"`
	ReplicaRole            string `json:"replicaRole,omitempty"`
	DatabaseCount          int    `json:"databaseCount,omitempty"`
	TotalSizeMB            int64  `json:"totalSizeMb,omitempty"`
	InnoDBBufferPool       int    `json:"innodbBufferPool,omitempty"`
	BinaryLogEnabled       bool   `json:"binaryLogEnabled"`
	GTIDMode               string `json:"gtidMode,omitempty"`
	ReadOnly               bool   `json:"readOnly"`
	MigrationStrategy      string `json:"migrationStrategy,omitempty"`
	VerificationQuery      string `json:"verificationQuery,omitempty"`
	RollbackRecommendation string `json:"rollbackRecommendation,omitempty"`
}

// PostgreSQLIntelligence holds PostgreSQL-specific migration intelligence.
type PostgreSQLIntelligence struct {
	Version                string `json:"version,omitempty"`
	HasReplication         bool   `json:"hasReplication"`
	ReplicaRole            string `json:"replicaRole,omitempty"`
	DatabaseCount          int    `json:"databaseCount,omitempty"`
	TotalSizeMB            int64  `json:"totalSizeMb,omitempty"`
	WALLevel               string `json:"walLevel,omitempty"`
	MaxConnections         int    `json:"maxConnections,omitempty"`
	MigrationStrategy      string `json:"migrationStrategy,omitempty"`
	VerificationQuery      string `json:"verificationQuery,omitempty"`
	RollbackRecommendation string `json:"rollbackRecommendation,omitempty"`
}

// RedisIntelligence holds Redis-specific migration intelligence.
type RedisIntelligence struct {
	Version                string   `json:"version,omitempty"`
	UsageType              string   `json:"usageType"`
	MaxMemory              int64    `json:"maxMemory,omitempty"`
	UsedMemory             int64    `json:"usedMemory,omitempty"`
	KeysCount              int64    `json:"keysCount,omitempty"`
	HasPersistence         bool     `json:"hasPersistence"`
	PersistenceType        string   `json:"persistenceType,omitempty"`
	BullMQDetected         bool     `json:"bullmqDetected"`
	QueueNames             []string `json:"queueNames,omitempty"`
	MigrationStrategy      string   `json:"migrationStrategy,omitempty"`
	RollbackRecommendation string   `json:"rollbackRecommendation,omitempty"`
}

// MongoDBIntelligence holds MongoDB-specific migration intelligence.
type MongoDBIntelligence struct {
	Version           string `json:"version,omitempty"`
	HasReplication    bool   `json:"hasReplication"`
	ReplicaRole       string `json:"replicaRole,omitempty"`
	DatabaseCount     int    `json:"databaseCount,omitempty"`
	TotalSizeMB       int64  `json:"totalSizeMb,omitempty"`
	MigrationStrategy string `json:"migrationStrategy,omitempty"`
}

// AnalyzeDatabaseIntelligence performs deep analysis of database configuration.
func AnalyzeDatabaseIntelligence(ctx context.Context, client SSHExecuter, snapshot *ServerSnapshot) *DatabaseIntelligence {
	intel := &DatabaseIntelligence{}
	if client == nil || snapshot == nil {
		return intel
	}

	for _, db := range snapshot.Databases {
		switch strings.ToLower(strings.TrimSpace(db.Type)) {
		case "mysql", "mariadb":
			if intel.MySQL == nil {
				intel.MySQL = analyzeMySQLIntelligence(ctx, client, db)
			}
		case "postgresql", "postgres":
			if intel.PostgreSQL == nil {
				intel.PostgreSQL = analyzePostgreSQLIntelligence(ctx, client, db)
			}
		case "redis":
			if intel.Redis == nil {
				intel.Redis = analyzeRedisIntelligence(ctx, client, db)
			}
		case "mongodb", "mongo":
			if intel.MongoDB == nil {
				intel.MongoDB = analyzeMongoDBIntelligence(ctx, client, db)
			}
		}
	}

	return intel
}

func analyzeMySQLIntelligence(ctx context.Context, client SSHExecuter, db DatabaseInfo) *MySQLIntelligence {
	info := &MySQLIntelligence{}

	if out, err := execText(ctx, client, `mysql -e "SHOW VARIABLES LIKE 'version'"`); err == nil {
		info.Version = extractVersionString(out)
	}

	replicationOut := ""
	if out, err := execText(ctx, client, `mysql -e "SHOW SLAVE STATUS\G"`); err == nil {
		replicationOut = out
	}
	if replicationOut == "" {
		if out, err := execText(ctx, client, `mysql -e "SHOW REPLICA STATUS\G"`); err == nil {
			replicationOut = out
		}
	}
	if strings.TrimSpace(replicationOut) != "" && !strings.Contains(strings.ToLower(replicationOut), "empty set") {
		info.HasReplication = true
		info.ReplicaRole = "slave"
	}

	if out, err := execText(ctx, client, `mysql -e "SHOW VARIABLES LIKE 'gtid_mode'"`); err == nil {
		info.GTIDMode = extractMySQLVariableValue(out)
	}
	if out, err := execText(ctx, client, `mysql -e "SHOW VARIABLES LIKE 'log_bin'"`); err == nil {
		info.BinaryLogEnabled = parseMySQLBool(extractMySQLVariableValue(out))
	}
	if out, err := execText(ctx, client, `mysql -e "SHOW VARIABLES LIKE 'read_only'"`); err == nil {
		info.ReadOnly = parseMySQLBool(extractMySQLVariableValue(out))
	}
	if out, err := execText(ctx, client, `mysql -e "SHOW VARIABLES LIKE 'innodb_buffer_pool_size'"`); err == nil {
		if v, parseErr := strconv.Atoi(extractMySQLVariableValue(out)); parseErr == nil {
			info.InnoDBBufferPool = v
		}
	}
	if out, err := execText(ctx, client, `mysql -e "SHOW DATABASES"`); err == nil {
		info.DatabaseCount = countMySQLDatabases(out)
	}
	if out, err := execText(ctx, client, `mysql -e "SELECT ROUND(SUM(data_length + index_length) / 1024 / 1024) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')"`); err == nil {
		info.TotalSizeMB = extractFirstInt64(out)
	}

	if info.HasReplication {
		if info.GTIDMode == "ON" {
			info.MigrationStrategy = "use a GTID-aware replica promotion with a brief read-only cutover"
		} else {
			info.MigrationStrategy = "use replica catch-up, pause writes, and promote the target after final sync"
		}
		info.VerificationQuery = "SELECT 1"
		info.RollbackRecommendation = "keep the source primary available until validation passes, then re-point traffic back if needed"
	} else {
		info.MigrationStrategy = "perform an offline logical backup and restore during a maintenance window"
		info.VerificationQuery = "SELECT 1"
		info.RollbackRecommendation = "restore the pre-migration backup to the original host if validation fails"
	}

	if info.ReplicaRole == "" && db.Running {
		info.ReplicaRole = "master"
	}
	return info
}

func analyzePostgreSQLIntelligence(ctx context.Context, client SSHExecuter, db DatabaseInfo) *PostgreSQLIntelligence {
	info := &PostgreSQLIntelligence{}

	if out, err := execText(ctx, client, `psql -c "SELECT version()"`); err == nil {
		info.Version = extractVersionString(out)
	}
	if out, err := execText(ctx, client, `psql -c "SELECT pg_is_in_recovery()"`); err == nil {
		info.HasReplication = parsePostgresBoolValue(extractPsqlValue(out))
		if info.HasReplication {
			info.ReplicaRole = "secondary"
		} else {
			info.ReplicaRole = "primary"
		}
	}
	if out, err := execText(ctx, client, `psql -c "SHOW wal_level"`); err == nil {
		info.WALLevel = extractPsqlValue(out)
	}
	if out, err := execText(ctx, client, `psql -c "SHOW max_connections"`); err == nil {
		info.MaxConnections = int(extractFirstInt64(out))
	}
	if out, err := execText(ctx, client, `psql -c "SELECT count(*) FROM pg_database WHERE datistemplate = false"`); err == nil {
		info.DatabaseCount = int(extractFirstInt64(out))
	}
	if out, err := execText(ctx, client, `psql -c "SELECT pg_database_size(current_database()) / 1024 / 1024"`); err == nil {
		info.TotalSizeMB = extractFirstInt64(out)
	}

	if info.HasReplication {
		info.MigrationStrategy = "promote a synchronized replica after WAL catch-up and a short write freeze"
		info.VerificationQuery = "SELECT 1"
		info.RollbackRecommendation = "reconnect clients to the source primary and keep the original cluster intact until validation passes"
	} else {
		info.MigrationStrategy = "use pg_dump/pg_restore with a brief maintenance window"
		info.VerificationQuery = "SELECT 1"
		info.RollbackRecommendation = "restore the last base backup and replay WAL if the new target fails validation"
	}

	if info.ReplicaRole == "" && db.Running {
		info.ReplicaRole = "primary"
	}
	return info
}

func analyzeRedisIntelligence(ctx context.Context, client SSHExecuter, db DatabaseInfo) *RedisIntelligence {
	info := &RedisIntelligence{UsageType: "unknown"}
	port := db.Port

	if out, err := execText(ctx, client, redisCommand(port, `INFO server`)); err == nil {
		info.Version = extractRedisVersion(out)
	}
	if out, err := execText(ctx, client, redisCommand(port, `INFO memory`)); err == nil {
		info.UsedMemory, info.MaxMemory = parseRedisMemoryInfo(out)
	}
	if out, err := execText(ctx, client, redisCommand(port, `INFO keyspace`)); err == nil {
		info.KeysCount = parseRedisKeyspaceKeysCount(out)
	}
	if out, err := execText(ctx, client, redisCommand(port, `INFO persistence`)); err == nil {
		info.HasPersistence, info.PersistenceType = parseRedisPersistenceInfo(out)
	}

	info.UsageType = detectRedisUsage(ctx, client, port)
	info.BullMQDetected = info.UsageType == "bullmq"
	if info.BullMQDetected {
		if out, err := execText(ctx, client, redisCommand(port, `KEYS "bull:*"`)); err == nil {
			info.QueueNames = extractBullMQQueueNames(out)
		}
	}

	info.MigrationStrategy, info.RollbackRecommendation = buildRedisMigrationGuidance(info.UsageType, info.HasPersistence, info.PersistenceType)
	return info
}

func analyzeMongoDBIntelligence(ctx context.Context, client SSHExecuter, db DatabaseInfo) *MongoDBIntelligence {
	info := &MongoDBIntelligence{}

	if out, err := execText(ctx, client, `mongod --version`); err == nil {
		info.Version = extractVersionString(out)
	}

	replOut := ""
	for _, cmd := range []string{`mongosh --quiet --eval 'rs.status()' 2>/dev/null`, `mongo --quiet --eval 'rs.status()' 2>/dev/null`} {
		if out, err := execText(ctx, client, cmd); err == nil && strings.TrimSpace(out) != "" {
			replOut = out
			break
		}
	}
	if replOut != "" {
		lower := strings.ToLower(replOut)
		if strings.Contains(lower, "primary") || strings.Contains(lower, "secondary") || strings.Contains(lower, "replicaset") {
			info.HasReplication = true
			if strings.Contains(lower, "secondary") {
				info.ReplicaRole = "secondary"
			} else {
				info.ReplicaRole = "primary"
			}
		}
	}

	if info.HasReplication {
		info.MigrationStrategy = "sync a replica set member, validate elections, and promote the target after final catch-up"
	} else {
		info.MigrationStrategy = "take a consistent dump, restore on the target, and verify indexes before cutover"
	}
	if info.ReplicaRole == "" && db.Running {
		info.ReplicaRole = "primary"
	}
	return info
}

func redisCommand(port int, command string) string {
	if port > 0 {
		return fmt.Sprintf("redis-cli -p %d %s", port, command)
	}
	return "redis-cli " + command
}

func detectRedisUsage(ctx context.Context, client SSHExecuter, port int) string {
	if client == nil {
		return "unknown"
	}

	// Priority order: explicit patterns first.
	patterns := []struct {
		command string
		usage   string
	}{
		{`KEYS "bull:*"`, "bullmq"},
		{`KEYS "session:*"`, "session"},
		{`KEYS "sess:*"`, "session"},
		{`KEYS "rate_limit:*"`, "rate_limiter"},
		{`KEYS "ratelimit:*"`, "rate_limiter"},
		{`KEYS "lock:*"`, "lock"},
		{`KEYS "redis-lock:*"`, "lock"},
	}
	for _, pattern := range patterns {
		if out, err := execText(ctx, client, redisCommand(port, pattern.command)); err == nil && strings.TrimSpace(out) != "" {
			return pattern.usage
		}
	}

	if out, err := execText(ctx, client, redisCommand(port, `PUBSUB CHANNELS`)); err == nil && strings.TrimSpace(out) != "" {
		return "pubsub"
	}

	keyspaceOut, _ := execText(ctx, client, redisCommand(port, `INFO keyspace`))
	memoryOut, _ := execText(ctx, client, redisCommand(port, `INFO memory`))
	persistenceOut, _ := execText(ctx, client, redisCommand(port, `INFO persistence`))

	keys, expires, avgTTL := parseRedisKeyspaceStats(keyspaceOut)
	usedMemory, maxMemory := parseRedisMemoryInfo(memoryOut)
	aofEnabled, rdbEnabled := parseRedisPersistenceFlags(persistenceOut)

	if expires > 0 && avgTTL > 0 && (maxMemory == 0 || usedMemory < maxMemory/2) {
		return "cache"
	}
	if keys > 0 && expires == 0 && (aofEnabled || rdbEnabled) {
		return "queue"
	}
	return "unknown"
}

func (r *RedisIntelligence) BullMQMigrationWorkflow() []string {
	return []string{
		"1. Pause all BullMQ workers",
		"2. Wait for active jobs to complete",
		"3. Final Redis sync (RDB/AOF)",
		"4. Start new workers on target",
		"5. Verify queue processing",
		"6. Resume normal operations",
	}
}

func buildRedisMigrationGuidance(usageType string, hasPersistence bool, persistenceType string) (string, string) {
	switch usageType {
	case "bullmq":
		return "pause BullMQ workers, wait for active jobs to finish, sync Redis, and resume workers on the target", "keep the source Redis available and restart workers there if queue verification fails"
	case "session":
		return "perform a short cutover with session draining and a synchronized data snapshot", "switch traffic back to the source Redis if sessions do not deserialize cleanly on the target"
	case "rate_limiter":
		return "rebuild rate-limit state during a brief maintenance window", "restore the original Redis instance and clear stale limiter state if needed"
	case "lock":
		return "ensure all distributed locks are released before the cutover and rehydrate lock state on the target", "fall back to the source Redis and reinitialize locks if contention appears"
	case "queue":
		return "drain queues, capture a final persistence snapshot, and resume consumers on the target", "restore the source persistence snapshot and restart consumers on the original Redis if jobs stall"
	case "cache":
		return "warm the cache on the target after cutover and tolerate cache misses during the transition", "switch back to the source Redis and rebuild cache entries if hit rates degrade"
	case "pubsub":
		return "coordinate publisher and subscriber restarts to avoid message loss during the cutover", "restore the original pub/sub endpoints if subscription state is lost"
	default:
		if hasPersistence && persistenceType != "" {
			return "perform a persistence-aware Redis migration with a final synchronized snapshot", "restore the original persistence snapshot and route traffic back if validation fails"
		}
		return "perform a snapshot-and-restore Redis migration with a brief cutover window", "restore the last backup and restart consumers on the source Redis if the target misbehaves"
	}
}

func extractRedisVersion(output string) string {
	if match := versionPattern.FindString(output); match != "" {
		return match
	}
	return strings.TrimSpace(output)
}

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

func parseRedisMemoryInfo(output string) (usedMemory, maxMemory int64) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "used_memory:"):
			usedMemory = extractInt64AfterColon(line)
		case strings.HasPrefix(line, "maxmemory:"):
			maxMemory = extractInt64AfterColon(line)
		}
	}
	return usedMemory, maxMemory
}

func parseRedisKeyspaceStats(output string) (keys, expires, avgTTL int64) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "keys=") {
			continue
		}
		for _, field := range strings.Split(line, ",") {
			field = strings.TrimSpace(field)
			switch {
			case strings.HasPrefix(field, "keys="):
				keys += extractInt64AfterEquals(field)
			case strings.HasPrefix(field, "expires="):
				expires += extractInt64AfterEquals(field)
			case strings.HasPrefix(field, "avg_ttl="):
				avgTTL = extractInt64AfterEquals(field)
			}
		}
	}
	return keys, expires, avgTTL
}

func parseRedisKeyspaceKeysCount(output string) int64 {
	keys, _, _ := parseRedisKeyspaceStats(output)
	return keys
}

func parseRedisPersistenceInfo(output string) (bool, string) {
	aofEnabled, rdbEnabled := parseRedisPersistenceFlags(output)
	if aofEnabled && rdbEnabled {
		return true, "aof"
	}
	if aofEnabled {
		return true, "aof"
	}
	if rdbEnabled {
		return true, "rdb"
	}
	return false, ""
}

func parseRedisPersistenceFlags(output string) (aofEnabled, rdbEnabled bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "aof_enabled:"):
			aofEnabled = parseBoolish(extractStringAfterColon(line))
		case strings.HasPrefix(line, "rdb_bgsave_in_progress:"):
			rdbEnabled = rdbEnabled || parseBoolish(extractStringAfterColon(line))
		case strings.HasPrefix(line, "rdb_last_bgsave_status:"):
			rdbEnabled = rdbEnabled || strings.EqualFold(extractStringAfterColon(line), "ok")
		}
	}
	return aofEnabled, rdbEnabled
}

func extractBullMQQueueNames(output string) []string {
	var queues []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "bull:") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) >= 2 && parts[1] != "" {
			queues = append(queues, parts[1])
		}
	}
	return uniqueStrings(queues)
}

func countMySQLDatabases(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "database") {
			continue
		}
		lower := strings.ToLower(line)
		switch lower {
		case "mysql", "information_schema", "performance_schema", "sys":
			continue
		default:
			count++
		}
	}
	return count
}

func extractMySQLVariableValue(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "variable_name\tvalue") || strings.HasPrefix(strings.ToLower(line), "variable_name") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		return fields[len(fields)-1]
	}
	return ""
}

func parseMySQLBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "1", "yes", "true", "enabled":
		return true
	default:
		return false
	}
}

func extractPsqlValue(output string) string {
	var candidates []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "(") {
			continue
		}
		candidates = append(candidates, line)
	}
	if len(candidates) == 0 {
		return ""
	}
	return candidates[len(candidates)-1]
}

func parsePostgresBoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "t", "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func extractVersionString(output string) string {
	if match := versionPattern.FindString(output); match != "" {
		return match
	}
	return strings.TrimSpace(output)
}

func extractFirstInt64(output string) int64 {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if value, err := strconv.ParseInt(strings.Fields(line)[len(strings.Fields(line))-1], 10, 64); err == nil {
			return value
		}
		if value, err := strconv.ParseInt(line, 10, 64); err == nil {
			return value
		}
	}
	return 0
}

func extractLastNonEmptyToken(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") || strings.Contains(line, "(") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[len(fields)-1]
		}
	}
	return ""
}

func extractInt64AfterColon(line string) int64 {
	return extractInt64AfterDelimiter(line, ":")
}

func extractInt64AfterEquals(line string) int64 {
	return extractInt64AfterDelimiter(line, "=")
}

func extractInt64AfterDelimiter(line, delimiter string) int64 {
	parts := strings.SplitN(line, delimiter, 2)
	if len(parts) != 2 {
		return 0
	}
	value := strings.TrimSpace(parts[1])
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func extractStringAfterColon(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func parseBoolish(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "ok":
		return true
	default:
		return false
	}
}

// Ensure JSON imports stay justified when some analyses are compiled out in
// minimal test runs.
var _ = json.RawMessage{}
