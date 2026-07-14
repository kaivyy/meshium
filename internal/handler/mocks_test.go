package handler

import (
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
)

// mockSnapshotStore is a lightweight in-memory SnapshotStore used by the
// handler tests. It was previously defined alongside the now-removed
// PlanHandler tests; kept here so discovery/compat handler tests still build.
type mockSnapshotStore struct {
	snapshots map[int]*discovery.ServerSnapshot
}

func newMockSnapshotStore() *mockSnapshotStore {
	return &mockSnapshotStore{snapshots: make(map[int]*discovery.ServerSnapshot)}
}

func (m *mockSnapshotStore) SaveSnapshot(serverID int, snapshot *discovery.ServerSnapshot) error {
	m.snapshots[serverID] = snapshot
	return nil
}
func (m *mockSnapshotStore) LoadSnapshot(serverID int) (*discovery.ServerSnapshot, error) {
	s, ok := m.snapshots[serverID]
	if !ok {
		return nil, errSnapshotNotFound
	}
	return s, nil
}
func (m *mockSnapshotStore) LoadSnapshotAt(serverID int, _ time.Time) (*discovery.ServerSnapshot, error) {
	return m.LoadSnapshot(serverID)
}
func (m *mockSnapshotStore) DeleteSnapshot(serverID int) error {
	delete(m.snapshots, serverID)
	return nil
}

var errSnapshotNotFound = fmt.Errorf("snapshot not found")
