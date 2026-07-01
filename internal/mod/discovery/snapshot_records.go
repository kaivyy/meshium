package discovery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// SnapshotRecord stores a discovery snapshot row plus its decoded payload.
type SnapshotRecord struct {
	ID        int             `json:"id"`
	ServerID  int             `json:"serverId"`
	CapturedAt time.Time       `json:"capturedAt"`
	Snapshot   *ServerSnapshot `json:"snapshot,omitempty"`
}

// ListSnapshots returns all snapshots for a server ordered from oldest to newest.
func (s *SQLiteSnapshotStore) ListSnapshots(serverID int) ([]SnapshotRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, server_id, snapshot, captured_at
		 FROM discovery_snapshots
		 WHERE server_id = ?
		 ORDER BY captured_at ASC, id ASC`,
		serverID,
	)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()

	records := make([]SnapshotRecord, 0)
	for rows.Next() {
		var record SnapshotRecord
		var raw string
		if err := rows.Scan(&record.ID, &record.ServerID, &raw, &record.CapturedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot record: %w", err)
		}

		var snapshot ServerSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot %d: %w", record.ID, err)
		}
		record.Snapshot = &snapshot
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	return records, nil
}

// GetSnapshotByID returns a single snapshot record by discovery_snapshots.id.
func (s *SQLiteSnapshotStore) GetSnapshotByID(snapshotID string) (*SnapshotRecord, error) {
	id, err := strconv.Atoi(snapshotID)
	if err != nil {
		return nil, fmt.Errorf("invalid snapshot id %q: %w", snapshotID, err)
	}

	var record SnapshotRecord
	var raw string
	err = s.db.QueryRow(
		`SELECT id, server_id, snapshot, captured_at
		 FROM discovery_snapshots
		 WHERE id = ?`,
		id,
	).Scan(&record.ID, &record.ServerID, &raw, &record.CapturedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("snapshot %d not found", id)
		}
		return nil, fmt.Errorf("get snapshot %d: %w", id, err)
	}

	var snapshot ServerSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot %d: %w", id, err)
	}
	record.Snapshot = &snapshot
	return &record, nil
}

// GetLatestSnapshotRecord returns the newest snapshot record for a server.
func (s *SQLiteSnapshotStore) GetLatestSnapshotRecord(serverID int) (*SnapshotRecord, error) {
	var record SnapshotRecord
	var raw string
	err := s.db.QueryRow(
		`SELECT id, server_id, snapshot, captured_at
		 FROM discovery_snapshots
		 WHERE server_id = ?
		 ORDER BY captured_at DESC, id DESC
		 LIMIT 1`,
		serverID,
	).Scan(&record.ID, &record.ServerID, &raw, &record.CapturedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no snapshot found for server %d", serverID)
		}
		return nil, fmt.Errorf("get latest snapshot for server %d: %w", serverID, err)
	}

	var snapshot ServerSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal latest snapshot %d: %w", record.ID, err)
	}
	record.Snapshot = &snapshot
	return &record, nil
}

// ListSnapshots returns an error for the no-op store.
func (s *NoopSnapshotStore) ListSnapshots(serverID int) ([]SnapshotRecord, error) {
	return nil, fmt.Errorf("no snapshots for server %d (noop store)", serverID)
}

// GetSnapshotByID returns an error for the no-op store.
func (s *NoopSnapshotStore) GetSnapshotByID(snapshotID string) (*SnapshotRecord, error) {
	return nil, fmt.Errorf("snapshot %s not found (noop store)", snapshotID)
}

// GetLatestSnapshotRecord returns an error for the no-op store.
func (s *NoopSnapshotStore) GetLatestSnapshotRecord(serverID int) (*SnapshotRecord, error) {
	return nil, fmt.Errorf("no snapshot found for server %d (noop store)", serverID)
}
