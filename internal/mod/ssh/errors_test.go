package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestIsTransientErrorTyped checks that isTransientError correctly identifies
// typed errors from the standard library.
func TestIsTransientErrorTyped(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"syscall.ECONNREFUSED", syscall.ECONNREFUSED, true},
		{"syscall.ECONNRESET", syscall.ECONNRESET, true},
		{"syscall.EPIPE", syscall.EPIPE, true},
		{"syscall.EAGAIN", syscall.EAGAIN, true},
		{"wrapped EOF", fmt.Errorf("wrapper: %w", io.EOF), true},
		{"wrapped ECONNREFUSED", fmt.Errorf("wrapper: %w", syscall.ECONNREFUSED), true},
		{"plain error", errors.New("authentication failed"), false},
		{"ssh handshake error", errors.New("ssh: handshake failed: ssh: unable to authenticate"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.err)
			if got != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// TestIsTransientErrorNetTimeout checks that net.Error with Timeout() is
// detected as transient.
func TestIsTransientErrorNetTimeout(t *testing.T) {
	// Create a timeout error
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// Set a very short deadline to trigger a timeout
	conn.SetDeadline(time.Now().Add(-1 * time.Second))

	// Read should timeout
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !isTransientError(err) {
		t.Errorf("expected timeout error to be transient, got false. Error: %v", err)
	}
}

// TestIsTransientErrorStringFallback checks that the string fallback
// still works for errors that don't unwrap to standard types.
func TestIsTransientErrorStringFallback(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{fmt.Errorf("connection refused"), true},
		{fmt.Errorf("connection reset by peer"), true},
		{fmt.Errorf("i/o timeout"), true},
		{fmt.Errorf("read: EOF"), true},
		{fmt.Errorf("broken pipe"), true},
		{fmt.Errorf("temporarily unavailable"), true},
		{fmt.Errorf("authentication failed"), false},
		{fmt.Errorf("no supported methods remain"), false},
	}

	for _, tt := range tests {
		got := isTransientError(tt.err)
		if got != tt.expected {
			t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.expected)
		}
	}
}

// TestSentinelErrors verifies that the sentinel errors are defined and
// can be used with errors.Is.
func TestSentinelErrors(t *testing.T) {
	if !errors.Is(ErrConnectionRefused, ErrConnectionRefused) {
		t.Fatal("ErrConnectionRefused should be identifiable with errors.Is")
	}
	if !errors.Is(ErrConnectionReset, ErrConnectionReset) {
		t.Fatal("ErrConnectionReset should be identifiable with errors.Is")
	}
	if !errors.Is(ErrConnectionTimeout, ErrConnectionTimeout) {
		t.Fatal("ErrConnectionTimeout should be identifiable with errors.Is")
	}
	if !errors.Is(ErrConnectionEOF, ErrConnectionEOF) {
		t.Fatal("ErrConnectionEOF should be identifiable with errors.Is")
	}
	if !errors.Is(ErrBrokenPipe, ErrBrokenPipe) {
		t.Fatal("ErrBrokenPipe should be identifiable with errors.Is")
	}
	if !errors.Is(ErrTemporarilyUnavailable, ErrTemporarilyUnavailable) {
		t.Fatal("ErrTemporarilyUnavailable should be identifiable with errors.Is")
	}
}

// TestGetContextCancellation verifies that GetContext returns immediately
// when the context is already cancelled.
func TestGetContextCancellation(t *testing.T) {
	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
		MaxRetries:  3,
	})
	defer pool.CloseAll()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := ServerConfig{Host: "127.0.0.1", Port: 1, Username: "test"}
	_, err := pool.GetContext(ctx, 1, cfg, ssh.InsecureIgnoreHostKey())
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

// TestGetContextCancelledDuringRetry verifies that the retry loop
// respects context cancellation during backoff.
func TestGetContextCancelledDuringRetry(t *testing.T) {
	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
		MaxRetries:  10, // high retry count to ensure we're in backoff
	})
	defer pool.CloseAll()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay to trigger cancellation during backoff
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	cfg := ServerConfig{Host: "127.0.0.1", Port: 1, Username: "test"}
	start := time.Now()
	_, err := pool.GetContext(ctx, 99, cfg, ssh.InsecureIgnoreHostKey())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	// Should return quickly after cancellation, not wait for full backoff
	if elapsed > 5*time.Second {
		t.Fatalf("took too long to return after cancellation: %v", elapsed)
	}
}

// TestGetContextSuccess verifies that GetContext works normally when
// the context is not cancelled.
func TestGetContextSuccess(t *testing.T) {
	addr := startPoolTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	pool := NewPool(PoolConfig{
		MaxIdle:     time.Minute,
		MaxLifetime: 5 * time.Minute,
	})
	defer pool.CloseAll()

	ctx := context.Background()
	cfg := ServerConfig{Host: host, Port: port, Username: "test"}
	client, err := pool.GetContext(ctx, 1, cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("GetContext failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
}

// TestGetContextConcurrentDedup verifies that concurrent GetContext calls
// for the same serverID are deduplicated.
func TestGetContextConcurrentDedup(t *testing.T) {
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
			clients[idx], errs[idx] = pool.GetContext(context.Background(), 1, cfg, ssh.InsecureIgnoreHostKey())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}

	if got := pool.Count(); got != 1 {
		t.Fatalf("expected pool count 1, got %d", got)
	}
}
