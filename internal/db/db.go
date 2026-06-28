package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite database at the given path with WAL mode enabled
// for concurrent read/write support. It configures:
//   - WAL journal mode: allows readers to proceed while a write is in progress
//   - busy_timeout: wait up to 5s for a lock instead of failing immediately
//   - synchronous=NORMAL: safe with WAL, better performance than FULL
//   - foreign_keys=ON: enforce referential integrity
//
// For file-based databases, it allows multiple connections for concurrent
// reads. For in-memory databases (used in tests), it uses a single connection
// with shared cache.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if path == ":memory:" {
		dsn = "file:meshium?mode=memory&cache=shared"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// For in-memory databases, keep a single connection to maintain the
	// shared cache. For file-based databases, allow multiple connections
	// for concurrent reads (WAL mode supports this).
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		// Allow multiple readers but limit to a reasonable number.
		// SQLite WAL mode supports concurrent readers with a single writer.
		db.SetMaxOpenConns(10)
	}

	// Configure pragmas
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000", // 5 seconds
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to set %s: %w", pragma, err)
		}
	}

	return db, nil
}
