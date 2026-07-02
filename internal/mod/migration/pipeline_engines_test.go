package migration

import (
	"context"
	"testing"
	"time"
)

// --- Health Engine Tests ---

func TestHealthEngine_ScoreFromLatency(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	tests := []struct {
		latency time.Duration
		min     float64
		max     float64
	}{
		{10 * time.Millisecond, 95, 100},    // Very fast
		{100 * time.Millisecond, 85, 100},   // Fast
		{500 * time.Millisecond, 70, 90},    // Medium
		{2 * time.Second, 50, 70},           // Slow
		{10 * time.Second, 0, 50},           // Very slow
	}

	for _, tt := range tests {
		score := engine.scoreFromLatency(tt.latency)
		if score < tt.min || score > tt.max {
			t.Errorf("scoreFromLatency(%v) = %.1f, want between %.1f and %.1f", tt.latency, score, tt.min, tt.max)
		}
	}
}

func TestHealthEngine_CalculateScore(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	tests := []struct {
		name     string
		results  []HealthCheckResult
		expected float64
	}{
		{
			name:     "empty results",
			results:  []HealthCheckResult{},
			expected: 0,
		},
		{
			name: "all healthy",
			results: []HealthCheckResult{
				{Status: "healthy", HealthScore: 100},
				{Status: "healthy", HealthScore: 100},
			},
			expected: 100,
		},
		{
			name: "mixed results",
			results: []HealthCheckResult{
				{Status: "healthy", HealthScore: 100},
				{Status: "unhealthy", HealthScore: 0},
			},
			expected: 50,
		},
		{
			name: "all failed",
			results: []HealthCheckResult{
				{Status: "error", HealthScore: 0},
				{Status: "error", HealthScore: 0},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := engine.CalculateScore(tt.results)
			if score.Score != tt.expected {
				t.Errorf("CalculateScore().Score = %.1f, want %.1f", score.Score, tt.expected)
			}
		})
	}
}

func TestHealthEngine_CalculateScore_PassFailCount(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	results := []HealthCheckResult{
		{Status: "healthy", HealthScore: 100},
		{Status: "healthy", HealthScore: 90},
		{Status: "unhealthy", HealthScore: 20},
		{Status: "error", HealthScore: 0},
	}

	score := engine.CalculateScore(results)

	if score.ChecksTotal != 4 {
		t.Errorf("ChecksTotal = %d, want 4", score.ChecksTotal)
	}
	if score.ChecksPassed != 2 {
		t.Errorf("ChecksPassed = %d, want 2", score.ChecksPassed)
	}
	if score.ChecksFailed != 2 {
		t.Errorf("ChecksFailed = %d, want 2", score.ChecksFailed)
	}
	if score.ErrorRate != 0.5 {
		t.Errorf("ErrorRate = %.2f, want 0.50", score.ErrorRate)
	}
}

func TestHealthEngine_CheckDNS(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	// Test with a hostname that should resolve
	result := engine.CheckDNS(context.Background(), "localhost")
	if result.Status != "healthy" {
		t.Logf("DNS check for localhost: status=%s error=%s", result.Status, result.ErrorMessage)
	}
}

