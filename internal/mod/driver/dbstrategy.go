package driver

// This file is the honest catalog of database migration strategies for the
// MySQL, PostgreSQL, and Redis workloads. It exists so a planner (Tahap 7) can
// reason about *which* strategy to use for a database workload and, critically,
// so no layer can derive a false zero-downtime claim.
//
// The catalog is grounded in what the codebase actually executes, not in what
// the drivers aspire to:
//
//   - Continuous replication IS implemented and wired into the pipeline's
//     live-replication stage (see internal/mod/migration/replication.go):
//     MySQL binlog (CHANGE MASTER / START SLAVE), PostgreSQL streaming
//     (pg_basebackup -Xs -R), and Redis replica handoff (REPLICAOF), each with
//     lag monitoring, promote, and rollback.
//   - Logical dumps (mysqldump, pg_dump/pg_restore, Redis RDB/BGSAVE) exist as
//     plan-generation command strings in the planer package, but there is no
//     orchestrated dump-based migrate path that runs them end to end. They are
//     therefore catalogued as DESCRIPTOR maturity, not IMPLEMENTED.
//   - Everything else (physical backups, logical replication, AOF streaming,
//     BullMQ queue-drain awareness) is FUTURE and labelled as such.
//
// A driver's Capabilities() advertises what a driver can do; this catalog adds
// the finer-grained, per-strategy honesty a planner needs to pick one and to
// classify downtime correctly.

// StrategyMaturity states how real a strategy is, so a planner never treats an
// aspiration as a shipping capability.
type StrategyMaturity string

const (
	// MaturityImplemented: the strategy is coded and wired into an executed
	// migration path today.
	MaturityImplemented StrategyMaturity = "implemented"
	// MaturityDescriptor: the strategy's commands exist (e.g. for plan display)
	// but no orchestrated end-to-end path runs them. Selectable only for
	// planning/preview, never as an executed migration.
	MaturityDescriptor StrategyMaturity = "descriptor"
	// MaturityFuture: the strategy is intended but not built. It must never be
	// selected for execution.
	MaturityFuture StrategyMaturity = "future"
)

// DowntimeClass is the honest downtime characterization of a strategy. It is
// the single value a UI/CLI/API must consult before making any claim about
// downtime — there is deliberately no "zero" class for a strategy whose only
// mechanism is a dump.
type DowntimeClass string

const (
	// DowntimeFull: the source must be quiesced for the whole transfer (a
	// consistent dump/restore). Proportional to data size.
	DowntimeFull DowntimeClass = "full"
	// DowntimeBrief: a short cutover window after continuous replication has
	// caught up (stop writes, final sync, promote). Not zero, but bounded and
	// independent of total data size.
	DowntimeBrief DowntimeClass = "brief"
)

// DBStrategy is one honest, catalogued strategy for a database workload.
type DBStrategy struct {
	// Name is a stable identifier (e.g. "mysql_binlog_replication").
	Name string `json:"name"`
	// Workload is the database workload this strategy applies to.
	Workload WorkloadType `json:"workload"`
	// Maturity states whether this strategy is executed today, merely described,
	// or still future work.
	Maturity StrategyMaturity `json:"maturity"`
	// Mechanism names the underlying technique (e.g. "binlog", "pg_basebackup",
	// "mysqldump", "REPLICAOF").
	Mechanism string `json:"mechanism"`
	// RequiresDowntime is the honest downtime flag. A dump-based strategy always
	// requires downtime; a replication-based one still needs a brief cutover
	// window, so this is true for it as well — "brief" is not "none".
	RequiresDowntime bool `json:"requiresDowntime"`
	// Downtime classifies the downtime shape (full vs brief). Empty for future
	// strategies whose behavior is not yet pinned down.
	Downtime DowntimeClass `json:"downtime,omitempty"`
	// Description is honest free-form text about what the strategy does and its
	// limitations.
	Description string `json:"description"`
}

