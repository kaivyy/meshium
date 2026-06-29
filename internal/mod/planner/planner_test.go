package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"meshium/internal/db"
	"meshium/internal/mod/discovery"
	"meshium/internal/mod/migration"
)

// --- Test Helpers ---

// mockSourceSnapshot creates a realistic source server snapshot for testing.
func mockSourceSnapshot() *discovery.ServerSnapshot {
	return &discovery.ServerSnapshot{
		CapturedAt: time.Now().UTC(),
		OS: discovery.OSInfo{
			Distro:       "Ubuntu 22.04.3 LTS",
			Kernel:       "5.15.0-91-generic",
			Architecture:  "x86_64",
			Hostname:     "source-server",
			Timezone:     "UTC",
			Uptime:       864000,
		},
		Hardware: discovery.HardwareInfo{
			CPUModel:    "Intel Xeon E5-2680",
			CPUCores:    4,
			RAMTotalMB:  8192,
			RAMUsedMB:   4096,
			DiskTotalGB: 100.0,
			DiskUsedGB:  45.0,
		},
		Docker: &discovery.DockerInfo{
			Version: "24.0.7",
			Containers: []discovery.ContainerInfo{
				{
					Name:   "web-app",
					Image:  "nginx:1.25",
					Status: "Up 3 days",
					State:  "running",
					Ports: []discovery.PortMapping{
						{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
					},
					Volumes: []string{"/var/www/html:/usr/share/nginx/html"},
					Networks: []string{"bridge"},
				},
				{
					Name:   "db",
					Image:  "mysql:8.0",
					Status: "Up 3 days",
					State:  "running",
					Ports: []discovery.PortMapping{
						{HostPort: 3306, ContainerPort: 3306, Protocol: "tcp"},
					},
					Volumes: []string{"/var/lib/mysql:/var/lib/mysql"},
					Networks: []string{"bridge"},
				},
			},
			Images: []discovery.ImageInfo{
				{Repository: "nginx", Tag: "1.25", ID: "sha256:abc123", Size: "150MB"},
				{Repository: "mysql", Tag: "8.0", ID: "sha256:def456", Size: "600MB"},
			},
		},
		Services: []discovery.SystemService{
			{
				Name:        "nginx.service",
				Description: "The nginx HTTP and reverse proxy server",
				LoadState:   "loaded",
				ActiveState: "active",
				SubState:    "running",
				Type:        "simple",
				DependsOn:   []string{"network.target"},
			},
		},
		Databases: []discovery.DatabaseInfo{
			{
				Type:       "mysql",
				Version:    "8.0.35",
				Port:       3306,
				ProcessName: "mysqld",
				DataDir:    "/var/lib/mysql",
				SizeMB:     2048,
				Running:    true,
			},
		},
		Nginx: &discovery.NginxInfo{
			Version: "1.18.0",
			VHosts: []discovery.NginxVHost{
				{
					ServerName:  "example.com",
					Listen:      "80",
					Root:        "/var/www/html",
					ProxyPass:   "http://127.0.0.1:8080",
					ConfigFile:  "/etc/nginx/sites-available/example.com",
				},
			},
		},
		DiskUsage: []discovery.DiskPartition{
			{
				Filesystem:  "/dev/sda1",
				MountPoint:  "/",
				SizeGB:      100.0,
				UsedGB:      45.0,
				AvailGB:     55.0,
				UsePercent:  45.0,
			},
		},
		NetworkPorts: []discovery.OpenPort{
			{Port: 80, Protocol: "tcp", Process: "nginx", PID: 1234, Address: "0.0.0.0"},
			{Port: 3306, Protocol: "tcp", Process: "mysqld", PID: 5678, Address: "127.0.0.1"},
			{Port: 8080, Protocol: "tcp", Process: "docker-proxy", PID: 9012, Address: "0.0.0.0"},
		},
	}
}

// mockTargetSnapshot creates a realistic target server snapshot for testing.
func mockTargetSnapshot() *discovery.ServerSnapshot {
	return &discovery.ServerSnapshot{
		CapturedAt: time.Now().UTC(),
		OS: discovery.OSInfo{
			Distro:       "Ubuntu 22.04.3 LTS",
			Kernel:       "5.15.0-91-generic",
			Architecture:  "x86_64",
			Hostname:     "target-server",
			Timezone:     "UTC",
			Uptime:       3600,
		},
		Hardware: discovery.HardwareInfo{
			CPUModel:    "Intel Xeon E5-2680",
			CPUCores:    8,
			RAMTotalMB:  16384,
			RAMUsedMB:   2048,
			DiskTotalGB: 200.0,
			DiskUsedGB:  10.0,
		},
		Docker: &discovery.DockerInfo{
			Version: "24.0.7",
			Containers: []discovery.ContainerInfo{},
			Images:    []discovery.ImageInfo{},
		},
		NetworkPorts: []discovery.OpenPort{
			{Port: 22, Protocol: "tcp", Process: "sshd", PID: 1, Address: "0.0.0.0"},
		},
	}
}

// mockTargetSnapshotWithPortConflict creates a target with a port conflict.
func mockTargetSnapshotWithPortConflict() *discovery.ServerSnapshot {
	target := mockTargetSnapshot()
	target.NetworkPorts = append(target.NetworkPorts, discovery.OpenPort{
		Port: 80, Protocol: "tcp", Process: "apache2", PID: 999, Address: "0.0.0.0",
	})
	return target
}

// --- Plan Generation Tests ---

func TestCreatePlan_BasicPlan(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if plan == nil {
		t.Fatal("plan is nil")
	}

	if plan.ID == "" {
		t.Error("plan ID is empty")
	}

	if plan.CreatedAt.IsZero() {
		t.Error("plan CreatedAt is zero")
	}

	if plan.Source.Hostname != "source-server" {
		t.Errorf("expected source hostname 'source-server', got '%s'", plan.Source.Hostname)
	}

	if plan.Target.Hostname != "target-server" {
		t.Errorf("expected target hostname 'target-server', got '%s'", plan.Target.Hostname)
	}

	if len(plan.Steps) == 0 {
		t.Error("plan has no steps")
	}

	t.Logf("Plan has %d steps, risk level: %s", len(plan.Steps), plan.RiskLevel)
	for i, step := range plan.Steps {
		t.Logf("  Step %d: %s (type=%s, risk=%s, dependsOn=%v)", i, step.Name, step.Type, step.RiskLevel, step.DependsOn)
	}
}

func TestCreatePlan_StepOrder(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Steps should be ordered from topological sort
	for i, step := range plan.Steps {
		if step.Order != i {
			t.Errorf("step %d has order %d, expected %d", i, step.Order, i)
		}
	}
}

func TestCreatePlan_StepTypes(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// We expect steps for containers, databases, services, and nginx
	stepTypes := make(map[StepType]bool)
	for _, step := range plan.Steps {
		stepTypes[step.Type] = true
	}

	// Should have at least database and service steps
	if !stepTypes[StepTypeDatabase] {
		t.Error("plan missing database step")
	}

	if !stepTypes[StepTypeService] {
		t.Error("plan missing service step")
	}
}

func TestCreatePlan_WithBlockers(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshotWithPortConflict()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if !plan.HasBlockers() {
		t.Error("expected plan to have blockers due to port conflict")
	}

	t.Logf("Plan has %d blockers:", len(plan.Blockers))
	for _, b := range plan.Blockers {
		t.Logf("  Blocker: %s — %s", b.Code, b.Message)
	}

	if plan.RiskLevel != RiskCritical {
		t.Errorf("expected risk level Critical with blockers, got %s", plan.RiskLevel)
	}
}

func TestCreatePlan_NoBlockers(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if plan.HasBlockers() {
		t.Errorf("expected no blockers, got %d: %v", len(plan.Blockers), plan.Blockers)
	}
}

func TestCreatePlan_NilSource(t *testing.T) {
	planner := NewDefaultPlanner()
	_, err := planner.CreatePlan(context.Background(), nil, mockTargetSnapshot())
	if err == nil {
		t.Error("expected error for nil source")
	}
}

func TestCreatePlan_NilTarget(t *testing.T) {
	planner := NewDefaultPlanner()
	_, err := planner.CreatePlan(context.Background(), mockSourceSnapshot(), nil)
	if err == nil {
		t.Error("expected error for nil target")
	}
}

func TestCreatePlan_CancelledContext(t *testing.T) {
	planner := NewDefaultPlanner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := planner.CreatePlan(ctx, mockSourceSnapshot(), mockTargetSnapshot())
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestCreatePlan_TotalEstimate(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Total estimate should be non-zero if there are steps with data
	if len(plan.Steps) > 0 {
		var totalSize int64
		for _, step := range plan.Steps {
			totalSize += step.Estimate.SizeBytes
		}
		if totalSize > 0 && plan.TotalEstimate.SizeBytes == 0 {
			t.Error("total estimate size is zero but steps have data")
		}
	}
}

func TestCreatePlan_DependencyCycle(t *testing.T) {
	planner := NewDefaultPlanner()

	// Create a snapshot that would cause a dependency cycle
	// This is hard to do with the current graph builder, so we test
	// the behavior by using a custom graph builder that returns a graph with a cycle
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	// Override the graph builder to return a graph with a cycle
	planner.graphBuilder = func(s *discovery.ServerSnapshot) *discovery.DependencyGraph {
		return &discovery.DependencyGraph{
			Nodes: []discovery.DependencyNode{
				{ID: "service:a", Name: "a", Type: "service"},
				{ID: "service:b", Name: "b", Type: "service"},
			},
			Edges: []discovery.DependencyEdge{
				{From: "service:a", To: "service:b", Reason: "test"},
				{From: "service:b", To: "service:a", Reason: "cycle"},
			},
		}
	}

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if !plan.HasBlockers() {
		t.Error("expected blockers for dependency cycle")
	}

	found := false
	for _, b := range plan.Blockers {
		if b.Code == "DEPENDENCY_CYCLE" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DEPENDENCY_CYCLE blocker")
	}
}

// --- Risk Assessment Tests ---

func TestRiskAssessor_DatabaseStep(t *testing.T) {
	assessor := NewDefaultRiskAssessor()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	step := PlannedStep{
		Order:     0,
		Name:      "database-mysql",
		Type:      StepTypeDatabase,
		RiskLevel: RiskLow, // will be overridden
		Config: map[string]interface{}{
			"type": "mysql",
		},
	}

	risk := assessor.AssessStep(step, source, target)
	// Database with running process should be High risk
	if risk != RiskHigh {
		t.Errorf("expected High risk for running database, got %s", risk)
	}
}

func TestRiskAssessor_DockerVolumeStep(t *testing.T) {
	assessor := NewDefaultRiskAssessor()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	step := PlannedStep{
		Order:     0,
		Name:      "docker-volume-web-app",
		Type:      StepTypeDockerVolume,
		Config: map[string]interface{}{
			"containerName": "web-app",
		},
	}

	risk := assessor.AssessStep(step, source, target)
	// Running container with volume should be High risk (needs downtime)
	if risk != RiskHigh {
		t.Errorf("expected High risk for running container volume, got %s", risk)
	}
}

func TestRiskAssessor_NginxStep(t *testing.T) {
	assessor := NewDefaultRiskAssessor()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	step := PlannedStep{
		Order:     0,
		Name:      "nginx-example.com",
		Type:      StepTypeNginx,
		Config: map[string]interface{}{
			"serverName": "example.com",
		},
	}

	risk := assessor.AssessStep(step, source, target)
	if risk != RiskLow {
		t.Errorf("expected Low risk for nginx config, got %s", risk)
	}
}

func TestRiskAssessor_ServiceStep(t *testing.T) {
	assessor := NewDefaultRiskAssessor()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	step := PlannedStep{
		Order:     0,
		Name:      "service-nginx.service",
		Type:      StepTypeService,
		Config: map[string]interface{}{
			"name": "nginx.service",
		},
	}

	risk := assessor.AssessStep(step, source, target)
	// nginx.service has known dependencies (network.target), so Low risk
	if risk != RiskLow {
		t.Errorf("expected Low risk for service with known deps, got %s", risk)
	}
}

func TestRiskAssessor_OverallWithBlockers(t *testing.T) {
	assessor := NewDefaultRiskAssessor()

	plan := &MigrationPlan{
		Steps: []PlannedStep{
			{RiskLevel: RiskLow},
		},
		Blockers: []PlanBlocker{
			{Code: "port", Message: "port conflict"},
		},
	}

	risk := assessor.AssessOverall(plan)
	if risk != RiskCritical {
		t.Errorf("expected Critical with blockers, got %s", risk)
	}
}

func TestRiskAssessor_OverallMultipleHighRisk(t *testing.T) {
	assessor := NewDefaultRiskAssessor()

	plan := &MigrationPlan{
		Steps: []PlannedStep{
			{RiskLevel: RiskHigh},
			{RiskLevel: RiskHigh},
			{RiskLevel: RiskHigh},
		},
	}

	risk := assessor.AssessOverall(plan)
	// 3+ high risk steps should elevate to Critical
	if risk != RiskCritical {
		t.Errorf("expected Critical with 3+ high risk steps, got %s", risk)
	}
}

func TestRiskAssessor_OverallLowRisk(t *testing.T) {
	assessor := NewDefaultRiskAssessor()

	plan := &MigrationPlan{
		Steps: []PlannedStep{
			{RiskLevel: RiskLow},
			{RiskLevel: RiskLow},
		},
	}

	risk := assessor.AssessOverall(plan)
	if risk != RiskLow {
		t.Errorf("expected Low, got %s", risk)
	}
}

func TestHasPortConflict(t *testing.T) {
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	// No conflict — target only has port 22
	if HasPortConflict(source, target) {
		t.Error("expected no port conflict")
	}

	// Add conflict
	targetWithConflict := mockTargetSnapshotWithPortConflict()
	if !HasPortConflict(source, targetWithConflict) {
		t.Error("expected port conflict")
	}
}

// --- Estimator Tests ---

func TestEstimator_DatabaseStep(t *testing.T) {
	estimator := NewDefaultEstimator()
	source := mockSourceSnapshot()

	step := PlannedStep{
		Type: StepTypeDatabase,
		Config: map[string]interface{}{
			"type": "mysql",
		},
	}

	estimate := estimator.EstimateStep(step, source)

	// MySQL database with SizeMB=2048 should have ~2GB estimate
	expectedSize := int64(2048) * 1024 * 1024
	if estimate.SizeBytes != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, estimate.SizeBytes)
	}

	if estimate.DurationMin <= 0 {
		t.Error("expected non-zero duration min")
	}

	if estimate.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5 for database with known size, got %f", estimate.Confidence)
	}

	t.Logf("Database estimate: size=%d bytes, durationMin=%v, durationMax=%v, confidence=%.2f",
		estimate.SizeBytes, estimate.DurationMin, estimate.DurationMax, estimate.Confidence)
}

