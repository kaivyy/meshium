package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
// It runs backups for each category (mandatory — failure is fatal), then
// applies collected data. If any apply fails, all already-applied categories
// are rolled back in reverse order. Steps are checkpointed so that an
// interrupted migration can be resumed.
func (e *Executor) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return e.executeWithSkip(ctx, migrationID, onProgress, nil)
}

// Resume resumes an interrupted migration. It skips categories that have
// already been applied (tracked via StepStatusApplied checkpoint) and
// continues from where the migration left off. The migration must be in
// StatusInterrupted state.
func (e *Executor) Resume(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "resume", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	if migration.Status != StatusInterrupted {
		sendError(onProgress, "resume", "migration is not in interrupted state (current: "+migration.Status+")")
		return fmt.Errorf("migration not in interrupted state: %s", migration.Status)
	}

	// Get already-applied categories to skip
	appliedCats, err := e.repo.GetAppliedCategories(migrationID)
	if err != nil {
		sendError(onProgress, "resume", "failed to load applied categories")
		return fmt.Errorf("failed to load applied categories: %w", err)
	}

	skip := make(map[string]bool, len(appliedCats))
	for _, cat := range appliedCats {
		skip[cat] = true
	}

	onProgress(WSMessage{
		Step:   "resume",
		Status: "progress",
		Value:  fmt.Sprintf("Resuming migration (skipping %d already-applied categories)...", len(appliedCats)),
	})

	if err := e.repo.UpdateMigrationStatus(migrationID, StatusResuming, ""); err != nil {
		log.Printf("failed to update migration %d status to resuming: %v", migrationID, err)
	}

	return e.executeWithSkip(ctx, migrationID, onProgress, skip)
}

// RecoverInterrupted detects migrations stuck in StatusRunning (from a
// crashed process) and marks them as StatusInterrupted so they can be
// resumed. Returns the IDs of migrations that were recovered.
func (e *Executor) RecoverInterrupted() ([]int, error) {
	migrations, err := e.repo.ListMigrations()
	if err != nil {
		return nil, fmt.Errorf("failed to list migrations: %w", err)
	}

	var recovered []int
	for _, m := range migrations {
		if m.Status == StatusRunning {
			if err := e.repo.UpdateMigrationStatus(m.ID, StatusInterrupted, "process may have crashed"); err != nil {
				return recovered, fmt.Errorf("failed to update migration %d: %w", m.ID, err)
			}
			recovered = append(recovered, m.ID)
		}
	}

	return recovered, nil
}

