package migration

import (
	"context"
	"testing"
)

// statefulTrafficRepo is a mockPipelineRepo that actually stores the traffic
// switch config and cutover records it is asked to create, and serves them back
// from the Get* methods the idempotency guard consults. This lets a test
// simulate a resume: pre-seed the rows a prior (crashed) run inserted, then run
// the stage again and assert no duplicate is created.
type statefulTrafficRepo struct {
	mockPipelineRepo
	trafficConfigs []TrafficSwitchConfig
	cutovers       []CutoverRecord
	trafficCreates int
	cutoverCreates int
}

func (r *statefulTrafficRepo) CreateTrafficSwitchConfig(ctx context.Context, cfg TrafficSwitchConfig) (int64, error) {
	r.trafficCreates++
	cfg.ID = int64(len(r.trafficConfigs) + 1)
	r.trafficConfigs = append(r.trafficConfigs, cfg)
	return cfg.ID, nil
}

func (r *statefulTrafficRepo) GetTrafficSwitchConfig(migrationID int) (*TrafficSwitchConfig, error) {
	for i := len(r.trafficConfigs) - 1; i >= 0; i-- {
		if r.trafficConfigs[i].MigrationID == migrationID {
			cfg := r.trafficConfigs[i]
			return &cfg, nil
		}
	}
	return nil, nil
}

func (r *statefulTrafficRepo) CreateCutoverRecord(ctx context.Context, cr CutoverRecord) (int64, error) {
	r.cutoverCreates++
	cr.ID = int64(len(r.cutovers) + 1)
	r.cutovers = append(r.cutovers, cr)
	return cr.ID, nil
}

func (r *statefulTrafficRepo) GetCutoverHistory(migrationID int) ([]CutoverRecord, error) {
	out := make([]CutoverRecord, 0, len(r.cutovers))
	for _, cr := range r.cutovers {
		if cr.MigrationID == migrationID {
			out = append(out, cr)
		}
	}
	return out, nil
}

func newTrafficSwitchTestContext(repo PipelineRepo) *PipelineContext {
	return &PipelineContext{
		MigrationID: 7,
		Config:      DefaultMigrationConfig(),
		OnProgress:  func(WSMessage) {},
	}
}

// TestTrafficSwitchStageFirstRunCreatesRecords is the baseline: on a clean run
// the stage inserts exactly one traffic switch config and one cutover record.
func TestTrafficSwitchStageFirstRunCreatesRecords(t *testing.T) {
	repo := &statefulTrafficRepo{}
	pc := newTrafficSwitchTestContext(repo)
	pc.Repo = repo
	stage := &trafficSwitchStage{repo: repo}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed on first run: %v", err)
	}
	if repo.trafficCreates != 1 {
		t.Errorf("traffic switch config creates = %d, want 1", repo.trafficCreates)
	}
	if repo.cutoverCreates != 1 {
		t.Errorf("cutover record creates = %d, want 1", repo.cutoverCreates)
	}
}

// TestTrafficSwitchStageResumeCreatesNoDuplicates simulates a resume after the
// process crashed *after* the traffic switch config and cutover record were
// persisted but *before* the stage completion checkpoint landed. The pipeline
// loop re-runs the stage; the idempotency guard must see the existing rows and
// create no duplicates.
func TestTrafficSwitchStageResumeCreatesNoDuplicates(t *testing.T) {
	repo := &statefulTrafficRepo{}
	pc := newTrafficSwitchTestContext(repo)
	pc.Repo = repo
	stage := &trafficSwitchStage{repo: repo}

	// First run: side effects land (this is the pre-crash state).
	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed on first run: %v", err)
	}

	// Resume: the stage runs again because its checkpoint was never written.
	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed on resume: %v", err)
	}

	if repo.trafficCreates != 1 {
		t.Errorf("traffic switch config creates after resume = %d, want 1 (no duplicate)", repo.trafficCreates)
	}
	if repo.cutoverCreates != 1 {
		t.Errorf("cutover record creates after resume = %d, want 1 (no duplicate)", repo.cutoverCreates)
	}
	if got := len(repo.trafficConfigs); got != 1 {
		t.Errorf("stored traffic switch configs = %d, want 1", got)
	}
	if got := len(repo.cutovers); got != 1 {
		t.Errorf("stored cutover records = %d, want 1", got)
	}
}

// TestTrafficSwitchStageResumeAfterPartialCreate simulates the narrower crash
// window: the traffic switch config was persisted but the cutover record was
// not (crash between the two inserts). Resume must create only the missing
// cutover record, not a second config.
func TestTrafficSwitchStageResumeAfterPartialCreate(t *testing.T) {
	repo := &statefulTrafficRepo{}
	pc := newTrafficSwitchTestContext(repo)
	pc.Repo = repo
	stage := &trafficSwitchStage{repo: repo}

	// Pre-seed only the traffic switch config, as a crash between the two
	// inserts would leave behind. Seed directly (not via the counted Create) so
	// the create counters reflect only what the resume run does.
	repo.trafficConfigs = append(repo.trafficConfigs, TrafficSwitchConfig{
		ID:          1,
		MigrationID: pc.MigrationID,
		SwitchState: "switched",
	})

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed on resume: %v", err)
	}

	if repo.trafficCreates != 0 {
		t.Errorf("traffic switch config creates = %d, want 0 (already existed)", repo.trafficCreates)
	}
	if repo.cutoverCreates != 1 {
		t.Errorf("cutover record creates = %d, want 1 (the missing one)", repo.cutoverCreates)
	}
	if got := len(repo.trafficConfigs); got != 1 {
		t.Errorf("stored traffic switch configs = %d, want 1", got)
	}
}
