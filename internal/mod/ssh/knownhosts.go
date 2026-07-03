package ssh

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

var ErrHostKeyNotTrusted = errors.New("host key not trusted — first connection requires explicit trust")
var ErrHostKeyMismatch = errors.New("host key mismatch — possible MITM attack")

// KnownHostsStore persists host keys in SQLite and provides SSH host key callbacks.
type KnownHostsStore struct {
	db *sql.DB
}

// NewKnownHostsStore creates a known hosts store backed by the supplied database.
func NewKnownHostsStore(db *sql.DB) *KnownHostsStore {
	return &KnownHostsStore{db: db}
}

type serverEndpoint struct {
	host     string
	port     int
	username string
}

func (s *KnownHostsStore) getServerEndpoint(serverID int) (*serverEndpoint, error) {
	var ep serverEndpoint
	if err := s.db.QueryRow(
		`SELECT host, port, username FROM servers WHERE id = ?`,
		serverID,
	).Scan(&ep.host, &ep.port, &ep.username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("server not found")
		}
		return nil, err
	}
	if ep.port == 0 {
		ep.port = 22
	}
	return &ep, nil
}

func parseAuthorizedKeyFingerprint(keyText string) (string, error) {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keyText))
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pubKey), nil
}

func (s *KnownHostsStore) loadKnownHost(host string, port int) (key string, verified bool, found bool, err error) {
	var verifiedInt int
	err = s.db.QueryRow(
		"SELECT host_key, COALESCE(verified, 0) FROM known_hosts WHERE host = ? AND port = ?",
		host,
		port,
	).Scan(&key, &verifiedInt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return key, verifiedInt == 1, true, nil
}

// Get returns the stored host key for host:port.
// If the host is not known, it returns an empty key and false.
func (s *KnownHostsStore) Get(host string, port int) (string, bool, error) {
	key, _, found, err := s.loadKnownHost(host, port)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	return key, true, nil
}

// Save stores or updates a known host entry.
func (s *KnownHostsStore) Save(host string, port int, key string, serverID int) error {
	var serverIDValue any
	if serverID > 0 {
		var exists int
		if err := s.db.QueryRow("SELECT 1 FROM servers WHERE id = ?", serverID).Scan(&exists); err == nil {
			serverIDValue = serverID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	_, err := s.db.Exec(
		`INSERT INTO known_hosts (server_id, host_key, host, port, verified)
		 VALUES (?, ?, ?, ?, 1)
		 ON CONFLICT(host, port) DO UPDATE SET
			server_id = excluded.server_id,
			host_key = excluded.host_key,
			verified = 1`,
		serverIDValue,
		key,
		host,
		port,
	)
	return err
}

// MakeHostKeyCallback returns an ssh.HostKeyCallback that validates the remote key
// against the database-backed known hosts store. Unknown or unverified host keys
// are rejected until they are explicitly trusted.
// The serverID associates the host key with the server record.
func (s *KnownHostsStore) MakeHostKeyCallback(serverID int) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		port := 22
		if tcpAddr, ok := remote.(*net.TCPAddr); ok {
			port = tcpAddr.Port
		}

		storedKey, verified, found, err := s.loadKnownHost(hostname, port)
		if err != nil {
			return err
		}
		if !found || !verified {
			return ErrHostKeyNotTrusted
		}

		storedPubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(storedKey))
		if err != nil {
			return err
		}
		if !bytes.Equal(storedPubKey.Marshal(), key.Marshal()) {
			return ErrHostKeyMismatch
		}

		return nil
	}
}

// TrustHostKey connects to the configured server with host key verification disabled,
// captures the host key presented by the server, persists it as trusted, and returns
// the fingerprint.
func (s *KnownHostsStore) TrustHostKey(serverID int) (string, error) {
	ep, err := s.getServerEndpoint(serverID)
	if err != nil {
		return "", err
	}

	addr := net.JoinHostPort(ep.host, fmt.Sprintf("%d", ep.port))
	var observedKey ssh.PublicKey
	insecure := ssh.InsecureIgnoreHostKey()
	cfg := &ssh.ClientConfig{
		User: ep.username,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			observedKey = key
			return insecure(hostname, remote, key)
		},
		Timeout: 10 * time.Second,
	}

	client, err := ssh.Dial("tcp", addr, cfg)
	if client != nil {
		defer client.Close()
	}
	if observedKey == nil {
		if err != nil {
			return "", err
		}
		return "", errors.New("failed to capture host key")
	}

	keyText := string(ssh.MarshalAuthorizedKey(observedKey))
	if err := s.Save(ep.host, ep.port, keyText, serverID); err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(observedKey), nil
}

// GetFingerprint returns the stored fingerprint for the given server ID.
func (s *KnownHostsStore) GetFingerprint(serverID int) (string, error) {
	var key string
	err := s.db.QueryRow(
		`SELECT host_key FROM known_hosts WHERE server_id = ? ORDER BY created_at DESC LIMIT 1`,
		serverID,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("host key not found")
	}
	if err != nil {
		return "", err
	}
	return parseAuthorizedKeyFingerprint(key)
}

// IsTrusted reports whether a host:port entry exists and is marked verified.
func (s *KnownHostsStore) IsTrusted(host string, port int) (bool, error) {
	var verified int
	err := s.db.QueryRow(
		`SELECT COALESCE(verified, 0) FROM known_hosts WHERE host = ? AND port = ?`,
		host,
		port,
	).Scan(&verified)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return verified == 1, nil
}
