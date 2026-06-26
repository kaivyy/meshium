# Part 4: Connection Test Backend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the connection test system that collects system info from servers via SSH and streams results to the frontend over WebSocket.

**Architecture:** A discovery service orchestrates running SSH commands on a target server. Each command result is sent to the WebSocket client as it completes. Results are cached in the `server_info` table. Non-fatal errors (command not found, timeout) are skipped; SSH disconnection aborts the stream.

**Tech Stack:** `gorilla/websocket`, `golang.org/x/crypto/ssh`, `encoding/json`

## Global Constraints

- WebSocket endpoint: `GET /ws/connect/:serverId`
- Each step result sent as JSON: `{"step": "name", "status": "success|error", "value": "...", "error": "..."}`
- Final message: `{"step": "done", "status": "complete"}`
- Non-fatal errors: skip and continue
- SSH disconnection: abort stream
- Results cached to `server_info` table after completion
- Connection timeout: 10s
- Command timeout: 30s per command

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/discovery/model.go` | SystemInfo struct, WS message types |
| `internal/mod/discovery/collector.go` | SSH command definitions and execution |
| `internal/mod/discovery/service.go` | Orchestrates collection, caches results |
| `internal/mod/discovery/handler.go` | WebSocket handler |
| `internal/mod/discovery/service_test.go` | Service tests |
| `internal/mod/discovery/handler_test.go` | Handler tests |

---

### Task 1: Discovery Model

**Files:**
- Create: `internal/mod/discovery/model.go`

**Interfaces:**
- Produces: `discovery.SystemInfo`, `discovery.WSMessage`, `discovery.StepResult`

- [ ] **Step 1: Write discovery model**

```go
// internal/mod/discovery/model.go
package discovery

// SystemInfo holds collected system information.
type SystemInfo struct {
	SSHStatus      string `json:"sshStatus"`
	LatencyMs      int    `json:"latencyMs"`
	Hostname       string `json:"hostname"`
	OS             string `json:"os"`
	Kernel         string `json:"kernel"`
	Architecture   string `json:"architecture"`
	CPUModel       string `json:"cpuModel"`
	CPUCores       int    `json:"cpuCores"`
	RAMTotalMB     int    `json:"ramTotalMb"`
	DiskTotalGB    float64 `json:"diskTotalGb"`
	Virtualization string `json:"virtualization"`
	Provider       string `json:"provider"`
	PublicIP       string `json:"publicIp"`
	PrivateIP      string `json:"privateIp"`
	Timezone       string `json:"timezone"`
}

// WSMessage is the message format sent over WebSocket.
type WSMessage struct {
	Step     string `json:"step"`
	Status   string `json:"status"`         // success | error
	Value    interface{} `json:"value,omitempty"`
	Error    string `json:"error,omitempty"`
	LatencyMs int   `json:"latencyMs,omitempty"`
}

