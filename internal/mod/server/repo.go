package server

import (
	"database/sql"
	"errors"
	"strings"
)

type Repo interface {
	Create(s Server) (int, error)
	GetByID(id int) (*Server, error)
	GetRawServer(id int) (*Server, error)
	List(filter ListFilter) ([]Server, error)
	Update(id int, s Server) error
	Delete(id int) error
	ToggleFavorite(id int) error
	SaveServerInfo(serverID int, info ServerInfo, rawData string) error
	GetServerInfo(serverID int) (*ServerInfo, error)

	// Connection history
	RecordConnection(entry ConnectionHistoryEntry) error
	GetConnectionHistory(serverID int, limit int) ([]ConnectionHistoryEntry, error)
	GetConnectionMetrics(serverID int) (*ConnectionMetrics, error)
	UpdateAuthStatus(serverID int, status string, success bool) error

	// Key management
	StoreServerKey(key ServerKey) (int, error)
	GetServerKeys(serverID int) ([]ServerKey, error)
	GetServerKey(serverID int, keyID int) (*ServerKey, error)
	UpdateServerKey(key ServerKey) error
	DeleteServerKey(serverID int, keyID int) error
	SetDefaultKey(serverID int, keyID int) error

	// Auth priority
	GetAuthPriority() ([]AuthPriorityEntry, error)
	SetAuthPriority(entries []AuthPriorityEntry) error

	// Connection profiles
	ListConnectionProfiles() ([]ConnectionProfile, error)
	GetConnectionProfile(id int) (*ConnectionProfile, error)
	CreateConnectionProfile(p ConnectionProfile) (int, error)
	UpdateConnectionProfile(p ConnectionProfile) error
	DeleteConnectionProfile(id int) error

	// Retry config
	GetRetryConfig(serverID int) (*RetryConfig, error)
	SetRetryConfig(config RetryConfig) error

	// Known hosts
	ListKnownHosts() ([]KnownHostEntry, error)
	RemoveKnownHost(host string, port int) error
	SetHostStatus(host string, port int, status string) error
	ListHostKeyChanges(limit int) ([]HostKeyChange, error)

	// Credential audit
	RecordCredentialAudit(entry CredentialAuditEntry) error
	ListCredentialAudit(serverID int, limit int) ([]CredentialAuditEntry, error)

	// SSH agent config
	GetSSHAgentConfig(serverID int) (useAgent bool, preferredIdentity string, agentForwarding bool, err error)
	SetSSHAgentConfig(serverID int, useAgent bool, preferredIdentity string, agentForwarding bool) error

	// Dashboard
	GetAuthDashboard() (*AuthDashboard, error)
}

type ListFilter struct {
	Environment string
	Region      string
	Tag         string
	Query       string
}

type sqliteRepo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) Repo {
	return &sqliteRepo{db: db}
}

func (r *sqliteRepo) Create(s Server) (int, error) {
	// Use NULL for bastion_id when 0 to avoid FK constraint violation
	var bastionID any
	if s.BastionID > 0 {
		bastionID = s.BastionID
	}
	res, err := r.db.Exec(
		`INSERT INTO servers (name, description, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, auth_method, credential_status, key_type, bastion_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.Host, s.Port, s.Username,
		s.Password, s.SSHKey, s.Passphrase,
		tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, boolToInt(s.Favorite),
		s.AuthMethod, s.CredentialStatus, s.KeyType, bastionID,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) GetByID(id int) (*Server, error) {
	return r.GetRawServer(id)
}

func (r *sqliteRepo) GetRawServer(id int) (*Server, error) {
	var s Server
	var tagsJSON string
	var favorite int
	var authMethod, credentialStatus, fingerprint, keyType sql.NullString
	var bastionID sql.NullInt64
	var lastSuccess, lastFailure sql.NullString
	var failureCount, successCount sql.NullInt64

	err := r.db.QueryRow(
		`SELECT id, name, description, host, port, username, password, ssh_key, passphrase,
		 COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0),
		 COALESCE(auth_method, 'password'), COALESCE(credential_status, 'unknown'),
		 COALESCE(fingerprint, ''), COALESCE(key_type, 'rsa'), COALESCE(bastion_id, 0),
		 COALESCE(last_success, ''), COALESCE(last_failure, ''),
		 COALESCE(failure_count, 0), COALESCE(success_count, 0),
		 created_at, updated_at
		 FROM servers WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username, &s.Password, &s.SSHKey, &s.Passphrase,
		&tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite,
		&authMethod, &credentialStatus, &fingerprint, &keyType, &bastionID,
		&lastSuccess, &lastFailure, &failureCount, &successCount,
		&s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("server not found")
	}
	if err != nil {
		return nil, err
	}
	s.Tags = tagsFromJSON(tagsJSON)
	s.Favorite = favorite == 1
	s.AuthMethod = authMethod.String
	s.CredentialStatus = credentialStatus.String
	s.Fingerprint = fingerprint.String
	s.KeyType = keyType.String
	s.BastionID = int(bastionID.Int64)
	s.LastSuccess = lastSuccess.String
	s.LastFailure = lastFailure.String
	s.FailureCount = int(failureCount.Int64)
	s.SuccessCount = int(successCount.Int64)
	return &s, nil
}

