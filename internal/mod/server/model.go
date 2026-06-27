package server

import "encoding/json"

// Server is the internal server model (matches DB schema).
type Server struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"-"`
	SSHKey      string   `json:"-"`
	Passphrase  string   `json:"-"`
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
	Favorite    bool     `json:"favorite"`
	BastionID   int      `json:"bastionId"`     // 0 = direct, >0 = use server with this ID as bastion/jump host
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
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
	BastionID   int      `json:"bastionId"`
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
	BastionID   *int      `json:"bastionId,omitempty"`
}

// ServerResponse is the API response shape for servers.
// It intentionally omits credential fields.
type ServerResponse struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	AuthMethod  string   `json:"authMethod"`
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
	Favorite    bool     `json:"favorite"`
	BastionID   int      `json:"bastionId"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
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
	authMethod := "password"
	if s.SSHKey != "" {
		authMethod = "key"
	}

	return ServerResponse{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Host:        s.Host,
		Port:        s.Port,
		Username:    s.Username,
		AuthMethod:  authMethod,
		Tags:        s.Tags,
		Environment: s.Environment,
		Region:      s.Region,
		Icon:        s.Icon,
		Color:       s.Color,
		Favorite:    s.Favorite,
		BastionID:   s.BastionID,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
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
