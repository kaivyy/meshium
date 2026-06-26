package ssh

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client wraps an SSH connection.
type Client struct {
	serverID  int
	conn      *ssh.Client
	createdAt time.Time
	lastUsed  time.Time
}

// connect establishes an SSH connection.
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

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), sshConfig)
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

// Exec runs a command and returns stdout, stderr, and exit code.
func (c *Client) Exec(cmd string) (string, string, int, error) {
	c.lastUsed = time.Now()

	session, err := c.conn.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
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
	c.lastUsed = time.Now()

	session, err := c.conn.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}

	if err := session.Start(cmd); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		onOutput(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return session.Wait()
}

// Upload uploads a file via SFTP.
func (c *Client) Upload(src io.Reader, remotePath string) error {
	c.lastUsed = time.Now()

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
	c.lastUsed = time.Now()

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

// Close closes the SSH connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
