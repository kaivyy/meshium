package migration

import (
	"context"
	"testing"

	"meshium/internal/mod/server"
)

// TestPlannerPlanInterruptedNotLeftPlanned reproduces the root cause: when the
// plan WebSocket disconnects (ctx cancelled) after CreateMigration has
// committed the row but mid-collection, Plan() must not leave the migration in
// StatusPlanned with zero collect steps. That combination dead-ends the wizard
// (no completed stage to recover from, so the user can never proceed). The
// migration must be marked honestly (interrupted/failed) so recovery can act.
//
// Reuses the mockServerRepo / mockPool / mockAuthSvc / mockHostKeyStore from
// engine_test.go.
func TestPlannerPlanInterruptedNotLeftPlanned(t *testing.T) {
	repo := &mockRepo{}
	srvRepo := newMockServerRepo()
	srvRepo.AddServer(&server.Server{ID: 1, Host: "source", Port: 22, Username: "user"})
	srvRepo.AddServer(&server.Server{ID: 2, Host: "target", Port: 22, Username: "user"})

	registry := NewCategoryRegistry()
	planner := NewPlanner(registry, repo, srvRepo, newMockPool(), &mockAuthSvc{}, &mockHostKeyStore{})

	// Cancel after CreateMigration commits but before collection finishes. The
	// registry's real collectors run against mockSSH (instant), so cancel now to
	// force the interrupted path the early-return currently swallows.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = planner.Plan(ctx, PlanRequest{
		SourceServerID: 1,
		TargetServerID: 2,
		Categories:     []string{"packages"},
	}, func(WSMessage) {})

	m, _ := repo.GetMigration(1)
	if m == nil {
		t.Fatal("expected migration row to exist")
	}
	if m.Status == StatusPlanned {
		t.Fatalf("migration left as %q with no collection — wizard would dead-end; want interrupted/failed", m.Status)
	}
}