func (r *sqliteRepo) List(filter ListFilter) ([]Server, error) {
	query := `SELECT id, name, description, host, port, username, COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0), COALESCE(auth_method, 'password'), COALESCE(credential_status, 'unknown'), COALESCE(fingerprint, ''), COALESCE(key_type, 'rsa'), COALESCE(bastion_id, 0), COALESCE(last_success, ''), COALESCE(last_failure, ''), COALESCE(failure_count, 0), COALESCE(success_count, 0), created_at, updated_at FROM servers WHERE 1=1`
	args := []interface{}{}
	queryText := normalizeFilterQuery(filter.Query)

	if filter.Environment != "" {
		query += " AND environment = ?"
		args = append(args, filter.Environment)
	}
	if filter.Region != "" {
		query += " AND region = ?"
		args = append(args, filter.Region)
	}
	if filter.Tag != "" {
		query += " AND tags LIKE ?"
		args = append(args, `%"`+filter.Tag+`"%`)
	}
	if queryText != "" {
		query += " AND (name LIKE ? OR description LIKE ? OR host LIKE ?)"
		pattern := "%" + queryText + "%"
		args = append(args, pattern, pattern, pattern)
	}

	query += " ORDER BY favorite DESC, name ASC"

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []Server
	for rows.Next() {
		var s Server
		var tagsJSON string
		var favorite int
		var authMethod, credentialStatus, fingerprint, keyType sql.NullString
		var bastionID sql.NullInt64
		var lastSuccess, lastFailure sql.NullString
		var failureCount, successCount sql.NullInt64

		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username, &tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite, &authMethod, &credentialStatus, &fingerprint, &keyType, &bastionID, &lastSuccess, &lastFailure, &failureCount, &successCount, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Tags = tagsFromJSON(tagsJSON)
		s.Favorite = favorite == 1
		s.AuthMethod = authMethod.String
		s.CredentialStatus = credentialStatus.String
		s.Fingerprint = fingerprint.String
		s.KeyType = keyType.String
		s.BastionID = int(bastionID.Int64)
		s.LastSuccess = lastSuccess.String
		s.LastFailure = lastFailure.String
		s.FailureCount = int(failureCount.Int64)
		s.SuccessCount = int(successCount.Int64)
		servers = append(servers, s)
	}
	return servers, nil
}

