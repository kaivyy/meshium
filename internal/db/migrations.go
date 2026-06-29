package db

import (
	"database/sql"
	"fmt"
)

// Migrate runs all database migrations.
// Migrations are additive and backward compatible:
//   - New tables use CREATE TABLE IF NOT EXISTS
//   - New columns on existing tables use ALTER TABLE ADD COLUMN with safe defaults
//   - Column additions are guarded by a column-existence check to avoid errors on re-run
func Migrate(db *sql.DB) error {
	statements := []string{
		// ── Original tables (unchanged) ──────────────────────────────────
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

		// ── Enterprise SSH: new tables ──────────────────────────────────

		// connection_history records every SSH connection attempt with full
		// diagnostic metadata (auth method, cipher, KEX, latency, etc.).
		`CREATE TABLE IF NOT EXISTS connection_history (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id       INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			hostname         TEXT,
			ip              TEXT,
			username        TEXT,
			auth_method     TEXT,
			key_fingerprint TEXT,
			agent_used      INTEGER DEFAULT 0,
			bastion_id      INTEGER REFERENCES servers(id) ON DELETE SET NULL,
			success         INTEGER NOT NULL,
			duration_ms     INTEGER,
			latency_ms      INTEGER,
			exit_status     INTEGER,
			failure_reason  TEXT,
			cipher          TEXT,
			kex             TEXT,
			compression     TEXT,
			remote_banner   TEXT,
			timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// server_keys stores multiple SSH private keys per server (one-to-many).
		// Each key has a label, type, priority, enabled flag, and metadata.
		`CREATE TABLE IF NOT EXISTS server_keys (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id       INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			label           TEXT NOT NULL DEFAULT '',
			key_type        TEXT NOT NULL DEFAULT 'rsa',
			private_key     TEXT NOT NULL,
			public_key      TEXT,
			fingerprint     TEXT,
			passphrase      TEXT,
			notes           TEXT,
			enabled         INTEGER DEFAULT 1,
			is_default      INTEGER DEFAULT 0,
			priority        INTEGER DEFAULT 0,
			last_used       DATETIME,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// auth_priority stores the user-configured order of authentication
		// methods. The backend tries methods in this order during connection.
		`CREATE TABLE IF NOT EXISTS auth_priority (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			method     TEXT NOT NULL UNIQUE,
			priority   INTEGER NOT NULL DEFAULT 0,
			enabled    INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// connection_profiles stores preset configurations for different
		// use cases (interactive, migration, database, long-running, etc.).
		`CREATE TABLE IF NOT EXISTS connection_profiles (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL UNIQUE,
			description     TEXT,
			timeout_seconds INTEGER DEFAULT 30,
			retry_count     INTEGER DEFAULT 3,
			retry_delay_ms  INTEGER DEFAULT 1000,
			backoff_strategy TEXT DEFAULT 'exponential',
			keepalive_seconds INTEGER DEFAULT 30,
			reconnect_enabled INTEGER DEFAULT 1,
			buffer_size_kb  INTEGER DEFAULT 64,
			compression     INTEGER DEFAULT 0,
			parallelism     INTEGER DEFAULT 1,
			is_builtin       INTEGER DEFAULT 0,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// retry_config stores per-server retry strategy configuration.
		`CREATE TABLE IF NOT EXISTS retry_config (
			server_id         INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			retry_count       INTEGER DEFAULT 3,
			retry_delay_ms    INTEGER DEFAULT 1000,
			backoff_strategy  TEXT DEFAULT 'exponential',
			jitter_ms         INTEGER DEFAULT 0,
			reconnect_policy  TEXT DEFAULT 'always',
			auth_retry_order  TEXT,
			updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// host_key_changes tracks every time a host key changes (audit log
		// for TOFU / MITM detection).
		`CREATE TABLE IF NOT EXISTS host_key_changes (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			host            TEXT NOT NULL,
			port            INTEGER NOT NULL,
			old_fingerprint TEXT,
			new_fingerprint TEXT,
			risk_level      TEXT,
			action_taken    TEXT,
			server_id       INTEGER REFERENCES servers(id) ON DELETE SET NULL,
			timestamp       DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// credential_audit logs every access to decrypted credentials.
		`CREATE TABLE IF NOT EXISTS credential_audit (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id  INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			action     TEXT NOT NULL,
			detail     TEXT,
			ip         TEXT,
			timestamp  DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// ssh_agent_config stores SSH agent configuration per server.
		`CREATE TABLE IF NOT EXISTS ssh_agent_config (
			server_id          INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			use_agent          INTEGER DEFAULT 0,
			preferred_identity TEXT,
			agent_forwarding   INTEGER DEFAULT 0,
			updated_at         DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// server_settings stores per-server SSH configuration overrides.
		`CREATE TABLE IF NOT EXISTS server_settings (
			server_id          INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			profile_id         INTEGER REFERENCES connection_profiles(id) ON DELETE SET NULL,
			touf_mode          TEXT DEFAULT 'always',  -- always, once, reject
			keepalive_seconds  INTEGER DEFAULT 30,
			compression_enabled INTEGER DEFAULT 0,
			custom_ciphers     TEXT,
			custom_kex          TEXT,
			updated_at         DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	// ── Column additions for existing tables ──────────────────────────
	// SQLite does not support ALTER TABLE ADD COLUMN IF NOT EXISTS, so we
	// guard each addition with a column-existence check.

	newColumns := []struct {
		table string
		col   string
		ddl   string
	}{
		// servers table additions
		{"servers", "auth_method", "TEXT DEFAULT 'password'"},
		{"servers", "credential_status", "TEXT DEFAULT 'unknown'"},
		{"servers", "fingerprint", "TEXT"},
		{"servers", "key_type", "TEXT DEFAULT 'rsa'"},
		{"servers", "last_success", "DATETIME"},
		{"servers", "last_failure", "DATETIME"},
		{"servers", "failure_count", "INTEGER DEFAULT 0"},
		{"servers", "success_count", "INTEGER DEFAULT 0"},
		{"servers", "bastion_id", "INTEGER REFERENCES servers(id) ON DELETE SET NULL"},

		// known_hosts table additions
		{"known_hosts", "fingerprint_sha256", "TEXT"},
		{"known_hosts", "fingerprint_md5", "TEXT"},
		{"known_hosts", "algorithm", "TEXT"},
		{"known_hosts", "bits", "INTEGER"},
		{"known_hosts", "status", "TEXT DEFAULT 'unknown'"}, // verified, unknown, changed
		{"known_hosts", "updated_at", "DATETIME DEFAULT CURRENT_TIMESTAMP"},

		// migrations table additions
		{"migrations", "rolled_back_at", "DATETIME"},
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migration statement failed: %w", err)
		}
	}

	// Add new columns to existing tables (guarded by existence check).
	for _, nc := range newColumns {
		exists, err := columnExists(tx, nc.table, nc.col)
		if err != nil {
			return fmt.Errorf("checking column %s.%s: %w", nc.table, nc.col, err)
		}
		if exists {
			continue
		}
		alterStmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", nc.table, nc.col, nc.ddl)
		if _, err := tx.Exec(alterStmt); err != nil {
			return fmt.Errorf("adding column %s.%s: %w", nc.table, nc.col, err)
		}
	}

	// Seed default auth priority entries if the table is empty.
	seedAuthPriority := []struct {
		method   string
		priority int
	}{
		{"agent", 1},
		{"ed25519", 2},
		{"rsa", 3},
		{"ecdsa", 4},
		{"keyboard-interactive", 5},
		{"password", 6},
	}
	for _, ap := range seedAuthPriority {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO auth_priority (method, priority, enabled) VALUES (?, ?, 1)`,
			ap.method, ap.priority,
		)
		if err != nil {
			return fmt.Errorf("seeding auth_priority %s: %w", ap.method, err)
		}
	}

	// Seed default connection profiles if the table is empty.
	seedProfiles := []struct {
		name        string
		desc        string
		timeout     int
		retry       int
		retryDelay  int
		backoff     string
		keepalive   int
		reconnect   int
		bufferKB    int
		compression int
		parallel    int
	}{
		{"Interactive", "Optimized for interactive shell sessions", 30, 2, 500, "exponential", 30, 1, 64, 0, 1},
		{"Migration", "Optimized for server migration operations", 60, 3, 1000, "exponential", 60, 1, 128, 1, 4},
		{"Database", "Optimized for database connections", 45, 5, 2000, "exponential", 60, 1, 256, 0, 1},
		{"Long Running", "For long-running operations with keepalive", 120, 1, 5000, "linear", 15, 1, 256, 1, 1},
		{"Package Install", "For package installation operations", 90, 2, 1000, "exponential", 30, 0, 128, 0, 1},
		{"Docker", "Optimized for Docker operations", 60, 3, 1000, "exponential", 30, 1, 128, 0, 2},
		{"Monitoring", "For monitoring and health checks", 15, 5, 500, "exponential", 10, 1, 64, 0, 1},
		{"High Latency", "For high-latency connections (satellite, etc.)", 120, 5, 3000, "exponential", 60, 1, 256, 1, 1},
		{"Satellite", "For satellite connections with extreme latency", 300, 10, 5000, "exponential", 120, 1, 512, 1, 1},
		{"Backup", "For backup operations", 180, 2, 2000, "linear", 60, 0, 256, 1, 1},
	}
	for _, p := range seedProfiles {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO connection_profiles
			 (name, description, timeout_seconds, retry_count, retry_delay_ms, backoff_strategy,
			  keepalive_seconds, reconnect_enabled, buffer_size_kb, compression, parallelism, is_builtin)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			p.name, p.desc, p.timeout, p.retry, p.retryDelay, p.backoff,
			p.keepalive, p.reconnect, p.bufferKB, p.compression, p.parallel,
		)
		if err != nil {
			return fmt.Errorf("seeding connection profile %s: %w", p.name, err)
		}
	}

	return tx.Commit()
}

// columnExists checks whether a column exists in a table within the given
// transaction. It queries SQLite's pragma_table_info to determine this.
func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	var count int
	err := tx.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
		table, column,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