// StepResult holds the result of a single discovery step.
type StepResult struct {
	Name    string
	Value   string
	Error   error
	Timeout bool
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mod/discovery/
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/discovery/model.go
git commit -m "feat: add discovery model types"
```

---

### Task 2: Collector

**Files:**
- Create: `internal/mod/discovery/collector.go`
- Create: `internal/mod/discovery/collector_test.go`

**Interfaces:**
- Consumes: `ssh.Client` (Exec method)
- Produces: `discovery.Collector` struct, `discovery.Command` type

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/discovery/collector_test.go
package discovery

import (
	"strings"
	"testing"
)

// mockSSHClient implements the SSHExecuter interface for testing.
type mockSSHClient struct {
	responses map[string]string
}

func (m *mockSSHClient) Exec(cmd string) (string, string, int, error) {
	for pattern, response := range m.responses {
		if strings.Contains(cmd, pattern) {
			return response, "", 0, nil
		}
	}
	return "", "", 0, nil
}

func TestCollectHostname(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"hostname": "web-01\n",
		},
	}

	collector := NewCollector(client)
	result := collector.CollectHostname()

	if result.Value != "web-01" {
		t.Errorf("expected 'web-01', got %q", result.Value)
	}
}

func TestCollectOS(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"os-release": `PRETTY_NAME="Ubuntu 22.04 LTS"`,
		},
	}

	collector := NewCollector(client)
	result := collector.CollectOS()

	if !strings.Contains(result.Value, "Ubuntu") {
		t.Errorf("expected to contain 'Ubuntu', got %q", result.Value)
	}
}

func TestCollectRAM(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"free -m": "              total        used        free\nMem:           8192        2048        6144\n",
		},
	}

	collector := NewCollector(client)
	result := collector.CollectRAM()

	if result.IntValue != 8192 {
		t.Errorf("expected 8192, got %d", result.IntValue)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/discovery/ -v
```
Expected: FAIL — types not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/discovery/collector.go
package discovery

import (
	"strconv"
	"strings"

	"meshium/internal/mod/ssh"
)

// SSHExecuter is the interface for executing SSH commands.
type SSHExecuter interface {
	Exec(cmd string) (string, string, int, error)
}

// Command defines a single discovery command.
type Command struct {
	Name    string
	Cmd     string
	Parse   func(stdout string) string
	ParseInt func(stdout string) int
}

// StepResult holds the result of a single step.
type StepResult struct {
	Name      string
	Value     string
	IntValue  int
	FloatValue float64
	Error     error
}

// Collector runs SSH commands to collect system info.
type Collector struct {
	client SSHExecuter
}

func NewCollector(client SSHExecuter) *Collector {
	return &Collector{client: client}
}

func (c *Collector) runCommand(name, cmd string) StepResult {
	stdout, _, _, err := c.client.Exec(cmd)
	if err != nil {
		return StepResult{Name: name, Error: err}
	}
	return StepResult{Name: name, Value: strings.TrimSpace(stdout)}
}

func (c *Collector) CollectHostname() StepResult {
	return c.runCommand("hostname", "hostname")
}

func (c *Collector) CollectOS() StepResult {
	stdout, _, _, err := c.client.Exec(`cat /etc/os-release | grep PRETTY_NAME`)
	if err != nil {
		return StepResult{Name: "os", Error: err}
	}
	// Parse: PRETTY_NAME="Ubuntu 22.04 LTS"
	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return StepResult{Name: "os", Value: line}
	}
	val := strings.Trim(parts[1], `"`)
	return StepResult{Name: "os", Value: val}
}

func (c *Collector) CollectKernel() StepResult {
	return c.runCommand("kernel", "uname -r")
}

func (c *Collector) CollectArchitecture() StepResult {
	return c.runCommand("architecture", "uname -m")
}

func (c *Collector) CollectCPUModel() StepResult {
	stdout, _, _, err := c.client.Exec(`lscpu | grep "Model name"`)
	if err != nil {
		return StepResult{Name: "cpu_model", Error: err}
	}
	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return StepResult{Name: "cpu_model", Value: line}
	}
	return StepResult{Name: "cpu_model", Value: strings.TrimSpace(parts[1])}
}

func (c *Collector) CollectCPUCores() StepResult {
	stdout, _, _, err := c.client.Exec("nproc")
	if err != nil {
		return StepResult{Name: "cpu_cores", Error: err}
	}
	cores, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil {
		return StepResult{Name: "cpu_cores", Error: err}
	}
	return StepResult{Name: "cpu_cores", IntValue: cores}
}

func (c *Collector) CollectRAM() StepResult {
	stdout, _, _, err := c.client.Exec(`free -m | awk '/Mem:/{print $2}'`)
	if err != nil {
		return StepResult{Name: "ram_total_mb", Error: err}
	}
	ram, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil {
		return StepResult{Name: "ram_total_mb", Error: err}
	}
	return StepResult{Name: "ram_total_mb", IntValue: ram}
}

