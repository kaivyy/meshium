package driver

import "context"

// This file holds the initial workload-driver skeletons. Each skeleton is
// honest by construction: it advertises a capability model describing the
// strategies it will eventually support, but every lifecycle operation returns
// a NotImplementedError so nothing silently fakes a migration. As real logic
// lands per workload, the corresponding methods graduate out of this file into
// their own implementation and the Supported set grows.
//
// The capability models here are the single source of truth for downtime
// honesty: a skeleton whose only planned strategy is a dump sets
// RequiresDowntime=true and does NOT list CapZeroDowntimeCutover, so a planner
// (Tahap 7) can never derive a false zero-downtime claim from it.

// --- Docker Compose ---

// DockerComposeDriver will migrate a Docker Compose project (compose files,
// named volumes, and container state). Skeleton: capability model only.
type DockerComposeDriver struct{}

// NewDockerComposeDriver constructs a DockerComposeDriver skeleton.
func NewDockerComposeDriver() *DockerComposeDriver { return &DockerComposeDriver{} }

func (d *DockerComposeDriver) Capabilities() Capabilities {
	return Capabilities{
		Workload: WorkloadDockerCompose,
		Supported: []Capability{
			CapDiscover, CapValidate, CapPlan, CapBackup,
			CapMigrate, CapVerify, CapRollback,
		},
		DefaultStrategy:  "compose_redeploy",
		RequiresDowntime: true,
		Notes: "Skeleton. Intended strategy: copy compose files, migrate named " +
			"volumes via archive transfer, redeploy on target. Volume copy requires " +
			"stopping containers, so the default incurs downtime. Live volume " +
			"replication and zero-downtime cutover are future capabilities. All " +
			"operations currently return NotImplemented.",
	}
}

func (d *DockerComposeDriver) Discover(ctx context.Context, src Target) (DiscoverResult, error) {
	return DiscoverResult{}, NewNotImplemented(WorkloadDockerCompose, CapDiscover)
}
func (d *DockerComposeDriver) Validate(ctx context.Context, src, dst Target) (ValidateResult, error) {
	return ValidateResult{}, NewNotImplemented(WorkloadDockerCompose, CapValidate)
}
func (d *DockerComposeDriver) Plan(ctx context.Context, src, dst Target) (PlanResult, error) {
	return PlanResult{}, NewNotImplemented(WorkloadDockerCompose, CapPlan)
}
func (d *DockerComposeDriver) Backup(ctx context.Context, dst Target) (BackupResult, error) {
	return BackupResult{}, NewNotImplemented(WorkloadDockerCompose, CapBackup)
}
func (d *DockerComposeDriver) Migrate(ctx context.Context, src, dst Target) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadDockerCompose, CapMigrate)
}
func (d *DockerComposeDriver) Verify(ctx context.Context, src, dst Target) (VerifyResult, error) {
	return VerifyResult{}, NewNotImplemented(WorkloadDockerCompose, CapVerify)
}
func (d *DockerComposeDriver) Rollback(ctx context.Context, dst Target, backup BackupResult) error {
	return NewNotImplemented(WorkloadDockerCompose, CapRollback)
}
func (d *DockerComposeDriver) Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadDockerCompose, CapResume)
}
func (d *DockerComposeDriver) Risk(ctx context.Context, src, dst Target) (RiskAssessment, error) {
	return RiskAssessment{}, NewNotImplemented(WorkloadDockerCompose, CapMigrate)
}

// --- MySQL ---

// MySQLDriver will migrate a MySQL instance/database. Skeleton: capability
// model only. Concrete strategies (mysqldump, binlog replication) land in
// Tahap 6.
type MySQLDriver struct{}

// NewMySQLDriver constructs a MySQLDriver skeleton.
func NewMySQLDriver() *MySQLDriver { return &MySQLDriver{} }

