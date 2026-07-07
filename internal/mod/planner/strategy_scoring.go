package planner

// This file adds a capability-grounded, deterministic scoring selector on top of
// the driver-SDK strategy catalog (Tahap 6). It is additive: the existing
// rule-based SelectStrategy (and its GenerateWarnings wiring) is left untouched.
//
// Why a second selector rather than editing the first: SelectStrategy claims
// "near-zero downtime / < 1 minute" whenever HasDatabase && HasReplication,
// without checking whether the specific engine's replication is actually
// implemented and without ever admitting that even replication needs a brief
// (non-zero) cutover window. SelectStrategyScored closes that honesty gap by
// deriving feasibility from driver.SelectDBStrategy — replication is offered for
// a database engine only when the catalog confirms an *implemented* strategy —
// and by never labelling any strategy zero-downtime.
//
// Determinism: candidates are enumerated in a fixed order per workload, scored
// with an integer function whose components are documented below, and the best
// score wins with a first-in-enumeration tie-break. No maps are iterated during
// selection, so the output is stable for a given input.

import (
	"fmt"
	"math"

	"meshium/internal/mod/driver"
)

const (
	// replicationCutoverSeconds is the honest, size-independent estimate for a
	// replication-based cutover: stop writes, let lag reach zero, promote target.
	// It is brief but NOT zero.
	replicationCutoverSeconds = 45
	// filesystemFinalSyncSeconds estimates the brief final rsync pass after the
	// bulk incremental copy has completed.
	filesystemFinalSyncSeconds = 30
)

// SelectorConstraints is the input to the scoring selector. Zero values mean
// "unspecified": a zero DowntimeBudgetSeconds/RTOSeconds is treated as unlimited,
// and a zero DataSizeGB/BandwidthMbps falls back to conservative estimates.
type SelectorConstraints struct {
	// Workload is the driver workload type being migrated. Database workloads
	// (mysql/postgresql/redis) drive the replication-vs-dump decision.
	Workload driver.WorkloadType `json:"workload"`
	// DataSizeGB is the total data to move; drives dump downtime estimates.
	DataSizeGB float64 `json:"dataSizeGb"`
	// BandwidthMbps is the usable transfer bandwidth; drives dump downtime.
	BandwidthMbps float64 `json:"bandwidthMbps"`
	// DowntimeBudgetSeconds is the maximum tolerable cutover/downtime. Zero means
	// no budget was set (treated as unlimited). Because no strategy is
	// zero-downtime, a budget below the briefest available cutover rejects every
	// candidate and the decision says so honestly.
	DowntimeBudgetSeconds int `json:"downtimeBudgetSeconds"`
	// RPOSeconds is the recovery-point objective (tolerable data loss). A tight
	// RPO favours continuous replication in scoring.
	RPOSeconds int `json:"rpoSeconds"`
	// RTOSeconds is the recovery-time objective (tolerable downtime). It is folded
	// into the effective downtime budget.
	RTOSeconds int `json:"rtoSeconds"`
	// CanRollback reports whether the caller has a rollback path; rewards
	// strategies that support rollback.
	CanRollback bool `json:"canRollback"`
}

// StrategyCandidate is one scored migration-strategy option. Both the selected
// strategy and every rejected one are reported as candidates so a caller can
// explain the decision end to end.
type StrategyCandidate struct {
	Strategy          MigrationStrategy `json:"strategy"`
	Score             int               `json:"score"`
	Reason            string            `json:"reason"`
	RiskLevel         string            `json:"riskLevel"`
	EstimatedDowntime string            `json:"estimatedDowntime"`
	ManualSteps       []string          `json:"manualSteps,omitempty"`
	Rejected          bool              `json:"rejected"`
	RejectReason      string            `json:"rejectReason,omitempty"`
}

// StrategyDecision is the deterministic output of SelectStrategyScored. When no
// executable strategy fits the constraints, Selected is the zero value (its
// Strategy is empty) and Reasoning explains why; the reasons live in Rejected.
type StrategyDecision struct {
	Selected  StrategyCandidate   `json:"selected"`
	Rejected  []StrategyCandidate `json:"rejected,omitempty"`
	Reasoning string              `json:"reasoning"`
}

// scoredCandidate is the internal, pre-scoring shape of a candidate.
type scoredCandidate struct {
	strategy          MigrationStrategy
	feasibleForExec   bool // false = catalogued/described but no orchestrated run path
	continuous        bool // true = continuous replication (meets tight RPO)
	downtimeSeconds   int  // -1 = unknown (manual assessment)
	riskLevel         string
	rollbackAvailable bool
	manualSteps       []string
	note              string
}

