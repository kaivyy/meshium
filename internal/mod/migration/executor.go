package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/server"

	xssh "golang.org/x/crypto/ssh"
)

// Executor applies a migration plan to the target server.
type Executor struct {
	registry *CategoryRegistry
	repo     Repo
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

// NewExecutor creates an Executor.
func NewExecutor(
	registry *CategoryRegistry,
	repo Repo,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
) *Executor {
	return &Executor{
		registry: registry,
		repo:     repo,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
	}
}

// Registry returns the category registry.
func (e *Executor) Registry() *CategoryRegistry { return e.registry }

// SrvRepo returns the server repo.
func (e *Executor) SrvRepo() server.Repo { return e.srvRepo }

// Pool returns the connection pool.
func (e *Executor) Pool() ConnectionPool { return e.pool }

// AuthSvc returns the AES key provider.
func (e *Executor) AuthSvc() AESKeyProvider { return e.authSvc }

// Hosts returns the host key store.
func (e *Executor) Hosts() HostKeyStore { return e.hosts }

// Execute applies a migration plan to the target server.
// It runs backups for each category, then applies collected data.
func (e *Executor) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "execute", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	if migration.Status != StatusPlanned {
		sendError(onProgress, "execute", "migration is not in planned state (current: "+migration.Status+")")
		return fmt.Errorf("migration not in planned state: %s", migration.Status)
	}

	// 2. Update status to running
	e.repo.UpdateMigrationStatus(migrationID, StatusRunning, "")
	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Starting migration..."})

	// 3. Get SSH connection to target
	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		sendError(onProgress, "execute", "target server not found")
		e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "target server not found")
		return fmt.Errorf("target server not found: %w", err)
	}

	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Connecting to target server..."})

	sshClient, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		sendError(onProgress, "execute", "failed to connect to target: "+err.Error())
		e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "SSH connection failed")
		return fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "execute", Status: "success", Value: "Connected to target server"})

	// 4. Load collected steps
	steps, err := e.repo.GetSteps(migrationID)
	if err != nil {
		sendError(onProgress, "execute", "failed to load migration steps")
		e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "failed to load steps")
		return err
	}

	// Parse categories from migration
	var categories []string
	json.Unmarshal([]byte(migration.Categories), &categories)

	// 5. Backup phase: backup each category on target
	onProgress(WSMessage{Step: "backup", Status: "progress", Value: "Creating backups on target..."})

	backups := make(map[string]BackupData)
	for _, catName := range categories {
		if ctx.Err() != nil {
			e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "cancelled")
			return ctx.Err()
		}

		mod, ok := e.registry.Get(catName)
		if !ok {
			continue
		}

		onProgress(WSMessage{
			Step:   "backup:" + catName,
			Status: "progress",
			Value:  "Backing up " + catName + "...",
		})

		backup, err := mod.Applier.Backup(sshClient)
		if err != nil {
			onProgress(WSMessage{
				Step:   "backup:" + catName,
				Status: "error",
				Error:  fmt.Sprintf("backup failed: %v", err),
			})
			// Non-fatal: continue without backup
		} else {
			backups[catName] = backup
			// Save backup to DB
			rawBackup, _ := json.Marshal(backup)
			e.repo.CreateBackup(migrationID, migration.TargetID, catName, string(rawBackup))

			onProgress(WSMessage{
				Step:   "backup:" + catName,
				Status: "success",
				Value:  "Backed up " + catName,
			})
		}
	}

	// 6. Apply phase: apply collected data to target
	for _, step := range steps {
		if ctx.Err() != nil {
			e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "cancelled")
			return ctx.Err()
		}

		if step.Action != "collect" || step.Status != StepStatusCompleted {
			continue
		}

		mod, ok := e.registry.Get(step.Category)
		if !ok {
			continue
		}

		// Parse the collected data
		var data CategoryData
		if err := json.Unmarshal([]byte(step.Data), &data); err != nil {
			continue
		}

		onProgress(WSMessage{
			Step:   "apply:" + step.Category,
			Status: "progress",
			Value:  "Applying " + step.Category + "...",
		})

		// Apply
		err := mod.Applier.Apply(sshClient, data, func(msg WSMessage) {
			msg.Step = "apply:" + step.Category + ":" + msg.Step
			onProgress(msg)
		})

		if err != nil {
			onProgress(WSMessage{
				Step:   "apply:" + step.Category,
				Status: "error",
				Error:  fmt.Sprintf("apply failed: %v", err),
			})
			// Auto-rollback this category
			if backup, ok := backups[step.Category]; ok {
				onProgress(WSMessage{
					Step:   "rollback:" + step.Category,
					Status: "progress",
					Value:  "Rolling back " + step.Category + "...",
				})
				mod.Applier.Rollback(sshClient, backup)
				onProgress(WSMessage{
					Step:   "rollback:" + step.Category,
					Status: "success",
					Value:  "Rolled back " + step.Category,
				})
			}

			e.repo.UpdateMigrationStatus(migrationID, StatusFailed, step.Category+" apply failed")
			return fmt.Errorf("apply failed for %s: %w", step.Category, err)
		}

		e.repo.UpdateStepStatus(step.ID, StepStatusCompleted, "")
		onProgress(WSMessage{
			Step:   "apply:" + step.Category,
			Status: "success",
			Value:  "Applied " + step.Category,
		})
	}

	// 7. Update status to completed
	now := time.Now().Format(time.RFC3339)
	e.repo.UpdateMigrationStatus(migrationID, StatusCompleted, "")
	e.repo.SetMigrationCompletedAt(migrationID, now)

	onProgress(WSMessage{Step: "execute", Status: "complete", Value: "Migration completed successfully"})

	return nil
}

// getSSHClient obtains an SSH connection for the given server.
func (e *Executor) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, e.srvRepo, e.pool, e.authSvc, e.hosts)
}

var _ xssh.HostKeyCallback
