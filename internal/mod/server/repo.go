package server

import (
	"database/sql"
	"errors"

	"strings"
)

type Repo interface {
	Create(s Server) (int, error)
	GetByID(id int) (*Server, error)
	List(filter ListFilter) ([]Server, error)
	Update(id int, s Server) error
	Delete(id int) error
	ToggleFavorite(id int) error
	SaveServerInfo(serverID int, info ServerInfo, rawData string) error
	GetServerInfo(serverID int) (*ServerInfo, error)
	UpdateAuthStatus(serverID int, authMethod string, credentialStatus string, fingerprint string) error
	RecordConnection(serverID int, success bool, durationMs int, reason string, remoteIP string, fingerprint string, authMethod string) error
	GetConnectionHistory(serverID int, limit int) ([]ConnectionHistoryEntry, error)
	GetConnectionMetrics(serverID int) (*ConnectionMetrics, error)
	RemovePassword(serverID int) error
	ClearSSHKey(serverID int) error
	UpdateFingerprint(serverID int, fingerprint string) error
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
	res, err := r.db.Exec(
		`INSERT INTO servers (name, description, host, port, username, password, ssh_key, passphrase, auth_method, credential_status, key_type, bastion_id, tags, environment, region, icon, color, favorite)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), 'unknown'), COALESCE(NULLIF(?, ''), 'rsa'), ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.Host, s.Port, s.Username,
		s.Password, s.SSHKey, s.Passphrase, s.AuthMethod, s.CredentialStatus, s.KeyType,
		s.BastionID, tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, boolToInt(s.Favorite),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func (r *sqliteRepo) GetByID(id int) (*Server, error) {
	var s Server
	var tagsJSON string
	var favorite int
	err := r.db.QueryRow(
		`SELECT id, name, description, host, port, username, COALESCE(password, ''), COALESCE(ssh_key, ''), COALESCE(passphrase, ''),
		        COALESCE(auth_method, ''), COALESCE(credential_status, 'unknown'),
		        COALESCE(last_success, ''), COALESCE(last_failure, ''),
		        COALESCE(failure_count, 0), COALESCE(success_count, 0),
		        COALESCE(fingerprint, ''), COALESCE(key_type, 'rsa'),
		        COALESCE(bastion_id, 0),
		        COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0), created_at, updated_at
		 FROM servers WHERE id = ?`, id,
	).Scan(
		&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username, &s.Password, &s.SSHKey, &s.Passphrase,
		&s.AuthMethod, &s.CredentialStatus, &s.LastSuccess, &s.LastFailure, &s.FailureCount, &s.SuccessCount,
		&s.Fingerprint, &s.KeyType, &s.BastionID,
		&tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite, &s.CreatedAt, &s.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("server not found")
	}
	if err != nil {
		return nil, err
	}
	s.Tags = tagsFromJSON(tagsJSON)
	s.Favorite = favorite == 1
	return &s, nil
}

func (r *sqliteRepo) List(filter ListFilter) ([]Server, error) {
	query := `SELECT id, name, description, host, port, username,
		       COALESCE(auth_method, ''), COALESCE(credential_status, 'unknown'),
		       COALESCE(last_success, ''), COALESCE(last_failure, ''),
		       COALESCE(failure_count, 0), COALESCE(success_count, 0),
		       COALESCE(fingerprint, ''), COALESCE(key_type, 'rsa'),
		       COALESCE(bastion_id, 0),
		       COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0), created_at, updated_at
		FROM servers WHERE 1=1`
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
		if err := rows.Scan(
			&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username,
			&s.AuthMethod, &s.CredentialStatus, &s.LastSuccess, &s.LastFailure, &s.FailureCount, &s.SuccessCount,
			&s.Fingerprint, &s.KeyType, &s.BastionID,
			&tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		s.Tags = tagsFromJSON(tagsJSON)
		s.Favorite = favorite == 1
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return servers, nil
}

func (r *sqliteRepo) Update(id int, s Server) error {
	_, err := r.db.Exec(
		`UPDATE servers SET name = ?, description = ?, host = ?, port = ?, username = ?, password = ?, ssh_key = ?, passphrase = ?, auth_method = ?, credential_status = ?, key_type = ?, fingerprint = ?, bastion_id = ?, tags = ?, environment = ?, region = ?, icon = ?, color = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		s.Name, s.Description, s.Host, s.Port, s.Username, s.Password, s.SSHKey, s.Passphrase, s.AuthMethod, s.CredentialStatus, s.KeyType, s.Fingerprint, s.BastionID, tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, id,
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

