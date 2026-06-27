package server

import (
	"database/sql"
	"testing"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
)

func setupServiceTest(t *testing.T) (*Service, Repo, *auth.Service, *sql.DB) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	repo := NewRepo(d)
	authRepo := auth.NewRepo(d)
	authSvc := auth.NewService(authRepo)
	if err := authSvc.Setup("test-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	svc := NewService(repo, authSvc)
	return svc, repo, authSvc, d
}

func TestServiceCreateServer(t *testing.T) {
	svc, repo, _, d := setupServiceTest(t)
	defer d.Close()

	req := CreateRequest{
		Name:       "Test Server",
		Host:       "192.168.1.1",
		Port:       22,
		Username:   "root",
		Password:   "secret",
		SSHKey:     "ssh-private-key",
		Passphrase: "ssh-passphrase",
		Tags:       []string{"web"},
	}

	server, err := svc.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if server.ID != 1 {
		t.Errorf("expected ID 1, got %d", server.ID)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got %q", server.Name)
	}
	if server.Password != "" || server.SSHKey != "" || server.Passphrase != "" {
		t.Fatal("response should not include credential fields")
	}

	stored, err := repo.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if stored.Password == req.Password {
		t.Fatal("password should be stored encrypted")
	}
	if stored.SSHKey == req.SSHKey {
		t.Fatal("ssh key should be stored encrypted")
	}
	if stored.Passphrase == req.Passphrase {
		t.Fatal("passphrase should be stored encrypted")
	}

	password, sshKey, passphrase, err := svc.GetDecryptedCredentials(1)
	if err != nil {
		t.Fatalf("GetDecryptedCredentials failed: %v", err)
	}
	if password != req.Password || sshKey != req.SSHKey || passphrase != req.Passphrase {
		t.Fatalf("unexpected decrypted credentials: %q %q %q", password, sshKey, passphrase)
	}
}

func TestServiceGetByID(t *testing.T) {
	svc, _, _, d := setupServiceTest(t)
	defer d.Close()

	_, err := svc.Create(CreateRequest{Name: "Test Server", Host: "192.168.1.1", Port: 22, Username: "root", Password: "secret"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	server, err := svc.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected 'Test Server', got %q", server.Name)
	}
	if server.Password != "" || server.SSHKey != "" || server.Passphrase != "" {
		t.Fatal("response should not include credential fields")
	}
}

func TestServiceList(t *testing.T) {
	svc, _, _, d := setupServiceTest(t)
	defer d.Close()

	_, err := svc.Create(CreateRequest{Name: "Server 1", Host: "10.0.0.1", Port: 22, Username: "root"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	_, err = svc.Create(CreateRequest{Name: "Server 2", Host: "10.0.0.2", Port: 22, Username: "root", Environment: "development"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	servers, err := svc.List(ListFilter{Environment: "development"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Name != "Server 2" {
		t.Fatalf("expected 'Server 2', got %q", servers[0].Name)
	}
}

func TestServiceUpdate(t *testing.T) {
	svc, _, _, d := setupServiceTest(t)
	defer d.Close()

	_, err := svc.Create(CreateRequest{Name: "Server 1", Host: "10.0.0.1", Port: 22, Username: "root", Password: "secret"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	newName := "Updated Server"
	newPassword := "new-secret"
	newTags := []string{"web", "prod"}
	req := UpdateRequest{
		Name:     &newName,
		Password: &newPassword,
		Tags:     &newTags,
	}
	if err := svc.Update(1, req); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	server, err := svc.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if server.Name != newName {
		t.Fatalf("expected %q, got %q", newName, server.Name)
	}
	if len(server.Tags) != 2 || server.Tags[1] != "prod" {
		t.Fatalf("expected updated tags, got %v", server.Tags)
	}

	password, _, _, err := svc.GetDecryptedCredentials(1)
	if err != nil {
		t.Fatalf("GetDecryptedCredentials failed: %v", err)
	}
	if password != newPassword {
		t.Fatalf("expected updated password %q, got %q", newPassword, password)
	}
}

func TestServiceDelete(t *testing.T) {
	svc, _, _, d := setupServiceTest(t)
	defer d.Close()

	_, err := svc.Create(CreateRequest{Name: "To Delete", Host: "10.0.0.1", Port: 22, Username: "root"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := svc.Delete(1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := svc.GetByID(1); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestServiceToggleFavorite(t *testing.T) {
	svc, _, _, d := setupServiceTest(t)
	defer d.Close()

	_, err := svc.Create(CreateRequest{Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := svc.ToggleFavorite(1); err != nil {
		t.Fatalf("ToggleFavorite failed: %v", err)
	}
	server, err := svc.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if !server.Favorite {
		t.Fatal("expected favorite to be true after toggle")
	}

	if err := svc.ToggleFavorite(1); err != nil {
		t.Fatalf("ToggleFavorite failed: %v", err)
	}
	server, err = svc.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if server.Favorite {
		t.Fatal("expected favorite to be false after second toggle")
	}
}
