# Part 2: SSH Core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the SSH connection management layer — client wrapper, connection pool, key pair generation, and known hosts verification.

**Architecture:** SSH connections are managed by a thread-safe pool that reuses connections per server. The client wrapper provides exec, streaming exec, SFTP upload/download. Key pair management generates and stores the app's RSA 4096 key pair. Known hosts are stored in SQLite and verified on every connection.

**Tech Stack:** `golang.org/x/crypto/ssh`, `golang.org/x/crypto/rsa`, `github.com/pkg/sftp` (SFTP), `crypto/rand`

## Global Constraints

- SSH library: `golang.org/x/crypto/ssh`
- Key type: RSA 4096
- Connection timeout: 10s connect, 30s command
- Pool: max idle 10 min, max lifetime 30 min, keepalive 30s
- Ciphers: prefer `aes256-gcm@openssh.com`, `chacha20-poly1305@openssh.com`
- Known hosts stored in SQLite, not `~/.ssh/known_hosts`
- Auth priority: app key → server custom key → password

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/ssh/model.go` | SSH types (ServerConfig, ConnectResult) |
| `internal/mod/ssh/client.go` | SSH client wrapper (Exec, ExecStream, Upload, Download) |
| `internal/mod/ssh/pool.go` | Connection pool (Get, Close, CloseAll) |
| `internal/mod/ssh/keypair.go` | Key pair generation, encryption, installation |
| `internal/mod/ssh/knownhosts.go` | Known hosts store and verification |
| `internal/mod/ssh/pool_test.go` | Pool tests with mock SSH server |
| `internal/mod/ssh/keypair_test.go` | Key pair generation tests |

---

### Task 1: SSH Model

**Files:**
- Create: `internal/mod/ssh/model.go`

**Interfaces:**
- Produces: `ssh.ServerConfig`, `ssh.ConnectResult`, `ssh.HostKeyResult`

- [ ] **Step 1: Write SSH model**

```go
// internal/mod/ssh/model.go
package ssh

import "time"

// ServerConfig holds the connection parameters for a server.
type ServerConfig struct {
	ID         int
	Host       string
	Port       int
	Username   string
	Password   string
	PrivateKey []byte  // raw PEM bytes
	Passphrase string
}

// ConnectResult holds the result of a connection attempt.
type ConnectResult struct {
	Success    bool
	Latency    time.Duration
	Error      string
	HostKey    string
	NeedsVerify bool // true if host key not in known_hosts
}

// HostKeyResult holds the result of a host key check.
type HostKeyResult struct {
	Known    bool
	Match    bool
	Key      string
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mod/ssh/
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/ssh/model.go
git commit -m "feat: add SSH model types"
```

---

### Task 2: SSH Client Wrapper

**Files:**
- Create: `internal/mod/ssh/client.go`
- Create: `internal/mod/ssh/client_test.go`

**Interfaces:**
- Produces: `ssh.Client` struct with `Exec`, `ExecStream`, `Upload`, `Download`, `Close`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/ssh/client_test.go
package ssh

import (
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func startMockSSHServer(t *testing.T) (string, *ssh.ServerConfig) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer sshConn.Close()

		go ssh.DiscardRequests(reqs)

		for newChan := range chans {
			if newChan.ChannelType() != "session" {
				newChan.Reject(ssh.UnknownChannelType, "unknown")
				continue
			}
			ch, reqs, err := newChan.Accept()
			if err != nil {
				continue
			}
			go func(in <-chan *ssh.Request) {
				for req := range in {
					if req.Type == "exec" {
						req.Reply(true, nil)
						ch.Write([]byte("mock-output\n"))
						ch.Close()
						return
					}
					req.Reply(false, nil)
				}
			}(reqs)
		}
	}()

	return listener.Addr().String(), config
}

func TestExecCommand(t *testing.T) {
	addr, _ := startMockSSHServer(t)
	host, port, _ := net.SplitHostPort(addr)

	cfg := ServerConfig{
		Host:     host,
		Port:     func() int { p := 0; fmt.Sscanf(port, "%d", &p); return p }(),
		Username: "test",
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	stdout, _, exitCode, err := client.Exec("echo hello")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "mock-output") {
		t.Errorf("expected 'mock-output', got %q", stdout)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/ssh/ -v -run TestExec
```
Expected: FAIL — `connect` and `Client` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/ssh/client.go
package ssh

import (
	"bytes"
	"fmt"
	"io"
	"net"
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

	// Auth methods
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
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}
	sshConfig.Auth = authMethods

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:      conn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}, nil
}

