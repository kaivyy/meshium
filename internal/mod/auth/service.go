package auth

import (
	"errors"
	"sync"

	"meshium/internal/shared"
)

type Service struct {
	repo   Repo
	mu     sync.RWMutex
	aesKey []byte
	locked bool
}

func NewService(repo Repo) *Service {
	return &Service{repo: repo, locked: true}
}

// IsSetup returns true if a master password has been set.
func (s *Service) IsSetup() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.repo.HasMasterPassword()
}

// IsLocked returns true if the app is locked (no AES key in memory).
func (s *Service) IsLocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.locked
}

// GetAESKey returns the current AES key (nil if locked).
func (s *Service) GetAESKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.aesKey == nil {
		return nil
	}

	key := make([]byte, len(s.aesKey))
	copy(key, s.aesKey)
	return key
}

// Setup sets the master password for the first time.
func (s *Service) Setup(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	setup, err := s.repo.HasMasterPassword()
	if err != nil {
		return err
	}
	if setup {
		return errors.New("master password already set")
	}

	hash, err := shared.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repo.SetMasterPassword(hash); err != nil {
		return err
	}

	salt := []byte("meshium-salt-v1")
	s.aesKey = shared.DeriveKey(password, salt)
	s.locked = false

	return nil
}

// Unlock verifies the master password and loads the AES key into memory.
func (s *Service) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hash, err := s.repo.GetConfigValue("master_password_hash")
	if err != nil {
		return err
	}
	if hash == "" {
		return errors.New("master password not set")
	}

	if !shared.VerifyPassword(password, hash) {
		return errors.New("invalid password")
	}

	salt := []byte("meshium-salt-v1")
	s.aesKey = shared.DeriveKey(password, salt)
	s.locked = false
	return nil
}

// Lock clears the AES key from memory.
func (s *Service) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.aesKey {
		s.aesKey[i] = 0
	}
	s.aesKey = nil
	s.locked = true
}
