package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"

	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

type Service struct {
	repo      Repo
	mu        sync.RWMutex
	aesKey    []byte
	locked    bool
	sessionToken string
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

	privatePEM, publicSSH, err := ssh.GenerateKeyPair()
	if err != nil {
		return err
	}

	hash, err := shared.HashPassword(password)
	if err != nil {
		return err
	}

	// Generate a random per-installation salt (32 bytes)
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	saltB64 := base64.StdEncoding.EncodeToString(salt)

	aesKey := shared.DeriveKey(password, salt)
	encryptedPrivateKey, err := shared.Encrypt(aesKey, privatePEM)
	if err != nil {
		return err
	}

	if err := s.repo.SetupAll(hash, string(encryptedPrivateKey), string(publicSSH)); err != nil {
		return err
	}
	// Store the salt separately so it can be retrieved during unlock
	if err := s.repo.SetConfigValue("pbkdf2_salt", saltB64); err != nil {
		return err
	}

	s.aesKey = aesKey
	s.locked = false
	s.sessionToken = generateSessionToken()
	return nil
}

// GetSSHPublicKey returns the app's SSH public key.
func (s *Service) GetSSHPublicKey() (string, error) {
	return s.repo.GetSSHPublicKey()
}

// RegenerateSSHKey generates a new SSH key pair, encrypts the private key,
// and stores both keys in app_config.
func (s *Service) RegenerateSSHKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.locked {
		return "", errors.New("app is locked")
	}

	return s.generateAndStoreSSHKeyPair()
}

// EnsureSSHKey generates a key pair if one doesn't exist yet.
// Called during Setup.
func (s *Service) EnsureSSHKey() error {
	existing, err := s.repo.GetSSHPublicKey()
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}

	_, err = s.generateAndStoreSSHKeyPair()
	return err
}

func (s *Service) generateAndStoreSSHKeyPair() (string, error) {
	privatePEM, publicSSH, err := ssh.GenerateKeyPair()
	if err != nil {
		return "", err
	}

	encrypted, err := shared.Encrypt(s.aesKey, privatePEM)
	if err != nil {
		return "", err
	}

	if err := s.repo.SetSSHKeyPair(string(encrypted), string(publicSSH)); err != nil {
		return "", err
	}

	return string(publicSSH), nil
}

// Unlock verifies the master password and loads the AES key into memory.
func (s *Service) Unlock(password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hash, err := s.repo.GetConfigValue("master_password_hash")
	if err != nil {
		return "", err
	}
	if hash == "" {
		return "", errors.New("master password not set")
	}

	if !shared.VerifyPassword(password, hash) {
		return "", errors.New("invalid password")
	}

	// Retrieve the per-installation salt
	saltB64, err := s.repo.GetConfigValue("pbkdf2_salt")
	if err != nil {
		return "", err
	}
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return "", err
	}

	s.aesKey = shared.DeriveKey(password, salt)
	s.locked = false
	s.sessionToken = generateSessionToken()
	return s.sessionToken, nil
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
	s.sessionToken = ""
}

// GetSessionToken returns the current session token (empty if locked).
func (s *Service) GetSessionToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionToken
}

// ValidateSessionToken checks if the provided token matches the current session.
func (s *Service) ValidateSessionToken(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.locked || s.sessionToken == "" {
		return false
	}
	return token == s.sessionToken
}

// generateSessionToken creates a cryptographically random session token.
func generateSessionToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// This should never happen; if it does, the system is broken
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}
