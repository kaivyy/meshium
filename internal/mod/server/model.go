package server

import "encoding/json"

// Server is the internal server model (matches DB schema).
type Server struct {
	ID              int      `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	Username        string   `json:"username"`
	Password         string   `json:"-"`
	SSHKey          string   `json:"-"`
	Passphrase      string   `json:"-"`
	Tags            []string `json:"tags"`
	Environment     string   `json:"environment"`
	Region          string   `json:"region"`
	Icon            string   `json:"icon"`
	Color           string   `json:"color"`
	Favorite        bool     `json:"favorite"`
	AuthMethod      string   `json:"authMethod"`
	CredentialStatus string  `json:"credentialStatus"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
	KeyType         string   `json:"keyType,omitempty"`
	BastionID       int      `json:"bastionId,omitempty"`
	LastSuccess     string   `json:"lastSuccess,omitempty"`
	LastFailure     string   `json:"lastFailure,omitempty"`
	FailureCount    int      `json:"failureCount"`
	SuccessCount    int      `json:"successCount"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

// CreateRequest is the DTO for creating a server.
type CreateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	SSHKey      string   `json:"sshKey,omitempty"`
	Passphrase  string   `json:"passphrase,omitempty"`
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
	KeyType     string   `json:"keyType,omitempty"`
	BastionID   int      `json:"bastionId,omitempty"`
}

// UpdateRequest is the DTO for updating a server.
type UpdateRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Host        *string   `json:"host,omitempty"`
	Port        *int      `json:"port,omitempty"`
	Username    *string   `json:"username,omitempty"`
	Password    *string   `json:"password,omitempty"`
	SSHKey      *string   `json:"sshKey,omitempty"`
	Passphrase  *string   `json:"passphrase,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Environment *string   `json:"environment,omitempty"`
	Region      *string   `json:"region,omitempty"`
	Icon        *string   `json:"icon,omitempty"`
	Color       *string   `json:"color,omitempty"`
	KeyType     *string   `json:"keyType,omitempty"`
	BastionID   *int      `json:"bastionId,omitempty"`
}

// ServerResponse is the API response shape for servers.
// It intentionally omits credential fields.
type ServerResponse struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	Username         string   `json:"username"`
	AuthMethod       string   `json:"authMethod"`
	CredentialStatus string   `json:"credentialStatus"`
	Fingerprint      string   `json:"fingerprint,omitempty"`
	KeyType          string   `json:"keyType,omitempty"`
	BastionID        int      `json:"bastionId,omitempty"`
	Tags             []string `json:"tags"`
	Environment      string   `json:"environment"`
	Region           string   `json:"region"`
	Icon             string   `json:"icon"`
	Color            string   `json:"color"`
	Favorite         bool     `json:"favorite"`
	LastSuccess      string   `json:"lastSuccess,omitempty"`
	LastFailure      string   `json:"lastFailure,omitempty"`
	FailureCount     int      `json:"failureCount"`
	SuccessCount     int      `json:"successCount"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

// ServerInfo holds cached connection test results.
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

// KeyInstallResult holds the result of a key installation.
type KeyInstallResult struct {
	Success          bool   `json:"success"`
	Message          string `json:"message"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	KeyType          string `json:"keyType,omitempty"`
	AlreadyInstalled bool   `json:"alreadyInstalled"`
}

// KeyVerifyResult holds the result of a key verification.
type KeyVerifyResult struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Installed   bool   `json:"installed"`
}

// KeyRotationResult holds the result of a key rotation.
type KeyRotationResult struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	OldFingerprint string `json:"oldFingerprint,omitempty"`
	NewFingerprint string `json:"newFingerprint,omitempty"`
	KeyType        string `json:"keyType,omitempty"`
}

// FingerprintResult holds fingerprint information.
type FingerprintResult struct {
	SHA256  string `json:"sha256"`
	MD5     string `json:"md5"`
	KeyType string `json:"keyType,omitempty"`
}

