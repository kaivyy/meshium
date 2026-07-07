package driver

import "testing"

// TestDBStrategiesForKnownWorkloads verifies the catalog returns strategies for
// each database workload and nothing for non-database workloads.
func TestDBStrategiesForKnownWorkloads(t *testing.T) {
	for _, wt := range []WorkloadType{WorkloadMySQL, WorkloadPostgreSQL, WorkloadRedis} {
		if got := DBStrategiesFor(wt); len(got) == 0 {
			t.Errorf("expected strategies for %q, got none", wt)
		}
	}
	for _, wt := range []WorkloadType{WorkloadFilesystem, WorkloadDockerCompose, WorkloadType("unknown")} {
		if got := DBStrategiesFor(wt); got != nil {
			t.Errorf("expected no db strategies for %q, got %v", wt, got)
		}
	}
}

// TestDBStrategiesForReturnsCopy verifies the accessor hands back a copy so a
// caller cannot mutate the shared catalog.
func TestDBStrategiesForReturnsCopy(t *testing.T) {
	a := DBStrategiesFor(WorkloadMySQL)
	if len(a) == 0 {
		t.Fatal("expected mysql strategies")
	}
	a[0].Name = "mutated"
	b := DBStrategiesFor(WorkloadMySQL)
	if b[0].Name == "mutated" {
		t.Fatal("mutating the returned slice leaked into the catalog")
	}
}

// TestSelectDBStrategyPicksImplemented verifies selection returns the
// implemented, brief-downtime replication strategy for each DB workload — never
// a descriptor or future one.
func TestSelectDBStrategyPicksImplemented(t *testing.T) {
	cases := map[WorkloadType]string{
		WorkloadMySQL:      "mysql_binlog_replication",
		WorkloadPostgreSQL: "postgres_streaming_replication",
		WorkloadRedis:      "redis_replica_handoff",
	}
	for wt, wantName := range cases {
		got, ok := SelectDBStrategy(wt)
		if !ok {
			t.Errorf("%q: expected an implemented strategy, got none", wt)
			continue
		}
		if got.Name != wantName {
			t.Errorf("%q: expected %q, got %q", wt, wantName, got.Name)
		}
		if got.Maturity != MaturityImplemented {
			t.Errorf("%q: selected non-implemented strategy %q (maturity %q)", wt, got.Name, got.Maturity)
		}
		if got.Downtime != DowntimeBrief {
			t.Errorf("%q: expected brief-downtime selection, got %q", wt, got.Downtime)
		}
	}
}

// TestSelectDBStrategyNoneForNonDB verifies workloads with no implemented DB
// strategy report ok=false rather than fabricating a selection.
func TestSelectDBStrategyNoneForNonDB(t *testing.T) {
	for _, wt := range []WorkloadType{WorkloadFilesystem, WorkloadDockerCompose, WorkloadType("unknown")} {
		if _, ok := SelectDBStrategy(wt); ok {
			t.Errorf("%q: expected no implemented db strategy, got one", wt)
		}
	}
}

// TestNoDBStrategyIsZeroDowntime is the core honesty invariant for Tahap 6:
// no catalogued database strategy may claim zero downtime. Every implemented
// strategy is replication-based (brief cutover, RequiresDowntime=true) and
// every dump strategy needs full downtime.
func TestNoDBStrategyIsZeroDowntime(t *testing.T) {
	for wt := range dbStrategyCatalog {
		for _, s := range DBStrategiesFor(wt) {
			// No strategy — of any maturity — may report as zero-downtime.
			if s.IsZeroDowntime() {
				t.Errorf("strategy %q (%q) must not claim zero downtime", s.Name, s.Workload)
			}
			// Strategies with an actual or described path must set the honest
			// downtime flag. Future strategies intentionally leave it unset
			// because their behavior is not yet pinned down, and IsZeroDowntime
			// already refuses to treat them as zero-downtime.
			if s.Maturity != MaturityFuture && !s.RequiresDowntime {
				t.Errorf("strategy %q (%q) has RequiresDowntime=false; no strategy is zero-downtime yet", s.Name, s.Workload)
			}
		}
	}
}

// TestDumpStrategiesAreDescriptorOnly verifies dump-based strategies are not
// selectable for execution: they must be DESCRIPTOR maturity and full-downtime.
func TestDumpStrategiesAreDescriptorOnly(t *testing.T) {
	dumps := map[WorkloadType]string{
		WorkloadMySQL:      "mysqldump",
		WorkloadPostgreSQL: "pg_dump_restore",
		WorkloadRedis:      "redis_rdb_snapshot",
	}
	for wt, name := range dumps {
		var found bool
		for _, s := range DBStrategiesFor(wt) {
			if s.Name != name {
				continue
			}
			found = true
			if s.Maturity != MaturityDescriptor {
				t.Errorf("%q: expected descriptor maturity, got %q", name, s.Maturity)
			}
			if s.Downtime != DowntimeFull {
				t.Errorf("%q: expected full downtime, got %q", name, s.Downtime)
			}
		}
		if !found {
			t.Errorf("expected dump strategy %q in %q catalog", name, wt)
		}
	}
}

// TestImplementedStrategiesMatchDriverDowntimeHonesty ties the catalog back to
// the driver capability model: the DB drivers advertise RequiresDowntime=true
// and do not advertise zero-downtime cutover, and the catalog agrees.
func TestImplementedStrategiesMatchDriverDowntimeHonesty(t *testing.T) {
	drivers := map[WorkloadType]Driver{
		WorkloadMySQL:      NewMySQLDriver(),
		WorkloadPostgreSQL: NewPostgreSQLDriver(),
		WorkloadRedis:      NewRedisDriver(),
	}
	for wt, d := range drivers {
		caps := d.Capabilities()
		if !caps.RequiresDowntime {
			t.Errorf("%q driver claims no downtime; catalog has no zero-downtime strategy", wt)
		}
		if caps.Has(CapZeroDowntimeCutover) {
			t.Errorf("%q driver advertises zero-downtime cutover; catalog contradicts it", wt)
		}
		if _, ok := SelectDBStrategy(wt); !ok {
			t.Errorf("%q: catalog should offer an implemented strategy for a real DB driver", wt)
		}
	}
}