func TestHealthEngine_CheckDNS_CancelledContext(t *testing.T) {
	engine := NewHealthEngine(nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := engine.checkDNS(ctx, HealthCheckConfig{
		Type:   HealthCheckDNS,
		Target: "example.com",
	})

	if result.Status == "healthy" {
		t.Error("Expected non-healthy result with cancelled context")
	}
}

// --- Risk Engine Tests ---

func TestRiskEngine_ClassifyRisk(t *testing.T) {
	engine := NewRiskEngine(nil)

	tests := []struct {
		score float64
		class RiskClass
	}{
		{0, RiskClassLow},
		{10, RiskClassLow},
		{29, RiskClassLow},
		{30, RiskClassMedium},
		{50, RiskClassMedium},
		{59, RiskClassMedium},
		{60, RiskClassHigh},
		{80, RiskClassHigh},
		{84, RiskClassHigh},
		{85, RiskClassCritical},
		{100, RiskClassCritical},
	}

	for _, tt := range tests {
		result := engine.classifyRisk(tt.score)
		if result != tt.class {
			t.Errorf("classifyRisk(%.0f) = %s, want %s", tt.score, result, tt.class)
		}
	}
}

func TestRiskEngine_CalculateScore_LowRisk(t *testing.T) {
	engine := NewRiskEngine(nil)

	input := RiskInput{
		DataSizeBytes:       100 * 1024 * 1024, // 100MB
		DatabaseSizeBytes:   50 * 1024 * 1024,  // 50MB
		ContainerCount:      2,
		VolumeCount:         1,
		QueueCount:          0,
		ReplicationAvailable: true,
		NetworkSpeedMbps:    1000,
		SourceCPU:           4,
		TargetCPU:           8,
		SourceRAMMB:         4096,
		TargetRAMMB:         8192,
		CompatibilityIssues: 0,
		CriticalIssues:      0,
	}

	score := engine.calculateScore(input)
	if score >= 30 {
		t.Errorf("Expected low risk score for small migration with replication, got %.1f", score)
	}
}

func TestRiskEngine_CalculateScore_HighRisk(t *testing.T) {
	engine := NewRiskEngine(nil)

	input := RiskInput{
		DataSizeBytes:       500 * 1024 * 1024 * 1024, // 500GB
		DatabaseSizeBytes:   200 * 1024 * 1024 * 1024, // 200GB
		ContainerCount:      30,
		VolumeCount:         15,
		QueueCount:          8,
		ReplicationAvailable: false,
		NetworkSpeedMbps:    10,
		SourceCPU:           16,
		TargetCPU:           4,
		SourceRAMMB:         32768,
		TargetRAMMB:         8192,
		CompatibilityIssues: 5,
		CriticalIssues:      2,
	}

	score := engine.calculateScore(input)
	if score < 60 {
		t.Errorf("Expected high risk score for large migration without replication, got %.1f", score)
	}
}

func TestRiskEngine_EstimateDowntime_WithReplication(t *testing.T) {
	engine := NewRiskEngine(nil)

	input := RiskInput{
		ReplicationAvailable: true,
		NetworkSpeedMbps:    1000,
		ContainerCount:      5,
		QueueCount:          2,
	}

	downtime := engine.EstimateDowntime(input)
	if !containsStr(downtime, "0 seconds") && !containsStr(downtime, "seconds") {
		t.Errorf("Expected near-zero downtime with replication, got %s", downtime)
	}
}

func TestRiskEngine_EstimateDowntime_WithoutReplication(t *testing.T) {
	engine := NewRiskEngine(nil)

	input := RiskInput{
		DataSizeBytes:       10 * 1024 * 1024 * 1024, // 10GB
		ReplicationAvailable: false,
		NetworkSpeedMbps:    100,
	}

	downtime := engine.EstimateDowntime(input)
	if containsStr(downtime, "0 seconds") {
		t.Errorf("Expected non-zero downtime without replication, got %s", downtime)
	}
}

func TestRiskEngine_AssessRollbackComplexity(t *testing.T) {
	engine := NewRiskEngine(nil)

	tests := []struct {
		name       string
		input      RiskInput
		complexity string
	}{
		{
			name: "simple",
			input: RiskInput{
				ReplicationAvailable: true,
				ContainerCount:       2,
				VolumeCount:          1,
				QueueCount:           0,
				DatabaseSizeBytes:    1 * 1024 * 1024,
			},
			complexity: "simple",
		},
		{
			name: "complex",
			input: RiskInput{
				ReplicationAvailable: false,
				ContainerCount:       15,
				VolumeCount:          10,
				QueueCount:           5,
				DatabaseSizeBytes:    50 * 1024 * 1024 * 1024,
			},
			complexity: "complex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.assessRollbackComplexity(tt.input)
			if result != tt.complexity {
				t.Errorf("assessRollbackComplexity() = %s, want %s", result, tt.complexity)
			}
		})
	}
}

