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
	ID                  int
	Host                string
	Port                int
	Username            string
	Password            string
	PrivateKey          []byte // raw PEM bytes
	Passphrase          string
	Bastion             *BastionConfig // optional jump host configuration
	Timeouts            TimeoutConfig
	UseAgent            bool
	KeyboardInteractive bool
	AuthMethod          string
	KeyType             string
	PrivateKeys         [][]byte // Multiple private keys (for multiple key support)
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

// AuthMethod constants for specifying the authentication method.
const (
	AuthMethodPassword            = "password"
	AuthMethodKey                 = "key"
	AuthMethodPublicKey           = AuthMethodKey
	AuthMethodAgent               = "agent"
	AuthMethodKeyboardInteractive = "keyboard-interactive"
)

// CredentialStatus constants for tracking credential health.
const (
	CredentialStatusUnknown            = "unknown"
	CredentialStatusValid              = "valid"
	CredentialStatusInvalid            = "invalid"
	CredentialStatusBroken             = "broken"
	CredentialStatusNeverConnected     = "never_connected"
	CredentialStatusVerificationFailed = "verification_failed"
	CredentialStatusAuthFailed         = "auth_failed"
	CredentialStatusFingerprintChanged = "fingerprint_changed"
)

// Host key verification status values.
const (
	HostStatusUnknown  = "unknown"
	HostStatusVerified = "verified"
	HostStatusChanged  = "changed"
)

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
	Known bool
	Match bool
	Key   string
}

// WizardStep describes one connection-wizard diagnostic step.
type WizardStep struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Duration int64  `json:"duration,omitempty"`
}

// WizardResult is returned by the interactive SSH connection wizard.
type WizardResult struct {
	Success       bool         `json:"success"`
	Error         string       `json:"error,omitempty"`
	Steps         []WizardStep `json:"steps"`
	ServerInfo    *ServerInfo  `json:"serverInfo,omitempty"`
	TotalDuration int64        `json:"totalDuration,omitempty"`
}

// ServerInfo holds lightweight remote host facts collected by the SSH wizard.
type ServerInfo struct {
	SSHStatus      string  `json:"sshStatus"`
	LatencyMs      int     `json:"latencyMs"`
	Hostname       string  `json:"hostname"`
	OS             string  `json:"os"`
	Kernel         string  `json:"kernel"`
	Architecture   string  `json:"architecture"`
	CPUModel       string  `json:"cpuModel"`
	CPUCores       int     `json:"cpuCores"`
	RAMTotalMB     int     `json:"ramTotalMb"`
	DiskTotalGB    float64 `json:"diskTotalGb"`
	Virtualization string  `json:"virtualization"`
	Provider       string  `json:"provider"`
	PublicIP       string  `json:"publicIp"`
	PrivateIP      string  `json:"privateIp"`
	Timezone       string  `json:"timezone"`
}

// DiagnosticsResult groups SSH connectivity diagnostics by subsystem.
type DiagnosticsResult struct {
	DNS     *DNSDiag     `json:"dns,omitempty"`
	TCP     *TCPDiag     `json:"tcp,omitempty"`
	SSH     *SSHDiag     `json:"ssh,omitempty"`
	Auth    *AuthDiag    `json:"auth,omitempty"`
	HostKey *HostKeyDiag `json:"hostKey,omitempty"`
	Network *NetworkDiag `json:"network,omitempty"`
}

type DNSDiag struct {
	Resolved   bool     `json:"resolved"`
	Hostname   string   `json:"hostname"`
	Addresses  []string `json:"addresses"`
	Error      string   `json:"error,omitempty"`
	DurationMs int64    `json:"durationMs"`
}

type TCPDiag struct {
	Reachable  bool   `json:"reachable"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type SSHDiag struct {
	Reachable     bool     `json:"reachable"`
	Banner        string   `json:"banner,omitempty"`
	Ciphers       []string `json:"ciphers,omitempty"`
	KEXAlgorithms []string `json:"kexAlgorithms,omitempty"`
	Error         string   `json:"error,omitempty"`
	DurationMs    int64    `json:"durationMs"`
}

type AuthDiag struct {
	Success    bool   `json:"success"`
	Method     string `json:"method,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type HostKeyDiag struct {
	Status            string `json:"status"`
	FingerprintSHA256 string `json:"fingerprintSha256,omitempty"`
	FingerprintMD5    string `json:"fingerprintMd5,omitempty"`
	Algorithm         string `json:"algorithm,omitempty"`
	Bits              int    `json:"bits,omitempty"`
}

type NetworkDiag struct {
	LatencyMs int64 `json:"latencyMs"`
}

// AgentIdentity describes one identity exposed by SSH_AUTH_SOCK.
type AgentIdentity struct {
	FingerprintSHA256 string `json:"fingerprintSha256"`
	Type              string `json:"type"`
	Comment           string `json:"comment,omitempty"`
}

// SSHAgentStatus holds local SSH agent availability and loaded identities.
type SSHAgentStatus struct {
	Available  bool            `json:"available"`
	SocketPath string          `json:"socketPath,omitempty"`
	Identities []AgentIdentity `json:"identities"`
}

// CredentialHealthScore summarizes credential posture for a server.
type CredentialHealthScore struct {
	Score               int    `json:"score"`
	Grade               string `json:"grade"`
	PasswordExists      bool   `json:"passwordExists"`
	KeyInstalled        bool   `json:"keyInstalled"`
	FingerprintVerified bool   `json:"fingerprintVerified"`
	KnownHost           bool   `json:"knownHost"`
	RecentSuccess       bool   `json:"recentSuccess"`
	RecentFailure       bool   `json:"recentFailure"`
	PassphraseEnabled   bool   `json:"passphraseEnabled"`
	BastionHealthy      bool   `json:"bastionHealthy"`
	AgentHealthy        bool   `json:"agentHealthy"`
}

// AuthPriorityEntry configures preferred authentication-method ordering.
type AuthPriorityEntry struct {
	Method   string `json:"method"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}
