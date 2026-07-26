package db

import (
	"path/filepath"
	"testing"
)

func seedMaintainDB(t *testing.T) (dbPath string, closeFn func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	stmts := []string{
		`CREATE TABLE migrations (id INTEGER PRIMARY KEY, status TEXT, created_at DATETIME)`,
		`CREATE TABLE migration_steps (id INTEGER PRIMARY KEY, migration_id INTEGER, data TEXT)`,
		// old + terminal → prunable payload
		`INSERT INTO migrations VALUES (1,'completed', datetime('now','-90 days'))`,
		`INSERT INTO migration_steps VALUES (1,1,'` + bigPayload() + `')`,
		// old but RUNNING → must never be touched
		`INSERT INTO migrations VALUES (2,'running', datetime('now','-90 days'))`,
		`INSERT INTO migration_steps VALUES (2,2,'` + bigPayload() + `')`,
		// recent terminal → inside retention, keep
		`INSERT INTO migrations VALUES (3,'failed', datetime('now','-1 days'))`,
		`INSERT INTO migration_steps VALUES (3,3,'` + bigPayload() + `')`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return path, func() { conn.Close() }
}

// bigPayload exceeds Maintain's 64KiB prune threshold — small payloads are
// deliberately kept so compare on modest old plans still works.
func bigPayload() string {
	b := make([]byte, 100_000)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

// The production DB reached 429MB: a single stale plan carried a 113MB step
// payload forever, and freed pages were never vacuumed. Maintain prunes step
// payloads of migrations past the retention window that are not active, and
// leaves everything else untouched.
func TestMaintainPrunesOnlyOldInactivePayloads(t *testing.T) {
	path, done := seedMaintainDB(t)
	defer done()

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn.Close()

	res, err := Maintain(conn, 30)
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if res.StepsPruned != 1 {
		t.Errorf("StepsPruned = %d, want 1", res.StepsPruned)
	}

	var l1, l2, l3 int
	conn.QueryRow(`SELECT length(data) FROM migration_steps WHERE id=1`).Scan(&l1)
	conn.QueryRow(`SELECT length(data) FROM migration_steps WHERE id=2`).Scan(&l2)
	conn.QueryRow(`SELECT length(data) FROM migration_steps WHERE id=3`).Scan(&l3)

	if l1 >= 100_000 {
		t.Errorf("old terminal payload not pruned (len %d)", l1)
	}
	var marker string
	conn.QueryRow(`SELECT data FROM migration_steps WHERE id=1`).Scan(&marker)
	if marker != prunedMarker {
		t.Errorf("pruned payload should carry the explicit retention marker, got %q", marker)
	}
	if l2 < 100_000 {
		t.Errorf("RUNNING migration payload was pruned — never touch active work (len %d)", l2)
	}
	if l3 < 100_000 {
		t.Errorf("recent payload inside retention was pruned (len %d)", l3)
	}
}

// Maintain must be a no-op (not an error) on a database without the
// migration tables — the db package is also used by tests/tools with other
// schemas.
func TestMaintainNoopWithoutTables(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "empty.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if _, err := Maintain(conn, 30); err != nil {
		t.Fatalf("Maintain on empty schema: %v", err)
	}
}
