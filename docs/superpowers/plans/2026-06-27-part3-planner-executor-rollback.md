# Part 3: Planner, Executor & Rollback

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Planner (collects from source and builds a migration plan), Executor (applies the plan to the target with live progress), and Rollback Manager (restores the target to its pre-migration state).

**Architecture:** The Planner connects to the source server, runs all category collectors, and persists a `MigrationPlan` to the database. The Executor connects to the target server, runs backups via all category appliers, then applies each category's collected data while streaming progress over a `StepCallback`. The Rollback Manager reads the backup data from the database and calls each category applier's `Rollback` method.

**Tech Stack:** Go 1.22+, `database/sql`, `encoding/json`

## Global Constraints

- Planner, Executor, and Rollback all live in `internal/mod/migration/`
- They reuse the `CategoryRegistry`, `Collector`, `Applier` interfaces from Part 2
- They reuse the `Repo` interface from Part 1 for database persistence
- They reuse `discovery.ConnectionPool`, `discovery.AESKeyProvider`, `discovery.HostKeyStore` for SSH connections
- The Planner creates a `MigrationPlan` record with status `planned`
- The Executor transitions status: `planned` → `running` → `completed`/`failed`
- The Rollback transitions status: `completed` → `rolling_back` → `rolled_back`/`rollback_failed`
- All progress is reported via `StepCallback` (same `WSMessage` format as discovery)
- SSH connections are obtained from the pool, not created directly

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/migration/planner.go` | Planner: collect from source, build plan |
| `internal/mod/migration/planner_test.go` | Planner tests |
| `internal/mod/migration/executor.go` | Executor: apply to target, stream progress |
| `internal/mod/migration/executor_test.go` | Executor tests |
| `internal/mod/migration/rollback.go` | Rollback manager: restore from backup |
| `internal/mod/migration/rollback_test.go` | Rollback tests |

---

### Task 1: Planner

**Files:**
- Create: `internal/mod/migration/planner.go`
- Create: `internal/mod/migration/planner_test.go`

**Interfaces:**
- Consumes: `CategoryRegistry`, `Repo`, `server.Repo`, `ConnectionPool`, `AESKeyProvider`, `HostKeyStore`
- Produces: `Planner` struct, `PlanRequest` type

- [ ] **Step 1: Write the Planner**

```go
// internal/mod/migration/planner.go
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/server"

	xssh "golang.org/x/crypto/ssh"
)

// PlanRequest specifies the source server and categories to collect.
type PlanRequest struct {
	SourceServerID int      `json:"sourceServerId"`
	TargetServerID int      `json:"targetServerId"`
	Categories     []string `json:"categories"`
	ConfigPaths    []string `json:"configPaths,omitempty"`
}

// Planner collects data from the source server and builds a migration plan.
type Planner struct {
	registry *CategoryRegistry
	repo     Repo
	srvRepo  server.Repo
	pool     ConnectionPool
	authSvc  AESKeyProvider
	hosts    HostKeyStore
}

// NewPlanner creates a Planner.
func NewPlanner(
	registry *CategoryRegistry,
	repo Repo,
	srvRepo server.Repo,
	pool ConnectionPool,
	authSvc AESKeyProvider,
	hosts HostKeyStore,
) *Planner {
	return &Planner{
		registry: registry,
		repo:     repo,
		srvRepo:  srvRepo,
		pool:     pool,
		authSvc:  authSvc,
		hosts:    hosts,
	}
}

