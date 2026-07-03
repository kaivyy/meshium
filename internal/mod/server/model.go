package server

import "encoding/json"

// Server is the internal server model (matches DB schema).
type Server struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	Username         string   `json:"username"`
	AuthMethod       string   `json:"authMethod"`
	CredentialStatus string   `json:"credentialStatus"`
	LastSuccess      string   `json:"lastSuccess"`
	LastFailure      string   `json:"lastFailure"`
	FailureCount     int      `json:"failureCount"`
	SuccessCount     int      `json:"successCount"`
	Fingerprint      string   `json:"fingerprint"`
	KeyType          string   `json:"keyType"`
	Password         string   `json:"-"`
	SSHKey           string   `json:"-"`
	Passphrase       string   `json:"-"`
	BastionID        int      `json:"bastionId"` // 0 = direct, >0 = use server with this ID as bastion/jump host
	Tags             []string `json:"tags"`
	Environment      string   `json:"environment"`
	Region           string   `json:"region"`
	Icon             string   `json:"icon"`
	Color            string   `json:"color"`
	Favorite         bool     `json:"favorite"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

// CreateRequest is the DTO for creating a server.
type CreateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	AuthMethod  string   `json:"authMethod,omitempty"`
	KeyType     string   `json:"keyType,omitempty"`
	Password    string   `json:"password,omitempty"`
	SSHKey      string   `json:"sshKey,omitempty"`
	Passphrase  string   `json:"passphrase,omitempty"`
	BastionID   int      `json:"bastionId,omitempty"`
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
}

// UpdateRequest is the DTO for updating a server.
type UpdateRequest struct {
	Name             *string   `json:"name,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Host             *string   `json:"host,omitempty"`
	Port             *int      `json:"port,omitempty"`
	Username         *string   `json:"username,omitempty"`
	AuthMethod       *string   `json:"authMethod,omitempty"`
	KeyType          *string   `json:"keyType,omitempty"`
	CredentialStatus *string   `json:"credentialStatus,omitempty"`
	Fingerprint      *string   `json:"fingerprint,omitempty"`
	Password         *string   `json:"password,omitempty"`
	SSHKey           *string   `json:"sshKey,omitempty"`
	Passphrase       *string   `json:"passphrase,omitempty"`
	BastionID        *int      `json:"bastionId,omitempty"`
	Tags             *[]string `json:"tags,omitempty"`
	Environment      *string   `json:"environment,omitempty"`
	Region           *string   `json:"region,omitempty"`
	Icon             *string   `json:"icon,omitempty"`
	Color            *string   `json:"color,omitempty"`
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
	LastSuccess      string   `json:"lastSuccess"`
	LastFailure      string   `json:"lastFailure"`
	FailureCount     int      `json:"failureCount"`
	SuccessCount     int      `json:"successCount"`
	Fingerprint      string   `json:"fingerprint"`
	KeyType          string   `json:"keyType"`
	BastionID        int      `json:"bastionId"`
	Tags             []string `json:"tags"`
	Environment      string   `json:"environment"`
	Region           string   `json:"region"`
	Icon             string   `json:"icon"`
	Color            string   `json:"color"`
	Favorite         bool     `json:"favorite"`
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

// Response converts the internal server model into the public response shape.
func (s Server) Response() ServerResponse {
	authMethod := s.AuthMethod
	if authMethod == "" {
		authMethod = "password"
		if s.SSHKey != "" {
			authMethod = "key"
		}
	}

	return ServerResponse{
		ID:               s.ID,
		Name:             s.Name,
		Description:      s.Description,
		Host:             s.Host,
		Port:             s.Port,
		Username:         s.Username,
		AuthMethod:       authMethod,
		CredentialStatus: s.CredentialStatus,
		LastSuccess:      s.LastSuccess,
		LastFailure:      s.LastFailure,
		FailureCount:     s.FailureCount,
		SuccessCount:     s.SuccessCount,
		Fingerprint:      s.Fingerprint,
		KeyType:          s.KeyType,
		BastionID:        s.BastionID,
		Tags:             s.Tags,
		Environment:      s.Environment,
		Region:           s.Region,
		Icon:             s.Icon,
		Color:            s.Color,
		Favorite:         s.Favorite,
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

// AuthStatus represents the authentication status of a server.
type AuthStatus struct {
	ServerID         int    `json:"serverId"`
	AuthMethod       string `json:"authMethod"`
	CredentialStatus string `json:"credentialStatus"`
	HasPassword      bool   `json:"hasPassword"`
	HasSSHKey        bool   `json:"hasSSHKey"`
	HasPassphrase    bool   `json:"hasPassphrase"`
	Fingerprint      string `json:"fingerprint"`
	KeyType          string `json:"keyType"`
	LastSuccess      string `json:"lastSuccess"`
	LastFailure      string `json:"lastFailure"`
	FailureCount     int    `json:"failureCount"`
	SuccessCount     int    `json:"successCount"`
}

// FingerprintResult holds fingerprint information.
type FingerprintResult struct {
	SHA256  string `json:"sha256"`
	MD5     string `json:"md5"`
	KeyType string `json:"keyType"`
}

// KeyInstallResult holds the result of a key installation.
type KeyInstallResult struct {
	Success          bool   `json:"success"`
	Message          string `json:"message"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	KeyType          string `json:"keyType,omitempty"`
	AlreadyInstalled bool   `json:"alreadyInstalled"`
}

// KeyVerifyResult holds the result of key verification.
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

// ConnectionHistoryEntry represents a single connection history record.
type ConnectionHistoryEntry struct {
	ID          int    `json:"id"`
	ServerID    int    `json:"serverId"`
	Success     bool   `json:"success"`
	DurationMs  int    `json:"durationMs"`
	Reason      string `json:"reason"`
	RemoteIP    string `json:"remoteIp"`
	Fingerprint string `json:"fingerprint"`
	AuthMethod  string `json:"authMethod"`
	CreatedAt   string `json:"createdAt"`
}

// ConnectionMetrics holds aggregated connection metrics.
type ConnectionMetrics struct {
	TotalAttempts int     `json:"totalAttempts"`
	SuccessCount  int     `json:"successCount"`
	FailureCount  int     `json:"failureCount"`
	SuccessRate   float64 `json:"successRate"`
	FailureRate   float64 `json:"failureRate"`
	LastSuccess   string  `json:"lastSuccess,omitempty"`
	LastFailure   string  `json:"lastFailure,omitempty"`
}

// SSHAgentStatus holds the status of the SSH agent.
type SSHAgentStatus struct {
	Available    bool     `json:"available"`
	Socket       string   `json:"socket"`
	Identities   int      `json:"identities"`
	Fingerprints []string `json:"fingerprints"`
}