// Exec runs a command and returns stdout, stderr, exit code.
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
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			return stdout.String(), stderr.String(), -1, err
		}
	}

	return stdout.String(), stderr.String(), exitCode, nil
}

// ExecStream runs a command and calls onOutput for each line of stdout.
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

	return session.Wait()
}

// Upload uploads a file via SFTP.
func (c *Client) Upload(src io.Reader, remotePath string) error {
	c.lastUsed = time.Now()
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return err
	}
	defer sc.Close()

	dst, err := sc.Create(remotePath)
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
	sc, err := sftp.NewClient(c.conn)
	if err != nil {
		return err
	}
	defer sc.Close()

	src, err := sc.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(dst, src)
	return err
}

// Close closes the SSH connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// IsAlive checks if the connection is still alive.
func (c *Client) IsAlive() bool {
	_, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil, nil)
	return err == nil
}
```

- [ ] **Step 4: Fix the test import and run**

The test needs `fmt` import. Update the test file:

```go
// Add to imports in client_test.go
import (
	"fmt"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)
```

And fix the port parsing:

```go
// Replace the port parsing in the test with:
func parsePort(addr string) int {
	_, port, _ := net.SplitHostPort(addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}
```

```bash
go test ./internal/mod/ssh/ -v -run TestExec
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add internal/mod/ssh/client.go internal/mod/ssh/client_test.go go.mod go.sum
git commit -m "feat: add SSH client wrapper (Exec, ExecStream, Upload, Download)"
```

---

### Task 3: Connection Pool

**Files:**
- Create: `internal/mod/ssh/pool.go`
- Create: `internal/mod/ssh/pool_test.go`

**Interfaces:**
- Consumes: `ssh.Client`, `ssh.connect`, `ssh.ServerConfig`
- Produces: `ssh.Pool` struct with `Get`, `Close`, `CloseAll`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/ssh/pool_test.go
package ssh

import (
	"testing"
	"time"
)

func TestPoolGetCreatesConnection(t *testing.T) {
	pool := NewPool(PoolConfig{
		MaxIdle:    1 * time.Minute,
		MaxLifetime: 5 * time.Minute,
	})

	// Pool should be empty initially
	if pool.Count() != 0 {
		t.Error("pool should be empty initially")
	}

	pool.CloseAll()
}

func TestPoolCloseAll(t *testing.T) {
	pool := NewPool(PoolConfig{
		MaxIdle:    1 * time.Minute,
		MaxLifetime: 5 * time.Minute,
	})

	pool.CloseAll()

	if pool.Count() != 0 {
		t.Error("pool should be empty after CloseAll")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/ssh/ -v -run TestPool
```
Expected: FAIL — `NewPool` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/ssh/pool.go
package ssh

import (
	"sync"
	"time"
)

type PoolConfig struct {
	MaxIdle    time.Duration
	MaxLifetime time.Duration
}

// Pool manages SSH connections per server.
type Pool struct {
	mu          sync.RWMutex
	connections map[int]*poolEntry
	config      PoolConfig
}

type poolEntry struct {
	client    *Client
	createdAt time.Time
	lastUsed  time.Time
}

func NewPool(cfg PoolConfig) *Pool {
	return &Pool{
		connections: make(map[int]*poolEntry),
		config:      cfg,
	}
}

// Get returns an existing connection or creates a new one.
func (p *Pool) Get(serverID int, cfg ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key ssh.PublicKey) error) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if we have an existing connection
	if entry, ok := p.connections[serverID]; ok {
		// Check if connection is still alive and not expired
		if entry.client.IsAlive() &&
			time.Since(entry.createdAt) < p.config.MaxLifetime &&
			time.Since(entry.client.lastUsed) < p.config.MaxIdle {
			return entry.client, nil
		}
		// Connection expired or dead, close and remove
		entry.client.Close()
		delete(p.connections, serverID)
	}

	// Create new connection
	client, err := connect(cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}

	p.connections[serverID] = &poolEntry{
		client:    client,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	return client, nil
}

