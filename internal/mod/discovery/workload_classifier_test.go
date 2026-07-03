package discovery

import "testing"

func TestClassifyWorkloads(t *testing.T) {
	tests := []struct {
		name     string
		snapshot *ServerSnapshot
		expected WorkloadType
	}{
		{
			name: "mysql database",
			snapshot: &ServerSnapshot{
				Databases: []DatabaseInfo{{Type: "mysql", Port: 3306, Running: true}},
			},
			expected: WorkloadDatabase,
		},
		{
			name: "redis cache",
			snapshot: &ServerSnapshot{
				Databases: []DatabaseInfo{{Type: "redis", RedisUsage: "cache", Port: 6379, Running: true}},
			},
			expected: WorkloadCache,
		},
		{
			name: "redis bullmq queue",
			snapshot: &ServerSnapshot{
				Databases: []DatabaseInfo{{Type: "redis", RedisUsage: "bullmq", Port: 6379, Running: true}},
			},
			expected: WorkloadQueue,
		},
		{
			name: "nginx container",
			snapshot: &ServerSnapshot{
				Docker: &DockerInfo{Containers: []ContainerInfo{{
					Name:    "nginx",
					Image:   "nginx:1.25",
					Ports:   []PortMapping{{ContainerPort: 80, HostPort: 80}},
					Volumes: []string{},
				}}},
			},
			expected: WorkloadReverseProxy,
		},
		{
			name: "stateful app container",
			snapshot: &ServerSnapshot{
				Docker: &DockerInfo{Containers: []ContainerInfo{{
					Name:    "webapp",
					Image:   "myapp:1.0",
					Ports:   []PortMapping{{ContainerPort: 3000, HostPort: 3000}},
					Volumes: []string{"/data/app:/app/data"},
				}}},
			},
			expected: WorkloadStatefulApp,
		},
		{
			name: "stateless app container",
			snapshot: &ServerSnapshot{
				Docker: &DockerInfo{Containers: []ContainerInfo{{
					Name:  "webapp",
					Image: "myapp:1.0",
					Ports: []PortMapping{{ContainerPort: 3000, HostPort: 3000}},
				}}},
			},
			expected: WorkloadStatelessApp,
		},
		{
			name: "rabbitmq queue",
			snapshot: &ServerSnapshot{
				MessageQueues: []MessageQueueInfo{{Type: "rabbitmq", Port: 5672, Running: true}},
			},
			expected: WorkloadQueue,
		},
		{
			name: "prometheus monitoring",
			snapshot: &ServerSnapshot{
				Monitoring: []MonitoringServiceInfo{{Type: "prometheus", Port: 9090, Running: true}},
			},
			expected: WorkloadMonitoring,
		},
		{
			name: "cron scheduler",
			snapshot: &ServerSnapshot{
				CronJobs: []CronJobInfo{{User: "root", Schedule: "0 * * * *", Command: "/usr/local/bin/backup.sh", Source: "backup"}},
			},
			expected: WorkloadScheduler,
		},
		{
			name: "unknown container",
			snapshot: &ServerSnapshot{
				Docker: &DockerInfo{Containers: []ContainerInfo{{
					Name:  "helper",
					Image: "busybox:latest",
				}}},
			},
			expected: WorkloadUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := ClassifyWorkloads(tt.snapshot)
			if len(results) != 1 {
				t.Fatalf("expected 1 classification, got %d", len(results))
			}

			classification := results[0]
			if classification.Type != tt.expected {
				t.Fatalf("expected workload type %q, got %q", tt.expected, classification.Type)
			}
			if classification.Confidence < 0 || classification.Confidence > 1 {
				t.Fatalf("expected confidence between 0 and 1, got %f", classification.Confidence)
			}
			if len(classification.Reasons) == 0 {
				t.Fatal("expected at least one classification reason")
			}
		})
	}
}

func TestBuildDependencyGraphSetsWorkloadTypes(t *testing.T) {
	snapshot := &ServerSnapshot{
		Databases: []DatabaseInfo{{Type: "mysql", Port: 3306, Running: true}},
		Docker: &DockerInfo{Containers: []ContainerInfo{{
			Name:  "webapp",
			Image: "myapp:1.0",
			Ports: []PortMapping{{ContainerPort: 3000, HostPort: 3000}},
		}}},
	}

	graph := BuildDependencyGraph(snapshot)
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}

	var foundContainer, foundDatabase bool
	for _, node := range graph.Nodes {
		switch node.ID {
		case "container:webapp":
			foundContainer = true
			if node.WorkloadType != WorkloadStatelessApp {
				t.Fatalf("expected container workload type %q, got %q", WorkloadStatelessApp, node.WorkloadType)
			}
		case "database:mysql":
			foundDatabase = true
			if node.WorkloadType != WorkloadDatabase {
				t.Fatalf("expected database workload type %q, got %q", WorkloadDatabase, node.WorkloadType)
			}
		}
	}

	if !foundContainer {
		t.Fatal("expected container node in graph")
	}
	if !foundDatabase {
		t.Fatal("expected database node in graph")
	}
}
