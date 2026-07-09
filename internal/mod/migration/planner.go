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

	// 4. Collect data for each category IN PARALLEL. Each goroutine persists its
	// own collect step and streams its own progress/success/error as it finishes
	// — so the UI shows per-category completion live (not five "Collecting…"
	// lines then a long silence), and a category that finishes is recorded even
	// if a sibling hangs. Previously all progress/success was emitted only after
	// wg.Wait(), so a single slow collector stalled the whole step and left zero
	// steps if it never returned.
	type collectResult struct {
		catName string
		err     error
	}
	results := make([]collectResult, len(req.Categories))
	var wg sync.WaitGroup
	// onProgress writes to the WebSocket; conn.WriteJSON is not safe for
	// concurrent use, so serialize progress emission across goroutines.
	var progMu sync.Mutex
	emit := func(msg WSMessage) {
		progMu.Lock()
		defer progMu.Unlock()
		onProgress(msg)
	}

	for i, catName := range req.Categories {
		// If the caller (e.g. the plan WebSocket) disconnected, stop launching
		// new collection goroutines — but do NOT return early: already-launched
		// goroutines write their steps and must be joined by wg.Wait() below,
		// and the migration row must be marked honestly (interrupted) rather than
		// left as StatusPlanned with zero steps, which dead-ends the wizard.
		if ctx.Err() != nil {
			break
		}

		mod, ok := p.registry.Get(catName)
		if !ok {
			emit(WSMessage{
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

		emit(WSMessage{
			Step:   "plan:" + catName,
			Status: "progress",
			Value:  "Collecting " + catName + "...",
		})

		wg.Add(1)
		go func(idx int, name string, coll Collector) {
			defer wg.Done()
			data, err := coll.Collect(ctx, sshClient)
			results[idx] = collectResult{catName: name, err: err}
			if err != nil {
				emit(WSMessage{
					Step:   "plan:" + name,
					Status: "error",
					Error:  fmt.Sprintf("collect failed: %v", err),
				})
				p.repo.CreateStep(planID, name, "collect", fmt.Sprintf(`{"error":"collect failed: %s"}`, err.Error()))
				return
			}
			rawData, _ := json.Marshal(data)
			p.repo.CreateStep(planID, name, "collect", string(rawData))
			emit(WSMessage{
				Step:   "plan:" + name,
				Status: "success",
				Value:  "Collected " + name,
			})
		}(i, catName, collector)
	}

	wg.Wait()

	// If the caller disconnected mid-collection, mark the migration honestly.
	// Leaving it as StatusPlanned (set by CreateMigration above) with no collect
	// steps dead-ends the pipeline wizard: recovery finds nothing completed and
	// the user can never proceed. Interrupted lets recovery resync on next load.
	if ctx.Err() != nil {
		p.repo.UpdateMigrationStatus(planID, StatusInterrupted, "collection interrupted: client disconnected")
		emit(WSMessage{Step: "plan", Status: "error", Error: "collection interrupted"})
		return plan, ctx.Err()
	}

	// Tally results and mark the migration failed if any collection errored.
	collectionErrors := 0
	for _, res := range results {
		if res.err != nil {
			collectionErrors++
			p.repo.UpdateMigrationStatus(planID, StatusFailed, fmt.Sprintf("collection failed for %s: %v", res.catName, res.err))
		}
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
