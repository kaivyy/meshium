package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterRoutes(mux)
	serverHandler.RegisterRoutes(mux)
	discoveryHandler.RegisterRoutes(mux)
	migrationHandler.RegisterRoutes(mux)
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
