package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/server"

	xssh "golang.org/x/crypto/ssh"
)

// RollbackManager restores the target server to its pre-migration state.
type RollbackManager struct {
	registry *CategoryRegistry
	repo     Repo
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

// NewRollbackManager creates a RollbackManager.
func NewRollbackManager(
	registry *CategoryRegistry,
	repo Repo,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
) *RollbackManager {
	return &RollbackManager{
		registry: registry,
		repo:     repo,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
	}
}

// Rollback restores the target server using stored backup data.
func (rm *RollbackManager) Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Load the migration
	migration, err := rm.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "rollback", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	// 2. Update status to rolling_back
	rm.repo.UpdateMigrationStatus(migrationID, StatusRollingBack, "")
	onProgress(WSMessage{Step: "rollback", Status: "progress", Value: "Starting rollback..."})

	// 3. Get SSH connection to target
	targetServer, err := rm.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		sendError(onProgress, "rollback", "target server not found")
		rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "target server not found")
		return fmt.Errorf("target server not found: %w", err)
	}

	sshClient, err := rm.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		sendError(onProgress, "rollback", "failed to connect to target: "+err.Error())
		rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "SSH connection failed")
		return fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "rollback", Status: "success", Value: "Connected to target server"})

	// 4. Load backups
	backups, err := rm.repo.GetBackups(migrationID)
	if err != nil {
		sendError(onProgress, "rollback", "failed to load backups")
		rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "failed to load backups")
		return err
	}

	if len(backups) == 0 {
		sendError(onProgress, "rollback", "no backups found for migration")
		rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "no backups found")
		return fmt.Errorf("no backups found for migration %d", migrationID)
	}

	// 5. Rollback each category (in reverse order)
	for i := len(backups) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "cancelled")
			return ctx.Err()
		}

		backup := backups[i]
		mod, ok := rm.registry.Get(backup.Category)
		if !ok {
			continue
		}

		onProgress(WSMessage{
			Step:   "rollback:" + backup.Category,
			Status: "progress",
			Value:  "Rolling back " + backup.Category + "...",
		})

		// Parse backup data
		var backupData BackupData
		if err := json.Unmarshal([]byte(backup.Data), &backupData); err != nil {
			onProgress(WSMessage{
				Step:   "rollback:" + backup.Category,
				Status: "error",
				Error:  "failed to parse backup data",
			})
			continue
		}

		// Rollback
		err := mod.Applier.Rollback(sshClient, backupData)
		if err != nil {
			onProgress(WSMessage{
				Step:   "rollback:" + backup.Category,
				Status: "error",
				Error:  fmt.Sprintf("rollback failed: %v", err),
			})
			// Continue with other categories even if one fails
			continue
		}

		onProgress(WSMessage{
			Step:   "rollback:" + backup.Category,
			Status: "success",
			Value:  "Rolled back " + backup.Category,
		})
	}

	// 6. Update status to rolled_back
	now := time.Now().Format(time.RFC3339)
	rm.repo.UpdateMigrationStatus(migrationID, StatusRolledBack, "")
	rm.repo.SetMigrationRolledBackAt(migrationID, now)

	onProgress(WSMessage{Step: "rollback", Status: "complete", Value: "Rollback completed"})

	return nil
}

// getSSHClient obtains an SSH connection for the given server.
func (rm *RollbackManager) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, rm.srvRepo, rm.pool, rm.authSvc, rm.hosts)
}

var _ xssh.HostKeyCallback
