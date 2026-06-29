package discovery

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// --- Mock SSH Executer ---

// mockSSH is a configurable SSHExecuter for testing discovery collectors.
type mockSSH struct {
	mu         sync.Mutex
	responses  map[string]string // cmd prefix → stdout
	errors     map[string]error  // cmd prefix → error
	exitCodes  map[string]int    // cmd prefix → exit code
	alive      bool
}

func newMockSSH() *mockSSH {
	return &mockSSH{
		responses: make(map[string]string),
		errors:    make(map[string]error),
		exitCodes: make(map[string]int),
		alive:      true,
	}
}

// setResponse sets the stdout for a command prefix.
func (m *mockSSH) setResponse(cmdPrefix, stdout string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[cmdPrefix] = stdout
}

// setError sets an error for a command prefix.
func (m *mockSSH) setError(cmdPrefix string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[cmdPrefix] = err
}

// setExitCode sets a non-zero exit code for a command prefix.
func (m *mockSSH) setExitCode(cmdPrefix string, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exitCodes[cmdPrefix] = code
}

func (m *mockSSH) Exec(cmd string) (string, string, int, error) {
	return m.ExecContext(context.Background(), cmd)
}

func (m *mockSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for exact match first
	if out, ok := m.responses[cmd]; ok {
		if err, ok := m.errors[cmd]; ok {
			return out, "", 1, err
		}
		if code, ok := m.exitCodes[cmd]; ok {
			return out, "", code, nil
		}
		return out, "", 0, nil
	}

	// Check for prefix match (longest prefix first)
	var bestPrefix string
	for prefix := range m.responses {
		if strings.HasPrefix(cmd, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix = prefix
		}
	}
	if bestPrefix != "" {
		out := m.responses[bestPrefix]
		if err, ok := m.errors[bestPrefix]; ok {
			return out, "", 1, err
		}
		if code, ok := m.exitCodes[bestPrefix]; ok {
			return out, "", code, nil
		}
		return out, "", 0, nil
	}

	// Check for error prefix match (longest prefix first)
	var bestErrPrefix string
	for prefix := range m.errors {
		if strings.HasPrefix(cmd, prefix) && len(prefix) > len(bestErrPrefix) {
			bestErrPrefix = prefix
		}
	}
	if bestErrPrefix != "" {
		return "", "", 1, m.errors[bestErrPrefix]
	}

	return "", "", 0, nil
}

func (m *mockSSH) IsAlive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alive
}

func (m *mockSSH) Upload(src io.Reader, remotePath string) error  { return nil }
func (m *mockSSH) Download(remotePath string, dst io.Writer) error { return nil }

// --- Test Helpers ---

