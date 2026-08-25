package server

import (
	"database/sql"
	"errors"
	"testing"

	"meshium/internal/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return d
}

func TestCreateAndGetByID(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	srv := Server{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "encrypted-password",
		Tags:     []string{"web", "prod"},
	}

	id, err := repo.Create(srv)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id != 1 {
		t.Errorf("expected ID 1, got %d", id)
	}

	got, err := repo.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got %q", got.Name)
	}
	if got.Host != "192.168.1.1" {
		t.Errorf("expected host '192.168.1.1', got %q", got.Host)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "web" {
		t.Errorf("expected tags ['web', 'prod'], got %v", got.Tags)
	}
}

func TestList(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	repo.Create(Server{Name: "Server 1", Host: "10.0.0.1", Port: 22, Username: "root"})
	repo.Create(Server{Name: "Server 2", Host: "10.0.0.2", Port: 22, Username: "root"})

	servers, err := repo.List(ListFilter{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
}

func TestListWithFilter(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	repo.Create(Server{Name: "Prod", Host: "10.0.0.1", Port: 22, Username: "root", Environment: "production"})
	repo.Create(Server{Name: "Dev", Host: "10.0.0.2", Port: 22, Username: "root", Environment: "development"})

	servers, err := repo.List(ListFilter{Environment: "production"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Name != "Prod" {
		t.Errorf("expected 'Prod', got %q", servers[0].Name)
	}
}

func TestDelete(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "To Delete", Host: "10.0.0.1", Port: 22, Username: "root"})

	err := repo.Delete(id)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = repo.GetByID(id)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	err := repo.Delete(999)
	if err == nil {
		t.Fatal("expected error when deleting missing server")
	}
	if err.Error() != "server not found" {
		t.Fatalf("expected server not found error, got %v", err)
	}
}

func TestToggleFavoriteMissingReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	err := repo.ToggleFavorite(999)
	if err == nil {
		t.Fatal("expected error when toggling favorite on missing server")
	}
	if err.Error() != "server not found" {
		t.Fatalf("expected server not found error, got %v", err)
	}
}

func TestSaveAndGetServerInfo(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root"})

	info := ServerInfo{
		SSHStatus: "connected",
		Hostname:  "web-01",
		OS:        "Ubuntu 22.04",
	}

	err := repo.SaveServerInfo(id, info, `{"raw":"data"}`)
	if err != nil {
		t.Fatalf("SaveServerInfo failed: %v", err)
	}

	got, err := repo.GetServerInfo(id)
	if err != nil {
		t.Fatalf("GetServerInfo failed: %v", err)
	}
	if got.Hostname != "web-01" {
		t.Errorf("expected hostname 'web-01', got %q", got.Hostname)
	}
}

// connection_history's DDL was lost from Migrate() in the July 2026 merge
// while repo.go kept INSERTing into it — fresh databases failed every recorded
// connection with "no such table". This pins the table's existence.
func TestRecordConnectionPersistsHistory(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "Hist", Host: "10.0.0.1", Port: 22, Username: "root"})

	if err := repo.RecordConnection(id, true, 42, "", "10.0.0.9", "SHA256:abc", "password"); err != nil {
		t.Fatalf("RecordConnection failed: %v", err)
	}

	entries, err := repo.GetConnectionHistory(id, 10)
	if err != nil {
		t.Fatalf("GetConnectionHistory failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(entries))
	}
	if !entries[0].Success || entries[0].DurationMs != 42 {
		t.Errorf("unexpected entry %+v", entries[0])
	}
}

func mustExec(t *testing.T, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// Deleting a server that migrations/backups/health-history still reference
// used to surface as a raw SQLite "FOREIGN KEY constraint failed" and a
// generic 500. The repo must refuse up front with a typed error naming what
// blocks it, and leave the server in place.
func TestDeleteBlockedByReferencesReturnsTypedError(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "Ref", Host: "10.0.0.1", Port: 22, Username: "root"})
	mustExec(t, d, `INSERT INTO migrations (source_id, target_id, categories, status) VALUES (?, ?, '{}', 'planned')`, id, id)
	mustExec(t, d, `INSERT INTO migration_backups (migration_id, server_id, category, backup_path, backup_type) VALUES (1, ?, 'configs', '/tmp/b', 'file')`, id)
	mustExec(t, d, `INSERT INTO health_history (migration_id, server_id, check_type, status) VALUES (1, ?, 'ping', 'ok')`, id)

	err := repo.Delete(id)
	var ref *ReferencedError
	if !errors.As(err, &ref) {
		t.Fatalf("expected *ReferencedError, got %v", err)
	}
	if ref.Migrations != 1 || ref.Backups != 1 || ref.Health != 1 {
		t.Errorf("unexpected counts %+v", ref)
	}
	want := "server is referenced by 1 migration(s), 1 backup record(s), 1 health check record(s)"
	if ref.Error() != want {
		t.Errorf("message:\n got %q\nwant %q", ref.Error(), want)
	}
	if _, getErr := repo.GetByID(id); getErr != nil {
		t.Error("blocked delete must leave the server in place")
	}
}
