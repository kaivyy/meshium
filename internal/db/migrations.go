package db

import "database/sql"

// Migrate runs all database migrations.
func Migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS app_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS servers (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL,
			description TEXT,
			host        TEXT NOT NULL,
			port        INTEGER NOT NULL DEFAULT 22,
			username    TEXT NOT NULL,
			password    TEXT,
			ssh_key     TEXT,
			passphrase  TEXT,
			tags        TEXT,
			environment TEXT,
			region      TEXT,
			icon        TEXT,
			color       TEXT,
			favorite    INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS server_info (
			server_id      INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			ssh_status     TEXT,
			latency_ms     INTEGER,
			cpu_model      TEXT,
			cpu_cores      INTEGER,
			ram_total_mb   INTEGER,
			disk_total_gb  REAL,
			kernel         TEXT,
			architecture   TEXT,
			os             TEXT,
			virtualization TEXT,
			provider       TEXT,
			public_ip      TEXT,
			private_ip     TEXT,
			timezone       TEXT,
			hostname       TEXT,
			raw_data       TEXT,
			last_checked   DATETIME,
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS known_hosts (
			server_id  INTEGER REFERENCES servers(id) ON DELETE CASCADE,
			host_key   TEXT NOT NULL,
			host       TEXT NOT NULL,
			port       INTEGER NOT NULL,
			verified   INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(host, port)
		);`,
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}

	return tx.Commit()
}
