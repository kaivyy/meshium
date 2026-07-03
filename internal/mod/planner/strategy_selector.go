package planner

import "fmt"

// MigrationStrategy represents a migration strategy.
type MigrationStrategy string

const (
	StrategyColdMigration       MigrationStrategy = "cold_migration"
	StrategyWarmMigration       MigrationStrategy = "warm_migration"
	StrategyLiveSync            MigrationStrategy = "live_sync"
	StrategyShadowDeployment    MigrationStrategy = "shadow_deployment"
	StrategyBlueGreenPrep       MigrationStrategy = "blue_green_preparation"
	StrategyManualCutover       MigrationStrategy = "manual_cutover"
	StrategyReverseProxyCutover MigrationStrategy = "reverse_proxy_cutover"
	StrategyDNSCutover          MigrationStrategy = "dns_cutover"
	StrategyDatabaseReplication MigrationStrategy = "database_replication"
	StrategyQueueDrain          MigrationStrategy = "queue_drain"
)

// StrategySelection holds the result of strategy selection.
type StrategySelection struct {
	Strategy          MigrationStrategy `json:"strategy"`
	Reasoning         string            `json:"reasoning"`
	EstimatedDowntime string            `json:"estimatedDowntime"`
	RiskLevel         string            `json:"riskLevel"` // "low", "medium", "high"
	RollbackAvailable bool              `json:"rollbackAvailable"`
	Requirements      []string          `json:"requirements,omitempty"`
	Prerequisites     []string          `json:"prerequisites,omitempty"`
}

// StrategyInput holds the input for strategy selection.
type StrategyInput struct {
	WorkloadType       string  `json:"workloadType"`
	HasDatabase        bool    `json:"hasDatabase"`
	DatabaseType       string  `json:"databaseType,omitempty"`
	HasReplication     bool    `json:"hasReplication"`
	HasQueue           bool    `json:"hasQueue"`
	QueueType          string  `json:"queueType,omitempty"`
	HasReverseProxy    bool    `json:"hasReverseProxy"`
	ReverseProxyType   string  `json:"reverseProxyType,omitempty"`
	HasTrafficProvider bool    `json:"hasTrafficProvider"`
	TrafficProvider    string  `json:"trafficProvider,omitempty"`
	DataSizeGB         float64 `json:"dataSizeGb"`
	RiskScore          float64 `json:"riskScore"`
	CanRollback        bool    `json:"canRollback"`
	HasSSL             bool    `json:"hasSSL"`
}