// AuthTestResult holds the result of an authentication test.
type AuthTestResult struct {
	Success   bool   `json:"success"`
	AuthMethod string `json:"authMethod"`
	LatencyMs int64  `json:"latencyMs"`
	Message   string `json:"message,omitempty"`
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

// AuthStatus holds the full authentication status for a server.
type AuthStatus struct {
	AuthMethod       string `json:"authMethod"`
	CredentialStatus string `json:"credentialStatus"`
	HasPassword      bool   `json:"hasPassword"`
	HasSSHKey        bool   `json:"hasSSHKey"`
	HasPassphrase     bool   `json:"hasPassphrase"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	KeyType          string `json:"keyType,omitempty"`
}

// SSHAgentStatus holds the status of the SSH agent.
type SSHAgentStatus struct {
	Available  bool   `json:"available"`
	SocketPath string `json:"socketPath"`
	Identities []AgentIdentity `json:"identities"`
}

type AgentIdentity struct {
	FingerprintSHA256 string `json:"fingerprintSha256"`
	Type             string `json:"type"`
	Comment          string `json:"comment"`
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

// CredentialHealthScore holds the computed health score for a server's credentials.
type CredentialHealthScore struct {
	Score              int    `json:"score"`
	Grade              string `json:"grade"`
	PasswordExists     bool   `json:"passwordExists"`
	KeyInstalled       bool   `json:"keyInstalled"`
	FingerprintVerified bool   `json:"fingerprintVerified"`
	KnownHost          bool   `json:"knownHost"`
	RecentSuccess      bool   `json:"recentSuccess"`
	RecentFailure      bool   `json:"recentFailure"`
	KeyAge             string `json:"keyAge"`
	PassphraseEnabled  bool   `json:"passphraseEnabled"`
	BastionHealthy     bool   `json:"bastionHealthy"`
	AgentHealthy       bool   `json:"agentHealthy"`
}

// ServerKey represents a stored SSH key for a server.
type ServerKey struct {
	ID          int    `json:"id"`
	ServerID    int    `json:"serverId"`
	Label       string `json:"label"`
	KeyType     string `json:"keyType"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
	Notes       string `json:"notes,omitempty"`
	Enabled     bool   `json:"enabled"`
	IsDefault   bool   `json:"isDefault"`
	Priority    int    `json:"priority"`
	LastUsed    string `json:"lastUsed,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// ConnectionProfile represents a connection profile.
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
	ServerID         int      `json:"serverId"`
	RetryCount       int      `json:"retryCount"`
	RetryDelayMs     int      `json:"retryDelayMs"`
	BackoffStrategy  string   `json:"backoffStrategy"`
	JitterMs         int      `json:"jitterMs"`
	ReconnectPolicy  string   `json:"reconnectPolicy"`
	AuthRetryOrder   []string `json:"authRetryOrder"`
}

// AuthPriorityEntry represents a single auth method in the priority list.
type AuthPriorityEntry struct {
	Method   string `json:"method"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// KnownHostEntry represents a known host entry.
type KnownHostEntry struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	HostKey           string `json:"hostKey"`
	FingerprintSHA256 string `json:"fingerprintSha256"`
	FingerprintMD5    string `json:"fingerprintMd5"`
	Algorithm        string `json:"algorithm"`
	Bits              int    `json:"bits"`
	Status            string `json:"status"`
	Verified          bool   `json:"verified"`
	ServerID          int    `json:"serverId,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// HostKeyChange represents a host key change audit entry.
type HostKeyChange struct {
	ID              int    `json:"id"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	OldFingerprint  string `json:"oldFingerprint,omitempty"`
	NewFingerprint  string `json:"newFingerprint,omitempty"`
	RiskLevel       string `json:"riskLevel"`
	ActionTaken     string `json:"actionTaken"`
	ServerID        int    `json:"serverId,omitempty"`
	Timestamp       string `json:"timestamp"`
}

// CredentialAuditEntry represents a credential access audit log entry.
type CredentialAuditEntry struct {
	ID        int    `json:"id"`
	ServerID  int    `json:"serverId"`
	Action    string `json:"action"`
	Detail    string `json:"detail,omitempty"`
	IP        string `json:"ip,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Response converts the internal server model into the public response shape.
func (s Server) Response() ServerResponse {
	authMethod := "password"
	if s.SSHKey != "" {
		authMethod = "key"
	}
	if s.AuthMethod != "" {
		authMethod = s.AuthMethod
	}

	credentialStatus := s.CredentialStatus
	if credentialStatus == "" {
		credentialStatus = "unknown"
	}

	keyType := s.KeyType
	if keyType == "" {
		keyType = "rsa"
	}

	return ServerResponse{
		ID:               s.ID,
		Name:             s.Name,
		Description:      s.Description,
		Host:             s.Host,
		Port:             s.Port,
		Username:         s.Username,
		AuthMethod:       authMethod,
		CredentialStatus: credentialStatus,
		Fingerprint:      s.Fingerprint,
		KeyType:          keyType,
		BastionID:        s.BastionID,
		Tags:             s.Tags,
		Environment:      s.Environment,
		Region:           s.Region,
		Icon:             s.Icon,
		Color:            s.Color,
		Favorite:         s.Favorite,
		LastSuccess:      s.LastSuccess,
		LastFailure:      s.LastFailure,
		FailureCount:     s.FailureCount,
		SuccessCount:     s.SuccessCount,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

// tagsToJSON converts a string slice to JSON for SQLite storage.
func tagsToJSON(tags []string) string {
	if tags == nil {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

// tagsFromJSON parses a JSON string to a string slice.
func tagsFromJSON(s string) []string {
	if s == "" {
		return []string{}
	}
	var tags []string
	json.Unmarshal([]byte(s), &tags)
	return tags
}
