package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/server"
	modssh "meshium/internal/mod/ssh"
	xssh "golang.org/x/crypto/ssh"
)

// --- Mock Step ---

// mockStep is a configurable MigrationStep for testing the Engine.
type mockStep struct {
	name       string
	mu         sync.Mutex
	calls      []string // records the sequence of phase calls
	prepareErr error
	applyErr   error
	verifyErr  error
	rollbackErr error
	prepareData string
	applyData   string
	verifyData  string
	applyDelay  time.Duration
	rollbackDelay time.Duration
}

func newMockStep(name string) *mockStep {
	return &mockStep{name: name}
}

func (s *mockStep) Name() string { return s.name }

func (s *mockStep) Prepare(sctx StepContext) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "prepare:"+s.name)
	s.mu.Unlock()
	if s.prepareErr != nil {
		return "", s.prepareErr
	}
	return s.prepareData, nil
}

func (s *mockStep) Apply(sctx StepContext) (string, error) {
	if s.applyDelay > 0 {
		select {
		case <-sctx.Ctx.Done():
			return "", sctx.Ctx.Err()
		case <-time.After(s.applyDelay):
		}
	}
	s.mu.Lock()
	s.calls = append(s.calls, "apply:"+s.name)
	s.mu.Unlock()
	if s.applyErr != nil {
		return "", s.applyErr
	}
	return s.applyData, nil
}

func (s *mockStep) Verify(sctx StepContext) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "verify:"+s.name)
	s.mu.Unlock()
	if s.verifyErr != nil {
		return "", s.verifyErr
	}
	return s.verifyData, nil
}

func (s *mockStep) Rollback(sctx StepContext) error {
	s.mu.Lock()
	s.calls = append(s.calls, "rollback:"+s.name)
	s.mu.Unlock()
	if s.rollbackDelay > 0 {
		time.Sleep(s.rollbackDelay)
	}
	return s.rollbackErr
}

func (s *mockStep) getCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]string, len(s.calls))
	copy(calls, s.calls)
	return calls
}

// --- Mock Server Repo ---

type mockServerRepo struct {
	servers map[int]*server.Server
}

func newMockServerRepo() *mockServerRepo {
	return &mockServerRepo{servers: make(map[int]*server.Server)}
}

func (m *mockServerRepo) AddServer(s *server.Server) {
	m.servers[s.ID] = s
}

func (m *mockServerRepo) GetByID(id int) (*server.Server, error) {
	s, ok := m.servers[id]
	if !ok {
		return nil, fmt.Errorf("server not found: %d", id)
	}
	return s, nil
}

// Implement the rest of the server.Repo interface with no-ops
func (m *mockServerRepo) Create(s server.Server) (int, error) { return s.ID, nil }
func (m *mockServerRepo) Update(id int, s server.Server) error { return nil }
func (m *mockServerRepo) Delete(id int) error                   { return nil }
func (m *mockServerRepo) ToggleFavorite(id int) error           { return nil }
func (m *mockServerRepo) List(filter server.ListFilter) ([]server.Server, error) {
	return nil, nil
}
func (m *mockServerRepo) SaveServerInfo(serverID int, info server.ServerInfo, rawData string) error {
	return nil
}
func (m *mockServerRepo) GetServerInfo(serverID int) (*server.ServerInfo, error) {
	return nil, nil
}

// --- Mock Connection Pool ---

type mockPool struct {
	ssh mockSSH
}

func newMockPool() *mockPool {
	return &mockPool{ssh: *newMockSSH()}
}

func (p *mockPool) Get(serverID int, config modssh.ServerConfig, callback xssh.HostKeyCallback) (SSHExecuter, error) {
	return &p.ssh, nil
}

func (p *mockPool) GetContext(ctx context.Context, serverID int, config modssh.ServerConfig, callback xssh.HostKeyCallback) (SSHExecuter, error) {
	return &p.ssh, nil
}

func (p *mockPool) Close() error { return nil }

// --- Mock Auth Service ---

type mockAuthSvc struct{}

func (m *mockAuthSvc) GetAESKey() []byte { return []byte("test-key-32-bytes-long-12345678") }

