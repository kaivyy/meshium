// Package transport defines the shared interfaces for SSH transport,
// connection pooling, and credential management. Both the discovery and
// migration packages depend on these interfaces instead of defining
// their own duplicate copies.
package transport

import (
	"context"
	"io"

	modssh "meshium/internal/mod/ssh"

	xssh "golang.org/x/crypto/ssh"
)

// SSHExecuter is the interface for executing commands and transferring
// files over an SSH connection. It is satisfied by *ssh.Client.
type SSHExecuter interface {
	Exec(cmd string) (string, string, int, error)
	ExecContext(ctx context.Context, cmd string) (string, string, int, error)
	IsAlive() bool
	Upload(src io.Reader, remotePath string) error
	Download(remotePath string, dst io.Writer) error
}

// ConnectionPool provides SSH clients for a server.
type ConnectionPool interface {
	Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error)
	GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error)
}

// AESKeyProvider exposes the AES key needed to decrypt stored credentials.
type AESKeyProvider interface {
	GetAESKey() []byte
}

// HostKeyStore provides host key verification callbacks.
type HostKeyStore interface {
	MakeHostKeyCallback(serverID int) xssh.HostKeyCallback
}