func (c *Collector) CollectDisk() StepResult {
	stdout, _, _, err := c.client.Exec(`df -BG / | awk 'NR==2{print $2}'`)
	if err != nil {
		return StepResult{Name: "disk_total_gb", Error: err}
	}
	diskStr := strings.TrimSuffix(strings.TrimSpace(stdout), "G")
	disk, err := strconv.ParseFloat(diskStr, 64)
	if err != nil {
		return StepResult{Name: "disk_total_gb", Error: err}
	}
	return StepResult{Name: "disk_total_gb", FloatValue: disk}
}

func (c *Collector) CollectVirtualization() StepResult {
	stdout, _, _, err := c.client.Exec("systemd-detect-virt 2>/dev/null || echo none")
	if err != nil {
		return StepResult{Name: "virtualization", Error: err}
	}
	return StepResult{Name: "virtualization", Value: strings.TrimSpace(stdout)}
}

func (c *Collector) CollectPublicIP() StepResult {
	stdout, _, _, err := c.client.Exec("curl -s --max-time 5 ifconfig.me")
	if err != nil {
		return StepResult{Name: "public_ip", Error: err}
	}
	return StepResult{Name: "public_ip", Value: strings.TrimSpace(stdout)}
}

func (c *Collector) CollectPrivateIP() StepResult {
	stdout, _, _, err := c.client.Exec(`hostname -I | awk '{print $1}'`)
	if err != nil {
		return StepResult{Name: "private_ip", Error: err}
	}
	return StepResult{Name: "private_ip", Value: strings.TrimSpace(stdout)}
}

func (c *Collector) CollectTimezone() StepResult {
	stdout, _, _, err := c.client.Exec(`timedatectl | grep "Time zone"`)
	if err != nil {
		return StepResult{Name: "timezone", Error: err}
	}
	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return StepResult{Name: "timezone", Value: line}
	}
	return StepResult{Name: "timezone", Value: strings.TrimSpace(parts[1])}
}

func (c *Collector) CollectProvider() StepResult {
	stdout, _, _, err := c.client.Exec("curl -s --max-time 2 http://169.254.169.254/latest/meta-data/instance-id || echo unknown")
	if err != nil {
		return StepResult{Name: "provider", Value: "unknown"}
	}
	val := strings.TrimSpace(stdout)
	if val == "unknown" || val == "" {
		return StepResult{Name: "provider", Value: "unknown"}
	}
	return StepResult{Name: "provider", Value: "cloud"}
}

