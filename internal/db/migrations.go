package db

import (
	"database/sql"
	"strings"
)

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
			auth_method TEXT,
			credential_status TEXT DEFAULT 'unknown',
			last_success DATETIME,
			last_failure DATETIME,
			failure_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			fingerprint TEXT DEFAULT '',
			key_type TEXT DEFAULT 'rsa',
			tags        TEXT,
			environment TEXT,
			region      TEXT,
			icon        TEXT,
			color       TEXT,
			favorite    INTEGER DEFAULT 0,
			bastion_id  INTEGER DEFAULT 0,
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
			status     TEXT DEFAULT 'unknown',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME,
			UNIQUE(host, port)
		);`,
		`CREATE TABLE IF NOT EXISTS server_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			label TEXT NOT NULL,
			key_type TEXT DEFAULT '',
			public_key TEXT DEFAULT '',
			fingerprint TEXT DEFAULT '',
			notes TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			is_default INTEGER DEFAULT 0,
			priority INTEGER DEFAULT 0,
			last_used DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS auth_priority (
			method TEXT PRIMARY KEY,
			priority INTEGER NOT NULL,
			enabled INTEGER DEFAULT 1
		);`,
		`CREATE TABLE IF NOT EXISTS connection_profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			timeout_seconds INTEGER DEFAULT 30,
			retry_count INTEGER DEFAULT 3,
			retry_delay_ms INTEGER DEFAULT 1000,
			backoff_strategy TEXT DEFAULT 'exponential',
			keepalive_seconds INTEGER DEFAULT 30,
			reconnect_enabled INTEGER DEFAULT 1,
			buffer_size_kb INTEGER DEFAULT 64,
			compression INTEGER DEFAULT 0,
			parallelism INTEGER DEFAULT 1,
			is_builtin INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS retry_configs (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			retry_count INTEGER DEFAULT 3,
			retry_delay_ms INTEGER DEFAULT 1000,
			backoff_strategy TEXT DEFAULT 'exponential',
			jitter_ms INTEGER DEFAULT 0,
			reconnect_policy TEXT DEFAULT 'always',
			auth_retry_order TEXT DEFAULT 'agent,ed25519,rsa,ecdsa,password',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		// connection_history records each SSH attempt for the history/metrics
		// dashboards. Dropped from this slice during the July 2026 enterprise-auth
		// merge while repo.go kept writing to it — fresh databases then failed
		// every recorded connection with "no such table".
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
		`CREATE INDEX IF NOT EXISTS idx_connection_history_server_id ON connection_history(server_id)`,
		`CREATE TABLE IF NOT EXISTS host_key_changes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			old_fingerprint TEXT DEFAULT '',
			new_fingerprint TEXT DEFAULT '',
			risk_level TEXT DEFAULT 'medium',
			action_taken TEXT DEFAULT '',
			server_id INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS credential_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			detail TEXT DEFAULT '',
			ip TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS ssh_agent_configs (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			use_agent INTEGER DEFAULT 0,
			preferred_identity TEXT DEFAULT '',
			agent_forwarding INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
			completed_at DATETIME,
			operation_id TEXT DEFAULT ''
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
		// Phase 6B selective-apply: operator decisions per comparable item.
		// One row per (migration_id, item_key). Survives reload/reconnect/reopen
		// (SQL), auditable (rows = decision log), and supports optimistic FE
		// updates (PUT upserts). See parity spec §I.3.
		`CREATE TABLE IF NOT EXISTS migration_selections (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			item_key     TEXT NOT NULL,
			category     TEXT NOT NULL,
			action       TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(migration_id, item_key)
		);`,
		// Phase 6C-BE: per-item execution + verification evidence. One row per
		// (migration_id, item_key) — the honest source of truth that FE badges
		// and terminal status read. Applied != verified is enforced here by keeping
		// the two states in separate columns. See 6C contract §F.3/F.4.
		`CREATE TABLE IF NOT EXISTS migration_item_results (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id     INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			item_key         TEXT NOT NULL,
			category         TEXT NOT NULL DEFAULT '',
			execution_state  TEXT NOT NULL DEFAULT 'not_applicable',
			last_execution_at DATETIME,
			step_refs        TEXT DEFAULT '[]',
			execution_notes  TEXT DEFAULT '',
			verification_state TEXT NOT NULL DEFAULT 'not_verified',
			verification_level TEXT NOT NULL DEFAULT '',
			verify_evidence  TEXT DEFAULT '',
			verify_notes     TEXT DEFAULT '',
			backup_ref       TEXT DEFAULT '',
			created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(migration_id, item_key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_item_results_migration ON migration_item_results(migration_id, execution_state)`,
		// Phase 6C-BE: append-only audit of decision changes. Never updated,
		// only inserted — gives a tamper-evident record of why an item moved
		// from one action to another. See 6C contract §F.2.
		`CREATE TABLE IF NOT EXISTS migration_selection_history (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			item_key     TEXT NOT NULL,
			from_action  TEXT NOT NULL DEFAULT '',
			to_action    TEXT NOT NULL DEFAULT '',
			reason       TEXT DEFAULT '',
			actor        TEXT DEFAULT 'operator',
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sel_history_migration ON migration_selection_history(migration_id, item_key)`,
		`CREATE TABLE IF NOT EXISTS migration_backups (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			server_id    INTEGER NOT NULL REFERENCES servers(id),
			category     TEXT NOT NULL,
			backup_path  TEXT NOT NULL,
			backup_type  TEXT NOT NULL,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
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
		`CREATE TABLE IF NOT EXISTS migration_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL,
			sequence INTEGER NOT NULL,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			level TEXT NOT NULL DEFAULT 'info',
			stage TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			details TEXT DEFAULT '{}',
			source TEXT NOT NULL DEFAULT '',
			correlation_id TEXT DEFAULT '',
			FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS migration_rollback_steps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL,
			step_order INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			error TEXT DEFAULT '',
			started_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS migration_verifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL,
			component TEXT NOT NULL,
			check_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			message TEXT DEFAULT '',
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS migration_freezes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL,
			method TEXT NOT NULL,
			frozen_dbs TEXT DEFAULT '[]',
			frozen_apps TEXT DEFAULT '[]',
			errors TEXT DEFAULT '[]',
			success INTEGER NOT NULL DEFAULT 0,
			started_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY (migration_id) REFERENCES migrations(id) ON DELETE CASCADE
		);`,

		// --- Zero-Downtime Pipeline Tables ---

		// migration_stages tracks the 14-stage pipeline progress.
		// Each row represents a single stage execution with its own state,
		// checkpoint, retry count, and metrics.
		`CREATE TABLE IF NOT EXISTS migration_stages (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			stage_name      TEXT NOT NULL,
			stage_index     INTEGER NOT NULL,
			state           TEXT NOT NULL DEFAULT 'pending',
			attempt_count   INTEGER NOT NULL DEFAULT 0,
			checkpoint_data TEXT,
			result_data     TEXT,
			error           TEXT,
			started_at      DATETIME,
			completed_at    DATETIME,
			UNIQUE(migration_id, stage_name)
		);`,

		// replication_status tracks database/Redis replication setup.
		`CREATE TABLE IF NOT EXISTS replication_status (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			database_type   TEXT NOT NULL,
			database_name   TEXT,
			source_host     TEXT,
			target_host     TEXT,
			replication_mode TEXT NOT NULL DEFAULT 'none',
			replication_lag INTEGER DEFAULT 0,
			status          TEXT NOT NULL DEFAULT 'pending',
			last_error      TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// traffic_switch_config stores traffic switch configuration and state.
		`CREATE TABLE IF NOT EXISTS traffic_switch_config (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			provider        TEXT NOT NULL,
			original_config TEXT,
			new_config      TEXT,
			switch_state    TEXT NOT NULL DEFAULT 'pending',
			health_check_url TEXT,
			rollback_config TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// health_history stores health check results over time.
		`CREATE TABLE IF NOT EXISTS health_history (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			server_id       INTEGER NOT NULL REFERENCES servers(id),
			check_type      TEXT NOT NULL,
			check_target    TEXT,
			status          TEXT NOT NULL,
			response_time_ms INTEGER,
			status_code     INTEGER,
			error_message   TEXT,
			health_score    REAL,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// cutover_history records cutover events.
		`CREATE TABLE IF NOT EXISTS cutover_history (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			cutover_type    TEXT NOT NULL,
			previous_state  TEXT,
			new_state       TEXT,
			freeze_write    INTEGER DEFAULT 0,
			queue_drained   INTEGER DEFAULT 0,
			delta_synced    INTEGER DEFAULT 0,
			health_verified INTEGER DEFAULT 0,
			traffic_switched INTEGER DEFAULT 0,
			rollback_triggered INTEGER DEFAULT 0,
			error           TEXT,
			started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at    DATETIME
		);`,

		// rollback_history records rollback events.
		`CREATE TABLE IF NOT EXISTS rollback_history (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			rollback_type   TEXT NOT NULL,
			stage_name      TEXT,
			category        TEXT,
			traffic_reverted INTEGER DEFAULT 0,
			dns_reverted    INTEGER DEFAULT 0,
			db_role_reverted INTEGER DEFAULT 0,
			redis_role_reverted INTEGER DEFAULT 0,
			queue_resumed   INTEGER DEFAULT 0,
			containers_reverted INTEGER DEFAULT 0,
			configs_reverted INTEGER DEFAULT 0,
			success         INTEGER DEFAULT 0,
			error           TEXT,
			started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at    DATETIME
		);`,

		// sync_session tracks rsync/data transfer sessions.
		`CREATE TABLE IF NOT EXISTS sync_session (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			sync_type       TEXT NOT NULL,
			source_path     TEXT,
			target_path     TEXT,
			bytes_transferred INTEGER DEFAULT 0,
			bytes_total     INTEGER DEFAULT 0,
			files_transferred INTEGER DEFAULT 0,
			files_total     INTEGER DEFAULT 0,
			speed_bytes_sec INTEGER DEFAULT 0,
			checksum_verified INTEGER DEFAULT 0,
			status          TEXT NOT NULL DEFAULT 'pending',
			error           TEXT,
			started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at    DATETIME
		);`,

		// transfer_session tracks individual file transfer operations.
		`CREATE TABLE IF NOT EXISTS transfer_session (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			sync_session_id INTEGER REFERENCES sync_session(id) ON DELETE CASCADE,
			file_path       TEXT NOT NULL,
			file_size       INTEGER DEFAULT 0,
			bytes_transferred INTEGER DEFAULT 0,
			checksum_source TEXT,
			checksum_target TEXT,
			transfer_method TEXT,
			status          TEXT NOT NULL DEFAULT 'pending',
			error           TEXT,
			started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at    DATETIME
		);`,

		// verification_result stores verification check results.
		`CREATE TABLE IF NOT EXISTS verification_result (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			verification_type TEXT NOT NULL,
			target          TEXT,
			expected        TEXT,
			actual          TEXT,
			passed          INTEGER NOT NULL DEFAULT 0,
			error_message   TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// risk_report stores the risk assessment for a migration.
		`CREATE TABLE IF NOT EXISTS risk_report (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			risk_score      REAL NOT NULL DEFAULT 0,
			risk_class      TEXT NOT NULL DEFAULT 'low',
			downtime_estimate TEXT,
			data_size_bytes INTEGER DEFAULT 0,
			database_size_bytes INTEGER DEFAULT 0,
			container_count INTEGER DEFAULT 0,
			volume_count    INTEGER DEFAULT 0,
			rollback_complexity TEXT,
			details         TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// audit_trail records all state transitions and significant events.
		// Phase2D-2 added the identity/evidence columns; existing deployments
		// gain them via the idempotent ALTERs in Migrate().
		`CREATE TABLE IF NOT EXISTS audit_trail (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER REFERENCES migrations(id) ON DELETE CASCADE,
			event_type      TEXT NOT NULL,
			event_data      TEXT,
			previous_state  TEXT,
			new_state       TEXT,
			actor           TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			correlation_id       TEXT DEFAULT '',
			idempotency_key     TEXT DEFAULT '',
			actor_type           TEXT DEFAULT '',
			fence_generation     INTEGER DEFAULT 0,
			fence_status         TEXT DEFAULT '',
			topology_summary     TEXT DEFAULT '',
			traffic_verify_summary TEXT DEFAULT '',
			approval_ref         TEXT DEFAULT '',
			result               TEXT DEFAULT ''
		);`,

		// migration_metrics stores time-series metrics for a migration.
		`CREATE TABLE IF NOT EXISTS migration_metrics (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			metric_name     TEXT NOT NULL,
			metric_value    REAL NOT NULL,
			metric_unit     TEXT,
			stage_name      TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// migration_queue_state tracks queue state during cutover.
		`CREATE TABLE IF NOT EXISTS migration_queue_state (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			queue_type      TEXT NOT NULL,
			queue_name      TEXT,
			paused          INTEGER DEFAULT 0,
			active_jobs     INTEGER DEFAULT 0,
			drained         INTEGER DEFAULT 0,
			synced          INTEGER DEFAULT 0,
			verified        INTEGER DEFAULT 0,
			error           TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		// migration_provision_state tracks target provisioning progress.
		`CREATE TABLE IF NOT EXISTS migration_provision_state (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			component       TEXT NOT NULL,
			installed       INTEGER DEFAULT 0,
			configured      INTEGER DEFAULT 0,
			verified        INTEGER DEFAULT 0,
			version         TEXT,
			error           TEXT,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		// migration_fence_leases is the durable fencing authority for the
		// Phase 2A fenced cutover. One active lease per migration
		// (UNIQUE(migration_id)) + monotonic fence_token. Fail-closed:
		// missing/expired/conflicting/stale token → NeedsManualIntervention.
		`CREATE TABLE IF NOT EXISTS migration_fence_leases (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			holder       TEXT NOT NULL,
			fence_token  INTEGER NOT NULL,
			state        TEXT NOT NULL,
			acquired_at  DATETIME NOT NULL,
			expires_at   DATETIME NOT NULL,
			renewed_at   DATETIME,
			released_at  DATETIME,
			UNIQUE(migration_id)
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

	// Add columns that may not exist in older databases.
	alterStatements := []string{
		`ALTER TABLE servers ADD COLUMN bastion_id INTEGER DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN auth_method TEXT`,
		`ALTER TABLE servers ADD COLUMN credential_status TEXT DEFAULT 'unknown'`,
		`ALTER TABLE servers ADD COLUMN last_success DATETIME`,
		`ALTER TABLE servers ADD COLUMN last_failure DATETIME`,
		`ALTER TABLE servers ADD COLUMN failure_count INTEGER DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN success_count INTEGER DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN fingerprint TEXT DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN key_type TEXT DEFAULT 'rsa'`,
		`ALTER TABLE migrations ADD COLUMN state TEXT DEFAULT ''`,
		`ALTER TABLE migrations ADD COLUMN operation_id TEXT DEFAULT ''`,
		// Add columns for zero-downtime migration metadata.
		`ALTER TABLE migrations ADD COLUMN config TEXT DEFAULT '{}'`,
		`ALTER TABLE migrations ADD COLUMN risk_score REAL DEFAULT 0`,
		`ALTER TABLE migrations ADD COLUMN risk_class TEXT DEFAULT 'low'`,
		`ALTER TABLE known_hosts ADD COLUMN status TEXT DEFAULT 'unknown'`,
		`ALTER TABLE known_hosts ADD COLUMN updated_at DATETIME`,
	}

	for _, stmt := range alterStatements {
		// Ignore errors since the column may already exist
		tx.Exec(stmt)
	}

	// Add indexes for common query patterns.
	indexStatements := []string{
		`CREATE INDEX IF NOT EXISTS idx_servers_favorite_name ON servers(favorite, name)`,
		`CREATE INDEX IF NOT EXISTS idx_migrations_status_created ON migrations(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_migrations_source ON migrations(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_migrations_target ON migrations(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_migrations_operation_id ON migrations(operation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_steps_migration ON migration_steps(migration_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_events_migration_seq ON migration_events(migration_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_stages_migration ON migration_stages(migration_id, stage_index)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_stages_state ON migration_stages(state)`,
		`CREATE INDEX IF NOT EXISTS idx_replication_status_migration ON replication_status(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_health_history_migration ON health_history(migration_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cutover_history_migration ON cutover_history(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_rollback_history_migration ON rollback_history(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_session_migration ON sync_session(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_session_sync ON transfer_session(sync_session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_verification_result_migration ON verification_result(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_risk_report_migration ON risk_report(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_trail_migration ON audit_trail(migration_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_metrics_migration ON migration_metrics(migration_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_queue_state_migration ON migration_queue_state(migration_id)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_provision_state_migration ON migration_provision_state(migration_id)`,
		`CREATE TABLE IF NOT EXISTS migration_plans (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			migration_id    INTEGER NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
			workloads       TEXT DEFAULT '[]',
			dependency_graph TEXT DEFAULT '{}',
			compatibility_issues TEXT DEFAULT '[]',
			strategy        TEXT DEFAULT '{}',
			warnings        TEXT DEFAULT '[]',
			risk_score      REAL DEFAULT 0,
			blocking_issues INTEGER DEFAULT 0,
			recommendation_count INTEGER DEFAULT 0,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_plans_migration ON migration_plans(migration_id)`,

		// Phase 2B: durable transfer checkpoints for resume/reconcile after a
		// backend restart. (transfer_id, file_name) is the resume key.
		`CREATE TABLE IF NOT EXISTS transfer_checkpoints (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			transfer_id          TEXT NOT NULL,
			migration_id         INTEGER NOT NULL,
			category             TEXT NOT NULL DEFAULT '',
			file_name            TEXT NOT NULL,
			strategy             TEXT NOT NULL DEFAULT '',
			mode                 TEXT NOT NULL DEFAULT 'direct',
			source_host          TEXT NOT NULL DEFAULT '',
			source_path          TEXT NOT NULL DEFAULT '',
			target_host          TEXT NOT NULL DEFAULT '',
			target_path          TEXT NOT NULL DEFAULT '',
			total_bytes          INTEGER NOT NULL DEFAULT 0,
			bytes_transferred    INTEGER NOT NULL DEFAULT 0,
			resumable            INTEGER NOT NULL DEFAULT 0,
			source_snapshot      TEXT NOT NULL DEFAULT '',
			target_partial_state TEXT NOT NULL DEFAULT '',
			checksum_source      TEXT NOT NULL DEFAULT '',
			checksum_target      TEXT NOT NULL DEFAULT '',
			last_verified_phase  TEXT NOT NULL DEFAULT '',
			started_at           DATETIME,
			updated_at           DATETIME
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tcp_transfer_file ON transfer_checkpoints(transfer_id, file_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tcp_migration ON transfer_checkpoints(migration_id)`,
	}

	for _, stmt := range indexStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	// Phase2D-2: idempotent schema extensions for existing deployments.
	// ALTER ADD COLUMN is a no-op once the column exists; sqlite reports
	// "duplicate column" — we ignore that specific error so re-running Migrate
	// is safe. Any other error still fails the migration.
	alterPhase2D := []string{
		`ALTER TABLE audit_trail ADD COLUMN correlation_id        TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN idempotency_key      TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN actor_type            TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN fence_generation      INTEGER DEFAULT 0`,
		`ALTER TABLE audit_trail ADD COLUMN fence_status          TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN topology_summary      TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN traffic_verify_summary TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN approval_ref          TEXT DEFAULT ''`,
		`ALTER TABLE audit_trail ADD COLUMN result                TEXT DEFAULT ''`,
		// Phase 6C-BE: decision metadata on selections. All nullable-defaulted
		// so rows written before this migration stay valid (backward compat).
		`ALTER TABLE migration_selections ADD COLUMN decision_reason   TEXT DEFAULT ''`,
		`ALTER TABLE migration_selections ADD COLUMN risk_acknowledged INTEGER DEFAULT 0`,
		`ALTER TABLE migration_selections ADD COLUMN manual_followup   TEXT DEFAULT ''`,
	}
	for _, stmt := range alterPhase2D {
		if _, err := tx.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	return tx.Commit()
}