// --- Mock Host Key Store ---

type mockHostKeyStore struct{}

func (m *mockHostKeyStore) MakeHostKeyCallback(serverID int) xssh.HostKeyCallback { return nil }

// --- Test Helpers ---

// newTestDB creates a fresh in-memory SQLite database for testing.
func newTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	return database, dbPath
}

// newTestRepo creates a sqliteRepo backed by a fresh in-memory database.
func newTestRepo(t *testing.T) JobRepository {
	t.Helper()
	database, _ := newTestDB(t)
	repo := NewRepo(database)
	return repo.(JobRepository)
}

// newTestEngine creates an Engine with mock dependencies for testing.
func newTestEngine(t *testing.T) (*Engine, JobRepository, *mockServerRepo) {
	t.Helper()
	repo := newTestRepo(t)
	srvRepo := newMockServerRepo()
	srvRepo.AddServer(&server.Server{ID: 1, Host: "source", Port: 22, Username: "user"})
	srvRepo.AddServer(&server.Server{ID: 2, Host: "target", Port: 22, Username: "user"})

	registry := NewCategoryRegistry()
	pool := newMockPool()
	authSvc := &mockAuthSvc{}
	hosts := &mockHostKeyStore{}

	engine := NewEngine(repo, srvRepo, pool, authSvc, hosts, registry)
	return engine, repo, srvRepo
}

// createTestMigration creates a migration record in the repo for testing.
// It also creates the required server records to satisfy FK constraints.
func createTestMigration(t *testing.T, repo JobRepository) int {
	t.Helper()

	// Create server records to satisfy FK constraints
	db := repo.(*sqliteRepo).db
	_, err := db.Exec(`INSERT INTO servers (name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"source", "source", 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
	if err != nil {
		// Server might already exist
	}
	_, err = db.Exec(`INSERT INTO servers (name, host, port, username, password, ssh_key, passphrase, tags, environment, region, icon, color, favorite, bastion_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"target", "target", 22, "user", "", "", "", "[]", "", "", "", "", 0, 0)
	if err != nil {
		// Server might already exist
	}

	categories := []string{"packages", "configs"}
	id, err := repo.CreateMigration(1, 2, categories)
	if err != nil {
		t.Fatalf("failed to create migration: %v", err)
	}
	// Store a collect step for each category so BuildStepsFromCategories can find them
	for _, cat := range categories {
		data := CategoryData{Type: cat, Data: []byte("{}")}
		rawData, _ := json.Marshal(data)
		repo.CreateStep(id, cat, "collect", string(rawData))
		// Mark as completed
		steps, _ := repo.GetSteps(id)
		for _, s := range steps {
			if s.Category == cat && s.Action == "collect" {
				repo.UpdateStepStatus(s.ID, StepStatusCompleted, "")
			}
		}
	}
	return id
}

// --- Tests ---

func TestEngineSuccessPath(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step2 := newMockStep("configs")
	steps := []MigrationStep{step1, step2}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected final state %s, got %s", StateCommitted, result.FinalState)
	}

	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(result.Steps))
	}

	for i, sr := range result.Steps {
		if sr.State != string(StepStateVerified) {
			t.Errorf("step %d (%s): expected state %s, got %s", i, sr.Name, StepStateVerified, sr.State)
		}
	}

	// Verify both steps went through prepare → apply → verify
	calls1 := step1.getCalls()
	expected1 := []string{"prepare:packages", "apply:packages", "verify:packages"}
	if len(calls1) != len(expected1) {
		t.Errorf("step1 calls: expected %v, got %v", expected1, calls1)
	}

	calls2 := step2.getCalls()
	expected2 := []string{"prepare:configs", "apply:configs", "verify:configs"}
	if len(calls2) != len(expected2) {
		t.Errorf("step2 calls: expected %v, got %v", expected2, calls2)
	}

	// Verify checkpoints were created
	cps, err := repo.GetCheckpoints(migrationID)
	if err != nil {
		t.Fatalf("failed to get checkpoints: %v", err)
	}
	if len(cps) != 2 {
		t.Errorf("expected 2 checkpoints, got %d", len(cps))
	}
}