func (d *MySQLDriver) Capabilities() Capabilities {
	return Capabilities{
		Workload: WorkloadMySQL,
		Supported: []Capability{
			CapDiscover, CapValidate, CapPlan, CapBackup,
			CapMigrate, CapVerify, CapRollback,
		},
		DefaultStrategy:  "mysqldump",
		RequiresDowntime: true,
		Notes: "Skeleton. Default strategy mysqldump requires downtime (a " +
			"consistent dump locks or snapshots the source). Binlog-based " +
			"replication for low/zero-downtime cutover is a future capability and " +
			"is intentionally not advertised. All operations return NotImplemented.",
	}
}

func (d *MySQLDriver) Discover(ctx context.Context, src Target) (DiscoverResult, error) {
	return DiscoverResult{}, NewNotImplemented(WorkloadMySQL, CapDiscover)
}
func (d *MySQLDriver) Validate(ctx context.Context, src, dst Target) (ValidateResult, error) {
	return ValidateResult{}, NewNotImplemented(WorkloadMySQL, CapValidate)
}
func (d *MySQLDriver) Plan(ctx context.Context, src, dst Target) (PlanResult, error) {
	return PlanResult{}, NewNotImplemented(WorkloadMySQL, CapPlan)
}
func (d *MySQLDriver) Backup(ctx context.Context, dst Target) (BackupResult, error) {
	return BackupResult{}, NewNotImplemented(WorkloadMySQL, CapBackup)
}
func (d *MySQLDriver) Migrate(ctx context.Context, src, dst Target) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadMySQL, CapMigrate)
}
func (d *MySQLDriver) Verify(ctx context.Context, src, dst Target) (VerifyResult, error) {
	return VerifyResult{}, NewNotImplemented(WorkloadMySQL, CapVerify)
}
func (d *MySQLDriver) Rollback(ctx context.Context, dst Target, backup BackupResult) error {
	return NewNotImplemented(WorkloadMySQL, CapRollback)
}
func (d *MySQLDriver) Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadMySQL, CapResume)
}
func (d *MySQLDriver) Risk(ctx context.Context, src, dst Target) (RiskAssessment, error) {
	return RiskAssessment{}, NewNotImplemented(WorkloadMySQL, CapMigrate)
}

// --- PostgreSQL ---

// PostgreSQLDriver will migrate a PostgreSQL instance/database. Skeleton:
// capability model only. Concrete strategies (pg_dump/pg_restore, logical and
// streaming replication) land in Tahap 6.
type PostgreSQLDriver struct{}

// NewPostgreSQLDriver constructs a PostgreSQLDriver skeleton.
func NewPostgreSQLDriver() *PostgreSQLDriver { return &PostgreSQLDriver{} }

func (d *PostgreSQLDriver) Capabilities() Capabilities {
	return Capabilities{
		Workload: WorkloadPostgreSQL,
		Supported: []Capability{
			CapDiscover, CapValidate, CapPlan, CapBackup,
			CapMigrate, CapVerify, CapRollback,
		},
		DefaultStrategy:  "pg_dump",
		RequiresDowntime: true,
		Notes: "Skeleton. Default strategy pg_dump/pg_restore requires downtime. " +
			"Logical replication and streaming replication for low/zero-downtime " +
			"cutover are future capabilities and are intentionally not advertised. " +
			"All operations return NotImplemented.",
	}
}

