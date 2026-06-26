# Part 1: Project Scaffold & Crypto Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Set up the Go project structure, SQLite database, and crypto foundation (master password, AES-256 encryption) for Meshium.

**Architecture:** Modular domain-driven Go backend. This part creates the project skeleton, database layer, shared crypto utilities, and the auth module (master password setup/unlock/lock). The HTTP server starts but only serves auth endpoints.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite` (pure Go SQLite), `golang.org/x/crypto` (bcrypt, ssh), `gorilla/websocket`, `net/http` (stdlib router)

## Global Constraints

- Go module name: `meshium`
- Go version: 1.22+
- SQLite driver: `modernc.org/sqlite` (pure Go, no CGO)
- All credentials encrypted with AES-256-GCM before storage
- Master password hashed with bcrypt (cost 14)
- AES key derived from master password via PBKDF2 (SHA-256, 600000 iterations)
- API responses never include credential fields
- Error format: `{"error": "string", "code": "ERROR_CODE"}`

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Go module definition |
| `cmd/server/main.go` | Entry point, HTTP server, router setup |
| `internal/db/db.go` | SQLite connection, migration runner |
| `internal/db/migrations.go` | SQL migration definitions |
| `internal/shared/config.go` | App configuration (DB path, server port) |
| `internal/shared/crypto.go` | AES-256-GCM encrypt/decrypt, bcrypt, PBKDF2 |
| `internal/shared/types.go` | Shared types (API errors, JSON utils) |
| `internal/mod/auth/model.go` | Auth request/response DTOs |
| `internal/mod/auth/repo.go` | app_config table access |
| `internal/mod/auth/service.go` | Master password setup, unlock, lock logic |
| `internal/mod/auth/handler.go` | REST handlers for auth endpoints |
| `internal/mod/auth/handler_test.go` | API tests for auth endpoints |

---

### Task 1: Go Module Init

**Files:**
- Create: `go.mod`
- Create: `cmd/server/main.go`

**Interfaces:**
- Produces: `meshium` Go module, `main()` function

- [ ] **Step 1: Initialize Go module**

```bash
cd /root/meshium
go mod init meshium
```

- [ ] **Step 2: Create minimal main.go**

```go
// cmd/server/main.go
package main