// executeWithSkip is the core execution logic. If skip is non-nil,
// categories in the skip map are skipped (already applied). This is
// used by both Execute (skip=nil) and Resume (skip=applied categories).
func (e *Executor) executeWithSkip(ctx context.Context, migrationID int, onProgress StepCallback, skip map[string]bool) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Load the migration
	migration, err := e.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "execute", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	// Accept both StatusPlanned (fresh) and StatusResuming (resume)
	if migration.Status != StatusPlanned && migration.Status != StatusResuming {
		sendError(onProgress, "execute", "migration is not in planned/resuming state (current: "+migration.Status+")")
		return fmt.Errorf("migration not in planned/resuming state: %s", migration.Status)
	}

	// 2. Update status to running
	if err := e.repo.UpdateMigrationStatus(migrationID, StatusRunning, ""); err != nil {
		log.Printf("failed to update migration %d status to running: %v", migrationID, err)
	}
	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Starting migration..."})

	// 3. Get SSH connection to target
	targetServer, err := e.srvRepo.GetByID(migration.TargetID)
	if err != nil {
		sendError(onProgress, "execute", "target server not found")
		if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "target server not found"); err != nil {
			log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
		}
		return fmt.Errorf("target server not found: %w", err)
	}

	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Connecting to target server..."})

	sshClient, err := e.getSSHClient(migration.TargetID, targetServer)
	if err != nil {
		sendError(onProgress, "execute", "failed to connect to target: "+err.Error())
		if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "SSH connection failed"); err != nil {
			log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
		}
		return fmt.Errorf("target SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "execute", Status: "success", Value: "Connected to target server"})

	// 4. Load collected steps
	steps, err := e.repo.GetSteps(migrationID)
	if err != nil {
		sendError(onProgress, "execute", "failed to load migration steps")
		if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "failed to load steps"); err != nil {
			log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
		}
		return err
	}

	// Parse categories from migration
	var categories []string
	if err := json.Unmarshal([]byte(migration.Categories), &categories); err != nil {
		return fmt.Errorf("failed to parse migration categories: %w", err)
	}

	// 5. Backup phase: backup each category on target (MANDATORY)
	//    If any backup fails, the migration is aborted — no apply without backup.
	onProgress(WSMessage{Step: "backup", Status: "progress", Value: "Creating backups on target..."})

	backups := make(map[string]BackupData)
	for _, catName := range categories {
		if ctx.Err() != nil {
			if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "cancelled"); err != nil {
				log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
			}
			return ctx.Err()
		}

		// Skip already-applied categories on resume (their backups are already stored)
		if skip != nil && skip[catName] {
			continue
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

		backup, err := mod.Applier.Backup(ctx, sshClient)
		if err != nil {
			// FATAL: backup failure means we cannot safely apply changes.
			// Abort the entire migration — no apply without a backup.
			onProgress(WSMessage{
				Step:   "backup:" + catName,
				Status: "error",
				Error:  fmt.Sprintf("backup failed (aborting migration): %v", err),
			})
			if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed,
				fmt.Sprintf("backup failed for %s: %v", catName, err)); err != nil {
				log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
			}
			return fmt.Errorf("backup failed for %s (migration aborted — no apply without backup): %w", catName, err)
		}

		backups[catName] = backup
		// Save backup to DB
		rawBackup, err := json.Marshal(backup)
		if err != nil {
			log.Printf("failed to marshal backup for migration %d category %s: %v", migrationID, catName, err)
		}
		e.repo.CreateBackup(migrationID, migration.TargetID, catName, string(rawBackup))

		onProgress(WSMessage{
			Step:   "backup:" + catName,
			Status: "success",
			Value:  "Backed up " + catName,
		})
	}

	// 6. Apply phase: apply collected data to target
	//    Track applied categories for rollback on failure.
	appliedOrder := make([]string, 0, len(steps))

	for _, step := range steps {
		if ctx.Err() != nil {
			// Mark as interrupted so it can be resumed later
			if err := e.repo.UpdateMigrationStatus(migrationID, StatusInterrupted, "cancelled by context"); err != nil {
				log.Printf("failed to update migration %d status to interrupted: %v", migrationID, err)
			}
			return ctx.Err()
		}

		if step.Action != "collect" || step.Status != StepStatusCompleted {
			continue
		}

		// Skip already-applied categories on resume
		if skip != nil && skip[step.Category] {
			onProgress(WSMessage{
				Step:   "apply:" + step.Category,
				Status: "success",
				Value:  "Skipped (already applied): " + step.Category,
			})
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
		err := mod.Applier.Apply(ctx, sshClient, data, func(msg WSMessage) {
			msg.Step = "apply:" + step.Category + ":" + msg.Step
			onProgress(msg)
		})

		if err != nil {
			onProgress(WSMessage{
				Step:   "apply:" + step.Category,
				Status: "error",
				Error:  fmt.Sprintf("apply failed: %v", err),
			})

			// Roll back ALL already-applied categories in reverse order.
			// This ensures the target is restored to its pre-migration state.
			e.rollbackAll(ctx, migrationID, sshClient, appliedOrder, backups, onProgress)

			if err := e.repo.UpdateMigrationStatus(migrationID, StatusFailed, step.Category+" apply failed"); err != nil {
				log.Printf("failed to update migration %d status to failed: %v", migrationID, err)
			}
			return fmt.Errorf("apply failed for %s: %w", step.Category, err)
		}

		// Checkpoint: mark this step as applied so it can be skipped on resume
		if err := e.repo.UpdateStepStatus(step.ID, StepStatusApplied, ""); err != nil {
			log.Printf("failed to update step %d status to applied: %v", step.ID, err)
		}
		appliedOrder = append(appliedOrder, step.Category)

		onProgress(WSMessage{
			Step:   "apply:" + step.Category,
			Status: "success",
			Value:  "Applied " + step.Category,
		})
	}

	// 7. Update status to completed
	now := time.Now().Format(time.RFC3339)
	if err := e.repo.UpdateMigrationStatus(migrationID, StatusCompleted, ""); err != nil {
		log.Printf("failed to update migration %d status to completed: %v", migrationID, err)
	}
	e.repo.SetMigrationCompletedAt(migrationID, now)

	onProgress(WSMessage{Step: "execute", Status: "complete", Value: "Migration completed successfully"})

	return nil
}