// Close closes a specific server's connection.
func (p *Pool) Close(serverID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.connections[serverID]; ok {
		delete(p.connections, serverID)
		return entry.client.Close()
	}
	return nil
}

// CloseAll closes all connections.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, entry := range p.connections {
		entry.client.Close()
		delete(p.connections, id)
	}
}

// Count returns the number of active connections.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.connections)
}
```

- [ ] **Step 4: Fix import — pool.go needs `net` and `ssh`**

```go
// Add to imports in pool.go
import (
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mod/ssh/ -v -run TestPool
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mod/ssh/pool.go internal/mod/ssh/pool_test.go
git commit -m "feat: add SSH connection pool"
```

---

### Task 4: Key Pair Management

**Files:**
- Create: `internal/mod/ssh/keypair.go`
- Create: `internal/mod/ssh/keypair_test.go`

**Interfaces:**
- Consumes: `shared.Encrypt`, `shared.Decrypt`, `shared.DeriveKey`
- Produces: `ssh.GenerateKeyPair() → (privatePEM, publicSSH []byte, err)`, `ssh.InstallPublicKey(client, publicKey) → error`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/ssh/keypair_test.go
package ssh

import (
	"crypto/rsa"
	"strings"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	privatePEM, publicSSH, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if len(privatePEM) == 0 {
		t.Error("private key should not be empty")
	}
	if len(publicSSH) == 0 {
		t.Error("public key should not be empty")
	}

	// Public key should be in SSH format
	if !strings.HasPrefix(string(publicSSH), "ssh-rsa") {
		t.Error("public key should start with 'ssh-rsa'")
	}

	// Private key should be PEM format
	if !strings.Contains(string(privatePEM), "RSA PRIVATE KEY") {
		t.Error("private key should contain 'RSA PRIVATE KEY'")
	}
}

func TestGenerateKeyPairIsRSA4096(t *testing.T) {
	privatePEM, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Parse the private key to check key size
	key, err := ssh.ParseRawPrivateKey(privatePEM)
	if err != nil {
		t.Fatalf("ParseRawPrivateKey failed: %v", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("key should be RSA")
	}

	if rsaKey.N.BitLen() != 4096 {
		t.Errorf("expected 4096-bit key, got %d", rsaKey.N.BitLen())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/ssh/ -v -run TestGenerate
```
Expected: FAIL — `GenerateKeyPair` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/ssh/keypair.go
package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GenerateKeyPair generates an RSA 4096 key pair.
// Returns private key in PEM format and public key in SSH authorized_keys format.
func GenerateKeyPair() (privatePEM, publicSSH []byte, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	// Encode private key as PEM
	privateKeyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyDER,
	})

	// Encode public key as SSH authorized_keys format
	pubKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicSSH = ssh.MarshalAuthorizedKey(pubKey)

	return privatePEM, publicSSH, nil
}