func (r *sqliteRepo) Update(id int, s Server) error {
	// Use NULL for bastion_id when 0 to avoid FK constraint violation
	var bastionID any
	if s.BastionID > 0 {
		bastionID = s.BastionID
	}
	_, err := r.db.Exec(
		`UPDATE servers SET name = ?, description = ?, host = ?, port = ?, username = ?, password = ?, ssh_key = ?, passphrase = ?, tags = ?, environment = ?, region = ?, icon = ?, color = ?, auth_method = ?, credential_status = ?, key_type = ?, bastion_id = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		s.Name, s.Description, s.Host, s.Port, s.Username, s.Password, s.SSHKey, s.Passphrase, tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, s.AuthMethod, s.CredentialStatus, s.KeyType, bastionID, id,
	)
	return err
}

func (r *sqliteRepo) Delete(id int) error {
	res, err := r.db.Exec("DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errors.New("server not found")
	}

	return nil
}

func (r *sqliteRepo) ToggleFavorite(id int) error {
	res, err := r.db.Exec("UPDATE servers SET favorite = 1 - favorite WHERE id = ?", id)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errors.New("server not found")
	}

	return nil
}

func (r *sqliteRepo) SaveServerInfo(serverID int, info ServerInfo, rawData string) error {
	_, err := r.db.Exec(
		`INSERT INTO server_info (server_id, ssh_status, latency_ms, hostname, os, kernel, architecture, cpu_model, cpu_cores, ram_total_mb, disk_total_gb, virtualization, provider, public_ip, private_ip, timezone, raw_data, last_checked)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(server_id) DO UPDATE SET
		   ssh_status = excluded.ssh_status, latency_ms = excluded.latency_ms, hostname = excluded.hostname,
		   os = excluded.os, kernel = excluded.kernel, architecture = excluded.architecture,
		   cpu_model = excluded.cpu_model, cpu_cores = excluded.cpu_cores, ram_total_mb = excluded.ram_total_mb,
		   disk_total_gb = excluded.disk_total_gb, virtualization = excluded.virtualization,
		   provider = excluded.provider, public_ip = excluded.public_ip, private_ip = excluded.private_ip,
		   timezone = excluded.timezone, raw_data = excluded.raw_data, last_checked = CURRENT_TIMESTAMP`,
		serverID, info.SSHStatus, info.LatencyMs, info.Hostname, info.OS, info.Kernel, info.Architecture,
		info.CPUModel, info.CPUCores, info.RAMTotalMB, info.DiskTotalGB, info.Virtualization,
		info.Provider, info.PublicIP, info.PrivateIP, info.Timezone, rawData,
	)
	return err
}

func (r *sqliteRepo) GetServerInfo(serverID int) (*ServerInfo, error) {
	var info ServerInfo
	err := r.db.QueryRow(
		`SELECT ssh_status, latency_ms, hostname, os, kernel, architecture, cpu_model, cpu_cores, ram_total_mb, disk_total_gb, virtualization, provider, public_ip, private_ip, timezone
		 FROM server_info WHERE server_id = ?`, serverID,
	).Scan(&info.SSHStatus, &info.LatencyMs, &info.Hostname, &info.OS, &info.Kernel, &info.Architecture, &info.CPUModel, &info.CPUCores, &info.RAMTotalMB, &info.DiskTotalGB, &info.Virtualization, &info.Provider, &info.PublicIP, &info.PrivateIP, &info.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("server info not found")
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// ── Connection history ──────────────────────────────────────────────

func (r *sqliteRepo) RecordConnection(entry ConnectionHistoryEntry) error {
	_, err := r.db.Exec(
		`INSERT INTO connection_history (server_id, hostname, ip, username, auth_method, key_fingerprint, agent_used, bastion_id, success, duration_ms, latency_ms, exit_status, failure_reason, cipher, kex, compression, remote_banner)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ServerID, entry.Hostname, entry.IP, entry.Username, entry.AuthMethod,
		entry.KeyFingerprint, boolToInt(entry.AgentUsed), entry.BastionID,
		boolToInt(entry.Success), entry.DurationMs, entry.LatencyMs, entry.ExitStatus,
		entry.FailureReason, entry.Cipher, entry.KEX, entry.Compression, entry.RemoteBanner,
	)
	if err != nil {
		return err
	}

	// Update server's success/failure counters and timestamps
	if entry.Success {
		_, err = r.db.Exec(
			`UPDATE servers SET success_count = success_count + 1, last_success = CURRENT_TIMESTAMP, credential_status = 'valid' WHERE id = ?`,
			entry.ServerID,
		)
	} else {
		_, err = r.db.Exec(
			`UPDATE servers SET failure_count = failure_count + 1, last_failure = CURRENT_TIMESTAMP, credential_status = 'invalid' WHERE id = ?`,
			entry.ServerID,
		)
	}
	return err
}