// --- Compatibility Engine Tests ---

func TestCompatibilityEngine_CheckArchitecture(t *testing.T) {
	engine := NewCompatibilityEngine(nil, nil, nil)

	tests := []struct {
		name     string
		source   *serverInfo
		target   *serverInfo
		passed   bool
		severity Severity
	}{
		{
			name:     "same arch",
			source:   &serverInfo{Arch: "x86_64"},
			target:   &serverInfo{Arch: "x86_64"},
			passed:   true,
			severity: SeverityInfo,
		},
		{
			name:     "different arch",
			source:   &serverInfo{Arch: "x86_64"},
			target:   &serverInfo{Arch: "aarch64"},
			passed:   false,
			severity: SeverityHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.checkArchitecture(tt.source, tt.target)
			if result.Passed != tt.passed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.passed)
			}
			if result.Severity != tt.severity {
				t.Errorf("Severity = %s, want %s", result.Severity, tt.severity)
			}
		})
	}
}

func TestCompatibilityEngine_CheckRAM(t *testing.T) {
	engine := NewCompatibilityEngine(nil, nil, nil)

	tests := []struct {
		name   string
		source *serverInfo
		target *serverInfo
		passed bool
	}{
		{
			name:   "target has more RAM",
			source: &serverInfo{RAMMB: 4096},
			target: &serverInfo{RAMMB: 8192},
			passed: true,
		},
		{
			name:   "target has less RAM",
			source: &serverInfo{RAMMB: 8192},
			target: &serverInfo{RAMMB: 4096},
			passed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.checkRAM(tt.source, tt.target)
			if result.Passed != tt.passed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.passed)
			}
		})
	}
}

func TestCompatibilityEngine_CheckDisk(t *testing.T) {
	engine := NewCompatibilityEngine(nil, nil, nil)

	// Less disk on target is critical
	result := engine.checkDisk(
		&serverInfo{DiskGB: 500},
		&serverInfo{DiskGB: 100},
	)
	if result.Passed || result.Severity != SeverityCritical {
		t.Errorf("Expected critical severity for less disk, got severity=%s passed=%v", result.Severity, result.Passed)
	}
}

func TestCompatibilityEngine_HasBlockers(t *testing.T) {
	tests := []struct {
		name    string
		results []CompatibilityCheckResult
		blocked bool
	}{
		{
			name: "no blockers",
			results: []CompatibilityCheckResult{
				{CheckName: "arch", Severity: SeverityInfo, Passed: true},
				{CheckName: "ram", Severity: SeverityWarning, Passed: false},
			},
			blocked: false,
		},
		{
			name: "has critical blocker",
			results: []CompatibilityCheckResult{
				{CheckName: "disk", Severity: SeverityCritical, Passed: false},
				{CheckName: "arch", Severity: SeverityInfo, Passed: true},
			},
			blocked: true,
		},
		{
			name: "critical but passed",
			results: []CompatibilityCheckResult{
				{CheckName: "disk", Severity: SeverityCritical, Passed: true},
			},
			blocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasBlockers(tt.results)
			if result != tt.blocked {
				t.Errorf("HasBlockers = %v, want %v", result, tt.blocked)
			}
		})
	}
}

// --- State Machine Extended Tests ---

func TestStateMachineExtended_AllStates(t *testing.T) {
	// Test that all 20 states have string representations
	states := []MigrationState{
		StateCreated, StatePlanning, StateDiscovery, StateCompatibilityCheck,
		StateRiskAssessment, StateBackup, StateProvisionTarget,
		StateInstallDependencies, StateInitialSync, StateLiveReplication,
		StateVerification, StatePreCutover, StateTrafficSwitch,
		StatePostVerification, StateObservation, StateCommitted,
		StateFailed, StateRollback, StateRolledBack,
		StateInterrupted, StateResuming, StateCancelled,
	}

	for _, s := range states {
		str := s.String()
		if str == "" || len(str) > 30 {
			t.Errorf("State %d has invalid string: %q", int(s), str)
		}

		stateStr := s.StateString()
		if stateStr == "" {
			t.Errorf("State %d has empty StateString", int(s))
		}

		// Verify round-trip
		parsed, err := StateFromString(stateStr)
		if err != nil {
			t.Errorf("StateFromString(%q) error: %v", stateStr, err)
		} else if parsed != s {
			t.Errorf("StateFromString(%q) = %d, want %d", stateStr, int(parsed), int(s))
		}
	}
}

