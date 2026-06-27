package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
)

func setupHandlerTest(t *testing.T) (*Handler, *sql.DB) {
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
	h := NewHandler(svc)
	return h, d
}

func TestHandleCreateServer(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	body, _ := json.Marshal(CreateRequest{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var server Server
	if err := json.NewDecoder(w.Body).Decode(&server); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected 'Test Server', got %q", server.Name)
	}
	if server.Password != "" {
		t.Error("password should not be in response")
	}
}

func TestHandleListServers(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	body, _ := json.Marshal(CreateRequest{
		Name:     "Test",
		Host:     "10.0.0.1",
		Port:     22,
		Username: "root",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	req = httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	w = httptest.NewRecorder()
	h.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var servers []Server
	if err := json.NewDecoder(w.Body).Decode(&servers); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
}

func TestHandleGetUpdateDeleteAndFavorite(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	body, _ := json.Marshal(CreateRequest{
		Name:     "Test",
		Host:     "10.0.0.1",
		Port:     22,
		Username: "root",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	req = httptest.NewRequest(http.MethodGet, "/api/servers/1", nil)
	w = httptest.NewRecorder()
	h.handleGet(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	newName := "Updated"
	newRegion := "us-east"
	updateBody, _ := json.Marshal(UpdateRequest{Name: &newName, Region: &newRegion})
	req = httptest.NewRequest(http.MethodPut, "/api/servers/1", bytes.NewReader(updateBody))
	w = httptest.NewRecorder()
	h.handleUpdate(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var server Server
	if err := json.NewDecoder(w.Body).Decode(&server); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if server.Name != newName || server.Region != newRegion {
		t.Fatalf("unexpected updated server: %+v", server)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/servers/1/favorite", nil)
	w = httptest.NewRecorder()
	h.handleToggleFavorite(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/servers/1", nil)
	w = httptest.NewRecorder()
	h.handleDelete(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleGetServerInfo(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	body, _ := json.Marshal(CreateRequest{
		Name:     "Test",
		Host:     "10.0.0.1",
		Port:     22,
		Username: "root",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	repo := NewRepo(d)
	if err := repo.SaveServerInfo(1, ServerInfo{SSHStatus: "connected", Hostname: "web-01"}, `{"raw":"data"}`); err != nil {
		t.Fatalf("SaveServerInfo failed: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/servers/1/info", nil)
	w = httptest.NewRecorder()
	h.handleGetInfo(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var info ServerInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if info.Hostname != "web-01" {
		t.Fatalf("expected hostname 'web-01', got %q", info.Hostname)
	}
}