// rollbackAll rolls back all already-applied categories in reverse order.
// This is called when an apply fails to ensure the target is restored
// to its pre-migration state.
func (e *Executor) rollbackAll(ctx context.Context, migrationID int, sshClient SSHExecuter, appliedOrder []string, backups map[string]BackupData, onProgress StepCallback) {
	if len(appliedOrder) == 0 {
		return
	}

	onProgress(WSMessage{
		Step:   "rollback",
		Status: "progress",
		Value:  fmt.Sprintf("Rolling back %d applied categories...", len(appliedOrder)),
	})

	// Roll back in reverse order of application
	for i := len(appliedOrder) - 1; i >= 0; i-- {
		catName := appliedOrder[i]
		backup, ok := backups[catName]
		if !ok {
			// Try loading from DB if not in memory (e.g. after resume)
			dbBackups, err := e.repo.GetBackups(migrationID)
			if err == nil {
				for _, dbb := range dbBackups {
					if dbb.Category == catName {
						if err := json.Unmarshal([]byte(dbb.Data), &backup); err != nil {
							log.Printf("failed to unmarshal backup data for migration %d category %s: %v", migrationID, catName, err)
							continue
						}
						ok = true
						break
					}
				}
			}
		}
		if !ok {
			continue
		}

		mod, ok := e.registry.Get(catName)
		if !ok {
			continue
		}

		onProgress(WSMessage{
			Step:   "rollback:" + catName,
			Status: "progress",
			Value:  "Rolling back " + catName + "...",
		})

		if err := mod.Applier.Rollback(ctx, sshClient, backup); err != nil {
			onProgress(WSMessage{
				Step:   "rollback:" + catName,
				Status: "error",
				Error:  fmt.Sprintf("rollback failed: %v", err),
			})
			// Continue rolling back other categories even if one fails
		} else {
			// Clear the checkpoint since we rolled back
			if steps, err := e.repo.GetSteps(migrationID); err == nil {
				for _, s := range steps {
					if s.Category == catName && s.Status == StepStatusApplied {
						if err := e.repo.UpdateStepStatus(s.ID, StepStatusCompleted, "rolled back"); err != nil {
							log.Printf("failed to update step %d status to completed after rollback: %v", s.ID, err)
						}
					}
				}
			}
			onProgress(WSMessage{
				Step:   "rollback:" + catName,
				Status: "success",
				Value:  "Rolled back " + catName,
			})
		}
	}

	onProgress(WSMessage{
		Step:   "rollback",
		Status: "complete",
		Value:  "Rollback of all applied categories complete",
	})
}

// getSSHClient obtains an SSH connection for the given server.
func (e *Executor) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, e.srvRepo, e.pool, e.authSvc, e.hosts)
}

var _ xssh.HostKeyCallback
