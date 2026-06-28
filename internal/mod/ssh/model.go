package ssh

import (
	"time"

	xssh "golang.org/x/crypto/ssh"
)

// TimeoutConfig defines timeouts for different SSH operation types.
// A zero value for any field means "use the default" — see DefaultTimeouts.
type TimeoutConfig struct {
	// Connect is the timeout for establishing an SSH connection.
	Connect time.Duration
	// Command is the default timeout for a single SSH command.
	Command time.Duration
	// FileTransfer is the timeout for SFTP upload/download operations.
	FileTransfer time.Duration
}

// Predefined timeout profiles for different use cases.
// Callers should use these as starting points and override individual
// fields as needed.

// DefaultTimeouts is the general-purpose timeout profile.
// Suitable for most operations with moderate latency requirements.
var DefaultTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      30 * time.Second,
	FileTransfer: 5 * time.Minute,
}

// DiscoveryTimeouts is for system discovery commands (hostname, uname, lscpu, etc.).
// These are fast commands that should complete in seconds.
var DiscoveryTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      15 * time.Second,
	FileTransfer: 30 * time.Second,
}

// MigrationTimeouts is for general migration operations (config copying,
// service management, user creation, etc.).
var MigrationTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      5 * time.Minute,
	FileTransfer: 10 * time.Minute,
}

// PackageInstallTimeouts is for package installation/removal commands
// (apt-get install, dnf install, etc.) which can take many minutes.
var PackageInstallTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      10 * time.Minute,
	FileTransfer: 5 * time.Minute,
}

// InteractiveTimeouts is for interactive commands that may run indefinitely.
// A zero Command timeout means no explicit timeout — only the parent context applies.
var InteractiveTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      0, // no timeout — relies on parent context
	FileTransfer: 5 * time.Minute,
}

// DatabaseTimeouts is for remote database operations (mysql, psql, etc.)
// which may take longer than typical commands.
var DatabaseTimeouts = TimeoutConfig{
	Connect:      10 * time.Second,
	Command:      5 * time.Minute,
	FileTransfer: 5 * time.Minute,
}

// withDefaults returns a copy of cfg with zero values replaced by DefaultTimeouts.
func (cfg TimeoutConfig) withDefaults() TimeoutConfig {
	out := cfg
	if out.Connect == 0 {
		out.Connect = DefaultTimeouts.Connect
	}
	if out.Command == 0 {
		out.Command = DefaultTimeouts.Command
	}
	if out.FileTransfer == 0 {
		out.FileTransfer = DefaultTimeouts.FileTransfer
	}
	return out
}

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
	// Timeouts optionally specifies timeout values for this connection.
	// If zero, DefaultTimeouts is used.
	Timeouts TimeoutConfig
}

// BastionConfig holds connection parameters for a bastion/jump host.
type BastionConfig struct {
	Host            string
	Port            int
	Username        string
	Password        string
	PrivateKey      []byte
	Passphrase      string
	HostKeyCallback xssh.HostKeyCallback // optional: if nil, connection will fail
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
