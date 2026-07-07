package migration

import (
	"context"
	"fmt"
	"testing"
)

// TestRecoverInterruptedDetectsRunningMigrations verifies that
// RecoverInterrupted finds migrations stuck in StatusRunning and
// marks them as StatusInterrupted.
func TestRecoverInterruptedDetectsRunningMigrations(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StatusRunning},
			{ID: 2, Status: StatusCompleted},
			{ID: 3, Status: StatusRunning},
		},
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}

	if len(recovered) != 2 {
		t.Fatalf("expected 2 recovered migrations, got %d", len(recovered))
	}

	// Verify migrations 1 and 3 are now interrupted
	mig1, _ := repo.GetMigration(1)
	if mig1.Status != StatusInterrupted {
		t.Fatalf("expected migration 1 status %q, got %q", StatusInterrupted, mig1.Status)
	}

	mig3, _ := repo.GetMigration(3)
	if mig3.Status != StatusInterrupted {
		t.Fatalf("expected migration 3 status %q, got %q", StatusInterrupted, mig3.Status)
	}

	// Migration 2 should still be completed
	mig2, _ := repo.GetMigration(2)
	if mig2.Status != StatusCompleted {
		t.Fatalf("expected migration 2 status %q, got %q", StatusCompleted, mig2.Status)
	}
}

// TestRecoverInterruptedNoRunningMigrations verifies that
// RecoverInterrupted returns empty when no migrations are running.
func TestRecoverInterruptedNoRunningMigrations(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StatusCompleted},
			{ID: 2, Status: StatusFailed},
		},
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}

	if len(recovered) != 0 {
		t.Fatalf("expected 0 recovered migrations, got %d", len(recovered))
	}
}

// TestRecoverInterruptedEmptyList verifies that
// RecoverInterrupted handles an empty migration list.
func TestRecoverInterruptedEmptyList(t *testing.T) {
	repo := &mockRepo{}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}

	if len(recovered) != 0 {
		t.Fatalf("expected 0 recovered migrations, got %d", len(recovered))
	}
}

// stateAwareMockRepo extends mockRepo with typed-state support so it satisfies
// the stateResetter interface, mirroring the production *sqliteRepo. It records
// the last state written per migration and, like SetMigrationState in job.go,
// also mirrors the state string into the legacy status column.
type stateAwareMockRepo struct {
	mockRepo
	states map[int]MigrationState
}

func (m *stateAwareMockRepo) SetMigrationState(migrationID int, state MigrationState) error {
	if m.states == nil {
		m.states = make(map[int]MigrationState)
	}
	m.states[migrationID] = state
	// Mirror job.go's SetMigrationState: the state string is written into the
	// legacy status column too.
	return m.mockRepo.UpdateMigrationStatus(migrationID, state.StateString(), "")
}

// TestRecoverInterruptedDetectsStateMachineRunning reproduces a Jalur A / Jalur B
// crash mid-state-machine: SetMigrationState persists the state string (e.g.
// "initial_sync") into the status column, so status is never the literal
// "running". RecoverInterrupted must still detect these via IsRunning() and mark
// them interrupted, while leaving terminal/paused states untouched.
func TestRecoverInterruptedDetectsStateMachineRunning(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StateInitialSync.StateString()},    // crashed mid-pipeline
			{ID: 2, Status: StateLiveReplication.StateString()}, // crashed mid-replication
			{ID: 3, Status: StatusRunning},                      // legacy literal running
			{ID: 4, Status: StatusCompleted},                    // terminal, must be skipped
			{ID: 5, Status: StatePaused.StateString()},          // paused, must be skipped
			{ID: 6, Status: StatusInterrupted},                  // already interrupted, skipped
		},
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}

	if len(recovered) != 3 {
		t.Fatalf("expected 3 recovered migrations, got %d: %v", len(recovered), recovered)
	}

	for _, id := range []int{1, 2, 3} {
		m, _ := repo.GetMigration(id)
		if m.Status != StatusInterrupted {
			t.Fatalf("expected migration %d status %q, got %q", id, StatusInterrupted, m.Status)
		}
	}

	// Paused, completed, and already-interrupted migrations must be untouched.
	m4, _ := repo.GetMigration(4)
	if m4.Status != StatusCompleted {
		t.Fatalf("expected migration 4 to stay %q, got %q", StatusCompleted, m4.Status)
	}
	m5, _ := repo.GetMigration(5)
	if m5.Status != StatePaused.StateString() {
		t.Fatalf("expected migration 5 to stay %q, got %q", StatePaused.StateString(), m5.Status)
	}
}

