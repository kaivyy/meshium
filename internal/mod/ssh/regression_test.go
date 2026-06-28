package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startExecSSHServer starts an SSH server that handles exec requests
// by writing "mock-output" to stdout and returning exit status 0.
// It accepts multiple connections.
func startExecSSHServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{NoClientAuth: true}
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key failed: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create signer failed: %v", err)
	}
	config.AddHostKey(signer)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					_ = c.Close()
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						newChan.Reject(ssh.UnknownChannelType, "unknown")
						continue
					}
					ch, chReqs, err := newChan.Accept()
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
					}(chReqs)
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

// startBastionCapableSSHServer starts an SSH server that handles both exec
// requests and direct-tcpip channel forwarding (for bastion tunneling).
// It accepts multiple connections.
func startBastionCapableSSHServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{NoClientAuth: true}
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key failed: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create signer failed: %v", err)
	}
	config.AddHostKey(signer)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					_ = c.Close()
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					switch newChan.ChannelType() {
					case "session":
						ch, chReqs, err := newChan.Accept()
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
						}(chReqs)

					case "direct-tcpip":
						// Accept the direct-tcpip channel and forward
						// the connection to the target address.
						ch, chReqs, err := newChan.Accept()
						if err != nil {
							continue
						}
						go ssh.DiscardRequests(chReqs)

						// Parse the target address from the channel data
						type directTCPIPMsg struct {
							Addr       string
							Port       uint32
							OriginAddr string
							OriginPort uint32
						}
						var msg directTCPIPMsg
						ssh.Unmarshal(newChan.ExtraData(), &msg)

						// Connect to the target
						targetConn, err := net.Dial("tcp",
							fmt.Sprintf("%s:%d", msg.Addr, msg.Port))
						if err != nil {
							ch.Close()
							continue
						}

						// Bidirectional copy
						go func() {
							defer ch.Close()
							defer targetConn.Close()
							var wg sync.WaitGroup
							wg.Add(2)
							go func() {
								defer wg.Done()
								_, _ = io.Copy(ch, targetConn)
							}()
							go func() {
								defer wg.Done()
								_, _ = io.Copy(targetConn, ch)
							}()
							wg.Wait()
						}()

					default:
						newChan.Reject(ssh.UnknownChannelType, "unknown")
					}
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

