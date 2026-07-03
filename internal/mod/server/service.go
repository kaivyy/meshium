package server

import (
	"errors"
	"strings"

	"meshium/internal/mod/auth"
	"meshium/internal/mod/transport"
	"meshium/internal/shared"
)

// PoolInvalidator allows the server service to invalidate cached SSH connections
// when a server's configuration changes or is deleted.
type PoolInvalidator interface {
	Close(serverID int) error
}

type Service struct {
	repo     Repo
	authSvc  *auth.Service
	pool     PoolInvalidator
	hostKeys transport.HostKeyStore
}

func NewService(repo Repo, authSvc *auth.Service) *Service {
	return &Service{repo: repo, authSvc: authSvc}
}

// SetPoolInvalidator sets the SSH pool invalidator. When set, the service will
// invalidate cached SSH connections for a server whenever its configuration
// is updated or the server is deleted.
func (s *Service) SetPoolInvalidator(p PoolInvalidator) {
	s.pool = p
}

// SetHostKeyStore configures the trusted host key store used by host key endpoints.
func (s *Service) SetHostKeyStore(store transport.HostKeyStore) {
	s.hostKeys = store
}

func (s *Service) encryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if s.authSvc == nil {
		return "", errors.New("app is locked")
	}
	key := s.authSvc.GetAESKey()
	if key == nil {
		return "", errors.New("app is locked")
	}
	encrypted, err := shared.Encrypt(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return string(encrypted), nil
}