// setupFullMockSSH creates a mock SSH with all collector responses.
func setupFullMockSSH() *mockSSH {
	m := newMockSSH()

	// OS collector
	m.setResponse("hostname", "web-server-01\n")
	m.setResponse(`cat /etc/os-release | grep PRETTY_NAME`, `PRETTY_NAME="Ubuntu 22.04.3 LTS"`+"\n")
	m.setResponse("uname -r", "5.15.0-91-generic\n")
	m.setResponse("uname -m", "x86_64\n")
	m.setResponse(`timedatectl | grep "Time zone"`, `               Time zone: Asia/Jakarta (WIB, +0700)`+"\n")
	m.setResponse(`cat /proc/uptime | awk '{print int($1)}'`, "86400\n")
	m.setResponse("systemd-detect-virt", "kvm\n")

	// Hardware collector
	m.setResponse(`lscpu | grep "Model name"`, "Model name:                      Intel(R) Xeon(R) Platinum 8275CL CPU @ 3.00GHz\n")
	m.setResponse("nproc", "4\n")
	m.setResponse("free -m", "               total        used        free      shared  buff/cache   available\nMem:           8192        4096        2048         256        2048        3584\nSwap:          1024           0        1024\n")
	m.setResponse(`df -BG / | awk 'NR==2{print $2, $3}'`, "100G 50G\n")

	// Disk collector
	m.setResponse(`df -BG | awk`, "/dev/sda1 / 100G 50G 50G 50%\n/dev/sdb1 /data 200G 100G 100G 50%\n")

	// Port collector — output after awk (print $4, $6)
	m.setResponse(`ss -tlnp 2>/dev/null | awk`, "0.0.0.0:80 users:((\"nginx\",pid=1234,fd=6))\n0.0.0.0:443 users:((\"nginx\",pid=1234,fd=7))\n127.0.0.1:3306 users:((\"mysqld\",pid=5678,fd=10))\n")

	// Docker collector
	m.setResponse("which docker", "/usr/bin/docker\n")
	m.setResponse("docker version", "24.0.7\n")
	m.setResponse(`docker ps -a --format`, `{"Names":"webapp","Image":"node:18","Status":"Up 3 days","State":"running","Ports":"0.0.0.0:3000->3000/tcp","Labels":"com.docker.compose.project=myapp,com.docker.compose.service=web","Networks":"myapp_default","Mounts":"/data/app"}`+"\n"+`{"Names":"db","Image":"mysql:8","Status":"Up 3 days","State":"running","Ports":"0.0.0.0:3306->3306/tcp","Labels":"com.docker.compose.project=myapp,com.docker.compose.service=db","Networks":"myapp_default","Mounts":"/data/mysql"}`+"\n")
	m.setResponse(`docker images --format`, `{"Repository":"node","Tag":"18","ID":"abc123","Size":"350MB"}`+"\n"+`{"Repository":"mysql","Tag":"8","ID":"def456","Size":"520MB"}`+"\n")

	// Service collector
	m.setResponse(`systemctl list-units --type=service`, "nginx.service\nmysql.service\nssh.service\ndocker.service\n")
	m.setResponse(`systemctl show 'nginx.service'`, "Description=The nginx HTTP and reverse proxy server\nLoadState=loaded\nActiveState=active\nSubState=running\nType=forking\nAfter=network.target mysql.service\nRequires=mysql.service\n")
	m.setResponse(`systemctl show 'mysql.service'`, "Description=MySQL Community Server\nLoadState=loaded\nActiveState=active\nSubState=running\nType=forking\nAfter=network.target\n")
	m.setResponse(`systemctl show 'ssh.service'`, "Description=OpenSSH Daemon\nLoadState=loaded\nActiveState=active\nSubState=running\nType=notify\nAfter=network.target\n")
	m.setResponse(`systemctl show 'docker.service'`, "Description=Docker Application Container Engine\nLoadState=loaded\nActiveState=active\nSubState=running\nType=notify\nAfter=network.target docker.socket\n")

	// Database collector
	m.setResponse("pgrep -x 'mysqld'", "yes\n")
	m.setResponse("mysql --version", "mysql  Ver 8.0.35 for Linux on x86_64 (MySQL Community Server)\n")
	m.setResponse(`ss -tlnp 2>/dev/null | grep 'mysqld'`, "127.0.0.1:3306\n")
	m.setResponse(`mysql -e "SELECT @@datadir"`, "/var/lib/mysql/\n")
	m.setResponse(`mysql -e`, "/var/lib/mysql/\n")

	m.setResponse("pgrep -x 'postgres'", "no\n")
	m.setResponse("pgrep -x 'mongod'", "no\n")
	m.setResponse("pgrep -x 'redis-server'", "no\n")

	// Nginx collector
	m.setResponse("which nginx", "/usr/sbin/nginx\n")
	m.setResponse("nginx -v 2>&1", "nginx version: nginx/1.18.0\n")
	m.setResponse("nginx -T", "# configuration file /etc/nginx/nginx.conf:\nserver {\n    listen 80;\n    server_name example.com;\n    proxy_pass http://localhost:3000;\n}\nupstream app_backend {\n    server 127.0.0.1:3000 weight=3;\n}\n")
	m.setResponse(`find /etc/nginx -name "*.pem"`, "/etc/nginx/ssl/example.com.pem\n")
	m.setResponse(`openssl x509 -in '/etc/nginx/ssl/example.com.pem'`, "notAfter=Dec 31 23:59:59 2025 GMT\nissuer=C=US, O=Let's Encrypt\n")

	return m
}

// --- Tests ---

func TestOSCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &OSCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("OSCollector failed: %v", err)
	}

	info, ok := result.(*OSInfo)
	if !ok {
		t.Fatalf("expected *OSInfo, got %T", result)
	}

	if info.Hostname != "web-server-01" {
		t.Errorf("expected hostname 'web-server-01', got %q", info.Hostname)
	}
	if info.Distro != "Ubuntu 22.04.3 LTS" {
		t.Errorf("expected distro 'Ubuntu 22.04.3 LTS', got %q", info.Distro)
	}
	if info.Kernel != "5.15.0-91-generic" {
		t.Errorf("expected kernel '5.15.0-91-generic', got %q", info.Kernel)
	}
	if info.Architecture != "x86_64" {
		t.Errorf("expected architecture 'x86_64', got %q", info.Architecture)
	}
	if info.Uptime != 86400 {
		t.Errorf("expected uptime 86400, got %d", info.Uptime)
	}
	if info.Virtualization != "kvm" {
		t.Errorf("expected virtualization 'kvm', got %q", info.Virtualization)
	}
}

func TestHardwareCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &HardwareCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("HardwareCollector failed: %v", err)
	}

	info, ok := result.(*HardwareInfo)
	if !ok {
		t.Fatalf("expected *HardwareInfo, got %T", result)
	}

	if info.CPUCores != 4 {
		t.Errorf("expected 4 CPU cores, got %d", info.CPUCores)
	}
	if info.RAMTotalMB != 8192 {
		t.Errorf("expected 8192MB RAM total, got %d", info.RAMTotalMB)
	}
	if info.RAMUsedMB != 4096 {
		t.Errorf("expected 4096MB RAM used, got %d", info.RAMUsedMB)
	}
	if info.DiskTotalGB != 100 {
		t.Errorf("expected 100GB disk total, got %f", info.DiskTotalGB)
	}
	if info.DiskUsedGB != 50 {
		t.Errorf("expected 50GB disk used, got %f", info.DiskUsedGB)
	}
}

func TestDiskCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &DiskCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DiskCollector failed: %v", err)
	}

	partitions, ok := result.([]DiskPartition)
	if !ok {
		t.Fatalf("expected []DiskPartition, got %T", result)
	}

	if len(partitions) != 2 {
		t.Fatalf("expected 2 partitions, got %d", len(partitions))
	}

	if partitions[0].MountPoint != "/" {
		t.Errorf("expected mount point '/', got %q", partitions[0].MountPoint)
	}
	if partitions[0].SizeGB != 100 {
		t.Errorf("expected size 100GB, got %f", partitions[0].SizeGB)
	}
}

func TestPortCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &PortCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("PortCollector failed: %v", err)
	}

	ports, ok := result.([]OpenPort)
	if !ok {
		t.Fatalf("expected []OpenPort, got %T", result)
	}

	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(ports))
	}

	// Check first port (nginx on 80)
	if ports[0].Port != 80 {
		t.Errorf("expected port 80, got %d", ports[0].Port)
	}
	if ports[0].Process != "nginx" {
		t.Errorf("expected process 'nginx', got %q", ports[0].Process)
	}
	if ports[0].PID != 1234 {
		t.Errorf("expected PID 1234, got %d", ports[0].PID)
	}
}

func TestDockerCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &DockerCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DockerCollector failed: %v", err)
	}

	info, ok := result.(*DockerInfo)
	if !ok {
		t.Fatalf("expected *DockerInfo, got %T", result)
	}

	if info.Version != "24.0.7" {
		t.Errorf("expected version '24.0.7', got %q", info.Version)
	}
	if len(info.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(info.Containers))
	}
	if info.Containers[0].Name != "webapp" {
		t.Errorf("expected container name 'webapp', got %q", info.Containers[0].Name)
	}
	if info.Containers[0].State != "running" {
		t.Errorf("expected state 'running', got %q", info.Containers[0].State)
	}
	if len(info.Containers[0].Ports) != 1 {
		t.Fatalf("expected 1 port mapping, got %d", len(info.Containers[0].Ports))
	}
	if info.Containers[0].Ports[0].HostPort != 3000 {
		t.Errorf("expected host port 3000, got %d", info.Containers[0].Ports[0].HostPort)
	}
	if len(info.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(info.Images))
	}
	if len(info.ComposeProjects) != 1 {
		t.Fatalf("expected 1 compose project, got %d", len(info.ComposeProjects))
	}
	if info.ComposeProjects[0].Name != "myapp" {
		t.Errorf("expected project name 'myapp', got %q", info.ComposeProjects[0].Name)
	}
}

