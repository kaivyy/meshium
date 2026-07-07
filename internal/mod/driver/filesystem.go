package driver

import "context"

// FilesystemDriver migrates a filesystem tree (a directory and its contents).
// It is the most complete of the initial drivers: its planning, validation, and
// risk reasoning are fully implemented because they need no live connection.
// The data-movement operations (Discover/Backup/Migrate/Verify/Rollback/Resume)
// return NotImplementedError until the transport wiring lands — the driver is
// honest about this via Capabilities().Notes rather than faking success.
//
// Its intended strategy is rsync, which supports incremental sync and checksum
// verification but is not live replication, so RequiresDowntime is false for a
// quiescent tree yet the driver does NOT advertise zero-downtime cutover.
type FilesystemDriver struct{}

// NewFilesystemDriver constructs a FilesystemDriver.
func NewFilesystemDriver() *FilesystemDriver { return &FilesystemDriver{} }

// Capabilities advertises the filesystem driver's abilities. The reasoning
// methods (validate/plan/risk) are live; the execution methods are declared
// here as the driver's *intended* capability set so a planner can select it,
// with Notes stating that execution is not yet wired.
func (d *FilesystemDriver) Capabilities() Capabilities {
	return Capabilities{
		Workload: WorkloadFilesystem,
		Supported: []Capability{
			CapValidate,
			CapPlan,
			CapIncrementalSync,
			CapChecksumVerify,
		},
		DefaultStrategy:  "rsync",
		RequiresDowntime: false,
		Notes: "Planning, validation, and risk are implemented. Data movement " +
			"(discover/backup/migrate/verify/rollback/resume) is not yet wired to " +
			"the transport layer and returns NotImplemented. rsync is not live " +
			"replication, so this driver does not offer zero-downtime cutover.",
	}
}

// Validate performs the connection-independent precondition checks: a source
// and destination server must be identified. Path-level and permission checks
// require a live connection and are deferred to the wired implementation.
func (d *FilesystemDriver) Validate(ctx context.Context, src, dst Target) (ValidateResult, error) {
	res := ValidateResult{OK: true}
	if src.ServerID == 0 {
		res.OK = false
		res.Blockers = append(res.Blockers, "source server not specified")
	}
	if dst.ServerID == 0 {
		res.OK = false
		res.Blockers = append(res.Blockers, "destination server not specified")
	}
	if src.ServerID == dst.ServerID && src.ServerID != 0 {
		res.Warnings = append(res.Warnings, "source and destination are the same server")
	}
	if _, ok := src.Options["path"]; !ok {
		res.Warnings = append(res.Warnings, "no source path specified; will default to full tree at migrate time")
	}
	return res, nil
}

// Plan produces the rsync-based migration plan. It is deterministic and needs
// no connection, so a planner can obtain it during selection.
func (d *FilesystemDriver) Plan(ctx context.Context, src, dst Target) (PlanResult, error) {
	return PlanResult{
		Workload: WorkloadFilesystem,
		Strategy: "rsync",
		Steps: []string{
			"snapshot source tree metadata",
			"rsync source tree to target (archive mode, preserve perms/owners)",
			"incremental rsync to catch changes",
			"checksum-verify transferred files",
		},
		RequiresDowntime: false,
		EstDowntime:      "none for a quiescent tree; brief if source is actively written",
		ManualSteps: []string{
			"ensure the source tree is quiescent (or accept eventual-consistency) during the final sync",
		},
	}, nil
}

// Risk self-assesses filesystem migration risk. rsync is reversible (the target
// is only written, the source untouched) and checksum-verifiable, so baseline
// risk is low.
func (d *FilesystemDriver) Risk(ctx context.Context, src, dst Target) (RiskAssessment, error) {
	return RiskAssessment{
		Score:        20,
		Level:        "low",
		Factors:      []string{"rsync leaves source intact", "checksum verification available"},
		Reversible:   true,
		DataLossRisk: false,
	}, nil
}

// Discover requires a live connection to inspect the source tree.
func (d *FilesystemDriver) Discover(ctx context.Context, src Target) (DiscoverResult, error) {
	return DiscoverResult{}, NewNotImplemented(WorkloadFilesystem, CapDiscover)
}

// Backup requires a live connection to capture the target tree.
func (d *FilesystemDriver) Backup(ctx context.Context, dst Target) (BackupResult, error) {
	return BackupResult{}, NewNotImplemented(WorkloadFilesystem, CapBackup)
}

// Migrate requires the wired transport layer.
func (d *FilesystemDriver) Migrate(ctx context.Context, src, dst Target) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadFilesystem, CapMigrate)
}

// Verify requires a live connection to checksum the target.
func (d *FilesystemDriver) Verify(ctx context.Context, src, dst Target) (VerifyResult, error) {
	return VerifyResult{}, NewNotImplemented(WorkloadFilesystem, CapVerify)
}

// Rollback requires the wired transport layer.
func (d *FilesystemDriver) Rollback(ctx context.Context, dst Target, backup BackupResult) error {
	return NewNotImplemented(WorkloadFilesystem, CapRollback)
}

// Resume requires the wired transport layer.
func (d *FilesystemDriver) Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadFilesystem, CapResume)
}