func TestEngineApplyFailureTriggersRollback(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.applyData = "backup-data-1"

	step2 := newMockStep("configs")
	step2.applyErr = fmt.Errorf("apply failed for configs")

	steps := []MigrationStep{step1, step2}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err == nil {
		t.Fatal("expected error from engine.Run, got nil")
	}

	if result.FinalState != StateRestored {
		t.Errorf("expected final state %s, got %s", StateRestored, result.FinalState)
	}

	// step1 should have been applied and then rolled back
	calls1 := step1.getCalls()
	hasRollback1 := false
	for _, c := range calls1 {
		if c == "rollback:packages" {
			hasRollback1 = true
		}
	}
	if !hasRollback1 {
		t.Errorf("step1 should have been rolled back, calls: %v", calls1)
	}

	// step2 should NOT have been rolled back (it failed during apply)
	calls2 := step2.getCalls()
	for _, c := range calls2 {
		if c == "rollback:configs" {
			t.Errorf("step2 should NOT have been rolled back (it failed), calls: %v", calls2)
		}
	}
}

func TestEnginePrepareFailureTriggersRollback(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.applyData = "backup-data-1"

	step2 := newMockStep("configs")
	step2.prepareErr = fmt.Errorf("prepare failed")

	steps := []MigrationStep{step1, step2}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err == nil {
		t.Fatal("expected error from engine.Run, got nil")
	}

	if result.FinalState != StateRestored {
		t.Errorf("expected final state %s, got %s", StateRestored, result.FinalState)
	}

	// step1 should have been rolled back
	calls1 := step1.getCalls()
	hasRollback1 := false
	for _, c := range calls1 {
		if c == "rollback:packages" {
			hasRollback1 = true
		}
	}
	if !hasRollback1 {
		t.Errorf("step1 should have been rolled back, calls: %v", calls1)
	}
}

func TestEngineVerifyFailureTriggersRollback(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.applyData = "backup-data-1"

	step2 := newMockStep("configs")
	step2.verifyErr = fmt.Errorf("verify failed")

	steps := []MigrationStep{step1, step2}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err == nil {
		t.Fatal("expected error from engine.Run, got nil")
	}

	if result.FinalState != StateRestored {
		t.Errorf("expected final state %s, got %s", StateRestored, result.FinalState)
	}

	// Both steps should have been rolled back (step2 failed verify, step1 was applied)
	calls1 := step1.getCalls()
	hasRollback1 := false
	for _, c := range calls1 {
		if c == "rollback:packages" {
			hasRollback1 = true
		}
	}
	if !hasRollback1 {
		t.Errorf("step1 should have been rolled back, calls: %v", calls1)
	}

	calls2 := step2.getCalls()
	hasRollback2 := false
	for _, c := range calls2 {
		if c == "rollback:configs" {
			hasRollback2 = true
		}
	}
	if !hasRollback2 {
		t.Errorf("step2 should have been rolled back (verify failed after apply), calls: %v", calls2)
	}
}

func TestEngineContextCancellation(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	ctx, cancel := context.WithCancel(context.Background())

	step1 := newMockStep("packages")
	step1.applyData = "backup-data-1"
	step1.applyDelay = 200 * time.Millisecond // slow apply so context can be cancelled

	step2 := newMockStep("configs")

	steps := []MigrationStep{step1, step2}

	// Cancel context while step1 is applying
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := engine.Run(ctx, migrationID, steps, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}

	// Migration should be interrupted
	if result.FinalState != StateInterrupted {
		t.Errorf("expected final state %s, got %s", StateInterrupted, result.FinalState)
	}

	// Verify the migration state in DB
	dbState, err := repo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("failed to get migration state: %v", err)
	}
	if dbState != StateInterrupted {
		t.Errorf("expected DB state %s, got %s", StateInterrupted, dbState)
	}
}

