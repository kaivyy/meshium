package ssh

import (
	"time"

	"golang.org/x/crypto/ssh"
)

// Auth method constants.
const (
	AuthMethodPassword           = "password"
	AuthMethodPublicKey         = "publickey"
	AuthMethodKeyboardInteractive = "keyboard-interactive"
	AuthMethodAgent             = "agent"
)

// Key type constants.
const (
	KeyTypeRSA     = "rsa"
	KeyTypeED25519 = "ed25519"
	KeyTypeECDSA   = "ecdsa"
)

// Credential status constants.
const (
	CredentialStatusValid    = "valid"
	CredentialStatusInvalid  = "invalid"
	CredentialStatusExpired  = "expired"
	CredentialStatusLocked   = "locked"
	CredentialStatusUnknown  = "unknown"
)

// Host key trust status constants.
const (
	HostStatusVerified = "verified"
	HostStatusUnknown  = "unknown"
	HostStatusChanged  = "changed"
)

// TOFU mode constants.
const (
	TOFUModeAlways = "always"
	TOFUModeOnce   = "once"
	TOFUModeReject = "reject"
)

// Backoff strategy constants.
const (
	BackoffExponential = "exponential"
	BackoffLinear       = "linear"
)

// ServerConfig holds the connection parameters for a server.
type ServerConfig struct {
	ID           int
	Host         string
	Port         int
	Username     string
	Password     string
	PrivateKey   []byte // raw PEM bytes
	Passphrase   string
	BastionID    int    // 0 = no bastion
	BastionHost  string // resolved bastion host
	BastionPort  int    // resolved bastion port
	BastionUser  string // resolved bastion user
	BastionKey   []byte // resolved bastion private key
	BastionPass  string // resolved bastion password
	BastionPassphrase string
	BastionHostKeyCallback *ssh.HostKeyCallback // optional callback for bastion host key verification
	AuthPriority []string // ordered list of auth methods to try
	UseAgent     bool
	AgentSocket  string // SSH_AUTH_SOCK path
	AgentForwarding bool
	Profile      *ConnectionProfile
	RetryConfig   *RetryConfig
}

// ConnectResult holds the result of a connection attempt.
type ConnectResult struct {
	Success     bool
	Latency     time.Duration
	Error       string
	HostKey     string
	NeedsVerify bool // true if host key not in known_hosts
	AuthMethod  string
	Cipher      string
	KEX         string
	Compression string
	RemoteBanner string
}

// HostKeyResult holds the result of a host key check.
type HostKeyResult struct {
	Known       bool
	Match       bool
	Key         string
	FingerprintSHA256 string
	FingerprintMD5   string
	Algorithm   string
	Bits        int
}

// WizardStep represents a single step in the connection wizard.
type WizardStep struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // pending, running, success, skipped, warning, failed
	Duration int64  `json:"durationMs"`
	Message  string `json:"message"`
	Log      []string `json:"log,omitempty"`
}

// WizardResult holds the complete result of a connection wizard run.
type WizardResult struct {
	Steps      []WizardStep `json:"steps"`
	Success    bool         `json:"success"`
	TotalDuration int64    `json:"totalDurationMs"`
	Error      string       `json:"error,omitempty"`
	ServerInfo *ServerInfo  `json:"serverInfo,omitempty"`
}

// ServerInfo holds collected system information.
type ServerInfo struct {
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Architecture string `json:"architecture"`
	CPUModel     string `json:"cpuModel"`
	CPUCores     int    `json:"cpuCores"`
	RAMTotalMB   int    `json:"ramTotalMb"`
	DiskTotalGB  float64 `json:"diskTotalGb"`
	PublicIP     string `json:"publicIp"`
	PrivateIP    string `json:"privateIp"`
}

// ConnectionProfile holds preset configuration for different use cases.
type ConnectionProfile struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	TimeoutSeconds    int    `json:"timeoutSeconds"`
	RetryCount        int    `json:"retryCount"`
	RetryDelayMs      int    `json:"retryDelayMs"`
	BackoffStrategy   string `json:"backoffStrategy"`
	KeepaliveSeconds  int    `json:"keepaliveSeconds"`
	ReconnectEnabled  bool   `json:"reconnectEnabled"`
	BufferSizeKB      int    `json:"bufferSizeKb"`
	Compression       bool   `json:"compression"`
	Parallelism       int    `json:"parallelism"`
	IsBuiltin         bool   `json:"isBuiltin"`
}

// RetryConfig holds per-server retry strategy configuration.
type RetryConfig struct {
	RetryCount      int    `json:"retryCount"`
	RetryDelayMs    int    `json:"retryDelayMs"`
	BackoffStrategy string `json:"backoffStrategy"`
	JitterMs        int    `json:"jitterMs"`
	ReconnectPolicy string `json:"reconnectPolicy"`
	AuthRetryOrder  []string `json:"authRetryOrder"`
}

// CredentialHealthScore holds the computed health score for a server's credentials.
type CredentialHealthScore struct {
	Score            int    `json:"score"`
	Grade            string `json:"grade"`
	PasswordExists   bool   `json:"passwordExists"`
	KeyInstalled     bool   `json:"keyInstalled"`
	FingerprintVerified bool `json:"fingerprintVerified"`
	KnownHost        bool   `json:"knownHost"`
	RecentSuccess    bool   `json:"recentSuccess"`
	RecentFailure    bool   `json:"recentFailure"`
	KeyAge           string `json:"keyAge"`
	PassphraseEnabled bool  `json:"passphraseEnabled"`
	BastionHealthy   bool   `json:"bastionHealthy"`
	AgentHealthy     bool   `json:"agentHealthy"`
}

