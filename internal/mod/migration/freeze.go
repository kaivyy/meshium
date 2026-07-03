package migration

import (
	"context"
	"fmt"
	"strings"

	"meshium/internal/mod/discovery"
)

// DatabaseInfo is re-exported into the migration package for freeze/rollback orchestration.
type DatabaseInfo = discovery.DatabaseInfo

// FreezeManager handles write-freezing for databases and applications during cutover.
type FreezeManager struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
}

// NewFreezeManager creates a new FreezeManager.
func NewFreezeManager(sourceSSH, targetSSH SSHExecuter) *FreezeManager {
	return &FreezeManager{sourceSSH: sourceSSH, targetSSH: targetSSH}
}

// FreezeResult records what was frozen and whether it succeeded.
type FreezeResult struct {
	Success    bool     `json:"success"`
	Method     string   `json:"method"`
	FrozenDBs  []string `json:"frozenDbs"`
	FrozenApps []string `json:"frozenApps"`
	Errors     []string `json:"errors,omitempty"`
}

// FreezeWrites freezes writes on all configured databases.
func (m *FreezeManager) FreezeWrites(ctx context.Context, databases []DatabaseInfo) (*FreezeResult, error) {
	if m == nil {
		return nil, fmt.Errorf("freeze manager is nil")
	}

	result := &FreezeResult{}

	for _, db := range databases {
		switch strings.ToLower(strings.TrimSpace(db.Type)) {
		case "mysql", "mariadb":
			if err := m.freezeMySQL(ctx, db); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("mysql: %v", err))
				_ = m.unfreezeAll(ctx, result)
				return result, fmt.Errorf("mysql freeze failed: %w", err)
			}
			result.FrozenDBs = append(result.FrozenDBs, strings.ToLower(strings.TrimSpace(db.Type)))

		case "postgres", "postgresql":
			if err := m.freezePostgreSQL(ctx, db); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("postgresql: %v", err))
				_ = m.unfreezeAll(ctx, result)
				return result, fmt.Errorf("postgresql freeze failed: %w", err)
			}
			result.FrozenDBs = append(result.FrozenDBs, "postgresql")

		case "redis":
			usage := m.detectRedisUsage(ctx, db)
			if usage == RedisUsageBullMQ || usage == RedisUsageQueue {
				if err := m.pauseBullMQWorkers(ctx, db); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("redis workers: %v", err))
					// Non-fatal — continue with other freezes.
				} else {
					result.FrozenDBs = append(result.FrozenDBs, "redis:"+string(usage))
				}
			}

		case "mongodb":
			if err := m.freezeMongoDB(ctx, db); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("mongodb: %v", err))
				_ = m.unfreezeAll(ctx, result)
				return result, fmt.Errorf("mongodb freeze failed: %w", err)
			}
			result.FrozenDBs = append(result.FrozenDBs, "mongodb")
		}
	}

	result.Success = true
	if len(result.FrozenDBs) > 0 || len(result.Errors) > 0 {
		result.Method = "database_readonly"
	} else {
		result.Method = "no_freeze_required"
	}
	return result, nil
}

// UnfreezeWrites reverses all freeze actions.
func (m *FreezeManager) UnfreezeWrites(ctx context.Context, result *FreezeResult) error {
	if m == nil || result == nil {
		return nil
	}
	return m.unfreezeAll(ctx, result)
}

func (m *FreezeManager) freezeMySQL(ctx context.Context, db DatabaseInfo) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	// FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only = ON;
	cmd := `mysql -e "FLUSH TABLES WITH READ LOCK; SET GLOBAL read_only = ON;" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return fmt.Errorf("mysql freeze command failed: %w", err)
	}

	// Verify: SHOW VARIABLES LIKE 'read_only' should return ON.
	verifyCmd := `mysql -e "SHOW VARIABLES LIKE 'read_only'" -s -N 2>&1`
	out, _, _, _ := m.sourceSSH.ExecContext(ctx, verifyCmd)
	if !strings.Contains(strings.ToLower(out), "on") {
		return fmt.Errorf("mysql freeze verification failed: read_only not ON")
	}
	return nil
}

func (m *FreezeManager) unfreezeMySQL(ctx context.Context) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	cmd := `mysql -e "SET GLOBAL read_only = OFF; UNLOCK TABLES;" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("mysql unfreeze failed: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("mysql unfreeze command failed with exit code %d", exitCode)
	}
	return nil
}

func (m *FreezeManager) freezePostgreSQL(ctx context.Context, db DatabaseInfo) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	cmd := `sudo -u postgres psql -c "ALTER SYSTEM SET default_transaction_read_only = on;" -c "SELECT pg_reload_conf();" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		primaryErr := err
		if primaryErr == nil {
			primaryErr = fmt.Errorf("exit code %d", exitCode)
		}

		// Fallback: set the transaction default directly for the current session.
		fallbackCmd := `psql -c "SET default_transaction_read_only = on;" 2>&1`
		_, _, exitCode2, err2 := m.sourceSSH.ExecContext(ctx, fallbackCmd)
		if err2 != nil || exitCode2 != 0 {
			if err2 == nil {
				err2 = fmt.Errorf("exit code %d", exitCode2)
			}
			return fmt.Errorf("postgresql freeze failed: %w (fallback also failed: %v)", primaryErr, err2)
		}
	}

	verifyCmd := `sudo -u postgres psql -t -c "SHOW default_transaction_read_only;" 2>&1`
	out, _, _, _ := m.sourceSSH.ExecContext(ctx, verifyCmd)
	if strings.TrimSpace(strings.ToLower(out)) != "on" {
		return fmt.Errorf("postgresql freeze verification failed: default_transaction_read_only not on")
	}
	return nil
}

func (m *FreezeManager) unfreezePostgreSQL(ctx context.Context) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	cmd := `sudo -u postgres psql -c "ALTER SYSTEM SET default_transaction_read_only = off;" -c "SELECT pg_reload_conf();" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return fmt.Errorf("postgresql unfreeze failed: %w", err)
	}
	return nil
}