// SelectStrategyScored picks a migration strategy for the given constraints
// using capability-grounded scoring. It never selects a strategy that has no
// orchestrated execution path (e.g. a logical dump, which the catalog marks
// descriptor-only) and never labels any strategy zero-downtime.
func SelectStrategyScored(c SelectorConstraints) StrategyDecision {
	cands := candidatesFor(c)
	budget := effectiveBudget(c)

	pub := make([]StrategyCandidate, 0, len(cands))
	bestIdx := -1
	bestScore := -1

	for _, sc := range cands {
		p := StrategyCandidate{
			Strategy:          sc.strategy,
			RiskLevel:         sc.riskLevel,
			EstimatedDowntime: downtimeString(sc.downtimeSeconds),
			ManualSteps:       sc.manualSteps,
			Reason:            sc.note,
		}
		switch {
		case !sc.feasibleForExec:
			p.Rejected = true
			p.RejectReason = "no orchestrated execution path yet (planning/preview only)"
		case budget > 0 && sc.downtimeSeconds >= 0 && sc.downtimeSeconds > budget:
			p.Rejected = true
			p.RejectReason = fmt.Sprintf(
				"estimated downtime %s exceeds budget %s; no zero-downtime strategy exists for this workload",
				downtimeString(sc.downtimeSeconds), downtimeString(budget))
		default:
			p.Score = scoreCandidate(sc, c)
			if p.Score > bestScore {
				bestScore = p.Score
				bestIdx = len(pub)
			}
		}
		pub = append(pub, p)
	}

	decision := StrategyDecision{}
	if bestIdx >= 0 {
		decision.Selected = pub[bestIdx]
		decision.Reasoning = fmt.Sprintf("selected %s: %s", decision.Selected.Strategy, decision.Selected.Reason)
	} else {
		decision.Reasoning = "no executable strategy fits the constraints; see rejected candidates for why"
	}
	for i := range pub {
		if i == bestIdx {
			continue
		}
		rc := pub[i]
		if !rc.Rejected {
			rc.Rejected = true
			rc.RejectReason = fmt.Sprintf("lower score than selected (%d < %d)", rc.Score, decision.Selected.Score)
		}
		decision.Rejected = append(decision.Rejected, rc)
	}
	return decision
}

// candidatesFor enumerates the ordered candidate strategies for a workload.
func candidatesFor(c SelectorConstraints) []scoredCandidate {
	switch {
	case isDatabaseWorkload(c.Workload):
		return databaseCandidates(c)
	case c.Workload == driver.WorkloadFilesystem:
		return filesystemCandidates()
	default:
		return []scoredCandidate{manualCandidate(c.Workload)}
	}
}

// databaseCandidates builds the replication-then-dump candidate list for a DB
// workload. Replication is offered only when the driver catalog confirms an
// implemented strategy for the engine; the dump is always catalogued but marked
// non-executable so it is shown yet never selected for a real migration.
func databaseCandidates(c SelectorConstraints) []scoredCandidate {
	var cands []scoredCandidate

	if impl, ok := driver.SelectDBStrategy(c.Workload); ok {
		cands = append(cands, scoredCandidate{
			strategy:          StrategyDatabaseReplication,
			feasibleForExec:   true,
			continuous:        true,
			downtimeSeconds:   replicationCutoverSeconds,
			riskLevel:         "low",
			rollbackAvailable: true,
			manualSteps: []string{
				"Set up replication on the target",
				"Verify replication lag is near zero before cutover",
				"Stop writes, run a final sync, then promote the target",
			},
			note: fmt.Sprintf("continuous %s replication (%s); brief cutover window, not zero-downtime",
				impl.Mechanism, impl.Name),
		})
	}

	if dump, ok := dumpStrategyFor(c.Workload); ok {
		strategy := StrategyColdMigration
		if c.DataSizeGB > 100 {
			strategy = StrategyWarmMigration
		}
		cands = append(cands, scoredCandidate{
			strategy:          strategy,
			feasibleForExec:   false,
			downtimeSeconds:   dumpDowntimeSeconds(c.DataSizeGB, c.BandwidthMbps),
			riskLevel:         "medium",
			rollbackAvailable: true,
			manualSteps: []string{
				"Stop writes to the source database",
				fmt.Sprintf("Run %s and restore on the target", dump.Mechanism),
				"Verify row counts / checksums before cutover",
			},
			note: "logical dump/restore; full downtime scaling with data size; no orchestrated execution path yet",
		})
	}

	return cands
}

