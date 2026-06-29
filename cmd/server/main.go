package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/middleware"
	"meshium/internal/mod/migration"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

func main() {
	logger := shared.NewLogger(os.Stdout, shared.ParseLogLevel(os.Getenv("MESHium_LOG_LEVEL")))
	shared.SetLogger(logger)

	cfg, err := shared.LoadConfig()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open database", "error", err, "path", cfg.DBPath)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	authRepo := auth.NewRepo(database)
	authSvc := auth.NewService(authRepo)
	sessionMgr := auth.NewSessionManager()
	authHandler := auth.NewHandler(authSvc, sessionMgr)

	serverRepo := server.NewRepo(database)
	serverSvc := server.NewService(serverRepo, authSvc)
	serverHandler := server.NewHandler(serverSvc)

	sshPool := ssh.NewPool(ssh.PoolConfig{
		MaxIdle:     10 * time.Minute,
		MaxLifetime: 30 * time.Minute,
	})
	defer sshPool.CloseAll()
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

	finalHandler := middleware.Chain(
		mux,
		middleware.SecurityHeaders(),
		middleware.RateLimit(),
		middleware.RequestSizeLimit(),
		func(next http.Handler) http.Handler {
			return auth.AuthMiddleware(authSvc, sessionMgr, next)
		},
	)

	httpServer := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           finalHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("meshium server starting", "addr", httpServer.Addr, "tlsEnabled", cfg.TLSCertFile != "" && cfg.TLSKeyFile != "")
		var serveErr error
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			logger.Info("tls enabled", "certFile", cfg.TLSCertFile)
			serveErr = httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			serveErr = httpServer.ListenAndServe()
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("server error", "error", serveErr)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	<-quit

	logger.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
	}

	logger.Info("server exited")
}
