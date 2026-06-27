package discovery

// SystemInfo holds collected system information.
type SystemInfo struct {
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

// WSMessage is the message format sent over WebSocket.
type WSMessage struct {
	Step      string      `json:"step"`
	Status    string      `json:"status"`
	Value     any         `json:"value,omitempty"`
	Error     string      `json:"error,omitempty"`
	LatencyMs int         `json:"latencyMs,omitempty"`
}

// StepResult holds the result of a single discovery step.
type StepResult struct {
	Name       string
	Value      string
	IntValue   int
	FloatValue float64
	Error      error
	Timeout    bool
}
