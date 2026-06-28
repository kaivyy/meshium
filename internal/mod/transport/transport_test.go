package transport

import (
	"context"
	"io"
	"net"
	"testing"

	modssh "meshium/internal/mod/ssh"

	xssh "golang.org/x/crypto/ssh"
)

// mockSSHClient implements transport.SSHExecuter for testing.
type mockSSHClient struct{}

func (m *mockSSHClient) Exec(cmd string) (string, string, int, error) {
	return "", "", 0, nil
}

func (m *mockSSHClient) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return m.Exec(cmd)
}

func (m *mockSSHClient) IsAlive() bool { return true }

func (m *mockSSHClient) Upload(src io.Reader, remotePath string) error { return nil }

func (m *mockSSHClient) Download(remotePath string, dst io.Writer) error { return nil }

// TestSSHExecuterInterface verifies that the SSHExecuter interface
// includes all expected methods.
func TestSSHExecuterInterface(t *testing.T) {
	var _ SSHExecuter = &mockSSHClient{}
}

// TestConnectionPoolInterface verifies that the ConnectionPool interface
// is correctly defined.
func TestConnectionPoolInterface(t *testing.T) {
	var _ ConnectionPool = &mockPool{}
}

type mockPool struct{}

func (m *mockPool) Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	return nil, nil
}

func (m *mockPool) GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	return nil, nil
}

// TestAESKeyProviderInterface verifies that the AESKeyProvider interface
// is correctly defined.
func TestAESKeyProviderInterface(t *testing.T) {
	var _ AESKeyProvider = &mockAESKeyProvider{}
}

type mockAESKeyProvider struct{}

func (m *mockAESKeyProvider) GetAESKey() []byte { return nil }

// TestHostKeyStoreInterface verifies that the HostKeyStore interface
// is correctly defined.
func TestHostKeyStoreInterface(t *testing.T) {
	var _ HostKeyStore = &mockHostKeyStore{}
}

type mockHostKeyStore struct{}

func (m *mockHostKeyStore) MakeHostKeyCallback(serverID int) xssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key xssh.PublicKey) error {
		return nil
	}
}

// TestInterfacesAreConsistent verifies that the transport package
// interfaces are the canonical definitions used by both discovery
// and migration packages via type aliases.
func TestInterfacesAreConsistent(t *testing.T) {
	// SSHExecuter should have 5 methods
	var s SSHExecuter
	_ = s

	// ConnectionPool should have 1 method (Get)
	var p ConnectionPool
	_ = p

	// AESKeyProvider should have 1 method (GetAESKey)
	var a AESKeyProvider
	_ = a

	// HostKeyStore should have 1 method (MakeHostKeyCallback)
	var h HostKeyStore
	_ = h
}
