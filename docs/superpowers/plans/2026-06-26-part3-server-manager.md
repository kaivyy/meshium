# Part 3: Server Manager Backend

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the server management module — CRUD operations, credential encryption, filtering/search, SSH key management endpoints, and server info caching.

**Architecture:** Server module follows the domain-driven pattern: model → repo → service → handler. The service layer encrypts/decrypts credentials using the AES key from the auth service. The handler exposes REST endpoints. The repo layer handles SQLite queries.

**Tech Stack:** `database/sql`, `encoding/json`, `meshium/internal/shared` (crypto)

## Global Constraints

- All credentials encrypted with AES-256-GCM before storage
- API responses never include credential fields (password, ssh_key, passphrase)
- Tags stored as JSON array in SQLite
- Error format: `{"error": "string", "code": "ERROR_CODE"}`
- Server info cached after connection test

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/server/model.go` | Server struct, DTOs, ServerInfo struct |
| `internal/mod/server/repo.go` | SQLite queries (servers + server_info tables) |
| `internal/mod/server/service.go` | Business logic (encrypt/decrypt, filtering) |
| `internal/mod/server/handler.go` | REST handlers |
| `internal/mod/server/repo_test.go` | Repo tests |
| `internal/mod/server/service_test.go` | Service tests |
| `internal/mod/server/handler_test.go` | Handler tests |

---

### Task 1: Server Model

**Files:**
- Create: `internal/mod/server/model.go`

**Interfaces:**
- Produces: `server.Server`, `server.CreateRequest`, `server.UpdateRequest`, `server.ServerResponse`, `server.ServerInfo`

- [ ] **Step 1: Write server model**

```go
// internal/mod/server/model.go
package server

import "encoding/json"

// Server is the internal server model (matches DB schema).
type Server struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"-"`          // encrypted, never sent to client
	SSHKey      string   `json:"-"`          // encrypted, never sent to client
	Passphrase  string   `json:"-"`          // encrypted, never sent to client
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
	Favorite    bool     `json:"favorite"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// CreateRequest is the DTO for creating a server.
type CreateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	SSHKey      string   `json:"sshKey,omitempty"`
	Passphrase  string   `json:"passphrase,omitempty"`
	Tags        []string `json:"tags"`
	Environment string   `json:"environment"`
	Region      string   `json:"region"`
	Icon        string   `json:"icon"`
	Color       string   `json:"color"`
}

// UpdateRequest is the DTO for updating a server.
type UpdateRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Host        *string   `json:"host,omitempty"`
	Port        *int      `json:"port,omitempty"`
	Username    *string   `json:"username,omitempty"`
	Password    *string   `json:"password,omitempty"`
	SSHKey      *string   `json:"sshKey,omitempty"`
	Passphrase  *string   `json:"passphrase,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Environment *string   `json:"environment,omitempty"`
	Region      *string   `json:"region,omitempty"`
	Icon        *string   `json:"icon,omitempty"`
	Color       *string   `json:"color,omitempty"`
}

// ServerInfo holds cached connection test results.
type ServerInfo struct {
	SSHStatus      string  `json:"sshStatus"`
	LatencyMs      int     `json:"latencyMs"`
	Hostname       string  `json:"hostname"`
	OS             string  `json:"os"`
	Kernel         string  `json:"kernel"`
	Architecture   string  `json:"architecture"`
	CPUModel       string  `json:"cpuModel"`
	CPUCores       int     `json:"cpuCores"`
	RAMTotalMB     int     `json:"ramTotalMb"`
	DiskTotalGB    float64 `json:"diskTotalGb"`
	Virtualization string  `json:"virtualization"`
	Provider       string  `json:"provider"`
	PublicIP       string  `json:"publicIp"`
	PrivateIP      string  `json:"privateIp"`
	Timezone       string  `json:"timezone"`
}

// tagsToJSON converts a string slice to JSON for SQLite storage.
func tagsToJSON(tags []string) string {
	if tags == nil {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

// tagsFromJSON parses a JSON string to a string slice.
func tagsFromJSON(s string) []string {
	if s == "" {
		return []string{}
	}
	var tags []string
	json.Unmarshal([]byte(s), &tags)
	return tags
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mod/server/
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/server/model.go
git commit -m "feat: add server model with DTOs and tag helpers"
```