func (r *sqliteRepo) GetConnectionHistory(serverID int, limit int) ([]ConnectionHistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, server_id, COALESCE(hostname, ''), COALESCE(ip, ''), COALESCE(username, ''),
		 COALESCE(auth_method, ''), COALESCE(key_fingerprint, ''), COALESCE(agent_used, 0),
		 COALESCE(bastion_id, 0), success, COALESCE(duration_ms, 0), COALESCE(latency_ms, 0),
		 COALESCE(exit_status, 0), COALESCE(failure_reason, ''), COALESCE(cipher, ''),
		 COALESCE(kex, ''), COALESCE(compression, ''), COALESCE(remote_banner, ''), timestamp
		 FROM connection_history WHERE server_id = ? ORDER BY timestamp DESC LIMIT ?`,
		serverID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ConnectionHistoryEntry
	for rows.Next() {
		var e ConnectionHistoryEntry
		var agentUsed, success int
		if err := rows.Scan(&e.ID, &e.ServerID, &e.Hostname, &e.IP, &e.Username, &e.AuthMethod,
			&e.KeyFingerprint, &agentUsed, &e.BastionID, &success, &e.DurationMs, &e.LatencyMs,
			&e.ExitStatus, &e.FailureReason, &e.Cipher, &e.KEX, &e.Compression, &e.RemoteBanner, &e.Timestamp); err != nil {
			return nil, err
		}
		e.AgentUsed = agentUsed == 1
		e.Success = success == 1
		entries = append(entries, e)
	}
	return entries, nil
}

func (r *sqliteRepo) GetConnectionMetrics(serverID int) (*ConnectionMetrics, error) {
	metrics := &ConnectionMetrics{}

	rows, err := r.db.Query(
		`SELECT success, COALESCE(duration_ms, 0) FROM connection_history WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	durations := make([]int, 0)
	for rows.Next() {
		var success int
		var duration int
		if err := rows.Scan(&success, &duration); err != nil {
			return nil, err
		}
		metrics.TotalAttempts++
		if success == 1 {
			metrics.SuccessCount++
		} else {
			metrics.FailureCount++
		}
		if duration > 0 {
			durations = append(durations, duration)
		}
	}

	if metrics.TotalAttempts > 0 {
		metrics.SuccessRate = float64(metrics.SuccessCount) / float64(metrics.TotalAttempts)
		metrics.FailureRate = float64(metrics.FailureCount) / float64(metrics.TotalAttempts)
	}

	// Get last success/failure timestamps from server record
	var lastSuccess, lastFailure sql.NullString
	r.db.QueryRow(
		`SELECT last_success, last_failure FROM servers WHERE id = ?`,
		serverID,
	).Scan(&lastSuccess, &lastFailure)
	if lastSuccess.Valid {
		metrics.LastSuccess = lastSuccess.String
	}
	if lastFailure.Valid {
		metrics.LastFailure = lastFailure.String
	}

	return metrics, nil
}

func (r *sqliteRepo) UpdateAuthStatus(serverID int, status string, success bool) error {
	if success {
		_, err := r.db.Exec(
			`UPDATE servers SET credential_status = ?, last_success = CURRENT_TIMESTAMP, success_count = success_count + 1 WHERE id = ?`,
			status, serverID,
		)
		return err
	}
	_, err := r.db.Exec(
		`UPDATE servers SET credential_status = ?, last_failure = CURRENT_TIMESTAMP, failure_count = failure_count + 1 WHERE id = ?`,
		status, serverID,
	)
	return err
}

// ── Key management ──────────────────────────────────────────────────

