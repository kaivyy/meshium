package logview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/discovery"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

const (
	defaultLogLines = 100
	maxLogLines     = 1000
)

// LogResponse is the standard log payload returned by the REST API.
type LogResponse struct {
	File      string   `json:"file"`
	Lines     []string `json:"lines"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated"`
}

// LogFileInfo describes a log file discovered on the remote server.
type LogFileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// LogSearchRequest is the payload used by the search endpoint.
type LogSearchRequest struct {
	File    string `json:"file"`
	Pattern string `json:"pattern"`
	Lines   int    `json:"lines"`
}

// StreamOptions controls the live log stream endpoint.
type StreamOptions struct {
	File        string
	ServiceName string
	System      bool
	Filter      string
}

type streamExecutor interface {
	ExecStreamLinesContextWithTimeout(ctx context.Context, cmd string, timeout time.Duration, onOutput modssh.StreamCallback) error
}

// Service orchestrates remote log viewing over SSH.
type Service struct {
	serverRepo server.Repo
	pool       *modssh.Pool
	authSvc    transport.AESKeyProvider
	hosts      transport.HostKeyStore
}

// NewService creates a log viewer service.
func NewService(serverRepo server.Repo, pool *modssh.Pool, authSvc transport.AESKeyProvider, knownHosts transport.HostKeyStore) *Service {
	return &Service{serverRepo: serverRepo, pool: pool, authSvc: authSvc, hosts: knownHosts}
}

func clampLines(lines int) int {
	if lines <= 0 {
		return defaultLogLines
	}
	if lines > maxLogLines {
		return maxLogLines
	}
	return lines
}

func splitLines(out string) []string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return []string{}
	}
	return strings.Split(out, "\n")
}

func validateLogPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("file path is required")
	}
	if strings.ContainsAny(path, "`$&;|<>\n\r\t") {
		return fmt.Errorf("invalid file path")
	}
	return nil
}

func quote(path string) string {
	return shared.ShellQuote(path)
}

func decryptValue(key []byte, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(value))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func (s *Service) getSSHClient(serverID int) (transport.SSHExecuter, error) {
	if s == nil || s.serverRepo == nil {
		return nil, errors.New("log viewer service is not configured")
	}
	if s.pool == nil {
		return nil, errors.New("ssh pool is not configured")
	}
	if s.authSvc == nil || s.hosts == nil {
		return nil, errors.New("credential providers are not configured")
	}

	aesKey := s.authSvc.GetAESKey()
	if len(aesKey) == 0 {
		return nil, errors.New("app is locked")
	}

	srv, err := s.serverRepo.GetByID(serverID)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		return nil, errors.New("server not found")
	}

	password, err := decryptValue(aesKey, srv.Password)
	if err != nil {
		return nil, err
	}
	privateKey, err := decryptValue(aesKey, srv.SSHKey)
	if err != nil {
		return nil, err
	}
	passphrase, err := decryptValue(aesKey, srv.Passphrase)
	if err != nil {
		return nil, err
	}

	cfg := modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       srv.Port,
		Username:   srv.Username,
		Password:   password,
		PrivateKey: []byte(privateKey),
		Passphrase: passphrase,
	}

	adapter := discovery.NewPoolAdapter(s.pool)
	return adapter.Get(serverID, cfg, s.hosts.MakeHostKeyCallback(serverID))
}

func responseFromOutput(file, out string, lines int) *LogResponse {
	entries := splitLines(out)
	limit := clampLines(lines)
	return &LogResponse{
		File:      file,
		Lines:     entries,
		Total:     len(entries),
		Truncated: limit > 0 && len(entries) >= limit,
	}
}

func parseLogFiles(out string) []LogFileInfo {
	lines := splitLines(out)
	files := make([]LogFileInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}

		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		timestamp, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}

		path := parts[0]
		files = append(files, LogFileInfo{
			Name:     filepath.Base(path),
			Path:     path,
			Size:     size,
			Modified: time.Unix(timestamp, 0).Format(time.RFC3339),
		})
	}
	return files
}

func commandFailed(stderr string) bool {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return false
	}
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "cannot open") ||
		strings.Contains(lower, "not found")
}

