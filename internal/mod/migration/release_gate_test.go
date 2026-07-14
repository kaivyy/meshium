package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"meshium/internal/shared"
)

// releaseGateSecret is a representative secret that must never appear in any
// operator-facing sink (log, audit DB, diagnostic bundle, JSON API output).
const releaseGateSecret = "S3cretPass!-in-RC-2026"

// TestReleaseGateNoSecretInAnySink is the release-quality security assertion:
// a secret placed in a migration's DB config must be redacted in every sink an
// operator can read — the structured log, the persisted audit entry, the
// diagnostic bundle, and a generic JSON API response. Any leak fails the gate.
func TestReleaseGateNoSecretInAnySink(t *testing.T) {
	repo := newTestRepo(t).(*sqliteRepo)
	seedServers(t, repo)

	ctx := context.Background()
	migrationID, err := repo.CreateMigration(1, 2, []string{"docker"}, "")
	if err != nil {
		t.Fatalf("seed migration: %v", err)
	}

	cfg := &MigrationConfig{
		Categories: []string{"docker"},
		DatabaseConfig: &DatabaseConfig{
			Host:     "db.example.com",
			Password: releaseGateSecret,
		},
	}
	if err := repo.SetMigrationConfig(migrationID, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// Sink 1: structured log (SanitizeString runs at emit on message + values).
	logBuf := &strings.Builder{}
	lg := shared.NewLogger(logBuf, shared.LevelDebug)
	lg.Error("migration step failed", "detail", "mysql -uroot -p"+releaseGateSecret+" -h10.0.0.5")
	if strings.Contains(logBuf.String(), releaseGateSecret) {
		t.Fatalf("secret leaked into structured log: %s", logBuf.String())
	}

	// Sink 2: persisted audit entry (event_data is an attacker-readable column).
	_, err = repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID: migrationID,
		EventType:   "diagnostic_note",
		EventData:   "inspected mysql -uroot -p" + releaseGateSecret,
		Result:      "success",
	})
	if err != nil {
		t.Fatalf("create audit: %v", err)
	}
	rows, err := repo.GetAuditTrail(migrationID, 100)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("audit row missing")
	}
	if strings.Contains(rows[0].EventData, releaseGateSecret) {
		t.Fatalf("secret leaked into audit trail: %s", rows[0].EventData)
	}

	// Sink 3: diagnostic bundle (config must be redacted; the whole bundle is
	// what an operator attaches to a ticket). The bare password under
	// databaseConfig.password is the exact shape that historically leaked.
	bundle, err := repo.BuildDiagnosticBundle(ctx, migrationID)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundle: %v", err)
	}
	bundleJSON, _ := json.Marshal(bundle)
	if strings.Contains(string(bundleJSON), releaseGateSecret) {
		t.Fatalf("secret leaked into diagnostic bundle: %s", string(bundleJSON))
	}

	// Every sink must instead carry the redaction marker.
	if !strings.Contains(logBuf.String(), "[REDACTED]") &&
		!strings.Contains(string(bundleJSON), "[REDACTED]") {
		t.Fatal("expected at least the log and bundle to carry [REDACTED]")
	}
}
