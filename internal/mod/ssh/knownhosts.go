package ssh

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// KnownHostsStore persists host keys in SQLite and provides SSH host key callbacks.
type KnownHostsStore struct {
	db *sql.DB
}

// NewKnownHostsStore creates a known hosts store backed by the supplied database.
func NewKnownHostsStore(db *sql.DB) *KnownHostsStore {
	return &KnownHostsStore{db: db}
}

// Get returns the stored host key for host:port.
// If the host is not known, it returns an empty key and false.
func (s *KnownHostsStore) Get(host string, port int) (string, bool, error) {
	var key string
	err := s.db.QueryRow(
		"SELECT host_key FROM known_hosts WHERE host = ? AND port = ?",
		host, port,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

// GetServerKey returns the stored host key for a specific server ID.
func (s *KnownHostsStore) GetServerKey(serverID int) (string, bool, error) {
	if serverID <= 0 {
		return "", false, nil
	}

	var key string
	err := s.db.QueryRow(
		`SELECT host_key FROM known_hosts
		 WHERE server_id = ?
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT 1`,
		serverID,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

// GetWithFingerprint returns the stored host key with fingerprint information.
func (s *KnownHostsStore) GetWithFingerprint(host string, port int) (*HostKeyResult, error) {
	var hostKey, fpSHA256, fpMD5, algorithm, status string
	var bits int

	err := s.db.QueryRow(
		`SELECT host_key, COALESCE(fingerprint_sha256, ''), COALESCE(fingerprint_md5, ''),
		 COALESCE(algorithm, ''), COALESCE(bits, 0), COALESCE(status, 'unknown')
		 FROM known_hosts WHERE host = ? AND port = ?`,
		host, port,
	).Scan(&hostKey, &fpSHA256, &fpMD5, &algorithm, &bits, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return &HostKeyResult{Known: false, Match: false, Key: ""}, nil
	}
	if err != nil {
		return nil, err
	}

	return &HostKeyResult{
		Known:             true,
		Match:             true,
		Key:               hostKey,
		FingerprintSHA256: fpSHA256,
		FingerprintMD5:    fpMD5,
		Algorithm:         algorithm,
		Bits:              bits,
	}, nil
}

// AddServerKey stores or updates a known host entry with the given server ID.
func (s *KnownHostsStore) AddServerKey(serverID int, host string, port int, key string) error {
	return s.upsertKnownHost(host, port, key, serverID, nil, nil, nil, nil)
}

// Save stores or updates a known host entry.
func (s *KnownHostsStore) Save(host string, port int, key string, serverID int) error {
	return s.AddServerKey(serverID, host, port, key)
}

// SaveWithFingerprint stores a known host entry with full fingerprint metadata.
func (s *KnownHostsStore) SaveWithFingerprint(host string, port int, key string, serverID int, fingerprintSHA256, fingerprintMD5, algorithm string, bits int) error {
	return s.upsertKnownHost(host, port, key, serverID, &fingerprintSHA256, &fingerprintMD5, &algorithm, &bits)
}

// RemoveServerKey removes all known host entries associated with a server ID.
func (s *KnownHostsStore) RemoveServerKey(serverID int) error {
	if serverID <= 0 {
		return nil
	}

	_, err := s.db.Exec("DELETE FROM known_hosts WHERE server_id = ?", serverID)
	return err
}

// RecordHostKeyChange logs a host key change event for audit purposes.
func (s *KnownHostsStore) RecordHostKeyChange(host string, port int, oldFingerprint, newFingerprint, riskLevel, actionTaken string, serverID int) error {
	var serverIDValue any
	if serverID > 0 {
		serverIDValue = serverID
	}
	_, err := s.db.Exec(
		`INSERT INTO host_key_changes (host, port, old_fingerprint, new_fingerprint, risk_level, action_taken, server_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		host, port, oldFingerprint, newFingerprint, riskLevel, actionTaken, serverIDValue,
	)
	return err
}

// ListKnownHosts returns all known host entries.
func (s *KnownHostsStore) ListKnownHosts() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(
		`SELECT kh.host, kh.port, kh.host_key, COALESCE(kh.fingerprint_sha256, ''),
		 COALESCE(kh.fingerprint_md5, ''), COALESCE(kh.algorithm, ''),
		 COALESCE(kh.bits, 0), COALESCE(kh.status, 'unknown'), COALESCE(kh.verified, 0),
		 COALESCE(kh.server_id, 0), kh.created_at, COALESCE(kh.updated_at, kh.created_at)
		 FROM known_hosts kh ORDER BY kh.host`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []map[string]interface{}
	for rows.Next() {
		var host, key, fpSHA256, fpMD5, algorithm, status, createdAt, updatedAt string
		var port, bits, verified, serverID int
		if err := rows.Scan(&host, &port, &key, &fpSHA256, &fpMD5, &algorithm, &bits, &status, &verified, &serverID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		hosts = append(hosts, map[string]interface{}{
			"host":              host,
			"port":              port,
			"hostKey":           key,
			"fingerprintSha256": fpSHA256,
			"fingerprintMd5":    fpMD5,
			"algorithm":         algorithm,
			"bits":              bits,
			"status":            status,
			"verified":          verified == 1,
			"serverId":          serverID,
			"createdAt":         createdAt,
			"updatedAt":         updatedAt,
		})
	}
	return hosts, nil
}

// RemoveKnownHost removes a known host entry.
func (s *KnownHostsStore) RemoveKnownHost(host string, port int) error {
	res, err := s.db.Exec("DELETE FROM known_hosts WHERE host = ? AND port = ?", host, port)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("known host not found")
	}
	return nil
}

// SetHostStatus updates the status of a known host entry.
func (s *KnownHostsStore) SetHostStatus(host string, port int, status string) error {
	_, err := s.db.Exec(
		"UPDATE known_hosts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE host = ? AND port = ?",
		status, host, port,
	)
	return err
}

// MakeHostKeyCallback returns an ssh.HostKeyCallback that validates the remote key
// against the database-backed known hosts store.
func (s *KnownHostsStore) MakeHostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		port := 22
		if tcpAddr, ok := remote.(*net.TCPAddr); ok {
			port = tcpAddr.Port
		}

		storedKey, known, err := s.Get(hostname, port)
		if err != nil {
			return err
		}
		if !known {
			return errors.New("host key not found — needs verification")
		}

		storedPubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(storedKey))
		if err != nil {
			return err
		}
		if !bytes.Equal(storedPubKey.Marshal(), key.Marshal()) {
			return errors.New("host key mismatch — possible MITM")
		}

		return nil
	}
}

// MakeTOFUCallback returns an ssh.HostKeyCallback that implements Trust On First Use.
// If the host is unknown, it automatically trusts and saves the key.
// If the host is known and the key matches, it succeeds.
// If the host is known and the key differs, it returns an error.
func (s *KnownHostsStore) MakeTOFUCallback(serverID int) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		port := 22
		if tcpAddr, ok := remote.(*net.TCPAddr); ok {
			port = tcpAddr.Port
		}

		storedKey, known, err := s.Get(hostname, port)
		if err != nil {
			return err
		}

		keyStr := string(ssh.MarshalAuthorizedKey(key))
		fpSHA256 := ssh.FingerprintSHA256(key)
		fpMD5 := ssh.FingerprintLegacyMD5(key)

		if !known {
			// Trust on first use: save the key
			return s.SaveWithFingerprint(hostname, port, keyStr, serverID, fpSHA256, fpMD5, key.Type(), 0)
		}

		storedPubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(storedKey))
		if err != nil {
			return err
		}
		if !bytes.Equal(storedPubKey.Marshal(), key.Marshal()) {
			// Record the change for audit
			oldFP := ssh.FingerprintSHA256(storedPubKey)
			_ = s.RecordHostKeyChange(hostname, port, oldFP, fpSHA256, "high", "rejected", serverID)
			_ = s.SetHostStatus(hostname, port, HostStatusChanged)
			return fmt.Errorf("host key mismatch — possible MITM. Old: %s, New: %s", oldFP, fpSHA256)
		}

		return nil
	}
}

// MakeInsecureCallback returns an ssh.HostKeyCallback that accepts any host key.
// This is used for initial connection testing before the user has verified the host.
func MakeInsecureCallback() ssh.HostKeyCallback {
	return ssh.InsecureIgnoreHostKey()
}

func (s *KnownHostsStore) upsertKnownHost(host string, port int, key string, serverID int, fingerprintSHA256, fingerprintMD5, algorithm *string, bits *int) error {
	serverIDValue, err := s.resolveServerIDValue(serverID)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO known_hosts (server_id, host_key, host, port, verified, fingerprint_sha256, fingerprint_md5, algorithm, bits, status)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, 'verified')
		 ON CONFLICT(host, port) DO UPDATE SET
			server_id = excluded.server_id,
			host_key = excluded.host_key,
			verified = 1,
			fingerprint_sha256 = excluded.fingerprint_sha256,
			fingerprint_md5 = excluded.fingerprint_md5,
			algorithm = excluded.algorithm,
			bits = excluded.bits,
			status = 'verified',
			updated_at = CURRENT_TIMESTAMP`,
		serverIDValue,
		key,
		host,
		port,
		nullableString(fingerprintSHA256),
		nullableString(fingerprintMD5),
		nullableString(algorithm),
		nullableInt(bits),
	)
	return err
}

func (s *KnownHostsStore) resolveServerIDValue(serverID int) (any, error) {
	if serverID <= 0 {
		return nil, nil
	}

	var exists int
	err := s.db.QueryRow("SELECT 1 FROM servers WHERE id = ?", serverID).Scan(&exists)
	if err == nil {
		return serverID, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return nil, err
}

func nullableString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil || *value == 0 {
		return nil
	}
	return *value
}