func TestDockerCollectorNotInstalled(t *testing.T) {
	ssh := newMockSSH()
	// Don't set "which docker" response — returns empty
	collector := &DockerCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DockerCollector should not error when Docker is not installed: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when Docker is not installed, got %T", result)
	}
}

func TestServiceCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &ServiceCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("ServiceCollector failed: %v", err)
	}

	services, ok := result.([]SystemService)
	if !ok {
		t.Fatalf("expected []SystemService, got %T", result)
	}

	if len(services) != 4 {
		t.Fatalf("expected 4 services, got %d", len(services))
	}

	// Check nginx service
	var nginxSvc *SystemService
	for i := range services {
		if services[i].Name == "nginx.service" {
			nginxSvc = &services[i]
			break
		}
	}
	if nginxSvc == nil {
		t.Fatal("nginx.service not found")
	}
	if nginxSvc.ActiveState != "active" {
		t.Errorf("expected active state 'active', got %q", nginxSvc.ActiveState)
	}
	if nginxSvc.SubState != "running" {
		t.Errorf("expected sub state 'running', got %q", nginxSvc.SubState)
	}
	// Should depend on mysql.service
	foundMysqlDep := false
	for _, dep := range nginxSvc.DependsOn {
		if dep == "mysql.service" {
			foundMysqlDep = true
			break
		}
	}
	if !foundMysqlDep {
		t.Error("expected nginx.service to depend on mysql.service")
	}
}

func TestDatabaseCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &DatabaseCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DatabaseCollector failed: %v", err)
	}

	databases, ok := result.([]DatabaseInfo)
	if !ok {
		t.Fatalf("expected []DatabaseInfo, got %T", result)
	}

	if len(databases) != 1 {
		t.Fatalf("expected 1 database (MySQL), got %d", len(databases))
	}

	if databases[0].Type != "mysql" {
		t.Errorf("expected type 'mysql', got %q", databases[0].Type)
	}
	if databases[0].Port != 3306 {
		t.Errorf("expected port 3306, got %d", databases[0].Port)
	}
	if !databases[0].Running {
		t.Error("expected database to be running")
	}
	if databases[0].DataDir != "/var/lib/mysql/" {
		t.Errorf("expected data dir '/var/lib/mysql/', got %q", databases[0].DataDir)
	}
}

func TestNginxCollector(t *testing.T) {
	ssh := setupFullMockSSH()
	collector := &NginxCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("NginxCollector failed: %v", err)
	}

	info, ok := result.(*NginxInfo)
	if !ok {
		t.Fatalf("expected *NginxInfo, got %T", result)
	}

	if info.Version != "1.18.0" {
		t.Errorf("expected version '1.18.0', got %q", info.Version)
	}
	if len(info.VHosts) != 1 {
		t.Fatalf("expected 1 vhost, got %d", len(info.VHosts))
	}
	if info.VHosts[0].ServerName != "example.com" {
		t.Errorf("expected server_name 'example.com', got %q", info.VHosts[0].ServerName)
	}
	if info.VHosts[0].ProxyPass != "http://localhost:3000" {
		t.Errorf("expected proxy_pass 'http://localhost:3000', got %q", info.VHosts[0].ProxyPass)
	}
	if len(info.Upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(info.Upstreams))
	}
	if info.Upstreams[0].Name != "app_backend" {
		t.Errorf("expected upstream name 'app_backend', got %q", info.Upstreams[0].Name)
	}
	if len(info.Upstreams[0].Servers) != 1 {
		t.Fatalf("expected 1 upstream server, got %d", len(info.Upstreams[0].Servers))
	}
	if info.Upstreams[0].Servers[0].Address != "127.0.0.1:3000" {
		t.Errorf("expected upstream server address '127.0.0.1:3000', got %q", info.Upstreams[0].Servers[0].Address)
	}
	if len(info.SSLCerts) != 1 {
		t.Fatalf("expected 1 SSL cert, got %d", len(info.SSLCerts))
	}
}

func TestNginxCollectorNotInstalled(t *testing.T) {
	ssh := newMockSSH()
	collector := &NginxCollector{}

	result, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("NginxCollector should not error when Nginx is not installed: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when Nginx is not installed, got %T", result)
	}
}

// --- CollectorRunner Tests ---

