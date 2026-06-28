package ssh

import (
	"net"
	"strings"
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
	pending     map[int]chan struct{}
	config      PoolConfig
	semaphore   chan struct{}

	keepaliveStop chan struct{}
	keepaliveOnce sync.Once
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
func (p *Pool) Get(serverID int, cfg ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key ssh.PublicKey) error) (*Client, error) {
	p.mu.Lock()
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
	p.mu.Unlock()

	if p.semaphore != nil {
		p.semaphore <- struct{}{}
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
			time.Sleep(backoff)
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

// isTransientError returns true for errors that may resolve on retry
// (e.g. connection refused, timeout, EOF).
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Common transient error patterns
	for _, needle := range []string{
		"connection refused",
		"connection reset",
		"timeout",
		"timed out",
		"eof",
		"broken pipe",
		"temporarily unavailable",
		"i/o timeout",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// StartKeepalive launches a background goroutine that periodically sends
// keepalive probes to all pooled connections. This prevents NAT/firewall
// timeouts from silently dropping idle connections. Call StopKeepalive
// to stop the goroutine. It is safe to call multiple times — only the
// first call starts the goroutine.
func (p *Pool) StartKeepalive() {
	p.keepaliveOnce.Do(func() {
		p.keepaliveStop = make(chan struct{})
		go p.keepaliveLoop()
	})
}

// StopKeepalive stops the background keepalive goroutine.
func (p *Pool) StopKeepalive() {
	if p.keepaliveStop != nil {
		close(p.keepaliveStop)
		p.keepaliveStop = nil
	}
}

func (p *Pool) keepaliveLoop() {
	ticker := time.NewTicker(p.config.KeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.keepaliveStop:
			return
		case <-ticker.C:
			p.sendKeepalives()
		}
	}
}

// sendKeepalives probes all pooled connections and removes dead ones.
func (p *Pool) sendKeepalives() {
	p.mu.RLock()
	type deadEntry struct {
		serverID int
		entry    *poolEntry
	}
	var dead []deadEntry
	for serverID, entry := range p.connections {
		if entry == nil || entry.client == nil {
			continue
		}
		if !entry.client.IsAlive() {
			dead = append(dead, deadEntry{serverID, entry})
		}
	}
	p.mu.RUnlock()

	if len(dead) == 0 {
		return
	}

	// Remove dead connections from the pool and close them
	p.mu.Lock()
	for _, d := range dead {
		if existing, ok := p.connections[d.serverID]; ok && existing == d.entry {
			delete(p.connections, d.serverID)
		}
	}
	p.mu.Unlock()

	for _, d := range dead {
		_ = d.entry.client.Close()
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
