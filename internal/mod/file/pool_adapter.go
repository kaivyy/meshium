package file

import (
	"context"
	"net"

	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"

	xssh "golang.org/x/crypto/ssh"
)

// PoolAdapter adapts ssh.Pool to the transport.ConnectionPool interface.
type PoolAdapter struct {
	Inner interface {
		Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key xssh.PublicKey) error) (*modssh.Client, error)
		GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key xssh.PublicKey) error) (*modssh.Client, error)
	}
}

// Get implements transport.ConnectionPool.Get.
func (a *PoolAdapter) Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (transport.SSHExecuter, error) {
	client, err := a.Inner.Get(serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// GetContext implements transport.ConnectionPool.GetContext.
func (a *PoolAdapter) GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (transport.SSHExecuter, error) {
	client, err := a.Inner.GetContext(ctx, serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	return client, nil
}
