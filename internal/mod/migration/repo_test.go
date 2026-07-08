package migration

import (
	"database/sql"
	"testing"
)

// Regression: a migration with a NULL error column (the common case — never
// failed) must list without error. Scanning the nullable column into a plain
// string previously returned "converting NULL to string is unsupported".
func TestListMigrationsHandlesNullError(t *testing.T) {
	database, _ := newTestDB(t)
	insertServer(t, database, 1)
	insertServer(t, database, 2)

	r := NewRepo(database)
	if _, err := r.CreateMigration(1, 2, []string{"packages"}); err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	got, err := r.ListMigrations()
	if err != nil {
		t.Fatalf("ListMigrations failed on NULL error column: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(got))
	}
	if got[0].Error != "" {
		t.Fatalf("expected empty Error for never-failed migration, got %q", got[0].Error)
	}
}

func insertServer(t *testing.T, d *sql.DB, id int) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO servers (id, name, host, port, username) VALUES (?, ?, ?, ?, ?)`,
		id, "srv", "127.0.0.1", 22, "root",
	); err != nil {
		t.Fatalf("insert server %d failed: %v", id, err)
	}
}
