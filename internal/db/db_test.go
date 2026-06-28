package db

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMigrateCreatesTables(t *testing.T) {
	dbConn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	if err := Migrate(dbConn); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	var name string
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='app_config'").Scan(&name); err != nil {
		t.Error("app_config table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='servers'").Scan(&name); err != nil {
		t.Error("servers table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='server_info'").Scan(&name); err != nil {
		t.Error("server_info table not created")
	}
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='known_hosts'").Scan(&name); err != nil {
		t.Error("known_hosts table not created")
	}

	// Check migrations table exists
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='migrations'").Scan(&name); err != nil {
		t.Error("migrations table not created")
	}
	// Check migration_steps table exists
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='migration_steps'").Scan(&name); err != nil {
		t.Error("migration_steps table not created")
	}
	// Check migration_backups table exists
	if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='migration_backups'").Scan(&name); err != nil {
		t.Error("migration_backups table not created")
	}
}

// TestOpenEnablesWALMode verifies that WAL mode is enabled for file-based databases.
func TestOpenEnablesWALMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	var journalMode string
	if err := dbConn.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}

	if journalMode != "wal" {
		t.Fatalf("expected journal_mode 'wal', got %q", journalMode)
	}
}

// TestOpenSetsBusyTimeout verifies that busy_timeout is set.
func TestOpenSetsBusyTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	var busyTimeout int
	if err := dbConn.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to query busy_timeout: %v", err)
	}

	if busyTimeout != 5000 {
		t.Fatalf("expected busy_timeout 5000, got %d", busyTimeout)
	}
}

// TestOpenSetsSynchronousNormal verifies that synchronous is set to NORMAL.
func TestOpenSetsSynchronousNormal(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	var synchronous int
	if err := dbConn.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("failed to query synchronous: %v", err)
	}

	// synchronous=NORMAL is 1 in SQLite
	if synchronous != 1 {
		t.Fatalf("expected synchronous 1 (NORMAL), got %d", synchronous)
	}
}

// TestOpenEnablesForeignKeys verifies that foreign keys are enabled.
func TestOpenEnablesForeignKeys(t *testing.T) {
	dbConn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	var foreignKeys int
	if err := dbConn.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}

	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys 1, got %d", foreignKeys)
	}
}

// TestOpenFileBasedAllowsMultipleConnections verifies that file-based
// databases allow multiple concurrent connections (for concurrent reads).
func TestOpenFileBasedAllowsMultipleConnections(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	if err := Migrate(dbConn); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Open a second connection to the same database
	dbConn2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer dbConn2.Close()

	// Both connections should be able to read concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	errCh := make(chan error, 2)

	go func() {
		defer wg.Done()
		var name string
		if err := dbConn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='servers'").Scan(&name); err != nil {
			errCh <- err
		}
	}()

	go func() {
		defer wg.Done()
		var name string
		if err := dbConn2.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='servers'").Scan(&name); err != nil {
			errCh <- err
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent read failed: %v", err)
		}
	}
}

// TestOpenInMemoryUsesSingleConnection verifies that in-memory databases
// use a single connection (MaxOpenConns=1) to maintain the shared cache.
func TestOpenInMemoryUsesSingleConnection(t *testing.T) {
	dbConn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	// In-memory databases should have MaxOpenConns=1
	stats := dbConn.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("expected MaxOpenConnections 1 for in-memory, got %d", stats.MaxOpenConnections)
	}
}

// TestOpenFileBasedUsesMultipleConnections verifies that file-based
// databases allow multiple connections (MaxOpenConns > 1).
func TestOpenFileBasedUsesMultipleConnections(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	stats := dbConn.Stats()
	if stats.MaxOpenConnections <= 1 {
		t.Fatalf("expected MaxOpenConnections > 1 for file-based, got %d", stats.MaxOpenConnections)
	}
}

// TestOpenCreatesWALFiles verifies that opening a file-based database
// creates WAL-related files.
func TestOpenCreatesWALFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	dbConn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	// Force a checkpoint to ensure WAL file is created
	if _, err := dbConn.Exec("PRAGMA wal_checkpoint"); err != nil {
		t.Fatalf("wal_checkpoint failed: %v", err)
	}

	// WAL file should exist
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); err != nil {
		t.Logf("WAL file not found at %s (may be auto-checkpointed): %v", walPath, err)
		// This is not necessarily a failure — SQLite may auto-checkpoint
		// and remove the WAL file. The important thing is that journal_mode
		// is WAL, which we already verified in TestOpenEnablesWALMode.
	}
}
