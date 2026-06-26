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
		`INSERT INTO servers (name, description, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Description, s.Host, s.Port, s.Username,
		s.Password, s.SSHKey, s.Passphrase,
		tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, boolToInt(s.Favorite),
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
		`SELECT id, name, description, host, port, username, password, ssh_key, passphrase, COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0), created_at, updated_at
		 FROM servers WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username, &s.Password, &s.SSHKey, &s.Passphrase, &tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite, &s.CreatedAt, &s.UpdatedAt)
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
	query := `SELECT id, name, description, host, port, username, COALESCE(tags, '[]'), environment, region, icon, color, COALESCE(favorite, 0), created_at, updated_at FROM servers WHERE 1=1`
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
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Host, &s.Port, &s.Username, &tagsJSON, &s.Environment, &s.Region, &s.Icon, &s.Color, &favorite, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Tags = tagsFromJSON(tagsJSON)
		s.Favorite = favorite == 1
		servers = append(servers, s)
	}
	return servers, nil
}

func (r *sqliteRepo) Update(id int, s Server) error {
	_, err := r.db.Exec(
		`UPDATE servers SET name = ?, description = ?, host = ?, port = ?, username = ?, password = ?, ssh_key = ?, passphrase = ?, tags = ?, environment = ?, region = ?, icon = ?, color = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		s.Name, s.Description, s.Host, s.Port, s.Username, s.Password, s.SSHKey, s.Passphrase, tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, id,
	)
	return err
}

func (r *sqliteRepo) Delete(id int) error {
	_, err := r.db.Exec("DELETE FROM servers WHERE id = ?", id)
	return err
}

func (r *sqliteRepo) ToggleFavorite(id int) error {
	_, err := r.db.Exec("UPDATE servers SET favorite = 1 - favorite WHERE id = ?", id)
	return err
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

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeFilterQuery(q string) string {
	return strings.TrimSpace(q)
}