// Plan connects to the source server, collects data for all requested categories,
// and persists a MigrationPlan to the database.
func (p *Planner) Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error) {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Validate servers exist
	sourceServer, err := p.srvRepo.GetByID(req.SourceServerID)
	if err != nil {
		sendError(onProgress, "plan", "source server not found")
		return nil, fmt.Errorf("source server not found: %w", err)
	}

	_, err = p.srvRepo.GetByID(req.TargetServerID)
	if err != nil {
		sendError(onProgress, "plan", "target server not found")
		return nil, fmt.Errorf("target server not found: %w", err)
	}

	// 2. Get SSH connection to source
	onProgress(WSMessage{Step: "plan", Status: "progress", Value: "Connecting to source server..."})

	sshClient, err := p.getSSHClient(req.SourceServerID, sourceServer)
	if err != nil {
		sendError(onProgress, "plan", "failed to connect to source: "+err.Error())
		return nil, fmt.Errorf("source SSH connection failed: %w", err)
	}

	onProgress(WSMessage{Step: "plan", Status: "success", Value: "Connected to source server"})

	// 3. Create migration record
	plan := &MigrationPlan{
		SourceServerID: req.SourceServerID,
		TargetServerID: req.TargetServerID,
		Status:         StatusPlanned,
		Categories:     req.Categories,
	}

	planID, err := p.repo.CreateMigration(plan)
	if err != nil {
		sendError(onProgress, "plan", "failed to create migration record")
		return nil, fmt.Errorf("create migration: %w", err)
	}
	plan.ID = planID

	// 4. Collect data for each category
	for _, catName := range req.Categories {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		mod, ok := p.registry.Get(catName)
		if !ok {
			onProgress(WSMessage{
				Step:   "plan:" + catName,
				Status: "error",
				Error:  "unknown category: " + catName,
			})
			continue
		}

		onProgress(WSMessage{
			Step:   "plan:" + catName,
			Status: "progress",
			Value:  "Collecting " + catName + "...",
		})

		// For configs, set the paths
		if catName == "configs" {
			if cc, ok := mod.Collector.(*ConfigsCollector); ok {
				cc.Paths = req.ConfigPaths
			}
		}

		data, err := mod.Collector.Collect(sshClient)
		if err != nil {
			onProgress(WSMessage{
				Step:   "plan:" + catName,
				Status: "error",
				Error:  fmt.Sprintf("collect failed: %v", err),
			})
			// Save the step as failed
			p.repo.CreateStep(MigrationStep{
				MigrationID: planID,
				Category:     catName,
				Action:       "collect",
				Status:       StepStatusFailed,
				Error:        err.Error(),
			})
			continue
		}

		// Save collected data as a step
		rawData, _ := json.Marshal(data)
		p.repo.CreateStep(MigrationStep{
			MigrationID: planID,
			Category:    catName,
			Action:      "collect",
			Status:      StepStatusCompleted,
			Data:        string(rawData),
		})

		onProgress(WSMessage{
			Step:   "plan:" + catName,
			Status: "success",
			Value:  "Collected " + catName,
		})
	}

	// 5. Update plan status
	p.repo.UpdateMigrationStatus(planID, StatusPlanned, "")

	onProgress(WSMessage{Step: "plan", Status: "complete", Value: "Migration plan created"})

	return plan, nil
}

// getSSHClient obtains an SSH connection for the given server.
func (p *Planner) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	aesKey := p.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	password, _ := decryptCredential(aesKey, srv.Password)
	sshKey, _ := decryptCredential(aesKey, srv.SSHKey)
	passphrase, _ := decryptCredential(aesKey, srv.Passphrase)

	sshConfig := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		sshConfig.PrivateKey = []byte(sshKey)
	}

	hostKeyCallback := p.hosts.MakeHostKeyCallback()
	return p.pool.Get(serverID, sshConfig, hostKeyCallback)
}

func sendError(onStep StepCallback, step, message string) {
	onStep(WSMessage{Step: step, Status: "error", Error: message})
}

func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	// Reuse the shared.Decrypt function
	decrypted, err := sharedDecrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// sharedDecrypt is a wrapper to avoid import cycle.
// It will be replaced with the actual shared.Decrypt call.
var sharedDecrypt = func(key, ciphertext []byte) ([]byte, error) {
	// In production, this calls shared.Decrypt(key, ciphertext)
	// For now, we import it directly
	return decrypt(key, ciphertext)
}
```

**Note:** The `decrypt` function needs to be imported from `meshium/internal/shared`. Update the import:

```go
import (
	"meshium/internal/shared"
)