import (
	"fmt"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	fmt.Println("Meshium server starting on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}
```

- [ ] **Step 3: Verify it compiles and runs**

```bash
go build ./cmd/server/
go run ./cmd/server/ &
curl -s http://localhost:8080/api/health
kill %1
```
Expected: `{"status":"ok"}`

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum cmd/
git commit -m "feat: init Go module and minimal HTTP server"
```

---

### Task 2: Config Package

**Files:**
- Create: `internal/shared/config.go`

**Interfaces:**
- Produces: `shared.Config` struct, `shared.LoadConfig()` function

- [ ] **Step 1: Write config package**

```go
// internal/shared/config.go
package shared

import (
	"os"
	"path/filepath"
)

type Config struct {
	DBPath   string
	ServerPort string
	DataDir  string
}

func LoadConfig() *Config {
	dataDir := os.Getenv("MESHium_DATA_DIR")
	if dataDir == "" {
		homeDir, _ := os.UserHomeDir()
		dataDir = filepath.Join(homeDir, ".meshium")
	}
	os.MkdirAll(dataDir, 0700)

	return &Config{
		DBPath:     filepath.Join(dataDir, "meshium.db"),
		ServerPort: getEnv("MESHium_PORT", "8080"),
		DataDir:    dataDir,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/shared/
```

- [ ] **Step 3: Commit**

```bash
git add internal/shared/config.go
git commit -m "feat: add config package with data directory support"
```

---

### Task 3: Crypto Package

**Files:**
- Create: `internal/shared/crypto.go`
- Create: `internal/shared/crypto_test.go`

**Interfaces:**
- Produces: `shared.Encrypt(key, plaintext) → ciphertext`, `shared.Decrypt(key, ciphertext) → plaintext`, `shared.HashPassword(password) → hash`, `shared.VerifyPassword(password, hash) → bool`, `shared.DeriveKey(password, salt) → key`

- [ ] **Step 1: Write the failing test**

```go
// internal/shared/crypto_test.go
package shared

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes for AES-256
	plaintext := []byte("super-secret-password")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := []byte("same-input")

	ct1, _ := Encrypt(key, plaintext)
	ct2, _ := Encrypt(key, plaintext)

	if bytes.Equal(ct1, ct2) {
		t.Error("ciphertext should differ due to random nonce")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	password := "my-master-password"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !VerifyPassword(password, hash) {
		t.Error("VerifyPassword should return true for correct password")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Error("VerifyPassword should return false for wrong password")
	}
}

func TestDeriveKey(t *testing.T) {
	password := "my-master-password"
	salt := []byte("0123456789abcdef")

	key := DeriveKey(password, salt)

	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}

	// Same input should produce same key
	key2 := DeriveKey(password, salt)
	if !bytes.Equal(key, key2) {
		t.Error("DeriveKey should be deterministic")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/shared/ -v
```
Expected: FAIL — functions not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/shared/crypto.go
package shared

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

// Encrypt encrypts plaintext using AES-256-GCM with the given key.
// Returns nonce prepended to ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ciphertext...), nil
}

// Decrypt decrypts ciphertext (nonce-prepended) using AES-256-GCM.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword compares a password against a bcrypt hash.
func VerifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// DeriveKey derives a 32-byte AES key from a password and salt using PBKDF2.
func DeriveKey(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, 600000, 32, sha256.New)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/shared/ -v
```
Expected: PASS — all 4 tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/shared/crypto.go internal/shared/crypto_test.go go.mod go.sum
git commit -m "feat: add crypto package (AES-256-GCM, bcrypt, PBKDF2)"
```

---

### Task 4: Shared Types

**Files:**
- Create: `internal/shared/types.go`

**Interfaces:**
- Produces: `shared.APIError` struct, `shared.WriteJSON()`, `shared.WriteError()`

- [ ] **Step 1: Write shared types**

```go
// internal/shared/types.go
package shared

import (
	"encoding/json"
	"net/http"
)

// APIError is the standard error response format.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes a standard API error response.
func WriteError(w http.ResponseWriter, status int, message, code string) {
	WriteJSON(w, status, APIError{Error: message, Code: code})
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/shared/
```

- [ ] **Step 3: Commit**

```bash
git add internal/shared/types.go
git commit -m "feat: add shared types (APIError, WriteJSON, WriteError)"
```

---

### Task 5: Database Layer

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/migrations.go`
- Create: `internal/db/db_test.go`

**Interfaces:**
- Produces: `db.Open(path) → *sql.DB`, `db.Migrate(db) → error`

- [ ] **Step 1: Write the failing test**

```go
// internal/db/db_test.go
package db

import (
	"database/sql"
	"testing"
)

func TestMigrateCreatesTables(t *testing.T) {
	dbConn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer dbConn.Close()

	if err := Migrate(dbConn); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Check app_config table exists
	var name string
	err = dbConn.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='app_config'",
	).Scan(&name)
	if err != nil {
		t.Error("app_config table not created")
	}

	// Check servers table exists
	err = dbConn.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='servers'",
	).Scan(&name)
	if err != nil {
		t.Error("servers table not created")
	}

	// Check server_info table exists
	err = dbConn.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='server_info'",
	).Scan(&name)
	if err != nil {
		t.Error("server_info table not created")
	}

	// Check known_hosts table exists
	err = dbConn.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='known_hosts'",
	).Scan(&name)
	if err != nil {
		t.Error("known_hosts table not created")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/db/ -v
```
Expected: FAIL — package not found

- [ ] **Step 3: Write the implementation**

```go
// internal/db/db.go
package db

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite database at the given path.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Enable foreign keys
	db.Exec("PRAGMA foreign_keys = ON")
	return db, nil
}
```

```go
// internal/db/migrations.go
package db

import (
	"database/sql"
)

