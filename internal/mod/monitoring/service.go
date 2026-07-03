package monitoring

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// Service collects real-time monitoring data from remote servers over SSH.
type Service struct {
	serverRepo server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	knownHosts transport.HostKeyStore
}

// NewService creates a monitoring service.
func NewService(serverRepo server.Repo, pool *modssh.Pool, authSvc transport.AESKeyProvider, knownHosts transport.HostKeyStore) *Service {
	return &Service{
		serverRepo: serverRepo,
		pool:       pool,
		authSvc:    authSvc,
		knownHosts: knownHosts,
	}
}

// ServerMetrics is the high-level monitoring payload streamed to the UI.
type ServerMetrics struct {
	Timestamp    int64            `json:"timestamp"`
	ServerID     int              `json:"serverId,omitempty"`
	Status       string           `json:"status"`              // "ok", "degraded", "unknown"
	Source       string           `json:"source,omitempty"`    // source server identifier
	Target       string           `json:"target,omitempty"`    // target server identifier
	CPU          CPUMetrics       `json:"cpu"`
	Memory       MemoryMetrics    `json:"memory"`
	Disk         []DiskMetrics    `json:"disk"`
	Network      []NetworkMetrics `json:"network"`
	Load         LoadMetrics      `json:"load"`
	Uptime       int64            `json:"uptime"`
	ProcessCount int              `json:"processCount"`
	Temperature  float64          `json:"temperature,omitempty"`
	// Extended metrics (Phase 2)
	Containers   []ContainerMetrics `json:"containers,omitempty"`
	Services     []ServiceMetrics   `json:"services,omitempty"`
	Transfer     TransferMetrics    `json:"transfer,omitempty"`
	Database     []DatabaseMetrics  `json:"database,omitempty"`
	Redis        *RedisMetrics      `json:"redis,omitempty"`
	Queue        []QueueMetrics     `json:"queue,omitempty"`
}

type CPUMetrics struct {
	Usage float64 `json:"usage"`
	Cores int     `json:"cores"`
	Model string  `json:"model"`
}

type MemoryMetrics struct {
	Total         int64   `json:"total"`
	Used          int64   `json:"used"`
	Free          int64   `json:"free"`
	Available     int64   `json:"available"`
	Cached        int64   `json:"cached"`
	SwapTotal     int64   `json:"swapTotal"`
	SwapUsed      int64   `json:"swapUsed"`
	UsagePercent  float64 `json:"usagePercent"`
}

type DiskMetrics struct {
	Filesystem   string  `json:"filesystem"`
	Mount        string  `json:"mount"`
	Total        int64   `json:"total"`
	Used         int64   `json:"used"`
	Available    int64   `json:"available"`
	UsagePercent float64 `json:"usagePercent"`
}

type NetworkMetrics struct {
	Interface string `json:"interface"`
	RxBytes   int64  `json:"rxBytes"`
	TxBytes   int64  `json:"txBytes"`
	RxPackets int64  `json:"rxPackets"`
	TxPackets int64  `json:"txPackets"`
}

type LoadMetrics struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// ContainerMetrics represents health and resource usage of a Docker container.
type ContainerMetrics struct {
	Name         string  `json:"name"`
	Image        string  `json:"image"`
	Status       string  `json:"status"`        // "running", "exited", "unknown"
	Healthy      bool    `json:"healthy"`
	RestartCount int     `json:"restartCount"`
	CPUPercent   float64 `json:"cpuPercent"`
	MemoryUsage  int64   `json:"memoryUsage"`
	MemoryLimit  int64   `json:"memoryLimit"`
	HealthScore  float64 `json:"healthScore"`   // 0-100, 100 = fully healthy
}