// TestRecoverInterruptedResetsStateColumn verifies that when the repo supports
// typed state, recovery resets the state column to Interrupted, not just the
// legacy status. This keeps the migration resumable: Resume reads
// GetMigrationState (which prefers the state column) and requires CanResume().
func TestRecoverInterruptedResetsStateColumn(t *testing.T) {
	repo := &stateAwareMockRepo{
		mockRepo: mockRepo{
			migrations: []Migration{
				{ID: 1, Status: StateInitialSync.StateString()},
			},
		},
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("expected 1 recovered migration, got %d", len(recovered))
	}

	if got := repo.states[1]; got != StateInterrupted {
		t.Fatalf("expected state column reset to %v, got %v", StateInterrupted, got)
	}
	if !repo.states[1].CanResume() {
		t.Fatalf("recovered migration must be resumable, state %v CanResume=false", repo.states[1])
	}
	m, _ := repo.GetMigration(1)
	if m.Status != StatusInterrupted {
		t.Fatalf("expected status %q, got %q", StatusInterrupted, m.Status)
	}
}

// TestRecoverInterruptedIsIdempotent verifies that running recovery twice
// produces no further changes on the second pass — after the first pass every
// migration is interrupted, which IsRunning() excludes.
func TestRecoverInterruptedIsIdempotent(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StateInitialSync.StateString()},
			{ID: 2, Status: StatusRunning},
		},
	}
	executor := &Executor{repo: repo}

	first, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("first RecoverInterrupted failed: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 recovered on first pass, got %d", len(first))
	}

	second, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("second RecoverInterrupted failed: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("expected 0 recovered on second pass, got %d: %v", len(second), second)
	}
}

// TestGetAppliedCategories verifies that the repo correctly returns
// categories that have been applied (StepStatusApplied).
func TestGetAppliedCategories(t *testing.T) {
	repo := &mockRepo{
		steps: []MigrationStepRecord{
			{ID: 1, MigrationID: 1, Category: "docker", Action: "collect", Status: StepStatusApplied},
			{ID: 2, MigrationID: 1, Category: "packages", Action: "collect", Status: StepStatusCompleted},
			{ID: 3, MigrationID: 1, Category: "configs", Action: "collect", Status: StepStatusApplied},
			{ID: 4, MigrationID: 2, Category: "docker", Action: "collect", Status: StepStatusApplied},
		},
	}

	applied, err := repo.GetAppliedCategories(1)
	if err != nil {
		t.Fatalf("GetAppliedCategories failed: %v", err)
	}

	if len(applied) != 2 {
		t.Fatalf("expected 2 applied categories, got %d", len(applied))
	}

	// Should contain docker and configs (not packages which is only "completed")
	found := map[string]bool{}
	for _, cat := range applied {
		found[cat] = true
	}
	if !found["docker"] {
		t.Fatal("expected docker to be in applied categories")
	}
	if !found["configs"] {
		t.Fatal("expected configs to be in applied categories")
	}
	if found["packages"] {
		t.Fatal("packages should not be in applied categories (only completed, not applied)")
	}
}

// TestGetAppliedCategoriesNoApplied verifies that GetAppliedCategories
// returns empty when no categories have been applied yet.
func TestGetAppliedCategoriesNoApplied(t *testing.T) {
	repo := &mockRepo{
		steps: []MigrationStepRecord{
			{ID: 1, MigrationID: 1, Category: "docker", Action: "collect", Status: StepStatusCompleted},
		},
	}

	applied, err := repo.GetAppliedCategories(1)
	if err != nil {
		t.Fatalf("GetAppliedCategories failed: %v", err)
	}

	if len(applied) != 0 {
		t.Fatalf("expected 0 applied categories, got %d", len(applied))
	}
}

// TestResumeRejectsNonInterruptedMigration verifies that Resume
// only works on migrations in StatusInterrupted state.
func TestResumeRejectsNonInterruptedMigration(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StatusCompleted},
		},
	}

	executor := &Executor{repo: repo}

	err := executor.Resume(context.Background(), 1, nil)
	if err == nil {
		t.Fatal("expected error when resuming a completed migration")
	}
}

