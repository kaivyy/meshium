package gsync

import (
	"context"
	"testing"
	"time"
)

// A pair that has never run is due immediately; a disabled one never is.
// After a successful run inside the interval, Tick must be a no-op.
func TestTickRunsOnlyDuePairs(t *testing.T) {
	s := newTestService(t)
	fr := s.useRunner(t)
	_ = s.SaveToken(context.Background(), "{}")
	p, _ := s.UpsertPair(context.Background(), Pair{Name: "due", LocalPath: "/a", RemotePath: "b", Enabled: true, IntervalHours: 1})

	if n := s.Tick(context.Background()); n != 1 {
		t.Fatalf("first Tick ran %d pairs, want 1", n)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("runner called %d times, want 1", len(fr.calls))
	}

	// Fresh interval => not due.
	if n := s.Tick(context.Background()); n != 0 {
		t.Fatalf("second Tick ran %d pairs, want 0", n)
	}

	// Backdate the last run past the interval => due again.
	s.mu.Lock()
	all := map[string]RunResult{s.lastRun(t, p.ID).StartedAt.Format(time.RFC3339): {}}
	_ = all
	last := s.lastRun(t, p.ID)
	last.StartedAt = time.Now().Add(-2 * time.Hour)
	b, _ := marshal(last)
	_ = s.store.SetConfigValue(lastRunKey, string(b))
	s.mu.Unlock()

	if n := s.Tick(context.Background()); n != 1 {
		t.Fatalf("Tick after interval elapsed ran %d pairs, want 1", n)
	}
}

func marshal(v any) ([]byte, error) { return jsonMarshal(v) }