func TestEstimator_DockerImageStep(t *testing.T) {
	estimator := NewDefaultEstimator()
	source := mockSourceSnapshot()

	step := PlannedStep{
		Type: StepTypeDockerImage,
		Config: map[string]interface{}{
			"image": "nginx:1.25",
		},
	}

	estimate := estimator.EstimateStep(step, source)

	// nginx:1.25 image is 150MB
	expectedSize := int64(150) * 1024 * 1024
	if estimate.SizeBytes != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, estimate.SizeBytes)
	}
}

func TestEstimator_ServiceStep(t *testing.T) {
	estimator := NewDefaultEstimator()
	source := mockSourceSnapshot()

	step := PlannedStep{
		Type: StepTypeService,
		Config: map[string]interface{}{
			"name": "nginx.service",
		},
	}

	estimate := estimator.EstimateStep(step, source)

	// Services don't transfer data
	if estimate.SizeBytes != 0 {
		t.Errorf("expected 0 size for service step, got %d", estimate.SizeBytes)
	}
}

func TestEstimator_NilSource(t *testing.T) {
	estimator := NewDefaultEstimator()

	step := PlannedStep{Type: StepTypeFile}

	estimate := estimator.EstimateStep(step, nil)

	if estimate.Confidence != 0.0 {
		t.Errorf("expected 0 confidence for nil source, got %f", estimate.Confidence)
	}
}

func TestParseImageSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"150MB", 150 * 1024 * 1024},
		{"1.2GB", 1 * 1024 * 1024 * 1024}, // integer part only
		{"500KB", 500 * 1024},
		{"2TB", 2 * 1024 * 1024 * 1024 * 1024},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		got := parseImageSize(tt.input)
		if got != tt.want {
			t.Errorf("parseImageSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// --- Plan Store Tests ---

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := fmt.Sprintf(":memory:")
	db, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPlanStore_SaveAndLoad(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	plan := &MigrationPlan{
		ID:        "test-plan-1",
		CreatedAt:  time.Now().UTC(),
		Source:    ServerSummary{Hostname: "source-server", OS: "Ubuntu 22.04"},
		Target:    ServerSummary{Hostname: "target-server", OS: "Ubuntu 22.04"},
		Steps: []PlannedStep{
			{
				Order:      0,
				Name:       "database-mysql",
				Type:       StepTypeDatabase,
				RiskLevel:  RiskHigh,
				Reversible:  true,
				Config:     map[string]interface{}{"type": "mysql"},
			},
			{
				Order:      1,
				Name:       "service-nginx",
				Type:       StepTypeService,
				RiskLevel:  RiskLow,
				Reversible:  true,
				Config:     map[string]interface{}{"name": "nginx.service"},
			},
		},
		RiskLevel: RiskHigh,
	}
	plan.TotalEstimate = TransferEstimate{
		SizeBytes:   2 * 1024 * 1024 * 1024,
		DurationMin: 20 * time.Second,
		DurationMax: 30 * time.Second,
		Confidence:  0.85,
	}

	// Save
	if err := store.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// Load
	loaded, err := store.LoadPlan(context.Background(), "test-plan-1")
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}

	if loaded.ID != plan.ID {
		t.Errorf("ID mismatch: got %s, want %s", loaded.ID, plan.ID)
	}
	if loaded.Source.Hostname != plan.Source.Hostname {
		t.Errorf("Source hostname mismatch: got %s, want %s", loaded.Source.Hostname, plan.Source.Hostname)
	}
	if len(loaded.Steps) != len(plan.Steps) {
		t.Errorf("Steps count mismatch: got %d, want %d", len(loaded.Steps), len(plan.Steps))
	}
	if loaded.RiskLevel != plan.RiskLevel {
		t.Errorf("RiskLevel mismatch: got %s, want %s", loaded.RiskLevel, plan.RiskLevel)
	}
	if loaded.TotalEstimate.SizeBytes != plan.TotalEstimate.SizeBytes {
		t.Errorf("TotalEstimate size mismatch: got %d, want %d", loaded.TotalEstimate.SizeBytes, plan.TotalEstimate.SizeBytes)
	}
}

func TestPlanStore_LoadNotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	_, err := store.LoadPlan(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent plan")
	}
}

