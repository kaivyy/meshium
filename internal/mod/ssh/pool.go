package ssh

import (
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// PoolConfig controls connection expiry and concurrency behavior for the SSH connection pool.
type PoolConfig struct {
	MaxIdle       time.Duration
	MaxLifetime   time.Duration
	MaxConcurrent int
}

// Pool manages SSH connections per server.
type Pool struct {
	mu          sync.RWMutex
	connections map[int]*poolEntry
	pending     map[int]chan struct{}
	config      PoolConfig
	semaphore   chan struct{}
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

	return &Pool{
		connections: make(map[int]*poolEntry),
		pending:     make(map[int]chan struct{}),
		config:      cfg,
		semaphore:   make(chan struct{}, cfg.MaxConcurrent),
	}
}

// Get returns a cached connection for the server if it is still valid,
// otherwise it establishes a new SSH connection.
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

	client, err := connect(cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.connections[serverID]; ok {
		if p.isEntryValid(entry, now) {
			_ = client.Close()
			entry.client.touch()
			return entry.client, nil
		}

		_ = entry.client.Close()
	}

	p.connections[serverID] = &poolEntry{
		client:    client,
		createdAt: now,
	}

	return client, nil
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

// CloseAll closes every pooled connection and clears the pool.
func (p *Pool) CloseAll() {
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
