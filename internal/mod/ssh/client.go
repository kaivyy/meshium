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

const commandTimeout = 30 * time.Second

// connect establishes an SSH connection.
// If a bastion config is provided, the connection is tunneled through the bastion.
func connect(cfg ServerConfig, hostKeyCallback ssh.HostKeyCallback) (*Client, error) {
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
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
		Timeout:         10 * time.Second,
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
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	// Close the target connection first
	targetErr := c.conn.Close()

	// Close the bastion tunnel if present
	if c.bastionConn != nil {
		_ = c.bastionConn.Close()
	}

	return targetErr
}
