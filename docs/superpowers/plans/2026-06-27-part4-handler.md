# Part 4: Handler (REST + WebSocket)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the REST API handlers and WebSocket handler for the migration engine, and wire everything into the main server.

**Architecture:** The REST handler exposes endpoints for creating plans, listing migrations, getting details, deleting, and triggering rollback. The WebSocket handler streams live progress during plan creation and execution. The handler follows the same pattern as `discovery.Handler` and `server.Handler`.

**Tech Stack:** Go 1.22+, `net/http`, `github.com/gorilla/websocket`, `encoding/json`

## Global Constraints

- Handler lives in `internal/mod/migration/`
- REST endpoints follow the pattern: `/api/migrations`, `/api/migrations/{id}`, etc.
- WebSocket endpoint: `/ws/migrate/{id}` for execution, `/ws/plan` for planning
- Uses `shared.WriteJSON` and `shared.WriteError` for REST responses
- WebSocket uses the same `WSMessage` format as discovery
- Handler is wired into `cmd/server/main.go` following the same pattern as discovery

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/mod/migration/handler.go` | REST + WebSocket handlers |
| `internal/mod/migration/handler_test.go` | Handler tests |
| `cmd/server/main.go` | Wire migration handler into server (modify) |

---

### Task 1: REST Handler

**Files:**
- Create: `internal/mod/migration/handler.go`

**Interfaces:**
- Consumes: `Planner`, `Executor`, `RollbackManager`, `Repo`
- Produces: `Handler` struct, `MigrationRunner` interface

- [ ] **Step 1: Write the REST handler**

```go
// internal/mod/migration/handler.go
package migration

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"meshium/internal/shared"

	"github.com/gorilla/websocket"
)

// MigrationRunner exposes the planning and execution methods needed by the handler.
type MigrationRunner interface {
	Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error)
	Execute(ctx context.Context, migrationID int, onProgress StepCallback) error
	Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error
}

// Handler exposes REST and WebSocket routes for migrations.
type Handler struct {
	runner   MigrationRunner
	repo     Repo
	upgrader websocket.Upgrader
}

// NewHandler creates a migration Handler.
func NewHandler(runner MigrationRunner, repo Repo) *Handler {
	return &Handler{
		runner: runner,
		repo:   repo,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// RegisterRoutes registers all migration routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/migrations", h.handleMigrations)
	mux.HandleFunc("/api/migrations/", h.handleMigrationByID)
	mux.HandleFunc("/ws/migrate/", h.handleMigrateWS)
	mux.HandleFunc("/ws/plan", h.handlePlanWS)
}

// --- REST: /api/migrations ---

func (h *Handler) handleMigrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	}
}

// --- REST: /api/migrations/{id} ---

func (h *Handler) handleMigrationByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/migrations/"), "/")
	if path == "" {
		shared.WriteError(w, http.StatusBadRequest, "invalid path", "VALIDATION_ERROR")
		return
	}

	parts := strings.Split(path, "/")
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid migration ID", "VALIDATION_ERROR")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, id)
		case http.MethodDelete:
			h.handleDelete(w, r, id)
		default:
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		}
		return
	}

	switch parts[1] {
	case "rollback":
		if r.Method != http.MethodPost {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleRollback(w, r, id)
	case "steps":
		if r.Method != http.MethodGet {
			shared.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		h.handleGetSteps(w, r, id)
	default:
		shared.WriteError(w, http.StatusNotFound, "not found", "NOT_FOUND")
	}
}

// --- REST handlers ---

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	migrations, err := h.repo.ListMigrations()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to list migrations", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, migrations)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req PlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "invalid request body", "VALIDATION_ERROR")
		return
	}

	if req.SourceServerID == 0 || req.TargetServerID == 0 {
		shared.WriteError(w, http.StatusBadRequest, "sourceServerId and targetServerId are required", "VALIDATION_ERROR")
		return
	}

	if len(req.Categories) == 0 {
		shared.WriteError(w, http.StatusBadRequest, "at least one category is required", "VALIDATION_ERROR")
		return
	}

	// Plan synchronously (for REST, without WebSocket progress)
	plan, err := h.runner.Plan(r.Context(), req, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to create plan: "+err.Error(), "INTERNAL")
		return
	}

	shared.WriteJSON(w, http.StatusCreated, plan)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id int) {
	plan, err := h.repo.GetMigration(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to get migration", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, plan)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.repo.DeleteMigration(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			shared.WriteError(w, http.StatusNotFound, "migration not found", "MIGRATION_NOT_FOUND")
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, "failed to delete migration", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request, id int) {
	// Rollback synchronously (for REST)
	err := h.runner.Rollback(r.Context(), id, nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "rollback failed: "+err.Error(), "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "rolled_back"})
}

func (h *Handler) handleGetSteps(w http.ResponseWriter, r *http.Request, id int) {
	steps, err := h.repo.GetSteps(id)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, "failed to get steps", "INTERNAL")
		return
	}
	shared.WriteJSON(w, http.StatusOK, steps)
}