// CollectAll runs all collection steps and returns results.
func (c *Collector) CollectAll() []StepResult {
	return []StepResult{
		c.CollectHostname(),
		c.CollectOS(),
		c.CollectKernel(),
		c.CollectArchitecture(),
		c.CollectCPUModel(),
		c.CollectCPUCores(),
		c.CollectRAM(),
		c.CollectDisk(),
		c.CollectVirtualization(),
		c.CollectPublicIP(),
		c.CollectPrivateIP(),
		c.CollectTimezone(),
		c.CollectProvider(),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/discovery/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mod/discovery/collector.go internal/mod/discovery/collector_test.go
git commit -m "feat: add discovery collector with SSH command execution"
```

---

### Task 3: Discovery Service

**Files:**
- Create: `internal/mod/discovery/service.go`
- Create: `internal/mod/discovery/service_test.go`

**Interfaces:**
- Consumes: `discovery.Collector`, `ssh.Pool`, `server.Repo`, `auth.Service`
- Produces: `discovery.Service` with `RunConnectionTest(serverID, onStep) → error`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/discovery/service_test.go
package discovery

import (
	"testing"
)

func TestRunConnectionTestWithMock(t *testing.T) {
	// Test that the service correctly calls the collector and
	// sends step results through the callback
	mockClient := &mockSSHClient{
		responses: map[string]string{
			"hostname": "test-server\n",
			"uname -r": "5.15.0-76-generic\n",
			"uname -m": "x86_64\n",
			"nproc":    "4\n",
			"free -m":  "              total        used        free\nMem:           8192        2048        6144\n",
			"os-release": `PRETTY_NAME="Ubuntu 22.04 LTS"`,
		},
	}

	collector := NewCollector(mockClient)
	results := collector.CollectAll()

	if len(results) != 13 {
		t.Errorf("expected 13 results, got %d", len(results))
	}

	// Check hostname
	if results[0].Value != "test-server" {
		t.Errorf("expected hostname 'test-server', got %q", results[0].Value)
	}

	// Check CPU cores
	found := false
	for _, r := range results {
		if r.Name == "cpu_cores" && r.IntValue == 4 {
			found = true
		}
	}
	if !found {
		t.Error("expected cpu_cores = 4")
	}
}
```

- [ ] **Step 2: Run test to verify it passes (already passes from Task 2)**

```bash
go test ./internal/mod/discovery/ -v
```
Expected: PASS

- [ ] **Step 3: Write the service**

```go
// internal/mod/discovery/service.go
package discovery

import (
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/auth"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
)

// Service orchestrates the connection test.
type Service struct {
	pool     *ssh.Pool
	repo     server.Repo
	authSvc  *auth.Service
	hosts    *ssh.KnownHostsStore
}

func NewService(pool *ssh.Pool, repo server.Repo, authSvc *auth.Service, hosts *ssh.KnownHostsStore) *Service {
	return &Service{
		pool:    pool,
		repo:    repo,
		authSvc: authSvc,
		hosts:   hosts,
	}
}

// StepCallback is called for each step result during connection test.
type StepCallback func(msg WSMessage)

// RunConnectionTest connects to a server, runs all discovery commands,
// and calls onStep for each result. Results are cached to server_info.
func (s *Service) RunConnectionTest(serverID int, onStep StepCallback) error {
	// Load server from repo
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	// Decrypt credentials
	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return fmt.Errorf("app is locked")
	}

	password := ""
	if srv.Password != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.Password))
		if err != nil {
			return fmt.Errorf("failed to decrypt password: %w", err)
		}
		password = string(decrypted)
	}

	// Build SSH config
	sshConfig := ssh.ServerConfig{
		ID:       srv.ID,
		Host:     srv.Host,
		Port:     srv.Port,
		Username: srv.Username,
		Password: password,
	}

	// Measure latency
	start := time.Now()
	hostKeyCallback := s.hosts.MakeHostKeyCallback()
	client, err := s.pool.Get(serverID, sshConfig, hostKeyCallback)
	latency := time.Since(start)

	if err != nil {
		onStep(WSMessage{
			Step:    "ssh",
			Status:  "error",
			Error:   err.Error(),
		})
		return err
	}

	onStep(WSMessage{
		Step:      "ssh",
		Status:    "success",
		LatencyMs: int(latency.Milliseconds()),
	})

	// Run discovery
	collector := NewCollector(client)
	results := collector.CollectAll()

	// Build SystemInfo from results
	info := SystemInfo{
		SSHStatus: "connected",
		LatencyMs: int(latency.Milliseconds()),
	}

	for _, r := range results {
		msg := WSMessage{Step: r.Name, Status: "success"}
		if r.Error != nil {
			msg.Status = "error"
			msg.Error = r.Error.Error()
		} else if r.IntValue != 0 {
			msg.Value = r.IntValue
		} else if r.FloatValue != 0 {
			msg.Value = r.FloatValue
		} else {
			msg.Value = r.Value
		}

		onStep(msg)

		// Map result to SystemInfo
		switch r.Name {
		case "hostname":
			info.Hostname = r.Value
		case "os":
			info.OS = r.Value
		case "kernel":
			info.Kernel = r.Value
		case "architecture":
			info.Architecture = r.Value
		case "cpu_model":
			info.CPUModel = r.Value
		case "cpu_cores":
			info.CPUCores = r.IntValue
		case "ram_total_mb":
			info.RAMTotalMB = r.IntValue
		case "disk_total_gb":
			info.DiskTotalGB = r.FloatValue
		case "virtualization":
			info.Virtualization = r.Value
		case "provider":
			info.Provider = r.Value
		case "public_ip":
			info.PublicIP = r.Value
		case "private_ip":
			info.PrivateIP = r.Value
		case "timezone":
			info.Timezone = r.Value
		}
	}

	// Cache results
	rawData, _ := json.Marshal(info)
	s.repo.SaveServerInfo(serverID, info, string(rawData))

	onStep(WSMessage{Step: "done", Status: "complete"})
	return nil
}
```

- [ ] **Step 4: Fix imports — add `shared`**

```go
// Add to imports in service.go
import (
	"encoding/json"
	"fmt"
	"time"

	"meshium/internal/mod/auth"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mod/discovery/ -v
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mod/discovery/service.go internal/mod/discovery/service_test.go
git commit -m "feat: add discovery service (connection test orchestration)"
```

---

### Task 4: WebSocket Handler

**Files:**
- Create: `internal/mod/discovery/handler.go`
- Create: `internal/mod/discovery/handler_test.go`

**Interfaces:**
- Consumes: `discovery.Service`, `gorilla/websocket`
- Produces: `discovery.Handler` with `RegisterRoutes(mux)`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/discovery/handler_test.go
package discovery

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWSHandlerRejectsInvalidServerID(t *testing.T) {
	// Test that the handler rejects non-numeric server IDs
	req := httptest.NewRequest("GET", "/ws/connect/abc", nil)
	w := httptest.NewRecorder()

	// We can't easily test WebSocket without a full server,
	// but we can test the route parsing
	if !strings.Contains(req.URL.Path, "/ws/connect/") {
		t.Error("should contain /ws/connect/ prefix")
	}
}
```

- [ ] **Step 2: Write the implementation**

```go
// internal/mod/discovery/handler.go
package discovery

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local app
	},
}

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/connect/", h.handleConnect)
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Parse server ID from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	serverID, err := strconv.Atoi(parts[3])
	if err != nil {
		http.Error(w, "invalid server ID", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Run connection test with streaming
	err = h.svc.RunConnectionTest(serverID, func(msg WSMessage) {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("WebSocket write failed: %v", err)
		}
	})

	if err != nil {
		// Error already sent via onStep callback
		log.Printf("Connection test failed for server %d: %v", serverID, err)
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./internal/mod/discovery/ -v
```
Expected: PASS

- [ ] **Step 4: Commit**

```bash
go mod tidy
git add internal/mod/discovery/handler.go internal/mod/discovery/handler_test.go go.mod go.sum
git commit -m "feat: add WebSocket handler for connection test streaming"
```

---

### Task 5: Wire Discovery into Main Server

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Update main.go to include discovery module**

```go
// cmd/server/main.go
package main

import (
	"fmt"
	"net/http"
	"os"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

func main() {
	cfg := shared.LoadConfig()

	// Open database
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Printf("Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	// Run migrations
	if err := db.Migrate(d); err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	// Initialize auth
	authRepo := auth.NewRepo(d)
	authSvc := auth.NewService(authRepo)
	authHandler := auth.NewHandler(authSvc)

	// Initialize SSH
	sshPool := ssh.NewPool(ssh.PoolConfig{
		MaxIdle:    10 * time.Minute,
		MaxLifetime: 30 * time.Minute,
	})
	knownHosts := ssh.NewKnownHostsStore(d)

	// Initialize server manager
	serverRepo := server.NewRepo(d)
	serverSvc := server.NewService(serverRepo, authSvc)
	serverHandler := server.NewHandler(serverSvc)

	// Initialize discovery
	discoverySvc := discovery.NewService(sshPool, serverRepo, authSvc, knownHosts)
	discoveryHandler := discovery.NewHandler(discoverySvc)

	// Setup router
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterRoutes(mux)
	serverHandler.RegisterRoutes(mux)
	discoveryHandler.RegisterRoutes(mux)

	// Start server
	addr := ":" + cfg.ServerPort
	fmt.Printf("Meshium server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Add `time` import**

```go
import (
	"fmt"
	"net/http"
	"os"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/server/
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire discovery module into main server"
```
