package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meshium/internal/db"
	"meshium/internal/handler"
	"meshium/internal/jobengine"
	"meshium/internal/mod/ai"
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/drift"
	"meshium/internal/mod/file"
	"meshium/internal/mod/firewall"
	"meshium/internal/mod/logview"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/monitoring"
	"meshium/internal/mod/planner"
	"meshium/internal/mod/process"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

func main() {
	cfg, err := shared.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	authRepo := auth.NewRepo(database)
	authSvc := auth.NewService(authRepo)
	authHandler := auth.NewHandler(authSvc)
	authMiddleware := auth.NewMiddleware(authSvc)

	serverRepo := server.NewRepo(database)
	serverSvc := server.NewService(serverRepo, authSvc)

	sshPool := ssh.NewPool(ssh.PoolConfig{
		MaxIdle:     10 * time.Minute,
		MaxLifetime: 30 * time.Minute,
	})
	knownHosts := ssh.NewKnownHostsStore(database)
	serverSvc.SetHostKeyStore(knownHosts)
	serverHandler := server.NewHandler(serverSvc)

	// Invalidate cached SSH connections when server config changes
	serverSvc.SetPoolInvalidator(sshPool)

	discoverySvc := discovery.NewService(discovery.NewPoolAdapter(sshPool), serverRepo, authSvc, knownHosts)
	discoveryHandler := discovery.NewHandler(discoverySvc)

	// Migration engine
	migrationRepo := migration.NewRepo(database)
	migrationRegistry := migration.NewCategoryRegistry()
	poolAdapter := &migration.PoolAdapter{Inner: discovery.NewPoolAdapter(sshPool)}
	migrationPlanner := migration.NewPlanner(migrationRegistry, migrationRepo, serverRepo, poolAdapter, authSvc, knownHosts)
	migrationExecutor := migration.NewExecutor(migrationRegistry, migrationRepo, serverRepo, poolAdapter, authSvc, knownHosts)
	migrationRollback := migration.NewRollbackManager(migrationRegistry, migrationRepo, serverRepo, poolAdapter, authSvc, knownHosts)
	migrationRunner := migration.NewCompositeRunner(migrationPlanner, migrationExecutor, migrationRollback)
	migrationHandler := migration.NewHandler(migrationRunner, migrationRepo)

	// New typed-state Engine (Phase 2) for the job engine path
	migrationEngine := migration.NewEngine(migrationRepo.(migration.JobRepository), serverRepo, poolAdapter, authSvc, knownHosts, migrationRegistry)

	// --- Phase 8: Job Engine, Planner, Discovery REST handlers ---

	// 1. Instantiate stores and ensure tables exist
	snapshotStore := discovery.NewSQLiteSnapshotStore(database)
	if err := snapshotStore.EnsureTable(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create discovery_snapshots table: %v\n", err)
		os.Exit(1)
	}

	planStore := planner.NewSQLitePlanStore(database)
	if err := planStore.EnsureTable(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create migration_plans table: %v\n", err)
		os.Exit(1)
	}

	jobStore := jobengine.NewSQLiteJobStore(database)
	if err := jobStore.EnsureTable(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create jobs/job_logs tables: %v\n", err)
		os.Exit(1)
	}

	jobQueue := jobengine.NewSQLiteJobQueue(database)
	if err := jobQueue.EnsureTable(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create job_queue table: %v\n", err)
		os.Exit(1)
	}

	// 2. Create handler factory for the job engine
	handlerFactory := handler.NewHandlerFactory(
		snapshotStore,
		planStore,
		serverRepo,
		sshPool,
		authSvc,
		knownHosts,
		migrationRepo,
		migrationEngine,
	)

	// 3. Create and start the job engine
	broadcaster := jobengine.NewDefaultProgressBroadcaster()
	engine := jobengine.NewEngine(jobengine.EngineConfig{
		Queue:          jobQueue,
		Store:          jobStore,
		Broadcaster:    broadcaster,
		HandlerFactory: handlerFactory,
		MaxWorkers:     1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start job engine: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Job engine started (maxWorkers=1)\n")

	// Recover interrupted migrations from a previous crash/restart.
	// This marks any migrations stuck in "running" state as "interrupted"
	// so they can be resumed by the user.
	recoveredIDs, err := migrationExecutor.RecoverInterrupted()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to recover interrupted migrations: %v\n", err)
	} else if len(recoveredIDs) > 0 {
		fmt.Printf("Recovered %d interrupted migration(s): %v\n", len(recoveredIDs), recoveredIDs)
	}

	// 4. Create HTTP handlers
	defaultPlanner := planner.NewDefaultPlanner()
	jobHTTPHandler := handler.NewJobHandler(engine, jobStore)
	planHTTPHandler := handler.NewPlanHandler(defaultPlanner, planStore, snapshotStore, engine)
	discoveryRESTHandler := handler.NewDiscoveryHandler(snapshotStore, engine)
	terminalHandler := handler.NewTerminalHandler(handlerFactory, authSvc, serverRepo)

	aiService := ai.NewService(snapshotStore, serverRepo, sshPool, authSvc, knownHosts)
	aiHandler := handler.NewAIHandler(aiService)

	// File explorer service and handler
	filePoolAdapter := &file.PoolAdapter{Inner: sshPool}
	fileService := file.NewService(serverRepo, filePoolAdapter, authSvc, knownHosts)
	fileHandler := handler.NewFileHandler(fileService)

	// Log viewer service and handler
	logViewService := logview.NewService(serverRepo, sshPool, authSvc, knownHosts)
	logViewHandler := handler.NewLogViewHandler(logViewService, authSvc)

	// Firewall manager service and handler
	firewallService := firewall.NewService(serverRepo, sshPool, authSvc, knownHosts)
	firewallHandler := handler.NewFirewallHandler(firewallService)

	// Process manager service and handler
	processService := process.NewService(serverRepo, sshPool, authSvc, knownHosts)
	processHandler := handler.NewProcessHandler(processService)

	// Drift detection service and handler
	driftService := drift.NewService(snapshotStore, serverRepo, sshPool, authSvc, knownHosts)
	driftHandler := handler.NewDriftHandler(driftService)

	// Monitoring service and handler
	monitoringService := monitoring.NewService(serverRepo, sshPool, authSvc, knownHosts)
	monitoringHandler := handler.NewMonitoringHandler(monitoringService, authSvc)

	// 5. Setup graceful shutdown
	httpServer := &http.Server{}
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Printf("\nShutting down...\n")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := engine.Stop(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "Job engine shutdown error: %v\n", err)
		}
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server shutdown error: %v\n", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterRoutes(mux)
	serverHandler.RegisterRoutes(mux)
	discoveryHandler.RegisterRoutes(mux)
	migrationHandler.RegisterRoutes(mux)

	// Register Phase 8 REST handlers
	jobHTTPHandler.RegisterRoutes(mux)
	planHTTPHandler.RegisterRoutes(mux)
	discoveryRESTHandler.RegisterRoutes(mux)
	aiHandler.RegisterRoutes(mux)
	terminalHandler.RegisterRoutes(mux)
	fileHandler.RegisterRoutes(mux)
	logViewHandler.RegisterRoutes(mux)
	firewallHandler.RegisterRoutes(mux)
	processHandler.RegisterRoutes(mux)
	driftHandler.RegisterRoutes(mux)
	monitoringHandler.RegisterRoutes(mux)

	mux.Handle("/", staticHandler())

	// Wrap the mux with middleware layers (outermost to innermost):
	// 1. CORS + security headers
	// 2. CSRF (Content-Type validation for state-changing methods)
	// 3. Authentication (session token validation)
	protectedMux := shared.CORSMiddleware(shared.CSRFMiddleware(authMiddleware.RequireAuth(mux)))

	addr := ":" + cfg.ServerPort
	fmt.Printf("Meshium server starting on %s\n", addr)
	httpServer.Addr = addr
	httpServer.Handler = protectedMux
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
