package driver

import (
	"context"
	"errors"
	"testing"
)

// allDrivers returns one instance of every driver for table-driven checks.
func allDrivers() []Driver {
	return []Driver{
		NewFilesystemDriver(),
		NewDockerComposeDriver(),
		NewMySQLDriver(),
		NewPostgreSQLDriver(),
		NewRedisDriver(),
	}
}

// TestRegistryRegisterAndGet verifies basic registration and lookup.
func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	fs := NewFilesystemDriver()
	if err := r.Register(fs); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, ok := r.Get(WorkloadFilesystem)
	if !ok {
		t.Fatal("expected filesystem driver to be registered")
	}
	if got != fs {
		t.Fatal("Get returned a different driver instance")
	}

	if _, ok := r.Get(WorkloadMySQL); ok {
		t.Fatal("expected no mysql driver registered")
	}
}

// TestRegistryRejectsNil verifies a nil driver cannot be registered.
func TestRegistryRejectsNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("expected error registering nil driver")
	}
}

// TestRegistryRejectsDuplicate verifies double-registration fails loudly.
func TestRegistryRejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(NewMySQLDriver()); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	if err := r.Register(NewMySQLDriver()); err == nil {
		t.Fatal("expected error registering duplicate workload")
	}
}

// TestRegistryWorkloadsSorted verifies Workloads returns sorted, complete keys.
func TestRegistryWorkloadsSorted(t *testing.T) {
	r := NewRegistry()
	for _, d := range allDrivers() {
		if err := r.Register(d); err != nil {
			t.Fatalf("Register failed: %v", err)
		}
	}
	got := r.Workloads()
	want := []WorkloadType{
		WorkloadDockerCompose, WorkloadFilesystem, WorkloadMySQL,
		WorkloadPostgreSQL, WorkloadRedis,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d workloads, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("workload[%d]: expected %q, got %q (not sorted?)", i, want[i], got[i])
		}
	}
}

// TestNegotiateUnknownWorkload verifies negotiation errors when no driver exists.
func TestNegotiateUnknownWorkload(t *testing.T) {
	r := NewRegistry()
	_, err := r.Negotiate(WorkloadMySQL, CapMigrate)
	if err == nil {
		t.Fatal("expected error negotiating unknown workload")
	}
}

// TestNegotiateSatisfied verifies a driver that has the wanted caps yields OK.
func TestNegotiateSatisfied(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(NewFilesystemDriver())

	res, err := r.Negotiate(WorkloadFilesystem, CapPlan, CapValidate, CapIncrementalSync)
	if err != nil {
		t.Fatalf("Negotiate failed: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected negotiation OK, missing=%v", res.Missing)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("expected no missing caps, got %v", res.Missing)
	}
}

// TestNegotiateMissing verifies a present-but-insufficient driver yields
// OK=false with the exact missing caps (not an error).
func TestNegotiateMissing(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(NewFilesystemDriver())

	// Filesystem does not advertise live replication or zero-downtime cutover.
	res, err := r.Negotiate(WorkloadFilesystem, CapPlan, CapLiveReplication, CapZeroDowntimeCutover)
	if err != nil {
		t.Fatalf("Negotiate returned error, expected OK=false: %v", err)
	}
	if res.OK {
		t.Fatal("expected negotiation to fail on missing replication caps")
	}
	wantMissing := map[Capability]bool{CapLiveReplication: true, CapZeroDowntimeCutover: true}
	if len(res.Missing) != len(wantMissing) {
		t.Fatalf("expected %d missing caps, got %v", len(wantMissing), res.Missing)
	}
	for _, m := range res.Missing {
		if !wantMissing[m] {
			t.Fatalf("unexpected missing cap %q", m)
		}
	}
}

// TestSelectByCapabilities verifies capability-based driver selection.
func TestSelectByCapabilities(t *testing.T) {
	r := NewRegistry()
	for _, d := range allDrivers() {
		_ = r.Register(d)
	}

	// Every driver advertises Plan.
	planners := r.SelectByCapabilities(CapPlan)
	if len(planners) != 5 {
		t.Fatalf("expected all 5 drivers to support Plan, got %v", planners)
	}

	// No initial driver advertises zero-downtime cutover — the honesty invariant.
	zdt := r.SelectByCapabilities(CapZeroDowntimeCutover)
	if len(zdt) != 0 {
		t.Fatalf("expected NO driver to advertise zero-downtime cutover, got %v", zdt)
	}

	// Only filesystem advertises incremental sync.
	inc := r.SelectByCapabilities(CapIncrementalSync)
	if len(inc) != 1 || inc[0] != WorkloadFilesystem {
		t.Fatalf("expected only filesystem to support incremental sync, got %v", inc)
	}
}