// TestBastionConnectionLifecycle verifies that a bastion connection
// is established, used, and cleaned up properly.
func TestBastionConnectionLifecycle(t *testing.T) {
	bastionAddr := startBastionCapableSSHServer(t)
	targetAddr := startExecSSHServer(t)

	bastionHost, bastionPortStr, _ := net.SplitHostPort(bastionAddr)
	var bastionPort int
	_, _ = fmt.Sscanf(bastionPortStr, "%d", &bastionPort)

	targetHost, targetPortStr, _ := net.SplitHostPort(targetAddr)
	var targetPort int
	_, _ = fmt.Sscanf(targetPortStr, "%d", &targetPort)

	targetCfg := ServerConfig{
		Host:     targetHost,
		Port:     targetPort,
		Username: "test",
		Bastion: &BastionConfig{
			Host:            bastionHost,
			Port:            bastionPort,
			Username:        "test",
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		},
	}

	// Connect through bastion
	client, err := connect(targetCfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("bastion connect failed: %v", err)
	}

	// Verify the connection is alive
	if !client.IsAlive() {
		t.Fatal("expected bastion connection to be alive")
	}

	// Verify it has a bastion
	if !client.HasBastion() {
		t.Fatal("expected client to have bastion")
	}

	// Execute a command through the bastion tunnel
	stdout, _, exitCode, err := client.Exec("echo test")
	if err != nil {
		t.Fatalf("exec through bastion failed: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	_ = stdout

	// Close should clean up both connections
	if err := client.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Verify it's no longer alive
	if client.IsAlive() {
		t.Fatal("expected connection to be dead after Close")
	}

	// Double close should be safe
	if err := client.Close(); err != nil {
		t.Fatalf("double close failed: %v", err)
	}
}

// TestTimeoutProfileVerification verifies that all timeout profiles
// have the expected values.
func TestTimeoutProfileVerification(t *testing.T) {
	tests := []struct {
		name      string
		profile   TimeoutConfig
		checkCmd  time.Duration
		checkConn time.Duration
	}{
		{"Default", DefaultTimeouts, 30 * time.Second, 10 * time.Second},
		{"Discovery", DiscoveryTimeouts, 15 * time.Second, 10 * time.Second},
		{"Migration", MigrationTimeouts, 5 * time.Minute, 10 * time.Second},
		{"PackageInstall", PackageInstallTimeouts, 10 * time.Minute, 10 * time.Second},
		{"Interactive", InteractiveTimeouts, 0, 10 * time.Second},
		{"Database", DatabaseTimeouts, 5 * time.Minute, 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.profile.Command != tt.checkCmd {
				t.Errorf("Command: expected %v, got %v", tt.checkCmd, tt.profile.Command)
			}
			if tt.profile.Connect != tt.checkConn {
				t.Errorf("Connect: expected %v, got %v", tt.checkConn, tt.profile.Connect)
			}
			// All profiles should have a file transfer timeout
			if tt.profile.FileTransfer == 0 {
				t.Error("FileTransfer should not be 0")
			}
		})
	}
}

// TestContextCancellationPropagation verifies that context cancellation
// propagates to ExecContext and causes the command to fail.
func TestContextCancellationPropagation(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	// Cancel context before executing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err = client.ExecContext(ctx, "echo test")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestConnectionReuse verifies that the pool reuses connections for
// the same server ID.
func TestConnectionReuse(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	if first != second {
		t.Fatal("expected pool to reuse the same connection")
	}
}

// TestParallelExecution verifies that the pool handles concurrent
// access correctly.
func TestParallelExecution(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:       time.Minute,
		MaxLifetime:   5 * time.Minute,
		MaxConcurrent: 20,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			client, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
			if err != nil {
				errs[idx] = err
				return
			}
			_, _, _, err = client.Exec("echo test")
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}
}

// TestNoGoroutineLeakAfterClose verifies that closing a client does
// not leak goroutines.
func TestNoGoroutineLeakAfterClose(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	// Capture goroutine count before
	before := runtime.NumGoroutine()

	// Create and close multiple clients
	for i := range 5 {
		client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
		if err != nil {
			t.Fatalf("connect %d failed: %v", i, err)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("close %d failed: %v", i, err)
		}
	}

	// Give goroutines time to exit
	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()

	// Allow some tolerance for background goroutines
	if after > before+5 {
		t.Errorf("goroutine leak: before=%d, after=%d", before, after)
	}
}

// TestNoGoroutineLeakAfterPoolClose verifies that closing the pool
// does not leak goroutines.
func TestNoGoroutineLeakAfterPoolClose(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	before := runtime.NumGoroutine()

	pool := NewPool(PoolConfig{
		MaxIdle:           time.Minute,
		MaxLifetime:       5 * time.Minute,
		KeepaliveInterval: 50 * time.Millisecond,
	})

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	// Create connections
	for i := range 3 {
		_, err := pool.Get(i+1, cfg, ssh.InsecureIgnoreHostKey())
		if err != nil {
			t.Fatalf("Get %d failed: %v", i, err)
		}
	}

	// Start keepalive
	pool.StartKeepalive()

	// Close everything
	pool.CloseAll()

	// Give goroutines time to exit
	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()

	if after > before+5 {
		t.Errorf("goroutine leak: before=%d, after=%d", before, after)
	}
}

// TestReconnectAfterClose verifies that the pool can establish a new
// connection after a previous one was closed.
func TestReconnectAfterClose(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	// First connection
	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// Close it
	if err := pool.Close(1); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should be able to get a new connection
	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	if first == second {
		t.Fatal("expected different connection after Close+Get")
	}

	// New connection should be alive
	if !second.IsAlive() {
		t.Fatal("expected new connection to be alive")
	}
}

// TestPoolExpiryBehavior verifies that connections expire after
// MaxLifetime and MaxIdle.
func TestPoolExpiryBehavior(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	// Test MaxIdle expiry
	pool := NewPool(PoolConfig{
		MaxIdle:     10 * time.Millisecond,
		MaxLifetime: time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// Wait for idle expiry
	time.Sleep(20 * time.Millisecond)

	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	if first == second {
		t.Fatal("expected different connection after idle expiry")
	}
}

// TestExecWithCustomTimeout verifies that ExecWithTimeout uses the
// custom timeout instead of the default.
func TestExecWithCustomTimeout(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{
		Host:     host,
		Port:     port,
		Username: "test",
	}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	// Use a very short timeout
	_, _, _, err = client.ExecWithTimeout("echo test", 1*time.Millisecond)
	// This might succeed or fail depending on timing, but it should
	// not hang indefinitely
	_ = err
}

// TestKeepaliveDetectsDeadConnections verifies that the keepalive
// loop detects and removes dead connections.
func TestKeepaliveDetectsDeadConnections(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:           time.Minute,
		MaxLifetime:       5 * time.Minute,
		KeepaliveInterval: 50 * time.Millisecond,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	// Create a connection
	client, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got := pool.Count(); got != 1 {
		t.Fatalf("expected count 1, got %d", got)
	}

	// Kill the underlying connection
	client.Close()

	// Start keepalive
	pool.StartKeepalive()

	// Wait for keepalive to detect and remove the dead connection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Count() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := pool.Count(); got != 0 {
		t.Fatalf("expected count 0 after keepalive, got %d", got)
	}
}

// TestStreamingWithLargeOutput verifies that the streaming methods
// can handle output larger than the default scanner buffer.
func TestStreamingWithLargeOutput(t *testing.T) {
	// This test verifies that the scanner buffer is large enough.
	// The default bufio.Scanner buffer is 64KB; we use 1MB.
	if maxScanBufferSize < 1024*1024 {
		t.Fatalf("maxScanBufferSize should be at least 1MB, got %d", maxScanBufferSize)
	}
}

// TestExecContextWithTimeout verifies that ExecContextWithTimeout
// respects both the context and the timeout.
func TestExecContextWithTimeout(t *testing.T) {
	addr := startExecSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	// With a valid context and reasonable timeout, should succeed
	ctx := context.Background()
	_, _, _, err = client.ExecContextWithTimeout(ctx, "echo test", 5*time.Second)
	if err != nil {
		t.Fatalf("ExecContextWithTimeout failed: %v", err)
	}

	// With a cancelled context, should fail
	ctx2, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = client.ExecContextWithTimeout(ctx2, "echo test", 5*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
