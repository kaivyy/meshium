package discovery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SnapshotStore is the interface for persisting and retrieving snapshots.
type SnapshotStore interface {
	// SaveSnapshot stores a snapshot for a server.
	SaveSnapshot(serverID int, snapshot *ServerSnapshot) error
	// LoadSnapshot retrieves the latest snapshot for a server.
	LoadSnapshot(serverID int) (*ServerSnapshot, error)
	// LoadSnapshotAt retrieves a snapshot for a server at a specific time.
	// Returns the most recent snapshot at or before the given time.
	LoadSnapshotAt(serverID int, at time.Time) (*ServerSnapshot, error)
	// DeleteSnapshot removes all snapshots for a server.
	DeleteSnapshot(serverID int) error
}

// --- SQLite Snapshot Store ---

// SQLiteSnapshotStore stores snapshots in SQLite as JSON.
type SQLiteSnapshotStore struct {
	db *sql.DB
}

// NewSQLiteSnapshotStore creates a new SQLiteSnapshotStore.
func NewSQLiteSnapshotStore(db *sql.DB) *SQLiteSnapshotStore {
	return &SQLiteSnapshotStore{db: db}
}

// EnsureTable creates the discovery_snapshots table if it doesn't exist.
func (s *SQLiteSnapshotStore) EnsureTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS discovery_snapshots (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id   INTEGER NOT NULL,
		snapshot    TEXT NOT NULL,
		captured_at DATETIME NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	return err
}

// SaveSnapshot stores a snapshot for a server.
func (s *SQLiteSnapshotStore) SaveSnapshot(serverID int, snapshot *ServerSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	capturedAt := snapshot.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}

	_, err = s.db.Exec(
		`INSERT INTO discovery_snapshots (server_id, snapshot, captured_at) VALUES (?, ?, ?)`,
		serverID, string(data), capturedAt,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	return nil
}

// LoadSnapshot retrieves the latest snapshot for a server.
func (s *SQLiteSnapshotStore) LoadSnapshot(serverID int) (*ServerSnapshot, error) {
	var data string
	err := s.db.QueryRow(
		`SELECT snapshot FROM discovery_snapshots WHERE server_id = ? ORDER BY captured_at DESC, id DESC LIMIT 1`,
		serverID,
	).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no snapshot found for server %d", serverID)
		}
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	var snapshot ServerSnapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return &snapshot, nil
}

// LoadSnapshotAt retrieves a snapshot for a server at a specific time.
func (s *SQLiteSnapshotStore) LoadSnapshotAt(serverID int, at time.Time) (*ServerSnapshot, error) {
	var data string
	err := s.db.QueryRow(
		`SELECT snapshot FROM discovery_snapshots WHERE server_id = ? AND captured_at <= ? ORDER BY captured_at DESC, id DESC LIMIT 1`,
		serverID, at,
	).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no snapshot found for server %d at %s", serverID, at)
		}
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	var snapshot ServerSnapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return &snapshot, nil
}

// DeleteSnapshot removes all snapshots for a server.
func (s *SQLiteSnapshotStore) DeleteSnapshot(serverID int) error {
	_, err := s.db.Exec(
		`DELETE FROM discovery_snapshots WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return fmt.Errorf("delete snapshots: %w", err)
	}
	return nil
}

// --- Noop Snapshot Store ---

// NoopSnapshotStore is a no-op implementation for testing.
type NoopSnapshotStore struct{}

func NewNoopSnapshotStore() *NoopSnapshotStore { return &NoopSnapshotStore{} }

func (s *NoopSnapshotStore) SaveSnapshot(serverID int, snapshot *ServerSnapshot) error { return nil }
func (s *NoopSnapshotStore) LoadSnapshot(serverID int) (*ServerSnapshot, error) {
	return nil, fmt.Errorf("no snapshot (noop store)")
}
func (s *NoopSnapshotStore) LoadSnapshotAt(serverID int, at time.Time) (*ServerSnapshot, error) {
	return nil, fmt.Errorf("no snapshot (noop store)")
}
func (s *NoopSnapshotStore) DeleteSnapshot(serverID int) error { return nil }
