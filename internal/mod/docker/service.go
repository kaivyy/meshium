package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// Service provides Docker management operations on remote servers over SSH.
type Service struct {
	serverRepo server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	knownHosts transport.HostKeyStore
}

// NewService creates a new Docker service.
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

// PortMapping represents a Docker port mapping.
type PortMapping struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

// Container represents a Docker container.
type Container struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Image  string        `json:"image"`
	State  string        `json:"state"`
	Status string        `json:"status"`
	Ports  []PortMapping `json:"ports,omitempty"`
}

// Image represents a Docker image.
type Image struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
}

// ContainerListResponse wraps a container list for API responses.
type ContainerListResponse struct {
	Containers []Container `json:"containers"`
	Total      int         `json:"total"`
}

// ImageListResponse wraps an image list for API responses.
type ImageListResponse struct {
	Images []Image `json:"images"`
	Total  int     `json:"total"`
}

// ActionResponse is returned after Docker operations such as start/stop/remove.
type ActionResponse struct {
	Name    string `json:"name"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

// PullResponse is returned after pulling an image.
type PullResponse struct {
	Image  string `json:"image"`
	Output string `json:"output"`
}

// LogsResponse is returned by the logs endpoint.
type LogsResponse struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
	Logs  string `json:"logs"`
}

type dockerContainerJSON struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

type dockerImageJSON struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
}

// ListContainers returns all Docker containers on the target server.
func (s *Service) ListContainers(ctx context.Context, serverID int) ([]Container, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, "docker ps -a --format '{{json .}}'")
	if exitCode != 0 {
		if isDockerUnavailable(stderr, stdout, err) {
			return []Container{}, nil
		}
		return nil, fmt.Errorf("docker ps failed: %s", combineOutput(stdout, stderr, err))
	}

	containers := parseContainers(stdout)
	return containers, nil
}

// ListImages returns all Docker images on the target server.
func (s *Service) ListImages(ctx context.Context, serverID int) ([]Image, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, "docker images --format '{{json .}}'")
	if exitCode != 0 {
		if isDockerUnavailable(stderr, stdout, err) {
			return []Image{}, nil
		}
		return nil, fmt.Errorf("docker images failed: %s", combineOutput(stdout, stderr, err))
	}

	images := parseImages(stdout)
	return images, nil
}

// StartContainer starts a Docker container by name.
func (s *Service) StartContainer(ctx context.Context, serverID int, name string) error {
	return s.runContainerCommand(ctx, serverID, "start", name)
}

// StopContainer stops a Docker container by name.
func (s *Service) StopContainer(ctx context.Context, serverID int, name string) error {
	return s.runContainerCommand(ctx, serverID, "stop", name)
}

// RestartContainer restarts a Docker container by name.
func (s *Service) RestartContainer(ctx context.Context, serverID int, name string) error {
	return s.runContainerCommand(ctx, serverID, "restart", name)
}

// RemoveContainer removes a Docker container by name.
func (s *Service) RemoveContainer(ctx context.Context, serverID int, name string, force bool) error {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return err
	}

	cmd := "docker rm"
	if force {
		cmd += " -f"
	}
	cmd = fmt.Sprintf("%s %s", cmd, shared.ShellQuote(name))

	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if exitCode != 0 {
		return fmt.Errorf("docker rm failed: %s", combineOutput(stdout, stderr, err))
	}
	return nil
}

// PullImage pulls a Docker image by name.
func (s *Service) PullImage(ctx context.Context, serverID int, image string) (string, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return "", err
	}

	cmd := fmt.Sprintf("docker pull %s", shared.ShellQuote(image))
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if exitCode != 0 {
		return "", fmt.Errorf("docker pull failed: %s", combineOutput(stdout, stderr, err))
	}

	output := strings.TrimSpace(stdout)
	if trimmedErr := strings.TrimSpace(stderr); trimmedErr != "" {
		if output != "" {
			output += "\n"
		}
		output += trimmedErr
	}
	return output, nil
}

// GetContainerLogs returns the last N lines of logs for a container.
func (s *Service) GetContainerLogs(ctx context.Context, serverID int, name string, lines int) (string, error) {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return "", err
	}

	if lines <= 0 {
		lines = 100
	}
	if lines > 1000 {
		lines = 1000
	}

	cmd := fmt.Sprintf("docker logs --tail %d %s", lines, shared.ShellQuote(name))
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if exitCode != 0 {
		return "", fmt.Errorf("docker logs failed: %s", combineOutput(stdout, stderr, err))
	}

	return stdout, nil
}

func (s *Service) runContainerCommand(ctx context.Context, serverID int, action, name string) error {
	sshClient, err := s.getSSHClient(serverID)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf("docker %s %s", action, shared.ShellQuote(name))
	stdout, stderr, exitCode, err := sshClient.ExecContext(ctx, cmd)
	if exitCode != 0 {
		return fmt.Errorf("docker %s failed: %s", action, combineOutput(stdout, stderr, err))
	}
	return nil
}

func (s *Service) getSSHClient(serverID int) (transport.SSHExecuter, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ssh pool is not configured")
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
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       port,
		Username:   srv.Username,
		Password:   password,
		Passphrase: passphrase,
	}
	if sshKey != "" {
		cfg.PrivateKey = []byte(sshKey)
	}

	if srv.BastionID != 0 {
		if bastion, err := s.serverRepo.GetByID(srv.BastionID); err == nil {
			cfg.Bastion = s.buildBastionConfig(bastion, aesKey)
		}
	}

	hostKeyCallback := s.knownHosts.MakeHostKeyCallback(serverID)
	poolAdapter := discovery.NewPoolAdapter(s.pool)
	return poolAdapter.Get(serverID, cfg, hostKeyCallback)
}

func (s *Service) buildBastionConfig(bastion *server.Server, aesKey []byte) *modssh.BastionConfig {
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

func parseContainers(output string) []Container {
	var containers []Container
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var raw dockerContainerJSON
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		containers = append(containers, Container{
			ID:     raw.ID,
			Name:   strings.TrimSpace(raw.Names),
			Image:  strings.TrimSpace(raw.Image),
			State:  strings.TrimSpace(raw.State),
			Status: strings.TrimSpace(raw.Status),
			Ports:  parseDockerPorts(raw.Ports),
		})
	}
	return containers
}

func parseImages(output string) []Image {
	var images []Image
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var raw dockerImageJSON
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		images = append(images, Image{
			ID:         raw.ID,
			Repository: strings.TrimSpace(raw.Repository),
			Tag:        strings.TrimSpace(raw.Tag),
			Size:       strings.TrimSpace(raw.Size),
		})
	}
	return images
}

func parseDockerPorts(raw string) []PortMapping {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var ports []PortMapping
	for _, part := range strings.Split(raw, ",") {
		port := parseDockerPort(strings.TrimSpace(part))
		if port.ContainerPort == 0 && port.HostPort == 0 && port.Protocol == "" {
			continue
		}
		ports = append(ports, port)
	}
	return ports
}

func parseDockerPort(s string) PortMapping {
	pm := PortMapping{Protocol: "tcp"}
	if s == "" {
		return pm
	}

	if !strings.Contains(s, "->") {
		if idx := strings.Index(s, "/"); idx >= 0 {
			pm.ContainerPort, _ = strconv.Atoi(strings.TrimSpace(s[:idx]))
			pm.Protocol = strings.TrimSpace(s[idx+1:])
		}
		return pm
	}

	parts := strings.SplitN(s, "->", 2)
	if len(parts) != 2 {
		return pm
	}

	hostPart := strings.TrimSpace(parts[0])
	containerPart := strings.TrimSpace(parts[1])
	if idx := strings.LastIndex(hostPart, ":"); idx >= 0 {
		pm.HostPort, _ = strconv.Atoi(strings.TrimSpace(hostPart[idx+1:]))
	}
	if idx := strings.Index(containerPart, "/"); idx >= 0 {
		pm.ContainerPort, _ = strconv.Atoi(strings.TrimSpace(containerPart[:idx]))
		pm.Protocol = strings.TrimSpace(containerPart[idx+1:])
	}
	return pm
}

func combineOutput(stdout, stderr string, err error) string {
	parts := make([]string, 0, 3)
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if err != nil {
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, ": ")
}

func isDockerUnavailable(stderr, stdout string, err error) bool {
	msg := strings.ToLower(strings.Join([]string{stderr, stdout, errorString(err)}, " "))
	return strings.Contains(msg, "docker: not found") ||
		strings.Contains(msg, "command not found") ||
		strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file or directory")
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