func TestStateMachineExtended_Transitions(t *testing.T) {
	// Test the full pipeline transition path
	sm := NewStateMachine(StateCreated)

	transitions := []MigrationState{
		StatePlanning,
		StateDiscovery,
		StateCompatibilityCheck,
		StateRiskAssessment,
		StateBackup,
		StateProvisionTarget,
		StateInstallDependencies,
		StateInitialSync,
		StateLiveReplication,
		StateVerification,
		StatePreCutover,
		StateTrafficSwitch,
		StatePostVerification,
		StateObservation,
		StateCommitted,
	}

	for _, target := range transitions {
		if err := sm.Transition(target); err != nil {
			t.Fatalf("Transition to %s failed: %v", target, err)
		}
	}

	if !sm.IsTerminal() {
		t.Error("Expected terminal state after committed")
	}
}

func TestStateMachineExtended_FailedTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)

	// Can't jump directly to committed
	err := sm.Transition(StateCommitted)
	if err == nil {
		t.Error("Expected error for invalid transition Created → Committed")
	}
}

func TestStateMachineExtended_InterruptAndResume(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.Transition(StatePlanning)
	sm.Transition(StateDiscovery)

	// Interrupt
	if err := sm.Transition(StateInterrupted); err != nil {
		t.Fatalf("Transition to interrupted failed: %v", err)
	}

	if !sm.CanResume() {
		t.Error("Expected CanResume() to be true for interrupted state")
	}

	// Resume back to discovery
	if err := sm.Transition(StateResuming); err != nil {
		t.Fatalf("Transition to resuming failed: %v", err)
	}

	if err := sm.Transition(StateDiscovery); err != nil {
		t.Fatalf("Transition from resuming to discovery failed: %v", err)
	}
}

func TestStateMachineExtended_Cancel(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.Transition(StatePlanning)

	// Can cancel from interrupted
	sm.Transition(StateInterrupted)
	if err := sm.Transition(StateCancelled); err != nil {
		t.Fatalf("Transition to cancelled failed: %v", err)
	}

	if !sm.IsTerminal() {
		t.Error("Expected cancelled to be terminal")
	}
}

func TestStateMachineExtended_ForceTransition(t *testing.T) {
	sm := NewStateMachine(StateCreated)
	sm.ForceTransition(StateCommitted)

	if sm.State() != StateCommitted {
		t.Error("ForceTransition should set state without validation")
	}
}

// --- Observation Engine Tests ---

func TestObservationEngine_CheckThresholds(t *testing.T) {
	engine := NewObservationEngine(nil, nil)

	config := ObservationConfig{
		MinHealthScore: 80,
		MaxErrorRate:   0.05,
		MaxLatencyMs:   5000,
	}

	tests := []struct {
		name  string
		score HealthScore
		ok    bool
	}{
		{
			name:  "all good",
			score: HealthScore{Score: 95, ErrorRate: 0.01, AvgResponseMs: 100},
			ok:    true,
		},
		{
			name:  "low health score",
			score: HealthScore{Score: 60, ErrorRate: 0.01, AvgResponseMs: 100},
			ok:    false,
		},
		{
			name:  "high error rate",
			score: HealthScore{Score: 95, ErrorRate: 0.10, AvgResponseMs: 100},
			ok:    false,
		},
		{
			name:  "high latency",
			score: HealthScore{Score: 95, ErrorRate: 0.01, AvgResponseMs: 10000},
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.checkThresholds(tt.score, config)
			if result != tt.ok {
				t.Errorf("checkThresholds() = %v, want %v", result, tt.ok)
			}
		})
	}
}

// --- Helper ---

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
