package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// --- Server Keys ---

func (r *sqliteRepo) StoreServerKey(key ServerKey) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO server_keys (server_id, label, key_type, public_key, fingerprint, notes, enabled, is_default, priority)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ServerID, key.Label, key.KeyType, key.PublicKey, key.Fingerprint, key.Notes,
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
		`SELECT id, server_id, label, COALESCE(key_type, ''), COALESCE(public_key, ''), COALESCE(fingerprint, ''),
		        COALESCE(notes, ''), COALESCE(enabled, 1), COALESCE(is_default, 0), COALESCE(priority, 0),
		        COALESCE(last_used, ''), created_at, COALESCE(updated_at, '')
		 FROM server_keys WHERE server_id = ? ORDER BY is_default DESC, priority ASC, id ASC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]ServerKey, 0)
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
	return keys, rows.Err()
}

func (r *sqliteRepo) GetServerKey(serverID int, keyID int) (*ServerKey, error) {
	var k ServerKey
	var enabled, isDefault int
	err := r.db.QueryRow(
		`SELECT id, server_id, label, COALESCE(key_type, ''), COALESCE(public_key, ''), COALESCE(fingerprint, ''),
		        COALESCE(notes, ''), COALESCE(enabled, 1), COALESCE(is_default, 0), COALESCE(priority, 0),
		        COALESCE(last_used, ''), created_at, COALESCE(updated_at, '')
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
	res, err := r.db.Exec(
		`UPDATE server_keys SET label = ?, key_type = ?, public_key = ?, fingerprint = ?, notes = ?, enabled = ?, priority = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND server_id = ?`,
		key.Label, key.KeyType, key.PublicKey, key.Fingerprint, key.Notes, boolToInt(key.Enabled), key.Priority,
		key.ID, key.ServerID,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("server key not found")
	}
	return nil
}

func (r *sqliteRepo) DeleteServerKey(serverID int, keyID int) error {
	res, err := r.db.Exec("DELETE FROM server_keys WHERE server_id = ? AND id = ?", serverID, keyID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("server key not found")
	}
	return nil
}

func (r *sqliteRepo) SetDefaultKey(serverID int, keyID int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE server_keys SET is_default = 0 WHERE server_id = ?", serverID); err != nil {
		return err
	}
	res, err := tx.Exec("UPDATE server_keys SET is_default = 1 WHERE server_id = ? AND id = ?", serverID, keyID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("server key not found")
	}
	return tx.Commit()
}

// --- Auth Priority ---