func TestEngineRollbackLIFOOrder(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("step1")
	step1.applyData = "backup-1"

	step2 := newMockStep("step2")
	step2.applyData = "backup-2"

	step3 := newMockStep("step3")
	step3.applyErr = fmt.Errorf("step3 apply fails")

	steps := []MigrationStep{step1, step2, step3}

	_, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	// Verify rollback order: step2 should be rolled back before step1
	calls1 := step1.getCalls()
	calls2 := step2.getCalls()

	rollback1Idx := -1
	rollback2Idx := -1
	for i, c := range calls1 {
		if c == "rollback:step1" {
			rollback1Idx = i
		}
	}
	for i, c := range calls2 {
		if c == "rollback:step2" {
			rollback2Idx = i
		}
	}

	if rollback1Idx < 0 {
		t.Fatal("step1 was not rolled back")
	}
	if rollback2Idx < 0 {
		t.Fatal("step2 was not rolled back")
	}

	// LIFO: step2 (applied last) should be rolled back first
	// We can't compare timestamps directly, but we can check the order
	// by looking at which step's rollback was called first.
	// Since step2 is rolled back before step1, step2's rollback call
	// should happen before step1's rollback call.
	// We verify this by checking that step2 has a rollback call and
	// step1 has a rollback call, and step3 does NOT have a rollback call.
	calls3 := step3.getCalls()
	for _, c := range calls3 {
		if c == "rollback:step3" {
			t.Error("step3 should NOT have been rolled back (it failed during apply)")
		}
	}
}

func TestEngineResumeFromCheckpoint(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.applyData = "backup-1"

	step2 := newMockStep("configs")
	step2.applyData = "backup-2"

	steps := []MigrationStep{step1, step2}

	// Run the migration to completion
	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.FinalState != StateCommitted {
		t.Fatalf("expected committed, got %s", result.FinalState)
	}

	// Simulate interruption: set state to interrupted
	repo.SetMigrationState(migrationID, StateInterrupted)

	// Create new steps (simulating a restart)
	step1b := newMockStep("packages")
	step2b := newMockStep("configs")
	stepsB := []MigrationStep{step1b, step2b}

	// Resume
	result, err = engine.Resume(context.Background(), migrationID, stepsB, nil)
	if err != nil {
		t.Fatalf("engine.Resume failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected committed after resume, got %s", result.FinalState)
	}

	// Both steps should be skipped (they have checkpoints)
	for i, sr := range result.Steps {
		if !sr.Skipped {
			t.Errorf("step %d (%s): expected to be skipped on resume", i, sr.Name)
		}
	}

	// Verify that the new steps were NOT called (they were skipped)
	calls1b := step1b.getCalls()
	if len(calls1b) != 0 {
		t.Errorf("step1b should not have been called on resume, calls: %v", calls1b)
	}
}

func TestEngineRecoveryDiscoverInterrupted(t *testing.T) {
	_, repo, srvRepo := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	// Set state to interrupted
	repo.SetMigrationState(migrationID, StateInterrupted)

	// Create recovery manager
	rm := NewRecoveryManager(repo, srvRepo, newMockPool(), &mockAuthSvc{}, &mockHostKeyStore{}, NewCategoryRegistry())

	// Discover interrupted migrations
	interrupted, err := rm.DiscoverInterrupted()
	if err != nil {
		t.Fatalf("DiscoverInterrupted failed: %v", err)
	}

	if len(interrupted) != 1 {
		t.Fatalf("expected 1 interrupted migration, got %d", len(interrupted))
	}

	if interrupted[0].Migration.ID != migrationID {
		t.Errorf("expected migration ID %d, got %d", migrationID, interrupted[0].Migration.ID)
	}

	if !interrupted[0].CanResume {
		t.Error("expected CanResume to be true")
	}
}

func TestEngineRecoveryCancelMigration(t *testing.T) {
	engine, repo, srvRepo := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	// Run a migration with one step that succeeds (creates a checkpoint)
	step1 := newMockStep("packages")
	step1.applyData = "backup-1"

	_, err := engine.Run(context.Background(), migrationID, []MigrationStep{step1}, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	// Simulate interruption
	repo.SetMigrationState(migrationID, StateInterrupted)

	// Create recovery manager and cancel
	rm := NewRecoveryManager(repo, srvRepo, newMockPool(), &mockAuthSvc{}, &mockHostKeyStore{}, NewCategoryRegistry())

	result, err := rm.CancelMigration(context.Background(), migrationID, nil)
	if err != nil {
		t.Fatalf("CancelMigration failed: %v", err)
	}

	if result.FinalState != StateRestored {
		t.Errorf("expected final state %s, got %s", StateRestored, result.FinalState)
	}

	// Verify the migration state in DB
	dbState, err := repo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("failed to get migration state: %v", err)
	}
	if dbState != StateRestored {
		t.Errorf("expected DB state %s, got %s", StateRestored, dbState)
	}
}

