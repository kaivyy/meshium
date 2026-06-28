package discovery

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"

	xssh "golang.org/x/crypto/ssh"
)

type fakeAuthKeyProvider struct {
	key []byte
}

func (f fakeAuthKeyProvider) GetAESKey() []byte {
	return f.key
}

type fakeConnectionPool struct {
	client SSHExecuter

	called     bool
	serverID   int
	serverConf modssh.ServerConfig
}

func (f *fakeConnectionPool) Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	f.called = true
	f.serverID = serverID
	f.serverConf = cfg
	return f.client, nil
}

func (f *fakeConnectionPool) GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	return f.Get(serverID, cfg, hostKeyCallback)
}

type fakeHostKeyStore struct{}

func (f fakeHostKeyStore) MakeHostKeyCallback(serverID int) xssh.HostKeyCallback {
	return func(string, net.Addr, xssh.PublicKey) error { return nil }
}

type fakeServerRepo struct {
	server      *server.Server
	savedInfo   server.ServerInfo
	savedRaw    string
	saveCalled  bool
	getByIDErr  error
	saveInfoErr error
}

func (f *fakeServerRepo) Create(s server.Server) (int, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeServerRepo) GetByID(id int) (*server.Server, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	if f.server == nil {
		return nil, errors.New("server not found")
	}
	return f.server, nil
}

func (f *fakeServerRepo) List(filter server.ListFilter) ([]server.Server, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeServerRepo) Update(id int, s server.Server) error { return errors.New("not implemented") }
func (f *fakeServerRepo) Delete(id int) error                  { return errors.New("not implemented") }
func (f *fakeServerRepo) ToggleFavorite(id int) error          { return errors.New("not implemented") }

func (f *fakeServerRepo) SaveServerInfo(serverID int, info server.ServerInfo, rawData string) error {
	if f.saveInfoErr != nil {
		return f.saveInfoErr
	}
	f.saveCalled = true
	f.savedInfo = info
	f.savedRaw = rawData
	return nil
}

func (f *fakeServerRepo) GetServerInfo(serverID int) (*server.ServerInfo, error) {
	return nil, errors.New("not implemented")
}

func TestRunConnectionTestWithMock(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	encPassword, err := shared.Encrypt(key, []byte("secret-password"))
	if err != nil {
		t.Fatalf("encrypt password: %v", err)
	}

	repo := &fakeServerRepo{
		server: &server.Server{
			ID:       1,
			Host:     "example.com",
			Port:     22,
			Username: "ubuntu",
			Password: string(encPassword),
		},
	}

	pool := &fakeConnectionPool{client: &mockSSHClient{
		responses: map[string]string{
			"hostname":   "test-server\n",
			"uname -r":   "5.15.0-76-generic\n",
			"uname -m":   "x86_64\n",
			"nproc":      "4\n",
			"free -m":    "              total        used        free\nMem:           8192        2048        6144\n",
			"os-release": `PRETTY_NAME="Ubuntu 22.04 LTS"`,
		},
		alive: true,
	}}

	svc := NewService(pool, repo, fakeAuthKeyProvider{key: key}, fakeHostKeyStore{})

	var messages []WSMessage
	if err := svc.RunConnectionTest(context.Background(), 1, func(msg WSMessage) {
		messages = append(messages, msg)
	}); err != nil {
		t.Fatalf("RunConnectionTest returned error: %v", err)
	}

	if !pool.called {
		t.Fatal("expected pool.Get to be called")
	}
	if pool.serverID != 1 {
		t.Fatalf("expected serverID 1, got %d", pool.serverID)
	}
	if pool.serverConf.Username != "ubuntu" || pool.serverConf.Host != "example.com" {
		t.Fatalf("unexpected server config: %+v", pool.serverConf)
	}

	if len(messages) != 15 {
		t.Fatalf("expected 15 websocket messages, got %d", len(messages))
	}
	if messages[0].Step != "ssh" || messages[0].Status != "success" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
	if messages[1].Step != "hostname" || messages[1].Value != "test-server" {
		t.Fatalf("unexpected hostname message: %+v", messages[1])
	}
	if messages[len(messages)-1].Step != "done" || messages[len(messages)-1].Status != "complete" {
		t.Fatalf("unexpected final message: %+v", messages[len(messages)-1])
	}

	if !repo.saveCalled {
		t.Fatal("expected server info to be cached")
	}
	if repo.savedInfo.Hostname != "test-server" {
		t.Fatalf("expected cached hostname test-server, got %q", repo.savedInfo.Hostname)
	}
	if repo.savedInfo.CPUCores != 4 {
		t.Fatalf("expected cached cpu cores 4, got %d", repo.savedInfo.CPUCores)
	}
	if !strings.Contains(repo.savedRaw, "test-server") {
		t.Fatalf("expected cached raw data to contain hostname, got %q", repo.savedRaw)
	}
}