// SelectStrategy selects the best migration strategy for a workload.
func SelectStrategy(input StrategyInput) StrategySelection {
	// Decision tree:

	// 1. Database with replication support → Database Replication
	if input.HasDatabase && input.HasReplication {
		return StrategySelection{
			Strategy:          StrategyDatabaseReplication,
			Reasoning:         fmt.Sprintf("%s database with replication support — use replication for near-zero downtime", input.DatabaseType),
			EstimatedDowntime: "< 1 minute",
			RiskLevel:         "low",
			RollbackAvailable: true,
			Requirements:      []string{"Replication must be healthy", "Lag must be near zero before cutover"},
			Prerequisites:     []string{"Set up replication on target", "Verify replication lag < 1s"},
		}
	}

	// 2. Queue workload → Queue Drain
	if input.WorkloadType == "queue" || input.HasQueue {
		return StrategySelection{
			Strategy:          StrategyQueueDrain,
			Reasoning:         "Queue workload detected — drain queue, pause workers, sync, resume",
			EstimatedDowntime: "1-5 minutes",
			RiskLevel:         "medium",
			RollbackAvailable: true,
			Requirements:      []string{"All workers must be paused", "Queue must be drained before migration"},
			Prerequisites:     []string{"Identify all queue workers", "Plan worker pause/resume order"},
		}
	}

	// 3. Reverse proxy with traffic provider → Reverse Proxy Cutover
	if input.WorkloadType == "reverse_proxy" || (input.HasReverseProxy && input.HasTrafficProvider) {
		return StrategySelection{
			Strategy:          StrategyReverseProxyCutover,
			Reasoning:         fmt.Sprintf("Reverse proxy (%s) with traffic provider (%s) — switch traffic at proxy level", input.ReverseProxyType, input.TrafficProvider),
			EstimatedDowntime: "< 30 seconds",
			RiskLevel:         "low",
			RollbackAvailable: true,
			Requirements:      []string{"Target must be provisioned and verified", "Health checks must pass"},
			Prerequisites:     []string{"Configure reverse proxy on target", "Set up health check endpoint"},
		}
	}

	// 4. Database without replication → Warm Migration
	if input.HasDatabase && !input.HasReplication {
		if input.DataSizeGB > 100 {
			return StrategySelection{
				Strategy:          StrategyWarmMigration,
				Reasoning:         fmt.Sprintf("Large %s database (%.0f GB) without replication — warm migration with logical backup", input.DatabaseType, input.DataSizeGB),
				EstimatedDowntime: "5-30 minutes",
				RiskLevel:         "medium",
				RollbackAvailable: true,
				Requirements:      []string{"Database must be set to read-only during final sync", "Logical backup must complete before cutover"},
				Prerequisites:     []string{"Install database on target", "Test logical backup/restore timing"},
			}
		}
		return StrategySelection{
			Strategy:          StrategyColdMigration,
			Reasoning:         fmt.Sprintf("Small %s database (%.0f GB) without replication — cold migration with downtime", input.DatabaseType, input.DataSizeGB),
			EstimatedDowntime: "5-60 minutes",
			RiskLevel:         "medium",
			RollbackAvailable: true,
			Requirements:      []string{"Application must be stopped during migration", "Database dump must be verified"},
			Prerequisites:     []string{"Install database on target", "Verify disk space on target"},
		}
	}

	// 5. Stateless application → Live Sync or Shadow Deployment
	if input.WorkloadType == "stateless_application" {
		if input.HasReverseProxy {
			return StrategySelection{
				Strategy:          StrategyBlueGreenPrep,
				Reasoning:         "Stateless application with reverse proxy — blue-green deployment with traffic switch",
				EstimatedDowntime: "< 10 seconds",
				RiskLevel:         "low",
				RollbackAvailable: true,
				Requirements:      []string{"Target application must be healthy", "Reverse proxy must support upstream switching"},
				Prerequisites:     []string{"Deploy application on target", "Configure reverse proxy upstream"},
			}
		}
		return StrategySelection{
			Strategy:          StrategyLiveSync,
			Reasoning:         "Stateless application — deploy on target and switch traffic",
			EstimatedDowntime: "< 30 seconds",
			RiskLevel:         "low",
			RollbackAvailable: true,
			Requirements:      []string{"Application must be stateless", "Session state must be externalized"},
		}
	}

	// 6. Stateful application → Warm Migration
	if input.WorkloadType == "stateful_application" {
		return StrategySelection{
			Strategy:          StrategyWarmMigration,
			Reasoning:         "Stateful application — migrate data, verify, then switch",
			EstimatedDowntime: "1-10 minutes",
			RiskLevel:         "medium",
			RollbackAvailable: input.CanRollback,
			Requirements:      []string{"Data must be synced", "Application state must be preserved", "Verify data integrity after sync"},
			Prerequisites:     []string{"Set up data sync mechanism", "Plan verification queries"},
		}
	}

	// 7. Cache → can be rebuilt
	if input.WorkloadType == "cache" {
		return StrategySelection{
			Strategy:          StrategyLiveSync,
			Reasoning:         "Cache workload — can be rebuilt on target, no data migration needed",
			EstimatedDowntime: "< 5 seconds",
			RiskLevel:         "low",
			RollbackAvailable: true,
			Requirements:      []string{"Cache must be warmable", "Application must handle cache misses gracefully"},
		}
	}

	// 8. Default → Manual Cutover
	return StrategySelection{
		Strategy:          StrategyManualCutover,
		Reasoning:         fmt.Sprintf("Unknown/unsupported workload type '%s' — manual cutover required", input.WorkloadType),
		EstimatedDowntime: "Unknown — manual assessment needed",
		RiskLevel:         "high",
		RollbackAvailable: false,
		Requirements:      []string{"Manual assessment required", "Test migration in staging first"},
		Prerequisites:     []string{"Document current architecture", "Plan migration steps manually"},
	}
}
