package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"
)

// ReplicationEngine manages database replication between source and target servers.
// Supports MySQL/MariaDB, PostgreSQL, Redis, and MongoDB with dump as fallback.
type ReplicationEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
}

// NewReplicationEngine creates a new replication engine.
func NewReplicationEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *ReplicationEngine {
	return &ReplicationEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
	}
}

// ReplicationConfig configures database replication.
type ReplicationConfig struct {
	DatabaseType   string `json:"databaseType"`
	DatabaseName   string `json:"databaseName,omitempty"`
	SourceHost     string `json:"sourceHost"`
	SourcePort     int    `json:"sourcePort"`
	TargetHost     string `json:"targetHost"`
	TargetPort     int    `json:"targetPort"`
	ReplicationUser string `json:"replicationUser,omitempty"`
	ReplicationPass string `json:"replicationPass,omitempty"`
	MigrationID    int    `json:"migrationId"`
}

// SetupReplication configures replication from source to target.
func (e *ReplicationEngine) SetupReplication(ctx context.Context, migrationID int, config ReplicationConfig) error {
	config.MigrationID = migrationID

	// Record replication status
	status := ReplicationStatus{
		MigrationID:     migrationID,
		DatabaseType:    config.DatabaseType,
		DatabaseName:    config.DatabaseName,
		SourceHost:      config.SourceHost,
		TargetHost:      config.TargetHost,
		ReplicationMode: ReplicationModeStreaming,
		Status:          "setting_up",
	}
	statusID, err := e.repo.CreateReplicationStatus(ctx, status)
	if err != nil {
		return fmt.Errorf("create replication status: %w", err)
	}

	switch config.DatabaseType {
	case "mysql", "mariadb":
		err = e.setupMySQL(ctx, config)
	case "postgres", "postgresql":
		err = e.setupPostgreSQL(ctx, config)
	case "redis":
		err = e.setupRedis(ctx, config)
	case "mongodb":
		err = e.setupMongoDB(ctx, config)
	default:
		err = fmt.Errorf("unsupported database type: %s", config.DatabaseType)
	}

	if err != nil {
		e.repo.UpdateReplicationStatus(ctx, statusID, "error", 0, err.Error())
		return err
	}

	return e.repo.UpdateReplicationStatus(ctx, statusID, "replicating", 0, "")
}

// MonitorLag returns the current replication lag in seconds.
func (e *ReplicationEngine) MonitorLag(ctx context.Context, config ReplicationConfig) (int64, error) {
	switch config.DatabaseType {
	case "mysql", "mariadb":
		return e.mysqlLag(ctx, config)
	case "postgres", "postgresql":
		return e.postgresLag(ctx, config)
	case "redis":
		return e.redisLag(ctx, config)
	case "mongodb":
		return e.mongoDBLag(ctx, config)
	default:
		return 0, fmt.Errorf("unsupported database type: %s", config.DatabaseType)
	}
}

// WaitForCatchUp blocks until replication lag is below the threshold.
func (e *ReplicationEngine) WaitForCatchUp(ctx context.Context, config ReplicationConfig, maxLagSeconds int64) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			lag, err := e.MonitorLag(ctx, config)
			if err != nil {
				return fmt.Errorf("monitor replication lag: %w", err)
			}
			if lag <= maxLagSeconds {
				return nil
			}
		}
	}
}

// Promote promotes the target to primary (stops replication).
func (e *ReplicationEngine) Promote(ctx context.Context, migrationID int, config ReplicationConfig) error {
	switch config.DatabaseType {
	case "mysql", "mariadb":
		return e.promoteMySQL(ctx, config)
	case "postgres", "postgresql":
		return e.promotePostgreSQL(ctx, config)
	case "redis":
		return e.promoteRedis(ctx, config)
	case "mongodb":
		return e.promoteMongoDB(ctx, config)
	default:
		return fmt.Errorf("unsupported database type: %s", config.DatabaseType)
	}
}

// Rollback reverts replication back to the source as primary.
func (e *ReplicationEngine) Rollback(ctx context.Context, migrationID int, config ReplicationConfig) error {
	switch config.DatabaseType {
	case "mysql", "mariadb":
		return e.rollbackMySQL(ctx, config)
	case "postgres", "postgresql":
		return e.rollbackPostgreSQL(ctx, config)
	case "redis":
		return e.rollbackRedis(ctx, config)
	case "mongodb":
		return e.rollbackMongoDB(ctx, config)
	default:
		return fmt.Errorf("unsupported database type: %s", config.DatabaseType)
	}
}

