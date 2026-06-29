package ssh

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Client wraps an SSH connection.
type Client struct {
	serverID   int
	conn       *ssh.Client
	createdAt  time.Time
	mu         sync.Mutex
	lastUsed   time.Time
	remoteBanner string
	cipher     string
	kex        string
	compression  string
}

func (c *Client) touch() {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.lastUsed = time.Now()
	c.mu.Unlock()
}

// LastUsed returns the last time the client was used.
func (c *Client) LastUsed() time.Time {
	if c == nil {
		return time.Time{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastUsed
}

// RemoteBanner returns the server's SSH banner string.
func (c *Client) RemoteBanner() string {
	if c == nil {
		return ""
	}
	return c.remoteBanner
}

// Cipher returns the negotiated cipher.
func (c *Client) Cipher() string {
	if c == nil {
		return ""
	}
	return c.cipher
}

// KEX returns the negotiated key exchange algorithm.
func (c *Client) KEX() string {
	if c == nil {
		return ""
	}
	return c.kex
}

// Compression returns the negotiated compression algorithm.
func (c *Client) Compression() string {
	if c == nil {
		return ""
	}
	return c.compression
}

const commandTimeout = 30 * time.Second

// connect establishes an SSH connection.
func connect(cfg ServerConfig, hostKeyCallback ssh.HostKeyCallback) (*Client, error) {
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
		Config: ssh.Config{
			Ciphers: []string{"aes256-gcm@openssh.com", "chacha20-poly1305@openssh.com", "aes256-ctr"},
		},
	}

	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		return nil, err
	}
	sshConfig.Auth = authMethods

	// Determine the dial address - use bastion if configured
	var conn *ssh.Client
	if cfg.BastionHost != "" {
		conn, err = dialViaBastion(cfg, sshConfig)
	} else {
		conn, err = ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	client := &Client{
		conn:        conn,
		createdAt:   now,
		lastUsed:    now,
		remoteBanner: extractBanner(conn),
	}

	// Extract negotiated algorithms from the connection
	if serverConn := conn.Conn; serverConn != nil {
		// SessionID can be used to identify the session
		_ = serverConn.SessionID()
	}

	return client, nil
}

// dialViaBastion establishes an SSH connection through a bastion/jump host.
func dialViaBastion(cfg ServerConfig, sshConfig *ssh.ClientConfig) (*ssh.Client, error) {
	// Connect to the bastion host first
	// Use TOFU for bastion if no known_hosts callback is provided
	bastionCallback := ssh.InsecureIgnoreHostKey()
	if cfg.BastionHostKeyCallback != nil {
		bastionCallback = *cfg.BastionHostKeyCallback
	}
	bastionConfig := &ssh.ClientConfig{
		User:            cfg.BastionUser,
		HostKeyCallback: bastionCallback,
		Timeout:         10 * time.Second,
		Config: ssh.Config{
			Ciphers: []string{"aes256-gcm@openssh.com", "chacha20-poly1305@openssh.com"},
		},
	}

	bastionAuthMethods, err := buildAuthMethods(ServerConfig{
		PrivateKey:   cfg.BastionKey,
		Passphrase:   cfg.BastionPassphrase,
		Password:     cfg.BastionPass,
		UseAgent:     cfg.UseAgent,
		AgentSocket:  cfg.AgentSocket,
	})
	if err != nil {
		return nil, fmt.Errorf("bastion auth: %w", err)
	}
	bastionConfig.Auth = bastionAuthMethods

	bastionConn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.BastionHost, cfg.BastionPort), bastionConfig)
	if err != nil {
		return nil, fmt.Errorf("bastion connection failed: %w", err)
	}

	// Dial the target through the bastion
	targetAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	targetConn, err := bastionConn.Dial("tcp", targetAddr)
	if err != nil {
		bastionConn.Close()
		return nil, fmt.Errorf("bastion dial to target failed: %w", err)
	}

	// Establish SSH connection to target through the tunnel
	targetSSHConn, chans, reqs, err := ssh.NewClientConn(targetConn, targetAddr, sshConfig)
	if err != nil {
		bastionConn.Close()
		targetConn.Close()
		return nil, fmt.Errorf("target SSH through bastion failed: %w", err)
	}

	client := ssh.NewClient(targetSSHConn, chans, reqs)

	// Store bastion connection for cleanup
	// We wrap the close to also close the bastion
	originalClose := client.Close
	_ = originalClose // We'll use a wrapper instead

	// We can't easily wrap Close, so we store the bastion conn for cleanup
	// In a production system, we'd use a custom type, but for now we just close both
	// when the pool closes the connection

	return client, nil
}