func TestCollectorRunnerAllSuccess(t *testing.T) {
	ssh := setupFullMockSSH()
	runner := NewCollectorRunner(DefaultCollectors()...)

	snapshot, err := runner.Run(context.Background(), ssh)
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}

	if snapshot.OS.Hostname != "web-server-01" {
		t.Errorf("expected hostname 'web-server-01', got %q", snapshot.OS.Hostname)
	}
	if snapshot.Hardware.CPUCores != 4 {
		t.Errorf("expected 4 CPU cores, got %d", snapshot.Hardware.CPUCores)
	}
	if snapshot.Docker == nil {
		t.Error("expected Docker info, got nil")
	}
	if len(snapshot.Services) != 4 {
		t.Errorf("expected 4 services, got %d", len(snapshot.Services))
	}
	if len(snapshot.Databases) != 1 {
		t.Errorf("expected 1 database, got %d", len(snapshot.Databases))
	}
	if snapshot.Nginx == nil {
		t.Error("expected Nginx info, got nil")
	}
	if len(snapshot.DiskUsage) != 2 {
		t.Errorf("expected 2 disk partitions, got %d", len(snapshot.DiskUsage))
	}
	if len(snapshot.NetworkPorts) != 3 {
		t.Errorf("expected 3 ports, got %d", len(snapshot.NetworkPorts))
	}
	if len(snapshot.CollectionErrors) != 0 {
		t.Errorf("expected 0 collection errors, got %d: %v", len(snapshot.CollectionErrors), snapshot.CollectionErrors)
	}
}

func TestCollectorRunnerPartialFailure(t *testing.T) {
	ssh := setupFullMockSSH()
	// Make the Disk collector fail
	ssh.setError(`df -BG | awk`, fmt.Errorf("df command failed"))

	runner := NewCollectorRunner(DefaultCollectors()...)

	snapshot, err := runner.Run(context.Background(), ssh)
	if err != nil {
		t.Fatalf("runner should not fail on partial collector failure: %v", err)
	}

	// Disk collector should have an error
	if len(snapshot.CollectionErrors) == 0 {
		t.Error("expected at least one collection error")
	}

	// Other collectors should still succeed
	if snapshot.OS.Hostname != "web-server-01" {
		t.Errorf("expected OS collector to succeed, got hostname %q", snapshot.OS.Hostname)
	}
	if snapshot.Hardware.CPUCores != 4 {
		t.Errorf("expected hardware collector to succeed, got %d cores", snapshot.Hardware.CPUCores)
	}
	if snapshot.Docker == nil {
		t.Error("expected Docker collector to succeed")
	}
}

func TestCollectorRunnerContextCancellation(t *testing.T) {
	ssh := setupFullMockSSH()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	runner := NewCollectorRunner(DefaultCollectors()...)

	snapshot, _ := runner.Run(ctx, ssh)

	// With cancelled context, collectors should fail or return empty
	// The snapshot should still be created (partial)
	if snapshot == nil {
		t.Fatal("expected non-nil snapshot even with cancelled context")
	}
}

// --- Dependency Graph Tests ---

func TestBuildDependencyGraph(t *testing.T) {
	ssh := setupFullMockSSH()
	runner := NewCollectorRunner(DefaultCollectors()...)

	snapshot, err := runner.Run(context.Background(), ssh)
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}

	graph := BuildDependencyGraph(snapshot)

	if graph == nil {
		t.Fatal("expected non-nil graph")
	}

	// Should have nodes for containers, databases, services, nginx vhosts
	if len(graph.Nodes) == 0 {
		t.Error("expected non-empty nodes")
	}

	// Check for container nodes
	hasContainerNode := false
	for _, n := range graph.Nodes {
		if n.Type == "container" {
			hasContainerNode = true
			break
		}
	}
	if !hasContainerNode {
		t.Error("expected at least one container node")
	}

	// Check for database nodes
	hasDatabaseNode := false
	for _, n := range graph.Nodes {
		if n.Type == "database" {
			hasDatabaseNode = true
			break
		}
	}
	if !hasDatabaseNode {
		t.Error("expected at least one database node")
	}

	// Should have edges
	if len(graph.Edges) == 0 {
		t.Error("expected non-empty edges")
	}

	// Check for container → database edge (container "db" exposes port 3306 → MySQL)
	hasContainerDBEdge := false
	for _, e := range graph.Edges {
		if e.From == "container:db" && e.To == "database:mysql" {
			hasContainerDBEdge = true
			break
		}
	}
	if !hasContainerDBEdge {
		t.Error("expected container:db → database:mysql edge")
	}

	// Check for nginx → container edge (nginx proxy_pass to port 3000 → container "webapp")
	hasNginxContainerEdge := false
	for _, e := range graph.Edges {
		if strings.HasPrefix(e.From, "nginx:") && e.To == "container:webapp" {
			hasNginxContainerEdge = true
			break
		}
	}
	if !hasNginxContainerEdge {
		t.Error("expected nginx → container:webapp edge")
	}

	// Check for service → service edge (nginx.service → mysql.service)
	hasServiceEdge := false
	for _, e := range graph.Edges {
		if e.From == "service:nginx.service" && e.To == "service:mysql.service" {
			hasServiceEdge = true
			break
		}
	}
	if !hasServiceEdge {
		t.Error("expected service:nginx.service → service:mysql.service edge")
	}
}