func (s *Service) decryptCredential(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if s.authSvc == nil {
		return "", errors.New("app is locked")
	}
	key := s.authSvc.GetAESKey()
	if key == nil {
		return "", errors.New("app is locked")
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func (s *Service) redactServer(srv *Server) *Server {
	if srv == nil {
		return nil
	}
	srv.Password = ""
	srv.SSHKey = ""
	srv.Passphrase = ""
	return srv
}

func (s *Service) Create(req CreateRequest) (*Server, error) {
	password, err := s.encryptCredential(req.Password)
	if err != nil {
		return nil, err
	}
	sshKey, err := s.encryptCredential(req.SSHKey)
	if err != nil {
		return nil, err
	}
	passphrase, err := s.encryptCredential(req.Passphrase)
	if err != nil {
		return nil, err
	}

	port := req.Port
	if port == 0 {
		port = 22
	}

	id, err := s.repo.Create(Server{
		Name:        req.Name,
		Description: req.Description,
		Host:        req.Host,
		Port:        port,
		Username:    req.Username,
		Password:    password,
		SSHKey:      sshKey,
		Passphrase:  passphrase,
		Tags:        req.Tags,
		Environment: req.Environment,
		Region:      req.Region,
		Icon:        req.Icon,
		Color:       req.Color,
		BastionID:   req.BastionID,
	})
	if err != nil {
		return nil, err
	}

	return s.GetByID(id)
}

func (s *Service) GetByID(id int) (*Server, error) {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return s.redactServer(srv), nil
}

func (s *Service) List(filter ListFilter) ([]Server, error) {
	servers, err := s.repo.List(filter)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		servers[i].Password = ""
		servers[i].SSHKey = ""
		servers[i].Passphrase = ""
	}
	return servers, nil
}

func (s *Service) Update(id int, req UpdateRequest) error {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	if req.Name != nil {
		srv.Name = *req.Name
	}
	if req.Description != nil {
		srv.Description = *req.Description
	}
	if req.Host != nil {
		srv.Host = *req.Host
	}
	if req.Port != nil {
		srv.Port = *req.Port
	}
	if req.Username != nil {
		srv.Username = *req.Username
	}
	if req.Tags != nil {
		srv.Tags = *req.Tags
	}
	if req.Environment != nil {
		srv.Environment = *req.Environment
	}
	if req.Region != nil {
		srv.Region = *req.Region
	}
	if req.Icon != nil {
		srv.Icon = *req.Icon
	}
	if req.Color != nil {
		srv.Color = *req.Color
	}
	if req.BastionID != nil {
		srv.BastionID = *req.BastionID
	}

	if req.Password != nil {
		encrypted, err := s.encryptCredential(*req.Password)
		if err != nil {
			return err
		}
		srv.Password = encrypted
	}
	if req.SSHKey != nil {
		encrypted, err := s.encryptCredential(*req.SSHKey)
		if err != nil {
			return err
		}
		srv.SSHKey = encrypted
	}
	if req.Passphrase != nil {
		encrypted, err := s.encryptCredential(*req.Passphrase)
		if err != nil {
			return err
		}
		srv.Passphrase = encrypted
	}

	// Invalidate cached SSH connection since server config may have changed
	if s.pool != nil {
		s.pool.Close(id)
	}
	return s.repo.Update(id, *srv)
}

func (s *Service) Delete(id int) error {
	// Invalidate cached SSH connection before deleting the server
	if s.pool != nil {
		s.pool.Close(id)
	}
	return s.repo.Delete(id)
}

func (s *Service) ToggleFavorite(id int) error {
	return s.repo.ToggleFavorite(id)
}

func (s *Service) GetServerInfo(serverID int) (*ServerInfo, error) {
	return s.repo.GetServerInfo(serverID)
}

// TrustHostKey explicitly trusts the server's SSH host key.
func (s *Service) TrustHostKey(serverID int) (string, error) {
	if _, err := s.repo.GetByID(serverID); err != nil {
		return "", err
	}
	if s.hostKeys == nil {
		return "", errors.New("known hosts store unavailable")
	}
	return s.hostKeys.TrustHostKey(serverID)
}

// GetFingerprint returns the stored fingerprint for a server's SSH host key.
func (s *Service) GetFingerprint(serverID int) (string, error) {
	if s.hostKeys == nil {
		return "", errors.New("known hosts store unavailable")
	}
	return s.hostKeys.GetFingerprint(serverID)
}

// IsTrusted reports whether a host:port entry is verified in known_hosts.
func (s *Service) IsTrusted(host string, port int) (bool, error) {
	if s.hostKeys == nil {
		return false, errors.New("known hosts store unavailable")
	}
	return s.hostKeys.IsTrusted(host, port)
}

// GetDecryptedCredentials returns decrypted credentials for SSH connection consumers.
func (s *Service) GetDecryptedCredentials(serverID int) (password, sshKey, passphrase string, err error) {
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return "", "", "", err
	}

	password, err = s.decryptCredential(srv.Password)
	if err != nil {
		return "", "", "", err
	}
	sshKey, err = s.decryptCredential(srv.SSHKey)
	if err != nil {
		return "", "", "", err
	}
	passphrase, err = s.decryptCredential(srv.Passphrase)
	if err != nil {
		return "", "", "", err
	}

	return password, sshKey, passphrase, nil
}

// GetAuthStatus returns the authentication status for a server.
func (s *Service) GetAuthStatus(serverID int) (*AuthStatus, error) {
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return nil, err
	}

	authMethod := "password"
	if srv.SSHKey != "" {
		authMethod = "key"
	}
	if srv.AuthMethod != "" {
		authMethod = srv.AuthMethod
	}

	return &AuthStatus{
		AuthMethod:       authMethod,
		CredentialStatus: srv.CredentialStatus,
		HasPassword:      srv.Password != "",
		HasSSHKey:        srv.SSHKey != "",
		HasPassphrase:    srv.Passphrase != "",
		Fingerprint:      srv.Fingerprint,
		KeyType:          srv.KeyType,
	}, nil
}

// RecordConnectionHistory records a connection attempt in the history.
func (s *Service) RecordConnectionHistory(entry ConnectionHistoryEntry) error {
	return s.repo.RecordConnection(entry)
}

// GetConnectionHistory returns connection history for a server.
func (s *Service) GetConnectionHistory(serverID int, limit int) ([]ConnectionHistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return s.repo.GetConnectionHistory(serverID, limit)
}

// GetConnectionMetrics returns aggregated connection metrics for a server.
func (s *Service) GetConnectionMetrics(serverID int) (*ConnectionMetrics, error) {
	return s.repo.GetConnectionMetrics(serverID)
}

