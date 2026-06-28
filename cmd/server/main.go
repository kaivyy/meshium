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
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/planner"
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
	serverHandler := server.NewHandler(serverSvc)

	sshPool := ssh.NewPool(ssh.PoolConfig{
		MaxIdle:     10 * time.Minute,
		MaxLifetime: 30 * time.Minute,
	})
	knownHosts := ssh.NewKnownHostsStore(database)

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

	// 4. Create HTTP handlers
	defaultPlanner := planner.NewDefaultPlanner()
	jobHTTPHandler := handler.NewJobHandler(engine, jobStore)
	planHTTPHandler := handler.NewPlanHandler(defaultPlanner, planStore, snapshotStore, engine)
	discoveryRESTHandler := handler.NewDiscoveryHandler(snapshotStore, engine)
	terminalHandler := handler.NewTerminalHandler(handlerFactory, authSvc, serverRepo)

	// 5. Setup graceful shutdown
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
	terminalHandler.RegisterRoutes(mux)

	mux.Handle("/", staticHandler())

	// Wrap the mux with middleware layers (outermost to innermost):
	// 1. CORS + security headers
	// 2. CSRF (Content-Type validation for state-changing methods)
	// 3. Authentication (session token validation)
	protectedMux := shared.CORSMiddleware(shared.CSRFMiddleware(authMiddleware.RequireAuth(mux)))

	addr := ":" + cfg.ServerPort
	fmt.Printf("Meshium server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, protectedMux); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
