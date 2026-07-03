package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"

	"golang.org/x/crypto/ssh"
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
	svc.SetHostKeyStore(modssh.NewKnownHostsStore(d))
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

func TestHandleDeleteMissingServerReturns404(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	req := httptest.NewRequest(http.MethodDelete, "/api/servers/999", nil)
	w := httptest.NewRecorder()
	h.handleDelete(w, req, 999)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	var apiErr shared.APIError
	if err := json.NewDecoder(w.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if apiErr.Error != "server not found" || apiErr.Code != "NOT_FOUND" {
		t.Fatalf("unexpected error response: %+v", apiErr)
	}
}

func TestHandleToggleFavoriteMissingServerReturns404(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	req := httptest.NewRequest(http.MethodPatch, "/api/servers/999/favorite", nil)
	w := httptest.NewRecorder()
	h.handleToggleFavorite(w, req, 999)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	var apiErr shared.APIError
	if err := json.NewDecoder(w.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if apiErr.Error != "server not found" || apiErr.Code != "NOT_FOUND" {
		t.Fatalf("unexpected error response: %+v", apiErr)
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

func startHandlerTrustTestSSHServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key failed: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create signer failed: %v", err)
	}

	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)
				for range chans {
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

func TestHandleTrustHostAndFingerprint(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	addr := startHandlerTrustTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	createBody, _ := json.Marshal(CreateRequest{
		Name:     "Trust Test",
		Host:     host,
		Port:     port,
		Username: "root",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/servers", bytes.NewReader(createBody))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected create 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/servers/1/trust-host", nil)
	w = httptest.NewRecorder()
	h.handleTrustHost(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected trust-host 200, got %d", w.Code)
	}

	var trustResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&trustResp); err != nil {
		t.Fatalf("decode trust response failed: %v", err)
	}
	if trustResp["fingerprint"] == "" {
		t.Fatal("expected fingerprint in trust response")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/servers/1/fingerprint", nil)
	w = httptest.NewRecorder()
	h.handleGetFingerprint(w, req, 1)
	if w.Code != http.StatusOK {
		t.Fatalf("expected fingerprint 200, got %d", w.Code)
	}

	var fpResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&fpResp); err != nil {
		t.Fatalf("decode fingerprint response failed: %v", err)
	}
	if fpResp["fingerprint"] != trustResp["fingerprint"] {
		t.Fatalf("expected matching fingerprints, got %q and %q", trustResp["fingerprint"], fpResp["fingerprint"])
	}
}