// filesystemCandidates returns the single rsync candidate. Filesystem transfer
// is executed by the real transfer layer (not the driver skeleton), so it is
// feasible for execution here.
func filesystemCandidates() []scoredCandidate {
	return []scoredCandidate{{
		strategy:          StrategyLiveSync,
		feasibleForExec:   true,
		downtimeSeconds:   filesystemFinalSyncSeconds,
		riskLevel:         "low",
		rollbackAvailable: true,
		manualSteps: []string{
			"Run the bulk incremental rsync while the source stays live",
			"Quiesce the source (or accept eventual consistency) for the final sync",
			"Checksum-verify transferred files before cutover",
		},
		note: "rsync incremental sync with checksum verification; brief final-sync window, not zero-downtime",
	}}
}

// manualCandidate is the non-executable fallback for workloads with no
// automatic strategy (docker_compose is not yet orchestrated; anything else is
// unrecognized).
func manualCandidate(wt driver.WorkloadType) scoredCandidate {
	note := fmt.Sprintf("unrecognized workload %q; manual assessment required", wt)
	if wt == driver.WorkloadDockerCompose {
		note = "docker_compose migration is not yet orchestrated; manual steps required"
	}
	return scoredCandidate{
		strategy:          StrategyManualCutover,
		feasibleForExec:   false,
		downtimeSeconds:   -1,
		riskLevel:         "high",
		rollbackAvailable: false,
		manualSteps: []string{
			"Document the current architecture",
			"Plan and test the migration manually in staging",
		},
		note: note,
	}
}

// dumpStrategyFor finds the descriptor-maturity, full-downtime dump strategy for
// a database workload in the driver catalog.
func dumpStrategyFor(wt driver.WorkloadType) (driver.DBStrategy, bool) {
	for _, s := range driver.DBStrategiesFor(wt) {
		if s.Maturity == driver.MaturityDescriptor && s.Downtime == driver.DowntimeFull {
			return s, true
		}
	}
	return driver.DBStrategy{}, false
}

// isDatabaseWorkload reports whether the workload is one of the catalogued DB
// engines.
func isDatabaseWorkload(wt driver.WorkloadType) bool {
	switch wt {
	case driver.WorkloadMySQL, driver.WorkloadPostgreSQL, driver.WorkloadRedis:
		return true
	default:
		return false
	}
}

// effectiveBudget folds RTO into the downtime budget: the effective ceiling is
// the smaller of the two, ignoring zeros (which mean "unset"). Zero means
// unlimited.
func effectiveBudget(c SelectorConstraints) int {
	b := c.DowntimeBudgetSeconds
	if c.RTOSeconds > 0 && (b == 0 || c.RTOSeconds < b) {
		b = c.RTOSeconds
	}
	return b
}

// dumpDowntimeSeconds estimates full-downtime for a dump/restore: transfer time
// (from size and bandwidth) plus dump+restore overhead and a fixed floor. With
// unknown bandwidth it falls back to a conservative per-GB rate.
func dumpDowntimeSeconds(dataGB, bwMbps float64) int {
	if dataGB <= 0 {
		dataGB = 1
	}
	var transfer float64
	if bwMbps > 0 {
		transfer = dataGB * 8000.0 / bwMbps // GB→gigabits→megabits / Mbps = seconds
	} else {
		transfer = dataGB * 120.0 // ~120s/GB conservative fallback
	}
	return int(math.Ceil(transfer*2 + 60)) // dump + restore ≈ 2× transfer, +60s floor
}

// scoreCandidate computes a deterministic integer score. Components:
//
//	base                                            50
//	downtime  <=60s +40 | <=10m +20 | <=1h +5       (lower is better)
//	risk      low   +15 | medium +5                 (safer is better)
//	rollback  +15 when caller can roll back and the strategy supports it
//	RPO       +10 for continuous replication under a tight (<=60s) RPO
func scoreCandidate(sc scoredCandidate, c SelectorConstraints) int {
	s := 50
	switch {
	case sc.downtimeSeconds >= 0 && sc.downtimeSeconds <= 60:
		s += 40
	case sc.downtimeSeconds <= 600:
		s += 20
	case sc.downtimeSeconds <= 3600:
		s += 5
	}
	switch sc.riskLevel {
	case "low":
		s += 15
	case "medium":
		s += 5
	}
	if c.CanRollback && sc.rollbackAvailable {
		s += 15
	}
	if sc.continuous && c.RPOSeconds > 0 && c.RPOSeconds <= 60 {
		s += 10
	}
	return s
}

// downtimeString renders a downtime estimate. Negative means unknown.
func downtimeString(s int) string {
	if s < 0 {
		return "unknown — manual assessment needed"
	}
	switch {
	case s < 60:
		return fmt.Sprintf("~%ds", s)
	case s < 3600:
		return fmt.Sprintf("~%dm", (s+59)/60)
	default:
		return fmt.Sprintf("~%dh", (s+3599)/3600)
	}
}
