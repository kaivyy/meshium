package file

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// validFileMode is a regex that matches valid octal file permission modes (e.g., "644", "755", "0644").
var validFileMode = regexp.MustCompile(`^[0-7]{3,4}$`)

// validateFileMode returns an error if mode is non-empty and not a valid octal mode.
func validateFileMode(mode string) error {
	if mode == "" {
		return nil
	}
	if !validFileMode.MatchString(mode) {
		return fmt.Errorf("invalid file mode: %s (must be 3-4 octal digits, e.g., 0644)", mode)
	}
	return nil
}

// Service provides file operations on remote servers via SSH.
type Service struct {
	srvRepo server.Repo
	pool    transport.ConnectionPool
	authSvc transport.AESKeyProvider
	hosts   transport.HostKeyStore
}

// NewService creates a new file service.
func NewService(
	srvRepo server.Repo,
	pool transport.ConnectionPool,
	authSvc transport.AESKeyProvider,
	hosts transport.HostKeyStore,
) *Service {
	return &Service{
		srvRepo: srvRepo,
		pool:    pool,
		authSvc: authSvc,
		hosts:   hosts,
	}
}

// getSSHClient obtains an SSH connection for the given server.
func (s *Service) getSSHClient(serverID int, srv *server.Server) (transport.SSHExecuter, error) {
	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return nil, fmt.Errorf("app is locked")
	}

	sshConfig := modssh.ServerConfig{
		ID:       srv.ID,
		Host:     srv.Host,
		Port:     srv.Port,
		Username: srv.Username,
	}

	// Decrypt credentials
	if srv.Password != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.Password))
		if err != nil {
			return nil, fmt.Errorf("decrypt password: %w", err)
		}
		sshConfig.Password = string(decrypted)
	}
	if srv.SSHKey != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.SSHKey))
		if err != nil {
			return nil, fmt.Errorf("decrypt ssh key: %w", err)
		}
		sshConfig.PrivateKey = decrypted
	}
	if srv.Passphrase != "" {
		decrypted, err := shared.Decrypt(aesKey, []byte(srv.Passphrase))
		if err != nil {
			return nil, fmt.Errorf("decrypt passphrase: %w", err)
		}
		sshConfig.Passphrase = string(decrypted)
	}

	// Resolve bastion if configured
	if srv.BastionID != 0 {
		bastion, err := s.srvRepo.GetByID(srv.BastionID)
		if err == nil {
			sshConfig.Bastion = s.buildBastionConfig(bastion, aesKey)
		}
	}

	hostKeyCallback := s.hosts.MakeHostKeyCallback(serverID)
	return s.pool.Get(serverID, sshConfig, hostKeyCallback)
}

func (s *Service) buildBastionConfig(bastion *server.Server, aesKey []byte) *modssh.BastionConfig {
	bastionPort := bastion.Port
	if bastionPort == 0 {
		bastionPort = 22
	}

	cfg := &modssh.BastionConfig{
		Host:     bastion.Host,
		Port:     bastionPort,
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

// ListDirectory lists the contents of a directory on a remote server.
func (s *Service) ListDirectory(ctx context.Context, serverID int, path string, showHidden bool) ([]FileInfo, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	// Use ls -la for listing
	lsCmd := fmt.Sprintf("ls -la --time-style=+%%s %s", shellEscape(path))
	if showHidden {
		lsCmd = fmt.Sprintf("ls -la --time-style=+%%s %s", shellEscape(path))
	}

	stdout, stderr, _, err := sshClient.ExecContext(ctx, lsCmd)
	if err != nil {
		if strings.Contains(stderr, "No such file") || strings.Contains(stderr, "not a directory") {
			return nil, fmt.Errorf("directory not found: %s", path)
		}
		return nil, fmt.Errorf("ls failed: %w (stderr: %s)", err, stderr)
	}

	return parseLSOutput(stdout, path), nil
}

// parseLSOutput parses the output of ls -la command.
func parseLSOutput(output, basePath string) []FileInfo {
	lines := strings.Split(output, "\n")
	var files []FileInfo

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}

		// Parse ls -la --time-style=+%s output format:
		// drwxr-xr-x  2 root root 4096 1234567890 dirname
		// -rw-r--r--  1 root root 1234 1234567890 filename
		// lrwxrwxrwx  1 root root 7    1234567890 link -> target
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		mode := fields[0]
		// nlink := fields[1]
		owner := fields[2]
		group := fields[3]
		size, _ := strconv.ParseInt(fields[4], 10, 64)

		// With --time-style=+%s, fields[5] is the timestamp (seconds since epoch)
		timestamp, _ := strconv.ParseInt(fields[5], 10, 64)
		modTime := time.Unix(timestamp, 0)

		// Name is everything after the timestamp
		name := strings.Join(fields[6:], " ")

		// Skip . and .. entries
		if name == "." || name == ".." {
			continue
		}

		isDir := strings.HasPrefix(mode, "d")
		isSymlink := strings.HasPrefix(mode, "l")
		var linkTarget string
		if isSymlink {
			// Parse symlink target (name -> target)
			parts := strings.SplitN(name, " -> ", 2)
			if len(parts) == 2 {
				name = parts[0]
				linkTarget = parts[1]
			}
		}

		fullPath := filepath.Join(basePath, name)

		files = append(files, FileInfo{
			Name:       name,
			Path:       fullPath,
			IsDir:      isDir,
			Size:       size,
			Mode:       mode,
			ModTime:    modTime,
			Owner:      owner,
			Group:      group,
			IsSymlink:  isSymlink,
			LinkTarget: linkTarget,
		})
	}

	return files
}