func (d *PostgreSQLDriver) Discover(ctx context.Context, src Target) (DiscoverResult, error) {
	return DiscoverResult{}, NewNotImplemented(WorkloadPostgreSQL, CapDiscover)
}
func (d *PostgreSQLDriver) Validate(ctx context.Context, src, dst Target) (ValidateResult, error) {
	return ValidateResult{}, NewNotImplemented(WorkloadPostgreSQL, CapValidate)
}
func (d *PostgreSQLDriver) Plan(ctx context.Context, src, dst Target) (PlanResult, error) {
	return PlanResult{}, NewNotImplemented(WorkloadPostgreSQL, CapPlan)
}
func (d *PostgreSQLDriver) Backup(ctx context.Context, dst Target) (BackupResult, error) {
	return BackupResult{}, NewNotImplemented(WorkloadPostgreSQL, CapBackup)
}
func (d *PostgreSQLDriver) Migrate(ctx context.Context, src, dst Target) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadPostgreSQL, CapMigrate)
}
func (d *PostgreSQLDriver) Verify(ctx context.Context, src, dst Target) (VerifyResult, error) {
	return VerifyResult{}, NewNotImplemented(WorkloadPostgreSQL, CapVerify)
}
func (d *PostgreSQLDriver) Rollback(ctx context.Context, dst Target, backup BackupResult) error {
	return NewNotImplemented(WorkloadPostgreSQL, CapRollback)
}
func (d *PostgreSQLDriver) Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadPostgreSQL, CapResume)
}
func (d *PostgreSQLDriver) Risk(ctx context.Context, src, dst Target) (RiskAssessment, error) {
	return RiskAssessment{}, NewNotImplemented(WorkloadPostgreSQL, CapMigrate)
}

// --- Redis ---

// RedisDriver will migrate a Redis instance. Skeleton: capability model only.
// Concrete strategies (RDB snapshot, AOF, replica handoff) land in Tahap 6.
type RedisDriver struct{}

// NewRedisDriver constructs a RedisDriver skeleton.
func NewRedisDriver() *RedisDriver { return &RedisDriver{} }

func (d *RedisDriver) Capabilities() Capabilities {
	return Capabilities{
		Workload: WorkloadRedis,
		Supported: []Capability{
			CapDiscover, CapValidate, CapPlan, CapBackup,
			CapMigrate, CapVerify, CapRollback,
		},
		DefaultStrategy:  "rdb_snapshot",
		RequiresDowntime: true,
		Notes: "Skeleton. Default strategy RDB snapshot requires a brief downtime " +
			"window. AOF transfer and replica handoff (REPLICAOF) for low-downtime " +
			"cutover are future capabilities. BullMQ queue-drain awareness is a " +
			"future capability. All operations return NotImplemented.",
	}
}

func (d *RedisDriver) Discover(ctx context.Context, src Target) (DiscoverResult, error) {
	return DiscoverResult{}, NewNotImplemented(WorkloadRedis, CapDiscover)
}
func (d *RedisDriver) Validate(ctx context.Context, src, dst Target) (ValidateResult, error) {
	return ValidateResult{}, NewNotImplemented(WorkloadRedis, CapValidate)
}
func (d *RedisDriver) Plan(ctx context.Context, src, dst Target) (PlanResult, error) {
	return PlanResult{}, NewNotImplemented(WorkloadRedis, CapPlan)
}
func (d *RedisDriver) Backup(ctx context.Context, dst Target) (BackupResult, error) {
	return BackupResult{}, NewNotImplemented(WorkloadRedis, CapBackup)
}
func (d *RedisDriver) Migrate(ctx context.Context, src, dst Target) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadRedis, CapMigrate)
}
func (d *RedisDriver) Verify(ctx context.Context, src, dst Target) (VerifyResult, error) {
	return VerifyResult{}, NewNotImplemented(WorkloadRedis, CapVerify)
}
func (d *RedisDriver) Rollback(ctx context.Context, dst Target, backup BackupResult) error {
	return NewNotImplemented(WorkloadRedis, CapRollback)
}
func (d *RedisDriver) Resume(ctx context.Context, src, dst Target, checkpoint string) (MigrateResult, error) {
	return MigrateResult{}, NewNotImplemented(WorkloadRedis, CapResume)
}
func (d *RedisDriver) Risk(ctx context.Context, src, dst Target) (RiskAssessment, error) {
	return RiskAssessment{}, NewNotImplemented(WorkloadRedis, CapMigrate)
}
