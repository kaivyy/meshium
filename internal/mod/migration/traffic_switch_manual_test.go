package migration

import (
	"context"
	"strings"
	"testing"
)

// These tests lock in the B1 fix: the live pipeline's trafficSwitchStage must
// NOT claim an automatic traffic switch succeeded, because it never moves any
// traffic (the real TrafficSwitchEngine in traffic.go is not wired into the
// pipeline). They assert the stage records an honest manual-cutover checkpoint
// and tells the operator a manual cutover is required.

// captureProgress collects every WSMessage the stage emits so a test can assert
// on the operator-facing status/value stream.
func captureProgress() (*[]WSMessage, func(WSMessage)) {
	var msgs []WSMessage
	return &msgs, func(m WSMessage) { msgs = append(msgs, m) }
}

// TestTrafficSwitchStageDoesNotClaimAutomaticSuccess proves the stage never
// emits a success status asserting traffic was switched. Reporting success here
// is exactly the false-success bug (B1): the operator would believe the cutover
// happened when no DNS / reverse proxy / load balancer was touched.
func TestTrafficSwitchStageDoesNotClaimAutomaticSuccess(t *testing.T) {
	repo := &statefulTrafficRepo{}
	msgs, onProgress := captureProgress()
	pc := &PipelineContext{
		MigrationID: 7,
		Config:      DefaultMigrationConfig(),
		Repo:        repo,
		OnProgress:  onProgress,
	}
	stage := &trafficSwitchStage{repo: repo}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	for _, m := range *msgs {
		if m.Step != "traffic_switch" {
			continue
		}
		if m.Status == "success" {
			t.Errorf("stage emitted a success status %q — it must not claim an automatic traffic switch succeeded", m.Value)
		}
		lower := strings.ToLower(m.Value)
		if strings.Contains(lower, "traffic switched to target") {
			t.Errorf("stage emitted misleading message %q — traffic was not switched", m.Value)
		}
	}
}

// TestTrafficSwitchStageEmitsManualCutoverWarning proves the operator is told,
// explicitly, that a manual cutover is required and traffic still points at the
// source.
func TestTrafficSwitchStageEmitsManualCutoverWarning(t *testing.T) {
	repo := &statefulTrafficRepo{}
	msgs, onProgress := captureProgress()
	pc := &PipelineContext{
		MigrationID: 7,
		Config:      DefaultMigrationConfig(),
		Repo:        repo,
		OnProgress:  onProgress,
	}
	stage := &trafficSwitchStage{repo: repo}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var warned bool
	for _, m := range *msgs {
		if m.Step == "traffic_switch" && m.Status == "warning" {
			lower := strings.ToLower(m.Value)
			if strings.Contains(lower, "manual cutover required") &&
				strings.Contains(lower, "automatic traffic switch is not enabled") {
				warned = true
			}
		}
	}
	if !warned {
		t.Errorf("stage did not emit an explicit manual-cutover warning; messages = %+v", *msgs)
	}
}

// TestTrafficSwitchStageDoesNotCreateSuccessfulCutoverRecord proves the
// persisted records do not misrepresent a completed cutover: the cutover record
// must have TrafficSwitched=false and must not advance the state to "target",
// and the traffic switch config must not be marked "switched".
func TestTrafficSwitchStageDoesNotCreateSuccessfulCutoverRecord(t *testing.T) {
	repo := &statefulTrafficRepo{}
	msgs, onProgress := captureProgress()
	_ = msgs
	pc := &PipelineContext{
		MigrationID: 7,
		Config:      DefaultMigrationConfig(),
		Repo:        repo,
		OnProgress:  onProgress,
	}
	stage := &trafficSwitchStage{repo: repo}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(repo.cutovers) != 1 {
		t.Fatalf("expected exactly 1 cutover record, got %d", len(repo.cutovers))
	}
	cr := repo.cutovers[0]
	if cr.TrafficSwitched {
		t.Errorf("cutover record has TrafficSwitched=true — no traffic was switched")
	}
	if cr.NewState == "target" {
		t.Errorf("cutover record advanced NewState to %q — traffic still points at the source", cr.NewState)
	}
	if cr.Error == "" {
		t.Errorf("cutover record has no note explaining why traffic was not switched")
	}

	if len(repo.trafficConfigs) != 1 {
		t.Fatalf("expected exactly 1 traffic switch config, got %d", len(repo.trafficConfigs))
	}
	if repo.trafficConfigs[0].SwitchState == "switched" {
		t.Errorf("traffic switch config marked %q — it must not claim traffic was switched", repo.trafficConfigs[0].SwitchState)
	}
	if repo.trafficConfigs[0].SwitchState != trafficSwitchManualState {
		t.Errorf("traffic switch config state = %q, want %q", repo.trafficConfigs[0].SwitchState, trafficSwitchManualState)
	}
}

// TestTrafficSwitchStageStillContinues proves the honest downgrade does not
// break the pipeline: Execute returns nil so the remaining stages
// (observation, finalization) still run. The stage records a checkpoint; it
// does not fail the migration.
func TestTrafficSwitchStageStillContinues(t *testing.T) {
	repo := &statefulTrafficRepo{}
	_, onProgress := captureProgress()
	pc := &PipelineContext{
		MigrationID: 7,
		Config:      DefaultMigrationConfig(),
		Repo:        repo,
		OnProgress:  onProgress,
	}
	stage := &trafficSwitchStage{repo: repo}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute must return nil so the pipeline continues, got: %v", err)
	}
}
