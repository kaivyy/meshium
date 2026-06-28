package ssh

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client wraps an SSH connection.
// If the connection is tunneled through a bastion/jump host, bastionConn
// holds the bastion SSH client so its lifetime is tied to the target
// connection. Closing the Client closes both connections.
type Client struct {
	serverID    int
	conn        *ssh.Client
	bastionConn *ssh.Client // non-nil if connection is via bastion
	createdAt   time.Time
	timeouts    TimeoutConfig
	closed      bool // protected by mu
	mu          sync.Mutex
	lastUsed    time.Time
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

// connect establishes an SSH connection.
// If a bastion config is provided, the connection is tunneled through the bastion.
func connect(cfg ServerConfig, hostKeyCallback ssh.HostKeyCallback) (*Client, error) {
	timeouts := cfg.Timeouts.withDefaults()

	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeouts.Connect,
		Config: ssh.Config{
			Ciphers: []string{"aes256-gcm@openssh.com", "chacha20-poly1305@openssh.com"},
		},
	}

	var authMethods []ssh.AuthMethod
	if cfg.PrivateKey != nil {
		var signer ssh.Signer
		var err error
		if cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(cfg.PrivateKey, []byte(cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(cfg.PrivateKey)
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}
	sshConfig.Auth = authMethods

	targetAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	// If bastion is configured, tunnel through it
	if cfg.Bastion != nil {
		bastionClient, err := dialBastion(cfg.Bastion)
		if err != nil {
			return nil, fmt.Errorf("bastion connection failed: %w", err)
		}

		// Dial the target through the bastion
		conn, err := bastionClient.Dial("tcp", targetAddr)
		if err != nil {
			bastionClient.Close()
			return nil, fmt.Errorf("bastion dial target failed: %w", err)
		}

		// Establish SSH over the tunneled connection
		ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, sshConfig)
		if err != nil {
			conn.Close()
			bastionClient.Close()
			return nil, fmt.Errorf("target SSH through bastion failed: %w", err)
		}

		// Store the bastion client so it lives as long as the target
		// connection. The target connection is tunneled through the
		// bastion — closing the bastion would kill the tunnel.
		now := time.Now()
		return &Client{
			conn:        ssh.NewClient(ncc, chans, reqs),
			bastionConn: bastionClient,
			createdAt:   now,
			timeouts:    timeouts,
			lastUsed:     now,
		}, nil
	}

	// Direct connection (no bastion)
	conn, err := ssh.Dial("tcp", targetAddr, sshConfig)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &Client{
		conn:      conn,
		createdAt: now,
		timeouts:  timeouts,
		lastUsed:  now,
	}, nil
}

// dialBastion establishes a connection to the bastion/jump host.
func dialBastion(b *BastionConfig) (*ssh.Client, error) {
	if b.HostKeyCallback == nil {
		return nil, fmt.Errorf("bastion host key callback is required — refusing to connect without host key verification")
	}

	bastionConfig := &ssh.ClientConfig{
		User:            b.Username,
		HostKeyCallback: b.HostKeyCallback,
		Timeout:         DefaultTimeouts.Connect,
	}

	var authMethods []ssh.AuthMethod
	if b.PrivateKey != nil {
		var signer ssh.Signer
		var err error
		if b.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(b.PrivateKey, []byte(b.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(b.PrivateKey)
		}
		if err != nil {
			return nil, fmt.Errorf("parse bastion private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if b.Password != "" {
		authMethods = append(authMethods, ssh.Password(b.Password))
	}
	bastionConfig.Auth = authMethods

	bastionAddr := fmt.Sprintf("%s:%d", b.Host, b.Port)
	return ssh.Dial("tcp", bastionAddr, bastionConfig)
}

// Exec runs a command with the client's default command timeout and returns
// stdout, stderr, and exit code. It is a backward-compatible wrapper around
// ExecContextWithTimeout.
func (c *Client) Exec(cmd string) (string, string, int, error) {
	return c.ExecContextWithTimeout(context.Background(), cmd, c.timeouts.Command)
}

// ExecContext runs a command with the supplied parent context for cancellation.
// The client's default command timeout is applied in addition to the parent
// context — whichever fires first wins.
func (c *Client) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return c.ExecContextWithTimeout(ctx, cmd, c.timeouts.Command)
}

// ExecWithTimeout runs a command with a custom timeout. The parent context
// is background, so only the timeout can cancel the command.
func (c *Client) ExecWithTimeout(cmd string, timeout time.Duration) (string, string, int, error) {
	return c.ExecContextWithTimeout(context.Background(), cmd, timeout)
}

// ExecContextWithTimeout is the primary command execution method. It combines
// a parent context (for caller-driven cancellation) with a per-command timeout
// (for hung-session protection). Whichever fires first cancels the command.
// A timeout of 0 means no explicit timeout — only the parent context applies.
func (c *Client) ExecContextWithTimeout(ctx context.Context, cmd string, timeout time.Duration) (string, string, int, error) {
	c.touch()

	var execCtx context.Context
	var cancel context.CancelFunc

	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
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
		case <-execCtx.Done():
			closeSession()
		case <-done:
		}
	}()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
	if execCtx.Err() != nil {
		return stdout.String(), stderr.String(), -1, execCtx.Err()
	}
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}

	if exitErr, ok := err.(*ssh.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitStatus(), nil
	}

	return stdout.String(), stderr.String(), -1, err
}