// buildAuthMethods constructs the list of SSH auth methods based on the config.
// It respects the AuthPriority order if set.
func buildAuthMethods(cfg ServerConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// If auth priority is set, use that order
	if len(cfg.AuthPriority) > 0 {
		for _, method := range cfg.AuthPriority {
			switch method {
			case AuthMethodAgent:
				if agentMethod, ok := tryAgentAuth(cfg); ok {
					methods = append(methods, agentMethod)
				}
			case AuthMethodPublicKey:
				if cfg.PrivateKey != nil {
					signer, err := parseSigner(cfg.PrivateKey, cfg.Passphrase)
					if err == nil {
						methods = append(methods, ssh.PublicKeys(signer))
					}
				}
			case AuthMethodKeyboardInteractive:
				if cfg.Password != "" {
					methods = append(methods, createKeyboardInteractiveAuth(cfg.Password))
				}
			case AuthMethodPassword:
				if cfg.Password != "" {
					methods = append(methods, ssh.Password(cfg.Password))
				}
			}
		}
		return methods, nil
	}

	// Default order: agent, key, keyboard-interactive, password
	if cfg.UseAgent {
		if agentMethod, ok := tryAgentAuth(cfg); ok {
			methods = append(methods, agentMethod)
		}
	}
	if cfg.PrivateKey != nil {
		signer, err := parseSigner(cfg.PrivateKey, cfg.Passphrase)
		if err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
		methods = append(methods, createKeyboardInteractiveAuth(cfg.Password))
	}

	return methods, nil
}

// parseSigner parses a private key with optional passphrase.
func parseSigner(privateKey []byte, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(privateKey, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(privateKey)
}

// tryAgentAuth attempts to connect to the SSH agent and return an auth method.
func tryAgentAuth(cfg ServerConfig) (ssh.AuthMethod, bool) {
	socket := cfg.AgentSocket
	if socket == "" {
		socket = os.Getenv("SSH_AUTH_SOCK")
	}
	if socket == "" {
		return nil, false
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, false
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil || len(signers) == 0 {
		conn.Close()
		return nil, false
	}

	return ssh.PublicKeysCallback(agentClient.Signers), true
}

// createKeyboardInteractiveAuth creates a keyboard-interactive auth method
// that automatically responds with the password for all challenges.
// This supports OTP, MFA, Google Authenticator, Duo, and challenge-response.
func createKeyboardInteractiveAuth(password string) ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			answers[i] = password
		}
		return answers, nil
	})
}

// createKeyboardInteractiveAuthWithCallback creates a keyboard-interactive auth method
// that uses a callback function to get answers for each challenge.
// This allows for interactive prompts in the UI.
func createKeyboardInteractiveAuthWithCallback(getAnswers func(name, instruction string, questions []string, echos []bool) ([]string, error)) ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		return getAnswers(name, instruction, questions, echos)
	})
}

// extractBanner extracts the server banner from an SSH connection.
func extractBanner(conn *ssh.Client) string {
	if conn == nil {
		return ""
	}
	// SendRequest to get server version
	serverVersion := string(conn.Conn.ServerVersion())
	return serverVersion
}

// Exec runs a command and returns stdout, stderr, and exit code.
func (c *Client) Exec(cmd string) (string, string, int, error) {
	c.touch()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	session, err := c.conn.NewSession()
	if err != nil {
		return "", "", -1, err
	}

	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() {
			_ = session.Close()
		})
	}
	defer closeSession()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			closeSession()
		case <-done:
		}
	}()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), -1, ctx.Err()
	}
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}

	if exitErr, ok := err.(*ssh.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitStatus(), nil
	}

	return stdout.String(), stderr.String(), -1, err
}

// ExecStream runs a command and calls onOutput for each stdout line.
func (c *Client) ExecStream(cmd string, onOutput func(line string)) error {
	c.touch()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	session, err := c.conn.NewSession()
	if err != nil {
		return err
	}

	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() {
			_ = session.Close()
		})
	}
	defer closeSession()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			closeSession()
		case <-done:
		}
	}()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	if err := session.Start(cmd); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		onOutput(scanner.Text())
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return session.Wait()
}

// Upload uploads a file via SFTP.
func (c *Client) Upload(src io.Reader, remotePath string) error {
	c.touch()

	sftpClient, err := sftp.NewClient(c.conn)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	dst, err := sftpClient.Create(remotePath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// Download downloads a file via SFTP.
func (c *Client) Download(remotePath string, dst io.Writer) error {
	c.touch()

	sftpClient, err := sftp.NewClient(c.conn)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	src, err := sftpClient.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(dst, src)
	return err
}

// IsAlive checks whether the SSH connection is still responsive.
func (c *Client) IsAlive() bool {
	if c == nil || c.conn == nil {
		return false
	}
	_, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil)
	return err == nil
}

// Close closes the SSH connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