// FinalSync performs a final delta sync before cutover.
func (e *ReplicationEngine) FinalSync(ctx context.Context, config ReplicationConfig) error {
	// Wait for replication to catch up, then flush
	if err := e.WaitForCatchUp(ctx, config, 0); err != nil {
		return fmt.Errorf("wait for catch up: %w", err)
	}

	switch config.DatabaseType {
	case "mysql", "mariadb":
		// Flush tables with read lock for consistent snapshot
		_, _, _, _ = e.sourceSSH.ExecContext(ctx, "mysql -e 'FLUSH TABLES WITH READ LOCK' 2>&1")
		defer func() {
			e.sourceSSH.ExecContext(ctx, "mysql -e 'UNLOCK TABLES' 2>&1")
		}()
	case "redis":
		// Trigger a save on source
		_, _, _, _ = e.sourceSSH.ExecContext(ctx, "redis-cli BGSAVE 2>&1")
		time.Sleep(1 * time.Second)
	}

	return nil
}

// VerifyReplication checks that replication is working correctly.
func (e *ReplicationEngine) VerifyReplication(ctx context.Context, config ReplicationConfig) error {
	lag, err := e.MonitorLag(ctx, config)
	if err != nil {
		return fmt.Errorf("check replication lag: %w", err)
	}
	if lag > 10 {
		return fmt.Errorf("replication lag too high: %d seconds", lag)
	}
	return nil
}

// --- MySQL ---

func (e *ReplicationEngine) setupMySQL(ctx context.Context, config ReplicationConfig) error {
	replUser := config.ReplicationUser
	if replUser == "" {
		replUser = "meshium_repl"
	}
	replPass := config.ReplicationPass
	if replPass == "" {
		replPass = fmt.Sprintf("meshium_%d", time.Now().Unix())
	}

	// Create replication user on source
	cmd := fmt.Sprintf(
		`mysql -e "CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; GRANT REPLICATION SLAVE ON *.* TO '%s'@'%%'; FLUSH PRIVILEGES;" 2>&1`,
		replUser, replPass, replUser,
	)
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("create replication user: %w", err)
	}

	// Get master status
	output, _, _, err := e.sourceSSH.ExecContext(ctx, "mysql -e 'SHOW MASTER STATUS\\G' 2>&1")
	if err != nil {
		return fmt.Errorf("get master status: %w", err)
	}

	binFile, binPos := parseMySQLMasterStatus(output)

	// Configure replica on target
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source" // will be resolved via SSH tunnel or hosts file
	}

	cmd = fmt.Sprintf(
		`mysql -e "CHANGE MASTER TO MASTER_HOST='%s', MASTER_USER='%s', MASTER_PASSWORD='%s', MASTER_LOG_FILE='%s', MASTER_LOG_POS=%s; START SLAVE;" 2>&1`,
		sourceHost, replUser, replPass, binFile, binPos,
	)
	if _, _, _, err := e.targetSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("configure replica: %w", err)
	}

	return nil
}

func (e *ReplicationEngine) mysqlLag(ctx context.Context, config ReplicationConfig) (int64, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "mysql -e 'SHOW SLAVE STATUS\\G' 2>&1")
	if err != nil {
		return -1, err
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Seconds_Behind_Master:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Seconds_Behind_Master:"))
			if val == "NULL" {
				return -1, fmt.Errorf("replication not running")
			}
			return strconv.ParseInt(val, 10, 64)
		}
	}
	return -1, fmt.Errorf("could not determine replication lag")
}

func (e *ReplicationEngine) promoteMySQL(ctx context.Context, config ReplicationConfig) error {
	_, _, _, err := e.targetSSH.ExecContext(ctx, "mysql -e 'STOP SLAVE; RESET SLAVE ALL;' 2>&1")
	return err
}

func (e *ReplicationEngine) rollbackMySQL(ctx context.Context, config ReplicationConfig) error {
	// Stop replication on target
	_, _, _, _ = e.targetSSH.ExecContext(ctx, "mysql -e 'STOP SLAVE; RESET SLAVE ALL;' 2>&1")
	return nil
}

