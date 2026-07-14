package migration

import (
	"context"
	"strings"
	"testing"
	"time"

	"meshium/internal/mod/discovery"
)

// TestPlanCategoryMetaRefusesReuseWhenSnapshotDiverges proves the freshness-
// aware gate never silently reuses an onboarding snapshot whose shape does not
// faithfully match what a category's applier consumes. Today every category is
// refused (the honest "discovery runs again" answer), each with a concrete
// reason — not a silent skip, not a fake match.
func TestPlanCategoryMetaRefusesReuseWhenSnapshotDiverges(t *testing.T) {
	p := &Planner{snapStore: &fakeReuseStore{}}
	cats := []string{"packages", "services", "database", "users", "docker", "configs"}
	for _, cat := range cats {
		m := p.planCategoryMeta(context.Background(), 1, cat, false)
		if m.CollectionStrategy != CollectRefresh {
			t.Errorf("%s: expected refresh (reuse refused), got %s", cat, m.CollectionStrategy)
		}
		if m.ReuseSource != ReuseNone {
			t.Errorf("%s: expected reuseSource none, got %s", cat, m.ReuseSource)
		}
		if len(m.Warnings) == 0 {
			t.Errorf("%s: expected a refusal reason in Warnings, got none", cat)
		}
	}
}

// TestPlanCategoryMetaNoStoreCollectsLive documents that with no snapshot
// store configured the planner collects live and says so — it does not fabricate
// a reuse.
func TestPlanCategoryMetaNoStoreCollectsLive(t *testing.T) {
	p := &Planner{snapStore: nil}
	m := p.planCategoryMeta(context.Background(), 1, "packages", false)
	if m.CollectionStrategy != CollectRefresh || m.ReuseSource != ReuseNone {
		t.Errorf("no-store path should be refresh/none, got %s/%s", m.CollectionStrategy, m.ReuseSource)
	}
	if len(m.Warnings) == 0 {
		t.Errorf("expected a warning explaining live collection")
	}
}

// TestPlanCategoryMetaFreshnessWindow proves a snapshot older than the freshness
// window is rejected even when its fields would otherwise be usable.
func TestPlanCategoryMetaFreshnessWindow(t *testing.T) {
	old := time.Now().Add(-2 * planReuseMaxAge)
	store := &fakeReuseStore{snap: &discovery.ServerSnapshot{CapturedAt: old}}
	p := &Planner{snapStore: store}
	m := p.planCategoryMeta(context.Background(), 1, "packages", false)
	if m.CollectionStrategy != CollectRefresh {
		t.Errorf("stale snapshot must be refreshed, got %s", m.CollectionStrategy)
	}
	if len(m.Warnings) == 0 || !strings.Contains(m.Warnings[0], "old") {
		t.Errorf("expected stale-window warning, got %v", m.Warnings)
	}
}

// TestZeroDowntimeCapableFalse documents the honest global gate: no category may
// claim zero_downtime until the live-replication + freeze + traffic-switch +
// observation chain is genuinely wired.
func TestZeroDowntimeCapableFalse(t *testing.T) {
	p := &Planner{}
	if p.zeroDowntimeCapable() {
		t.Errorf("zero-downtime must be false until the chain is wired")
	}
	// DowntimeClassFor must therefore never return zero for any category yet.
	for _, cat := range []string{"database", "docker", "packages", "configs", "services", "users"} {
		if DowntimeClassFor(cat, p.zeroDowntimeCapable()) == DowntimeZero {
			t.Errorf("%s must not claim zero_downtime while capability gate is false", cat)
		}
	}
}

// --- fakes ---

type fakeReuseStore struct {
	snap *discovery.ServerSnapshot
	err  error
}

func (s *fakeReuseStore) SaveSnapshot(int, *discovery.ServerSnapshot) error { return nil }
func (s *fakeReuseStore) LoadSnapshot(int) (*discovery.ServerSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.snap == nil {
		return nil, errNoSnapshot
	}
	return s.snap, nil
}
func (s *fakeReuseStore) LoadSnapshotAt(int, time.Time) (*discovery.ServerSnapshot, error) {
	return s.LoadSnapshot(0)
}
func (s *fakeReuseStore) DeleteSnapshot(int) error { return nil }

var errNoSnapshot = &storeErr{"no snapshot"}

type storeErr struct{ msg string }

func (e *storeErr) Error() string { return e.msg }
