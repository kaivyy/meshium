package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"meshium/internal/mod/auth"
	modssh "meshium/internal/mod/ssh"
	"meshium/internal/shared"
)

type connectionMode int

const (
	connectionModeConfigured connectionMode = iota
	connectionModePassword
	connectionModeKey
)

// KeyHandler exposes SSH key-management and authentication routes.
type KeyHandler struct {
	serverSvc  *Service
	pool       *modssh.Pool
	knownHosts *modssh.KnownHostsStore
	authSvc    *auth.Service
}

// NewKeyHandler creates a KeyHandler.
func NewKeyHandler(serverSvc *Service, pool *modssh.Pool, knownHosts *modssh.KnownHostsStore, authSvc *auth.Service) *KeyHandler {
	return &KeyHandler{
		serverSvc:  serverSvc,
		pool:       pool,
		knownHosts: knownHosts,
		authSvc:    authSvc,
	}
}

type installKeyRequest struct {
	KeyType  string `json:"keyType"`
	Password string `json:"password"`
}

type rotateKeyRequest struct {
	KeyType string `json:"keyType"`
}

type authTestResult struct {
	Success    bool   `json:"success"`
	AuthMethod string `json:"authMethod"`
	LatencyMs  int    `json:"latencyMs"`
	Message    string `json:"message,omitempty"`
}

// RegisterRoutes registers all key-management routes on the mux.
// The /api/servers/{id}/... patterns are more specific than the existing
// /api/servers/ prefix handler, so they coexist cleanly.
func (h *KeyHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers/{id}/install-key", h.handleInstallKey)
	mux.HandleFunc("/api/servers/{id}/verify-key", h.handleVerifyKey)
	mux.HandleFunc("/api/servers/{id}/rotate-key", h.handleRotateKey)
	mux.HandleFunc("/api/servers/{id}/fingerprint", h.handleGetFingerprint)
	mux.HandleFunc("/api/servers/{id}/test-auth", h.handleTestAuth)
	mux.HandleFunc("/api/servers/{id}/history", h.handleGetConnectionHistory)
	mux.HandleFunc("/api/servers/{id}/metrics", h.handleGetConnectionMetrics)
	mux.HandleFunc("/api/servers/{id}/remove-password", h.handleRemovePassword)
	mux.HandleFunc("/api/servers/{id}/clear-key", h.handleClearSSHKey)
	mux.HandleFunc("/api/servers/{id}/auth-status", h.handleGetAuthStatus)
	mux.HandleFunc("/api/ssh-agent/status", h.handleGetSSHAgentStatus)
}

func (h *KeyHandler) handleInstallKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	shared.LimitRequestBody(r)
	var req installKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	keyType := normalizeKeyType(req.KeyType)
	start := time.Now()
	client, err := h.connectToServerWithPassword(serverID, req.Password)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, KeyInstallResult{Success: false, Message: err.Error(), AlreadyInstalled: false})
		return
	}
	defer client.Close()

	privateKey, publicKey, err := modssh.GenerateKeyPairWithAlgorithm(keyType)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyInstallResult{Success: false, Message: fmt.Sprintf("failed to generate SSH key pair: %v", err), AlreadyInstalled: false})
		return
	}

	fingerprint, err := modssh.FingerprintSHA256(publicKey)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyInstallResult{Success: false, Message: fmt.Sprintf("failed to calculate fingerprint: %v", err), AlreadyInstalled: false})
		return
	}

	if err := modssh.InstallPublicKey(client, publicKey); err != nil {
		shared.WriteJSON(w, http.StatusOK, KeyInstallResult{Success: false, Message: fmt.Sprintf("failed to install SSH key: %v", err), AlreadyInstalled: false})
		return
	}

	privateKeyStr := string(privateKey)
	authMethod := modssh.AuthMethodKey
	credentialStatus := modssh.CredentialStatusValid
	if err := h.serverSvc.Update(serverID, UpdateRequest{
		SSHKey:           &privateKeyStr,
		KeyType:          &keyType,
		AuthMethod:       &authMethod,
		CredentialStatus: &credentialStatus,
		Fingerprint:      &fingerprint,
	}); err != nil {
		_ = modssh.RemovePublicKey(client, publicKey)
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		shared.WriteJSON(w, http.StatusInternalServerError, KeyInstallResult{Success: false, Message: fmt.Sprintf("failed to store SSH key: %v", err), AlreadyInstalled: false})
		return
	}

	_ = h.serverSvc.RecordConnection(serverID, true, int(time.Since(start).Milliseconds()), "SSH key installed", shared.ExtractIP(r), fingerprint, authMethod)

	shared.WriteJSON(w, http.StatusOK, KeyInstallResult{
		Success:          true,
		Message:          "SSH key installed successfully",
		Fingerprint:      fingerprint,
		KeyType:          keyType,
		AlreadyInstalled: false,
	})
}