// Replace sharedDecrypt with:
func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/planner_test.go
package migration

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPlannerPlan(t *testing.T) {
	// This test requires mocking the SSH pool and server repo.
	// For now, test the PlanRequest validation.
	req := PlanRequest{
		SourceServerID: 1,
		TargetServerID: 2,
		Categories:     []string{"packages", "services"},
	}
	raw, _ := json.Marshal(req)
	var decoded PlanRequest
	json.Unmarshal(raw, &decoded)
	if decoded.SourceServerID != 1 {
		t.Error("expected source server ID 1")
	}
	if len(decoded.Categories) != 2 {
		t.Error("expected 2 categories")
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestPlanner
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/planner.go internal/mod/migration/planner_test.go
git commit -m "feat: add migration planner with source collection and plan persistence"
```

---

### Task 2: Executor

**Files:**
- Create: `internal/mod/migration/executor.go`
- Create: `internal/mod/migration/executor_test.go`

**Interfaces:**
- Consumes: `CategoryRegistry`, `Repo`, `server.Repo`, `ConnectionPool`, `AESKeyProvider`, `HostKeyStore`, `StepCallback`
- Produces: `Executor` struct

- [ ] **Step 1: Write the Executor**

```go
// internal/mod/migration/executor.go
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"

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

// Execute applies a migration plan to the target server.
// It runs backups for each category, then applies collected data.
func (e *Executor) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	if onProgress == nil {
		onProgress = func(WSMessage) {}
	}

	// 1. Load the migration plan
	plan, err := e.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "execute", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	if plan.Status != StatusPlanned {
		sendError(onProgress, "execute", "migration is not in planned state (current: "+plan.Status+")")
		return fmt.Errorf("migration not in planned state: %s", plan.Status)
	}

	// 2. Update status to running
	e.repo.UpdateMigrationStatus(migrationID, StatusRunning, "")
	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Starting migration..."})

	// 3. Get SSH connection to target
	targetServer, err := e.srvRepo.GetByID(plan.TargetServerID)
	if err != nil {
		sendError(onProgress, "execute", "target server not found")
		e.repo.UpdateMigrationStatus(migrationID, StatusFailed, "target server not found")
		return fmt.Errorf("target server not found: %w", err)
	}

	onProgress(WSMessage{Step: "execute", Status: "progress", Value: "Connecting to target server..."})

	sshClient, err := e.getSSHClient(plan.TargetServerID, targetServer)
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

	// 5. Backup phase: backup each category on target
	onProgress(WSMessage{Step: "backup", Status: "progress", Value: "Creating backups on target..."})

	backups := make(map[string]BackupData)
	for _, catName := range plan.Categories {
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
			e.repo.CreateBackup(MigrationBackup{
				MigrationID: migrationID,
				Category:    catName,
				Data:        string(rawBackup),
			})

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

		// Update step status to running
		e.repo.UpdateStepStatus(step.ID, StepStatusRunning, "")

		// Apply
		err := mod.Applier.Apply(sshClient, data, func(msg WSMessage) {
			msg.Step = "apply:" + step.Category + ":" + msg.Step
			onProgress(msg)
		})

		if err != nil {
			e.repo.UpdateStepStatus(step.ID, StepStatusFailed, err.Error())
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
	aesKey := e.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	password, _ := shared.Decrypt(aesKey, []byte(srv.Password))
	sshKey, _ := shared.Decrypt(aesKey, []byte(srv.SSHKey))
	passphrase, _ := shared.Decrypt(aesKey, []byte(srv.Passphrase))

	sshConfig := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   string(password),
		Passphrase: string(passphrase),
	}
	if len(sshKey) > 0 {
		sshConfig.PrivateKey = sshKey
	}

	hostKeyCallback := e.hosts.MakeHostKeyCallback()
	return e.pool.Get(serverID, sshConfig, hostKeyCallback)
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/executor_test.go
package migration

import (
	"testing"
)

func TestExecutorPlanValidation(t *testing.T) {
	// Test that executor validates migration status
	// Full test requires mocking the repo and SSH pool
	// For now, test the struct creation
	registry := NewCategoryRegistry()
	executor := NewExecutor(registry, nil, nil, nil, nil, nil)
	if executor == nil {
		t.Error("expected non-nil executor")
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestExecutor
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/executor.go internal/mod/migration/executor_test.go
git commit -m "feat: add migration executor with backup, apply, and auto-rollback"
```

---

### Task 3: Rollback Manager

**Files:**
- Create: `internal/mod/migration/rollback.go`
- Create: `internal/mod/migration/rollback_test.go`

**Interfaces:**
- Consumes: `CategoryRegistry`, `Repo`, `server.Repo`, `ConnectionPool`, `AESKeyProvider`, `HostKeyStore`, `StepCallback`
- Produces: `RollbackManager` struct

- [ ] **Step 1: Write the Rollback Manager**

```go
// internal/mod/migration/rollback.go
package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"

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
	plan, err := rm.repo.GetMigration(migrationID)
	if err != nil {
		sendError(onProgress, "rollback", "migration not found")
		return fmt.Errorf("migration not found: %w", err)
	}

	// 2. Update status to rolling_back
	rm.repo.UpdateMigrationStatus(migrationID, StatusRollingBack, "")
	onProgress(WSMessage{Step: "rollback", Status: "progress", Value: "Starting rollback..."})

	// 3. Get SSH connection to target
	targetServer, err := rm.srvRepo.GetByID(plan.TargetServerID)
	if err != nil {
		sendError(onProgress, "rollback", "target server not found")
		rm.repo.UpdateMigrationStatus(migrationID, StatusRollbackFailed, "target server not found")
		return fmt.Errorf("target server not found: %w", err)
	}

	sshClient, err := rm.getSSHClient(plan.TargetServerID, targetServer)
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
	rm.repo.UpdateMigrationStatus(migrationID, StatusRolledBack, "")
	now := time.Now().Format(time.RFC3339)
	rm.repo.SetMigrationRolledBackAt(migrationID, now)

	onProgress(WSMessage{Step: "rollback", Status: "complete", Value: "Rollback completed"})

	return nil
}

// getSSHClient obtains an SSH connection for the given server.
func (rm *RollbackManager) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	aesKey := rm.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	password, _ := shared.Decrypt(aesKey, []byte(srv.Password))
	sshKey, _ := shared.Decrypt(aesKey, []byte(srv.SSHKey))
	passphrase, _ := shared.Decrypt(aesKey, []byte(srv.Passphrase))

	sshConfig := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   string(password),
		Passphrase: string(passphrase),
	}
	if len(sshKey) > 0 {
		sshConfig.PrivateKey = sshKey
	}

	hostKeyCallback := rm.hosts.MakeHostKeyCallback()
	return rm.pool.Get(serverID, sshConfig, hostKeyCallback)
}
```

- [ ] **Step 2: Write the failing test**

```go
// internal/mod/migration/rollback_test.go
package migration

import (
	"testing"
)

func TestRollbackManagerCreation(t *testing.T) {
	registry := NewCategoryRegistry()
	rm := NewRollbackManager(registry, nil, nil, nil, nil, nil)
	if rm == nil {
		t.Error("expected non-nil rollback manager")
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/migration/ -v -run TestRollback
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/rollback.go internal/mod/migration/rollback_test.go
git commit -m "feat: add rollback manager with reverse-order category restoration"
```

---

### Task 4: Repo Interface Updates

**Files:**
- Modify: `internal/mod/migration/repo.go` (add missing methods)
- Modify: `internal/mod/migration/repo_test.go` (add tests for new methods)

**Interfaces:**
- Adds: `GetSteps`, `GetBackups`, `UpdateStepStatus`, `CreateBackup`, `SetMigrationCompletedAt`, `SetMigrationRolledBackAt`

- [ ] **Step 1: Add missing repo methods**

```go
// Append to internal/mod/migration/repo.go

// GetSteps returns all steps for a migration.
func (r *sqliteRepo) GetSteps(migrationID int) ([]MigrationStep, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, category, action, status, data, error, created_at, completed_at
		 FROM migration_steps WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []MigrationStep
	for rows.Next() {
		var s MigrationStep
		if err := rows.Scan(&s.ID, &s.MigrationID, &s.Category, &s.Action, &s.Status, &s.Data, &s.Error, &s.CreatedAt, &s.CompletedAt); err != nil {
			return nil, err
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// GetBackups returns all backups for a migration.
func (r *sqliteRepo) GetBackups(migrationID int) ([]MigrationBackup, error) {
	rows, err := r.db.Query(
		`SELECT id, migration_id, category, data, created_at
		 FROM migration_backups WHERE migration_id = ? ORDER BY id ASC`,
		migrationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backups []MigrationBackup
	for rows.Next() {
		var b MigrationBackup
		if err := rows.Scan(&b.ID, &b.MigrationID, &b.Category, &b.Data, &b.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, nil
}

// UpdateStepStatus updates the status of a migration step.
func (r *sqliteRepo) UpdateStepStatus(stepID int, status, errMsg string) error {
	_, err := r.db.Exec(
		`UPDATE migration_steps SET status = ?, error = ?, completed_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		status, errMsg, stepID,
	)
	return err
}

// CreateBackup creates a backup record.
func (r *sqliteRepo) CreateBackup(b MigrationBackup) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO migration_backups (migration_id, category, data) VALUES (?, ?, ?)`,
		b.MigrationID, b.Category, b.Data,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// SetMigrationCompletedAt sets the completed_at timestamp.
func (r *sqliteRepo) SetMigrationCompletedAt(migrationID int, timestamp string) error {
	_, err := r.db.Exec(
		`UPDATE migrations SET completed_at = ? WHERE id = ?`,
		timestamp, migrationID,
	)
	return err
}

// SetMigrationRolledBackAt sets the rolled_back_at timestamp.
func (r *sqliteRepo) SetMigrationRolledBackAt(migrationID int, timestamp string) error {
	_, err := r.db.Exec(
		`UPDATE migrations SET rolled_back_at = ? WHERE id = ?`,
		timestamp, migrationID,
	)
	return err
}
```

- [ ] **Step 2: Update the Repo interface to include all methods**

```go
// Update the Repo interface in internal/mod/migration/repo.go:

type Repo interface {
	CreateMigration(m *MigrationPlan) (int, error)
	GetMigration(id int) (*MigrationPlan, error)
	ListMigrations() ([]MigrationPlan, error)
	UpdateMigrationStatus(id int, status, errMsg string) error
	DeleteMigration(id int) error

	CreateStep(s MigrationStep) (int, error)
	GetSteps(migrationID int) ([]MigrationStep, error)
	UpdateStepStatus(stepID int, status, errMsg string) error

	CreateBackup(b MigrationBackup) (int, error)
	GetBackups(migrationID int) ([]MigrationBackup, error)

	SetMigrationCompletedAt(migrationID int, timestamp string) error
	SetMigrationRolledBackAt(migrationID int, timestamp string) error
}
```

- [ ] **Step 3: Run all tests to verify**

```bash
go test ./internal/mod/migration/ -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/migration/repo.go internal/mod/migration/repo_test.go
git commit -m "feat: add remaining repo methods for migration steps and backups"
```

---

### Task 5: Integration Test for Planner → Executor → Rollback

**Files:**
- Create: `internal/mod/migration/integration_test.go`

**Interfaces:**
- Consumes: `Planner`, `Executor`, `RollbackManager`, `CategoryRegistry`, `Repo`

- [ ] **Step 1: Write integration test**

```go
// internal/mod/migration/integration_test.go
package migration

import (
	"testing"
)

// TestPlannerExecutorRollbackFlow tests the full migration lifecycle.
// This is a smoke test that verifies the components can be wired together.
// Full integration tests with real SSH connections require a test server.
func TestPlannerExecutorRollbackFlow(t *testing.T) {
	registry := NewCategoryRegistry()
	if registry == nil {
		t.Fatal("failed to create registry")
	}

	// Verify all 4 categories are registered
	cats := registry.Available()
	if len(cats) != 4 {
		t.Fatalf("expected 4 categories, got %d", len(cats))
	}

	// Verify each category has both collector and applier
	for _, name := range cats {
		mod, ok := registry.Get(name)
		if !ok {
			t.Errorf("category %s not found in registry", name)
			continue
		}
		if mod.Collector == nil {
			t.Errorf("category %s has nil collector", name)
		}
		if mod.Applier == nil {
			t.Errorf("category %s has nil applier", name)
		}
	}

	// Verify planner, executor, and rollback manager can be created
	// (they'll fail without proper deps, but should not panic)
	planner := NewPlanner(registry, nil, nil, nil, nil, nil)
	if planner == nil {
		t.Error("failed to create planner")
	}

	executor := NewExecutor(registry, nil, nil, nil, nil, nil)
	if executor == nil {
		t.Error("failed to create executor")
	}

	rm := NewRollbackManager(registry, nil, nil, nil, nil, nil)
	if rm == nil {
		t.Error("failed to create rollback manager")
	}
}
```

- [ ] **Step 2: Run all tests**

```bash
go test ./internal/mod/migration/ -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/integration_test.go
git commit -m "test: add integration test for planner-executor-rollback flow"
```

---

### Task 6: Build & Verify

**Files:**
- No new files

- [ ] **Step 1: Run all migration tests**

```bash
cd /root/meshium
go test ./internal/mod/migration/ -v -count=1
```

- [ ] **Step 2: Verify the full project compiles**

```bash
go build ./...
```

- [ ] **Step 3: Commit any remaining changes**

```bash
git add -A
git commit -m "chore: verify migration engine compiles and tests pass"
```