func TestPlanStore_ListPlans(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	// Save multiple plans
	for i := 0; i < 3; i++ {
		plan := &MigrationPlan{
			ID:        fmt.Sprintf("test-plan-%d", i),
			CreatedAt:  time.Now().Add(time.Duration(i) * time.Minute).UTC(),
			Source:    ServerSummary{Hostname: "source"},
			Target:    ServerSummary{Hostname: "target"},
			Steps:     []PlannedStep{{Order: 0, Name: "step1", Type: StepTypeFile}},
			RiskLevel: RiskLow,
		}
		if err := store.SavePlan(context.Background(), plan); err != nil {
			t.Fatalf("SavePlan %d: %v", i, err)
		}
	}

	// List
	summaries, err := store.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}

	if len(summaries) != 3 {
		t.Errorf("expected 3 plans, got %d", len(summaries))
	}
}

func TestPlanStore_DeletePlan(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	plan := &MigrationPlan{
		ID:        "delete-test",
		CreatedAt:  time.Now().UTC(),
		Source:    ServerSummary{Hostname: "source"},
		Target:    ServerSummary{Hostname: "target"},
		Steps:     []PlannedStep{{Order: 0, Name: "step1", Type: StepTypeFile}},
		RiskLevel: RiskLow,
	}

	if err := store.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	if err := store.DeletePlan(context.Background(), "delete-test"); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}

	// Verify it's gone
	_, err := store.LoadPlan(context.Background(), "delete-test")
	if err == nil {
		t.Error("expected error after deletion")
	}
}