// ServiceMetrics represents the status of a system service.
type ServiceMetrics struct {
	Name      string `json:"name"`
	Status    string `json:"status"`    // "active", "inactive", "failed", "unknown"
	SubState  string `json:"subState"`  // "running", "dead", "exited"
	Uptime    int64  `json:"uptime"`    // seconds
	PID       int    `json:"pid,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// TransferMetrics represents current data transfer progress.
type TransferMetrics struct {
	BytesTransferred int64   `json:"bytesTransferred"`
	BytesTotal       int64   `json:"bytesTotal"`
	SpeedBytesSec    int64   `json:"speedBytesSec"`
	ETA              string  `json:"eta"`         // estimated time of arrival
	Progress         float64 `json:"progress"`    // 0-100
}

// DatabaseMetrics represents the status of a database instance.
type DatabaseMetrics struct {
	Type          string  `json:"type"`          // "mysql", "postgresql", "mongodb"
	Name          string  `json:"name"`
	Status        string  `json:"status"`        // "ok", "degraded", "down", "unknown"
	Connections   int     `json:"connections"`
	ReplicationLag int64  `json:"replicationLag"` // milliseconds
	SizeBytes     int64   `json:"sizeBytes"`
	HealthScore   float64 `json:"healthScore"`   // 0-100
}

// RedisMetrics represents the status of a Redis instance.
type RedisMetrics struct {
	Status       string `json:"status"`         // "ok", "degraded", "down", "unknown"`
	Version      string `json:"version"`
	MemoryUsed   int64  `json:"memoryUsed"`
	MemoryMax    int64  `json:"memoryMax"`
	Connected    int    `json:"connected"`      // connected clients
	Keys         int64  `json:"keys"`
	Uptime       int64  `json:"uptime"`         // seconds
	Replication  string `json:"replication"`    // "master", "slave", "none"
	HealthScore  float64 `json:"healthScore"`
}

// QueueMetrics represents the status of a message queue.
type QueueMetrics struct {
	Type       string `json:"type"`        // "bullmq", "rabbitmq", "kafka"
	Name       string `json:"name"`
	Status     string `json:"status"`      // "ok", "paused", "down", "unknown"
	ActiveJobs int    `json:"activeJobs"`
	QueueLen   int64  `json:"queueLength"`
	Workers    int    `json:"workers"`
	Drained    bool   `json:"drained"`
}

// NetworkInterface and DiskUsage are convenience aliases for the dedicated
// monitoring endpoints. They intentionally match the corresponding metric
// payloads so the API stays simple for the frontend.
type NetworkInterface = NetworkMetrics
type DiskUsage = DiskMetrics

type ProcessInfo struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	Command string  `json:"command"`
}

func (s *Service) GetMetrics(ctx context.Context, serverID int) (*ServerMetrics, error) {
	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	metrics := &ServerMetrics{Timestamp: time.Now().Unix()}

	if cpu, err := s.collectCPUMetrics(ctx, client); err == nil {
		metrics.CPU = cpu
	}
	if memory, err := s.collectMemoryMetrics(ctx, client); err == nil {
		metrics.Memory = memory
	}
	if disks, err := s.collectDiskUsage(ctx, client); err == nil {
		metrics.Disk = make([]DiskMetrics, 0, len(disks))
		for _, d := range disks {
			metrics.Disk = append(metrics.Disk, DiskMetrics(d))
		}
	}
	if network, err := s.collectNetworkInterfaces(ctx, client); err == nil {
		metrics.Network = make([]NetworkMetrics, 0, len(network))
		for _, n := range network {
			metrics.Network = append(metrics.Network, NetworkMetrics(n))
		}
	}
	if load, err := s.collectLoadMetrics(ctx, client); err == nil {
		metrics.Load = load
	}
	if uptime, err := s.collectUptime(ctx, client); err == nil {
		metrics.Uptime = uptime
	}
	if count, err := s.collectProcessCount(ctx, client); err == nil {
		metrics.ProcessCount = count
	}
	if temp, err := s.collectTemperature(ctx, client); err == nil {
		metrics.Temperature = temp
	}

	return metrics, nil
}

func (s *Service) GetMetricsHistory(ctx context.Context, serverID int, points int) ([]ServerMetrics, error) {
	_ = ctx
	_ = serverID
	_ = points

	return nil, fmt.Errorf("metrics history storage is not yet implemented")
}

func (s *Service) GetNetworkInterfaces(ctx context.Context, serverID int) ([]NetworkInterface, error) {
	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	return s.collectNetworkInterfaces(ctx, client)
}

func (s *Service) GetDiskUsage(ctx context.Context, serverID int) ([]DiskUsage, error) {
	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	return s.collectDiskUsage(ctx, client)
}

