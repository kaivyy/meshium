package main

import (
	"fmt"
	"net/http"
	"os"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterRoutes(mux)

	addr := ":" + cfg.ServerPort
	fmt.Printf("Meshium server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
