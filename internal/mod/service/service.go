package service

import (
	"context"
	"fmt"
	"strings"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// ServiceInfo represents a systemd service exposed to the UI.
type ServiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
	Type        string `json:"type"`
	Enabled     bool   `json:"enabled"`
}

// ServiceStatus is the detailed view for a single service.
type ServiceStatus = ServiceInfo

// Service provides systemd service operations over SSH.
type Service struct {
	serverRepo server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	knownHosts transport.HostKeyStore
}

// NewService creates a new Service operations manager.
func NewService(
	serverRepo server.Repo,
	pool *modssh.Pool,
	authSvc transport.AESKeyProvider,
	knownHosts transport.HostKeyStore,
) *Service {
	return &Service{
		serverRepo: serverRepo,
		pool:       pool,
		authSvc:    authSvc,
		knownHosts: knownHosts,
	}
}

// getSSHClient obtains an SSH connection to the remote server.
func (s *Service) getSSHClient(serverID int) (transport.SSHExecuter, error) {
	if s == nil {
		return nil, fmt.Errorf("service manager is not configured")
	}
	if s.serverRepo == nil {
		return nil, fmt.Errorf("server repository is not configured")
	}
	if s.authSvc == nil {
		return nil, fmt.Errorf("auth service is not configured")
	}
	if s.knownHosts == nil {
		return nil, fmt.Errorf("known hosts store is not configured")
	}

	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server %d not found: %w", serverID, err)
	}

	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked — cannot decrypt credentials")
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
		Host:       srv.Host,
		Port:       port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	hostKeyCallback := s.knownHosts.MakeHostKeyCallback(serverID)
	poolAdapter := discovery.NewPoolAdapter(s.pool)
	return poolAdapter.Get(serverID, cfg, hostKeyCallback)
}

// ListServices lists systemd services on a remote server.
func (s *Service) ListServices(ctx context.Context, serverID int) ([]ServiceInfo, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := `systemctl list-units --type=service --all --no-legend --no-pager 2>/dev/null`
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("list services failed: %w (stderr: %s)", err, stderr)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = "systemctl list-units failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	services := make([]ServiceInfo, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		info := ServiceInfo{
			Name:        fields[0],
			LoadState:   fields[1],
			ActiveState: fields[2],
			SubState:    fields[3],
		}
		if len(fields) > 4 {
			info.Description = strings.Join(fields[4:], " ")
		}

		if status, err := s.queryServiceStatus(ctx, sshClient, info.Name); err == nil && status != nil {
			if status.Description != "" {
				info.Description = status.Description
			}
			if status.LoadState != "" {
				info.LoadState = status.LoadState
			}
			if status.ActiveState != "" {
				info.ActiveState = status.ActiveState
			}
			if status.SubState != "" {
				info.SubState = status.SubState
			}
			info.Type = status.Type
			info.Enabled = status.Enabled
		}

		services = append(services, info)
	}

	return services, nil
}

// GetServiceStatus returns the current systemd status for a service.
func (s *Service) GetServiceStatus(ctx context.Context, serverID int, name string) (*ServiceStatus, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}
	return s.queryServiceStatus(ctx, sshClient, name)
}

// StartService starts a systemd service.
func (s *Service) StartService(ctx context.Context, serverID int, name string) error {
	return s.runServiceCommand(ctx, serverID, name, "start", fmt.Sprintf("sudo systemctl start %s", shared.ShellQuote(name)))
}

// StopService stops a systemd service.
func (s *Service) StopService(ctx context.Context, serverID int, name string) error {
	return s.runServiceCommand(ctx, serverID, name, "stop", fmt.Sprintf("sudo systemctl stop %s", shared.ShellQuote(name)))
}

// RestartService restarts a systemd service.
func (s *Service) RestartService(ctx context.Context, serverID int, name string) error {
	return s.runServiceCommand(ctx, serverID, name, "restart", fmt.Sprintf("sudo systemctl restart %s", shared.ShellQuote(name)))
}

// EnableService enables a systemd service.
func (s *Service) EnableService(ctx context.Context, serverID int, name string) error {
	return s.runServiceCommand(ctx, serverID, name, "enable", fmt.Sprintf("sudo systemctl enable %s", shared.ShellQuote(name)))
}

// DisableService disables a systemd service.
func (s *Service) DisableService(ctx context.Context, serverID int, name string) error {
	return s.runServiceCommand(ctx, serverID, name, "disable", fmt.Sprintf("sudo systemctl disable %s", shared.ShellQuote(name)))
}

func (s *Service) runServiceCommand(ctx context.Context, serverID int, name, action, command string) error {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return err
	}

	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, command)
	if err != nil {
		return fmt.Errorf("%s service %s failed: %w (stderr: %s)", action, name, err, stderr)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = fmt.Sprintf("systemctl %s failed", action)
		}
		return fmt.Errorf("%s service %s failed: %s", action, name, msg)
	}

	return nil
}

func (s *Service) queryServiceStatus(ctx context.Context, sshClient transport.SSHExecuter, name string) (*ServiceStatus, error) {
	cmd := fmt.Sprintf(
		`systemctl show %s --property=ActiveState,SubState,Description,LoadState,Type,UnitFileState`,
		shared.ShellQuote(name),
	)
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("get service status for %s failed: %w (stderr: %s)", name, err, stderr)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = fmt.Sprintf("systemctl show failed for %s", name)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	status := &ServiceStatus{Name: name}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]
		switch key {
		case "Description":
			status.Description = value
		case "LoadState":
			status.LoadState = value
		case "ActiveState":
			status.ActiveState = value
		case "SubState":
			status.SubState = value
		case "Type":
			status.Type = value
		case "UnitFileState":
			status.Enabled = isEnabledState(value)
		}
	}

	return status, nil
}

func isEnabledState(state string) bool {
	switch strings.TrimSpace(state) {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias":
		return true
	default:
		return false
	}
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
