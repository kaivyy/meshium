package ssh

import (
	"context"
	"sync"
)

// maxConcurrentSessions caps how many SSH channels this client opens at once.
//
// sshd's default MaxSessions is 10 channels per CONNECTION, and the pool
// multiplexes every subsystem onto one connection per server: discovery
// collectors, open web terminals, log tails, monitoring probes and migration
// commands all share it. No single subsystem exceeds 10, but the total can —
// that is exactly the original incident, where ~13 collectors ran on one
// connection, sshd rejected the overflow, and the affected collectors returned
// blank data with no error.
//
// 8 leaves headroom under the default 10 for a terminal or tail that the pool
// does not know about. Excess work waits for a slot instead of being rejected.
const maxConcurrentSessions = 8

// acquireSession reserves a session slot, blocking until one frees or ctx is
// done. The returned function releases the slot and is safe to call more than
// once.
func (c *Client) acquireSession(ctx context.Context) (func(), error) {
	sem := c.sessionSem()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-sem }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sessionSem lazily creates the per-client semaphore. Lazy so a zero-value
// Client (tests, and clients built by connect) needs no extra construction
// step and can never be used with a nil channel.
func (c *Client) sessionSem() chan struct{} {
	c.semOnce.Do(func() {
		c.sem = make(chan struct{}, maxConcurrentSessions)
	})
	return c.sem
}