func (h *KeyHandler) handleVerifyKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	_, sshKey, passphrase, fingerprint, err := h.getStoredKeyMaterial(serverID)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, KeyVerifyResult{Success: false, Message: err.Error(), Fingerprint: fingerprint})
		return
	}

	client, err := h.connectToServerUsingKey(serverID)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		shared.WriteJSON(w, http.StatusOK, KeyVerifyResult{Success: false, Message: fmt.Sprintf("failed to connect using SSH key: %v", err), Fingerprint: fingerprint})
		return
	}
	defer client.Close()

	publicKey, err := publicKeyFromPrivateKey(sshKey, passphrase)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyVerifyResult{Success: false, Message: fmt.Sprintf("failed to derive public key: %v", err), Fingerprint: fingerprint})
		return
	}

	installed, err := modssh.IsKeyInstalled(client, publicKey)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, KeyVerifyResult{Success: false, Message: fmt.Sprintf("failed to verify SSH key installation: %v", err), Fingerprint: fingerprint})
		return
	}
	if !installed {
		shared.WriteJSON(w, http.StatusOK, KeyVerifyResult{Success: false, Message: "SSH key is not installed on the remote server", Fingerprint: fingerprint})
		return
	}

	shared.WriteJSON(w, http.StatusOK, KeyVerifyResult{Success: true, Message: "SSH key verified successfully", Fingerprint: fingerprint, Installed: true})
}

func (h *KeyHandler) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	shared.LimitRequestBody(r)
	var req rotateKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	keyType := normalizeKeyType(req.KeyType)
	_, oldSSHKey, oldPassphrase, oldFingerprint, err := h.getStoredKeyMaterial(serverID)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, KeyRotationResult{Success: false, Message: err.Error(), OldFingerprint: oldFingerprint})
		return
	}

	oldPublicKey, err := publicKeyFromPrivateKey(oldSSHKey, oldPassphrase)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to derive existing public key: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	client, err := h.connectToServerUsingKey(serverID)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		shared.WriteJSON(w, http.StatusOK, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to connect using existing SSH key: %v", err), OldFingerprint: oldFingerprint})
		return
	}
	defer client.Close()

	newPrivateKey, newPublicKey, err := modssh.GenerateKeyPairWithAlgorithm(keyType)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to generate SSH key pair: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	newFingerprint, err := modssh.FingerprintSHA256(newPublicKey)
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to calculate fingerprint: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	if err := modssh.InstallPublicKey(client, newPublicKey); err != nil {
		shared.WriteJSON(w, http.StatusOK, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to install new SSH key: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	if err := modssh.RemovePublicKey(client, oldPublicKey); err != nil {
		_ = modssh.RemovePublicKey(client, newPublicKey)
		shared.WriteJSON(w, http.StatusOK, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to remove old SSH key: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	newPrivateKeyStr := string(newPrivateKey)
	authMethod := modssh.AuthMethodKey
	credentialStatus := modssh.CredentialStatusValid
	if err := h.serverSvc.Update(serverID, UpdateRequest{
		SSHKey:           &newPrivateKeyStr,
		KeyType:          &keyType,
		AuthMethod:       &authMethod,
		CredentialStatus: &credentialStatus,
		Fingerprint:      &newFingerprint,
	}); err != nil {
		_ = modssh.InstallPublicKey(client, oldPublicKey)
		_ = modssh.RemovePublicKey(client, newPublicKey)
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		shared.WriteJSON(w, http.StatusInternalServerError, KeyRotationResult{Success: false, Message: fmt.Sprintf("failed to store rotated SSH key: %v", err), OldFingerprint: oldFingerprint})
		return
	}

	shared.WriteJSON(w, http.StatusOK, KeyRotationResult{
		Success:        true,
		Message:        "SSH key rotated successfully",
		OldFingerprint: oldFingerprint,
		NewFingerprint: newFingerprint,
		KeyType:        keyType,
	})
}

func (h *KeyHandler) handleGetFingerprint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	_, sshKey, passphrase, _, err := h.getStoredKeyMaterial(serverID)
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get SSH key", "INTERNAL")
		return
	}

	srv, err := h.getRawServer(serverID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	publicKey, err := publicKeyFromPrivateKey(sshKey, passphrase)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to derive public key", "INTERNAL")
		return
	}

	sha, err := modssh.FingerprintSHA256(publicKey)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to calculate SHA256 fingerprint", "INTERNAL")
		return
	}
	md5, err := modssh.FingerprintMD5(publicKey)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to calculate MD5 fingerprint", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, FingerprintResult{SHA256: sha, MD5: md5, KeyType: srv.KeyType})
}