func (r *sqliteRepo) StoreServerKey(key ServerKey) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO server_keys (server_id, label, key_type, private_key, public_key, fingerprint, passphrase, notes, enabled, is_default, priority)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ServerID, key.Label, key.KeyType, "", key.PublicKey, key.Fingerprint, "", key.Notes,
		boolToInt(key.Enabled), boolToInt(key.IsDefault), key.Priority,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) GetServerKeys(serverID int) ([]ServerKey, error) {
	rows, err := r.db.Query(
		`SELECT id, server_id, label, key_type, public_key, COALESCE(fingerprint, ''), COALESCE(notes, ''),
		 enabled, is_default, priority, COALESCE(last_used, ''), created_at, updated_at
		 FROM server_keys WHERE server_id = ? ORDER BY priority ASC, is_default DESC, created_at ASC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []ServerKey
	for rows.Next() {
		var k ServerKey
		var enabled, isDefault int
		if err := rows.Scan(&k.ID, &k.ServerID, &k.Label, &k.KeyType, &k.PublicKey, &k.Fingerprint,
			&k.Notes, &enabled, &isDefault, &k.Priority, &k.LastUsed, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		k.Enabled = enabled == 1
		k.IsDefault = isDefault == 1
		keys = append(keys, k)
	}
	return keys, nil
}

func (r *sqliteRepo) GetServerKey(serverID int, keyID int) (*ServerKey, error) {
	var k ServerKey
	var enabled, isDefault int
	err := r.db.QueryRow(
		`SELECT id, server_id, label, key_type, public_key, COALESCE(fingerprint, ''), COALESCE(notes, ''),
		 enabled, is_default, priority, COALESCE(last_used, ''), created_at, updated_at
		 FROM server_keys WHERE server_id = ? AND id = ?`,
		serverID, keyID,
	).Scan(&k.ID, &k.ServerID, &k.Label, &k.KeyType, &k.PublicKey, &k.Fingerprint,
		&k.Notes, &enabled, &isDefault, &k.Priority, &k.LastUsed, &k.CreatedAt, &k.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("server key not found")
	}
	if err != nil {
		return nil, err
	}
	k.Enabled = enabled == 1
	k.IsDefault = isDefault == 1
	return &k, nil
}

func (r *sqliteRepo) UpdateServerKey(key ServerKey) error {
	_, err := r.db.Exec(
		`UPDATE server_keys SET label = ?, notes = ?, enabled = ?, is_default = ?, priority = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND server_id = ?`,
		key.Label, key.Notes, boolToInt(key.Enabled), boolToInt(key.IsDefault), key.Priority,
		key.ID, key.ServerID,
	)
	return err
}

func (r *sqliteRepo) DeleteServerKey(serverID int, keyID int) error {
	res, err := r.db.Exec("DELETE FROM server_keys WHERE id = ? AND server_id = ?", keyID, serverID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("server key not found")
	}
	return nil
}

func (r *sqliteRepo) SetDefaultKey(serverID int, keyID int) error {
	// Unset all defaults first
	_, err := r.db.Exec("UPDATE server_keys SET is_default = 0 WHERE server_id = ?", serverID)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("UPDATE server_keys SET is_default = 1 WHERE id = ? AND server_id = ?", keyID, serverID)
	return err
}

// ── Auth priority ───────────────────────────────────────────────────

func (r *sqliteRepo) GetAuthPriority() ([]AuthPriorityEntry, error) {
	rows, err := r.db.Query(
		`SELECT method, priority, enabled FROM auth_priority ORDER BY priority ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuthPriorityEntry
	for rows.Next() {
		var e AuthPriorityEntry
		var enabled int
		if err := rows.Scan(&e.Method, &e.Priority, &enabled); err != nil {
			return nil, err
		}
		e.Enabled = enabled == 1
		entries = append(entries, e)
	}
	return entries, nil
}

func (r *sqliteRepo) SetAuthPriority(entries []AuthPriorityEntry) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM auth_priority")
	if err != nil {
		return err
	}

	for _, e := range entries {
		_, err = tx.Exec(
			`INSERT INTO auth_priority (method, priority, enabled) VALUES (?, ?, ?)`,
			e.Method, e.Priority, boolToInt(e.Enabled),
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ── Connection profiles ─────────────────────────────────────────────

func (r *sqliteRepo) ListConnectionProfiles() ([]ConnectionProfile, error) {
	rows, err := r.db.Query(
		`SELECT id, name, COALESCE(description, ''), timeout_seconds, retry_count, retry_delay_ms,
		 backoff_strategy, keepalive_seconds, reconnect_enabled, buffer_size_kb, compression,
		 parallelism, is_builtin FROM connection_profiles ORDER BY is_builtin DESC, name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []ConnectionProfile
	for rows.Next() {
		var p ConnectionProfile
		var reconnect, compression, isBuiltin int
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.TimeoutSeconds, &p.RetryCount,
			&p.RetryDelayMs, &p.BackoffStrategy, &p.KeepaliveSeconds, &reconnect,
			&p.BufferSizeKB, &compression, &p.Parallelism, &isBuiltin); err != nil {
			return nil, err
		}
		p.ReconnectEnabled = reconnect == 1
		p.Compression = compression == 1
		p.IsBuiltin = isBuiltin == 1
		profiles = append(profiles, p)
	}
	return profiles, nil
}

func (r *sqliteRepo) GetConnectionProfile(id int) (*ConnectionProfile, error) {
	var p ConnectionProfile
	var reconnect, compression, isBuiltin int
	err := r.db.QueryRow(
		`SELECT id, name, COALESCE(description, ''), timeout_seconds, retry_count, retry_delay_ms,
		 backoff_strategy, keepalive_seconds, reconnect_enabled, buffer_size_kb, compression,
		 parallelism, is_builtin FROM connection_profiles WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.TimeoutSeconds, &p.RetryCount,
		&p.RetryDelayMs, &p.BackoffStrategy, &p.KeepaliveSeconds, &reconnect,
		&p.BufferSizeKB, &compression, &p.Parallelism, &isBuiltin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("connection profile not found")
	}
	if err != nil {
		return nil, err
	}
	p.ReconnectEnabled = reconnect == 1
	p.Compression = compression == 1
	p.IsBuiltin = isBuiltin == 1
	return &p, nil
}

func (r *sqliteRepo) CreateConnectionProfile(p ConnectionProfile) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO connection_profiles (name, description, timeout_seconds, retry_count, retry_delay_ms,
		 backoff_strategy, keepalive_seconds, reconnect_enabled, buffer_size_kb, compression, parallelism, is_builtin)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.TimeoutSeconds, p.RetryCount, p.RetryDelayMs,
		p.BackoffStrategy, p.KeepaliveSeconds, boolToInt(p.ReconnectEnabled),
		p.BufferSizeKB, boolToInt(p.Compression), p.Parallelism, boolToInt(p.IsBuiltin),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) UpdateConnectionProfile(p ConnectionProfile) error {
	_, err := r.db.Exec(
		`UPDATE connection_profiles SET name = ?, description = ?, timeout_seconds = ?, retry_count = ?,
		 retry_delay_ms = ?, backoff_strategy = ?, keepalive_seconds = ?, reconnect_enabled = ?,
		 buffer_size_kb = ?, compression = ?, parallelism = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		p.Name, p.Description, p.TimeoutSeconds, p.RetryCount, p.RetryDelayMs,
		p.BackoffStrategy, p.KeepaliveSeconds, boolToInt(p.ReconnectEnabled),
		p.BufferSizeKB, boolToInt(p.Compression), p.Parallelism, p.ID,
	)
	return err
}

func (r *sqliteRepo) DeleteConnectionProfile(id int) error {
	// Don't allow deleting builtin profiles
	res, err := r.db.Exec("DELETE FROM connection_profiles WHERE id = ? AND is_builtin = 0", id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("connection profile not found or is builtin")
	}
	return nil
}

// ── Retry config ────────────────────────────────────────────────────

func (r *sqliteRepo) GetRetryConfig(serverID int) (*RetryConfig, error) {
	var rc RetryConfig
	var authRetryOrder sql.NullString
	err := r.db.QueryRow(
		`SELECT server_id, retry_count, retry_delay_ms, backoff_strategy, jitter_ms,
		 reconnect_policy, auth_retry_order FROM retry_config WHERE server_id = ?`,
		serverID,
	).Scan(&rc.ServerID, &rc.RetryCount, &rc.RetryDelayMs, &rc.BackoffStrategy,
		&rc.JitterMs, &rc.ReconnectPolicy, &authRetryOrder)
	if errors.Is(err, sql.ErrNoRows) {
		// Return defaults
		return &RetryConfig{
			ServerID:        serverID,
			RetryCount:      3,
			RetryDelayMs:    1000,
			BackoffStrategy: "exponential",
			ReconnectPolicy: "always",
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if authRetryOrder.Valid && authRetryOrder.String != "" {
		rc.AuthRetryOrder = strings.Split(authRetryOrder.String, ",")
	}
	return &rc, nil
}

func (r *sqliteRepo) SetRetryConfig(config RetryConfig) error {
	authRetryOrder := ""
	if len(config.AuthRetryOrder) > 0 {
		authRetryOrder = strings.Join(config.AuthRetryOrder, ",")
	}
	_, err := r.db.Exec(
		`INSERT INTO retry_config (server_id, retry_count, retry_delay_ms, backoff_strategy, jitter_ms, reconnect_policy, auth_retry_order)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		   retry_count = excluded.retry_count, retry_delay_ms = excluded.retry_delay_ms,
		   backoff_strategy = excluded.backoff_strategy, jitter_ms = excluded.jitter_ms,
		   reconnect_policy = excluded.reconnect_policy, auth_retry_order = excluded.auth_retry_order,
		   updated_at = CURRENT_TIMESTAMP`,
		config.ServerID, config.RetryCount, config.RetryDelayMs, config.BackoffStrategy,
		config.JitterMs, config.ReconnectPolicy, authRetryOrder,
	)
	return err
}

// ── Known hosts ─────────────────────────────────────────────────────

func (r *sqliteRepo) ListKnownHosts() ([]KnownHostEntry, error) {
	rows, err := r.db.Query(
		`SELECT host, port, host_key, COALESCE(fingerprint_sha256, ''), COALESCE(fingerprint_md5, ''),
		 COALESCE(algorithm, ''), COALESCE(bits, 0), COALESCE(status, 'unknown'), COALESCE(verified, 0),
		 COALESCE(server_id, 0), created_at, COALESCE(updated_at, created_at)
		 FROM known_hosts ORDER BY host ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []KnownHostEntry
	for rows.Next() {
		var h KnownHostEntry
		var verified int
		if err := rows.Scan(&h.Host, &h.Port, &h.HostKey, &h.FingerprintSHA256, &h.FingerprintMD5,
			&h.Algorithm, &h.Bits, &h.Status, &verified, &h.ServerID, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Verified = verified == 1
		hosts = append(hosts, h)
	}
	return hosts, nil
}

func (r *sqliteRepo) RemoveKnownHost(host string, port int) error {
	res, err := r.db.Exec("DELETE FROM known_hosts WHERE host = ? AND port = ?", host, port)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("known host not found")
	}
	return nil
}

func (r *sqliteRepo) SetHostStatus(host string, port int, status string) error {
	_, err := r.db.Exec(
		"UPDATE known_hosts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE host = ? AND port = ?",
		status, host, port,
	)
	return err
}

func (r *sqliteRepo) ListHostKeyChanges(limit int) ([]HostKeyChange, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, host, port, COALESCE(old_fingerprint, ''), COALESCE(new_fingerprint, ''),
		 COALESCE(risk_level, ''), COALESCE(action_taken, ''), COALESCE(server_id, 0), timestamp
		 FROM host_key_changes ORDER BY timestamp DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []HostKeyChange
	for rows.Next() {
		var c HostKeyChange
		if err := rows.Scan(&c.ID, &c.Host, &c.Port, &c.OldFingerprint, &c.NewFingerprint,
			&c.RiskLevel, &c.ActionTaken, &c.ServerID, &c.Timestamp); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// ── Credential audit ────────────────────────────────────────────────

func (r *sqliteRepo) RecordCredentialAudit(entry CredentialAuditEntry) error {
	_, err := r.db.Exec(
		`INSERT INTO credential_audit (server_id, action, detail, ip) VALUES (?, ?, ?, ?)`,
		entry.ServerID, entry.Action, entry.Detail, entry.IP,
	)
	return err
}

func (r *sqliteRepo) ListCredentialAudit(serverID int, limit int) ([]CredentialAuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, server_id, action, COALESCE(detail, ''), COALESCE(ip, ''), timestamp
		 FROM credential_audit WHERE server_id = ? ORDER BY timestamp DESC LIMIT ?`,
		serverID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []CredentialAuditEntry
	for rows.Next() {
		var e CredentialAuditEntry
		if err := rows.Scan(&e.ID, &e.ServerID, &e.Action, &e.Detail, &e.IP, &e.Timestamp); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ── SSH agent config ───────────────────────────────────────────────

func (r *sqliteRepo) GetSSHAgentConfig(serverID int) (useAgent bool, preferredIdentity string, agentForwarding bool, err error) {
	var ua, af int
	var pi sql.NullString
	err = r.db.QueryRow(
		`SELECT use_agent, COALESCE(preferred_identity, ''), agent_forwarding FROM ssh_agent_config WHERE server_id = ?`,
		serverID,
	).Scan(&ua, &pi, &af)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	return ua == 1, pi.String, af == 1, nil
}

func (r *sqliteRepo) SetSSHAgentConfig(serverID int, useAgent bool, preferredIdentity string, agentForwarding bool) error {
	_, err := r.db.Exec(
		`INSERT INTO ssh_agent_config (server_id, use_agent, preferred_identity, agent_forwarding)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		   use_agent = excluded.use_agent, preferred_identity = excluded.preferred_identity,
		   agent_forwarding = excluded.agent_forwarding, updated_at = CURRENT_TIMESTAMP`,
		serverID, boolToInt(useAgent), preferredIdentity, boolToInt(agentForwarding),
	)
	return err
}

// ── Dashboard ───────────────────────────────────────────────────────

func (r *sqliteRepo) GetAuthDashboard() (*AuthDashboard, error) {
	d := &AuthDashboard{}

	// Count servers by auth method
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(auth_method, 'password') = 'password'`).Scan(&d.PasswordServers)
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(auth_method, 'password') = 'key'`).Scan(&d.KeyServers)
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(auth_method, 'password') = 'agent'`).Scan(&d.AgentServers)
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(bastion_id, 0) > 0`).Scan(&d.BastionServers)

	// Count known hosts by status
	r.db.QueryRow(`SELECT COUNT(*) FROM known_hosts WHERE COALESCE(status, 'unknown') = 'changed'`).Scan(&d.FingerprintChanged)
	r.db.QueryRow(`SELECT COUNT(*) FROM known_hosts WHERE COALESCE(status, 'unknown') = 'unknown'`).Scan(&d.UnknownHosts)

	// Count credential warnings
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(credential_status, 'unknown') = 'invalid'`).Scan(&d.CredentialWarnings)
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE COALESCE(credential_status, 'unknown') = 'expired'`).Scan(&d.ExpiredKeys)

	// Count auth failures from connection history
	r.db.QueryRow(`SELECT COUNT(*) FROM connection_history WHERE success = 0`).Scan(&d.AuthFailures)

	// Calculate success rates
	var totalAttempts int
	r.db.QueryRow(`SELECT COUNT(*) FROM connection_history`).Scan(&totalAttempts)
	if totalAttempts > 0 {
		var successCount int
		r.db.QueryRow(`SELECT COUNT(*) FROM connection_history WHERE success = 1`).Scan(&successCount)
		d.AuthSuccessRate = float64(successCount) / float64(totalAttempts)
		d.ConnectionSuccessRate = d.AuthSuccessRate
	}

	// Calculate latency stats
	var avgLatency sql.NullFloat64
	r.db.QueryRow(`SELECT AVG(COALESCE(latency_ms, 0)) FROM connection_history WHERE success = 1 AND COALESCE(latency_ms, 0) > 0`).Scan(&avgLatency)
	if avgLatency.Valid {
		d.AverageLatency = avgLatency.Float64
	}

	return d, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeFilterQuery(q string) string {
	return strings.TrimSpace(q)
}