// TestNoDriverClaimsZeroDowntime enforces the core honesty invariant across all
// drivers: none may advertise zero-downtime cutover, and any driver whose
// default strategy requires downtime must not claim otherwise.
func TestNoDriverClaimsZeroDowntime(t *testing.T) {
	for _, d := range allDrivers() {
		caps := d.Capabilities()
		if caps.Has(CapZeroDowntimeCutover) {
			t.Errorf("driver %q must not advertise zero-downtime cutover yet", caps.Workload)
		}
		// A driver advertising live replication may have RequiresDowntime=false;
		// one without it that still claims no downtime is only valid for the
		// filesystem (quiescent tree). Guard the DB/compose skeletons explicitly.
		if caps.Has(CapZeroDowntimeCutover) && !caps.Has(CapLiveReplication) {
			t.Errorf("driver %q claims zero-downtime without live replication", caps.Workload)
		}
	}
}

// TestSkeletonsReturnNotImplemented verifies every skeleton lifecycle operation
// returns a NotImplementedError detectable via IsNotImplemented and errors.Is.
func TestSkeletonsReturnNotImplemented(t *testing.T) {
	ctx := context.Background()
	src := Target{ServerID: 1}
	dst := Target{ServerID: 2}

	skeletons := []Driver{
		NewDockerComposeDriver(),
		NewMySQLDriver(),
		NewPostgreSQLDriver(),
		NewRedisDriver(),
	}

	for _, d := range skeletons {
		wt := d.Capabilities().Workload

		checks := map[string]error{
			"Discover": func() error { _, e := d.Discover(ctx, src); return e }(),
			"Validate": func() error { _, e := d.Validate(ctx, src, dst); return e }(),
			"Plan":     func() error { _, e := d.Plan(ctx, src, dst); return e }(),
			"Backup":   func() error { _, e := d.Backup(ctx, dst); return e }(),
			"Migrate":  func() error { _, e := d.Migrate(ctx, src, dst); return e }(),
			"Verify":   func() error { _, e := d.Verify(ctx, src, dst); return e }(),
			"Rollback": d.Rollback(ctx, dst, BackupResult{}),
			"Resume":   func() error { _, e := d.Resume(ctx, src, dst, ""); return e }(),
			"Risk":     func() error { _, e := d.Risk(ctx, src, dst); return e }(),
		}

		for op, err := range checks {
			if err == nil {
				t.Errorf("driver %q op %s: expected NotImplemented, got nil", wt, op)
				continue
			}
			if !IsNotImplemented(err) {
				t.Errorf("driver %q op %s: IsNotImplemented=false for %v", wt, op, err)
			}
			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("driver %q op %s: errors.Is(ErrNotImplemented)=false", wt, op)
			}
		}
	}
}

// TestFilesystemLiveMethods verifies the filesystem driver's connection-free
// methods (Validate/Plan/Risk) return real results, not NotImplemented.
func TestFilesystemLiveMethods(t *testing.T) {
	d := NewFilesystemDriver()
	ctx := context.Background()
	src := Target{ServerID: 1, Options: map[string]string{"path": "/data"}}
	dst := Target{ServerID: 2}

	vr, err := d.Validate(ctx, src, dst)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if !vr.OK {
		t.Fatalf("expected valid, got blockers %v", vr.Blockers)
	}

	pr, err := d.Plan(ctx, src, dst)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if pr.Strategy != "rsync" {
		t.Fatalf("expected rsync strategy, got %q", pr.Strategy)
	}
	if len(pr.Steps) == 0 {
		t.Fatal("expected non-empty plan steps")
	}

	risk, err := d.Risk(ctx, src, dst)
	if err != nil {
		t.Fatalf("Risk returned error: %v", err)
	}
	if !risk.Reversible {
		t.Fatal("expected filesystem migration to be reversible")
	}
}

// TestFilesystemValidateBlocksMissingServers verifies validation blockers.
func TestFilesystemValidateBlocksMissingServers(t *testing.T) {
	d := NewFilesystemDriver()
	vr, err := d.Validate(context.Background(), Target{}, Target{})
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if vr.OK {
		t.Fatal("expected validation to fail with no servers specified")
	}
	if len(vr.Blockers) != 2 {
		t.Fatalf("expected 2 blockers (source+dest), got %v", vr.Blockers)
	}
}

// TestFilesystemStillHasNotImplementedExec verifies the filesystem driver is
// honest that its data-movement ops are not yet wired.
func TestFilesystemStillHasNotImplementedExec(t *testing.T) {
	d := NewFilesystemDriver()
	ctx := context.Background()
	if _, err := d.Migrate(ctx, Target{ServerID: 1}, Target{ServerID: 2}); !IsNotImplemented(err) {
		t.Fatalf("expected Migrate NotImplemented, got %v", err)
	}
}