func (s *Service) GetTopProcesses(ctx context.Context, serverID int, limit int) ([]ProcessInfo, error) {
	client, err := s.getSSHClient(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	return s.collectTopProcesses(ctx, client, limit)
}

func (s *Service) collectDiskUsage(ctx context.Context, client transport.SSHExecuter) ([]DiskUsage, error) {
	stdout, _, _, err := client.ExecContext(ctx, "df -B1P")
	if err != nil {
		return nil, err
	}
	return parseDiskUsage(stdout), nil
}

func (s *Service) collectNetworkInterfaces(ctx context.Context, client transport.SSHExecuter) ([]NetworkInterface, error) {
	stdout, _, _, err := client.ExecContext(ctx, "cat /proc/net/dev")
	if err != nil {
		return nil, err
	}
	return parseNetworkInterfaces(stdout), nil
}

func (s *Service) collectTopProcesses(ctx context.Context, client transport.SSHExecuter, limit int) ([]ProcessInfo, error) {
	cmd := fmt.Sprintf("ps -eo pid,user,%%cpu,%%mem,comm --sort=-%%cpu,-%%mem | head -n %s", shared.ShellQuote(strconv.Itoa(limit+1)))
	stdout, _, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return parseTopProcesses(stdout), nil
}

func (s *Service) getSSHClient(ctx context.Context, serverID int) (transport.SSHExecuter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.serverRepo == nil || s.pool == nil || s.authSvc == nil || s.knownHosts == nil {
		return nil, fmt.Errorf("monitoring service is not configured")
	}

	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server %d not found: %w", serverID, err)
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	password, err := decryptCredential(aesKey, srv.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, srv.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, srv.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt passphrase: %w", err)
	}

	cfg := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	if srv.BastionID > 0 {
		bastion, err := s.serverRepo.GetByID(srv.BastionID)
		if err != nil {
			return nil, fmt.Errorf("load bastion %d: %w", srv.BastionID, err)
		}
		bastionCfg, err := buildBastionConfig(aesKey, bastion)
		if err != nil {
			return nil, err
		}
		cfg.Bastion = bastionCfg
	}

	hostKeyCallback := s.knownHosts.MakeHostKeyCallback(serverID)
	poolAdapter := discovery.NewPoolAdapter(s.pool)
	return poolAdapter.GetContext(ctx, serverID, cfg, hostKeyCallback)
}

func buildBastionConfig(aesKey []byte, bastion *server.Server) (*modssh.BastionConfig, error) {
	if bastion == nil {
		return nil, fmt.Errorf("bastion server is required")
	}

	password, err := decryptCredential(aesKey, bastion.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt bastion password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, bastion.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt bastion ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, bastion.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt bastion passphrase: %w", err)
	}

	port := bastion.Port
	if port == 0 {
		port = 22
	}

	cfg := &modssh.BastionConfig{
		Host:       bastion.Host,
		Port:       port,
		Username:   bastion.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}
	return cfg, nil
}

func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func (s *Service) collectCPUMetrics(ctx context.Context, client transport.SSHExecuter) (CPUMetrics, error) {
	metrics := CPUMetrics{}

	if out, _, _, err := client.ExecContext(ctx, "nproc"); err == nil {
		if cores, parseErr := strconv.Atoi(strings.TrimSpace(out)); parseErr == nil {
			metrics.Cores = cores
		}
	}
	if out, _, _, err := client.ExecContext(ctx, "LC_ALL=C lscpu | awk -F: '/Model name/ {print $2; exit}'"); err == nil {
		metrics.Model = strings.TrimSpace(out)
	}
	if out, _, _, err := client.ExecContext(ctx, "LC_ALL=C top -bn1 | head -5"); err == nil {
		metrics.Usage = parseTopCPUUsage(out)
	}

	return metrics, nil
}

func (s *Service) collectMemoryMetrics(ctx context.Context, client transport.SSHExecuter) (MemoryMetrics, error) {
	stdout, _, _, err := client.ExecContext(ctx, "free -m")
	if err != nil {
		return MemoryMetrics{}, err
	}
	return parseMemory(stdout), nil
}

func (s *Service) collectLoadMetrics(ctx context.Context, client transport.SSHExecuter) (LoadMetrics, error) {
	stdout, _, _, err := client.ExecContext(ctx, "cat /proc/loadavg")
	if err != nil {
		return LoadMetrics{}, err
	}
	return parseLoad(stdout), nil
}

func (s *Service) collectUptime(ctx context.Context, client transport.SSHExecuter) (int64, error) {
	stdout, _, _, err := client.ExecContext(ctx, "cat /proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseUptime(stdout), nil
}

func (s *Service) collectProcessCount(ctx context.Context, client transport.SSHExecuter) (int, error) {
	stdout, _, _, err := client.ExecContext(ctx, "ps aux | wc -l")
	if err != nil {
		return 0, err
	}
	count, parseErr := strconv.Atoi(strings.TrimSpace(stdout))
	if parseErr != nil {
		return 0, parseErr
	}
	return count, nil
}

func (s *Service) collectTemperature(ctx context.Context, client transport.SSHExecuter) (float64, error) {
	stdout, _, _, err := client.ExecContext(ctx, "cat /sys/class/thermal/thermal_zone*/temp 2>/dev/null | head -1")
	if err != nil {
		return 0, err
	}
	return parseTemperature(stdout), nil
}

func parseTopCPUUsage(output string) float64 {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "id") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if strings.HasPrefix(field, "id") || field == "id," || field == "id" {
				if i == 0 {
					continue
				}
				value := strings.TrimSuffix(fields[i-1], ",")
				if idle, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					usage := 100 - idle
					if usage < 0 {
						return 0
					}
					return usage
				}
			}
		}
	}
	return 0
}