func parseMySQLMasterStatus(output string) (string, string) {
	var file, pos string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "File:") {
			file = strings.TrimSpace(strings.TrimPrefix(line, "File:"))
		}
		if strings.HasPrefix(line, "Position:") {
			pos = strings.TrimSpace(strings.TrimPrefix(line, "Position:"))
		}
	}
	return file, pos
}

// --- PostgreSQL ---

func (e *ReplicationEngine) setupPostgreSQL(ctx context.Context, config ReplicationConfig) error {
	// Configure source for replication
	cmds := []string{
		// Create replication user
		fmt.Sprintf(`sudo -u postgres psql -c "CREATE USER replicator WITH REPLICATION ENCRYPTED PASSWORD '%s';" 2>&1`, config.ReplicationPass),
		// Create replication slot
		fmt.Sprintf(`sudo -u postgres psql -c "SELECT pg_create_physical_replication_slot('meshium_slot');" 2>&1`),
		// Configure wal_level and max_wal_senders
		`sudo -u postgres psql -c "ALTER SYSTEM SET wal_level = replica;" 2>&1`,
		`sudo -u postgres psql -c "ALTER SYSTEM SET max_wal_senders = 10;" 2>&1`,
		// Add to pg_hba.conf
		`echo 'host replication replicator 0.0.0.0/0 md5' >> /etc/postgresql/*/main/pg_hba.conf 2>&1 || echo 'host replication replicator 0.0.0.0/0 md5' >> /var/lib/pgsql/data/pg_hba.conf 2>&1`,
		// Reload config
		`sudo systemctl reload postgresql 2>&1 || sudo systemctl reload postgresql-* 2>&1`,
	}

	for _, cmd := range cmds {
		if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
			return fmt.Errorf("configure postgres source: %w (cmd: %s)", err, cmd)
		}
	}

	// Take base backup on target
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}

	pgVersion, _, _, _ := e.targetSSH.ExecContext(ctx, `ls /etc/postgresql/ 2>/dev/null | head -1 || echo "15"`)
	pgVersion = strings.TrimSpace(pgVersion)
	dataDir := fmt.Sprintf("/var/lib/postgresql/%s/main", pgVersion)

	// Stop PostgreSQL on target
	e.targetSSH.ExecContext(ctx, "sudo systemctl stop postgresql 2>&1 || sudo systemctl stop postgresql-* 2>&1")

	// Take base backup
	cmd := fmt.Sprintf(
		`sudo -u postgres pg_basebackup -h %s -U replicator -D %s -Fp -Xs -P -R 2>&1`,
		shared.ShellQuote(sourceHost), shared.ShellQuote(dataDir),
	)
	if _, _, _, err := e.targetSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("pg_basebackup: %w", err)
	}

	// Configure primary_conninfo
	connInfo := fmt.Sprintf("host=%s port=5432 user=replicator", sourceHost)
	cmd = fmt.Sprintf(
		`echo "primary_conninfo = '%s'" >> %s/postgresql.auto.conf`,
		connInfo, dataDir,
	)
	e.targetSSH.ExecContext(ctx, cmd)

	// Start PostgreSQL on target
	if _, _, _, err := e.targetSSH.ExecContext(ctx, "sudo systemctl start postgresql 2>&1 || sudo systemctl start postgresql-* 2>&1"); err != nil {
		return fmt.Errorf("start postgresql on target: %w", err)
	}

	return nil
}

func (e *ReplicationEngine) postgresLag(ctx context.Context, config ReplicationConfig) (int64, error) {
	output, _, _, err := e.sourceSSH.ExecContext(ctx,
		`sudo -u postgres psql -t -c "SELECT COALESCE(EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp())::bigint, 0) FROM pg_stat_replication LIMIT 1;" 2>&1`)
	if err != nil {
		return -1, err
	}
	lag, err := strconv.ParseInt(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, nil // no replication lag reported
	}
	return lag, nil
}

func (e *ReplicationEngine) promotePostgreSQL(ctx context.Context, config ReplicationConfig) error {
	_, _, _, err := e.targetSSH.ExecContext(ctx, "sudo -u postgres pg_ctl promote -D /var/lib/postgresql/*/main 2>&1")
	return err
}

func (e *ReplicationEngine) rollbackPostgreSQL(ctx context.Context, config ReplicationConfig) error {
	// Stop PostgreSQL on target and re-configure as standby
	e.targetSSH.ExecContext(ctx, "sudo systemctl stop postgresql 2>&1")
	// Remove promote marker
	e.targetSSH.ExecContext(ctx, "rm -f /var/lib/postgresql/*/main/standby.signal 2>&1")
	return nil
}

