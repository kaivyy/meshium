package migration

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Live replication stage. Only runs when replication is explicitly enabled;
// the default path skips it and the migration is a snapshot copy.
type liveReplicationStage struct {
	repo PipelineRepo
}

func (s *liveReplicationStage) Name() PipelineStageName { return StageLiveReplication }

// detectDatabases scans the source server for running database services.
// Returns a list of detected database types and their default ports.
func detectDatabases(ctx context.Context, ssh SSHExecuter) []DatabaseInfo {
	databases := make([]DatabaseInfo, 0)

	// Check for MySQL/MariaDB
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "mysql", Port: 3306})
	}

	// Check for PostgreSQL
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x postgres >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "postgres", Port: 5432})
	}

	// Check for Redis
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x redis-server >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "redis", Port: 6379})
	}

	// Check for MongoDB
	if _, _, exitCode, _ := ssh.ExecContext(ctx, "pgrep -x mongod >/dev/null 2>&1"); exitCode == 0 {
		databases = append(databases, DatabaseInfo{Type: "mongodb", Port: 27017})
	}

	return databases
}

func (s *liveReplicationStage) Execute(ctx context.Context, pc *PipelineContext) error {
	if !pc.Config.ReplicationEnabled {
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "Replication disabled, skipping"})
		return nil
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress", Value: "Scanning source server for databases..."})

	// Detect databases running on source
	databases := detectDatabases(ctx, pc.SourceSSH)
	if len(databases) == 0 {
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "No databases detected on source — replication not needed"})
		return nil
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
		Value: fmt.Sprintf("Detected %d database(s): %s", len(databases), formatDBList(databases))})

	// Create replication engine
	replEngine := NewReplicationEngine(pc.SourceSSH, pc.TargetSSH, pc.Repo)

	// Set up replication for each detected database
	for _, db := range databases {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		replConfig := ReplicationConfig{
			DatabaseType: db.Type,
			SourcePort:   db.Port,
			TargetPort:   db.Port,
			SourceHost:   pc.SourceServer.Host,
			TargetHost:   pc.TargetServer.Host,
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("Setting up %s replication %s:%d → %s:%d...", db.Type, replConfig.SourceHost, db.Port, replConfig.TargetHost, db.Port)})

		if err := replEngine.SetupReplication(ctx, pc.MigrationID, replConfig); err != nil {
			return fmt.Errorf("%s replication setup failed: %w", db.Type, err)
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success",
			Value: fmt.Sprintf("%s replication established", db.Type)})

		// Monitor initial lag
		lag, err := replEngine.MonitorLag(ctx, replConfig)
		if err != nil {
			return fmt.Errorf("%s lag monitoring failed: %w", db.Type, err)
		}

		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("%s replication lag: %d seconds", db.Type, lag)})

		// Wait for initial catch-up (max 5 minutes, lag threshold 30s)
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress",
			Value: fmt.Sprintf("Waiting for %s initial sync...", db.Type)})

		catchupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = replEngine.WaitForCatchUp(catchupCtx, replConfig, 30)
		cancel()
		if err != nil {
			return fmt.Errorf("%s initial sync failed: %w", db.Type, err)
		}
		pc.OnProgress(WSMessage{Step: "live_replication", Status: "success",
			Value: fmt.Sprintf("%s initial sync completed", db.Type)})
	}

	pc.OnProgress(WSMessage{Step: "live_replication", Status: "success", Value: "Live replication setup completed"})
	return nil
}

func (s *liveReplicationStage) Rollback(ctx context.Context, pc *PipelineContext) error {
	pc.OnProgress(WSMessage{Step: "live_replication", Status: "progress", Value: "Reverting replication..."})

	// Detect what databases were set up
	databases := detectDatabases(ctx, pc.SourceSSH)
	if len(databases) == 0 {
		return nil
	}

	replEngine := NewReplicationEngine(pc.SourceSSH, pc.TargetSSH, pc.Repo)
	for _, db := range databases {
		replConfig := ReplicationConfig{
			DatabaseType: db.Type,
			SourcePort:   db.Port,
			TargetPort:   db.Port,
			SourceHost:   pc.SourceServer.Host,
			TargetHost:   pc.TargetServer.Host,
		}
		if err := replEngine.Rollback(ctx, pc.MigrationID, replConfig); err != nil {
			pc.OnProgress(WSMessage{Step: "live_replication", Status: "warning",
				Value: fmt.Sprintf("%s replication rollback warning: %v", db.Type, err)})
		}
	}
	return nil
}

func formatDBList(dbs []DatabaseInfo) string {
	types := make([]string, len(dbs))
	for i, db := range dbs {
		types[i] = db.Type
	}
	return strings.Join(types, ", ")
}

// healthVerificationStage verifies that all services are healthy on the target.
