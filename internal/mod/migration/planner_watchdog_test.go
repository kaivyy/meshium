package migration

import (
	"context"
	"testing"
	"time"

	"meshium/internal/mod/server"
)

// wedgingCollector blocks inside Collect until the context is cancelled. It
// models the real-world bug (configs scanning /etc can hang on a wedged SSH
// session): without a collection deadline, Plan() would block forever on
// wg.Wait() and never emit the terminal frame, hanging the wizard.
type wedgingCollector struct{}

func (wedgingCollector) Collect(ctx context.Context, _ SSHExecuter) (CategoryData, error) {
	<-ctx.Done()
	return CategoryData{}, ctx.Err()
}

func (wedgingCollector) Backup(context.Context, SSHExecuter) (BackupData, error) { return BackupData{}, nil }
func (wedgingCollector) Apply(context.Context, SSHExecuter, CategoryData, StepCallback) error {
	return nil
}
func (wedgingCollector) Rollback(context.Context, SSHExecuter, BackupData) error { return nil }

// TestPlanReturnsOnWedgedCollector proves the collection phase is deadline-
// bounded: a category collector that never returns on its own cannot hang the
// whole plan. The wedging configs collector blocks until its context is
// cancelled; the shared collectCtx must expire it (via planCollectTimeout) and
// unblock Plan() — otherwise the wizard would spin forever. We shorten the
// timeout to keep the test fast.
func TestPlanReturnsOnWedgedCollector(t *testing.T) {
	orig := planCollectTimeout
	planCollectTimeout = 50 * time.Millisecond
	defer func() { planCollectTimeout = orig }()

	repo := &mockRepo{}
	srvRepo := newMockServerRepo()
	srvRepo.AddServer(&server.Server{ID: 1, Host: "src", Port: 22, Username: "u"})
	srvRepo.AddServer(&server.Server{ID: 2, Host: "dst", Port: 22, Username: "u"})
	pool := newMockPool()

	// Real registry, then swap the configs (applier-preserving) module for a
	// wedging collector — this isolates the guard from the other collectors.
	registry := NewCategoryRegistry()
	cfg, _ := registry.Get("configs")
	registry.Register("configs", wedgingCollector{}, cfg.Applier)

	p := NewPlanner(registry, repo, srvRepo, pool, &mockAuthSvc{}, &mockHostKeyStore{})

	// Live parent context: the guard must come from planCollectTimeout firing,
	// not from a pre-cancelled parent.
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := p.Plan(ctx, PlanRequest{
			SourceServerID: 1, TargetServerID: 2,
			Categories:     []string{"packages", "configs", "services"},
		}, nil)
		done <- err
	}()

	select {
	case <-done:
		// Plan() returned — the collection deadline bounded the wedged collector.
	case <-time.After(3 * time.Second):
		t.Fatal("Plan() hung on a wedged collector — collection phase is not deadline-bounded")
	}
}
