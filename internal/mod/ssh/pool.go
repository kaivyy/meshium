package ssh

import (
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// PoolConfig controls connection expiry behavior for the SSH connection pool.
type PoolConfig struct {
	MaxIdle     time.Duration
	MaxLifetime time.Duration
}

// Pool manages SSH connections per server.
type Pool struct {
	mu          sync.RWMutex
	connections map[int]*poolEntry
	config      PoolConfig
	stopCh      chan struct{}
	doneCh      chan struct{}
	closeOnce   sync.Once
	closed      bool
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

	p := &Pool{
		connections: make(map[int]*poolEntry),
		config:      cfg,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}

	go p.backgroundSweep()
	return p
}

// Get returns a cached connection for the server if it is still valid,
// otherwise it establishes a new SSH connection.
func (p *Pool) Get(serverID int, cfg ServerConfig, hostKeyCallback func(hostname string, remote net.Addr, key ssh.PublicKey) error) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errors.New("ssh pool is closed")
	}

	now := time.Now()
	if entry, ok := p.connections[serverID]; ok {
		if p.isEntryValid(entry, now) {
			entry.client.touch()
			return entry.client, nil
		}

		_ = entry.client.Close()
		delete(p.connections, serverID)
	}

	client, err := connect(cfg, hostKeyCallback)
	if err != nil {
		return nil, err
	}

	now = time.Now()
	p.connections[serverID] = &poolEntry{
		client:    client,
		createdAt: now,
	}

	return client, nil
}

// Release closes a specific server connection and removes it from the pool.
func (p *Pool) Release(serverID int) error {
	entry := p.removeEntry(serverID, nil)
	if entry == nil {
		return nil
	}
	return entry.client.Close()
}

// Close closes every pooled connection, clears the pool, and stops the sweeper.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		entries := p.connections
		p.connections = make(map[int]*poolEntry)
		p.mu.Unlock()

		close(p.stopCh)
		<-p.doneCh

		for _, entry := range entries {
			if entry != nil && entry.client != nil {
				_ = entry.client.Close()
			}
		}
	})
}

// CloseAll closes every pooled connection and clears the pool.
// It is retained for backward compatibility.
func (p *Pool) CloseAll() {
	p.Close()
}

// Count returns the number of active pooled connections.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.connections)
}

func (p *Pool) backgroundSweep() {
	defer close(p.doneCh)

	interval := p.sweepInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.sweepExpired(time.Now())
		case <-p.stopCh:
			return
		}
	}
}

func (p *Pool) sweepInterval() time.Duration {
	interval := p.config.MaxIdle
	if interval <= 0 || (p.config.MaxLifetime > 0 && p.config.MaxLifetime < interval) {
		interval = p.config.MaxLifetime
	}
	if interval <= 0 {
		interval = time.Minute
	}
	interval /= 2
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	return interval
}

func (p *Pool) sweepExpired(now time.Time) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return
	}

	snapshot := make(map[int]*poolEntry, len(p.connections))
	for serverID, entry := range p.connections {
		snapshot[serverID] = entry
	}
	p.mu.RUnlock()

	for serverID, entry := range snapshot {
		if !p.isEntryValid(entry, now) {
			if removed := p.removeEntry(serverID, entry); removed != nil {
				_ = removed.client.Close()
			}
		}
	}
}

func (p *Pool) removeEntry(serverID int, expected *poolEntry) *poolEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.connections[serverID]
	if !ok {
		return nil
	}
	if expected != nil && entry != expected {
		return nil
	}

	delete(p.connections, serverID)
	return entry
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
