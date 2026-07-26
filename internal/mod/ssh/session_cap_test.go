package ssh

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sshd's default MaxSessions is 10 channels per connection. The pool
// multiplexes every subsystem onto ONE connection per server — discovery
// collectors, open web terminals, log tails, monitoring, migration commands —
// so the total can exceed 10 even though no single subsystem does. The
// original incident: ~13 collectors on one connection, sshd rejected the
// overflow, and the affected collectors silently returned blank data.
//
// The semaphore caps concurrent session opens per client so the overflow waits
// instead of being rejected.

func TestSessionSemaphoreCapsConcurrency(t *testing.T) {
	c := &Client{}
	var live, peak int64

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := c.acquireSession(context.Background())
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer release()

			n := atomic.AddInt64(&live, 1)
			for {
				p := atomic.LoadInt64(&peak)
				if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt64(&live, -1)
		}()
	}
	wg.Wait()

	if peak > maxConcurrentSessions {
		t.Errorf("peak concurrent sessions = %d, cap is %d — sshd would reject the overflow", peak, maxConcurrentSessions)
	}
	if peak == 0 {
		t.Fatal("no sessions were acquired")
	}
}

// Waiting for a slot must honour the caller's context rather than blocking
// forever behind a stuck holder.
func TestSessionSemaphoreHonorsContext(t *testing.T) {
	c := &Client{}

	// Fill every slot and keep them.
	releases := make([]func(), 0, maxConcurrentSessions)
	for i := 0; i < maxConcurrentSessions; i++ {
		release, err := c.acquireSession(context.Background())
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := c.acquireSession(ctx); err == nil {
		t.Error("acquire succeeded with every slot held")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waited %v — context not honoured", elapsed)
	}
}

// Releasing must return the slot so later work proceeds.
func TestSessionSemaphoreReleaseFreesSlot(t *testing.T) {
	c := &Client{}
	release, err := c.acquireSession(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release() // double release must not corrupt the count

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r2, err := c.acquireSession(ctx)
	if err != nil {
		t.Fatalf("slot was not returned: %v", err)
	}
	r2()
}