func TestRunConnectionTestPreflightEmitsErrors(t *testing.T) {
	t.Run("server not found", func(t *testing.T) {
		repo := &fakeServerRepo{getByIDErr: errors.New("missing server")}
		svc := NewService(&fakeConnectionPool{}, repo, fakeAuthKeyProvider{key: []byte("01234567890123456789012345678901")}, fakeHostKeyStore{})

		var messages []WSMessage
		err := svc.RunConnectionTest(context.Background(), 1, func(msg WSMessage) {
			messages = append(messages, msg)
		})
		if err == nil || !strings.Contains(err.Error(), "server not found") {
			t.Fatalf("expected server not found error, got %v", err)
		}
		if len(messages) != 1 {
			t.Fatalf("expected one message, got %d", len(messages))
		}
		if messages[0].Step != "lookup" || messages[0].Status != "error" || messages[0].Error != "server not found" {
			t.Fatalf("unexpected lookup message: %+v", messages[0])
		}
	})

	t.Run("app locked", func(t *testing.T) {
		repo := &fakeServerRepo{server: &server.Server{ID: 1, Host: "example.com", Port: 22, Username: "ubuntu"}}
		svc := NewService(&fakeConnectionPool{}, repo, fakeAuthKeyProvider{}, fakeHostKeyStore{})

		var messages []WSMessage
		err := svc.RunConnectionTest(context.Background(), 1, func(msg WSMessage) {
			messages = append(messages, msg)
		})
		if err == nil || !strings.Contains(err.Error(), "app is locked") {
			t.Fatalf("expected app locked error, got %v", err)
		}
		if len(messages) != 1 {
			t.Fatalf("expected one message, got %d", len(messages))
		}
		if messages[0].Step != "auth" || messages[0].Status != "error" || messages[0].Error != "app is locked" {
			t.Fatalf("unexpected auth message: %+v", messages[0])
		}
	})

	t.Run("decrypt failure", func(t *testing.T) {
		keyForEncrypt := make([]byte, 32)
		for i := range keyForEncrypt {
			keyForEncrypt[i] = byte(i + 1)
		}
		encPassword, err := shared.Encrypt(keyForEncrypt, []byte("secret-password"))
		if err != nil {
			t.Fatalf("encrypt password: %v", err)
		}

		repo := &fakeServerRepo{server: &server.Server{ID: 1, Host: "example.com", Port: 22, Username: "ubuntu", Password: string(encPassword)}}
		svc := NewService(&fakeConnectionPool{}, repo, fakeAuthKeyProvider{key: []byte("abcdefghijklmnopqrstuvwxyzABCDEF")}, fakeHostKeyStore{})

		var messages []WSMessage
		err = svc.RunConnectionTest(context.Background(), 1, func(msg WSMessage) {
			messages = append(messages, msg)
		})
		if err == nil || !strings.Contains(err.Error(), "failed to decrypt password") {
			t.Fatalf("expected decrypt failure, got %v", err)
		}
		if len(messages) != 1 {
			t.Fatalf("expected one message, got %d", len(messages))
		}
		if messages[0].Step != "auth" || messages[0].Status != "error" || messages[0].Error != "failed to decrypt credentials" {
			t.Fatalf("unexpected auth message: %+v", messages[0])
		}
	})
}

func TestRunConnectionTestAbortsOnSSHDisconnect(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	encPassword, err := shared.Encrypt(key, []byte("secret-password"))
	if err != nil {
		t.Fatalf("encrypt password: %v", err)
	}

	repo := &fakeServerRepo{
		server: &server.Server{
			ID:       1,
			Host:     "example.com",
			Port:     22,
			Username: "ubuntu",
			Password: string(encPassword),
		},
	}

	pool := &fakeConnectionPool{client: &mockSSHClient{
		responses: map[string]string{
			"hostname": "test-server\n",
			"uname -r": "5.15.0-76-generic\n",
			"uname -m": "x86_64\n",
			"nproc":    "4\n",
		},
		errors: map[string]error{
			"free -m": errors.New("ssh: connection reset by peer"),
		},
		alive: false,
	}}

	svc := NewService(pool, repo, fakeAuthKeyProvider{key: key}, fakeHostKeyStore{})

	var messages []WSMessage
	err = svc.RunConnectionTest(context.Background(), 1, func(msg WSMessage) {
		messages = append(messages, msg)
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ssh connection lost") {
		t.Fatalf("expected ssh connection lost error, got %v", err)
	}
	if repo.saveCalled {
		t.Fatal("expected server info not to be cached after disconnect")
	}
	if len(messages) == 0 {
		t.Fatal("expected websocket messages to be sent")
	}
	if messages[len(messages)-1].Step != "ssh" || messages[len(messages)-1].Status != "error" || messages[len(messages)-1].Error != "SSH connection lost" {
		t.Fatalf("unexpected final message: %+v", messages[len(messages)-1])
	}
	for _, msg := range messages {
		if msg.Step == "done" {
			t.Fatalf("did not expect done message after ssh disconnect: %+v", msg)
		}
		if msg.Step == "ram_total_mb" {
			t.Fatalf("did not expect RAM step message after ssh disconnect: %+v", msg)
		}
	}
}
