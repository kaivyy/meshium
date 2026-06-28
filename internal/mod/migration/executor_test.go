package migration

import (
	"context"
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

// TestGetAppliedCategories verifies that the repo correctly returns
// categories that have been applied (StepStatusApplied).
func TestGetAppliedCategories(t *testing.T) {
	repo := &mockRepo{
		steps: []MigrationStep{
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
		steps: []MigrationStep{
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
		steps: []MigrationStep{
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
	}

	runner = &CompositeRunner{}

	// If we get here, the methods exist
	_ = runner
}