func TestPlanStore_DeleteNonExistent(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	err := store.DeletePlan(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for deleting non-existent plan")
	}
}

func TestPlanStore_SaveNilPlan(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	err := store.SavePlan(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestPlanStore_SaveEmptyID(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	plan := &MigrationPlan{
		ID:        "",
		CreatedAt:  time.Now().UTC(),
	}

	err := store.SavePlan(context.Background(), plan)
	if err == nil {
		t.Error("expected error for empty plan ID")
	}
}

// --- Bridge Tests ---

func TestBuildSteps_BasicPlan(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	steps, err := BuildSteps(plan, nil, nil)
	if err != nil {
		t.Fatalf("BuildSteps failed: %v", err)
	}

	if len(steps) != len(plan.Steps) {
		t.Errorf("expected %d steps, got %d", len(plan.Steps), len(steps))
	}

	// Verify each step implements migration.MigrationStep
	for i, step := range steps {
		if step.Name() == "" {
			t.Errorf("step %d has empty name", i)
		}
		var _ migration.MigrationStep = step
	}

	t.Logf("Built %d migration steps from plan", len(steps))
	for i, step := range steps {
		t.Logf("  Step %d: %s", i, step.Name())
	}
}

func TestBuildSteps_EmptyPlan(t *testing.T) {
	plan := &MigrationPlan{
		ID:        "empty-plan",
		CreatedAt: time.Now().UTC(),
	}

	steps, err := BuildSteps(plan, nil, nil)
	if err != nil {
		t.Fatalf("BuildSteps failed: %v", err)
	}

	if len(steps) != 0 {
		t.Errorf("expected 0 steps for empty plan, got %d", len(steps))
	}
}

func TestBuildSteps_NilPlan(t *testing.T) {
	_, err := BuildSteps(nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil plan")
	}
}

func TestBuildSteps_WithBlockers(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshotWithPortConflict()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if !plan.HasBlockers() {
		t.Fatal("expected plan to have blockers")
	}

	// BuildSteps should still work — the caller decides whether to proceed
	steps, err := BuildSteps(plan, nil, nil)
	if err != nil {
		t.Fatalf("BuildSteps failed with blockers: %v", err)
	}

	if len(steps) == 0 {
		t.Error("expected steps even with blockers")
	}
}

// --- Concurrency Tests (--race) ---

func TestPlanStore_ConcurrentSave(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			plan := &MigrationPlan{
				ID:        fmt.Sprintf("concurrent-%d", idx),
				CreatedAt:  time.Now().UTC(),
				Source:    ServerSummary{Hostname: "source"},
				Target:    ServerSummary{Hostname: "target"},
				Steps:     []PlannedStep{{Order: 0, Name: "step", Type: StepTypeFile}},
				RiskLevel: RiskLow,
			}
			_ = store.SavePlan(context.Background(), plan)
		}(i)
	}
	wg.Wait()

	// Verify all plans were saved
	summaries, err := store.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(summaries) != 10 {
		t.Errorf("expected 10 plans, got %d", len(summaries))
	}
}

