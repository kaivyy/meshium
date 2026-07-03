package migration

import (
	"context"
	"testing"
	"time"

	"meshium/internal/mod/server"
)

// --- State Machine Tests ---

func TestStateMachineTransitions(t *testing.T) {
	sm := NewStateMachine(StateCreated)

	// Valid forward transitions
	steps := []MigrationState{
		StatePlanning, StateDiscovery, StateCompatibilityCheck,
		StateRiskAssessment, StateBackup, StateProvisionTarget,
		StateInstallDependencies, StateInitialSync, StateLiveReplication,
		StateVerification, StatePreCutover, StateTrafficSwitch,
		StatePostVerification, StateObservation, StateCommitted,
	}
	for _, target := range steps {
		if err := sm.Transition(target); err != nil {
			t.Fatalf("transition to %s failed: %v", target, err)
		}
	}
	if !sm.IsTerminal() {
		t.Error("expected terminal state after committed")
	}
}

func TestStateMachineInvalidTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	err := sm.Transition(StateCommitted)
	if err == nil {
		t.Error("expected error for invalid transition Created → Committed")
	}
}

func TestStateMachineFailureTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.Transition(StatePlanning)

	// Any running state can transition to Failed
	if err := sm.Transition(StateFailed); err != nil {
		t.Fatalf("transition to Failed: %v", err)
	}

	// Failed can transition to Rollback
	if err := sm.Transition(StateRollback); err != nil {
		t.Fatalf("transition to Rollback: %v", err)
	}

	// Rollback can transition to RolledBack
	if err := sm.Transition(StateRolledBack); err != nil {
		t.Fatalf("transition to RolledBack: %v", err)
	}

	if !sm.IsTerminal() {
		t.Error("expected terminal state after rolled back")
	}
}

func TestStateMachineInterruptResume(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.Transition(StatePlanning)
	sm.Transition(StateDiscovery)

	// Interrupt from running state
	if err := sm.Transition(StateInterrupted); err != nil {
		t.Fatalf("transition to Interrupted: %v", err)
	}
	if !sm.CanResume() {
		t.Error("expected CanResume after interrupted")
	}

	// Resume
	if err := sm.Transition(StateResuming); err != nil {
		t.Fatalf("transition to Resuming: %v", err)
	}

	// Resume can go to any stage
	if err := sm.Transition(StateDiscovery); err != nil {
		t.Fatalf("transition from Resuming to Discovery: %v", err)
	}
}

func TestStateMachineCancel(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.Transition(StatePlanning)
	sm.Transition(StateInterrupted)

	if err := sm.Transition(StateCancelled); err != nil {
		t.Fatalf("transition to Cancelled: %v", err)
	}
	if !sm.IsTerminal() {
		t.Error("expected terminal state after cancelled")
	}
}

func TestStateMachineTrafficSwitchRollback(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	// Fast forward to TrafficSwitch
	for _, s := range []MigrationState{
		StatePlanning, StateDiscovery, StateCompatibilityCheck,
		StateRiskAssessment, StateBackup, StateProvisionTarget,
		StateInstallDependencies, StateInitialSync, StateLiveReplication,
		StateVerification, StatePreCutover, StateTrafficSwitch,
	} {
		sm.Transition(s)
	}

	// TrafficSwitch can transition to Rollback (not just Failed)
	if err := sm.Transition(StateRollback); err != nil {
		t.Fatalf("TrafficSwitch → Rollback: %v", err)
	}
}

