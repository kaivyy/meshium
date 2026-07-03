package planner

import "testing"

func TestGenerateWarnings_ColdMigration(t *testing.T) {
	strategy := StrategySelection{
		Strategy:          StrategyColdMigration,
		RollbackAvailable: true,
	}

	warnings := GenerateWarnings("database", strategy, nil)
	if len(warnings) != 1 {
		t.Fatalf("GenerateWarnings() len = %d, want 1", len(warnings))
	}

	warning := warnings[0]
	if warning.Type != WarningTypeRisk {
		t.Fatalf("warning type = %s, want %s", warning.Type, WarningTypeRisk)
	}
	if warning.Severity != "warning" {
		t.Fatalf("warning severity = %s, want warning", warning.Severity)
	}
	if warning.Blocking {
		t.Fatal("cold migration warning should not be blocking")
	}
	if warning.RiskScore < 0 || warning.RiskScore > 100 {
		t.Fatalf("warning risk score = %v, want 0-100", warning.RiskScore)
	}
}

func TestGenerateWarnings_UnsupportedWorkload(t *testing.T) {
	strategy := StrategySelection{
		Strategy:          StrategyManualCutover,
		RollbackAvailable: false,
	}

	warnings := GenerateWarnings("unknown", strategy, nil)
	if len(warnings) != 2 {
		t.Fatalf("GenerateWarnings() len = %d, want 2", len(warnings))
	}

	if warnings[0].Type != WarningTypeUnsupported {
		t.Fatalf("first warning type = %s, want %s", warnings[0].Type, WarningTypeUnsupported)
	}
	if !warnings[0].Blocking {
		t.Fatal("unsupported workload warning should be blocking")
	}
	if warnings[1].Type != WarningTypeRollbackNote {
		t.Fatalf("second warning type = %s, want %s", warnings[1].Type, WarningTypeRollbackNote)
	}
}

func TestGenerateWarnings_RollbackNotAvailable(t *testing.T) {
	strategy := StrategySelection{
		Strategy:          StrategyManualCutover,
		RollbackAvailable: false,
	}

	warnings := GenerateWarnings("custom", strategy, nil)
	if len(warnings) != 1 {
		t.Fatalf("GenerateWarnings() len = %d, want 1", len(warnings))
	}
	if warnings[0].Type != WarningTypeRollbackNote {
		t.Fatalf("warning type = %s, want %s", warnings[0].Type, WarningTypeRollbackNote)
	}
	if warnings[0].Blocking {
		t.Fatal("rollback note should not be blocking")
	}
}

func TestGenerateWarnings_WithIssues(t *testing.T) {
	strategy := StrategySelection{
		Strategy:          StrategyColdMigration,
		RollbackAvailable: true,
	}
	issues := []string{"backup is stale", "target disk space is low"}

	warnings := GenerateWarnings("database", strategy, issues)
	if len(warnings) != 3 {
		t.Fatalf("GenerateWarnings() len = %d, want 3", len(warnings))
	}

	if warnings[1].Message != issues[0] {
		t.Fatalf("warning[1] message = %q, want %q", warnings[1].Message, issues[0])
	}
	if warnings[2].Message != issues[1] {
		t.Fatalf("warning[2] message = %q, want %q", warnings[2].Message, issues[1])
	}
}

func TestGenerateWarnings_SeverityLevels(t *testing.T) {
	strategy := StrategySelection{
		Strategy:          StrategyManualCutover,
		RollbackAvailable: false,
	}
	warnings := GenerateWarnings("unknown", strategy, []string{"manual review required"})

	for i, warning := range warnings {
		switch warning.Severity {
		case "info", "warning", "error", "critical":
			// valid
		default:
			t.Fatalf("warning[%d] severity = %q, want a valid severity level", i, warning.Severity)
		}
	}
}

func TestGenerateWarnings_BlockingWarningsAreMarked(t *testing.T) {
	cases := []struct {
		name     string
		workload string
		strategy StrategySelection
		typeWant PlannerWarningType
	}{
		{
			name:     "database replication",
			workload: "database",
			strategy: StrategySelection{
				Strategy:          StrategyDatabaseReplication,
				RollbackAvailable: true,
			},
			typeWant: WarningTypeVerification,
		},
		{
			name:     "queue drain",
			workload: "queue",
			strategy: StrategySelection{
				Strategy:          StrategyQueueDrain,
				RollbackAvailable: true,
			},
			typeWant: WarningTypeManualStep,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := GenerateWarnings(tc.workload, tc.strategy, nil)
			if len(warnings) != 1 {
				t.Fatalf("GenerateWarnings() len = %d, want 1", len(warnings))
			}
			if warnings[0].Type != tc.typeWant {
				t.Fatalf("warning type = %s, want %s", warnings[0].Type, tc.typeWant)
			}
			if !warnings[0].Blocking {
				t.Fatal("expected warning to be blocking")
			}
		})
	}
}

func TestGenerateWarnings_RiskScoresInRange(t *testing.T) {
	warningSets := [][]PlannerWarning{
		GenerateWarnings("database", StrategySelection{Strategy: StrategyColdMigration, RollbackAvailable: true}, []string{"issue"}),
		GenerateWarnings("unknown", StrategySelection{Strategy: StrategyManualCutover, RollbackAvailable: false}, []string{"issue"}),
		GenerateWarnings("custom", StrategySelection{Strategy: StrategyManualCutover, RollbackAvailable: false}, nil),
		GenerateWarnings("database", StrategySelection{Strategy: StrategyDatabaseReplication, RollbackAvailable: true}, nil),
		GenerateWarnings("queue", StrategySelection{Strategy: StrategyQueueDrain, RollbackAvailable: true}, nil),
	}

	for setIndex, warnings := range warningSets {
		for warningIndex, warning := range warnings {
			if warning.RiskScore < 0 || warning.RiskScore > 100 {
				t.Fatalf("warning set %d item %d risk score = %v, want 0-100", setIndex, warningIndex, warning.RiskScore)
			}
		}
	}
}
