package planner

import "testing"

func TestSelectStrategy(t *testing.T) {
	testCases := []struct {
		name  string
		input StrategyInput
		want  MigrationStrategy
	}{
		{
			name: "database with replication",
			input: StrategyInput{
				WorkloadType:   "database",
				HasDatabase:    true,
				DatabaseType:   "postgresql",
				HasReplication: true,
			},
			want: StrategyDatabaseReplication,
		},
		{
			name: "queue workload",
			input: StrategyInput{
				WorkloadType: "queue",
				HasQueue:     true,
				QueueType:    "redis",
			},
			want: StrategyQueueDrain,
		},
		{
			name: "reverse proxy with traffic provider",
			input: StrategyInput{
				WorkloadType:       "reverse_proxy",
				HasReverseProxy:    true,
				ReverseProxyType:   "nginx",
				HasTrafficProvider: true,
				TrafficProvider:    "cloudflare",
			},
			want: StrategyReverseProxyCutover,
		},
		{
			name: "small database without replication",
			input: StrategyInput{
				WorkloadType:   "database",
				HasDatabase:    true,
				DatabaseType:   "mysql",
				HasReplication: false,
				DataSizeGB:     12,
			},
			want: StrategyColdMigration,
		},
		{
			name: "large database without replication",
			input: StrategyInput{
				WorkloadType:   "database",
				HasDatabase:    true,
				DatabaseType:   "postgresql",
				HasReplication: false,
				DataSizeGB:     250,
			},
			want: StrategyWarmMigration,
		},
		{
			name: "stateless app with reverse proxy",
			input: StrategyInput{
				WorkloadType:    "stateless_application",
				HasReverseProxy: true,
			},
			want: StrategyBlueGreenPrep,
		},
		{
			name: "stateless app without reverse proxy",
			input: StrategyInput{
				WorkloadType: "stateless_application",
			},
			want: StrategyLiveSync,
		},
		{
			name: "stateful app",
			input: StrategyInput{
				WorkloadType: "stateful_application",
				CanRollback:  true,
			},
			want: StrategyWarmMigration,
		},
		{
			name: "cache",
			input: StrategyInput{
				WorkloadType: "cache",
			},
			want: StrategyLiveSync,
		},
		{
			name: "unknown workload",
			input: StrategyInput{
				WorkloadType: "unknown",
			},
			want: StrategyManualCutover,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectStrategy(tc.input)
			if got.Strategy != tc.want {
				t.Fatalf("SelectStrategy() strategy = %s, want %s", got.Strategy, tc.want)
			}
			if got.Reasoning == "" {
				t.Fatal("SelectStrategy() reasoning is empty")
			}
			if got.EstimatedDowntime == "" {
				t.Fatal("SelectStrategy() estimated downtime is empty")
			}
		})
	}
}

func TestSelectStrategy_AllStrategiesHaveReasoningAndDowntime(t *testing.T) {
	testCases := []StrategyInput{
		{WorkloadType: "database", HasDatabase: true, DatabaseType: "postgresql", HasReplication: true},
		{WorkloadType: "queue", HasQueue: true},
		{WorkloadType: "reverse_proxy", HasReverseProxy: true, HasTrafficProvider: true, ReverseProxyType: "nginx", TrafficProvider: "cloudflare"},
		{WorkloadType: "database", HasDatabase: true, DatabaseType: "mysql", DataSizeGB: 25},
		{WorkloadType: "database", HasDatabase: true, DatabaseType: "postgresql", DataSizeGB: 250},
		{WorkloadType: "stateless_application", HasReverseProxy: true},
		{WorkloadType: "stateless_application"},
		{WorkloadType: "stateful_application"},
		{WorkloadType: "cache"},
		{WorkloadType: "unknown"},
	}

	for i, input := range testCases {
		got := SelectStrategy(input)
		if got.Reasoning == "" {
			t.Fatalf("case %d: reasoning is empty", i)
		}
		if got.EstimatedDowntime == "" {
			t.Fatalf("case %d: estimated downtime is empty", i)
		}
	}
}
