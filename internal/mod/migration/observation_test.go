package migration

import (
	"context"
	"testing"
	"time"
)

type observationTestRepo struct {
	mockPipelineRepo
	rollbackCount int
}

func (r *observationTestRepo) CreateRollbackRecord(ctx context.Context, record RollbackRecord) (int64, error) {
	r.rollbackCount++
	return r.mockPipelineRepo.CreateRollbackRecord(ctx, record)
}

func TestObservation_AutoRollbackTriggersOnThresholdBreach(t *testing.T) {
	repo := &observationTestRepo{}
	health := NewHealthEngine(nil, nil, repo)
	cutover := &CutoverEngine{repo: repo}
	engine := NewObservationEngine(health, repo, cutover)

	cfg := ObservationConfig{
		MigrationID:         42,
		Duration:            100 * time.Millisecond,
		CheckInterval:       5 * time.Millisecond,
		MinHealthScore:      1,
		AutoRollback:        true,
		ConsecutiveFailures: 1,
	}

	if err := engine.Observe(context.Background(), cfg); err == nil {
		t.Fatal("Observe() error = nil, want auto rollback failure")
	}
	if repo.rollbackCount != 1 {
		t.Fatalf("rollbackCount = %d, want 1", repo.rollbackCount)
	}
}

func TestObservation_ReturnsErrorWhenThresholdBreachedAndNoAutoRollback(t *testing.T) {
	repo := &observationTestRepo{}
	health := NewHealthEngine(nil, nil, repo)
	engine := NewObservationEngine(health, repo)

	cfg := ObservationConfig{
		MigrationID:         43,
		Duration:            100 * time.Millisecond,
		CheckInterval:       5 * time.Millisecond,
		MinHealthScore:      1,
		AutoRollback:        false,
		ConsecutiveFailures: 1,
	}

	if err := engine.Observe(context.Background(), cfg); err == nil {
		t.Fatal("Observe() error = nil, want threshold breach error")
	}
	if repo.rollbackCount != 0 {
		t.Fatalf("rollbackCount = %d, want 0", repo.rollbackCount)
	}
}