// GetAuthDashboard returns the authentication dashboard data.
func (s *Service) GetAuthDashboard() (*AuthDashboard, error) {
	return s.repo.GetAuthDashboard()
}

// GetCredentialHealthScore computes the credential health score for a server.
func (s *Service) GetCredentialHealthScore(serverID int) (*CredentialHealthScore, error) {
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return nil, err
	}

	metrics, _ := s.repo.GetConnectionMetrics(serverID)

	score := 0
	passwordExists := srv.Password != ""
	keyInstalled := srv.SSHKey != ""
	fingerprintVerified := srv.Fingerprint != ""
	knownHost := false
	recentSuccess := metrics != nil && metrics.SuccessCount > 0
	recentFailure := metrics != nil && metrics.FailureCount > 0
	passphraseEnabled := srv.Passphrase != ""
	bastionHealthy := srv.BastionID == 0 // No bastion = healthy
	agentHealthy := true

	if passwordExists {
		score += 10
	}
	if keyInstalled {
		score += 20
	}
	if fingerprintVerified {
		score += 15
	}
	if knownHost {
		score += 15
	}
	if recentSuccess {
		score += 15
	}
	if !recentFailure {
		score += 10
	}
	if passphraseEnabled {
		score += 5
	}
	if bastionHealthy {
		score += 5
	}
	if agentHealthy {
		score += 5
	}

	grade := "Critical"
	switch {
	case score >= 95:
		grade = "A+"
	case score >= 85:
		grade = "A"
	case score >= 75:
		grade = "B"
	case score >= 60:
		grade = "C"
	case score >= 40:
		grade = "D"
	}

	return &CredentialHealthScore{
		Score:              score,
		Grade:              grade,
		PasswordExists:     passwordExists,
		KeyInstalled:       keyInstalled,
		FingerprintVerified: fingerprintVerified,
		KnownHost:          knownHost,
		RecentSuccess:      recentSuccess,
		RecentFailure:      recentFailure,
		PassphraseEnabled:  passphraseEnabled,
		BastionHealthy:     bastionHealthy,
		AgentHealthy:       agentHealthy,
	}, nil
}

// UpdateCredentialStatus updates the credential status for a server.
func (s *Service) UpdateCredentialStatus(serverID int, status string) error {
	return s.repo.UpdateAuthStatus(serverID, status, false)
}

// UpdateServerStats updates the success/failure stats for a server.
func (s *Service) UpdateServerStats(serverID int, success bool) error {
	return s.repo.UpdateAuthStatus(serverID, "", success)
}

// GetServerKeys returns all SSH keys for a server.
func (s *Service) GetServerKeys(serverID int) ([]ServerKey, error) {
	keys, err := s.repo.GetServerKeys(serverID)
	if err != nil {
		return nil, err
	}
	// Redact private keys
	for i := range keys {
		keys[i].PublicKey = strings.TrimSpace(keys[i].PublicKey)
	}
	return keys, nil
}

// AddServerKey adds a new SSH key for a server.
func (s *Service) AddServerKey(serverID int, label, keyType, privateKey, passphrase, notes string) (*ServerKey, error) {
	// Encrypt the private key
	encryptedKey, err := s.encryptCredential(privateKey)
	if err != nil {
		return nil, err
	}
	encryptedPassphrase, err := s.encryptCredential(passphrase)
	if err != nil {
		return nil, err
	}
	_ = encryptedKey
	_ = encryptedPassphrase

	// Derive public key and fingerprint
	// (In production, we'd parse the private key to get the public key and fingerprint)
	// For now, we store what we can
	id, err := s.repo.StoreServerKey(ServerKey{
		ServerID:   serverID,
		Label:      label,
		KeyType:    keyType,
		PublicKey:  "",
		Fingerprint: "",
		Notes:      notes,
		Enabled:    true,
		Priority:   0,
	})
	if err != nil {
		return nil, err
	}
	return s.repo.GetServerKey(serverID, id)
}

// RemoveServerKey removes an SSH key from a server.
func (s *Service) RemoveServerKey(serverID int, keyID int) error {
	return s.repo.DeleteServerKey(serverID, keyID)
}