// dbStrategyCatalog is the source of truth. It is intentionally a package-level
// value built once; callers receive copies via the accessor functions so they
// cannot mutate the catalog.
var dbStrategyCatalog = map[WorkloadType][]DBStrategy{
	WorkloadMySQL: {
		{
			Name:             "mysql_binlog_replication",
			Workload:         WorkloadMySQL,
			Maturity:         MaturityImplemented,
			Mechanism:        "binlog",
			RequiresDowntime: true,
			Downtime:         DowntimeBrief,
			Description: "Continuous binlog replication (CHANGE MASTER/START SLAVE) " +
				"with lag monitoring, promote, and rollback. Wired into the pipeline " +
				"live-replication stage. Requires only a brief cutover window (stop " +
				"writes, final sync, promote); it is NOT zero-downtime.",
		},
		{
			Name:             "mysqldump",
			Workload:         WorkloadMySQL,
			Maturity:         MaturityDescriptor,
			Mechanism:        "mysqldump",
			RequiresDowntime: true,
			Downtime:         DowntimeFull,
			Description: "Consistent logical dump/restore. Command generation exists " +
				"for plan preview, but there is no orchestrated dump-based migrate " +
				"path today, so it is selectable for planning only. Requires full " +
				"downtime for a consistent snapshot; downtime scales with data size.",
		},
		{
			Name:      "mysql_physical_backup",
			Workload:  WorkloadMySQL,
			Maturity:  MaturityFuture,
			Mechanism: "xtrabackup",
			Description: "Physical hot backup (e.g. Percona XtraBackup) for large " +
				"datasets. Not implemented.",
		},
	},
	WorkloadPostgreSQL: {
		{
			Name:             "postgres_streaming_replication",
			Workload:         WorkloadPostgreSQL,
			Maturity:         MaturityImplemented,
			Mechanism:        "pg_basebackup",
			RequiresDowntime: true,
			Downtime:         DowntimeBrief,
			Description: "Streaming physical replication via pg_basebackup -Xs -R " +
				"with lag monitoring, promote, and rollback. Wired into the pipeline " +
				"live-replication stage. Requires only a brief cutover window; it is " +
				"NOT zero-downtime.",
		},
		{
			Name:             "pg_dump_restore",
			Workload:         WorkloadPostgreSQL,
			Maturity:         MaturityDescriptor,
			Mechanism:        "pg_dump/pg_restore",
			RequiresDowntime: true,
			Downtime:         DowntimeFull,
			Description: "Logical dump/restore (pg_dump/pg_dumpall + pg_restore). " +
				"Command generation exists for plan preview, but no orchestrated " +
				"dump-based migrate path runs it today. Requires full downtime; " +
				"downtime scales with data size.",
		},
		{
			Name:      "postgres_logical_replication",
			Workload:  WorkloadPostgreSQL,
			Maturity:  MaturityFuture,
			Mechanism: "pgoutput/publication-subscription",
			Description: "Logical replication (publications/subscriptions) for " +
				"selective and cross-version migration. Not implemented.",
		},
	},
	WorkloadRedis: {
		{
			Name:             "redis_replica_handoff",
			Workload:         WorkloadRedis,
			Maturity:         MaturityImplemented,
			Mechanism:        "REPLICAOF",
			RequiresDowntime: true,
			Downtime:         DowntimeBrief,
			Description: "Replica handoff via REPLICAOF with lag monitoring, promote " +
				"(REPLICAOF NO ONE), and rollback. Wired into the pipeline " +
				"live-replication stage. Requires only a brief cutover window; it is " +
				"NOT zero-downtime.",
		},
		{
			Name:             "redis_rdb_snapshot",
			Workload:         WorkloadRedis,
			Maturity:         MaturityDescriptor,
			Mechanism:        "RDB/BGSAVE",
			RequiresDowntime: true,
			Downtime:         DowntimeFull,
			Description: "Point-in-time RDB snapshot (BGSAVE) transfer. BGSAVE is " +
				"invoked as part of the replication final-sync, but a standalone " +
				"snapshot-only migrate path is not orchestrated. Requires a downtime " +
				"window for a consistent point-in-time copy.",
		},
		{
			Name:      "redis_aof_stream",
			Workload:  WorkloadRedis,
			Maturity:  MaturityFuture,
			Mechanism: "AOF",
			Description: "Append-only-file streaming for finer-grained durability. " +
				"Not implemented.",
		},
		{
			Name:      "redis_bullmq_queue_drain",
			Workload:  WorkloadRedis,
			Maturity:  MaturityFuture,
			Mechanism: "BullMQ-aware drain",
			Description: "BullMQ queue-drain awareness (pause producers, drain jobs, " +
				"resume on target) to avoid dropped or duplicated jobs. Not implemented.",
		},
	},
}

// DBStrategiesFor returns the catalogued strategies for a database workload, in
// catalog order (implemented first). It returns nil for a non-database workload
// or one with no catalog entry. The returned slice is a copy; mutating it does
// not affect the catalog.
func DBStrategiesFor(wt WorkloadType) []DBStrategy {
	src, ok := dbStrategyCatalog[wt]
	if !ok {
		return nil
	}
	out := make([]DBStrategy, len(src))
	copy(out, src)
	return out
}

// SelectDBStrategy picks the strategy to use for a database workload under an
// honest policy: prefer the lowest-downtime IMPLEMENTED strategy. It never
// selects a DESCRIPTOR or FUTURE strategy for execution, because neither has an
// orchestrated migrate path. It returns ok=false when the workload has no
// implemented strategy (the caller must then fall back to planning-only or
// report the workload as unsupported for execution).
//
// "Lowest downtime" ranks DowntimeBrief above DowntimeFull; among equal
// downtime classes, catalog order (which lists the primary strategy first)
// breaks the tie deterministically.
func SelectDBStrategy(wt WorkloadType) (DBStrategy, bool) {
	var best DBStrategy
	found := false
	for _, s := range dbStrategyCatalog[wt] {
		if s.Maturity != MaturityImplemented {
			continue
		}
		if !found || downtimeRank(s.Downtime) < downtimeRank(best.Downtime) {
			best = s
			found = true
		}
	}
	return best, found
}

// downtimeRank orders downtime classes from least to most disruptive so
// selection is deterministic. Brief downtime is preferred over full downtime.
func downtimeRank(d DowntimeClass) int {
	switch d {
	case DowntimeBrief:
		return 0
	case DowntimeFull:
		return 1
	default:
		return 2
	}
}

// IsZeroDowntime reports whether a strategy is genuinely zero-downtime. It
// requires the strategy to be IMPLEMENTED before it can make any downtime claim
// at all — a descriptor or future strategy has no executed path, so it can
// never be asserted zero-downtime regardless of its (possibly unset) flags.
// The result is always false in the current catalog: every implemented database
// strategy is replication-based and still needs a brief cutover window. This
// helper exists so callers assert the honest answer through one function rather
// than re-deriving it (and getting it wrong) at each call site.
func (s DBStrategy) IsZeroDowntime() bool {
	return s.Maturity == MaturityImplemented && !s.RequiresDowntime
}
