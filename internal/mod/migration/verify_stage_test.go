package migration

import (
	"context"
	"testing"
)

// The docker branch of health_verification passed the WHOLE appliedByCat map
// into attestVerification, so a single container reporting "Up" upgraded every
// applied item in EVERY category to runtime_verified — packages and configs
// included. That is a false green of exactly the kind this stage exists to
// prevent. Verification must be per item, from that item's own probe.
func TestHealthVerificationDoesNotUpgradeUnrelatedCategories(t *testing.T) {
	repo := newStubVerifyRepo()
	repo.items = []ItemResult{
		{ItemKey: "docker-container:web", Category: "docker", ExecutionState: ExecApplied},
		{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecApplied},
	}

	ssh := newMockSSH()
	ssh.addOutput("echo ok", "ok\n")
	ssh.addOutput("docker ps", "web Up 3 minutes\n")
	// The package is NOT installed on the target.
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  curl\n")

	stage := &healthVerificationStage{repo: repo}
	pc := &PipelineContext{
		MigrationID: 1,
		TargetSSH:   ssh,
		Repo:        repo,
		Migration:   &Migration{ID: 1, Categories: []string{"docker", "packages"}},
		OnProgress:  func(WSMessage) {},
	}

	_ = stage.Execute(context.Background(), pc)

	pkg := repo.saved["package:nginx"]
	if pkg.VerificationState == VerifyRuntime {
		t.Errorf("package was upgraded to runtime by a DOCKER probe: %+v", pkg)
	}
	if pkg.VerificationState == VerifyInfra || pkg.VerificationState == VerifyApp {
		t.Errorf("package absent from the target but verified %q", pkg.VerificationState)
	}
	if pkg.VerificationState != VerifyFailed {
		t.Errorf("absent package should be verify_failed, got %q (%s)", pkg.VerificationState, pkg.VerifyNotes)
	}
}

// A package that IS present earns infra, and a running service earns runtime —
// each from its own evidence.
func TestHealthVerificationEarnsLevelsPerCategory(t *testing.T) {
	repo := newStubVerifyRepo()
	repo.items = []ItemResult{
		{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecApplied},
		{ItemKey: "service:nginx", Category: "services", ExecutionState: ExecApplied},
	}

	ssh := newMockSSH()
	ssh.addOutput("echo ok", "ok\n")
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\n")
	ssh.addOutput("is-active", "active\n")

	stage := &healthVerificationStage{repo: repo}
	pc := &PipelineContext{
		MigrationID: 1,
		TargetSSH:   ssh,
		Repo:        repo,
		Migration:   &Migration{ID: 1, Categories: []string{"packages", "services"}},
		OnProgress:  func(WSMessage) {},
	}

	if err := stage.Execute(context.Background(), pc); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := repo.saved["package:nginx"].VerificationState; got != VerifyInfra {
		t.Errorf("installed package = %q, want infra_verified", got)
	}
	if got := repo.saved["service:nginx"].VerificationState; got != VerifyRuntime {
		t.Errorf("active service = %q, want runtime_verified", got)
	}
}

// Items that were never applied (keep_target / skip / blocked) must stay
// unresolved — verifying them would be a false green for work never done.
func TestHealthVerificationLeavesUnappliedItemsAlone(t *testing.T) {
	repo := newStubVerifyRepo()
	repo.items = []ItemResult{
		{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecSkipped},
	}

	ssh := newMockSSH()
	ssh.addOutput("echo ok", "ok\n")
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\n")

	stage := &healthVerificationStage{repo: repo}
	pc := &PipelineContext{
		MigrationID: 1, TargetSSH: ssh, Repo: repo,
		Migration:  &Migration{ID: 1, Categories: []string{"packages"}},
		OnProgress: func(WSMessage) {},
	}
	_ = stage.Execute(context.Background(), pc)

	if _, touched := repo.saved["package:nginx"]; touched {
		t.Error("a skipped item was given a verification verdict")
	}
}

// stubVerifyRepo embeds mockPipelineRepo and records item-result writes so a
// test can assert exactly which items got which verdict.
type stubVerifyRepo struct {
	*mockPipelineRepo
	items []ItemResult
	saved map[string]ItemResult
}

func newStubVerifyRepo() *stubVerifyRepo {
	return &stubVerifyRepo{mockPipelineRepo: &mockPipelineRepo{}, saved: map[string]ItemResult{}}
}

func (s *stubVerifyRepo) GetItemResults(ctx context.Context, migrationID int) ([]ItemResult, error) {
	return s.items, nil
}

func (s *stubVerifyRepo) UpsertItemResult(ctx context.Context, migrationID int, res ItemResult) error {
	s.saved[res.ItemKey] = res
	return nil
}
