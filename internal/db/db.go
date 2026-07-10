package db

import (
	"database/sql"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// pragmaQuery is the set of pragmas applied to every new connection in the pool.
// These MUST live in the DSN, not a post-Open db.Exec: pragmas are per-connection
// state, and database/sql may open additional conns that never inherit an Exec'd
// pragma. Exec-ing "PRAGMA foreign_keys = ON" only set it on one pool conn, so
// DELETE FROM migrations (which relies on ON DELETE CASCADE) hit conns with FKs
// OFF and silently left orphaned migration_steps rows bloating the DB. The
// _pragma query param runs on every conn the driver opens.
//
// journal_mode=WAL and synchronous=NORMAL are persistent (stored in the DB file
// header), so they only need setting once — but including them in the DSN is
// harmless and keeps all config in one place.
const pragmaQuery = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

// Open opens a SQLite database at the given path with WAL mode enabled
// for concurrent read/write support. Pragmas are set per-connection via the
// DSN (see pragmaQuery) so every conn in the pool enforces foreign keys.
func Open(path string) (*sql.DB, error) {
	var dsn string
	if path == ":memory:" {
		dsn = "file:meshium?mode=memory&cache=shared&" + pragmaQuery
	} else {
		// For a file path, embed it as a file:// URI so the _pragma query
		// params attach cleanly (modernc/sqlite accepts query params only on
		// URI-form DSNs). Percent-encode the path.
		u := &url.URL{Scheme: "file", Path: path}
		dsn = u.String()
		if strings.Contains(dsn, "?") {
			dsn += "&" + pragmaQuery
		} else {
			dsn += "?" + pragmaQuery
		}
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

	return db, nil
}