// ReadFile reads the content of a file on a remote server.
func (s *Service) ReadFile(ctx context.Context, serverID int, path string, maxSize int64) (*ReadFileResponse, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	// Check file size and type
	statCmd := fmt.Sprintf("stat -c '%%s %%F' %s 2>/dev/null || echo '0 unknown'", shellEscape(path))
	stdout, stderr, _, err := sshClient.ExecContext(ctx, statCmd)
	if err != nil {
		return nil, fmt.Errorf("stat failed: %w (stderr: %s)", err, stderr)
	}

	statParts := strings.Fields(strings.TrimSpace(stdout))
	if len(statParts) < 2 {
		return nil, fmt.Errorf("cannot stat file")
	}

	size, _ := strconv.ParseInt(statParts[0], 10, 64)
	fileType := strings.Join(statParts[1:], " ")

	if strings.Contains(fileType, "directory") {
		return nil, fmt.Errorf("cannot read directory")
	}

	// Detect MIME type
	fileCmd := fmt.Sprintf("file --mime-type -b %s", shellEscape(path))
	mimeType, _, _, _ := sshClient.ExecContext(ctx, fileCmd)
	mimeType = strings.TrimSpace(mimeType)

	isBinary := !strings.HasPrefix(mimeType, "text/") &&
		!strings.HasPrefix(mimeType, "application/json") &&
		!strings.HasPrefix(mimeType, "application/xml") &&
		!strings.HasPrefix(mimeType, "application/javascript")

	if maxSize <= 0 {
		maxSize = 100 * 1024 // 100KB default
	}

	var content string
	var truncated bool
	var lineCount int

	if isBinary {
		if size > 1024*1024 {
			return &ReadFileResponse{
				Path:      path,
				Content:   "",
				IsBinary:  true,
				Size:      size,
				MimeType:  mimeType,
				Truncated: true,
			}, nil
		}
		readSize := size
		if readSize > maxSize {
			readSize = maxSize
			truncated = true
		}
		catCmd := fmt.Sprintf("base64 %s | head -c %d", shellEscape(path), readSize*4/3+10)
		stdout, stderr, _, err = sshClient.ExecContext(ctx, catCmd)
		if err != nil {
			return nil, fmt.Errorf("read file failed: %w (stderr: %s)", err, stderr)
		}
		content = stdout
	} else {
		maxLines := 10000
		catCmd := fmt.Sprintf("head -n %d %s", maxLines, shellEscape(path))
		stdout, stderr, _, err = sshClient.ExecContext(ctx, catCmd)
		if err != nil {
			return nil, fmt.Errorf("read file failed: %w (stderr: %s)", err, stderr)
		}
		content = stdout
		lineCount = strings.Count(content, "\n")
		if lineCount >= maxLines-1 {
			truncated = true
		}
	}

	return &ReadFileResponse{
		Path:      path,
		Content:   content,
		IsBinary:  isBinary,
		Size:      size,
		MimeType:  mimeType,
		Encoding:  "utf-8",
		LineCount: lineCount,
		Truncated: truncated,
		MaxLines:  10000,
	}, nil
}

// WriteFile writes content to a file on a remote server.
func (s *Service) WriteFile(ctx context.Context, serverID int, req WriteFileRequest) error {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}

	// Check if file exists
	checkCmd := fmt.Sprintf("test -e %s", shellEscape(req.Path))
	_, _, _, err = sshClient.ExecContext(ctx, checkCmd)
	if err == nil && !req.Overwrite {
		return fmt.Errorf("file already exists")
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(req.Path)
	mkdirCmd := fmt.Sprintf("mkdir -p %s", shellEscape(parentDir))
	if _, stderr, _, err := sshClient.ExecContext(ctx, mkdirCmd); err != nil {
		return fmt.Errorf("create parent directory failed: %w (stderr: %s)", err, stderr)
	}

	// Write content using base64
	encodedContent := base64.StdEncoding.EncodeToString([]byte(req.Content))
	writeCmd := fmt.Sprintf("echo '%s' | base64 -d > %s", encodedContent, shellEscape(req.Path))

	_, stderr, _, err := sshClient.ExecContext(ctx, writeCmd)
	if err != nil {
		return fmt.Errorf("write file failed: %w (stderr: %s)", err, stderr)
	}

	// Set mode if specified
	if req.Mode != "" {
		if err := validateFileMode(req.Mode); err != nil {
			return err
		}
		chmodCmd := fmt.Sprintf("chmod %s %s", req.Mode, shellEscape(req.Path))
		if _, stderr, _, err := sshClient.ExecContext(ctx, chmodCmd); err != nil {
			fmt.Printf("warning: chmod failed: %s\n", stderr)
		}
	}

	return nil
}