// AuthPriorityEntry represents a single auth method in the priority list.
type AuthPriorityEntry struct {
	Method   string `json:"method"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// KeyboardInteractiveChallenge represents a single challenge in a keyboard-interactive auth flow.
type KeyboardInteractiveChallenge struct {
	Prompt    string `json:"prompt"`
	Echo      bool   `json:"echo"`
	Instruction string `json:"instruction,omitempty"`
}

// KeyboardInteractiveResponse holds the user's responses to challenges.
type KeyboardInteractiveResponse struct {
	Answers []string `json:"answers"`
}

// SSHAgentStatus holds the status of the SSH agent.
type SSHAgentStatus struct {
	Available    bool              `json:"available"`
	SocketPath   string            `json:"socketPath"`
	Identities   []AgentIdentity   `json:"identities"`
}

// AgentIdentity represents a single identity loaded in the SSH agent.
type AgentIdentity struct {
	FingerprintSHA256 string `json:"fingerprintSha256"`
	Type             string `json:"type"`
	Comment          string `json:"comment"`
}

// ConnectionHistoryEntry represents a single connection history record.
type ConnectionHistoryEntry struct {
	ID              int     `json:"id"`
	ServerID        int     `json:"serverId"`
	Hostname        string  `json:"hostname"`
	IP              string  `json:"ip"`
	Username        string  `json:"username"`
	AuthMethod      string  `json:"authMethod"`
	KeyFingerprint  string  `json:"keyFingerprint,omitempty"`
	AgentUsed       bool    `json:"agentUsed"`
	BastionID       int     `json:"bastionId,omitempty"`
	Success         bool    `json:"success"`
	DurationMs      int     `json:"durationMs"`
	LatencyMs       int     `json:"latencyMs,omitempty"`
	ExitStatus      int     `json:"exitStatus,omitempty"`
	FailureReason   string  `json:"failureReason,omitempty"`
	Cipher          string  `json:"cipher,omitempty"`
	KEX             string  `json:"kex,omitempty"`
	Compression     string  `json:"compression,omitempty"`
	RemoteBanner    string  `json:"remoteBanner,omitempty"`
	Timestamp       string  `json:"timestamp"`
}

// ConnectionMetrics holds aggregated connection metrics for a server.
type ConnectionMetrics struct {
	TotalAttempts int     `json:"totalAttempts"`
	SuccessCount  int     `json:"successCount"`
	FailureCount  int     `json:"failureCount"`
	SuccessRate   float64 `json:"successRate"`
	FailureRate   float64 `json:"failureRate"`
	LastSuccess   string  `json:"lastSuccess,omitempty"`
	LastFailure   string  `json:"lastFailure,omitempty"`
}

// AuthDashboard holds the authentication dashboard data.
type AuthDashboard struct {
	PasswordServers      int     `json:"passwordServers"`
	KeyServers           int     `json:"keyServers"`
	AgentServers         int     `json:"agentServers"`
	BastionServers       int     `json:"bastionServers"`
	FingerprintChanged   int     `json:"fingerprintChanged"`
	UnknownHosts         int     `json:"unknownHosts"`
	CredentialWarnings   int     `json:"credentialWarnings"`
	ExpiredKeys          int     `json:"expiredKeys"`
	AuthFailures         int     `json:"authFailures"`
	AuthSuccessRate      float64 `json:"authSuccessRate"`
	ConnectionSuccessRate float64 `json:"connectionSuccessRate"`
	AverageLatency       float64 `json:"averageLatency"`
	MedianLatency        float64 `json:"medianLatency"`
	P95Latency           float64 `json:"p95Latency"`
	P99Latency           float64 `json:"p99Latency"`
}

// DiagnosticsResult holds the result of a diagnostics run.
type DiagnosticsResult struct {
	DNS       *DNSDiag      `json:"dns,omitempty"`
	TCP       *TCPDiag      `json:"tcp,omitempty"`
	SSH       *SSHDiag      `json:"ssh,omitempty"`
	Auth      *AuthDiag     `json:"auth,omitempty"`
	HostKey   *HostKeyDiag  `json:"hostKey,omitempty"`
	Network   *NetworkDiag  `json:"network,omitempty"`
}

type DNSDiag struct {
	Resolved   bool   `json:"resolved"`
	Hostname   string `json:"hostname"`
	Addresses  []string `json:"addresses"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type TCPDiag struct {
	Reachable  bool   `json:"reachable"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type SSHDiag struct {
	Reachable    bool   `json:"reachable"`
	Banner       string `json:"banner,omitempty"`
	Ciphers      []string `json:"ciphers,omitempty"`
	KEXAlgorithms []string `json:"kexAlgorithms,omitempty"`
	Error        string `json:"error,omitempty"`
	DurationMs   int64  `json:"durationMs"`
}

type AuthDiag struct {
	Methods   []string `json:"methods"`
	Success   bool     `json:"success"`
	Method    string   `json:"method,omitempty"`
	Error     string   `json:"error,omitempty"`
	DurationMs int64   `json:"durationMs"`
}

type HostKeyDiag struct {
	Known           bool   `json:"known"`
	Match           bool   `json:"match"`
	FingerprintSHA256 string `json:"fingerprintSha256,omitempty"`
	FingerprintMD5   string `json:"fingerprintMd5,omitempty"`
	Algorithm       string `json:"algorithm,omitempty"`
	Bits            int    `json:"bits,omitempty"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}

type NetworkDiag struct {
	LatencyMs  int64    `json:"latencyMs"`
	PacketLoss float64  `json:"packetLoss"`
	MTU        int      `json:"mtu"`
	Hops       []string `json:"hops,omitempty"`
	Error      string   `json:"error,omitempty"`
}
