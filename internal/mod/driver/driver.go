// Package driver defines the Workload Driver SDK: a workload-level abstraction
// over the migration lifecycle.
//
// Meshium's existing migration package operates at two granularities — per
// OS-resource categories (packages/configs/services/users/docker via the
// Collector/Applier pair) and a fixed 14-stage zero-downtime Pipeline. Neither
// models a *workload* (a filesystem tree, a Docker Compose project, a MySQL
// instance, a Redis instance) as a first-class unit with its own discovery,
// backup, migrate, verify, and rollback semantics.
//
// This package fills that gap. A Driver encapsulates everything specific to one
// workload type behind a small, uniform interface, and a Registry lets a
// planner discover drivers and negotiate against their advertised Capabilities.
//
// Design constraints:
//   - Stdlib-only. This package must not import meshium/internal/mod/migration
//     (a future planner may import driver, so the dependency must point one
//     way). Keeping it dependency-free also guarantees it cannot break the
//     existing pipeline: nothing imports it yet.
//   - Honest skeletons. A driver that cannot yet perform an operation returns a
//     NotImplementedError rather than silently succeeding or faking work. The
//     capability model advertises up front what a driver can and cannot do, so
//     a planner never has to call an operation to discover it is unsupported.
package driver

import (
	"context"
	"errors"
	"fmt"
)

// Capability names a discrete ability a driver may support. A driver advertises
// the set it supports via Capabilities(); a planner negotiates against that set
// before selecting a driver, so unsupported operations are known before they
// are attempted rather than discovered via a runtime error.
type Capability string

const (
	// CapDiscover: the driver can inspect a source and report what it found.
	CapDiscover Capability = "discover"
	// CapValidate: the driver can check source/target preconditions.
	CapValidate Capability = "validate"
	// CapPlan: the driver can produce a migration plan for its workload.
	CapPlan Capability = "plan"
	// CapBackup: the driver can capture a restorable backup before migrating.
	CapBackup Capability = "backup"
	// CapMigrate: the driver can perform the data movement itself.
	CapMigrate Capability = "migrate"
	// CapVerify: the driver can verify the migrated workload on the target.
	CapVerify Capability = "verify"
	// CapRollback: the driver can undo a migration using its backup.
	CapRollback Capability = "rollback"
	// CapResume: the driver can resume an interrupted migration from a checkpoint.
	CapResume Capability = "resume"
	// CapLiveReplication: the driver supports continuous replication (as opposed
	// to a one-shot dump/copy), which is the prerequisite for low/zero downtime
	// cutover. Absence of this capability means any migration is dump-based and
	// therefore incurs downtime — callers must not claim zero-downtime.
	CapLiveReplication Capability = "live_replication"
	// CapZeroDowntimeCutover: the driver can switch traffic to the target with
	// effectively no downtime. Implies CapLiveReplication in practice.
	CapZeroDowntimeCutover Capability = "zero_downtime_cutover"
	// CapIncrementalSync: the driver can sync only changes after an initial copy.
	CapIncrementalSync Capability = "incremental_sync"
	// CapChecksumVerify: the driver can verify integrity via checksums.
	CapChecksumVerify Capability = "checksum_verify"
)

// WorkloadType identifies the kind of workload a driver handles. It is the key
// under which a driver registers, so values must be unique across drivers.
type WorkloadType string

const (
	WorkloadFilesystem    WorkloadType = "filesystem"
	WorkloadDockerCompose WorkloadType = "docker_compose"
	WorkloadMySQL         WorkloadType = "mysql"
	WorkloadPostgreSQL    WorkloadType = "postgresql"
	WorkloadRedis         WorkloadType = "redis"
)

// Capabilities describes what a driver can do and the honest downtime/risk
// characteristics of its default strategy. A planner reads this to select a
// driver and to decide whether a zero-downtime claim is warranted.
type Capabilities struct {
	// Workload is the workload type this driver handles.
	Workload WorkloadType `json:"workload"`
	// Supported lists the operations the driver can actually perform today.
	// Operations not listed here return a NotImplementedError.
	Supported []Capability `json:"supported"`
	// DefaultStrategy names the strategy used when no override is given
	// (e.g. "rsync", "mysqldump", "rdb_snapshot"). Empty for pure skeletons.
	DefaultStrategy string `json:"defaultStrategy,omitempty"`
	// RequiresDowntime is true when the driver's default strategy cannot avoid
	// downtime (e.g. a dump/restore). This is the single source of truth a
	// planner should consult before making any zero-downtime claim.
	RequiresDowntime bool `json:"requiresDowntime"`
	// Notes is free-form text describing limitations, honestly. Skeletons use
	// this to say what is not yet implemented.
	Notes string `json:"notes,omitempty"`
}

// Has reports whether the given capability is supported.
func (c Capabilities) Has(cap Capability) bool {
	for _, s := range c.Supported {
		if s == cap {
			return true
		}
	}
	return false
}