---

### Task 2: Server Repo

**Files:**
- Create: `internal/mod/server/repo.go`
- Create: `internal/mod/server/repo_test.go`

**Interfaces:**
- Produces: `server.Repo` interface with `Create`, `GetByID`, `List`, `Update`, `Delete`, `ToggleFavorite`, `SaveServerInfo`, `GetServerInfo`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/server/repo_test.go
package server

import (
	"database/sql"
	"testing"

	"meshium/internal/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return d
}

func TestCreateAndGetByID(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	srv := Server{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "encrypted-password",
		Tags:     []string{"web", "prod"},
	}

	id, err := repo.Create(srv)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if id != 1 {
		t.Errorf("expected ID 1, got %d", id)
	}

	got, err := repo.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got %q", got.Name)
	}
	if got.Host != "192.168.1.1" {
		t.Errorf("expected host '192.168.1.1', got %q", got.Host)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "web" {
		t.Errorf("expected tags ['web', 'prod'], got %v", got.Tags)
	}
}

func TestList(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	repo.Create(Server{Name: "Server 1", Host: "10.0.0.1", Port: 22, Username: "root"})
	repo.Create(Server{Name: "Server 2", Host: "10.0.0.2", Port: 22, Username: "root"})

	servers, err := repo.List(ListFilter{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
}

func TestListWithFilter(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)

	repo.Create(Server{Name: "Prod", Host: "10.0.0.1", Port: 22, Username: "root", Environment: "production"})
	repo.Create(Server{Name: "Dev", Host: "10.0.0.2", Port: 22, Username: "root", Environment: "development"})

	servers, err := repo.List(ListFilter{Environment: "production"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Name != "Prod" {
		t.Errorf("expected 'Prod', got %q", servers[0].Name)
	}
}

func TestDelete(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "To Delete", Host: "10.0.0.1", Port: 22, Username: "root"})

	err := repo.Delete(id)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = repo.GetByID(id)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestSaveAndGetServerInfo(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	id, _ := repo.Create(Server{Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root"})

	info := ServerInfo{
		SSHStatus: "connected",
		Hostname:  "web-01",
		OS:        "Ubuntu 22.04",
	}

	err := repo.SaveServerInfo(id, info, `{"raw":"data"}`)
	if err != nil {
		t.Fatalf("SaveServerInfo failed: %v", err)
	}

	got, err := repo.GetServerInfo(id)
	if err != nil {
		t.Fatalf("GetServerInfo failed: %v", err)
	}
	if got.Hostname != "web-01" {
		t.Errorf("expected hostname 'web-01', got %q", got.Hostname)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/server/ -v
```
Expected: FAIL — `NewRepo` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/server/repo.go
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
		tagsToJSON(s.Tags), s.Environment, s.Region, s.Icon, s.Color, 0,
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
		`SELECT id, name, description, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, created_at, updated_at
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
	query := `SELECT id, name, description, host, port, username, tags, environment, region, icon, color, favorite, created_at, updated_at FROM servers WHERE 1=1`
	args := []interface{}{}

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
	if filter.Query != "" {
		query += " AND (name LIKE ? OR description LIKE ? OR host LIKE ?)"
		pattern := "%" + filter.Query + "%"
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/server/ -v
```
Expected: PASS — all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mod/server/repo.go internal/mod/server/repo_test.go
git commit -m "feat: add server repo with CRUD, filtering, and server info caching"
```

---

### Task 3: Server Service

**Files:**
- Create: `internal/mod/server/service.go`
- Create: `internal/mod/server/service_test.go`

**Interfaces:**
- Consumes: `server.Repo`, `auth.Service` (GetAESKey), `shared.Encrypt`, `shared.Decrypt`
- Produces: `server.Service` with `Create`, `GetByID`, `List`, `Update`, `Delete`, `ToggleFavorite`, `GetServerInfo`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/server/service_test.go
package server

import (
	"database/sql"
	"testing"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
)

func setupServiceTest(t *testing.T) (*Service, *sql.DB) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	db.Migrate(d)

	repo := NewRepo(d)
	authRepo := auth.NewRepo(d)
	authSvc := auth.NewService(authRepo)
	authSvc.Setup("test-password")

	svc := NewService(repo, authSvc)
	return svc, d
}

func TestServiceCreateServer(t *testing.T) {
	svc, d := setupServiceTest(t)
	defer d.Close()

	req := CreateRequest{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "secret",
		Tags:     []string{"web"},
	}

	server, err := svc.Create(req)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if server.ID != 1 {
		t.Errorf("expected ID 1, got %d", server.ID)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got %q", server.Name)
	}
	// Password should not be in response
	if server.Password != "" {
		t.Error("password should not be in response")
	}
}

func TestServiceGetByID(t *testing.T) {
	svc, d := setupServiceTest(t)
	defer d.Close()

	req := CreateRequest{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "secret",
	}
	svc.Create(req)

	server, err := svc.GetByID(1)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if server.Name != "Test Server" {
		t.Errorf("expected 'Test Server', got %q", server.Name)
	}
}

func TestServiceList(t *testing.T) {
	svc, d := setupServiceTest(t)
	defer d.Close()

	svc.Create(CreateRequest{Name: "Server 1", Host: "10.0.0.1", Port: 22, Username: "root"})
	svc.Create(CreateRequest{Name: "Server 2", Host: "10.0.0.2", Port: 22, Username: "root"})

	servers, err := svc.List(ListFilter{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
}

func TestServiceDelete(t *testing.T) {
	svc, d := setupServiceTest(t)
	defer d.Close()

	svc.Create(CreateRequest{Name: "To Delete", Host: "10.0.0.1", Port: 22, Username: "root"})

	err := svc.Delete(1)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = svc.GetByID(1)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestServiceToggleFavorite(t *testing.T) {
	svc, d := setupServiceTest(t)
	defer d.Close()

	svc.Create(CreateRequest{Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root"})

	svc.ToggleFavorite(1)
	server, _ := svc.GetByID(1)
	if !server.Favorite {
		t.Error("expected favorite to be true after toggle")
	}

	svc.ToggleFavorite(1)
	server, _ = svc.GetByID(1)
	if server.Favorite {
		t.Error("expected favorite to be false after second toggle")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/server/ -v -run Service
```
Expected: FAIL — `NewService` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/server/service.go
package server

import (
	"errors"

	"meshium/internal/mod/auth"
	"meshium/internal/shared"
)

type Service struct {
	repo    Repo
	authSvc *auth.Service
}

func NewService(repo Repo, authSvc *auth.Service) *Service {
	return &Service{repo: repo, authSvc: authSvc}
}

func (s *Service) encryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return "", errors.New("app is locked")
	}
	encrypted, err := shared.Encrypt(aesKey, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return string(encrypted), nil
}

func (s *Service) decryptCredential(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	aesKey := s.authSvc.GetAESKey()
	if aesKey == nil {
		return "", errors.New("app is locked")
	}
	decrypted, err := shared.Decrypt(aesKey, []byte(ciphertext))
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func (s *Service) Create(req CreateRequest) (*Server, error) {
	// Encrypt credentials
	encPassword, err := s.encryptCredential(req.Password)
	if err != nil {
		return nil, err
	}
	encSSHKey, err := s.encryptCredential(req.SSHKey)
	if err != nil {
		return nil, err
	}
	encPassphrase, err := s.encryptCredential(req.Passphrase)
	if err != nil {
		return nil, err
	}

	port := req.Port
	if port == 0 {
		port = 22
	}

	srv := Server{
		Name:        req.Name,
		Description: req.Description,
		Host:        req.Host,
		Port:        port,
		Username:    req.Username,
		Password:    encPassword,
		SSHKey:      encSSHKey,
		Passphrase:  encPassphrase,
		Tags:        req.Tags,
		Environment: req.Environment,
		Region:      req.Region,
		Icon:        req.Icon,
		Color:       req.Color,
	}

	id, err := s.repo.Create(srv)
	if err != nil {
		return nil, err
	}

	return s.GetByID(id)
}

func (s *Service) GetByID(id int) (*Server, error) {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	// Strip credentials from response
	srv.Password = ""
	srv.SSHKey = ""
	srv.Passphrase = ""
	return srv, nil
}

func (s *Service) List(filter ListFilter) ([]Server, error) {
	servers, err := s.repo.List(filter)
	if err != nil {
		return nil, err
	}
	// Strip credentials from all responses
	for i := range servers {
		servers[i].Password = ""
		servers[i].SSHKey = ""
		servers[i].Passphrase = ""
	}
	return servers, nil
}

func (s *Service) Update(id int, req UpdateRequest) error {
	srv, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	// Apply updates
	if req.Name != nil {
		srv.Name = *req.Name
	}
	if req.Description != nil {
		srv.Description = *req.Description
	}
	if req.Host != nil {
		srv.Host = *req.Host
	}
	if req.Port != nil {
		srv.Port = *req.Port
	}
	if req.Username != nil {
		srv.Username = *req.Username
	}
	if req.Tags != nil {
		srv.Tags = *req.Tags
	}
	if req.Environment != nil {
		srv.Environment = *req.Environment
	}
	if req.Region != nil {
		srv.Region = *req.Region
	}
	if req.Icon != nil {
		srv.Icon = *req.Icon
	}
	if req.Color != nil {
		srv.Color = *req.Color
	}

	// Re-encrypt credentials if provided
	if req.Password != nil {
		enc, err := s.encryptCredential(*req.Password)
		if err != nil {
			return err
		}
		srv.Password = enc
	}
	if req.SSHKey != nil {
		enc, err := s.encryptCredential(*req.SSHKey)
		if err != nil {
			return err
		}
		srv.SSHKey = enc
	}
	if req.Passphrase != nil {
		enc, err := s.encryptCredential(*req.Passphrase)
		if err != nil {
			return err
		}
		srv.Passphrase = enc
	}

	return s.repo.Update(id, *srv)
}

func (s *Service) Delete(id int) error {
	return s.repo.Delete(id)
}

func (s *Service) ToggleFavorite(id int) error {
	return s.repo.ToggleFavorite(id)
}

func (s *Service) GetServerInfo(serverID int) (*ServerInfo, error) {
	return s.repo.GetServerInfo(serverID)
}

// GetDecryptedCredentials returns decrypted credentials for SSH connection.
// Called by the discovery module.
func (s *Service) GetDecryptedCredentials(serverID int) (password, sshKey, passphrase string, err error) {
	srv, err := s.repo.GetByID(serverID)
	if err != nil {
		return "", "", "", err
	}

	password, err = s.decryptCredential(srv.Password)
	if err != nil {
		return "", "", "", err
	}
	sshKey, err = s.decryptCredential(srv.SSHKey)
	if err != nil {
		return "", "", "", err
	}
	passphrase, err = s.decryptCredential(srv.Passphrase)
	if err != nil {
		return "", "", "", err
	}

	return password, sshKey, passphrase, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/server/ -v
```
Expected: PASS — all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mod/server/service.go internal/mod/server/service_test.go
git commit -m "feat: add server service with credential encryption"
```

---

### Task 4: Server HTTP Handlers

**Files:**
- Create: `internal/mod/server/handler.go`
- Create: `internal/mod/server/handler_test.go`

**Interfaces:**
- Consumes: `server.Service`, `shared.WriteJSON`, `shared.WriteError`
- Produces: `server.Handler` with `RegisterRoutes(mux)`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/server/handler_test.go
package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
)

func setupHandlerTest(t *testing.T) (*Handler, *sql.DB) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	db.Migrate(d)

	repo := NewRepo(d)
	authRepo := auth.NewRepo(d)
	authSvc := auth.NewService(authRepo)
	authSvc.Setup("test-password")
	svc := NewService(repo, authSvc)
	h := NewHandler(svc)
	return h, d
}

func TestHandleCreateServer(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	body, _ := json.Marshal(CreateRequest{
		Name:     "Test Server",
		Host:     "192.168.1.1",
		Port:     22,
		Username: "root",
		Password: "secret",
	})

	req := httptest.NewRequest("POST", "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var server Server
	json.NewDecoder(w.Body).Decode(&server)
	if server.Name != "Test Server" {
		t.Errorf("expected 'Test Server', got %q", server.Name)
	}
	if server.Password != "" {
		t.Error("password should not be in response")
	}
}

func TestHandleListServers(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	// Create a server first
	body, _ := json.Marshal(CreateRequest{
		Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root",
	})
	req := httptest.NewRequest("POST", "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	// List servers
	req = httptest.NewRequest("GET", "/api/servers", nil)
	w = httptest.NewRecorder()
	h.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var servers []Server
	json.NewDecoder(w.Body).Decode(&servers)
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
}

func TestHandleDeleteServer(t *testing.T) {
	h, d := setupHandlerTest(t)
	defer d.Close()

	// Create
	body, _ := json.Marshal(CreateRequest{Name: "Test", Host: "10.0.0.1", Port: 22, Username: "root"})
	req := httptest.NewRequest("POST", "/api/servers", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCreate(w, req)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/servers/1", nil)
	w = httptest.NewRecorder()
	h.handleDelete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/server/ -v -run Handler
```
Expected: FAIL — `NewHandler` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/server/handler.go
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/shared"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/servers", h.handleServers)
	mux.HandleFunc("/api/servers/", h.handleServerByID)
}

func (h *Handler) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

func (h *Handler) handleServerByID(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/servers/{id} or /api/servers/{id}/favorite or /api/servers/{id}/info
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/")
	if len(parts) < 1 {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	id, err := strconv.Atoi(parts[0])
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid server ID", "VALIDATION_ERROR")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, id)
		case http.MethodPut:
			h.handleUpdate(w, r, id)
		case http.MethodDelete:
			h.handleDelete(w, r, id)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "favorite":
		if r.Method == http.MethodPatch {
			h.handleToggleFavorite(w, r, id)
		} else {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
	case "info":
		if r.Method == http.MethodGet {
			h.handleGetInfo(w, r, id)
		} else {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	filter := ListFilter{
		Environment: r.URL.Query().Get("environment"),
		Region:      r.URL.Query().Get("region"),
		Tag:         r.URL.Query().Get("tag"),
		Query:       r.URL.Query().Get("q"),
	}

	servers, err := h.svc.List(filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list servers", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, servers)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.Name == "" || req.Host == "" || req.Username == "" {
		shared.WriteError(w, http.StatusBadRequest, "name, host, and username are required", "VALIDATION_ERROR")
		return
	}

	server, err := h.svc.Create(req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to create server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id int) {
	server, err := h.svc.GetByID(id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "server not found", "SERVER_NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id int) {
	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Update(id, req); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to update server", "INTERNAL")
		return
	}

	server, err := h.svc.GetByID(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, server)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.svc.Delete(id); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to delete server", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleToggleFavorite(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.svc.ToggleFavorite(id); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to toggle favorite", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleGetInfo(w http.ResponseWriter, r *http.Request, id int) {
	info, err := h.svc.GetServerInfo(id)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, "server info not found", "SERVER_NOT_FOUND")
		return
	}

	shared.WriteJSON(w, http.StatusOK, info)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/server/ -v
```
Expected: PASS — all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mod/server/handler.go internal/mod/server/handler_test.go
git commit -m "feat: add server HTTP handlers (CRUD, favorite, info)"
```

---

### Task 5: SSH Key Management Endpoints

**Files:**
- Modify: `internal/mod/auth/handler.go` (add SSH key endpoints)
- Modify: `internal/mod/auth/handler_test.go`

**Interfaces:**
- Consumes: `auth.Repo` (GetSSHPublicKey, SetSSHPublicKey, GetEncryptedSSHKey, SetEncryptedSSHKey), `ssh.GenerateKeyPair`, `shared.Encrypt`, `shared.Decrypt`
- Produces: `GET /api/ssh-key/public`, `POST /api/ssh-key/regenerate`

- [ ] **Step 1: Add SSH key methods to auth service**

Add to `internal/mod/auth/service.go`:

```go
// GetSSHPublicKey returns the app's SSH public key.
func (s *Service) GetSSHPublicKey() (string, error) {
	return s.repo.GetSSHPublicKey()
}

// RegenerateSSHKey generates a new SSH key pair, encrypts the private key,
// and stores both keys in app_config.
func (s *Service) RegenerateSSHKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.locked {
		return "", errors.New("app is locked")
	}

	// Generate new key pair
	privatePEM, publicSSH, err := ssh.GenerateKeyPair()
	if err != nil {
		return "", err
	}

	// Encrypt private key with AES
	encrypted, err := shared.Encrypt(s.aesKey, privatePEM)
	if err != nil {
		return "", err
	}

	// Store keys
	if err := s.repo.SetEncryptedSSHKey(string(encrypted)); err != nil {
		return "", err
	}
	if err := s.repo.SetSSHPublicKey(string(publicSSH)); err != nil {
		return "", err
	}

	return string(publicSSH), nil
}

// EnsureSSHKey generates a key pair if one doesn't exist yet.
// Called during Setup.
func (s *Service) EnsureSSHKey() error {
	existing, err := s.repo.GetSSHPublicKey()
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}

	privatePEM, publicSSH, err := ssh.GenerateKeyPair()
	if err != nil {
		return err
	}

	encrypted, err := shared.Encrypt(s.aesKey, privatePEM)
	if err != nil {
		return err
	}

	if err := s.repo.SetEncryptedSSHKey(string(encrypted)); err != nil {
		return err
	}
	return s.repo.SetSSHPublicKey(string(publicSSH))
}
```

- [ ] **Step 2: Update Setup to call EnsureSSHKey**

In `internal/mod/auth/service.go`, add at the end of the `Setup` method, before `return nil`:

```go
	// Generate SSH key pair
	if err := s.EnsureSSHKey(); err != nil {
		return err
	}
```

- [ ] **Step 3: Add imports to service.go**

```go
import (
	"errors"
	"sync"

	"meshium/internal/mod/ssh"
	"meshium/internal/shared"
)
```

- [ ] **Step 4: Add SSH key endpoints to handler**

Add to `internal/mod/auth/handler.go`:

```go
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/setup", h.handleSetup)
	mux.HandleFunc("/api/auth/unlock", h.handleUnlock)
	mux.HandleFunc("/api/auth/lock", h.handleLock)
	mux.HandleFunc("/api/auth/status", h.handleStatus)
	mux.HandleFunc("/api/ssh-key/public", h.handleSSHKeyPublic)
	mux.HandleFunc("/api/ssh-key/regenerate", h.handleSSHKeyRegenerate)
}

func (h *Handler) handleSSHKeyPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	key, err := h.svc.GetSSHPublicKey()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, SSHKeyResponse{PublicKey: key})
}

func (h *Handler) handleSSHKeyRegenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	key, err := h.svc.RegenerateSSHKey()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to regenerate key", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, SSHKeyResponse{PublicKey: key})
}
```

- [ ] **Step 5: Run all tests to verify they pass**

```bash
go test ./... -v
```
Expected: PASS — all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/mod/auth/service.go internal/mod/auth/handler.go
git commit -m "feat: add SSH key management endpoints (public key, regenerate)"
```

---

### Task 6: Wire Server Manager into Main Server

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Update main.go to include server module**

This step is already covered in Part 4 Task 5 (which wires all modules together). Ensure that `serverHandler.RegisterRoutes(mux)` is included in the router setup.

- [ ] **Step 2: Verify it compiles**

```bash
go build ./cmd/server/
```

- [ ] **Step 3: Commit (if any changes were needed)**

```bash
git add cmd/server/main.go
git commit -m "feat: wire server manager into main server"
```