// InstallPublicKey installs a public key on a remote server's authorized_keys.
func InstallPublicKey(client *Client, publicKey []byte) error {
	cmd := fmt.Sprintf(
		"mkdir -p ~/.ssh && echo '%s' >> ~/.ssh/authorized_keys && chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys",
		string(publicKey),
	)
	_, stderr, exitCode, err := client.Exec(cmd)
	if err != nil {
		return fmt.Errorf("failed to install public key: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("install public key failed: %s", stderr)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/ssh/ -v -run TestGenerate
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mod/ssh/keypair.go internal/mod/ssh/keypair_test.go
git commit -m "feat: add SSH key pair generation and installation"
```

---

### Task 5: Known Hosts Verification

**Files:**
- Create: `internal/mod/ssh/knownhosts.go`
- Create: `internal/mod/ssh/knownhosts_test.go`

**Interfaces:**
- Consumes: `*sql.DB`
- Produces: `ssh.KnownHostsStore` with `Get(host, port) → key, known`, `Save(host, port, key, serverID) → error`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/ssh/knownhosts_test.go
package ssh

import (
	"database/sql"
	"testing"

	"meshium/internal/db"
)

func TestKnownHostsSaveAndGet(t *testing.T) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()
	db.Migrate(d)

	store := NewKnownHostsStore(d)

	// Should not exist initially
	_, known, err := store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if known {
		t.Error("host should not be known initially")
	}

	// Save a host key
	err = store.Save("example.com", 22, "ssh-rsa AAAA...", 1)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Should exist now
	key, known, err := store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !known {
		t.Error("host should be known after Save")
	}
	if key != "ssh-rsa AAAA..." {
		t.Errorf("expected 'ssh-rsa AAAA...', got %q", key)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/ssh/ -v -run TestKnownHosts
```
Expected: FAIL — `NewKnownHostsStore` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/ssh/knownhosts.go
package ssh

import (
	"database/sql"
	"errors"
)

type KnownHostsStore struct {
	db *sql.DB
}

func NewKnownHostsStore(db *sql.DB) *KnownHostsStore {
	return &KnownHostsStore{db: db}
}

// Get returns the stored host key for a given host:port.
// Returns (key, true, nil) if found, ("", false, nil) if not found.
func (s *KnownHostsStore) Get(host string, port int) (string, bool, error) {
	var key string
	err := s.db.QueryRow(
		"SELECT host_key FROM known_hosts WHERE host = ? AND port = ?",
		host, port,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return key, true, err
}

// Save stores a host key for a given host:port.
func (s *KnownHostsStore) Save(host string, port int, key string, serverID int) error {
	_, err := s.db.Exec(
		`INSERT INTO known_hosts (server_id, host_key, host, port, verified)
		 VALUES (?, ?, ?, ?, 1)
		 ON CONFLICT(host, port) DO UPDATE SET host_key = ?, verified = 1`,
		serverID, key, host, port, key,
	)
	return err
}

// MakeHostKeyCallback returns an ssh.HostKeyCallback that verifies
// against stored known hosts. If the host is unknown, it returns
// ssh.ErrUnknownHostKey to signal the caller to prompt the user.
func (s *KnownHostsStore) MakeHostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		port := 22
		if tcpAddr, ok := remote.(*net.TCPAddr); ok {
			port = tcpAddr.Port
		}

		storedKey, known, err := s.Get(hostname, port)
		if err != nil {
			return err
		}

		if !known {
			return errors.New("host key not found — needs verification")
		}

		// Compare stored key with received key
		storedPubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(storedKey))
		if err != nil {
			return err
		}

		if !bytes.Equal(storedPubKey.Marshal(), key.Marshal()) {
			return errors.New("host key mismatch — possible MITM")
		}

		return nil
	}
}
```

- [ ] **Step 4: Fix imports**

```go
// Add to imports in knownhosts.go
import (
	"bytes"
	"database/sql"
	"errors"
	"net"

	"golang.org/x/crypto/ssh"
)
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/mod/ssh/ -v -run TestKnownHosts
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mod/ssh/knownhosts.go internal/mod/ssh/knownhosts_test.go
git commit -m "feat: add known hosts verification with SQLite storage"
```
