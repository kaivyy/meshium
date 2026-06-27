package ssh

import "time"

// ServerConfig holds the connection parameters for a server.
type ServerConfig struct {
	ID         int
	Host       string
	Port       int
	Username   string
	Password   string
	PrivateKey []byte // raw PEM bytes
	Passphrase string
	Bastion    *BastionConfig // optional jump host configuration
}

// BastionConfig holds connection parameters for a bastion/jump host.
type BastionConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	PrivateKey []byte
	Passphrase string
}

// ConnectResult holds the result of a connection attempt.
type ConnectResult struct {
	Success     bool
	Latency     time.Duration
	Error       string
	HostKey     string
	NeedsVerify bool // true if host key not in known_hosts
}

// HostKeyResult holds the result of a host key check.
type HostKeyResult struct {
	Known  bool
	Match  bool
	Key    string
}
