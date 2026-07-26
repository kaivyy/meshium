package db

import (
	"os"
	"path/filepath"
	"testing"
)

// The database holds the full server inventory plus encrypted credential
// blobs. SQLite creates it with the process umask (0644 in production —
// observed live on /root/.meshium/meshium.db), which exposes it to every
// local user. Open must pin the main file and its WAL/SHM siblings to 0600.
func TestOpenRestrictsDatabaseFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	conn, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(f)
		if os.IsNotExist(err) {
			continue // -shm may not exist yet; only check what does
		}
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s mode = %o, want no group/other bits (0600)", f, mode)
		}
	}
}
