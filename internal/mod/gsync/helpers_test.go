package gsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"meshium/internal/shared"
)

// --- test doubles -------------------------------------------------------------

// memStore is an in-memory Store.
type memStore struct{ m map[string]string }

func newMemStore() *memStore { return &memStore{m: map[string]string{}} }

func (m *memStore) GetConfigValue(key string) (string, error) { return m.m[key], nil }

func (m *memStore) SetConfigValue(key, value string) error {
	m.m[key] = value
	return nil
}

func (m *memStore) get(t *testing.T, key string) string {
	t.Helper()
	return m.m[key]
}

// newTestService builds a Service with a real AES key and a private temp dir.
func newTestService(t *testing.T) *Service {
	t.Helper()
	key := shared.DeriveKey("test-password", []byte("test-salt"))
	s := NewService(newMemStore(), func() []byte { return key })
	s.tempDir = t.TempDir()
	return s
}

func (s *Service) useRunner(t *testing.T) *fakeRunner {
	t.Helper()
	fr := &fakeRunner{}
	s.SetBinaryRunner(fr)
	return fr
}

func (s *Service) lastRun(t *testing.T, pairID string) RunResult {
	t.Helper()
	raw, _ := s.store.GetConfigValue(lastRunKey)
	var all map[string]RunResult
	_ = json.Unmarshal([]byte(raw), &all)
	return all[pairID]
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

var _ = context.Background

// osReadFile/osReadDir are indirections so the fake runner can inspect the temp
// config without importing io/fs everywhere in the test body above.
func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func osReadDir(dir string) ([]os.DirEntry, error) { return os.ReadDir(dir) }

var tempDir = os.TempDir() // overridden per-test via newTestService; used by TestRunSyncsPairAndRecordsResult

// compile-time interface checks
var (
	_ Store  = (*memStore)(nil)
	_ Runner = (*fakeRunner)(nil)
)

// silence unused warnings for filepath in helpers
var _ = filepath.Join