func TestStateMachineThreadSafety(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	done := make(chan bool, 100)

	for i := 0; i < 50; i++ {
		go func() {
			sm.State()
			sm.IsTerminal()
			sm.IsRunning()
			done <- true
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

// --- State String Tests ---

func TestStateString(t *testing.T) {
	tests := []struct {
		state    MigrationState
		expected string
	}{
		{StateCreated, "created"},
		{StatePlanning, "planning"},
		{StateDiscovery, "discovery"},
		{StateCompatibilityCheck, "compatibility_check"},
		{StateRiskAssessment, "risk_assessment"},
		{StateBackup, "backup"},
		{StateProvisionTarget, "provision_target"},
		{StateInstallDependencies, "install_dependencies"},
		{StateInitialSync, "initial_sync"},
		{StateLiveReplication, "live_replication"},
		{StateVerification, "verification"},
		{StatePreCutover, "pre_cutover"},
		{StateTrafficSwitch, "traffic_switch"},
		{StatePostVerification, "post_verification"},
		{StateObservation, "observation"},
		{StateCommitted, "committed"},
		{StateFailed, "failed"},
		{StateRollback, "rollback"},
		{StateRolledBack, "rolled_back"},
		{StateInterrupted, "interrupted"},
		{StateResuming, "resuming"},
		{StateCancelled, "cancelled"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.expected)
		}
	}
}

func TestStateFromString(t *testing.T) {
	for state, str := range stateString {
		parsed, err := StateFromString(str)
		if err != nil {
			t.Errorf("StateFromString(%q) error: %v", str, err)
		}
		if parsed != state {
			t.Errorf("StateFromString(%q) = %d, want %d", str, parsed, state)
		}
	}

	// Unknown string
	_, err := StateFromString("nonexistent")
	if err == nil {
		t.Error("expected error for unknown state string")
	}
}

func TestPipelineStateIsRunning(t *testing.T) {
	runningStates := []MigrationState{
		StatePlanning, StateDiscovery, StateCompatibilityCheck,
		StateRiskAssessment, StateBackup, StateProvisionTarget,
		StateInstallDependencies, StateInitialSync, StateLiveReplication,
		StateVerification, StatePreCutover, StateTrafficSwitch,
		StatePostVerification, StateObservation, StateRollback, StateResuming,
	}
	for _, s := range runningStates {
		if !s.IsRunning() {
			t.Errorf("expected %s to be running", s)
		}
	}

	nonRunningStates := []MigrationState{
		StateCommitted, StateFailed, StateRolledBack, StateInterrupted, StateCancelled,
	}
	for _, s := range nonRunningStates {
		if s.IsRunning() {
			t.Errorf("expected %s to not be running", s)
		}
	}
}

// --- Pipeline Stage Name Tests ---

func TestAllStages(t *testing.T) {
	stages := AllStages()
	if len(stages) != 13 {
		t.Errorf("expected 13 pipeline stages, got %d", len(stages))
	}

	// Verify ordering
	expected := []PipelineStageName{
		StageDiscovery, StageAnalysis, StagePlanning, StageValidation,
		StagePreparation, StageInitialSync, StageLiveReplication,
		StageHealthVerification, StagePreCutoverValidation,
		StageTrafficSwitch, StagePostCutoverObservation,
		StageFinalization, StageArchive,
	}
	for i, name := range expected {
		if stages[i] != name {
			t.Errorf("stage[%d] = %q, want %q", i, stages[i], name)
		}
	}
}

// --- Risk Engine Tests ---

func TestRiskAssessment(t *testing.T) {
	repo := &mockPipelineRepo{}
	engine := NewRiskEngine(repo)

	tests := []struct {
		name     string
		input    RiskInput
		maxScore float64
		minScore float64
		class    RiskClass
	}{
		{
			name: "low_risk_small_migration",
			input: RiskInput{
				DataSizeBytes:        100 * 1024 * 1024, // 100MB
				DatabaseSizeBytes:    50 * 1024 * 1024,  // 50MB
				ContainerCount:       2,
				VolumeCount:          1,
				ReplicationAvailable: true,
				NetworkSpeedMbps:     1000,
				SourceCPU:            4,
				TargetCPU:            8,
				SourceRAMMB:          4096,
				TargetRAMMB:          8192,
			},
			maxScore: 30,
			minScore: 0,
			class:    RiskClassLow,
		},
		{
			name: "high_risk_large_migration_no_replication",
			input: RiskInput{
				DataSizeBytes:        500 * 1024 * 1024 * 1024, // 500GB
				DatabaseSizeBytes:    200 * 1024 * 1024 * 1024, // 200GB
				ContainerCount:       25,
				VolumeCount:          15,
				QueueCount:           5,
				ReplicationAvailable: false,
				NetworkSpeedMbps:     50,
				SourceCPU:            16,
				TargetCPU:            8,
				SourceRAMMB:          32768,
				TargetRAMMB:          16384,
				CriticalIssues:       1,
			},
			maxScore: 100,
			minScore: 60,
			class:    RiskClassCritical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := engine.AssessRisk(context.Background(), 1, tt.input)
			if err != nil {
				t.Fatalf("AssessRisk error: %v", err)
			}
			if report.RiskScore < tt.minScore || report.RiskScore > tt.maxScore {
				t.Errorf("risk score = %.1f, expected between %.1f and %.1f", report.RiskScore, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestRiskClassification(t *testing.T) {
	engine := &RiskEngine{}

	tests := []struct {
		score float64
		class RiskClass
	}{
		{0, RiskClassLow},
		{29, RiskClassLow},
		{30, RiskClassMedium},
		{59, RiskClassMedium},
		{60, RiskClassHigh},
		{84, RiskClassHigh},
		{85, RiskClassCritical},
		{100, RiskClassCritical},
	}

	for _, tt := range tests {
		got := engine.classifyRisk(tt.score)
		if got != tt.class {
			t.Errorf("classifyRisk(%.0f) = %q, want %q", tt.score, got, tt.class)
		}
	}
}

func TestEstimateDowntime(t *testing.T) {
	engine := &RiskEngine{}

	// With replication and good network → near-zero downtime
	input := RiskInput{
		DataSizeBytes:        10 * 1024 * 1024 * 1024,
		DatabaseSizeBytes:    1 * 1024 * 1024 * 1024,
		ReplicationAvailable: true,
		NetworkSpeedMbps:     1000,
		ContainerCount:       5,
	}
	estimate := engine.EstimateDowntime(input)
	if !containsInStr(estimate, "zero-downtime") && !containsInStr(estimate, "seconds") {
		t.Errorf("expected near-zero downtime with replication, got: %s", estimate)
	}

	// Without replication → longer downtime
	input.ReplicationAvailable = false
	input.NetworkSpeedMbps = 100
	estimate = engine.EstimateDowntime(input)
	if containsInStr(estimate, "zero-downtime") {
		t.Error("expected non-zero downtime without replication")
	}
}

// --- Compatibility Engine Tests ---

func TestCompatibilityArchitecture(t *testing.T) {
	engine := &CompatibilityEngine{}

	// Same architecture → pass
	result := engine.checkArchitecture(
		&serverInfo{Arch: "x86_64"},
		&serverInfo{Arch: "x86_64"},
	)
	if !result.Passed {
		t.Error("expected same architecture to pass")
	}

	// Different architecture → fail
	result = engine.checkArchitecture(
		&serverInfo{Arch: "x86_64"},
		&serverInfo{Arch: "aarch64"},
	)
	if result.Passed {
		t.Error("expected different architecture to fail")
	}
	if result.Severity != SeverityHigh {
		t.Errorf("expected high severity for arch mismatch, got %s", result.Severity)
	}
}

func TestCompatibilityRAM(t *testing.T) {
	engine := &CompatibilityEngine{}

	// Target has more RAM → pass
	result := engine.checkRAM(&serverInfo{RAMMB: 4096}, &serverInfo{RAMMB: 8192})
	if !result.Passed {
		t.Error("expected target with more RAM to pass")
	}

	// Target has less RAM → fail
	result = engine.checkRAM(&serverInfo{RAMMB: 8192}, &serverInfo{RAMMB: 4096})
	if result.Passed {
		t.Error("expected target with less RAM to fail")
	}
}

func TestCompatibilityDisk(t *testing.T) {
	engine := &CompatibilityEngine{}

	// Target has less disk → critical
	result := engine.checkDisk(&serverInfo{DiskGB: 500}, &serverInfo{DiskGB: 100})
	if result.Passed {
		t.Error("expected target with less disk to fail")
	}
	if result.Severity != SeverityCritical {
		t.Errorf("expected critical severity for disk, got %s", result.Severity)
	}
}

func TestHasBlockers(t *testing.T) {
	results := []CompatibilityCheckResult{
		{CheckName: "arch", Severity: SeverityWarning, Passed: true, Message: "ok"},
		{CheckName: "disk", Severity: SeverityCritical, Passed: false, Message: "not enough disk"},
	}
	if !HasBlockers(results) {
		t.Error("expected blockers with critical failure")
	}

	results[1].Passed = true
	if HasBlockers(results) {
		t.Error("expected no blockers when all critical checks pass")
	}
}

// --- Health Engine Tests ---

func TestHealthScoreCalculation(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	results := []HealthCheckResult{
		{Status: "healthy", HealthScore: 100, ResponseTimeMs: 10},
		{Status: "healthy", HealthScore: 90, ResponseTimeMs: 100},
		{Status: "unhealthy", HealthScore: 0, ResponseTimeMs: 5000},
	}

	score := engine.CalculateScore(results)
	if score.ChecksTotal != 3 {
		t.Errorf("expected 3 total checks, got %d", score.ChecksTotal)
	}
	if score.ChecksPassed != 2 {
		t.Errorf("expected 2 passed checks, got %d", score.ChecksPassed)
	}
	if score.ChecksFailed != 1 {
		t.Errorf("expected 1 failed check, got %d", score.ChecksFailed)
	}
	if score.Score <= 0 || score.Score > 100 {
		t.Errorf("expected score between 0-100, got %.1f", score.Score)
	}
}

func TestHealthScoreFromLatency(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	tests := []struct {
		latency  time.Duration
		minScore float64
		maxScore float64
	}{
		{10 * time.Millisecond, 95, 100},
		{100 * time.Millisecond, 85, 95},
		{500 * time.Millisecond, 70, 85},
		{2 * time.Second, 50, 70},
		{10 * time.Second, 0, 30},
	}

	for _, tt := range tests {
		score := engine.scoreFromLatency(tt.latency)
		if score < tt.minScore || score > tt.maxScore {
			t.Errorf("scoreFromLatency(%v) = %.1f, expected between %.1f and %.1f",
				tt.latency, score, tt.minScore, tt.maxScore)
		}
	}
}

// --- Migration Config Tests ---

func TestDefaultMigrationConfig(t *testing.T) {
	config := DefaultMigrationConfig()
	if len(config.Categories) == 0 {
		t.Error("expected default categories")
	}
	if !config.ReplicationEnabled {
		t.Error("expected replication enabled by default")
	}
	if !config.FreezeWriteOnCutover {
		t.Error("expected freeze write on cutover by default")
	}
	if !config.AutoRollbackOnError {
		t.Error("expected auto rollback on error by default")
	}
	if config.ObservationDuration < 5*time.Minute {
		t.Error("expected at least 5 minutes observation duration")
	}
}

// --- Rollback Complexity Tests ---

func TestRollbackComplexity(t *testing.T) {
	engine := &RiskEngine{}

	tests := []struct {
		name       string
		input      RiskInput
		complexity string
	}{
		{
			name: "simple",
			input: RiskInput{
				ReplicationAvailable: true,
				ContainerCount:       3,
				VolumeCount:          1,
			},
			complexity: "simple",
		},
		{
			name: "complex",
			input: RiskInput{
				ReplicationAvailable: false,
				ContainerCount:       15,
				VolumeCount:          8,
				QueueCount:           3,
				DatabaseSizeBytes:    50 * 1024 * 1024 * 1024,
			},
			complexity: "complex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.assessRollbackComplexity(tt.input)
			if got != tt.complexity {
				t.Errorf("rollback complexity = %q, want %q", got, tt.complexity)
			}
		})
	}
}

func newPauseResumeTestPipeline(t *testing.T) (*Pipeline, JobRepository, PipelineRepo) {
	t.Helper()

	jobRepo := newTestRepo(t)
	pipelineRepo := jobRepo.(PipelineRepo)
	srvRepo := newMockServerRepo()
	srvRepo.AddServer(&server.Server{ID: 1, Host: "source", Port: 22, Username: "user"})
	srvRepo.AddServer(&server.Server{ID: 2, Host: "target", Port: 22, Username: "user"})

	pipeline := &Pipeline{
		repo:     pipelineRepo,
		jobRepo:  jobRepo,
		srvRepo:  srvRepo,
		pool:     newMockPool(),
		authSvc:  &mockAuthSvc{},
		hosts:    &mockHostKeyStore{},
		registry: NewCategoryRegistry(),
		stages:   nil,
	}
	return pipeline, jobRepo, pipelineRepo
}

func TestPipelinePauseSetsPausedStateAndAuditEntry(t *testing.T) {
	pipeline, jobRepo, pipelineRepo := newPauseResumeTestPipeline(t)
	migrationID := createTestMigration(t, jobRepo)

	if err := jobRepo.SetMigrationState(migrationID, StatePlanning); err != nil {
		t.Fatalf("SetMigrationState(StatePlanning): %v", err)
	}

	if err := pipeline.Pause(context.Background(), migrationID, nil); err != nil {
		t.Fatalf("Pause() failed: %v", err)
	}

	state, err := jobRepo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("GetMigrationState(): %v", err)
	}
	if state != StatePaused {
		t.Fatalf("Pause() state = %s, want %s", state, StatePaused)
	}

	trail, err := pipelineRepo.GetAuditTrail(migrationID, 10)
	if err != nil {
		t.Fatalf("GetAuditTrail(): %v", err)
	}
	if len(trail) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(trail))
	}
	if trail[0].EventType != "migration_paused" {
		t.Fatalf("audit event type = %q, want %q", trail[0].EventType, "migration_paused")
	}
	if trail[0].PreviousState != StatePlanning.String() {
		t.Fatalf("audit previous state = %q, want %q", trail[0].PreviousState, StatePlanning.String())
	}
	if trail[0].NewState != StatePaused.String() {
		t.Fatalf("audit new state = %q, want %q", trail[0].NewState, StatePaused.String())
	}
}

func TestPipelinePauseIsIdempotentWhenAlreadyPaused(t *testing.T) {
	pipeline, jobRepo, pipelineRepo := newPauseResumeTestPipeline(t)
	migrationID := createTestMigration(t, jobRepo)

	if err := jobRepo.SetMigrationState(migrationID, StatePlanning); err != nil {
		t.Fatalf("SetMigrationState(StatePlanning): %v", err)
	}
	if err := pipeline.Pause(context.Background(), migrationID, nil); err != nil {
		t.Fatalf("first Pause() failed: %v", err)
	}

	if err := pipeline.Pause(context.Background(), migrationID, nil); err != nil {
		t.Fatalf("second Pause() should be idempotent, got error: %v", err)
	}

	state, err := jobRepo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("GetMigrationState(): %v", err)
	}
	if state != StatePaused {
		t.Fatalf("Pause() state = %s, want %s", state, StatePaused)
	}

	trail, err := pipelineRepo.GetAuditTrail(migrationID, 10)
	if err != nil {
		t.Fatalf("GetAuditTrail(): %v", err)
	}
	if len(trail) != 1 {
		t.Fatalf("expected 1 audit entry after idempotent pause, got %d", len(trail))
	}
}

func TestPipelineResumeFromPausedState(t *testing.T) {
	pipeline, jobRepo, _ := newPauseResumeTestPipeline(t)
	migrationID := createTestMigration(t, jobRepo)

	if err := jobRepo.SetMigrationState(migrationID, StatePaused); err != nil {
		t.Fatalf("SetMigrationState(StatePaused): %v", err)
	}

	if err := pipeline.Resume(context.Background(), migrationID, nil); err != nil {
		t.Fatalf("Resume() failed: %v", err)
	}

	state, err := jobRepo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("GetMigrationState(): %v", err)
	}
	if state != StateCommitted {
		t.Fatalf("Resume() final state = %s, want %s", state, StateCommitted)
	}
}

func TestPipelineCancelFromPausedState(t *testing.T) {
	pipeline, jobRepo, pipelineRepo := newPauseResumeTestPipeline(t)
	migrationID := createTestMigration(t, jobRepo)

	if err := jobRepo.SetMigrationState(migrationID, StatePaused); err != nil {
		t.Fatalf("SetMigrationState(StatePaused): %v", err)
	}

	if err := pipeline.Cancel(context.Background(), migrationID, nil); err != nil {
		t.Fatalf("Cancel() failed: %v", err)
	}

	state, err := jobRepo.GetMigrationState(migrationID)
	if err != nil {
		t.Fatalf("GetMigrationState(): %v", err)
	}
	if state != StateCancelled {
		t.Fatalf("Cancel() final state = %s, want %s", state, StateCancelled)
	}

	trail, err := pipelineRepo.GetAuditTrail(migrationID, 10)
	if err != nil {
		t.Fatalf("GetAuditTrail(): %v", err)
	}
	found := false
	for _, entry := range trail {
		if entry.EventType == "migration_cancelled" {
			found = true
			if entry.NewState != StateCancelled.String() {
				t.Fatalf("cancel audit new state = %q, want %q", entry.NewState, StateCancelled.String())
			}
		}
	}
	if !found {
		t.Fatal("expected migration_cancelled audit entry")
	}
}

// --- Mock PipelineRepo ---

type mockPipelineRepo struct{}

func (m *mockPipelineRepo) CreateStage(ctx context.Context, migrationID int, stageName string, stageIndex int) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateStageState(ctx context.Context, stageID int64, state StageState, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) UpdateStageCheckpoint(ctx context.Context, stageID int64, checkpointData string) error {
	return nil
}
func (m *mockPipelineRepo) UpdateStageResult(ctx context.Context, stageID int64, resultData string) error {
	return nil
}
func (m *mockPipelineRepo) IncrementStageAttempt(ctx context.Context, stageID int64) error {
	return nil
}
func (m *mockPipelineRepo) GetStages(migrationID int) ([]PipelineStage, error) { return nil, nil }
func (m *mockPipelineRepo) GetStageByName(migrationID int, stageName string) (*PipelineStage, error) {
	return nil, nil
}
func (m *mockPipelineRepo) GetCompletedStages(migrationID int) ([]PipelineStage, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateReplicationStatus(ctx context.Context, rs ReplicationStatus) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateReplicationStatus(ctx context.Context, id int64, status string, lag int64, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetReplicationStatus(migrationID int) ([]ReplicationStatus, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateTrafficSwitchConfig(ctx context.Context, cfg TrafficSwitchConfig) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateTrafficSwitchState(ctx context.Context, id int64, state string) error {
	return nil
}
func (m *mockPipelineRepo) GetTrafficSwitchConfig(migrationID int) (*TrafficSwitchConfig, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateHealthCheckResult(ctx context.Context, r HealthCheckResult) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) GetHealthHistory(migrationID int, limit int) ([]HealthCheckResult, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateCutoverRecord(ctx context.Context, r CutoverRecord) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateCutoverRecord(ctx context.Context, id int64, completedAt string, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetCutoverHistory(migrationID int) ([]CutoverRecord, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateRollbackRecord(ctx context.Context, r RollbackRecord) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateRollbackRecord(ctx context.Context, id int64, success bool, completedAt string, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetRollbackHistory(migrationID int) ([]RollbackRecord, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateSyncSession(ctx context.Context, s SyncSession) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateSyncSession(ctx context.Context, id int64, bytesTransferred int64, filesTransferred int, speed int64, status string) error {
	return nil
}
func (m *mockPipelineRepo) CompleteSyncSession(ctx context.Context, id int64, status string, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetSyncSessions(migrationID int) ([]SyncSession, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateTransferSession(ctx context.Context, t TransferSession) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateTransferSession(ctx context.Context, id int64, bytesTransferred int64, checksum string, status string) error {
	return nil
}
func (m *mockPipelineRepo) GetTransferSessions(syncSessionID int64) ([]TransferSession, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateVerificationResult(ctx context.Context, r VerificationResult) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) GetVerificationResults(migrationID int) ([]VerificationResult, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateRiskReport(ctx context.Context, r RiskReport) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) GetRiskReport(migrationID int) (*RiskReport, error) { return nil, nil }
func (m *mockPipelineRepo) CreateAuditEntry(ctx context.Context, e AuditEntry) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) GetAuditTrail(migrationID int, limit int) ([]AuditEntry, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateMetric(ctx context.Context, metric MigrationMetric) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) GetMetrics(migrationID int, limit int) ([]MigrationMetric, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateQueueState(ctx context.Context, q QueueState) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateQueueState(ctx context.Context, id int64, paused bool, activeJobs int, drained, synced, verified bool, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetQueueStates(migrationID int) ([]QueueState, error) {
	return nil, nil
}
func (m *mockPipelineRepo) CreateProvisionState(ctx context.Context, p ProvisionState) (int64, error) {
	return 1, nil
}
func (m *mockPipelineRepo) UpdateProvisionState(ctx context.Context, id int64, installed, configured, verified bool, version, errMsg string) error {
	return nil
}
func (m *mockPipelineRepo) GetProvisionStates(migrationID int) ([]ProvisionState, error) {
	return nil, nil
}
func (m *mockPipelineRepo) SetMigrationConfig(migrationID int, config *MigrationConfig) error {
	return nil
}
func (m *mockPipelineRepo) GetMigrationConfig(migrationID int) (*MigrationConfig, error) {
	return DefaultMigrationConfig(), nil
}
func (m *mockPipelineRepo) CreateEvent(ctx context.Context, event MigrationEvent) error {
	return nil
}
func (m *mockPipelineRepo) GetEvents(ctx context.Context, migrationID int, afterSequence int64, limit int) ([]MigrationEvent, error) {
	return nil, nil
}
func (m *mockPipelineRepo) SetMigrationRisk(migrationID int, score float64, class string) error {
	return nil
}
func (m *mockPipelineRepo) GetPlan(ctx context.Context, migrationID int) (map[string]any, error) {
	return nil, nil
}
func (m *mockPipelineRepo) SavePlan(ctx context.Context, migrationID int, plan map[string]any) error {
	return nil
}
func (m *mockPipelineRepo) GetSession(ctx context.Context, id int) (*Migration, error) {
	return nil, nil
}

// --- Helpers ---

func containsInStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