func TestPlanStore_ConcurrentReadWrite(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	// Pre-save some plans
	for i := 0; i < 5; i++ {
		plan := &MigrationPlan{
			ID:        fmt.Sprintf("rw-%d", i),
			CreatedAt:  time.Now().UTC(),
			Source:    ServerSummary{Hostname: "source"},
			Target:    ServerSummary{Hostname: "target"},
			Steps:     []PlannedStep{{Order: 0, Name: "step", Type: StepTypeFile}},
			RiskLevel: RiskLow,
		}
		_ = store.SavePlan(context.Background(), plan)
	}

	var wg sync.WaitGroup

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = store.LoadPlan(context.Background(), fmt.Sprintf("rw-%d", idx%5))
		}(i)
	}

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			plan := &MigrationPlan{
				ID:        fmt.Sprintf("rw-new-%d", idx),
				CreatedAt:  time.Now().UTC(),
				Source:    ServerSummary{Hostname: "source"},
				Target:    ServerSummary{Hostname: "target"},
				Steps:     []PlannedStep{{Order: 0, Name: "step", Type: StepTypeFile}},
				RiskLevel: RiskLow,
			}
			_ = store.SavePlan(context.Background(), plan)
		}(i)
	}

	// Concurrent lister
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.ListPlans(context.Background())
		}()
	}

	wg.Wait()
}