// TestResumeRejectsPlannedMigration verifies that Resume
// rejects migrations in StatusPlanned (use Execute instead).
func TestResumeRejectsPlannedMigration(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, Status: StatusPlanned},
		},
	}

	executor := &Executor{repo: repo}

	err := executor.Resume(context.Background(), 1, nil)
	if err == nil {
		t.Fatal("expected error when resuming a planned migration")
	}
}

// TestStepStatusAppliedConstant verifies the checkpoint status constant.
func TestStepStatusAppliedConstant(t *testing.T) {
	if StepStatusApplied != "applied" {
		t.Fatalf("expected StepStatusApplied to be 'applied', got %q", StepStatusApplied)
	}
}

// TestStatusInterruptedConstant verifies the interrupted status constant.
func TestStatusInterruptedConstant(t *testing.T) {
	if StatusInterrupted != "interrupted" {
		t.Fatalf("expected StatusInterrupted to be 'interrupted', got %q", StatusInterrupted)
	}
}

// TestStatusResumingConstant verifies the resuming status constant.
func TestStatusResumingConstant(t *testing.T) {
	if StatusResuming != "resuming" {
		t.Fatalf("expected StatusResuming to be 'resuming', got %q", StatusResuming)
	}
}

// TestMockRepoGetAppliedCategoriesWithMultipleMigrations verifies
// that GetAppliedCategories correctly filters by migration ID.
func TestMockRepoGetAppliedCategoriesWithMultipleMigrations(t *testing.T) {
	repo := &mockRepo{
		steps: []MigrationStepRecord{
			{ID: 1, MigrationID: 1, Category: "docker", Action: "collect", Status: StepStatusApplied},
			{ID: 2, MigrationID: 1, Category: "packages", Action: "collect", Status: StepStatusApplied},
			{ID: 3, MigrationID: 2, Category: "docker", Action: "collect", Status: StepStatusApplied},
			{ID: 4, MigrationID: 2, Category: "configs", Action: "collect", Status: StepStatusApplied},
			{ID: 5, MigrationID: 2, Category: "users", Action: "collect", Status: StepStatusApplied},
		},
	}

	// Migration 1 should have 2 applied categories
	applied1, _ := repo.GetAppliedCategories(1)
	if len(applied1) != 2 {
		t.Fatalf("expected 2 applied categories for migration 1, got %d", len(applied1))
	}

	// Migration 2 should have 3 applied categories
	applied2, _ := repo.GetAppliedCategories(2)
	if len(applied2) != 3 {
		t.Fatalf("expected 3 applied categories for migration 2, got %d", len(applied2))
	}

	// Migration 3 should have 0 applied categories
	applied3, _ := repo.GetAppliedCategories(3)
	if len(applied3) != 0 {
		t.Fatalf("expected 0 applied categories for migration 3, got %d", len(applied3))
	}
}

// TestCompositeRunnerHasResumeAndRecover verifies that the CompositeRunner
// exposes the new Resume and RecoverInterrupted methods.
func TestCompositeRunnerHasResumeAndRecover(t *testing.T) {
	// This is a compile-time check that the methods exist on CompositeRunner
	var runner interface {
		Resume(ctx context.Context, migrationID int, onProgress StepCallback) error
		RecoverInterrupted() ([]int, error)
	} = &CompositeRunner{}

	// If we get here, the methods exist
	_ = runner
}

// TestIsMigrationNotFound verifies that IsMigrationNotFound correctly
// identifies the typed error.
func TestIsMigrationNotFound(t *testing.T) {
	// Direct error
	if !IsMigrationNotFound(ErrMigrationNotFound) {
		t.Fatal("expected IsMigrationNotFound to return true for ErrMigrationNotFound")
	}

	// Wrapped error
	wrapped := fmt.Errorf("wrapper: %w", ErrMigrationNotFound)
	if !IsMigrationNotFound(wrapped) {
		t.Fatal("expected IsMigrationNotFound to return true for wrapped ErrMigrationNotFound")
	}

	// Unrelated error
	unrelated := fmt.Errorf("some other error")
	if IsMigrationNotFound(unrelated) {
		t.Fatal("expected IsMigrationNotFound to return false for unrelated error")
	}

	// Nil error
	if IsMigrationNotFound(nil) {
		t.Fatal("expected IsMigrationNotFound to return false for nil")
	}
}