func parseMemory(output string) MemoryMetrics {
	metrics := MemoryMetrics{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "Mem:":
			if len(fields) >= 7 {
				metrics.Total = parseInt64(fields[1])
				metrics.Used = parseInt64(fields[2])
				metrics.Free = parseInt64(fields[3])
				metrics.Available = parseInt64(fields[6])
				if len(fields) > 5 {
					metrics.Cached = parseInt64(fields[5])
				}
				if metrics.Total > 0 {
					metrics.UsagePercent = float64(metrics.Used) / float64(metrics.Total) * 100
				}
			}
		case "Swap:":
			if len(fields) >= 3 {
				metrics.SwapTotal = parseInt64(fields[1])
				metrics.SwapUsed = parseInt64(fields[2])
			}
		}
	}
	return metrics
}

func parseLoad(output string) LoadMetrics {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 3 {
		return LoadMetrics{}
	}
	return LoadMetrics{
		Load1:  parseFloat64(fields[0]),
		Load5:  parseFloat64(fields[1]),
		Load15: parseFloat64(fields[2]),
	}
}

func parseUptime(output string) int64 {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) == 0 {
		return 0
	}
	if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
		return int64(seconds)
	}
	return 0
}

func parseTemperature(output string) float64 {
	value := strings.TrimSpace(output)
	if value == "" {
		return 0
	}
	if temp, err := strconv.ParseFloat(value, 64); err == nil {
		if temp > 1000 {
			return temp / 1000
		}
		return temp
	}
	return 0
}

func parseDiskUsage(output string) []DiskUsage {
	var disks []DiskUsage
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Filesystem") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		disk := DiskUsage{
			Filesystem: fields[0],
			Total:      parseInt64(fields[1]),
			Used:       parseInt64(fields[2]),
			Available:  parseInt64(fields[3]),
			Mount:      fields[len(fields)-1],
		}
		if pct, err := strconv.ParseFloat(strings.TrimSuffix(fields[4], "%"), 64); err == nil {
			disk.UsagePercent = pct
		}
		disks = append(disks, disk)
	}
	return disks
}

func parseNetworkInterfaces(output string) []NetworkInterface {
	var interfaces []NetworkInterface
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Inter-") || strings.HasPrefix(line, "face") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(strings.TrimSpace(parts[1]))
		if len(fields) < 16 {
			continue
		}
		iface := NetworkInterface{
			Interface: name,
			RxBytes:   parseInt64(fields[0]),
			RxPackets: parseInt64(fields[1]),
			TxBytes:   parseInt64(fields[8]),
			TxPackets: parseInt64(fields[9]),
		}
		interfaces = append(interfaces, iface)
	}
	return interfaces
}

func parseTopProcesses(output string) []ProcessInfo {
	var processes []ProcessInfo
	for i, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 && strings.HasPrefix(strings.ToUpper(line), "PID ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		processes = append(processes, ProcessInfo{
			PID:     pid,
			User:    fields[1],
			CPU:     parseFloat64(fields[2]),
			Memory:  parseFloat64(fields[3]),
			Command: fields[4],
		})
	}
	return processes
}

func parseInt64(value string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return v
}

func parseFloat64(value string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return v
}
