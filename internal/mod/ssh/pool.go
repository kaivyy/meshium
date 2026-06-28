package ssh

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// PoolConfig controls connection expiry and concurrency behavior for the SSH connection pool.
type PoolConfig struct {
	MaxIdle          time.Duration
	MaxLifetime      time.Duration
	MaxConcurrent    int
	KeepaliveInterval time.Duration // interval between keepalive probes; 0 = disable
	MaxRetries       int            // max reconnection attempts on transient failure; 0 = no retry
}

// Pool manages SSH connections per server.
type Pool struct {
	mu          sync.RWMutex
	connections map[int]*poolEntry
	pending     map[int]chan struct{} // deduplicates concurrent connection attempts
	config      PoolConfig
	semaphore   chan struct{}

	keepaliveMu     sync.Mutex
	keepaliveStop   chan struct{}
	keepaliveRunning bool
}

type poolEntry struct {
	client    *Client
	createdAt time.Time
}

// NewPool constructs a new SSH connection pool.
func NewPool(cfg PoolConfig) *Pool {
	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = 10 * time.Minute
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = 30 * time.Minute
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}
	if cfg.KeepaliveInterval == 0 {
		cfg.KeepaliveInterval = 2 * time.Minute
	}

	return &Pool{
		connections: make(map[int]*poolEntry),
		pending:     make(map[int]chan struct{}),
		config:      cfg,
		semaphore:   make(chan struct{}, cfg.MaxConcurrent),
	}
}

// Get returns a cached connection for the server if it is still valid,
// otherwise it establishes a new SSH connection. If the connection fails
// and MaxRetries is configured, it retries with exponential backoff.
// Concurrent calls for the same serverID are deduplicated — only one
// goroutine establishes the connection while others wait.
//
// Get is a backward-compatible wrapper around GetContext that uses
// context.Background(). Use GetContext for cancellation support.
func (p *Pool) Get(serverID int, cfg ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key ssh.PublicKey) error) (*Client, error) {
	return p.GetContext(context.Background(), serverID, cfg, hostKeyCallback)
}

// GetContext is the context-aware version of Get. It behaves identically
// to Get but respects the supplied context for cancellation during retry
// backoff. If the context is cancelled during a backoff wait, GetContext
// returns the context error immediately.
func (p *Pool) GetContext(ctx context.Context, serverID int, cfg ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key ssh.PublicKey) error) (*Client, error) {
	p.mu.Lock()
	// Check for a valid cached connection
	if entry, ok := p.connections[serverID]; ok {
		now := time.Now()
		if p.isEntryValid(entry, now) {
			entry.client.touch()
			client := entry.client
			p.mu.Unlock()
			return client, nil
		}

		_ = entry.client.Close()
		delete(p.connections, serverID)
	}

	// Check if another goroutine is already connecting for this serverID
	if waitCh, ok := p.pending[serverID]; ok {
		p.mu.Unlock()
		// Wait for the other goroutine to finish connecting
		select {
		case <-waitCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Try again to get the cached connection
		return p.GetContext(ctx, serverID, cfg, hostKeyCallback)
	}

	// Register our intent to connect
	waitCh := make(chan struct{})
	p.pending[serverID] = waitCh
	p.mu.Unlock()

	// Ensure we clean up the pending entry and signal waiters
	defer func() {
		p.mu.Lock()
		delete(p.pending, serverID)
		p.mu.Unlock()
		close(waitCh)
	}()

	if p.semaphore != nil {
		select {
		case p.semaphore <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() {
			<-p.semaphore
		}()
	}

	// Attempt connection with retry on transient failures
	maxRetries := p.config.MaxRetries
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, ...
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			// Context-aware wait: return immediately if cancelled
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		client, err := connect(cfg, hostKeyCallback)
		if err != nil {
			lastErr = err
			if !isTransientError(err) || attempt == maxRetries {
				return nil, err
			}
			continue // retry on transient error
		}

		now := time.Now()
		p.mu.Lock()
		// Check if another valid connection was stored while we were connecting
		// (possible if the pending dedup missed due to timing)
		if entry, ok := p.connections[serverID]; ok {
			if p.isEntryValid(entry, now) {
				_ = client.Close()
				entry.client.touch()
				p.mu.Unlock()
				return entry.client, nil
			}
			_ = entry.client.Close()
		}

		p.connections[serverID] = &poolEntry{
			client:    client,
			createdAt: now,
		}
		p.mu.Unlock()

		return client, nil
	}

	return nil, lastErr
}

// StartKeepalive launches a background goroutine that periodically sends
// keepalive probes to all pooled connections. This prevents NAT/firewall
// timeouts from silently dropping idle connections. Call StopKeepalive
// to stop the goroutine. It is safe to call multiple times, and can be
// called again after StopKeepalive to restart the keepalive loop.
func (p *Pool) StartKeepalive() {
	p.keepaliveMu.Lock()
	defer p.keepaliveMu.Unlock()

	if p.keepaliveRunning {
		return
	}

	p.keepaliveStop = make(chan struct{})
	p.keepaliveRunning = true
	go p.keepaliveLoop()
}

// StopKeepalive stops the background keepalive goroutine.
// It is safe to call multiple times.
func (p *Pool) StopKeepalive() {
	p.keepaliveMu.Lock()
	defer p.keepaliveMu.Unlock()

	if !p.keepaliveRunning {
		return
	}

	close(p.keepaliveStop)
	p.keepaliveStop = nil
	p.keepaliveRunning = false
}

func (p *Pool) keepaliveLoop() {
	// Capture the stop channel before the loop starts so we don't
	// race with StopKeepalive setting it to nil.
	p.keepaliveMu.Lock()
	stopCh := p.keepaliveStop
	p.keepaliveMu.Unlock()

	ticker := time.NewTicker(p.config.KeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			p.sendKeepalives()
		}
	}
}