func TestDependencyGraphTopologicalSort(t *testing.T) {
	graph := &DependencyGraph{
		Nodes: []DependencyNode{
			{ID: "a", Name: "A", Type: "service"},
			{ID: "b", Name: "B", Type: "service"},
			{ID: "c", Name: "C", Type: "service"},
		},
		Edges: []DependencyEdge{
			{From: "a", To: "b", Reason: "a depends on b"},
			{From: "b", To: "c", Reason: "b depends on c"},
		},
	}

	sorted, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("topological sort failed: %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(sorted))
	}

	// C should come first (no dependencies), then B, then A
	if sorted[0].ID != "c" {
		t.Errorf("expected first node to be 'c', got %q", sorted[0].ID)
	}
	if sorted[1].ID != "b" {
		t.Errorf("expected second node to be 'b', got %q", sorted[1].ID)
	}
	if sorted[2].ID != "a" {
		t.Errorf("expected third node to be 'a', got %q", sorted[2].ID)
	}
}

func TestDependencyGraphCycleDetection(t *testing.T) {
	graph := &DependencyGraph{
		Nodes: []DependencyNode{
			{ID: "a", Name: "A", Type: "service"},
			{ID: "b", Name: "B", Type: "service"},
		},
		Edges: []DependencyEdge{
			{From: "a", To: "b", Reason: "a depends on b"},
			{From: "b", To: "a", Reason: "b depends on a"},
		},
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Error("expected cycle detection error")
	}
}

// --- Compatibility Checker Tests ---

func TestCheckCompatibilitySuccess(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 4096, DiskTotalGB: 100, DiskUsedGB: 50},
		OS:       OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
		OS:       OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
	}

	report := CheckCompatibility(source, target)

	if !report.Compatible {
		t.Errorf("expected compatible, got incompatible: %d blockers", len(report.Blockers))
	}
	if len(report.Blockers) != 0 {
		t.Errorf("expected 0 blockers, got %d: %v", len(report.Blockers), report.Blockers)
	}
}

func TestCheckCompatibilityRAMBlocker(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 8192, DiskTotalGB: 100, DiskUsedGB: 50},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 4096, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
	}

	report := CheckCompatibility(source, target)

	if report.Compatible {
		t.Error("expected incompatible due to RAM")
	}
	if len(report.Blockers) == 0 {
		t.Fatal("expected at least one blocker")
	}
	// Check that the RAM blocker exists
	foundRAMBlocker := false
	for _, b := range report.Blockers {
		if b.Category == "ram" {
			foundRAMBlocker = true
			break
		}
	}
	if !foundRAMBlocker {
		t.Error("expected RAM blocker")
	}
}

func TestCheckCompatibilityDiskBlocker(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 4096, DiskTotalGB: 100, DiskUsedGB: 100},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 50, DiskUsedGB: 25},
	}

	report := CheckCompatibility(source, target)

	if report.Compatible {
		t.Error("expected incompatible due to disk")
	}
	foundDiskBlocker := false
	for _, b := range report.Blockers {
		if b.Category == "disk" {
			foundDiskBlocker = true
			break
		}
	}
	if !foundDiskBlocker {
		t.Error("expected disk blocker")
	}
}