func (r *sqliteRepo) GetAuthPriority() ([]AuthPriorityEntry, error) {
	rows, err := r.db.Query(`SELECT method, priority, COALESCE(enabled, 1) FROM auth_priority ORDER BY priority ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]AuthPriorityEntry, 0)
	for rows.Next() {
		var e AuthPriorityEntry
		var enabled int
		if err := rows.Scan(&e.Method, &e.Priority, &enabled); err != nil {
			return nil, err
		}
		e.Enabled = enabled == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (r *sqliteRepo) SetAuthPriority(entries []AuthPriorityEntry) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM auth_priority"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := tx.Exec(
			`INSERT INTO auth_priority (method, priority, enabled) VALUES (?, ?, ?)`,
			e.Method, e.Priority, boolToInt(e.Enabled),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Connection Profiles ---

func (r *sqliteRepo) ListConnectionProfiles() ([]ConnectionProfile, error) {
	rows, err := r.db.Query(
		`SELECT id, name, COALESCE(description, ''), COALESCE(timeout_seconds, 30), COALESCE(retry_count, 3),
		        COALESCE(retry_delay_ms, 1000), COALESCE(backoff_strategy, 'exponential'), COALESCE(keepalive_seconds, 30),
		        COALESCE(reconnect_enabled, 1), COALESCE(buffer_size_kb, 64), COALESCE(compression, 0),
		        COALESCE(parallelism, 1), COALESCE(is_builtin, 0), created_at, COALESCE(updated_at, '')
		 FROM connection_profiles ORDER BY is_builtin DESC, name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := make([]ConnectionProfile, 0)
	for rows.Next() {
		var p ConnectionProfile
		var reconnect, compression, isBuiltin int
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.TimeoutSeconds, &p.RetryCount,
			&p.RetryDelayMs, &p.BackoffStrategy, &p.KeepaliveSeconds, &reconnect,
			&p.BufferSizeKb, &compression, &p.Parallelism, &isBuiltin,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.ReconnectEnabled = reconnect == 1
		p.Compression = compression == 1
		p.IsBuiltin = isBuiltin == 1
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (r *sqliteRepo) GetConnectionProfile(id int) (*ConnectionProfile, error) {
	var p ConnectionProfile
	var reconnect, compression, isBuiltin int
	err := r.db.QueryRow(
		`SELECT id, name, COALESCE(description, ''), COALESCE(timeout_seconds, 30), COALESCE(retry_count, 3),
		        COALESCE(retry_delay_ms, 1000), COALESCE(backoff_strategy, 'exponential'), COALESCE(keepalive_seconds, 30),
		        COALESCE(reconnect_enabled, 1), COALESCE(buffer_size_kb, 64), COALESCE(compression, 0),
		        COALESCE(parallelism, 1), COALESCE(is_builtin, 0), created_at, COALESCE(updated_at, '')
		 FROM connection_profiles WHERE id = ?`,
		id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.TimeoutSeconds, &p.RetryCount,
		&p.RetryDelayMs, &p.BackoffStrategy, &p.KeepaliveSeconds, &reconnect,
		&p.BufferSizeKb, &compression, &p.Parallelism, &isBuiltin,
		&p.CreatedAt, &p.UpdatedAt)
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

func (r *sqliteRepo) CreateConnectionProfile(profile ConnectionProfile) (int, error) {
	res, err := r.db.Exec(
		`INSERT INTO connection_profiles (name, description, timeout_seconds, retry_count, retry_delay_ms,
		   backoff_strategy, keepalive_seconds, reconnect_enabled, buffer_size_kb, compression, parallelism, is_builtin)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		profile.Name, profile.Description, profile.TimeoutSeconds, profile.RetryCount, profile.RetryDelayMs,
		profile.BackoffStrategy, profile.KeepaliveSeconds, boolToInt(profile.ReconnectEnabled),
		profile.BufferSizeKb, boolToInt(profile.Compression), profile.Parallelism, boolToInt(profile.IsBuiltin),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) UpdateConnectionProfile(profile ConnectionProfile) error {
	res, err := r.db.Exec(
		`UPDATE connection_profiles SET name = ?, description = ?, timeout_seconds = ?, retry_count = ?, retry_delay_ms = ?,
		   backoff_strategy = ?, keepalive_seconds = ?, reconnect_enabled = ?, buffer_size_kb = ?, compression = ?,
		   parallelism = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		profile.Name, profile.Description, profile.TimeoutSeconds, profile.RetryCount, profile.RetryDelayMs,
		profile.BackoffStrategy, profile.KeepaliveSeconds, boolToInt(profile.ReconnectEnabled),
		profile.BufferSizeKb, boolToInt(profile.Compression), profile.Parallelism, profile.ID,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("connection profile not found")
	}
	return nil
}

func (r *sqliteRepo) DeleteConnectionProfile(id int) error {
	res, err := r.db.Exec("DELETE FROM connection_profiles WHERE id = ? AND is_builtin = 0", id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("connection profile not found or is builtin")
	}
	return nil
}

// --- Retry Config ---

func (r *sqliteRepo) GetRetryConfig(serverID int) (*RetryConfig, error) {
	var rc RetryConfig
	var authRetryOrderJSON string
	err := r.db.QueryRow(
		`SELECT server_id, COALESCE(retry_count, 3), COALESCE(retry_delay_ms, 1000), COALESCE(backoff_strategy, 'exponential'),
		        COALESCE(jitter_ms, 0), COALESCE(reconnect_policy, 'always'), COALESCE(auth_retry_order, 'agent,ed25519,rsa,ecdsa,password')
		 FROM retry_configs WHERE server_id = ?`,
		serverID,
	).Scan(&rc.ServerID, &rc.RetryCount, &rc.RetryDelayMs, &rc.BackoffStrategy,
		&rc.JitterMs, &rc.ReconnectPolicy, &authRetryOrderJSON)
	if errors.Is(err, sql.ErrNoRows) {
		// Return defaults if no config exists
		return &RetryConfig{
			ServerID:        serverID,
			RetryCount:      3,
			RetryDelayMs:    1000,
			BackoffStrategy: "exponential",
			ReconnectPolicy: "always",
			AuthRetryOrder:  []string{"agent", "ed25519", "rsa", "ecdsa", "password"},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if authRetryOrderJSON != "" {
		json.Unmarshal([]byte(authRetryOrderJSON), &rc.AuthRetryOrder)
	}
	if rc.AuthRetryOrder == nil {
		rc.AuthRetryOrder = []string{"agent", "ed25519", "rsa", "ecdsa", "password"}
	}
	return &rc, nil
}

func (r *sqliteRepo) SetRetryConfig(config RetryConfig) error {
	authRetryOrderJSON := "[]"
	if config.AuthRetryOrder != nil {
		b, _ := json.Marshal(config.AuthRetryOrder)
		authRetryOrderJSON = string(b)
	}
	_, err := r.db.Exec(
		`INSERT INTO retry_configs (server_id, retry_count, retry_delay_ms, backoff_strategy, jitter_ms, reconnect_policy, auth_retry_order, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(server_id) DO UPDATE SET
		   retry_count = excluded.retry_count, retry_delay_ms = excluded.retry_delay_ms,
		   backoff_strategy = excluded.backoff_strategy, jitter_ms = excluded.jitter_ms,
		   reconnect_policy = excluded.reconnect_policy, auth_retry_order = excluded.auth_retry_order,
		   updated_at = CURRENT_TIMESTAMP`,
		config.ServerID, config.RetryCount, config.RetryDelayMs, config.BackoffStrategy,
		config.JitterMs, config.ReconnectPolicy, authRetryOrderJSON,
	)
	return err
}

// --- Known Hosts ---

func (r *sqliteRepo) ListKnownHosts() ([]KnownHostEntry, error) {
	rows, err := r.db.Query(
		`SELECT COALESCE(kh.server_id, 0), kh.host, kh.port, kh.host_key, COALESCE(kh.verified, 0), COALESCE(kh.status, 'unknown'),
		        kh.created_at, COALESCE(kh.updated_at, '')
		 FROM known_hosts kh ORDER BY kh.host ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]KnownHostEntry, 0)
	for rows.Next() {
		var e KnownHostEntry
		var verified int
		if err := rows.Scan(&e.ServerID, &e.Host, &e.Port, &e.HostKey, &verified, &e.Status, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Verified = verified == 1
		// Derive fingerprint and algorithm from the host key
		if e.HostKey != "" {
			if pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(e.HostKey)); err == nil {
				e.FingerprintSHA256 = ssh.FingerprintSHA256(pubKey)
				e.FingerprintMD5 = ssh.FingerprintLegacyMD5(pubKey)
				e.Algorithm = pubKey.Type()
				e.Bits = keyBits(pubKey)
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (r *sqliteRepo) RemoveKnownHost(host string, port int) error {
	res, err := r.db.Exec("DELETE FROM known_hosts WHERE host = ? AND port = ?", host, port)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("known host not found")
	}
	return nil
}

func (r *sqliteRepo) SetHostStatus(host string, port int, status string) error {
	res, err := r.db.Exec(
		"UPDATE known_hosts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE host = ? AND port = ?",
		status, host, port,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("known host not found")
	}
	return nil
}

// --- Host Key Changes ---

func (r *sqliteRepo) ListHostKeyChanges(limit int) ([]HostKeyChange, error) {
	query := `SELECT id, host, port, COALESCE(old_fingerprint, ''), COALESCE(new_fingerprint, ''),
	              COALESCE(risk_level, 'medium'), COALESCE(action_taken, ''), COALESCE(server_id, 0), created_at
	       FROM host_key_changes ORDER BY created_at DESC, id DESC`
	args := []interface{}{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	changes := make([]HostKeyChange, 0)
	for rows.Next() {
		var c HostKeyChange
		if err := rows.Scan(&c.ID, &c.Host, &c.Port, &c.OldFingerprint, &c.NewFingerprint,
			&c.RiskLevel, &c.ActionTaken, &c.ServerID, &c.Timestamp); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// --- Credential Audit ---

func (r *sqliteRepo) RecordCredentialAudit(entry CredentialAuditEntry) error {
	_, err := r.db.Exec(
		`INSERT INTO credential_audit (server_id, action, detail, ip) VALUES (?, ?, ?, ?)`,
		entry.ServerID, entry.Action, entry.Detail, entry.IP,
	)
	return err
}

func (r *sqliteRepo) ListCredentialAudit(serverID int, limit int) ([]CredentialAuditEntry, error) {
	query := `SELECT id, server_id, action, COALESCE(detail, ''), COALESCE(ip, ''), created_at
	          FROM credential_audit WHERE server_id = ? ORDER BY created_at DESC, id DESC`
	args := []interface{}{serverID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]CredentialAuditEntry, 0)
	for rows.Next() {
		var e CredentialAuditEntry
		if err := rows.Scan(&e.ID, &e.ServerID, &e.Action, &e.Detail, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- SSH Agent Config ---

func (r *sqliteRepo) GetSSHAgentConfig(serverID int) (bool, string, bool, error) {
	var useAgent, agentForwarding int
	var preferredIdentity string
	err := r.db.QueryRow(
		`SELECT COALESCE(use_agent, 0), COALESCE(preferred_identity, ''), COALESCE(agent_forwarding, 0)
		 FROM ssh_agent_configs WHERE server_id = ?`,
		serverID,
	).Scan(&useAgent, &preferredIdentity, &agentForwarding)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	return useAgent == 1, preferredIdentity, agentForwarding == 1, nil
}

func (r *sqliteRepo) SetSSHAgentConfig(serverID int, useAgent bool, preferredIdentity string, agentForwarding bool) error {
	_, err := r.db.Exec(
		`INSERT INTO ssh_agent_configs (server_id, use_agent, preferred_identity, agent_forwarding, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(server_id) DO UPDATE SET
		   use_agent = excluded.use_agent, preferred_identity = excluded.preferred_identity,
		   agent_forwarding = excluded.agent_forwarding, updated_at = CURRENT_TIMESTAMP`,
		serverID, boolToInt(useAgent), preferredIdentity, boolToInt(agentForwarding),
	)
	return err
}

// --- Auth Dashboard ---

func (r *sqliteRepo) GetAuthDashboard() (*AuthDashboard, error) {
	dash := &AuthDashboard{}

	// Count servers by auth method
	rows, err := r.db.Query(`SELECT COALESCE(auth_method, ''), COUNT(*) FROM servers GROUP BY auth_method`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var method string
		var count int
		if err := rows.Scan(&method, &count); err != nil {
			rows.Close()
			return nil, err
		}
		switch strings.ToLower(method) {
		case "password":
			dash.PasswordServers = count
		case "key":
			dash.KeyServers = count
		case "agent":
			dash.AgentServers = count
		}
	}
	rows.Close()

	// Count bastion servers
	var bastionCount int
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE bastion_id > 0`).Scan(&bastionCount)
	dash.BastionServers = bastionCount

	// Count credential warnings (servers with auth_failed or expired status)
	var warningCount int
	r.db.QueryRow(`SELECT COUNT(*) FROM servers WHERE credential_status IN ('auth_failed', 'expired', 'invalid')`).Scan(&warningCount)
	dash.CredentialWarnings = warningCount

	// Count auth failures from connection history
	var authFailures int
	r.db.QueryRow(`SELECT COUNT(*) FROM connection_history WHERE success = 0`).Scan(&authFailures)
	dash.AuthFailures = authFailures

	// Count unknown hosts
	var unknownHosts int
	r.db.QueryRow(`SELECT COUNT(*) FROM known_hosts WHERE COALESCE(verified, 0) = 0`).Scan(&unknownHosts)
	dash.UnknownHosts = unknownHosts

	// Count fingerprint changes
	var fpChanges int
	r.db.QueryRow(`SELECT COUNT(*) FROM host_key_changes`).Scan(&fpChanges)
	dash.FingerprintChanged = fpChanges

	// Calculate connection success rate
	var totalAttempts, successCount int
	r.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), 0) FROM connection_history`).Scan(&totalAttempts, &successCount)
	if totalAttempts > 0 {
		dash.ConnectionSuccessRate = float64(successCount) / float64(totalAttempts)
	}

	// Auth success rate (same as connection success rate for now)
	dash.AuthSuccessRate = dash.ConnectionSuccessRate

	// Calculate latency metrics from connection_history
	var latencies []float64
	latRows, err := r.db.Query(`SELECT duration_ms FROM connection_history WHERE duration_ms > 0 AND success = 1 ORDER BY duration_ms`)
	if err == nil {
		for latRows.Next() {
			var ms int
			if err := latRows.Scan(&ms); err == nil {
				latencies = append(latencies, float64(ms))
			}
		}
		latRows.Close()
	}
	if len(latencies) > 0 {
		var sum float64
		for _, l := range latencies {
			sum += l
		}
		dash.AverageLatency = sum / float64(len(latencies))
		dash.MedianLatency = latencies[len(latencies)/2]
		if len(latencies) > 0 {
			p95Idx := int(float64(len(latencies)) * 0.95)
			if p95Idx >= len(latencies) {
				p95Idx = len(latencies) - 1
			}
			dash.P95Latency = latencies[p95Idx]
		}
		if len(latencies) > 0 {
			p99Idx := int(float64(len(latencies)) * 0.99)
			if p99Idx >= len(latencies) {
				p99Idx = len(latencies) - 1
			}
			dash.P99Latency = latencies[p99Idx]
		}
	}

	return dash, nil
}

// --- Helpers ---

// keyBits returns the bit size of an SSH public key.
func keyBits(pubKey ssh.PublicKey) int {
	switch pubKey.Type() {
	case ssh.KeyAlgoRSA:
		return 0 // Not easily derivable without parsing the wire format
	case ssh.KeyAlgoECDSA256:
		return 256
	case ssh.KeyAlgoECDSA384:
		return 384
	case ssh.KeyAlgoECDSA521:
		return 521
	case ssh.KeyAlgoED25519:
		return 256
	default:
		return 0
	}
}