func (h *KeyHandler) handleTestAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	start := time.Now()
	srv, err := h.getRawServer(serverID)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	cfg, err := h.prepareConnectionConfig(serverID, connectionModeConfigured, "", make(map[int]struct{}))
	if err != nil {
		if isLockedError(err) {
			shared.WriteError(w, http.StatusForbidden, "app is locked", "LOCKED")
			return
		}
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteJSON(w, http.StatusOK, authTestResult{Success: false, AuthMethod: "", LatencyMs: int(time.Since(start).Milliseconds()), Message: err.Error()})
		return
	}

	if h.knownHosts == nil {
		shared.WriteError(w, http.StatusInternalServerError, "known hosts store is not configured", "INTERNAL")
		return
	}

	client, err := modssh.Connect(*cfg, h.knownHosts.MakeHostKeyCallback(serverID))
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		_ = h.serverSvc.UpdateAuthStatus(serverID, cfg.AuthMethod, modssh.CredentialStatusAuthFailed, srv.Fingerprint)
		_ = h.serverSvc.RecordConnection(serverID, false, latencyMs, err.Error(), shared.ExtractIP(r), srv.Fingerprint, cfg.AuthMethod)
		shared.WriteJSON(w, http.StatusOK, authTestResult{Success: false, AuthMethod: cfg.AuthMethod, LatencyMs: latencyMs, Message: err.Error()})
		return
	}
	defer client.Close()

	_ = h.serverSvc.UpdateAuthStatus(serverID, cfg.AuthMethod, modssh.CredentialStatusValid, srv.Fingerprint)
	_ = h.serverSvc.RecordConnection(serverID, true, latencyMs, "authentication successful", shared.ExtractIP(r), srv.Fingerprint, cfg.AuthMethod)
	shared.WriteJSON(w, http.StatusOK, authTestResult{Success: true, AuthMethod: cfg.AuthMethod, LatencyMs: latencyMs})
}

func (h *KeyHandler) handleGetConnectionHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if _, err := h.serverSvc.GetByID(serverID); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	entries, err := h.serverSvc.GetConnectionHistory(serverID, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get connection history", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, entries)
}

func (h *KeyHandler) handleGetConnectionMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if _, err := h.serverSvc.GetByID(serverID); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	metrics, err := h.serverSvc.GetConnectionMetrics(serverID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get connection metrics", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, metrics)
}

func (h *KeyHandler) handleGetSSHAgentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	socket := os.Getenv("SSH_AUTH_SOCK")
	status := SSHAgentStatus{Available: false, Socket: socket, Identities: 0, Fingerprints: []string{}}
	if socket == "" {
		shared.WriteJSON(w, http.StatusOK, status)
		return
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, status)
		return
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, status)
		return
	}

	fingerprints := make([]string, 0, len(signers))
	for _, signer := range signers {
		pub := ssh.MarshalAuthorizedKey(signer.PublicKey())
		fp, err := modssh.FingerprintSHA256(pub)
		if err != nil {
			continue
		}
		fingerprints = append(fingerprints, fp)
	}

	status.Available = true
	status.Identities = len(signers)
	status.Fingerprints = fingerprints
	shared.WriteJSON(w, http.StatusOK, status)
}

