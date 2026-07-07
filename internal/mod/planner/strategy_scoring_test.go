package planner

import (
	"strings"
	"testing"

	"meshium/internal/mod/driver"
)

// TestSelectStrategyScored_DatabasePrefersReplication verifies that for a DB
// engine with an implemented replication strategy, replication is selected and
// the dump is present but rejected as non-executable.
func TestSelectStrategyScored_DatabasePrefersReplication(t *testing.T) {
	for _, wt := range []driver.WorkloadType{driver.WorkloadMySQL, driver.WorkloadPostgreSQL, driver.WorkloadRedis} {
		d := SelectStrategyScored(SelectorConstraints{Workload: wt, CanRollback: true})
		if d.Selected.Strategy != StrategyDatabaseReplication {
			t.Errorf("%q: expected replication selected, got %q", wt, d.Selected.Strategy)
		}
		// The dump candidate must appear rejected as non-executable.
		var sawDumpRejected bool
		for _, r := range d.Rejected {
			if r.Strategy == StrategyColdMigration || r.Strategy == StrategyWarmMigration {
				sawDumpRejected = true
				if !strings.Contains(r.RejectReason, "no orchestrated execution path") {
					t.Errorf("%q: dump rejected for wrong reason: %q", wt, r.RejectReason)
				}
			}
		}
		if !sawDumpRejected {
			t.Errorf("%q: expected a rejected dump candidate", wt)
		}
	}
}

// TestSelectStrategyScored_NeverZeroDowntime is the core Tahap 7 honesty
// invariant: no selected or rejected candidate may advertise zero downtime.
func TestSelectStrategyScored_NeverZeroDowntime(t *testing.T) {
	inputs := []SelectorConstraints{
		{Workload: driver.WorkloadMySQL, CanRollback: true, RPOSeconds: 30},
		{Workload: driver.WorkloadPostgreSQL, DataSizeGB: 250},
		{Workload: driver.WorkloadRedis},
		{Workload: driver.WorkloadFilesystem},
		{Workload: driver.WorkloadDockerCompose},
		{Workload: driver.WorkloadType("unknown")},
	}
	for _, in := range inputs {
		d := SelectStrategyScored(in)
		all := append([]StrategyCandidate{d.Selected}, d.Rejected...)
		for _, c := range all {
			dt := strings.ToLower(c.EstimatedDowntime)
			if strings.Contains(dt, "zero") || dt == "~0s" || strings.Contains(dt, "none") {
				t.Errorf("%q/%s: downtime %q implies zero-downtime", in.Workload, c.Strategy, c.EstimatedDowntime)
			}
		}
	}
}

// TestSelectStrategyScored_DowntimeBudgetRejectsAll verifies that a budget below
// the briefest available cutover rejects every candidate and yields no selection
// with an honest reason.
func TestSelectStrategyScored_DowntimeBudgetRejectsAll(t *testing.T) {
	d := SelectStrategyScored(SelectorConstraints{
		Workload:              driver.WorkloadMySQL,
		DowntimeBudgetSeconds: 5, // below replicationCutoverSeconds
	})
	if d.Selected.Strategy != "" {
		t.Fatalf("expected no selection under impossible budget, got %q", d.Selected.Strategy)
	}
	if !strings.Contains(d.Reasoning, "no executable strategy") {
		t.Fatalf("expected honest no-fit reasoning, got %q", d.Reasoning)
	}
	var sawBudgetReject bool
	for _, r := range d.Rejected {
		if strings.Contains(r.RejectReason, "exceeds budget") {
			sawBudgetReject = true
		}
	}
	if !sawBudgetReject {
		t.Fatal("expected a candidate rejected for exceeding the downtime budget")
	}
}

// TestSelectStrategyScored_BudgetAllowsReplication verifies replication is
// selected when the budget comfortably exceeds its brief cutover.
func TestSelectStrategyScored_BudgetAllowsReplication(t *testing.T) {
	d := SelectStrategyScored(SelectorConstraints{
		Workload:              driver.WorkloadPostgreSQL,
		DowntimeBudgetSeconds: 120,
		CanRollback:           true,
	})
	if d.Selected.Strategy != StrategyDatabaseReplication {
		t.Fatalf("expected replication under a generous budget, got %q", d.Selected.Strategy)
	}
}