// ReadLog returns the last N lines from a log file, optionally filtering by a pattern.
func (s *Service) ReadLog(ctx context.Context, serverID int, filePath string, lines int, filter string) (*LogResponse, error) {
	if err := validateLogPath(filePath); err != nil {
		return nil, err
	}
	lines = clampLines(lines)

	client, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("tail -n %d %s", lines, quote(filePath))
	if strings.TrimSpace(filter) != "" {
		cmd = fmt.Sprintf("%s | grep -- %s", cmd, quote(filter))
	}

	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if commandFailed(stderr) {
		return nil, errors.New(strings.TrimSpace(stderr))
	}

	return responseFromOutput(filePath, stdout, lines), nil
}

// ListLogFiles returns common log files found in a directory.
func (s *Service) ListLogFiles(ctx context.Context, serverID int, dir string) ([]LogFileInfo, error) {
	if err := validateLogPath(dir); err != nil {
		return nil, err
	}

	client, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf(`sh -c 'if [ ! -d "$1" ]; then echo "directory not found" >&2; exit 1; fi; for f in "$1"/*.log "$1"/*.log.*; do [ -e "$f" ] || continue; if [ -f "$f" ] || [ -L "$f" ]; then stat -c "%%n\t%%s\t%%Y" "$f"; fi; done' sh %s`, quote(dir))
	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if commandFailed(stderr) {
		return nil, errors.New(strings.TrimSpace(stderr))
	}

	return parseLogFiles(stdout), nil
}

// GetSystemLogs returns the latest system journal entries.
func (s *Service) GetSystemLogs(ctx context.Context, serverID int, lines int) (*LogResponse, error) {
	lines = clampLines(lines)
	client, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("journalctl -n %d --no-pager", lines)
	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if commandFailed(stderr) {
		return nil, errors.New(strings.TrimSpace(stderr))
	}

	return responseFromOutput("system", stdout, lines), nil
}

// GetServiceLogs returns the latest journal entries for a service.
func (s *Service) GetServiceLogs(ctx context.Context, serverID int, serviceName string, lines int) (*LogResponse, error) {
	if strings.TrimSpace(serviceName) == "" {
		return nil, errors.New("service name is required")
	}
	lines = clampLines(lines)
	client, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager", quote(serviceName), lines)
	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if commandFailed(stderr) {
		return nil, errors.New(strings.TrimSpace(stderr))
	}

	return responseFromOutput(serviceName, stdout, lines), nil
}

// SearchLogs searches a log file for a pattern and returns the matching lines.
func (s *Service) SearchLogs(ctx context.Context, serverID int, req LogSearchRequest) (*LogResponse, error) {
	if err := validateLogPath(req.File); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, errors.New("search pattern is required")
	}

	lines := clampLines(req.Lines)
	client, err := s.getSSHClient(serverID)
	if err != nil {
		return nil, err
	}

	cmd := fmt.Sprintf("grep -- %s %s | tail -n %d", quote(req.Pattern), quote(req.File), lines)
	stdout, stderr, _, err := client.ExecContext(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if commandFailed(stderr) {
		return nil, errors.New(strings.TrimSpace(stderr))
	}

	return responseFromOutput(req.File, stdout, lines), nil
}

// StreamLogs follows log output and invokes onLine for each new line.
func (s *Service) StreamLogs(ctx context.Context, serverID int, opts StreamOptions, onLine func(string)) error {
	if onLine == nil {
		return errors.New("stream callback is required")
	}

	client, err := s.getSSHClient(serverID)
	if err != nil {
		return err
	}

	var cmd string
	switch {
	case strings.TrimSpace(opts.ServiceName) != "":
		cmd = fmt.Sprintf("journalctl -f -u %s --no-pager", quote(opts.ServiceName))
	case opts.System:
		cmd = "journalctl -f --no-pager"
	default:
		if err := validateLogPath(opts.File); err != nil {
			return err
		}
		cmd = fmt.Sprintf("tail -n 0 -F %s", quote(opts.File))
	}

	if strings.TrimSpace(opts.Filter) != "" {
		cmd = fmt.Sprintf("%s | grep --line-buffered -- %s", cmd, quote(opts.Filter))
	}

	streamer, ok := client.(streamExecutor)
	if !ok {
		return errors.New("ssh client does not support streaming")
	}

	return streamer.ExecStreamLinesContextWithTimeout(ctx, cmd, 0, func(source modssh.StreamSource, line string) {
		if source != modssh.StreamStdout {
			return
		}
		onLine(line)
	})
}