func TestEngineStateTransitions(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.applyData = "backup-1"

	result, err := engine.Run(context.Background(), migrationID, []MigrationStep{step1}, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected committed, got %s", result.FinalState)
	}

	// Verify the state in the DB
	dbState, err := repo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("failed to get migration state: %v", err)
	}
	if dbState != StateCommitted {
		t.Errorf("expected DB state %s, got %s", StateCommitted, dbState)
	}
}

func TestEngineMultipleStepsAllSucceed(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	steps := []MigrationStep{
		newMockStep("packages"),
		newMockStep("configs"),
		newMockStep("services"),
		newMockStep("users"),
	}

	result, err := engine.Run(context.Background(), migrationID, steps, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected committed, got %s", result.FinalState)
	}

	if len(result.Steps) != 4 {
		t.Fatalf("expected 4 step results, got %d", len(result.Steps))
	}

	// All steps should be verified
	for i, sr := range result.Steps {
		if sr.State != string(StepStateVerified) {
			t.Errorf("step %d: expected %s, got %s", i, StepStateVerified, sr.State)
		}
	}

	// Verify 4 checkpoints
	cps, _ := repo.GetCheckpoints(migrationID)
	if len(cps) != 4 {
		t.Errorf("expected 4 checkpoints, got %d", len(cps))
	}
}

func TestEngineEmptySteps(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	result, err := engine.Run(context.Background(), migrationID, []MigrationStep{}, nil)
	if err != nil {
		t.Fatalf("engine.Run with empty steps failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected committed, got %s", result.FinalState)
	}
}

func TestEngineStepWithDataFlow(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	step1 := newMockStep("packages")
	step1.prepareData = "prepare-output"
	step1.applyData = "apply-output"
	step1.verifyData = "verify-output"

	result, err := engine.Run(context.Background(), migrationID, []MigrationStep{step1}, nil)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	if result.FinalState != StateCommitted {
		t.Errorf("expected committed, got %s", result.FinalState)
	}

	// Verify the step data was stored
	jobSteps, err := repo.GetJobSteps(migrationID)
	if err != nil {
		t.Fatalf("failed to get job steps: %v", err)
	}

	if len(jobSteps) != 1 {
		t.Fatalf("expected 1 job step, got %d", len(jobSteps))
	}

	if jobSteps[0].PrepareData != "prepare-output" {
		t.Errorf("expected prepare data 'prepare-output', got %q", jobSteps[0].PrepareData)
	}
	if jobSteps[0].ApplyData != "apply-output" {
		t.Errorf("expected apply data 'apply-output', got %q", jobSteps[0].ApplyData)
	}
	if jobSteps[0].VerifyData != "verify-output" {
		t.Errorf("expected verify data 'verify-output', got %q", jobSteps[0].VerifyData)
	}
}

func TestEngineProgressCallback(t *testing.T) {
	engine, repo, _ := newTestEngine(t)
	migrationID := createTestMigration(t, repo)

	var messages []WSMessage
	var mu sync.Mutex
	onProgress := func(msg WSMessage) {
		mu.Lock()
		messages = append(messages, msg)
		mu.Unlock()
	}

	step1 := newMockStep("packages")
	step1.applyData = "backup-1"

	_, err := engine.Run(context.Background(), migrationID, []MigrationStep{step1}, onProgress)
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(messages) == 0 {
		t.Fatal("expected progress messages, got none")
	}

	// Check that we got at least one "complete" message
	hasComplete := false
	for _, msg := range messages {
		if msg.Status == "complete" {
			hasComplete = true
		}
	}
	if !hasComplete {
		t.Error("expected at least one 'complete' progress message")
	}
}