// StreamSource indicates whether a streamed line came from stdout or stderr.
type StreamSource int

const (
	// StreamStdout indicates a line from stdout.
	StreamStdout StreamSource = iota
	// StreamStderr indicates a line from stderr.
	StreamStderr
)

// StreamCallback is called for each line of output during streaming execution.
// The source parameter indicates whether the line came from stdout or stderr.
type StreamCallback func(source StreamSource, line string)

// maxScanBufferSize is the maximum buffer size for the line scanner.
// The default bufio.Scanner buffer is 64KB which can be too small for
// lines with long JSON payloads or base64-encoded data.
const maxScanBufferSize = 1024 * 1024 // 1MB

// ExecStream runs a command and calls onOutput for each stdout line.
// It uses the client's default command timeout.
// Deprecated: use ExecStreamLines for stdout and stderr capture.
func (c *Client) ExecStream(cmd string, onOutput func(line string)) error {
	return c.ExecStreamContext(context.Background(), cmd, onOutput)
}

// ExecStreamContext runs a command with the supplied parent context for
// cancellation and calls onOutput for each stdout line.
// Deprecated: use ExecStreamLinesContext for stdout and stderr capture.
func (c *Client) ExecStreamContext(ctx context.Context, cmd string, onOutput func(line string)) error {
	return c.ExecStreamContextWithTimeout(ctx, cmd, c.timeouts.Command, onOutput)
}

// ExecStreamWithTimeout runs a command with a custom timeout and calls
// onOutput for each stdout line.
// Deprecated: use ExecStreamLinesWithTimeout for stdout and stderr capture.
func (c *Client) ExecStreamWithTimeout(cmd string, timeout time.Duration, onOutput func(line string)) error {
	return c.ExecStreamContextWithTimeout(context.Background(), cmd, timeout, onOutput)
}

// ExecStreamContextWithTimeout is the primary streaming execution method
// for the stdout-only callback API. It combines a parent context with a
// per-command timeout. For stderr capture, use ExecStreamLinesContextWithTimeout.
func (c *Client) ExecStreamContextWithTimeout(ctx context.Context, cmd string, timeout time.Duration, onOutput func(line string)) error {
	return c.ExecStreamLinesContextWithTimeout(ctx, cmd, timeout, func(source StreamSource, line string) {
		if source == StreamStdout {
			onOutput(line)
		}
	})
}

// ExecStreamLines runs a command and calls onOutput for each output line
// from both stdout and stderr. It uses the client's default command timeout.
func (c *Client) ExecStreamLines(cmd string, onOutput StreamCallback) error {
	return c.ExecStreamLinesContext(context.Background(), cmd, onOutput)
}

// ExecStreamLinesContext runs a command with the supplied parent context
// for cancellation and calls onOutput for each output line from both
// stdout and stderr.
func (c *Client) ExecStreamLinesContext(ctx context.Context, cmd string, onOutput StreamCallback) error {
	return c.ExecStreamLinesContextWithTimeout(ctx, cmd, c.timeouts.Command, onOutput)
}

// ExecStreamLinesWithTimeout runs a command with a custom timeout and
// calls onOutput for each output line from both stdout and stderr.
func (c *Client) ExecStreamLinesWithTimeout(cmd string, timeout time.Duration, onOutput StreamCallback) error {
	return c.ExecStreamLinesContextWithTimeout(context.Background(), cmd, timeout, onOutput)
}

// ExecStreamLinesContextWithTimeout is the primary streaming execution method
// that captures both stdout and stderr. It combines a parent context with a
// per-command timeout. Whichever fires first cancels the command.
// A timeout of 0 means no explicit timeout — only the parent context applies.
func (c *Client) ExecStreamLinesContextWithTimeout(ctx context.Context, cmd string, timeout time.Duration, onOutput StreamCallback) error {
	c.touch()

	var execCtx context.Context
	var cancel context.CancelFunc

	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
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
		case <-execCtx.Done():
			closeSession()
		case <-done:
		}
	}()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	if err := session.Start(cmd); err != nil {
		if execCtx.Err() != nil {
			return execCtx.Err()
		}
		return err
	}

	// Scan stderr in a goroutine so we can interleave stdout and stderr
	// without blocking on either pipe.
	stderrDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, maxScanBufferSize), maxScanBufferSize)
		for scanner.Scan() {
			onOutput(StreamStderr, scanner.Text())
		}
		stderrDone <- scanner.Err()
	}()

	// Scan stdout in the current goroutine.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, maxScanBufferSize), maxScanBufferSize)
	for scanner.Scan() {
		onOutput(StreamStdout, scanner.Text())
	}
	if execCtx.Err() != nil {
		return execCtx.Err()
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Wait for stderr scanner to finish.
	if err := <-stderrDone; err != nil {
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

// HasBastion returns true if this connection is tunneled through a bastion.
func (c *Client) HasBastion() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bastionConn != nil
}

// Close closes the SSH connection and any associated bastion connection.
// The target connection is closed first, then the bastion tunnel.
// It is safe to call Close multiple times — subsequent calls are no-ops.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Close the target connection first
	targetErr := c.conn.Close()

	// Close the bastion tunnel if present
	if c.bastionConn != nil {
		_ = c.bastionConn.Close()
	}

	return targetErr
}