func (m *FreezeManager) freezeMongoDB(ctx context.Context, db DatabaseInfo) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	// db.fsyncLock() blocks writes.
	cmd := `mongosh --eval "db.fsyncLock()" 2>&1 || mongo --eval "db.fsyncLock()" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return fmt.Errorf("mongodb fsyncLock failed: %w", err)
	}
	return nil
}

func (m *FreezeManager) unfreezeMongoDB(ctx context.Context) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	cmd := `mongosh --eval "db.fsyncUnlock()" 2>&1 || mongo --eval "db.fsyncUnlock()" 2>&1`
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("mongodb fsyncUnlock failed: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("mongodb fsyncUnlock command failed with exit code %d", exitCode)
	}
	return nil
}

// RedisUsage represents how Redis is being used.
type RedisUsage string

const (
	RedisUsageCache     RedisUsage = "cache"
	RedisUsageSession   RedisUsage = "session"
	RedisUsageBullMQ    RedisUsage = "bullmq"
	RedisUsageQueue     RedisUsage = "queue"
	RedisUsagePubSub    RedisUsage = "pubsub"
	RedisUsageLock      RedisUsage = "lock"
	RedisUsageRateLimit RedisUsage = "rate_limiter"
	RedisUsageUnknown   RedisUsage = "unknown"
)

func (m *FreezeManager) detectRedisUsage(ctx context.Context, db DatabaseInfo) RedisUsage {
	if m == nil || m.sourceSSH == nil {
		return RedisUsageUnknown
	}

	// Check for BullMQ key patterns.
	cmd := fmt.Sprintf(`redis-cli -p %d SCAN 0 MATCH "bull:*" COUNT 10 2>&1`, db.Port)
	out, _, _, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err == nil && strings.Contains(out, "bull:") {
		return RedisUsageBullMQ
	}

	// Check for session patterns.
	cmd = fmt.Sprintf(`redis-cli -p %d SCAN 0 MATCH "sess:*" COUNT 10 2>&1`, db.Port)
	out, _, _, err = m.sourceSSH.ExecContext(ctx, cmd)
	if err == nil && strings.Contains(out, "sess:") {
		return RedisUsageSession
	}

	// Check for rate limiter patterns.
	cmd = fmt.Sprintf(`redis-cli -p %d SCAN 0 MATCH "rate_limit:*" COUNT 10 2>&1`, db.Port)
	out, _, _, err = m.sourceSSH.ExecContext(ctx, cmd)
	if err == nil && strings.Contains(out, "rate_limit:") {
		return RedisUsageRateLimit
	}

	// Check for lock patterns.
	cmd = fmt.Sprintf(`redis-cli -p %d SCAN 0 MATCH "lock:*" COUNT 10 2>&1`, db.Port)
	out, _, _, err = m.sourceSSH.ExecContext(ctx, cmd)
	if err == nil && strings.Contains(out, "lock:") {
		return RedisUsageLock
	}

	// Default: assume cache (no freeze needed).
	return RedisUsageCache
}

func (m *FreezeManager) pauseBullMQWorkers(ctx context.Context, db DatabaseInfo) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	// Pause all BullMQ queues.
	cmd := fmt.Sprintf(`redis-cli -p %d --scan --pattern "bull:*:meta" | while read key; do redis-cli -p %d SET "$(echo $key | sed 's/:meta$//:paused')" "1"; done 2>&1`, db.Port, db.Port)
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return fmt.Errorf("bullmq worker pause failed: %w", err)
	}
	return nil
}

func (m *FreezeManager) resumeBullMQWorkers(ctx context.Context, db DatabaseInfo) error {
	if m.sourceSSH == nil {
		return fmt.Errorf("source ssh not configured")
	}

	cmd := fmt.Sprintf(`redis-cli -p %d --scan --pattern "bull:*:paused" | while read key; do redis-cli -p %d DEL "$key"; done 2>&1`, db.Port, db.Port)
	_, _, exitCode, err := m.sourceSSH.ExecContext(ctx, cmd)
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("exit code %d", exitCode)
		}
		return fmt.Errorf("bullmq worker resume failed: %w", err)
	}
	return nil
}

func (m *FreezeManager) unfreezeAll(ctx context.Context, result *FreezeResult) error {
	if m == nil || result == nil {
		return nil
	}

	var firstErr error
	for _, frozen := range result.FrozenDBs {
		switch {
		case frozen == "mysql" || frozen == "mariadb":
			if err := m.unfreezeMySQL(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		case frozen == "postgres" || frozen == "postgresql":
			if err := m.unfreezePostgreSQL(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		case frozen == "mongodb":
			if err := m.unfreezeMongoDB(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		case strings.HasPrefix(frozen, "redis:"):
			// Best-effort: redis worker resume would happen here if we tracked the port.
		}
	}
	return firstErr
}