func TestCreatePlan_Concurrent(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := planner.CreatePlan(context.Background(), source, target)
			if err != nil {
				t.Errorf("CreatePlan failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

// --- JSON Serialization Test ---

func TestMigrationPlan_JSONRoundtrip(t *testing.T) {
	plan := &MigrationPlan{
		ID:        "json-test",
		CreatedAt:  time.Now().UTC(),
		Source:    ServerSummary{Hostname: "source", OS: "Ubuntu 22.04", RAMTotalMB: 8192, DiskTotalGB: 100},
		Target:    ServerSummary{Hostname: "target", OS: "Ubuntu 22.04", RAMTotalMB: 16384, DiskTotalGB: 200},
		Steps: []PlannedStep{
			{
				Order:      0,
				Name:       "database-mysql",
				Type:       StepTypeDatabase,
				DependsOn:  []int{},
				RiskLevel:  RiskHigh,
				Reversible:  true,
				Config:     map[string]interface{}{"type": "mysql", "port": 3306},
			},
		},
		TotalEstimate: TransferEstimate{
			SizeBytes:   2 * 1024 * 1024 * 1024,
			DurationMin: 20 * time.Second,
			DurationMax: 30 * time.Second,
			Confidence:  0.85,
		},
		RiskLevel: RiskHigh,
		Warnings:  []PlanWarning{{Code: "docker", Message: "target Docker version is older"}},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded MigrationPlan
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.ID != plan.ID {
		t.Errorf("ID mismatch: got %s, want %s", loaded.ID, plan.ID)
	}
	if loaded.Source.Hostname != plan.Source.Hostname {
		t.Errorf("Source hostname mismatch")
	}
	if len(loaded.Steps) != 1 {
		t.Fatalf("Steps count mismatch: got %d, want 1", len(loaded.Steps))
	}
	if loaded.Steps[0].Type != StepTypeDatabase {
		t.Errorf("Step type mismatch: got %s, want %s", loaded.Steps[0].Type, StepTypeDatabase)
	}
	if loaded.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel mismatch: got %s, want %s", loaded.RiskLevel, RiskHigh)
	}
}

// --- Integration Test: Full Pipeline ---

func TestIntegration_FullPipeline(t *testing.T) {
	// 1. Create snapshots
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()

	// 2. Create plan
	planner := NewDefaultPlanner()
	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	t.Logf("Plan created: %d steps, risk=%s", plan.StepCount(), plan.RiskLevel)

	// 3. Save plan to store
	db := newTestDB(t)
	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	if err := store.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// 4. Load plan from store
	loaded, err := store.LoadPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}

	if loaded.StepCount() != plan.StepCount() {
		t.Errorf("step count mismatch: loaded=%d, original=%d", loaded.StepCount(), plan.StepCount())
	}

	// 5. Build migration steps from plan
	migrationSteps, err := BuildSteps(loaded, nil, nil)
	if err != nil {
		t.Fatalf("BuildSteps: %v", err)
	}

	if len(migrationSteps) != loaded.StepCount() {
		t.Errorf("migration steps count mismatch: got %d, want %d", len(migrationSteps), loaded.StepCount())
	}

	// 6. Verify all steps implement MigrationStep
	for i, step := range migrationSteps {
		var _ migration.MigrationStep = step
		if step.Name() == "" {
			t.Errorf("step %d has empty name", i)
		}
		t.Logf("  Migration step %d: %s", i, step.Name())
	}

	// 7. List plans
	summaries, err := store.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}

	if len(summaries) != 1 {
		t.Errorf("expected 1 plan summary, got %d", len(summaries))
	}

	t.Log("Full pipeline test passed!")
}

// --- Edge Case Tests ---

func TestCreatePlan_EmptySnapshot(t *testing.T) {
	planner := NewDefaultPlanner()
	source := &discovery.ServerSnapshot{}
	target := &discovery.ServerSnapshot{}

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Empty snapshots should produce a plan with no steps
	if len(plan.Steps) != 0 {
		t.Errorf("expected 0 steps for empty snapshots, got %d", len(plan.Steps))
	}
}

func TestCreatePlan_NoDockerOnSource(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	source.Docker = nil // Remove Docker from source

	target := mockTargetSnapshot()

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Should not have Docker steps
	for _, step := range plan.Steps {
		if step.Type == StepTypeDockerVolume || step.Type == StepTypeDockerImage {
			t.Errorf("unexpected Docker step when source has no Docker: %s", step.Name)
		}
	}
}

func TestCreatePlan_DockerOnSourceNoDockerOnTarget(t *testing.T) {
	planner := NewDefaultPlanner()
	source := mockSourceSnapshot()
	target := mockTargetSnapshot()
	target.Docker = nil // Remove Docker from target

	plan, err := planner.CreatePlan(context.Background(), source, target)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Should have a blocker: source has Docker but target doesn't
	if !plan.HasBlockers() {
		t.Error("expected blockers when source has Docker but target doesn't")
	}

	found := false
	for _, b := range plan.Blockers {
		if b.Code == "docker" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Docker blocker")
	}
}

// --- File-based SQLite Test ---

func TestPlanStore_FileBasedDB(t *testing.T) {
	// Create a temporary file-based SQLite database
	tmpDir, err := os.MkdirTemp("", "planner-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := tmpDir + "/test.db"
	db, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	store := NewSQLitePlanStore(db)
	if err := store.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}

	plan := &MigrationPlan{
		ID:        "file-test",
		CreatedAt:  time.Now().UTC(),
		Source:    ServerSummary{Hostname: "source"},
		Target:    ServerSummary{Hostname: "target"},
		Steps:     []PlannedStep{{Order: 0, Name: "step", Type: StepTypeFile}},
		RiskLevel: RiskLow,
	}

	if err := store.SavePlan(context.Background(), plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// Verify WAL mode is enabled
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected WAL mode, got %s", journalMode)
	}

	// Load and verify
	loaded, err := store.LoadPlan(context.Background(), "file-test")
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if loaded.ID != "file-test" {
		t.Errorf("ID mismatch: got %s", loaded.ID)
	}
}
