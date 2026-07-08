package migration

import (
	"strings"
	"testing"
)

// RecoverInterrupted must also catch a migration whose collection was
// interrupted before any category data was saved: it sits in StatusPlanned
// (the default written by CreateMigration) with zero completed `collect`
// steps, looking like a finished plan. It should be flipped to
// StatusInterrupted with a clear, actionable error so it is not presented as
// a usable plan. This mirrors the crash-recovery path for StatusRunning.
func TestRecoverInterruptedMarksPlannedWithNoCollectSteps(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, SourceID: 1, TargetID: 2, Status: StatusPlanned, Categories: []string{"packages"}},
		},
		// No steps at all — collection never persisted anything.
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}
	if len(recovered) != 1 || recovered[0] != 1 {
		t.Fatalf("expected to recover migration [1], got %v", recovered)
	}

	got, err := repo.GetMigration(1)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}
	if got.Status != StatusInterrupted {
		t.Fatalf("expected status %q, got %q", StatusInterrupted, got.Status)
	}
	if !strings.Contains(strings.ToLower(got.Error), "interrupted") {
		t.Fatalf("expected error to explain the interrupted collection, got %q", got.Error)
	}
}

// A planned migration that DID finish collecting (has a completed `collect`
// step) must be left alone — the detection keys on missing collected data,
// not on the StatusPlanned status alone. Otherwise every healthy plan would
// be wrongly marked interrupted at startup.
func TestRecoverInterruptedLeavesCompletePlannedAlone(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, SourceID: 1, TargetID: 2, Status: StatusPlanned, Categories: []string{"packages"}},
		},
		steps: []MigrationStepRecord{
			{ID: 1, MigrationID: 1, Category: "packages", Action: "collect", Status: StepStatusCompleted, Data: `{}`},
		},
	}

	executor := &Executor{repo: repo}

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("expected no recoveries for a complete plan, got %v", recovered)
	}

	got, err := repo.GetMigration(1)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}
	if got.Status != StatusPlanned {
		t.Fatalf("complete plan should stay %q, got %q", StatusPlanned, got.Status)
	}
	if got.Error != "" {
		t.Fatalf("complete plan should have no error, got %q", got.Error)
	}
}

// Recovery is idempotent: once a migration is StatusInterrupted it is neither
// running nor planned-with-no-steps, so a second startup pass must not touch
// it (and must not append it to the recovered list again).
func TestRecoverInterruptedIdempotent(t *testing.T) {
	repo := &mockRepo{
		migrations: []Migration{
			{ID: 1, SourceID: 1, TargetID: 2, Status: StatusPlanned, Categories: []string{"packages"}},
		},
	}

	executor := &Executor{repo: repo}

	if _, err := executor.RecoverInterrupted(); err != nil {
		t.Fatalf("first RecoverInterrupted failed: %v", err)
	}
	got, _ := repo.GetMigration(1)
	if got.Status != StatusInterrupted {
		t.Fatalf("expected interrupted after first pass, got %q", got.Status)
	}
	firstError := got.Error

	recovered, err := executor.RecoverInterrupted()
	if err != nil {
		t.Fatalf("second RecoverInterrupted failed: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("expected no recoveries on second pass, got %v", recovered)
	}
	got, _ = repo.GetMigration(1)
	if got.Error != firstError {
		t.Fatalf("second pass must not rewrite the error; was %q, now %q", firstError, got.Error)
	}
}
