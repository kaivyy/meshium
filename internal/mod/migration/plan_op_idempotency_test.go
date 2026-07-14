package migration

import (
	"testing"
)

// phase-4g1: operationId is persisted and reconcileable. A refresh submits the
// same op key and the backend returns the existing migration (no second row).
// The actual dedup-on-resubmit lives in handlePlanWS; this test pins the repo
// contract it depends on.
func TestCreateMigrationPersistsOperationID(t *testing.T) {
	database, _ := newTestDB(t)
	insertServer(t, database, 1)
	insertServer(t, database, 2)
	r := NewRepo(database)

	const op = "op-abc-123"
	id, err := r.CreateMigration(1, 2, []string{"packages", "docker"}, op)
	if err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	// Reconcile by op → returns the same row.
	got, err := r.GetMigrationByOperationID(op)
	if err != nil {
		t.Fatalf("GetMigrationByOperationID failed: %v", err)
	}
	if got.ID != id {
		t.Fatalf("expected id %d, got %d", id, got.ID)
	}
	if got.OperationID != op {
		t.Fatalf("expected OperationID %q, got %q", op, got.OperationID)
	}

	// GetMigration also surfaces the op (FE uses it to pair id<->op).
	m, err := r.GetMigration(id)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}
	if m.OperationID != op {
		t.Fatalf("expected GetMigration OperationID %q, got %q", op, m.OperationID)
	}
}

func TestGetMigrationByOperationIDMissing(t *testing.T) {
	database, _ := newTestDB(t)
	insertServer(t, database, 1)
	insertServer(t, database, 2)
	r := NewRepo(database)
	if _, err := r.GetMigrationByOperationID("op-nope"); err == nil {
		t.Fatal("expected ErrMigrationNotFound for unknown op id")
	}
	// An empty op id must not match a row created with a real op id.
	_, _ = r.CreateMigration(1, 2, []string{"docker"}, "op-real")
	if _, err := r.GetMigrationByOperationID(""); err == nil {
		t.Fatal("empty op id must not match a real-op row")
	}
}

func TestCreateMigrationDistinctOpIDs(t *testing.T) {
	database, _ := newTestDB(t)
	insertServer(t, database, 1)
	insertServer(t, database, 2)
	r := NewRepo(database)

	a, err := r.CreateMigration(1, 2, []string{"packages"}, "op-a")
	if err != nil {
		t.Fatalf("create a failed: %v", err)
	}
	b, err := r.CreateMigration(1, 2, []string{"packages"}, "op-b")
	if err != nil {
		t.Fatalf("create b failed: %v", err)
	}
	if a == b {
		t.Fatalf("distinct op ids must produce distinct migrations")
	}
	all, err := r.ListMigrations()
	if err != nil {
		t.Fatalf("ListMigrations failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(all))
	}
}