// Migrate runs all database migrations.
func Migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS app_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS servers (
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
		);

		CREATE TABLE IF NOT EXISTS server_info (
			server_id       INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			ssh_status      TEXT,
			latency_ms      INTEGER,
			cpu_model       TEXT,
			cpu_cores       INTEGER,
			ram_total_mb    INTEGER,
			disk_total_gb   REAL,
			kernel          TEXT,
			architecture    TEXT,
			os              TEXT,
			virtualization  TEXT,
			provider        TEXT,
			public_ip       TEXT,
			private_ip      TEXT,
			timezone        TEXT,
			hostname        TEXT,
			raw_data        TEXT,
			last_checked    DATETIME,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS known_hosts (
			server_id   INTEGER REFERENCES servers(id) ON DELETE CASCADE,
			host_key    TEXT NOT NULL,
			host        TEXT NOT NULL,
			port        INTEGER NOT NULL,
			verified    INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(host, port)
		);
	`)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/db/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add internal/db/ go.mod go.sum
git commit -m "feat: add database layer with SQLite migrations"
```

---

### Task 6: Auth Module — Model & Repo

**Files:**
- Create: `internal/mod/auth/model.go`
- Create: `internal/mod/auth/repo.go`

**Interfaces:**
- Produces: `auth.SetupRequest`, `auth.UnlockRequest`, `auth.AuthStatus` structs, `auth.Repo` interface with `GetConfigValue`, `SetConfigValue`, `HasMasterPassword`, `SetMasterPassword`, `GetEncryptedSSHKey`, `SetEncryptedSSHKey`

- [ ] **Step 1: Write auth models**

```go
// internal/mod/auth/model.go
package auth

type SetupRequest struct {
	Password string `json:"password"`
}

type UnlockRequest struct {
	Password string `json:"password"`
}

type AuthStatus struct {
	Setup   bool `json:"setup"`
	Locked  bool `json:"locked"`
}

type SSHKeyResponse struct {
	PublicKey string `json:"publicKey"`
}
```

- [ ] **Step 2: Write auth repo**

```go
// internal/mod/auth/repo.go
package auth

import (
	"database/sql"
	"errors"
)

type Repo interface {
	GetConfigValue(key string) (string, error)
	SetConfigValue(key, value string) error
	HasMasterPassword() (bool, error)
	SetMasterPassword(hash string) error
	GetEncryptedSSHKey() (string, error)
	SetEncryptedSSHKey(key string) error
	GetSSHPublicKey() (string, error)
	SetSSHPublicKey(key string) error
}

type sqliteRepo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) Repo {
	return &sqliteRepo{db: db}
}

func (r *sqliteRepo) GetConfigValue(key string) (string, error) {
	var value string
	err := r.db.QueryRow("SELECT value FROM app_config WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (r *sqliteRepo) SetConfigValue(key, value string) error {
	_, err := r.db.Exec(
		"INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		key, value, value,
	)
	return err
}

func (r *sqliteRepo) HasMasterPassword() (bool, error) {
	val, err := r.GetConfigValue("master_password_hash")
	if err != nil {
		return false, err
	}
	return val != "", nil
}

func (r *sqliteRepo) SetMasterPassword(hash string) error {
	return r.SetConfigValue("master_password_hash", hash)
}

func (r *sqliteRepo) GetEncryptedSSHKey() (string, error) {
	return r.GetConfigValue("ssh_key_private_encrypted")
}

func (r *sqliteRepo) SetEncryptedSSHKey(key string) error {
	return r.SetConfigValue("ssh_key_private_encrypted", key)
}

func (r *sqliteRepo) GetSSHPublicKey() (string, error) {
	return r.GetConfigValue("ssh_key_public")
}

func (r *sqliteRepo) SetSSHPublicKey(key string) error {
	return r.SetConfigValue("ssh_key_public", key)
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/mod/auth/
```

- [ ] **Step 4: Commit**

```bash
git add internal/mod/auth/
git commit -m "feat: add auth model and repo (app_config access)"
```

---

### Task 7: Auth Module — Service

**Files:**
- Create: `internal/mod/auth/service.go`
- Create: `internal/mod/auth/service_test.go`

**Interfaces:**
- Consumes: `auth.Repo`, `shared.HashPassword`, `shared.VerifyPassword`, `shared.DeriveKey`, `shared.Encrypt`, `shared.Decrypt`
- Produces: `auth.Service` struct with `Setup`, `Unlock`, `Lock`, `IsSetup`, `IsLocked`, `GetAESKey`, `RegenerateSSHKey`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/auth/service_test.go
package auth

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

func TestSetupCreatesMasterPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	// Before setup
	setup, _ := svc.IsSetup()
	if setup {
		t.Error("should not be setup before Setup()")
	}

	// Setup
	err := svc.Setup("my-password")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// After setup
	setup, _ = svc.IsSetup()
	if !setup {
		t.Error("should be setup after Setup()")
	}

	// Should be unlocked after setup
	if svc.IsLocked() {
		t.Error("should be unlocked after Setup()")
	}
}

func TestUnlockWithCorrectPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")
	svc.Lock()

	if !svc.IsLocked() {
		t.Error("should be locked after Lock()")
	}

	err := svc.Unlock("my-password")
	if err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	if svc.IsLocked() {
		t.Error("should be unlocked after correct Unlock()")
	}
}

func TestUnlockWithWrongPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")
	svc.Lock()

	err := svc.Unlock("wrong-password")
	if err == nil {
		t.Error("Unlock with wrong password should fail")
	}
}

func TestGetAESKey(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")

	key := svc.GetAESKey()
	if len(key) != 32 {
		t.Errorf("expected 32-byte AES key, got %d", len(key))
	}
}

func TestLockClearsKey(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")
	svc.Lock()

	key := svc.GetAESKey()
	if key != nil {
		t.Error("AES key should be nil after Lock()")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/auth/ -v
```
Expected: FAIL — `NewService` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/auth/service.go
package auth

import (
	"errors"
	"sync"

	"meshium/internal/shared"
)

type Service struct {
	repo      Repo
	mu        sync.RWMutex
	aesKey    []byte
	locked    bool
}

func NewService(repo Repo) *Service {
	return &Service{repo: repo, locked: true}
}

// IsSetup returns true if a master password has been set.
func (s *Service) IsSetup() (bool, error) {
	return s.repo.HasMasterPassword()
}

// IsLocked returns true if the app is locked (no AES key in memory).
func (s *Service) IsLocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.locked
}

// GetAESKey returns the current AES key (nil if locked).
func (s *Service) GetAESKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aesKey
}

// Setup sets the master password for the first time. Generates SSH key pair.
func (s *Service) Setup(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	setup, err := s.repo.HasMasterPassword()
	if err != nil {
		return err
	}
	if setup {
		return errors.New("master password already set")
	}

	// Hash and store master password
	hash, err := shared.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.repo.SetMasterPassword(hash); err != nil {
		return err
	}

	// Derive AES key and keep in memory
	salt := []byte("meshium-salt-v1") // fixed salt for key derivation
	s.aesKey = shared.DeriveKey(password, salt)
	s.locked = false

	return nil
}

// Unlock verifies the master password and loads the AES key into memory.
func (s *Service) Unlock(password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	hash, err := s.repo.GetConfigValue("master_password_hash")
	if err != nil {
		return err
	}
	if hash == "" {
		return errors.New("master password not set")
	}

	if !shared.VerifyPassword(password, hash) {
		return errors.New("invalid password")
	}

	salt := []byte("meshium-salt-v1")
	s.aesKey = shared.DeriveKey(password, salt)
	s.locked = false
	return nil
}

// Lock clears the AES key from memory.
func (s *Service) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Zero out the key
	for i := range s.aesKey {
		s.aesKey[i] = 0
	}
	s.aesKey = nil
	s.locked = true
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/auth/ -v
```
Expected: PASS — all 5 tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mod/auth/service.go internal/mod/auth/service_test.go
git commit -m "feat: add auth service (setup, unlock, lock, AES key management)"
```

---

### Task 8: Auth Module — HTTP Handlers

**Files:**
- Create: `internal/mod/auth/handler.go`
- Create: `internal/mod/auth/handler_test.go`

**Interfaces:**
- Consumes: `auth.Service`, `shared.WriteJSON`, `shared.WriteError`
- Produces: `auth.Handler` struct with `RegisterRoutes(mux)`

- [ ] **Step 1: Write the failing test**

```go
// internal/mod/auth/handler_test.go
package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"meshium/internal/db"
)

func TestSetupEndpoint(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	h := NewHandler(svc)

	// GET /api/auth/status — should show setup=false
	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	h.handleStatus(w, req)

	var status AuthStatus
	json.NewDecoder(w.Body).Decode(&status)
	if status.Setup {
		t.Error("should not be setup initially")
	}
	if !status.Locked {
		t.Error("should be locked initially")
	}

	// POST /api/auth/setup
	body, _ := json.Marshal(SetupRequest{Password: "test-password"})
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w = httptest.NewRecorder()
	h.handleSetup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// GET /api/auth/status — should show setup=true, locked=false
	req = httptest.NewRequest("GET", "/api/auth/status", nil)
	w = httptest.NewRecorder()
	h.handleStatus(w, req)

	json.NewDecoder(w.Body).Decode(&status)
	if !status.Setup {
		t.Error("should be setup after setup()")
	}
	if status.Locked {
		t.Error("should be unlocked after setup()")
	}
}

func TestUnlockLockEndpoints(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)
	h := NewHandler(svc)

	svc.Setup("test-password")
	svc.Lock()

	// POST /api/auth/unlock — wrong password
	body, _ := json.Marshal(UnlockRequest{Password: "wrong"})
	req := httptest.NewRequest("POST", "/api/auth/unlock", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleUnlock(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong password, got %d", w.Code)
	}

	// POST /api/auth/unlock — correct password
	body, _ = json.Marshal(UnlockRequest{Password: "test-password"})
	req = httptest.NewRequest("POST", "/api/auth/unlock", bytes.NewReader(body))
	w = httptest.NewRecorder()
	h.handleUnlock(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// POST /api/auth/lock
	req = httptest.NewRequest("POST", "/api/auth/lock", nil)
	w = httptest.NewRecorder()
	h.handleLock(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mod/auth/ -v -run Handler
```
Expected: FAIL — `NewHandler` not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/mod/auth/handler.go
package auth

import (
	"encoding/json"
	"net/http"

	"meshium/internal/shared"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/setup", h.handleSetup)
	mux.HandleFunc("/api/auth/unlock", h.handleUnlock)
	mux.HandleFunc("/api/auth/lock", h.handleLock)
	mux.HandleFunc("/api/auth/status", h.handleStatus)
}

func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	setup, err := h.svc.IsSetup()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	if setup {
		shared.WriteError(w, http.StatusBadRequest, "master password already set", "ALREADY_SETUP")
		return
	}

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}
	if len(req.Password) < 8 {
		shared.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Setup(req.Password); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "setup failed", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	var req UnlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if err := h.svc.Unlock(req.Password); err != nil {
		shared.WriteError(w, http.StatusUnauthorized, "invalid password", "AUTH_FAILED")
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	h.svc.Lock()
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}

	setup, err := h.svc.IsSetup()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusOK, AuthStatus{
		Setup:  setup,
		Locked: h.svc.IsLocked(),
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/mod/auth/ -v
```
Expected: PASS — all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/mod/auth/handler.go internal/mod/auth/handler_test.go
git commit -m "feat: add auth HTTP handlers (setup, unlock, lock, status)"
```

---

### Task 9: Wire Up Main Server

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `shared.LoadConfig`, `db.Open`, `db.Migrate`, `auth.NewRepo`, `auth.NewService`, `auth.NewHandler`

- [ ] **Step 1: Update main.go to wire everything together**

```go
// cmd/server/main.go
package main

import (
	"fmt"
	"net/http"
	"os"

	"meshium/internal/db"
	"meshium/internal/mod/auth"
	"meshium/internal/shared"
)

func main() {
	cfg := shared.LoadConfig()

	// Open database
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Printf("Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	// Run migrations
	if err := db.Migrate(d); err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	// Initialize auth
	authRepo := auth.NewRepo(d)
	authSvc := auth.NewService(authRepo)
	authHandler := auth.NewHandler(authSvc)

	// Setup router
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	authHandler.RegisterRoutes(mux)

	// Start server
	addr := ":" + cfg.ServerPort
	fmt.Printf("Meshium server starting on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Verify it compiles and runs**

```bash
go build ./cmd/server/
go run ./cmd/server/ &
sleep 1
curl -s http://localhost:8080/api/health
curl -s http://localhost:8080/api/auth/status
kill %1
```
Expected:
- `{"status":"ok"}`
- `{"setup":false,"locked":true}`

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire up main server with auth endpoints"
```

---

### Task 10: Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write Makefile**

```makefile
.PHONY: build dev test clean

build:
	go build -o bin/meshium ./cmd/server/

dev:
	go run ./cmd/server/

test:
	go test ./... -v

clean:
	rm -rf bin/
```

- [ ] **Step 2: Verify it works**

```bash
make build
make test
```

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile (build, dev, test, clean)"
```
