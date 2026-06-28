package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func parsePort(addr string) int {
	_, port, _ := net.SplitHostPort(addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}

// startMockSSHServer starts a minimal SSH server that responds to "exec"
// requests with "mock-output". It accepts a single connection.
func startMockSSHServer(t *testing.T) (string, *ssh.ServerConfig) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	config.AddHostKey(signer)

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
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
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
	host, _, _ := net.SplitHostPort(addr)

	cfg := ServerConfig{
		Host:     host,
		Port:     parsePort(addr),
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

// startBastionSSHServer starts an SSH server that supports direct-tcpip
// channel forwarding (acting as a bastion/jump host). It accepts a single
// connection and forwards direct-tcpip channels to the target address.
func startBastionSSHServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bastion listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate bastion host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create bastion signer: %v", err)
	}

	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)

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
			if newChan.ChannelType() != "direct-tcpip" {
				newChan.Reject(ssh.UnknownChannelType, "only direct-tcpip allowed")
				continue
			}

			d := struct {
				Addr       string
				Port       uint32
				OriginAddr string
				OriginPort uint32
			}{}
			if err := ssh.Unmarshal(newChan.ExtraData(), &d); err != nil {
				newChan.Reject(ssh.ConnectionFailed, "parse failed")
				continue
			}

			targetAddr := fmt.Sprintf("%s:%d", d.Addr, d.Port)
			targetConn, err := net.Dial("tcp", targetAddr)
			if err != nil {
				newChan.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}

			ch, _, err := newChan.Accept()
			if err != nil {
				_ = targetConn.Close()
				continue
			}

			go func() {
				defer ch.Close()
				defer targetConn.Close()
				go func() {
					defer ch.Close()
					io.Copy(ch, targetConn)
				}()
				io.Copy(targetConn, ch)
			}()
		}
	}()

	return listener.Addr().String()
}

// startMultiConnSSHServer starts an SSH server that handles multiple
// connections and responds to "exec" requests with "mock-output".
func startMultiConnSSHServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
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
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
								ch.Close()
								return
							}
							req.Reply(false, nil)
						}
					}(reqs)
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

// TestBastionConnectionStaysAliveAfterConnect verifies that the bastion
// SSH tunnel is NOT closed when connect() returns. Before the fix,
// defer bastionClient.Close() killed the tunnel immediately, making
// all bastion connections unusable.
func TestBastionConnectionStaysAliveAfterConnect(t *testing.T) {
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	cfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect through bastion failed: %v", err)
	}
	defer client.Close()

	// The connection must be usable after connect() returns.
	// Before the fix, the bastion was closed immediately, killing the tunnel.
	stdout, _, exitCode, err := client.Exec("echo hello")
	if err != nil {
		t.Fatalf("Exec through bastion failed (tunnel was killed): %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "mock-output") {
		t.Errorf("expected 'mock-output', got %q", stdout)
	}
}

// TestBastionHasBastionFlag verifies that HasBastion() returns true for
// bastion connections and false for direct connections.
func TestBastionHasBastionFlag(t *testing.T) {
	// Direct connection — HasBastion should be false
	directAddr := startMultiConnSSHServer(t)
	directHost, _, _ := net.SplitHostPort(directAddr)

	directCfg := ServerConfig{
		Host:     directHost,
		Port:     parsePort(directAddr),
		Username: "test",
	}
	directClient, err := connect(directCfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("direct connect failed: %v", err)
	}
	defer directClient.Close()

	if directClient.HasBastion() {
		t.Error("direct connection should not have bastion")
	}

	// Bastion connection — HasBastion should be true
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	bastionCfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}
	bastionClient, err := connect(bastionCfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("bastion connect failed: %v", err)
	}
	defer bastionClient.Close()

	if !bastionClient.HasBastion() {
		t.Error("bastion connection should have bastion flag set")
	}
}

// TestBastionMultipleCommands verifies that multiple commands can be
// executed over a single bastion-tunneled connection. Before the fix,
// the first command would succeed (during connect) but subsequent
// commands would fail because the bastion was already closed.
func TestBastionMultipleCommands(t *testing.T) {
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	cfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect through bastion failed: %v", err)
	}
	defer client.Close()

	// Execute multiple commands — all should succeed
	for i := 0; i < 3; i++ {
		stdout, _, exitCode, err := client.Exec("echo hello")
		if err != nil {
			t.Fatalf("Exec #%d through bastion failed: %v", i+1, err)
		}
		if exitCode != 0 {
			t.Errorf("Exec #%d: expected exit code 0, got %d", i+1, exitCode)
		}
		if !strings.Contains(stdout, "mock-output") {
			t.Errorf("Exec #%d: expected 'mock-output', got %q", i+1, stdout)
		}
	}
}

// TestBastionCloseClosesBothConnections verifies that Close() closes both
// the target and bastion connections.
func TestBastionCloseClosesBothConnections(t *testing.T) {
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	cfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect through bastion failed: %v", err)
	}

	// Verify it works before close
	_, _, _, err = client.Exec("echo hello")
	if err != nil {
		t.Fatalf("Exec before Close failed: %v", err)
	}

	// Close should close both connections
	if err := client.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// After close, Exec should fail
	_, _, _, err = client.Exec("echo hello")
	if err == nil {
		t.Error("Exec after Close should fail")
	}

	// IsAlive should return false
	if client.IsAlive() {
		t.Error("IsAlive should return false after Close")
	}
}

// TestBastionPoolReusesConnection verifies that the pool correctly
// reuses a bastion-tunneled connection across multiple Get calls.
func TestBastionPoolReusesConnection(t *testing.T) {
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	cfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	pool := NewPool(PoolConfig{})
	defer pool.CloseAll()

	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("first pool.Get through bastion failed: %v", err)
	}

	// Execute a command to verify the connection is alive
	_, _, _, err = first.Exec("echo hello")
	if err != nil {
		t.Fatalf("first Exec through bastion failed: %v", err)
	}

	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second pool.Get through bastion failed: %v", err)
	}

	if first != second {
		t.Fatal("pool should reuse the same bastion connection")
	}

	// Execute again to verify the reused connection still works
	_, _, _, err = second.Exec("echo hello")
	if err != nil {
		t.Fatalf("second Exec through reused bastion connection failed: %v", err)
	}
}

// TestBastionConcurrentAccess verifies that the bastion connection is
// safe for concurrent use.
func TestBastionConcurrentAccess(t *testing.T) {
	bastionAddr := startBastionSSHServer(t)
	targetAddr := startMultiConnSSHServer(t)

	bastionHost, _, _ := net.SplitHostPort(bastionAddr)
	targetHost, _, _ := net.SplitHostPort(targetAddr)

	cfg := ServerConfig{
		Host:     targetHost,
		Port:     parsePort(targetAddr),
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            parsePort(bastionAddr),
			Username:        "bastion",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect through bastion failed: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	const goroutines = 4
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _, _, err := client.Exec("echo hello")
			if err != nil {
				t.Errorf("concurrent Exec through bastion failed: %v", err)
			}
		}()
	}

	wg.Wait()
}
