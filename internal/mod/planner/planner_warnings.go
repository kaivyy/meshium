package planner

// PlannerWarningType represents the type of planner warning.
type PlannerWarningType string

const (
	WarningTypeRisk           PlannerWarningType = "risk"
	WarningTypeRecommendation PlannerWarningType = "recommendation"
	WarningTypeBlocking       PlannerWarningType = "blocking"
	WarningTypeManualStep     PlannerWarningType = "manual_step"
	WarningTypeVerification   PlannerWarningType = "verification_step"
	WarningTypeRollbackNote   PlannerWarningType = "rollback_note"
	WarningTypeUnsupported    PlannerWarningType = "unsupported_workload"
)

// PlannerWarning represents a warning or note from the planner.
type PlannerWarning struct {
	Type           PlannerWarningType `json:"type"`
	Severity       string             `json:"severity"` // "info", "warning", "error", "critical"
	Message        string             `json:"message"`
	Recommendation string             `json:"recommendation,omitempty"`
	Blocking       bool               `json:"blocking"`
	RiskScore      float64            `json:"riskScore"` // 0-100
	ManualAction   string             `json:"manualAction,omitempty"`
	WorkloadName   string             `json:"workloadName,omitempty"`
	Category       string             `json:"category,omitempty"`
}

// GenerateWarnings generates warnings for a given workload and strategy.
func GenerateWarnings(workloadType string, strategy StrategySelection, issues []string) []PlannerWarning {
	var warnings []PlannerWarning

	// Add strategy-specific warnings
	switch strategy.Strategy {
	case StrategyColdMigration:
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeRisk,
			Severity:       "warning",
			Message:        "Cold migration requires planned downtime",
			Recommendation: "Schedule migration during maintenance window",
			Blocking:       false,
			RiskScore:      30,
		})
	case StrategyWarmMigration:
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeVerification,
			Severity:       "info",
			Message:        "Warm migration requires data verification after sync",
			Recommendation: "Run checksums or row count verification on critical tables",
			Blocking:       false,
			RiskScore:      10,
		})
	case StrategyDatabaseReplication:
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeVerification,
			Severity:       "info",
			Message:        "Database replication must be verified before cutover",
			Recommendation: "Check replication lag is < 1 second before proceeding",
			Blocking:       true,
			RiskScore:      5,
			ManualAction:   "Verify replication lag on target: SELECT now() - pg_last_xact_replay_timestamp()",
		})
	case StrategyQueueDrain:
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeManualStep,
			Severity:       "warning",
			Message:        "Queue workers must be paused before migration",
			Recommendation: "Pause all workers, wait for queue to drain, then proceed",
			Blocking:       true,
			RiskScore:      20,
			ManualAction:   "Identify and pause all queue workers before starting migration",
		})
	}

	// Add unsupported workload warnings
	if workloadType == "unknown" {
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeUnsupported,
			Severity:       "error",
			Message:        "Unsupported or unrecognized workload detected",
			Recommendation: "This workload type is not automatically supported. Manual migration steps required.",
			Blocking:       true,
			RiskScore:      80,
			ManualAction:   "Review workload manually and create custom migration plan",
		})
	}

	// Add rollback note if rollback not available
	if !strategy.RollbackAvailable {
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeRollbackNote,
			Severity:       "critical",
			Message:        "No automatic rollback available for this migration strategy",
			Recommendation: "Ensure you have manual rollback procedures in place",
			Blocking:       false,
			RiskScore:      60,
			ManualAction:   "Document manual rollback steps before starting migration",
		})
	}

	// Add warnings for each issue
	for _, issue := range issues {
		warnings = append(warnings, PlannerWarning{
			Type:           WarningTypeRisk,
			Severity:       "warning",
			Message:        issue,
			Recommendation: "Review and resolve before migration",
			Blocking:       false,
			RiskScore:      40,
		})
	}

	return warnings
}
