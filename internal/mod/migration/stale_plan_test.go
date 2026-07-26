package migration

import (
	"testing"
	"time"
)

// A plan captures the source's state at plan time. If the source has changed
// since, the payload about to be written to the target no longer describes the
// source — applying it silently migrates a stale snapshot. The audit found
// this surfaced only as an FE warning, never as a block.
//
// planFreshness turns "how old is this plan" into an explicit decision, so
// execute can refuse rather than proceed on stale data.

func TestPlanFreshnessFreshWithinWindow(t *testing.T) {
	planned := time.Now().Add(-10 * time.Minute)
	got := evaluatePlanFreshness(planned, time.Now())
	if got.Stale {
		t.Errorf("a 10-minute-old plan should be fresh: %+v", got)
	}
	if got.Blocking {
		t.Error("fresh plan must not block")
	}
}

func TestPlanFreshnessWarnsBeyondSoftWindow(t *testing.T) {
	planned := time.Now().Add(-2 * time.Hour)
	got := evaluatePlanFreshness(planned, time.Now())
	if !got.Stale {
		t.Error("a 2-hour-old plan should be marked stale")
	}
	if got.Blocking {
		t.Error("2 hours should warn, not block")
	}
	if got.Reason == "" {
		t.Error("stale verdict must carry a reason")
	}
}

// Past the hard window the plan must BLOCK, not warn. This is the difference
// the audit called out: a warning lets an operator apply a day-old snapshot.
func TestPlanFreshnessBlocksBeyondHardWindow(t *testing.T) {
	planned := time.Now().Add(-30 * time.Hour)
	got := evaluatePlanFreshness(planned, time.Now())
	if !got.Blocking {
		t.Errorf("a 30-hour-old plan must block execution: %+v", got)
	}
	if got.Reason == "" {
		t.Error("blocking verdict must explain itself")
	}
}

// An unknown plan time is not evidence of freshness. Refusing to guess is the
// safe reading — but it must not hard-block legacy migrations that never
// recorded a timestamp, so it warns.
func TestPlanFreshnessUnknownTimeWarnsNotBlocks(t *testing.T) {
	got := evaluatePlanFreshness(time.Time{}, time.Now())
	if got.Blocking {
		t.Error("an unknown plan time should warn, not block (legacy migrations)")
	}
	if !got.Stale {
		t.Error("an unknown plan time must not be reported as fresh")
	}
}

// A clock skew that puts the plan in the future must not read as "infinitely
// fresh".
func TestPlanFreshnessFuturePlanIsSuspect(t *testing.T) {
	planned := time.Now().Add(2 * time.Hour)
	got := evaluatePlanFreshness(planned, time.Now())
	if !got.Stale {
		t.Error("a plan timestamped in the future is suspect, not fresh")
	}
}
