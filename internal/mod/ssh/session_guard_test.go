package ssh

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Opening an SSH session or an SFTP client is a round-trip to the peer. On a
// wedged transport (channel-open sent, no reply) those calls block with no
// deadline of their own — they sit OUTSIDE the per-command watchdog, so a
// "bounded" 30s command could hang indefinitely before its timeout ever
// started counting. newSessionContext bounds the open itself.

func TestNewSessionContextReturnsOnSuccess(t *testing.T) {
	want := "session"
	got, err := openWithContext(context.Background(), 2*time.Second, func() (interface{}, error) {
		return want, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNewSessionContextPropagatesError(t *testing.T) {
	sentinel := errors.New("channel open failed")
	_, err := openWithContext(context.Background(), 2*time.Second, func() (interface{}, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want %v", err, sentinel)
	}
}

// The core case: the open never returns. The caller must get an error quickly
// rather than blocking forever.
func TestNewSessionContextTimesOutOnWedgedTransport(t *testing.T) {
	start := time.Now()
	_, err := openWithContext(context.Background(), 150*time.Millisecond, func() (interface{}, error) {
		select {} // never returns, like a channel-open with no reply
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a wedged open returned no error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v — the open is not bounded", elapsed)
	}
	if !errors.Is(err, ErrSessionOpenTimeout) {
		t.Errorf("error = %v, want ErrSessionOpenTimeout", err)
	}
}

// A cancelled caller context must abort the open too, without waiting out the
// full timeout.
func TestNewSessionContextHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := openWithContext(ctx, 10*time.Second, func() (interface{}, error) {
		select {}
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("cancelled open returned no error")
	}
	if elapsed > 3*time.Second {
		t.Errorf("took %v — caller cancellation is not honored", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