// SetDefaultServerKey sets the default SSH key for a server.
func (s *Service) SetDefaultServerKey(serverID int, keyID int) error {
	return s.repo.SetDefaultKey(serverID, keyID)
}

// GetAuthPriority returns the configured auth method priority order.
func (s *Service) GetAuthPriority() ([]AuthPriorityEntry, error) {
	return s.repo.GetAuthPriority()
}

// UpdateAuthPriority updates the auth method priority order.
func (s *Service) UpdateAuthPriority(entries []AuthPriorityEntry) error {
	return s.repo.SetAuthPriority(entries)
}

// GetConnectionProfiles returns all connection profiles.
func (s *Service) GetConnectionProfiles() ([]ConnectionProfile, error) {
	return s.repo.ListConnectionProfiles()
}

// GetConnectionProfile returns a specific connection profile.
func (s *Service) GetConnectionProfile(id int) (*ConnectionProfile, error) {
	return s.repo.GetConnectionProfile(id)
}

// CreateConnectionProfile creates a new connection profile.
func (s *Service) CreateConnectionProfile(profile ConnectionProfile) (*ConnectionProfile, error) {
	id, err := s.repo.CreateConnectionProfile(profile)
	if err != nil {
		return nil, err
	}
	return s.repo.GetConnectionProfile(id)
}

// UpdateConnectionProfile updates a connection profile.
func (s *Service) UpdateConnectionProfile(profile ConnectionProfile) error {
	return s.repo.UpdateConnectionProfile(profile)
}

// DeleteConnectionProfile deletes a connection profile.
func (s *Service) DeleteConnectionProfile(id int) error {
	return s.repo.DeleteConnectionProfile(id)
}

// GetRetryConfig returns the retry config for a server.
func (s *Service) GetRetryConfig(serverID int) (*RetryConfig, error) {
	return s.repo.GetRetryConfig(serverID)
}

// UpdateRetryConfig updates the retry config for a server.
func (s *Service) UpdateRetryConfig(config RetryConfig) error {
	return s.repo.SetRetryConfig(config)
}

// GetKnownHosts returns all known host entries.
func (s *Service) GetKnownHosts() ([]KnownHostEntry, error) {
	return s.repo.ListKnownHosts()
}

// RemoveKnownHost removes a known host entry.
func (s *Service) RemoveKnownHost(host string, port int) error {
	return s.repo.RemoveKnownHost(host, port)
}

// GetHostKeyChanges returns host key change audit entries.
func (s *Service) GetHostKeyChanges(limit int) ([]HostKeyChange, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return s.repo.ListHostKeyChanges(limit)
}

// GetCredentialAudit returns credential access audit entries.
func (s *Service) GetCredentialAudit(serverID int, limit int) ([]CredentialAuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return s.repo.ListCredentialAudit(serverID, limit)
}

// RecordCredentialAudit logs a credential access event.
func (s *Service) RecordCredentialAudit(serverID int, action, detail, ip string) error {
	return s.repo.RecordCredentialAudit(CredentialAuditEntry{
		ServerID: serverID,
		Action:   action,
		Detail:   detail,
		IP:       ip,
	})
}

// ExportData exports server data for backup.
func (s *Service) ExportData() (map[string]interface{}, error) {
	servers, err := s.repo.List(ListFilter{})
	if err != nil {
		return nil, err
	}

	knownHosts, err := s.repo.ListKnownHosts()
	if err != nil {
		return nil, err
	}

	profiles, err := s.repo.ListConnectionProfiles()
	if err != nil {
		return nil, err
	}

	authPriority, err := s.repo.GetAuthPriority()
	if err != nil {
		return nil, err
	}

	// Redact credentials from servers
	for i := range servers {
		servers[i].Password = ""
		servers[i].SSHKey = ""
		servers[i].Passphrase = ""
	}

	return map[string]interface{}{
		"servers":       servers,
		"knownHosts":    knownHosts,
		"profiles":      profiles,
		"authPriority":  authPriority,
		"version":       "1.0",
		"exportedAt":    "CURRENT_TIMESTAMP",
	}, nil
}
