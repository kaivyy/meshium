package migration

// Category metadata makes the plan-vs-execute contract explicit and
// user-visible (Phase 5B, Part 1). Every category declares what it collects at
// plan time, what it applies at execute time, whether it is resumable, and its
// honest downtime class. The pipeline remains the source of truth; this is
// descriptive metadata surfaced in Step 3/4 so the user understands what a
// selected category will actually do before executing.

// CollectionStrategy records whether a category's plan-time collection reused
// an onboarding scan or performed a fresh live collection.
type CollectionStrategy string

const (
	// CollectReuse means the planner reused a fresh onboarding snapshot
	// (server_info / discovery_snapshots) instead of re-collecting live.
	CollectReuse CollectionStrategy = "reuse"
	// CollectRefresh means the planner re-collected live from the source.
	CollectRefresh CollectionStrategy = "refresh"
	// CollectMixed means part was reused and part was collected live.
	CollectMixed CollectionStrategy = "mixed"
)

// DowntimeClass is the honest downtime model (Phase 5B, Part 3). It is NEVER
// "zero_downtime" unless the full live-replication + freeze/fencing + traffic
// switch + observation chain is genuinely wired and verified end-to-end.
type DowntimeClass string

const (
	// DowntimeOfflineCopy is a plain dump/copy/restore. Significant downtime.
	DowntimeOfflineCopy DowntimeClass = "offline_copy"
	// DowntimeMinimal is seed + catch-up + operator-gated cutover. Small switch
	// downtime. This is the honest default ambition for a safe MVP.
	DowntimeMinimal DowntimeClass = "minimal_downtime"
	// DowntimeZero is only exposed when live replication is active, lag is
	// verifiable, freeze/fencing actually run, traffic switch actually runs, and
	// observation + rollback safety are real.
	DowntimeZero DowntimeClass = "zero_downtime"
)

// DowntimeClassFor reports the most optimistic downtime class a category may
// legitimately claim GIVEN the current capability gates. It is intentionally
// conservative: the global zero-downtime gate must be satisfied, otherwise the
// category caps at minimal (or offline if it has no replication path at all).
//
// Phase 5E (G): database/docker default to offline_copy (honest snapshot
// copy / volume move), NOT minimal_downtime — minimal implies a cutover stage
// that is not the default execution path. zero_downtime is unreachable while
// zeroDowntimeCapable is false.
func DowntimeClassFor(cat string, zeroCapable bool) DowntimeClass {
	if cat == "database" || cat == "docker" {
		if zeroCapable {
			return DowntimeZero
		}
		return DowntimeOfflineCopy
	}
	// packages / configs / services / users are offline copies (no replication
	// path exists for them), regardless of the global gate.
	return DowntimeOfflineCopy
}

// DatabaseResumable reports whether a specific engine's default migration path
// can resume after interruption (Phase 5E, D5). File-path engines (PostgreSQL,
// Redis) resume the upload leg from a partial; streaming engines (MySQL,
// MongoDB) currently RESTART the transfer after interruption — they must NOT be
// shown a generic "resumable" badge.
func DatabaseResumable(engine string) bool {
	switch engine {
	case "postgres", "redis":
		return true
	case "mysql", "mongodb":
		return false
	}
	return false
}

// CategoryMeta is the per-category contract persisted into the plan step so the
// FE can render "what this category will do" before execute.
type CategoryMeta struct {
	Category          string            `json:"category"`
	CollectionStrategy CollectionStrategy `json:"collectionStrategy,omitempty"`
	ReuseSource       string            `json:"reuseSource,omitempty"` // server_info | discovery_snapshot | none
	ExecutionMode     string            `json:"executionMode,omitempty"`
	EstimatedBytes    int64             `json:"estimatedBytes,omitempty"`
	EstimatedDuration string            `json:"estimatedDuration,omitempty"`
	Resumable         bool              `json:"resumable"`
	DowntimeClass     DowntimeClass     `json:"downtimeClass"`
	Warnings          []string          `json:"warnings,omitempty"`
	BlockingIssues    []string          `json:"blockingIssues,omitempty"`
	// PlanBehavior / ExecuteBehavior are short human-readable summaries shown in
	// the wizard (kept as data so the FE need not hardcode category knowledge).
	PlanBehavior    string `json:"planBehavior,omitempty"`
	ExecuteBehavior string `json:"executeBehavior,omitempty"`
}

// ReuseSource values.
const (
	ReuseServerInfo      = "server_info"
	ReuseDiscoverySnap   = "discovery_snapshot"
	ReuseNone            = "none"
)

// categoryMetaFor returns the static (non-freshness-dependent) metadata for a
// category. Freshness-aware fields (CollectionStrategy, ReuseSource, Warnings,
// Estimated*) are filled in by the planner when it knows the reuse decision.
func categoryMetaFor(cat string, zeroCapable bool) CategoryMeta {
	m := CategoryMeta{
		Category:      cat,
		Resumable:    categoryResumable(cat),
		DowntimeClass: DowntimeClassFor(cat, zeroCapable),
	}
	switch cat {
	case "packages":
		m.PlanBehavior = "Detect package manager + installed packages; check target compatibility."
		m.ExecuteBehavior = "Install packages on target via its package manager. Fails clearly if the target manager is incompatible."
	case "configs":
		m.PlanBehavior = "Enumerate config paths (default + custom); classify size + checksum availability."
		m.ExecuteBehavior = "Transfer configs. Small files via SFTP; large files via resumable transfer. Shows overwrite/diff risk before apply."
	case "services":
		m.PlanBehavior = "Detect enabled systemd services; map dependencies to packages + configs."
		m.ExecuteBehavior = "Enable/start/restart only after package + config prerequisites are met. Fails clearly, never a vague warning."
	case "users":
		m.PlanBehavior = "Enumerate users, groups, cron jobs, firewall/security rules."
		m.ExecuteBehavior = "Apply in stages; audit-log every sensitive change. Destructive/security ops require explicit confirmation."
	case "docker":
		m.PlanBehavior = "Detect containers, compose files, images, mounts, named volumes; classify volume size."
		m.ExecuteBehavior = "Move definitions + images + volumes via the appropriate transfer engine. Large volumes use resumable transfer, not tar+SFTP no-resume."
	case "database":
		m.PlanBehavior = "Detect engine + location (host/container/compose); enumerate databases; assess snapshot vs live-replication capability."
		m.ExecuteBehavior = "Run the user-selected mode per engine capability. No silent no-op. Empty DB name = all user databases."
	}
	return m
}

// categoryResumable reports whether a category can resume mid-transfer. File/
// volume/DB transfers are resumable; pure metadata operations are not.
func categoryResumable(cat string) bool {
	switch cat {
	case "configs", "docker", "database":
		return true
	}
	return false
}

// CategoryMetaFor is the exported entry point used by the planner and tests to
// obtain a category's static metadata for a given zero-downtime capability gate.
func CategoryMetaFor(cat string, zeroCapable bool) CategoryMeta {
	return categoryMetaFor(cat, zeroCapable)
}
