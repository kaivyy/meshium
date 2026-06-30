package migration

import (
	"context"
	"encoding/json"
	"fmt"

	"meshium/internal/mod/server"

	xssh "golang.org/x/crypto/ssh"
)

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
// and persists a migration record to the database.
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
	planID, err := p.repo.CreateMigration(req.SourceServerID, req.TargetServerID, req.Categories)
	if err != nil {
		sendError(onProgress, "plan", "failed to create migration record")
		return nil, fmt.Errorf("create migration: %w", err)
	}

	plan := &MigrationPlan{
		ID:             planID,
		SourceServerID: req.SourceServerID,
		TargetServerID: req.TargetServerID,
		Status:         StatusPlanned,
		Categories:     req.Categories,
	}

	// 4. Collect data for each category
	collectionErrors := 0
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
			collectionErrors++
			continue
		}

		onProgress(WSMessage{
			Step:   "plan:" + catName,
			Status: "progress",
			Value:  "Collecting " + catName + "...",
		})

		// For configs, create a per-request copy with the specified paths
		// to avoid mutating the shared singleton (race condition fix).
		var collector Collector = mod.Collector
		if catName == "configs" {
			if cc, ok := mod.Collector.(*ConfigsCollector); ok {
				paths := cc.Paths // copy existing paths from the singleton
				if len(req.ConfigPaths) > 0 {
					paths = req.ConfigPaths
				}
				collector = &ConfigsCollector{Paths: paths}
			}
		}

		data, err := collector.Collect(ctx, sshClient)
		if err != nil {
			onProgress(WSMessage{
				Step:   "plan:" + catName,
				Status: "error",
				Error:  fmt.Sprintf("collect failed: %v", err),
			})
			// Record the failed step with error data so the executor
			// can detect it and refuse to execute the migration.
			p.repo.CreateStep(planID, catName, "collect", fmt.Sprintf(`{"error":"collect failed: %s"}`, err.Error()))
			// Mark the migration as failed — collection errors are fatal.
			p.repo.UpdateMigrationStatus(planID, StatusFailed, fmt.Sprintf("collection failed for %s: %v", catName, err))
			collectionErrors++
			continue
		}

		// Save collected data as a step
		rawData, _ := json.Marshal(data)
		p.repo.CreateStep(planID, catName, "collect", string(rawData))

		onProgress(WSMessage{
			Step:   "plan:" + catName,
			Status: "success",
			Value:  "Collected " + catName,
		})
	}

	// 5. Update plan status — only set to "planned" if all collections succeeded.
	// If any collection failed, the migration is already marked as "failed" above.
	if collectionErrors == 0 {
		p.repo.UpdateMigrationStatus(planID, StatusPlanned, "")
	} else {
		onProgress(WSMessage{
			Step:   "plan",
			Status: "error",
			Error:  fmt.Sprintf("migration plan has %d collection error(s) — cannot execute", collectionErrors),
		})
	}

	onProgress(WSMessage{Step: "plan", Status: "complete", Value: "Migration plan created"})

	return plan, nil
}

// getSSHClient obtains an SSH connection for the given server.
func (p *Planner) getSSHClient(serverID int, srv *server.Server) (SSHExecuter, error) {
	return getSSHClientForServer(serverID, srv, p.srvRepo, p.pool, p.authSvc, p.hosts)
}

func sendError(onStep StepCallback, step, message string) {
	onStep(WSMessage{Step: step, Status: "error", Error: message})
}

// Ensure xssh import is used (for type compatibility)
var _ xssh.HostKeyCallback
