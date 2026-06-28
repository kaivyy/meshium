package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"

	xssh "golang.org/x/crypto/ssh"
)

// AESKeyProvider exposes the AES key needed to decrypt stored credentials.
type AESKeyProvider = transport.AESKeyProvider

// ConnectionPool provides SSH clients for a server.
type ConnectionPool = transport.ConnectionPool

// HostKeyStore provides host key verification callbacks.
type HostKeyStore = transport.HostKeyStore

type poolAdapter struct {
	pool *modssh.Pool
}

// NewPoolAdapter wraps the real SSH pool so it can satisfy ConnectionPool.
func NewPoolAdapter(pool *modssh.Pool) ConnectionPool {
	return &poolAdapter{pool: pool}
}

func (a *poolAdapter) Get(serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	if a == nil || a.pool == nil {
		return nil, fmt.Errorf("ssh pool is not configured")
	}
	client, err := a.pool.Get(serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (a *poolAdapter) GetContext(ctx context.Context, serverID int, cfg modssh.ServerConfig, hostKeyCallback xssh.HostKeyCallback) (SSHExecuter, error) {
	if a == nil || a.pool == nil {
		return nil, fmt.Errorf("ssh pool is not configured")
	}
	client, err := a.pool.GetContext(ctx, serverID, cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Service orchestrates the connection test.
type Service struct {
	pool    ConnectionPool
	repo    server.Repo
	authSvc AESKeyProvider
	hosts   HostKeyStore
}

func NewService(pool ConnectionPool, repo server.Repo, authSvc AESKeyProvider, hosts HostKeyStore) *Service {
	return &Service{pool: pool, repo: repo, authSvc: authSvc, hosts: hosts}
}

// StepCallback is called for each step result during connection test.
type StepCallback func(msg WSMessage)

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

func stepMessageFromResult(r StepResult) WSMessage {
	msg := WSMessage{Step: r.Name, Status: "success"}
	if r.Error != nil {
		msg.Status = "error"
		if r.Timeout {
			msg.Error = fmt.Sprintf("timeout: %v", r.Error)
		} else {
			msg.Error = r.Error.Error()
		}
		return msg
	}

	switch r.Name {
	case "cpu_cores":
		msg.Value = r.IntValue
	case "ram_total_mb":
		msg.Value = r.IntValue
	case "disk_total_gb":
		msg.Value = r.FloatValue
	default:
		msg.Value = r.Value
	}

	return msg
}

func applyResultToSystemInfo(info *SystemInfo, r StepResult) {
	if info == nil || r.Error != nil {
		return
	}

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

func sendErrorStep(onStep StepCallback, step, message string) {
	onStep(WSMessage{Step: step, Status: "error", Error: message})
}

func isSSHDisconnectError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"eof",
		"connection lost",
		"connection reset",
		"broken pipe",
		"use of closed network connection",
		"connection timed out",
		"session is not active",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}

	return false
}

func collectorSteps(c *Collector) []func() StepResult {
	return []func() StepResult{
		c.CollectHostname,
		c.CollectOS,
		c.CollectKernel,
		c.CollectArchitecture,
		c.CollectCPUModel,
		c.CollectCPUCores,
		c.CollectRAM,
		c.CollectDisk,
		c.CollectVirtualization,
		c.CollectPublicIP,
		c.CollectPrivateIP,
		c.CollectTimezone,
		c.CollectProvider,
	}
}

// RunConnectionTest connects to a server, runs all discovery commands,
// and calls onStep for each result. Results are cached to server_info.
func (s *Service) RunConnectionTest(ctx context.Context, serverID int, onStep StepCallback) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if onStep == nil {
		onStep = func(WSMessage) {}
	}

	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		sendErrorStep(onStep, "lookup", "server not found")
		return fmt.Errorf("server not found: %w", err)
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		sendErrorStep(onStep, "auth", "app is locked")
		return fmt.Errorf("app is locked")
	}

	password, err := decryptCredential(aesKey, srv.Password)
	if err != nil {
		sendErrorStep(onStep, "auth", "failed to decrypt credentials")
		return fmt.Errorf("failed to decrypt password: %w", err)
	}
	sshKey, err := decryptCredential(aesKey, srv.SSHKey)
	if err != nil {
		sendErrorStep(onStep, "auth", "failed to decrypt credentials")
		return fmt.Errorf("failed to decrypt ssh key: %w", err)
	}
	passphrase, err := decryptCredential(aesKey, srv.Passphrase)
	if err != nil {
		sendErrorStep(onStep, "auth", "failed to decrypt credentials")
		return fmt.Errorf("failed to decrypt passphrase: %w", err)
	}

	sshConfig := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		sshConfig.PrivateKey = []byte(sshKey)
	}

	// Resolve bastion if configured
	if srv.BastionID > 0 {
		bastion, err := s.repo.GetByID(srv.BastionID)
		if err == nil {
			bPassword, _ := decryptCredential(aesKey, bastion.Password)
			bSSHKey, _ := decryptCredential(aesKey, bastion.SSHKey)
			bPassphrase, _ := decryptCredential(aesKey, bastion.Passphrase)
			bPort := bastion.Port
			if bPort == 0 {
				bPort = 22
			}
			bastionCfg := &modssh.BastionConfig{
				Host:            bastion.Host,
				Port:            bPort,
				Username:        bastion.Username,
				Password:        bPassword,
				Passphrase:      bPassphrase,
				HostKeyCallback: s.hosts.MakeHostKeyCallback(bastion.ID),
			}
			if bSSHKey != "" {
				bastionCfg.PrivateKey = []byte(bSSHKey)
			}
			sshConfig.Bastion = bastionCfg
		}
	}

	start := time.Now()
	hostKeyCallback := s.hosts.MakeHostKeyCallback(serverID)
	client, err := s.pool.Get(serverID, sshConfig, hostKeyCallback)
	latency := time.Since(start)
	if err != nil {
		onStep(WSMessage{
			Step:      "ssh",
			Status:    "error",
			Error:     err.Error(),
			LatencyMs: int(latency.Milliseconds()),
		})
		return err
	}

	onStep(WSMessage{
		Step:      "ssh",
		Status:    "success",
		LatencyMs: int(latency.Milliseconds()),
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}

	collector := NewCollector(client)
	results := collectorSteps(collector)
	info := SystemInfo{
		SSHStatus: "connected",
		LatencyMs: int(latency.Milliseconds()),
	}

	for _, collect := range results {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		result := collect()
		if result.Error != nil {
			if client == nil || isSSHDisconnectError(result.Error) || !client.IsAlive() {
				onStep(WSMessage{Step: "ssh", Status: "error", Error: "SSH connection lost"})
				return fmt.Errorf("ssh connection lost: %w", result.Error)
			}
		}

		onStep(stepMessageFromResult(result))
		if ctx.Err() != nil {
			return ctx.Err()
		}

		applyResultToSystemInfo(&info, result)
	}

	rawData, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := s.repo.SaveServerInfo(serverID, server.ServerInfo(info), string(rawData)); err != nil {
		return err
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	onStep(WSMessage{Step: "done", Status: "complete"})
	return nil
}