// Delete deletes a file or directory on a remote server.
func (s *Service) Delete(ctx context.Context, serverID int, req DeleteRequest) error {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}

	var rmCmd string
	if req.Recursive {
		rmCmd = fmt.Sprintf("rm -rf %s", shellEscape(req.Path))
	} else {
		rmCmd = fmt.Sprintf("rm -f %s", shellEscape(req.Path))
	}

	_, stderr, _, err := sshClient.ExecContext(ctx, rmCmd)
	if err != nil {
		return fmt.Errorf("delete failed: %w (stderr: %s)", err, stderr)
	}

	return nil
}

// Rename renames or moves a file on a remote server.
func (s *Service) Rename(ctx context.Context, serverID int, req RenameRequest) error {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}

	parentDir := filepath.Dir(req.NewPath)
	mkdirCmd := fmt.Sprintf("mkdir -p %s", shellEscape(parentDir))
	if _, stderr, _, err := sshClient.ExecContext(ctx, mkdirCmd); err != nil {
		return fmt.Errorf("create parent directory failed: %w (stderr: %s)", err, stderr)
	}

	mvCmd := fmt.Sprintf("mv %s %s", shellEscape(req.OldPath), shellEscape(req.NewPath))
	_, stderr, _, err := sshClient.ExecContext(ctx, mvCmd)
	if err != nil {
		return fmt.Errorf("rename failed: %w (stderr: %s)", err, stderr)
	}

	return nil
}

// Mkdir creates a directory on a remote server.
func (s *Service) Mkdir(ctx context.Context, serverID int, req MkdirRequest) error {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}

	var mkdirCmd string
	if req.Mode != "" {
		if err := validateFileMode(req.Mode); err != nil {
			return err
		}
		mkdirCmd = fmt.Sprintf("mkdir -p -m %s %s", req.Mode, shellEscape(req.Path))
	} else {
		mkdirCmd = fmt.Sprintf("mkdir -p %s", shellEscape(req.Path))
	}

	_, stderr, _, err := sshClient.ExecContext(ctx, mkdirCmd)
	if err != nil {
		return fmt.Errorf("mkdir failed: %w (stderr: %s)", err, stderr)
	}

	return nil
}

// Stat returns information about a file on a remote server.
func (s *Service) Stat(ctx context.Context, serverID int, path string) (*FileInfo, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	statCmd := fmt.Sprintf("stat -c '%%n|%%F|%%s|%%Y|%%a|%%U|%%G' %s 2>/dev/null || echo 'NOT_FOUND'", shellEscape(path))
	stdout, stderr, _, err := sshClient.ExecContext(ctx, statCmd)
	if err != nil {
		return nil, fmt.Errorf("stat failed: %w (stderr: %s)", err, stderr)
	}

	output := strings.TrimSpace(stdout)
	if output == "NOT_FOUND" {
		return nil, fmt.Errorf("file not found")
	}

	parts := strings.Split(output, "|")
	if len(parts) < 7 {
		return nil, fmt.Errorf("cannot parse stat output")
	}

	fullPath := parts[0]
	fileType := parts[1]
	size, _ := strconv.ParseInt(parts[2], 10, 64)
	mtime, _ := strconv.ParseInt(parts[3], 10, 64)
	mode := parts[4]
	owner := parts[5]
	group := parts[6]

	name := filepath.Base(fullPath)
	isDir := strings.Contains(strings.ToLower(fileType), "directory")
	isSymlink := strings.Contains(strings.ToLower(fileType), "symbolic") || strings.Contains(strings.ToLower(fileType), "link")

	return &FileInfo{
		Name:      name,
		Path:      path,
		IsDir:     isDir,
		Size:      size,
		Mode:      mode,
		ModTime:   time.Unix(mtime, 0),
		Owner:     owner,
		Group:     group,
		IsSymlink: isSymlink,
	}, nil
}

// DownloadFile returns a file's content for download.
func (s *Service) DownloadFile(ctx context.Context, serverID int, path string) ([]byte, string, error) {
	srv, err := s.srvRepo.GetByID(serverID)
	if err != nil {
		return nil, "", fmt.Errorf("server not found: %w", err)
	}

	sshClient, err := s.getSSHClient(serverID, srv)
	if err != nil {
		return nil, "", fmt.Errorf("SSH connection failed: %w", err)
	}

	catCmd := fmt.Sprintf("base64 %s", shellEscape(path))
	stdout, stderr, _, err := sshClient.ExecContext(ctx, catCmd)
	if err != nil {
		return nil, "", fmt.Errorf("download failed: %w (stderr: %s)", err, stderr)
	}

	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		return nil, "", fmt.Errorf("decode failed: %w", err)
	}

	filename := filepath.Base(path)

	return content, filename, nil
}

// shellEscape escapes a path for safe shell usage.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