func (h *KeyHandler) handleRemovePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if err := h.serverSvc.RemovePassword(serverID); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to remove password", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *KeyHandler) handleClearSSHKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if err := h.serverSvc.ClearSSHKey(serverID); err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to clear SSH key", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *KeyHandler) handleGetAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	serverID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || serverID <= 0 {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	srv, err := h.getRawServer(serverID)
	if err != nil {
		if isNotFoundError(err) {
			shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	authMethod := strings.TrimSpace(strings.ToLower(srv.AuthMethod))
	if authMethod == "" {
		if srv.SSHKey != "" {
			authMethod = modssh.AuthMethodKey
		} else {
			authMethod = modssh.AuthMethodPassword
		}
	}

	credentialStatus := srv.CredentialStatus
	if credentialStatus == "" {
		credentialStatus = modssh.CredentialStatusUnknown
	}

	status := AuthStatus{
		ServerID:         srv.ID,
		AuthMethod:       authMethod,
		CredentialStatus: credentialStatus,
		HasPassword:      srv.Password != "",
		HasSSHKey:        srv.SSHKey != "",
		HasPassphrase:    srv.Passphrase != "",
		Fingerprint:      srv.Fingerprint,
		KeyType:          srv.KeyType,
		LastSuccess:      srv.LastSuccess,
		LastFailure:      srv.LastFailure,
		FailureCount:     srv.FailureCount,
		SuccessCount:     srv.SuccessCount,
	}

	shared.WriteJSON(w, http.StatusOK, status)
}

func (h *KeyHandler) connectToServer(serverID int) (*modssh.Client, error) {
	return h.connectToServerWithMode(serverID, connectionModeConfigured, "")
}

func (h *KeyHandler) connectToServerWithPassword(serverID int, passwordOverride string) (*modssh.Client, error) {
	return h.connectToServerWithMode(serverID, connectionModePassword, passwordOverride)
}

func (h *KeyHandler) connectToServerUsingKey(serverID int) (*modssh.Client, error) {
	return h.connectToServerWithMode(serverID, connectionModeKey, "")
}

func (h *KeyHandler) connectToServerWithMode(serverID int, mode connectionMode, passwordOverride string) (*modssh.Client, error) {
	cfg, err := h.prepareConnectionConfig(serverID, mode, passwordOverride, make(map[int]struct{}))
	if err != nil {
		return nil, err
	}
	if h.knownHosts == nil {
		return nil, errors.New("known hosts store is nil")
	}
	return modssh.Connect(*cfg, h.knownHosts.MakeHostKeyCallback(serverID))
}

func (h *KeyHandler) prepareConnectionConfig(serverID int, mode connectionMode, passwordOverride string, seen map[int]struct{}) (*modssh.ServerConfig, error) {
	if h.serverSvc == nil || h.serverSvc.repo == nil {
		return nil, errors.New("server service is not configured")
	}

	if _, ok := seen[serverID]; ok {
		return nil, fmt.Errorf("bastion cycle detected for server %d", serverID)
	}
	seen[serverID] = struct{}{}
	defer delete(seen, serverID)

	srv, err := h.getRawServer(serverID)
	if err != nil {
		return nil, err
	}

	password := ""
	sshKey := ""
	passphrase := ""
	if !(mode == connectionModePassword && passwordOverride != "") {
		password, sshKey, passphrase, err = h.decryptCredentials(srv)
		if err != nil {
			return nil, err
		}
	}

	if passwordOverride != "" {
		password = passwordOverride
	}

	authMethod := ""
	switch mode {
	case connectionModePassword:
		authMethod = modssh.AuthMethodPassword
		if password == "" {
			return nil, errors.New("no password credential available")
		}
	case connectionModeKey:
		authMethod = modssh.AuthMethodKey
		if sshKey == "" {
			return nil, errors.New("no SSH key credential available")
		}
	case connectionModeConfigured:
		authMethod, err = resolveConfiguredAuthMethod(srv.AuthMethod, password, sshKey)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown connection mode: %d", mode)
	}

	port := srv.Port
	if port == 0 {
		port = 22
	}

	cfg := &modssh.ServerConfig{
		ID:         srv.ID,
		Host:       srv.Host,
		Port:       port,
		Username:   srv.Username,
		AuthMethod: authMethod,
		KeyType:    srv.KeyType,
		Timeouts:   modssh.DefaultTimeouts,
	}

	switch authMethod {
	case modssh.AuthMethodPassword:
		cfg.Password = password
	case modssh.AuthMethodKey:
		cfg.PrivateKey = []byte(sshKey)
		cfg.Passphrase = passphrase
	case modssh.AuthMethodAgent:
		cfg.UseAgent = true
	case modssh.AuthMethodKeyboardInteractive:
		cfg.Password = password
		cfg.KeyboardInteractive = true
	default:
		cfg.Password = password
	}

	if srv.BastionID > 0 {
		bastion, err := h.prepareBastionConfig(srv.BastionID, seen)
		if err != nil {
			return nil, err
		}
		cfg.Bastion = bastion
	}

	return cfg, nil
}

func (h *KeyHandler) prepareBastionConfig(serverID int, seen map[int]struct{}) (*modssh.BastionConfig, error) {
	if h.knownHosts == nil {
		return nil, errors.New("known hosts store is nil")
	}

	cfg, err := h.prepareConnectionConfig(serverID, connectionModeConfigured, "", seen)
	if err != nil {
		return nil, err
	}
	if cfg.UseAgent && len(cfg.PrivateKey) == 0 && cfg.Password == "" {
		return nil, errors.New("agent authentication is not supported for bastion connections")
	}

	port := cfg.Port
	if port == 0 {
		port = 22
	}

	return &modssh.BastionConfig{
		Host:            cfg.Host,
		Port:            port,
		Username:        cfg.Username,
		Password:        cfg.Password,
		PrivateKey:      cfg.PrivateKey,
		Passphrase:      cfg.Passphrase,
		HostKeyCallback: h.knownHosts.MakeHostKeyCallback(serverID),
	}, nil
}

func (h *KeyHandler) getRawServer(serverID int) (*Server, error) {
	if h.serverSvc == nil || h.serverSvc.repo == nil {
		return nil, errors.New("server service is not configured")
	}
	srv, err := h.serverSvc.repo.GetByID(serverID)
	if err != nil {
		return nil, err
	}
	return srv, nil
}

func (h *KeyHandler) decryptCredentials(srv *Server) (password, sshKey, passphrase string, err error) {
	if srv == nil {
		return "", "", "", errors.New("server is nil")
	}
	if h.authSvc == nil {
		return "", "", "", errors.New("app is locked")
	}
	key := h.authSvc.GetAESKey()
	if key == nil {
		return "", "", "", errors.New("app is locked")
	}

	password, err = decryptCredential(key, srv.Password)
	if err != nil {
		return "", "", "", err
	}
	sshKey, err = decryptCredential(key, srv.SSHKey)
	if err != nil {
		return "", "", "", err
	}
	passphrase, err = decryptCredential(key, srv.Passphrase)
	if err != nil {
		return "", "", "", err
	}
	return password, sshKey, passphrase, nil
}

func (h *KeyHandler) getStoredKeyMaterial(serverID int) (password, sshKey, passphrase, fingerprint string, err error) {
	srv, err := h.getRawServer(serverID)
	if err != nil {
		return "", "", "", "", err
	}
	password, sshKey, passphrase, err = h.decryptCredentials(srv)
	if err != nil {
		return "", "", "", "", err
	}
	return password, sshKey, passphrase, srv.Fingerprint, nil
}

func resolveConfiguredAuthMethod(storedMethod, password, sshKey string) (string, error) {
	method := strings.ToLower(strings.TrimSpace(storedMethod))
	switch method {
	case "":
		if sshKey != "" {
			return modssh.AuthMethodKey, nil
		}
		if password != "" {
			return modssh.AuthMethodPassword, nil
		}
		return "", errors.New("no credentials available")
	case modssh.AuthMethodPassword:
		if password == "" {
			return "", errors.New("no password credential available")
		}
		return modssh.AuthMethodPassword, nil
	case modssh.AuthMethodKey:
		if sshKey == "" {
			return "", errors.New("no SSH key credential available")
		}
		return modssh.AuthMethodKey, nil
	case modssh.AuthMethodAgent:
		return modssh.AuthMethodAgent, nil
	case modssh.AuthMethodKeyboardInteractive:
		if password == "" {
			return "", errors.New("no password credential available for keyboard-interactive auth")
		}
		return modssh.AuthMethodKeyboardInteractive, nil
	default:
		if sshKey != "" {
			return modssh.AuthMethodKey, nil
		}
		if password != "" {
			return modssh.AuthMethodPassword, nil
		}
		return "", fmt.Errorf("unsupported auth method %q", storedMethod)
	}
}

func normalizeKeyType(keyType string) string {
	method := strings.ToLower(strings.TrimSpace(keyType))
	switch method {
	case string(modssh.KeyTypeRSA):
		return string(modssh.KeyTypeRSA)
	case string(modssh.KeyTypeECDSA):
		return string(modssh.KeyTypeECDSA)
	default:
		return string(modssh.KeyTypeED25519)
	}
}

func publicKeyFromPrivateKey(privateKey, passphrase string) ([]byte, error) {
	if privateKey == "" {
		return nil, errors.New("private key is empty")
	}

	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(privateKey))
	}
	if err != nil {
		return nil, err
	}

	return ssh.MarshalAuthorizedKey(signer.PublicKey()), nil
}

func decryptCredential(key []byte, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	decrypted, err := shared.Decrypt(key, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func isLockedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "app is locked")
}
