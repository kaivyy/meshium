package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

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

	// 4. Collect data for each category IN PARALLEL
	type collectResult struct {
		catName string
		data    CategoryData
		err     error
	}

	results := make([]collectResult, len(req.Categories))
	var wg sync.WaitGroup

	for i, catName := range req.Categories {
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
			results[i] = collectResult{catName: catName, err: fmt.Errorf("unknown category: %s", catName)}
			continue
		}

		// For configs, create a per-request copy with the specified paths
		var collector Collector = mod.Collector
		if catName == "configs" {
			if cc, ok := mod.Collector.(*ConfigsCollector); ok {
				paths := cc.Paths
				if len(req.ConfigPaths) > 0 {
					paths = req.ConfigPaths
				}
				collector = &ConfigsCollector{Paths: paths}
			}
		}

		onProgress(WSMessage{
			Step:   "plan:" + catName,
			Status: "progress",
			Value:  "Collecting " + catName + "...",
		})

		wg.Add(1)
		go func(idx int, name string, coll Collector) {
			defer wg.Done()
			data, err := coll.Collect(ctx, sshClient)
			results[idx] = collectResult{catName: name, data: data, err: err}
		}(i, catName, collector)
	}

	wg.Wait()

	// Process results
	collectionErrors := 0
	for _, res := range results {
		if res.err != nil {
			// Check if it was the "unknown category" error
			if res.data.Type == "" && res.catName != "" {
				// Already reported above
				collectionErrors++
				continue
			}
			onProgress(WSMessage{
				Step:   "plan:" + res.catName,
				Status: "error",
				Error:  fmt.Sprintf("collect failed: %v", res.err),
			})
			p.repo.CreateStep(planID, res.catName, "collect", fmt.Sprintf(`{"error":"collect failed: %s"}`, res.err.Error()))
			p.repo.UpdateMigrationStatus(planID, StatusFailed, fmt.Sprintf("collection failed for %s: %v", res.catName, res.err))
			collectionErrors++
			continue
		}

		rawData, _ := json.Marshal(res.data)
		p.repo.CreateStep(planID, res.catName, "collect", string(rawData))

		onProgress(WSMessage{
			Step:   "plan:" + res.catName,
			Status: "success",
			Value:  "Collected " + res.catName,
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