func TestCheckCompatibilityDockerBlocker(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 4096, DiskTotalGB: 100, DiskUsedGB: 50},
		Docker:   &DockerInfo{Version: "24.0.7", Containers: []ContainerInfo{{Name: "webapp"}}},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
		// No Docker on target
	}

	report := CheckCompatibility(source, target)

	if report.Compatible {
		t.Error("expected incompatible due to Docker")
	}
	foundDockerBlocker := false
	for _, b := range report.Blockers {
		if b.Category == "docker" {
			foundDockerBlocker = true
			break
		}
	}
	if !foundDockerBlocker {
		t.Error("expected Docker blocker")
	}
}

func TestCheckCompatibilityPortConflict(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 4096, DiskTotalGB: 100, DiskUsedGB: 50},
		NetworkPorts: []OpenPort{
			{Port: 80, Process: "nginx"},
			{Port: 443, Process: "nginx"},
		},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
		NetworkPorts: []OpenPort{
			{Port: 80, Process: "apache2"}, // Different process on same port
			{Port: 8080, Process: "tomcat"},
		},
	}

	report := CheckCompatibility(source, target)

	// Port 80 is used by different processes → should be a blocker
	foundPortBlocker := false
	for _, b := range report.Blockers {
		if b.Category == "port" {
			foundPortBlocker = true
			break
		}
	}
	if !foundPortBlocker {
		t.Error("expected port conflict blocker")
	}
}

func TestCheckCompatibilityOSWarning(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 8192, RAMUsedMB: 4096, DiskTotalGB: 100, DiskUsedGB: 50},
		OS:       OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
		OS:       OSInfo{Distro: "CentOS Stream 9", Architecture: "x86_64"},
	}

	report := CheckCompatibility(source, target)

	// Different OS families → should be a warning (not blocker)
	if !report.Compatible {
		t.Error("expected compatible despite OS difference")
	}
	foundOSWarning := false
	for _, w := range report.Warnings {
		if w.Category == "os" {
			foundOSWarning = true
			break
		}
	}
	if !foundOSWarning {
		t.Error("expected OS compatibility warning")
	}
}

func TestCheckCompatibilityNilSnapshots(t *testing.T) {
	report := CheckCompatibility(nil, nil)

	if report.Compatible {
		t.Error("expected incompatible for nil snapshots")
	}
	// CheckCompatibility returns early after source nil check, so only 1 blocker
	if len(report.Blockers) != 1 {
		t.Errorf("expected 1 blocker (source nil), got %d", len(report.Blockers))
	}
}

// --- Snapshot Store Tests ---

func TestNoopSnapshotStore(t *testing.T) {
	store := NewNoopSnapshotStore()

	err := store.SaveSnapshot(1, &ServerSnapshot{OS: OSInfo{Hostname: "test"}})
	if err != nil {
		t.Fatalf("SaveSnapshot should not error: %v", err)
	}

	_, err = store.LoadSnapshot(1)
	if err == nil {
		t.Error("LoadSnapshot should error for noop store")
	}

	err = store.DeleteSnapshot(1)
	if err != nil {
		t.Fatalf("DeleteSnapshot should not error: %v", err)
	}
}

// --- Integration Test ---

func TestFullDiscoveryPipeline(t *testing.T) {
	ssh := setupFullMockSSH()
	runner := NewCollectorRunner(DefaultCollectors()...)

	// Step 1: Collect snapshot
	snapshot, err := runner.Run(context.Background(), ssh)
	if err != nil {
		t.Fatalf("snapshot collection failed: %v", err)
	}

	// Step 2: Build dependency graph
	graph := BuildDependencyGraph(snapshot)
	if len(graph.Nodes) == 0 {
		t.Fatal("expected non-empty graph")
	}

	// Step 3: Check compatibility (source vs a target with more resources)
	target := &ServerSnapshot{
		Hardware: HardwareInfo{RAMTotalMB: 16384, RAMUsedMB: 2048, DiskTotalGB: 200, DiskUsedGB: 100},
		OS:       OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
		Docker:   &DockerInfo{Version: "24.0.7"},
	}
	report := CheckCompatibility(snapshot, target)

	if !report.Compatible {
		t.Errorf("expected compatible, got %d blockers: %v", len(report.Blockers), report.Blockers)
	}

	// Step 4: Topological sort
	sorted, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("topological sort failed: %v", err)
	}
	if len(sorted) != len(graph.Nodes) {
		t.Errorf("expected %d sorted nodes, got %d", len(graph.Nodes), len(sorted))
	}
}
