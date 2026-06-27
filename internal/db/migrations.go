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
		`CREATE TABLE IF NOT EXISTS migrations (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id    INTEGER NOT NULL REFERENCES servers(id),
			target_id    INTEGER NOT NULL REFERENCES servers(id),
			categories   TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'planned',
			plan         TEXT,
			error        TEXT,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS migration_steps (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			category     TEXT NOT NULL,
			action       TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'pending',
			data         TEXT,
			output       TEXT,
			error        TEXT,
			started_at   DATETIME,
			completed_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS migration_backups (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			server_id    INTEGER NOT NULL REFERENCES servers(id),
			category     TEXT NOT NULL,
			backup_path  TEXT NOT NULL,
			backup_type  TEXT NOT NULL,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
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