// sendKeepalives probes all pooled connections and removes dead ones.
// It copies the entry list under a read lock, probes each connection
// outside the lock (to avoid blocking writers during network I/O),
// then removes dead entries under a write lock.
func (p *Pool) sendKeepalives() {
	// Snapshot the entries under a read lock.
	type probeEntry struct {
		serverID int
		client   *Client
	}
	p.mu.RLock()
	entries := make([]probeEntry, 0, len(p.connections))
	for serverID, entry := range p.connections {
		if entry == nil || entry.client == nil {
			continue
		}
		entries = append(entries, probeEntry{serverID, entry.client})
	}
	p.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	// Probe each connection outside the lock.
	type deadEntry struct {
		serverID int
		client   *Client
	}
	var dead []deadEntry
	for _, pe := range entries {
		if !pe.client.IsAlive() {
			dead = append(dead, deadEntry{pe.serverID, pe.client})
		}
	}

	if len(dead) == 0 {
		return
	}

	// Remove dead connections from the pool under a write lock.
	p.mu.Lock()
	for _, d := range dead {
		if entry, ok := p.connections[d.serverID]; ok && entry.client == d.client {
			delete(p.connections, d.serverID)
		}
	}
	p.mu.Unlock()

	// Close dead connections outside the lock.
	for _, d := range dead {
		_ = d.client.Close()
	}
}

// Close closes a specific server connection and removes it from the pool.
func (p *Pool) Close(serverID int) error {
	p.mu.Lock()
	entry, ok := p.connections[serverID]
	if ok {
		delete(p.connections, serverID)
	}
	p.mu.Unlock()

	if !ok {
		return nil
	}

	return entry.client.Close()
}

// CloseIdle closes all connections that have been idle longer than MaxIdle.
func (p *Pool) CloseIdle() {
	if p.config.MaxIdle <= 0 {
		return
	}

	now := time.Now()
	var stale []*poolEntry

	p.mu.Lock()
	for serverID, entry := range p.connections {
		if entry == nil || entry.client == nil {
			delete(p.connections, serverID)
			continue
		}
		if now.Sub(entry.client.LastUsed()) > p.config.MaxIdle {
			stale = append(stale, entry)
			delete(p.connections, serverID)
		}
	}
	p.mu.Unlock()

	for _, entry := range stale {
		_ = entry.client.Close()
	}
}

// CloseAll closes every pooled connection, stops the keepalive goroutine,
// and clears the pool.
func (p *Pool) CloseAll() {
	p.StopKeepalive()

	p.mu.Lock()
	entries := p.connections
	p.connections = make(map[int]*poolEntry)
	p.mu.Unlock()

	for _, entry := range entries {
		_ = entry.client.Close()
	}
}

// Count returns the number of active pooled connections.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.connections)
}

func (p *Pool) isEntryValid(entry *poolEntry, now time.Time) bool {
	if entry == nil || entry.client == nil {
		return false
	}
	if !entry.client.IsAlive() {
		return false
	}
	if p.config.MaxLifetime > 0 && now.Sub(entry.createdAt) > p.config.MaxLifetime {
		return false
	}
	if p.config.MaxIdle > 0 && now.Sub(entry.client.LastUsed()) > p.config.MaxIdle {
		return false
	}
	return true
}
