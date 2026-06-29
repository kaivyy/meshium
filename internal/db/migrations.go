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
			bastion_id  INTEGER DEFAULT 0,
			auth_method TEXT DEFAULT '',
			credential_status TEXT DEFAULT 'unknown',
			last_success TEXT,
			last_failure TEXT,
			failure_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			fingerprint TEXT,
			key_type    TEXT DEFAULT 'rsa',
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
		// migration_checkpoints stores per-step checkpoint state for resume.
		// Each row records that a specific step has been verified and can be
		// skipped on resume. The checkpoint is written AFTER Verify succeeds
		// and cleared if Rollback reverts the step.
		`CREATE TABLE IF NOT EXISTS migration_checkpoints (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			step_name    TEXT NOT NULL,
			step_index   INTEGER NOT NULL,
			state        TEXT NOT NULL DEFAULT 'verified',
			checkpoint_data TEXT,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(migration_id, step_name)
		);`,
		// migration_job_steps stores the ordered list of steps for a migration
		// job, with per-step state tracking (pending, preparing, prepared,
		// applying, applied, verifying, verified, rolling_back, rolled_back, failed).
		`CREATE TABLE IF NOT EXISTS migration_job_steps (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			step_name    TEXT NOT NULL,
			step_index   INTEGER NOT NULL,
			step_type    TEXT NOT NULL DEFAULT 'category',
			state        TEXT NOT NULL DEFAULT 'pending',
			prepare_data TEXT,
			apply_data   TEXT,
			verify_data  TEXT,
			error        TEXT,
			started_at   DATETIME,
			completed_at DATETIME,
			UNIQUE(migration_id, step_name)
		);`,
		// connection_history records each SSH connection attempt for auditing
		// and credential health tracking.
		`CREATE TABLE IF NOT EXISTS connection_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id   INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			success     INTEGER NOT NULL,
			duration_ms INTEGER,
			reason      TEXT,
			remote_ip   TEXT,
			fingerprint TEXT,
			auth_method TEXT,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		// server_keys stores multiple SSH keys per server for key rotation
		// and multi-key support.
		`CREATE TABLE IF NOT EXISTS server_keys (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id   INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name        TEXT NOT NULL DEFAULT 'default',
			key_type    TEXT NOT NULL DEFAULT 'rsa',
			public_key  TEXT NOT NULL,
			private_key TEXT,
			fingerprint TEXT,
			priority    INTEGER DEFAULT 0,
			enabled     INTEGER DEFAULT 1,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_connection_history_server_id ON connection_history(server_id);`,
		`CREATE INDEX IF NOT EXISTS idx_server_keys_server_id ON server_keys(server_id);`,
		`CREATE INDEX IF NOT EXISTS idx_servers_auth_method ON servers(auth_method);`,
		`CREATE INDEX IF NOT EXISTS idx_servers_credential_status ON servers(credential_status);`,
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

	// Add columns if they don't exist (for existing databases)
	// Use PRAGMA table_info to check which columns already exist
	existingColumns := map[string]struct{}{}
	rows, err := tx.Query(`PRAGMA table_info(servers)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		existingColumns[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	alterStatements := []struct {
		column    string
		statement string
	}{
		{column: "bastion_id", statement: `ALTER TABLE servers ADD COLUMN bastion_id INTEGER DEFAULT 0`},
		{column: "auth_method", statement: `ALTER TABLE servers ADD COLUMN auth_method TEXT DEFAULT ''`},
		{column: "credential_status", statement: `ALTER TABLE servers ADD COLUMN credential_status TEXT DEFAULT 'unknown'`},
		{column: "last_success", statement: `ALTER TABLE servers ADD COLUMN last_success TEXT`},
		{column: "last_failure", statement: `ALTER TABLE servers ADD COLUMN last_failure TEXT`},
		{column: "failure_count", statement: `ALTER TABLE servers ADD COLUMN failure_count INTEGER DEFAULT 0`},
		{column: "success_count", statement: `ALTER TABLE servers ADD COLUMN success_count INTEGER DEFAULT 0`},
		{column: "fingerprint", statement: `ALTER TABLE servers ADD COLUMN fingerprint TEXT`},
		{column: "key_type", statement: `ALTER TABLE servers ADD COLUMN key_type TEXT DEFAULT 'rsa'`},
	}

	for _, alter := range alterStatements {
		if _, ok := existingColumns[alter.column]; ok {
			continue
		}
		if _, err := tx.Exec(alter.statement); err != nil {
			return err
		}
	}

	// Add state column to migrations for typed state machine (Phase 2).
	// Stores the MigrationState string representation alongside the existing
	// status column for backward compatibility.
	migrationColumns := map[string]struct{}{}
	rows, err = tx.Query(`PRAGMA table_info(migrations)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		migrationColumns[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	if _, ok := migrationColumns["state"]; !ok {
		tx.Exec(`ALTER TABLE migrations ADD COLUMN state TEXT DEFAULT ''`)
	}

	return tx.Commit()
}
