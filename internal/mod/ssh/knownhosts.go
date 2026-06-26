package ssh

import (
	"bytes"
	"database/sql"
	"errors"
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
		host,
		port,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
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
