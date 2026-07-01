package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// ProcessInfo describes a single process entry returned by ps.
type ProcessInfo struct {
	PID     int     `json:"pid"`
	PPID    int     `json:"ppid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	VSZ     int64   `json:"vsz"`
	RSS     int64   `json:"rss"`
	Stat    string  `json:"stat"`
	Start   string  `json:"start"`
	Time    string  `json:"time"`
	Command string  `json:"command"`
}

// Service manages remote process inspection and control over SSH.
type Service struct {
	srvRepo    server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	knownHosts transport.HostKeyStore
}

// NewService creates a process service.
func NewService(
	srvRepo server.Repo,
	pool *modssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
) *Service {
	return &Service{
		srvRepo:    srvRepo,
		pool:       pool,
		authSvc:    authSvc,
		knownHosts: knownHosts,
	}
}

// getSSHClient obtains a pooled SSH client for a server.
func (s *Service) getSSHClient(serverID int) (transport.SSHExecuter, error) {
	if s == nil {
		return nil, fmt.Errorf("process service is not configured")
	}
	if s.srvRepo == nil {
		return nil, fmt.Errorf("server repository is not configured")
	}
	if s.pool == nil {
		return nil, fmt.Errorf("ssh pool is not configured")
	}
	if s.authSvc == nil {
		return nil, fmt.Errorf("app is locked")
	}
	if s.knownHosts == nil {
		return nil, fmt.Errorf("known hosts store is not configured")
	}

	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
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

	port := srv.Port
	if port == 0 {
		port = 22
	}

	cfg := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
		Timeouts:   modssh.DiscoveryTimeouts,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	if srv.BastionID != 0 {
		if bastion, err := s.srvRepo.GetByID(srv.BastionID); err == nil && bastion != nil {
			cfg.Bastion = buildBastionConfig(bastion, aesKey, s.knownHosts)
		}
	}

	hostKeyCallback := s.knownHosts.MakeHostKeyCallback(serverID)
	return discovery.NewPoolAdapter(s.pool).Get(serverID, cfg, hostKeyCallback)
}

// ListProcesses runs ps aux on the remote server and returns parsed results.
func (s *Service) ListProcesses(ctx context.Context, serverID int, sortBy string) ([]ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sortBy, err := normalizeSortBy(sortBy)
	if err != nil {
		return nil, err
	}

	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, "ps aux")
	if err != nil {
		return nil, fmt.Errorf("run ps aux: %w", err)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = "ps aux failed"
		}
		return nil, errors.New(msg)
	}

	processes, err := parsePsAux(stdout)
	if err != nil {
		return nil, err
	}
	applyProcessSort(processes, sortBy)
	return processes, nil
}

// GetProcess returns detailed information for a single process.
func (s *Service) GetProcess(ctx context.Context, serverID int, pid int) (*ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("ps -p %d -o pid=,ppid=,user=,%%cpu=,%%mem=,vsz=,rss=,stat=,start=,time=,command=", pid)
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("run ps for pid %d: %w", pid, err)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = fmt.Sprintf("process %d not found", pid)
		}
		return nil, errors.New(msg)
	}

	process, err := parsePsDetail(stdout)
	if err != nil {
		return nil, err
	}
	return &process, nil
}

// KillProcess sends a signal to a remote process.
func (s *Service) KillProcess(ctx context.Context, serverID int, pid int, signal string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	normalizedSignal, err := normalizeSignal(signal)
	if err != nil {
		return err
	}

	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("kill -%s %d", normalizedSignal, pid)
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("run kill for pid %d: %w", pid, err)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = fmt.Sprintf("failed to kill process %d", pid)
		}
		return errors.New(msg)
	}

	return nil
}

// GetTopProcesses returns the top processes according to the requested sort.
func (s *Service) GetTopProcesses(ctx context.Context, serverID int, limit int, sortBy string) ([]ProcessInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 10
	}

	sortBy, err := normalizeSortBy(sortBy)
	if err != nil {
		return nil, err
	}

	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("ps aux --sort=-%s | head -%d", sortKeyForPs(sortBy), limit+1)
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("run top process query: %w", err)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = "failed to get top processes"
		}
		return nil, errors.New(msg)
	}

	processes, err := parsePsAux(stdout)
	if err != nil {
		return nil, err
	}
	applyProcessSort(processes, sortBy)
	if len(processes) > limit {
		processes = processes[:limit]
	}
	return processes, nil
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

func buildBastionConfig(bastion *server.Server, aesKey []byte, knownHosts transport.HostKeyStore) *modssh.BastionConfig {
	if bastion == nil {
		return nil
	}

	port := bastion.Port
	if port == 0 {
		port = 22
	}

	cfg := &modssh.BastionConfig{
		Host:     bastion.Host,
		Port:     port,
		Username: bastion.Username,
	}
	if knownHosts != nil {
		cfg.HostKeyCallback = knownHosts.MakeHostKeyCallback(bastion.ID)
	}

	if bastion.Password != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.Password)); err == nil {
			cfg.Password = string(decrypted)
		}
	}
	if bastion.SSHKey != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.SSHKey)); err == nil {
			cfg.PrivateKey = decrypted
		}
	}
	if bastion.Passphrase != "" {
		if decrypted, err := shared.Decrypt(aesKey, []byte(bastion.Passphrase)); err == nil {
			cfg.Passphrase = string(decrypted)
		}
	}

	return cfg
}

func normalizeSortBy(sortBy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "", "cpu":
		return "cpu", nil
	case "mem", "memory":
		return "mem", nil
	case "pid":
		return "pid", nil
	default:
		return "", fmt.Errorf("invalid sort value: %s", sortBy)
	}
}

func normalizeSignal(signal string) (string, error) {
	signal = strings.ToUpper(strings.TrimSpace(signal))
	if signal == "" {
		signal = "TERM"
	}

	switch signal {
	case "TERM", "KILL", "HUP", "INT", "QUIT", "USR1", "USR2", "STOP", "CONT":
		return signal, nil
	default:
		return "", fmt.Errorf("invalid signal: %s", signal)
	}
}

func sortKeyForPs(sortBy string) string {
	switch sortBy {
	case "mem":
		return "pmem"
	case "pid":
		return "pid"
	default:
		return "pcpu"
	}
}

func applyProcessSort(processes []ProcessInfo, sortBy string) {
	switch sortBy {
	case "mem":
		sort.SliceStable(processes, func(i, j int) bool {
			if processes[i].Memory == processes[j].Memory {
				return processes[i].PID < processes[j].PID
			}
			return processes[i].Memory > processes[j].Memory
		})
	case "pid":
		sort.SliceStable(processes, func(i, j int) bool {
			if processes[i].PID == processes[j].PID {
				return processes[i].CPU > processes[j].CPU
			}
			return processes[i].PID < processes[j].PID
		})
	default:
		sort.SliceStable(processes, func(i, j int) bool {
			if processes[i].CPU == processes[j].CPU {
				return processes[i].PID < processes[j].PID
			}
			return processes[i].CPU > processes[j].CPU
		})
	}
}

func parsePsAux(output string) ([]ProcessInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	processes := make([]ProcessInfo, 0)
	lineNum := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineNum++
		if lineNum == 1 {
			continue
		}
		fields := strings.Fields(line)
		process, err := parsePsAuxFields(fields)
		if err != nil {
			return nil, err
		}
		processes = append(processes, process)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return processes, nil
}

func parsePsDetail(output string) (ProcessInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		return parsePsDetailFields(fields)
	}
	if err := scanner.Err(); err != nil {
		return ProcessInfo{}, err
	}
	return ProcessInfo{}, fmt.Errorf("process not found")
}

func parsePsAuxFields(fields []string) (ProcessInfo, error) {
	if len(fields) < 11 {
		return ProcessInfo{}, fmt.Errorf("unexpected ps aux output")
	}

	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse pid: %w", err)
	}
	cpu, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse cpu: %w", err)
	}
	memory, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse memory: %w", err)
	}
	vsz, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse vsz: %w", err)
	}
	rss, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse rss: %w", err)
	}

	return ProcessInfo{
		PID:     pid,
		User:    fields[0],
		CPU:     cpu,
		Memory:  memory,
		VSZ:     vsz,
		RSS:     rss,
		Stat:    fields[7],
		Start:   fields[8],
		Time:    fields[9],
		Command: strings.Join(fields[10:], " "),
	}, nil
}

func parsePsDetailFields(fields []string) (ProcessInfo, error) {
	if len(fields) < 11 {
		return ProcessInfo{}, fmt.Errorf("unexpected ps output")
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse pid: %w", err)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse ppid: %w", err)
	}
	cpu, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse cpu: %w", err)
	}
	memory, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse memory: %w", err)
	}
	vsz, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse vsz: %w", err)
	}
	rss, err := strconv.ParseInt(fields[6], 10, 64)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse rss: %w", err)
	}

	return ProcessInfo{
		PID:     pid,
		PPID:    ppid,
		User:    fields[2],
		CPU:     cpu,
		Memory:  memory,
		VSZ:     vsz,
		RSS:     rss,
		Stat:    fields[7],
		Start:   fields[8],
		Time:    fields[9],
		Command: strings.Join(fields[10:], " "),
	}, nil
}