// TestSelectStrategyScored_Filesystem verifies the filesystem workload selects
// rsync live-sync as an executable, brief-downtime strategy.
func TestSelectStrategyScored_Filesystem(t *testing.T) {
	d := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadFilesystem, CanRollback: true})
	if d.Selected.Strategy != StrategyLiveSync {
		t.Fatalf("expected live_sync for filesystem, got %q", d.Selected.Strategy)
	}
	if d.Selected.Rejected {
		t.Fatal("filesystem live_sync should be executable, not rejected")
	}
}

// TestSelectStrategyScored_UnknownWorkloadManual verifies an unrecognized
// workload falls back to a non-executable manual cutover with no selection.
func TestSelectStrategyScored_UnknownWorkloadManual(t *testing.T) {
	d := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadType("mystery")})
	if d.Selected.Strategy != "" {
		t.Fatalf("expected no executable selection for unknown workload, got %q", d.Selected.Strategy)
	}
	if len(d.Rejected) != 1 || d.Rejected[0].Strategy != StrategyManualCutover {
		t.Fatalf("expected a single manual-cutover rejected candidate, got %+v", d.Rejected)
	}
}

// TestSelectStrategyScored_Deterministic verifies repeated calls with identical
// input yield identical decisions (no map-iteration nondeterminism).
func TestSelectStrategyScored_Deterministic(t *testing.T) {
	in := SelectorConstraints{Workload: driver.WorkloadMySQL, DataSizeGB: 50, BandwidthMbps: 500, CanRollback: true, RPOSeconds: 30}
	first := SelectStrategyScored(in)
	for i := 0; i < 20; i++ {
		got := SelectStrategyScored(in)
		if got.Selected.Strategy != first.Selected.Strategy || got.Selected.Score != first.Selected.Score {
			t.Fatalf("nondeterministic selection: %q(%d) vs %q(%d)",
				got.Selected.Strategy, got.Selected.Score, first.Selected.Strategy, first.Selected.Score)
		}
		if len(got.Rejected) != len(first.Rejected) {
			t.Fatalf("nondeterministic rejected count: %d vs %d", len(got.Rejected), len(first.Rejected))
		}
	}
}

// TestSelectStrategyScored_RollbackAndRPOAffectScore verifies the scoring levers
// move in the documented direction: rollback availability and a tight RPO both
// raise the replication score.
func TestSelectStrategyScored_RollbackAndRPOAffectScore(t *testing.T) {
	base := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadMySQL})
	withRollback := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadMySQL, CanRollback: true})
	withRPO := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadMySQL, CanRollback: true, RPOSeconds: 30})

	if withRollback.Selected.Score <= base.Selected.Score {
		t.Errorf("rollback should raise score: base=%d withRollback=%d", base.Selected.Score, withRollback.Selected.Score)
	}
	if withRPO.Selected.Score <= withRollback.Selected.Score {
		t.Errorf("tight RPO should raise replication score: withRollback=%d withRPO=%d", withRollback.Selected.Score, withRPO.Selected.Score)
	}
}

// TestSelectStrategyScored_LargeDumpIsWarm verifies the dump candidate for a
// large database is labelled warm migration (still non-executable).
func TestSelectStrategyScored_LargeDumpIsWarm(t *testing.T) {
	d := SelectStrategyScored(SelectorConstraints{Workload: driver.WorkloadPostgreSQL, DataSizeGB: 250})
	var sawWarm bool
	for _, r := range d.Rejected {
		if r.Strategy == StrategyWarmMigration {
			sawWarm = true
		}
		if r.Strategy == StrategyColdMigration {
			t.Error("large database dump should be warm, not cold")
		}
	}
	if !sawWarm {
		t.Fatal("expected a warm-migration dump candidate for a large database")
	}
}