// --- WebSocket handlers ---

func (h *Handler) handlePlanWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		log.Printf("migration runner not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Read plan request from query params or body
	var req PlanRequest
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	} else {
		// GET with query params
		sourceID, _ := strconv.Atoi(r.URL.Query().Get("source"))
		targetID, _ := strconv.Atoi(r.URL.Query().Get("target"))
		categories := strings.Split(r.URL.Query().Get("categories"), ",")
		req = PlanRequest{
			SourceServerID: sourceID,
			TargetServerID: targetID,
			Categories:     categories,
		}
	}

	if req.SourceServerID == 0 || req.TargetServerID == 0 {
		http.Error(w, "sourceServerId and targetServerId are required", http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Stream planning progress
	_, err = h.runner.Plan(ctx, req, func(msg WSMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("websocket write failed: %v", writeErr)
			cancel()
		}
	})

	if err != nil {
		conn.WriteJSON(WSMessage{Step: "plan", Status: "error", Error: err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{Step: "plan", Status: "complete"})
}

func (h *Handler) handleMigrateWS(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.runner == nil {
		log.Printf("migration runner not configured")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ws/migrate/")
	if path == r.URL.Path || path == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	parts := strings.Split(path, "/")
	migrationID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "invalid migration ID", http.StatusBadRequest)
		return
	}

	action := "execute"
	if len(parts) > 1 && parts[1] == "rollback" {
		action = "rollback"
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var runErr error
	if action == "rollback" {
		runErr = h.runner.Rollback(ctx, migrationID, func(msg WSMessage) {
			if writeErr := conn.WriteJSON(msg); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	} else {
		runErr = h.runner.Execute(ctx, migrationID, func(msg WSMessage) {
			if writeErr := conn.WriteJSON(msg); writeErr != nil {
				log.Printf("websocket write failed: %v", writeErr)
				cancel()
			}
		})
	}

	if runErr != nil {
		conn.WriteJSON(WSMessage{
			Step:   action,
			Status: "error",
			Error:  runErr.Error(),
		})
		return
	}

	conn.WriteJSON(WSMessage{Step: action, Status: "complete"})
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /root/meshium
go build ./internal/mod/migration/
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/handler.go
git commit -m "feat: add migration REST and WebSocket handlers"
```

---

### Task 2: Handler Tests

**Files:**
- Create: `internal/mod/migration/handler_test.go`

**Interfaces:**
- Consumes: `Handler`, `Repo` (mock)

- [ ] **Step 1: Write handler tests**

```go
// internal/mod/migration/handler_test.go
package migration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockRepo implements Repo for testing.
type mockRepo struct {
	migrations []MigrationPlan
	steps      []MigrationStep
	backups    []MigrationBackup
}

func (m *mockRepo) CreateMigration(p *MigrationPlan) (int, error) {
	p.ID = len(m.migrations) + 1
	m.migrations = append(m.migrations, *p)
	return p.ID, nil
}

func (m *mockRepo) GetMigration(id int) (*MigrationPlan, error) {
	for _, p := range m.migrations {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, errNotFound
}

func (m *mockRepo) ListMigrations() ([]MigrationPlan, error) {
	return m.migrations, nil
}

func (m *mockRepo) UpdateMigrationStatus(id int, status, errMsg string) error {
	for i := range m.migrations {
		if m.migrations[i].ID == id {
			m.migrations[i].Status = status
			return nil
		}
	}
	return errNotFound
}

func (m *mockRepo) DeleteMigration(id int) error {
	for i, p := range m.migrations {
		if p.ID == id {
			m.migrations = append(m.migrations[:i], m.migrations[i+1:]...)
			return nil
		}
	}
	return errNotFound
}

func (m *mockRepo) CreateStep(s MigrationStep) (int, error) {
	s.ID = len(m.steps) + 1
	m.steps = append(m.steps, s)
	return s.ID, nil
}

func (m *mockRepo) GetSteps(migrationID int) ([]MigrationStep, error) {
	var result []MigrationStep
	for _, s := range m.steps {
		if s.MigrationID == migrationID {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockRepo) UpdateStepStatus(stepID int, status, errMsg string) error {
	return nil
}

func (m *mockRepo) CreateBackup(b MigrationBackup) (int, error) {
	b.ID = len(m.backups) + 1
	m.backups = append(m.backups, b)
	return b.ID, nil
}

func (m *mockRepo) GetBackups(migrationID int) ([]MigrationBackup, error) {
	var result []MigrationBackup
	for _, b := range m.backups {
		if b.MigrationID == migrationID {
			result = append(result, b)
		}
	}
	return result, nil
}

func (m *mockRepo) SetMigrationCompletedAt(id int, ts string) error { return nil }
func (m *mockRepo) SetMigrationRolledBackAt(id int, ts string) error { return nil }

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "not found" }

func TestHandleList(t *testing.T) {
	repo := &mockRepo{
		migrations: []MigrationPlan{
			{ID: 1, SourceServerID: 1, TargetServerID: 2, Status: StatusPlanned},
		},
	}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("GET", "/api/migrations", nil)
	w := httptest.NewRecorder()
	handler.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGetNotFound(t *testing.T) {
	repo := &mockRepo{}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("GET", "/api/migrations/999", nil)
	w := httptest.NewRecorder()
	handler.handleGet(w, req, 999)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleDelete(t *testing.T) {
	repo := &mockRepo{
		migrations: []MigrationPlan{
			{ID: 1, Status: StatusPlanned},
		},
	}
	handler := NewHandler(nil, repo)

	req := httptest.NewRequest("DELETE", "/api/migrations/1", nil)
	w := httptest.NewRecorder()
	handler.handleDelete(w, req, 1)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCreateValidation(t *testing.T) {
	repo := &mockRepo{}
	handler := NewHandler(nil, repo)

	// Missing sourceServerId
	body := `{"targetServerId":2,"categories":["packages"]}`
	req := httptest.NewRequest("POST", "/api/migrations", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.handleCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/mod/migration/ -v -run TestHandle
```

- [ ] **Step 3: Commit**

```bash
git add internal/mod/migration/handler_test.go
git commit -m "test: add migration handler tests with mock repo"
```

---

### Task 3: Wire Migration Handler into Main Server

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `migration.NewCategoryRegistry`, `migration.NewPlanner`, `migration.NewExecutor`, `migration.NewRollbackManager`, `migration.NewHandler`

- [ ] **Step 1: Update main.go to wire migration module**

Add the migration module to `cmd/server/main.go`. Insert after the discovery handler wiring and before the `mux.Handle("/", staticHandler())` line:

```go
// Add to imports:
import (
	"meshium/internal/mod/migration"
)

// Add after discoveryHandler wiring:
	migrationRepo := migration.NewRepo(database)
	migrationRegistry := migration.NewCategoryRegistry()
	migrationPlanner := migration.NewPlanner(migrationRegistry, migrationRepo, serverRepo, discovery.NewPoolAdapter(sshPool), authSvc, knownHosts)
	migrationExecutor := migration.NewExecutor(migrationRegistry, migrationRepo, serverRepo, discovery.NewPoolAdapter(sshPool), authSvc, knownHosts)
	migrationRollback := migration.NewRollbackManager(migrationRegistry, migrationRepo, serverRepo, discovery.NewPoolAdapter(sshPool), authSvc, knownHosts)

	// Create a composite runner that delegates to planner/executor/rollback
	migrationRunner := migration.NewCompositeRunner(migrationPlanner, migrationExecutor, migrationRollback)
	migrationHandler := migration.NewHandler(migrationRunner, migrationRepo)
	migrationHandler.RegisterRoutes(mux)
```

- [ ] **Step 2: Create the CompositeRunner**

Create `internal/mod/migration/runner.go`:

```go
// internal/mod/migration/runner.go
package migration

import "context"

// CompositeRunner delegates to Planner, Executor, and RollbackManager.
type CompositeRunner struct {
	planner  *Planner
	executor *Executor
	rollback *RollbackManager
}

// NewCompositeRunner creates a CompositeRunner.
func NewCompositeRunner(planner *Planner, executor *Executor, rollback *RollbackManager) *CompositeRunner {
	return &CompositeRunner{planner: planner, executor: executor, rollback: rollback}
}

// Plan delegates to the Planner.
func (r *CompositeRunner) Plan(ctx context.Context, req PlanRequest, onProgress StepCallback) (*MigrationPlan, error) {
	return r.planner.Plan(ctx, req, onProgress)
}

// Execute delegates to the Executor.
func (r *CompositeRunner) Execute(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return r.executor.Execute(ctx, migrationID, onProgress)
}

// Rollback delegates to the RollbackManager.
func (r *CompositeRunner) Rollback(ctx context.Context, migrationID int, onProgress StepCallback) error {
	return r.rollback.Rollback(ctx, migrationID, onProgress)
}
```

- [ ] **Step 3: Verify the full project compiles**

```bash
cd /root/meshium
go build ./cmd/server/
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go internal/mod/migration/runner.go
git commit -m "feat: wire migration engine into main server with composite runner"
```

---

### Task 4: Build & Verify

**Files:**
- No new files

- [ ] **Step 1: Run all migration tests**

```bash
cd /root/meshium
go test ./internal/mod/migration/ -v -count=1
```

- [ ] **Step 2: Verify the full project compiles**

```bash
go build ./...
```

- [ ] **Step 3: Run the server and verify endpoints respond**

```bash
# Start server in background
go run ./cmd/server/ &
sleep 2

# Test migration list endpoint
curl -s http://localhost:8080/api/migrations | head -20

# Test health
curl -s http://localhost:8080/api/health

# Kill server
kill %1
```

Expected: `[]` from migrations list, `{"status":"ok"}` from health.

- [ ] **Step 4: Commit any remaining changes**

```bash
git add -A
git commit -m "chore: verify migration handler endpoints respond correctly"
```
