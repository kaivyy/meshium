package ssh

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestPoolDeduplicatesConcurrentConnectionAttempts verifies that when
// multiple goroutines request a connection for the same serverID
// simultaneously, only one connection is established.
func TestPoolDeduplicatesConcurrentConnectionAttempts(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})
	defer pool.CloseAll()

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	clients := make([]*Client, goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			clients[idx], errs[idx] = pool.Get(1, cfg, ssh.InsecureIgnoreHostKey())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
		if clients[i] == nil {
			t.Fatalf("goroutine %d got nil client", i)
		}
	}

	// All goroutines should get the same connection (or at least the
	// first one should be in the pool). Some may get different connections
	// if they arrived before the first one was stored, but the pool
	// should only have one entry.
	if got := pool.Count(); got != 1 {
		t.Fatalf("expected pool count 1 after concurrent Get, got %d", got)
	}
}

// TestClientCloseIsIdempotent verifies that calling Close multiple times
// is safe and does not panic.
func TestClientCloseIsIdempotent(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// Close multiple times — should not panic
	if err := client.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("third Close failed: %v", err)
	}
}

// TestClientCloseConcurrent verifies that concurrent Close calls are safe.
func TestClientCloseConcurrent(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = client.Close()
		}()
	}
	wg.Wait()
}

// TestKeepaliveRestartable verifies that the keepalive goroutine can be
// started, stopped, and started again.
func TestKeepaliveRestartable(t *testing.T) {
	pool := NewPool(PoolConfig{
		KeepaliveInterval: 100 * time.Millisecond,
	})
	defer pool.CloseAll()

	// Start
	pool.StartKeepalive()
	if !pool.keepaliveRunning {
		t.Fatal("expected keepaliveRunning to be true after StartKeepalive")
	}

	// Stop
	pool.StopKeepalive()
	if pool.keepaliveRunning {
		t.Fatal("expected keepaliveRunning to be false after StopKeepalive")
	}

	// Start again — should work with the new implementation
	pool.StartKeepalive()
	if !pool.keepaliveRunning {
		t.Fatal("expected keepaliveRunning to be true after restart")
	}

	// Stop again
	pool.StopKeepalive()
	if pool.keepaliveRunning {
		t.Fatal("expected keepaliveRunning to be false after second stop")
	}
}

// TestStopKeepaliveIdempotent verifies that StopKeepalive can be called
// multiple times without panicking.
func TestStopKeepaliveIdempotent(t *testing.T) {
	pool := NewPool(PoolConfig{
		KeepaliveInterval: 100 * time.Millisecond,
	})
	defer pool.CloseAll()

	// Stop without starting — should not panic
	pool.StopKeepalive()
	pool.StopKeepalive()

	// Start then stop multiple times
	pool.StartKeepalive()
	pool.StopKeepalive()
	pool.StopKeepalive()
}

// TestStartKeepaliveConcurrent verifies that concurrent StartKeepalive
// calls are safe.
func TestStartKeepaliveConcurrent(t *testing.T) {
	pool := NewPool(PoolConfig{
		KeepaliveInterval: 100 * time.Millisecond,
	})
	defer pool.CloseAll()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			pool.StartKeepalive()
		}()
	}
	wg.Wait()

	if !pool.keepaliveRunning {
		t.Fatal("expected keepaliveRunning to be true after concurrent StartKeepalive")
	}
}
