package server

import (
	"database/sql"
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
