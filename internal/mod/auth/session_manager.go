package auth

import "sync"

// SessionManager is a small compatibility shim for older callers/tests.
// The auth Service remains the source of truth for production session checks.
type SessionManager struct {
	mu    sync.RWMutex
	token string
}

func NewSessionManager() *SessionManager {
	return &SessionManager{}
}

func (m *SessionManager) Set(token string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = token
}

func (m *SessionManager) Clear() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = ""
}

func (m *SessionManager) ValidateSession(token string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.token != "" && token == m.token
}