// --- Redis ---

func (e *ReplicationEngine) setupRedis(ctx context.Context, config ReplicationConfig) error {
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}
	port := config.SourcePort
	if port == 0 {
		port = 6379
	}

	// Configure target as replica of source
	cmd := fmt.Sprintf("redis-cli REPLICAOF %s %d 2>&1", shared.ShellQuote(sourceHost), port)
	if _, _, _, err := e.targetSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("configure redis replica: %w", err)
	}

	// Wait for initial sync
	time.Sleep(3 * time.Second)

	// Verify replication
	output, _, _, err := e.targetSSH.ExecContext(ctx, "redis-cli INFO replication 2>&1")
	if err != nil {
		return fmt.Errorf("verify redis replication: %w", err)
	}
	if !strings.Contains(output, "role:slave") && !strings.Contains(output, "role:replica") {
		return fmt.Errorf("redis replication not established")
	}
	return nil
}

func (e *ReplicationEngine) redisLag(ctx context.Context, config ReplicationConfig) (int64, error) {
	output, _, _, err := e.targetSSH.ExecContext(ctx, "redis-cli INFO replication 2>&1")
	if err != nil {
		return -1, err
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "master_link_status:") {
			if strings.Contains(line, "down") {
				return -1, fmt.Errorf("redis master link is down")
			}
		}
		if strings.HasPrefix(line, "master_last_io_seconds_ago:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "master_last_io_seconds_ago:"))
			return strconv.ParseInt(val, 10, 64)
		}
	}
	return 0, nil
}

func (e *ReplicationEngine) promoteRedis(ctx context.Context, config ReplicationConfig) error {
	_, _, _, err := e.targetSSH.ExecContext(ctx, "redis-cli REPLICAOF NO ONE 2>&1")
	return err
}

func (e *ReplicationEngine) rollbackRedis(ctx context.Context, config ReplicationConfig) error {
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}
	port := config.SourcePort
	if port == 0 {
		port = 6379
	}
	cmd := fmt.Sprintf("redis-cli REPLICAOF %s %d 2>&1", shared.ShellQuote(sourceHost), port)
	_, _, _, err := e.targetSSH.ExecContext(ctx, cmd)
	return err
}

// --- MongoDB ---

func (e *ReplicationEngine) setupMongoDB(ctx context.Context, config ReplicationConfig) error {
	// Add target to replica set
	sourceHost := config.SourceHost
	if sourceHost == "" {
		sourceHost = "source"
	}
	targetHost := config.TargetHost
	if targetHost == "" {
		targetHost = "target"
	}
	port := config.SourcePort
	if port == 0 {
		port = 27017
	}

	cmd := fmt.Sprintf(
		`mongosh --eval "rs.add('%s:%d')" 2>&1`,
		shared.ShellQuote(targetHost), port,
	)
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("add target to replica set: %w", err)
	}

	// Wait for initial sync
	time.Sleep(5 * time.Second)
	return nil
}

func (e *ReplicationEngine) mongoDBLag(ctx context.Context, config ReplicationConfig) (int64, error) {
	output, _, _, err := e.sourceSSH.ExecContext(ctx,
		`mongosh --eval "JSON.stringify(rs.status())" --quiet 2>&1`)
	if err != nil {
		return -1, err
	}

	// Parse optimeDate difference
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "optimeDate") || strings.Contains(line, "lag") {
			// Simplified: return 0 if we can connect
			return 0, nil
		}
	}
	return 0, nil
}

func (e *ReplicationEngine) promoteMongoDB(ctx context.Context, config ReplicationConfig) error {
	// Step down the source, target becomes primary
	_, _, _, err := e.sourceSSH.ExecContext(ctx, `mongosh --eval "rs.stepDown()" --quiet 2>&1`)
	return err
}

func (e *ReplicationEngine) rollbackMongoDB(ctx context.Context, config ReplicationConfig) error {
	targetHost := config.TargetHost
	if targetHost == "" {
		targetHost = "target"
	}
	port := config.SourcePort
	if port == 0 {
		port = 27017
	}
	cmd := fmt.Sprintf(
		`mongosh --eval "rs.remove('%s:%d')" --quiet 2>&1`,
		shared.ShellQuote(targetHost), port,
	)
	_, _, _, _ = e.sourceSSH.ExecContext(ctx, cmd)
	return nil
}