func (r *sqliteRepo) UpdateAuthStatus(serverID int, authMethod string, credentialStatus string, fingerprint string) error {
	res, err := r.db.Exec(
		`UPDATE servers SET auth_method = COALESCE(NULLIF(?, ''), auth_method), credential_status = COALESCE(NULLIF(?, ''), credential_status), fingerprint = COALESCE(NULLIF(?, ''), fingerprint), updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		authMethod, credentialStatus, fingerprint, serverID,
	)
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

func (r *sqliteRepo) RecordConnection(serverID int, success bool, durationMs int, reason string, remoteIP string, fingerprint string, authMethod string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	successInt := boolToInt(success)
	if _, err := tx.Exec(
		`INSERT INTO connection_history (server_id, success, duration_ms, reason, remote_ip, fingerprint, auth_method)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		serverID, successInt, durationMs, reason, remoteIP, fingerprint, authMethod,
	); err != nil {
		return err
	}

	res, err := tx.Exec(
		`UPDATE servers SET
			auth_method = COALESCE(NULLIF(?, ''), auth_method),
			fingerprint = COALESCE(NULLIF(?, ''), fingerprint),
			last_success = CASE WHEN ? = 1 THEN CURRENT_TIMESTAMP ELSE last_success END,
			last_failure = CASE WHEN ? = 1 THEN last_failure ELSE CURRENT_TIMESTAMP END,
			success_count = COALESCE(success_count, 0) + CASE WHEN ? = 1 THEN 1 ELSE 0 END,
			failure_count = COALESCE(failure_count, 0) + CASE WHEN ? = 1 THEN 0 ELSE 1 END,
			updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		authMethod, fingerprint, successInt, successInt, successInt, successInt, serverID,
	)
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

	return tx.Commit()
}

func (r *sqliteRepo) GetConnectionHistory(serverID int, limit int) ([]ConnectionHistoryEntry, error) {
	query := `SELECT id, server_id, success, duration_ms, COALESCE(reason, ''), COALESCE(remote_ip, ''), COALESCE(fingerprint, ''), COALESCE(auth_method, ''), created_at
		 FROM connection_history WHERE server_id = ? ORDER BY created_at DESC, id DESC`
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

	entries := make([]ConnectionHistoryEntry, 0)
	for rows.Next() {
		var entry ConnectionHistoryEntry
		var success int
		var duration sql.NullInt64
		if err := rows.Scan(&entry.ID, &entry.ServerID, &success, &duration, &entry.Reason, &entry.RemoteIP, &entry.Fingerprint, &entry.AuthMethod, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.Success = success == 1
		if duration.Valid {
			entry.DurationMs = int(duration.Int64)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *sqliteRepo) GetConnectionMetrics(serverID int) (*ConnectionMetrics, error) {
	rows, err := r.db.Query(
		`SELECT success, duration_ms, COALESCE(reason, '') FROM connection_history WHERE server_id = ?`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	metrics := &ConnectionMetrics{}
	for rows.Next() {
		var success int
		var duration sql.NullInt64
		var reason string
		if err := rows.Scan(&success, &duration, &reason); err != nil {
			return nil, err
		}
		metrics.TotalAttempts++
		if success == 1 {
			metrics.SuccessCount++
		} else {
			metrics.FailureCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if metrics.TotalAttempts > 0 {
		metrics.SuccessRate = float64(metrics.SuccessCount) / float64(metrics.TotalAttempts)
		metrics.FailureRate = float64(metrics.FailureCount) / float64(metrics.TotalAttempts)
	}

	// Get last success/failure timestamps from the server record
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

func (r *sqliteRepo) RemovePassword(serverID int) error {
	res, err := r.db.Exec(`UPDATE servers SET password = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, serverID)
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

func (r *sqliteRepo) ClearSSHKey(serverID int) error {
	res, err := r.db.Exec(`UPDATE servers SET ssh_key = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, serverID)
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

func (r *sqliteRepo) UpdateFingerprint(serverID int, fingerprint string) error {
	res, err := r.db.Exec(`UPDATE servers SET fingerprint = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, fingerprint, serverID)
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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeFilterQuery(q string) string {
	return strings.TrimSpace(q)
}

