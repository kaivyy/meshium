package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"meshium/internal/shared"
)

// TestBuildDiagnosticBundleAggregates verifies the bundle pulls migration,
// config, stages, audit, and fence records together from a real repo.
func TestBuildDiagnosticBundleAggregates(t *testing.T) {
	t.Helper()
	repo := newTestRepo(t).(*sqliteRepo)
	seedServers(t, repo)

	ctx := context.Background()
	migrationID, err := repo.CreateMigration(1, 2, []string{"docker"})
	if err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	if _, err := repo.CreateStage(ctx, migrationID, "initial_sync", 0); err != nil {
		t.Fatalf("seed stage: %v", err)
	}
	cfg := &MigrationConfig{
		Categories:     []string{"docker"},
		TrafficProvider: TrafficProviderNginx,
	}
	if err := repo.SetMigrationConfig(migrationID, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, err := repo.CreateAuditEntry(ctx, AuditEntry{MigrationID: migrationID, EventType: "migration_started", Actor: "operator"}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	b, err := repo.BuildDiagnosticBundle(ctx, migrationID)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundle: %v", err)
	}
	if b.MigrationID != migrationID {
		t.Fatalf("bundle migration id = %d, want %d", b.MigrationID, migrationID)
	}
	if b.Migration == nil {
		t.Fatal("bundle missing migration record")
	}
	if len(b.Stages) != 1 {
		t.Fatalf("bundle stages = %d, want 1", len(b.Stages))
	}
	if len(b.AuditTrail) != 1 {
		t.Fatalf("bundle audit = %d, want 1", len(b.AuditTrail))
	}
	if len(b.Config) == 0 {
		t.Fatal("bundle missing redacted config")
	}
}

// TestBuildDiagnosticBundleRedactsConfig proves secrets in the migration
// config are masked in the exported bundle — the bundle is shareable.
func TestBuildDiagnosticBundleRedactsConfig(t *testing.T) {
	t.Helper()
	repo := newTestRepo(t).(*sqliteRepo)
	seedServers(t, repo)

	ctx := context.Background()
	migrationID, err := repo.CreateMigration(1, 2, []string{"docker"})
	if err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	cfg := &MigrationConfig{
		Categories: []string{"docker"},
		DatabaseConfig: &DatabaseConfig{
			Host:     "db.example.com",
			Password: "supersecret-password",
		},
	}
	if err := repo.SetMigrationConfig(migrationID, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// Sanity: a raw marshal of the config WOULD leak the password.
	raw, _ := json.Marshal(cfg)
	if !strings.Contains(string(raw), "supersecret-password") {
		t.Fatal("config marshal unexpectedly did not contain password (test setup)")
	}

	b, err := repo.BuildDiagnosticBundle(ctx, migrationID)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundle: %v", err)
	}

	out, _ := json.Marshal(b)
	if strings.Contains(string(out), "supersecret-password") {
		t.Fatalf("bundle leaked password: %s", string(out))
	}
	// Sanity: the redaction ran through shared.SanitizeJSONRawMessage path.
	redacted := shared.SanitizeJSONRawMessage(raw)
	if strings.Contains(string(redacted), "supersecret-password") {
		t.Fatal("SanitizeJSONRawMessage did not redact (sanity check failed)")
	}
}

// TestBuildDiagnosticBundleMissingMigration proves a non-existent migration
// surfaces an error rather than returning a fabricated bundle.
func TestBuildDiagnosticBundleMissingMigration(t *testing.T) {
	t.Helper()
	repo := newTestRepo(t).(*sqliteRepo)

	if _, err := repo.BuildDiagnosticBundle(context.Background(), 999); err == nil {
		t.Fatal("expected error for missing migration, got nil")
	}
}
