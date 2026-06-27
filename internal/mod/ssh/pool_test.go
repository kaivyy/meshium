package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func startPoolTestSSHServer(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key failed: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("create signer failed: %v", err)
	}

	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				defer sshConn.Close()

				go ssh.DiscardRequests(reqs)
				for range chans {
				}
			}(conn)
		}
	}()

	return listener.Addr().String()
}

func TestPoolGetCreatesConnectionAndReusesIt(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

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
	if first == nil {
		t.Fatal("expected client")
	}
	if got := pool.Count(); got != 1 {
		t.Fatalf("expected count 1 after first Get, got %d", got)
	}

	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}
	if second != first {
		t.Fatal("expected pool to reuse existing connection")
	}
	if got := pool.Count(); got != 1 {
		t.Fatalf("expected count 1 after reuse, got %d", got)
	}
}

func TestPoolSeparatesConnectionsByServerID(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("Get server 1 failed: %v", err)
	}
	second, err := pool.Get(2, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("Get server 2 failed: %v", err)
	}

	if first == second {
		t.Fatal("expected different connections for different server IDs")
	}
	if got := pool.Count(); got != 2 {
		t.Fatalf("expected count 2 with two server IDs, got %d", got)
	}
}

func TestPoolCloseAndCloseAll(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	if _, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey()); err != nil {
		t.Fatalf("Get server 1 failed: %v", err)
	}
	if _, err := pool.Get(2, cfg, ssh.InsecureIgnoreHostKey()); err != nil {
		t.Fatalf("Get server 2 failed: %v", err)
	}

	if err := pool.Close(1); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := pool.Count(); got != 1 {
		t.Fatalf("expected count 1 after Close, got %d", got)
	}

	pool.CloseAll()
	if got := pool.Count(); got != 0 {
		t.Fatalf("expected count 0 after CloseAll, got %d", got)
	}
}

func TestPoolExpiresIdleConnections(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	pool := NewPool(PoolConfig{
		MaxIdle:     5 * time.Millisecond,
		MaxLifetime: time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	first, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	second, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}
	if first == second {
		t.Fatal("expected expired idle connection to be replaced")
	}
}

func TestPoolCloseIdleRemovesExpiredConnections(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	pool := NewPool(PoolConfig{
		MaxIdle:     5 * time.Millisecond,
		MaxLifetime: time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	if _, err := pool.Get(1, cfg, ssh.InsecureIgnoreHostKey()); err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	pool.CloseIdle()

	if got := pool.Count(); got != 0 {
		t.Fatalf("expected count 0 after CloseIdle, got %d", got)
	}
}

func TestNewPoolAppliesDefaultExpiryLimits(t *testing.T) {
	pool := NewPool(PoolConfig{})

	if got, want := pool.config.MaxIdle, 10*time.Minute; got != want {
		t.Fatalf("expected default MaxIdle %v, got %v", want, got)
	}
	if got, want := pool.config.MaxLifetime, 30*time.Minute; got != want {
		t.Fatalf("expected default MaxLifetime %v, got %v", want, got)
	}
	if got, want := pool.config.MaxConcurrent, 10; got != want {
		t.Fatalf("expected default MaxConcurrent %d, got %d", want, got)
	}
}

func TestClientLastUsedConcurrentAccess(t *testing.T) {
	var c Client
	const goroutines = 16
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				c.touch()
				_ = c.LastUsed()
			}
		}()
	}
	wg.Wait()
}