// Missing returns the subset of want that this driver does not support. A nil
// return means every wanted capability is supported. A planner uses this to
// explain precisely why a driver was rejected.
func (c Capabilities) Missing(want ...Capability) []Capability {
	var missing []Capability
	for _, w := range want {
		if !c.Has(w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// --- Lifecycle value types ---
//
// These are intentionally generic (maps/strings/counts) so the SDK does not
// couple to the migration package's concrete models. Drivers serialize their
// own workload-specific detail into the free-form fields.

// Target identifies where an operation runs and carries opaque, driver-specific
// options. It deliberately avoids importing server/ssh types so the SDK stays
// dependency-free; the runtime that invokes a driver supplies a connection via
// the context or a driver-specific constructor.
type Target struct {
	// ServerID is the Meshium server record this operation addresses.
	ServerID int `json:"serverId"`
	// Options carries driver-specific parameters (paths, DB names, ports, ...).
	Options map[string]string `json:"options,omitempty"`
}

// DiscoverResult reports what a driver found at the source.
type DiscoverResult struct {
	Workload  WorkloadType      `json:"workload"`
	Found     bool              `json:"found"`
	ItemCount int               `json:"itemCount"`
	SizeBytes int64             `json:"sizeBytes"`
	Details   map[string]string `json:"details,omitempty"`
}

// ValidateResult reports whether preconditions are met. A non-empty Blockers
// slice means the migration must not proceed; Warnings are advisory.
type ValidateResult struct {
	OK       bool     `json:"ok"`
	Blockers []string `json:"blockers,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// PlanResult is a driver's proposed migration plan for its workload.
type PlanResult struct {
	Workload         WorkloadType `json:"workload"`
	Strategy         string       `json:"strategy"`
	Steps            []string     `json:"steps"`
	RequiresDowntime bool         `json:"requiresDowntime"`
	EstDowntime      string       `json:"estDowntime,omitempty"`
	ManualSteps      []string     `json:"manualSteps,omitempty"`
}

// BackupResult references a backup the driver captured. Location is opaque and
// driver-specific (a path, a snapshot ID, ...); the driver must be able to
// restore from it in Rollback.
type BackupResult struct {
	Location  string            `json:"location"`
	SizeBytes int64             `json:"sizeBytes"`
	Details   map[string]string `json:"details,omitempty"`
}

// MigrateResult reports the outcome of the data movement. Checkpoint is an
// opaque token a driver can hand back to Resume to continue an interrupted run.
type MigrateResult struct {
	BytesTransferred int64  `json:"bytesTransferred"`
	Checkpoint       string `json:"checkpoint,omitempty"`
	Complete         bool   `json:"complete"`
}

// VerifyResult reports whether the migrated workload is correct on the target.
type VerifyResult struct {
	OK        bool     `json:"ok"`
	Mismatches []string `json:"mismatches,omitempty"`
}

// RiskAssessment is a driver's honest self-assessment of its own risk. It is
// deliberately coarse (a 0-100 score plus a level) and does not depend on the
// migration package's RiskEngine; a planner may combine several drivers'
// assessments.
type RiskAssessment struct {
	Score        int      `json:"score"` // 0-100, higher = riskier
	Level        string   `json:"level"` // low | medium | high | critical
	Factors      []string `json:"factors,omitempty"`
	Reversible   bool     `json:"reversible"`
	DataLossRisk bool     `json:"dataLossRisk"`
}

// Driver is the workload-level migration abstraction. One implementation exists
// per workload type. Every method takes a context for cancellation and returns
// a typed result plus an error; operations a driver does not support return a
// NotImplementedError (see NewNotImplemented), which callers can detect with
// IsNotImplemented.
//
// The interface is intentionally minimal — ten operations covering the full
// lifecycle — so implementing a new workload driver is tractable and the
// surface a planner reasons about is small.
type Driver interface {
	// Capabilities reports what this driver can do. It must be cheap, pure, and
	// safe to call without a connection — a planner calls it during selection.
	Capabilities() Capabilities

	// Discover inspects the source and reports what the driver found.
	Discover(ctx context.Context, src Target) (DiscoverResult, error)
	// Validate checks that source and target satisfy the driver's preconditions.
	Validate(ctx context.Context, src, dst Target) (ValidateResult, error)
	// Plan produces a migration plan for this workload.
	Plan(ctx context.Context, src, dst Target) (PlanResult, error)
	// Backup captures a restorable backup of the target (or source) before
	// migrating, so Rollback has something to restore.
	Backup(ctx context.Context, dst Target) (BackupResult, error)
	// Migrate performs the data movement from src to dst.
	Migrate(ctx context.Context, src, dst Target) (MigrateResult, error)
	// Verify checks the migrated workload on the target for correctness.
	Verify(ctx context.Context, src, dst Target) (VerifyResult, error)
	// Rollback undoes a migration using the given backup.
	Rollback(ctx context.Context, dst Target, backup BackupResult) error
	// Resume continues an interrupted migration from the given checkpoint token
	// (as returned in a prior MigrateResult.Checkpoint).
	Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error)
	// Risk returns the driver's self-assessed risk for migrating this workload.
	Risk(ctx context.Context, src, dst Target) (RiskAssessment, error)
}

// --- Errors ---

// ErrNotImplemented is the sentinel wrapped by NotImplementedError. Detect it
// with errors.Is(err, ErrNotImplemented) or the IsNotImplemented helper.
var ErrNotImplemented = errors.New("driver: operation not implemented")

// NotImplementedError is returned by a driver for an operation it does not yet
// support. It names the workload and operation so the error message is
// actionable, and wraps ErrNotImplemented for errors.Is detection.
type NotImplementedError struct {
	Workload  WorkloadType
	Operation Capability
}

// NewNotImplemented builds a NotImplementedError for the given workload/op.
func NewNotImplemented(workload WorkloadType, op Capability) *NotImplementedError {
	return &NotImplementedError{Workload: workload, Operation: op}
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("driver %q: operation %q not implemented", e.Workload, e.Operation)
}

// Unwrap lets errors.Is(err, ErrNotImplemented) match.
func (e *NotImplementedError) Unwrap() error { return ErrNotImplemented }

// IsNotImplemented reports whether err is (or wraps) a not-implemented error.
func IsNotImplemented(err error) bool {
	return errors.Is(err, ErrNotImplemented)
}
