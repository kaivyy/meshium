package discovery

import "testing"

func TestCompatibilityOSCompatibility(t *testing.T) {
	source := &ServerSnapshot{
		OS: OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
	}
	sameTarget := &ServerSnapshot{
		OS: OSInfo{Distro: "Ubuntu 22.04.3 LTS", Architecture: "x86_64"},
	}
	differentTarget := &ServerSnapshot{
		OS: OSInfo{Distro: "CentOS Stream 9", Architecture: "x86_64"},
	}

	if issues := CheckOSCompatibility(source, sameTarget); len(issues) != 0 {
		t.Fatalf("expected no OS issues for identical OS values, got %d: %#v", len(issues), issues)
	}

	issues := CheckOSCompatibility(source, differentTarget)
	if len(issues) == 0 {
		t.Fatal("expected OS compatibility issues for different operating systems")
	}
	if !hasIssueCategory(issues, "os") {
		t.Fatalf("expected an OS issue, got %#v", issues)
	}
	if !hasSeverity(issues, "warning") {
		t.Fatalf("expected a warning for different OS families, got %#v", issues)
	}
}

func TestCompatibilityKernelCompatibility(t *testing.T) {
	source := &ServerSnapshot{OS: OSInfo{Kernel: "3.10.0-1160-generic"}}
	target := &ServerSnapshot{OS: OSInfo{Kernel: "6.5.0-1018-generic"}}

	issues := CheckKernelCompatibility(source, target)
	if !hasSeverity(issues, "warning") {
		t.Fatalf("expected kernel major version gap to produce a warning, got %#v", issues)
	}
}

func TestCompatibilityPortConflicts(t *testing.T) {
	source := &ServerSnapshot{
		NetworkPorts: []OpenPort{{Port: 8080, Process: "source-app"}},
	}
	target := &ServerSnapshot{
		NetworkPorts: []OpenPort{{Port: 8080, Process: "target-app"}},
	}

	issues := CheckPortConflicts(source, target)
	if !hasIssueCategory(issues, "port") {
		t.Fatalf("expected port conflict issue, got %#v", issues)
	}
	if !hasBlockingIssue(issues) {
		t.Fatalf("expected port conflict to be blocking, got %#v", issues)
	}
}

func TestCompatibilityDatabaseDowngrade(t *testing.T) {
	source := &ServerSnapshot{
		Databases: []DatabaseInfo{{Type: "mysql", Version: "8.0.36"}},
	}
	target := &ServerSnapshot{
		Databases: []DatabaseInfo{{Type: "mysql", Version: "5.7.42"}},
	}

	issues := CheckDatabaseCompatibility(source, target)
	if !hasIssueCategory(issues, "database") {
		t.Fatalf("expected database compatibility issue, got %#v", issues)
	}
	if !hasBlockingIssue(issues) {
		t.Fatalf("expected database downgrade to be blocking, got %#v", issues)
	}
}

func TestCompatibilityStorageInsufficientDisk(t *testing.T) {
	source := &ServerSnapshot{
		Hardware: HardwareInfo{DiskUsedGB: 120},
	}
	target := &ServerSnapshot{
		Hardware: HardwareInfo{DiskTotalGB: 100, DiskUsedGB: 30},
	}

	issues := CheckStorageCompatibility(source, target)
	if !hasIssueCategory(issues, "storage") {
		t.Fatalf("expected storage issue, got %#v", issues)
	}
	if !hasBlockingIssue(issues) {
		t.Fatalf("expected insufficient disk space to be blocking, got %#v", issues)
	}
}

func TestCompatibilityRuntimeMissing(t *testing.T) {
	source := &ServerSnapshot{
		Runtimes: []RuntimeInfo{{Name: "node", Version: "v18.19.0"}},
	}
	target := &ServerSnapshot{
		Runtimes: []RuntimeInfo{{Name: "python", Version: "3.12.2"}},
	}

	issues := CheckRuntimeCompatibility(source, target)
	if !hasIssueCategory(issues, "runtime") {
		t.Fatalf("expected runtime issue, got %#v", issues)
	}
	if !hasBlockingIssue(issues) {
		t.Fatalf("expected missing runtime to be blocking, got %#v", issues)
	}
}

func TestCompatibilityNilSnapshotsNoPanic(t *testing.T) {
	checks := []struct {
		name string
		fn   func(*ServerSnapshot, *ServerSnapshot) []CompatibilityIssue
	}{
		{name: "os", fn: CheckOSCompatibility},
		{name: "kernel", fn: CheckKernelCompatibility},
		{name: "filesystem", fn: CheckFilesystemCompatibility},
		{name: "docker", fn: CheckDockerCompatibility},
		{name: "runtime", fn: CheckRuntimeCompatibility},
		{name: "port", fn: CheckPortConflicts},
		{name: "firewall", fn: CheckFirewallCompatibility},
		{name: "security", fn: CheckSecurityCompatibility},
		{name: "database", fn: CheckDatabaseCompatibility},
		{name: "storage", fn: CheckStorageCompatibility},
		{name: "network", fn: CheckNetworkCompatibility},
		{name: "ssl", fn: CheckSSLCompatibility},
		{name: "cloud", fn: CheckCloudCompatibility},
		{name: "all", fn: RunAllCompatibilityChecks},
	}

	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			issues := check.fn(nil, nil)
			if len(issues) != 0 {
				t.Fatalf("expected no issues for nil snapshots, got %#v", issues)
			}
		})
	}
}

func TestCompatibilityBlockingFlags(t *testing.T) {
	source := &ServerSnapshot{
		Runtimes:     []RuntimeInfo{{Name: "node", Version: "v18.19.0"}},
		NetworkPorts: []OpenPort{{Port: 8080, Process: "source-app"}},
		Databases:    []DatabaseInfo{{Type: "postgresql", Version: "16.2"}},
	}
	target := &ServerSnapshot{
		Runtimes:     []RuntimeInfo{{Name: "python", Version: "3.12.2"}},
		NetworkPorts: []OpenPort{{Port: 8080, Process: "target-app"}},
		Databases:    []DatabaseInfo{{Type: "postgresql", Version: "14.9"}},
	}

	issues := RunAllCompatibilityChecks(source, target)
	if len(issues) == 0 {
		t.Fatal("expected at least one issue from the combined compatibility run")
	}

	for _, issue := range issues {
		if issue.RiskScore < 0 || issue.RiskScore > 100 {
			t.Fatalf("risk score out of range for issue %#v", issue)
		}
		if issue.Blocking && issue.Severity != "error" && issue.Severity != "critical" {
			t.Fatalf("blocking issue has non-blocking severity: %#v", issue)
		}
	}
	if !hasBlockingIssue(issues) {
		t.Fatalf("expected blocking issues in combined result, got %#v", issues)
	}
}

func TestCompatibilityRiskScoresRange(t *testing.T) {
	source := &ServerSnapshot{
		OS:           OSInfo{Distro: "Ubuntu 18.04.6 LTS", Architecture: "x86_64", Kernel: "3.10.0-1160-generic"},
		Hardware:     HardwareInfo{DiskUsedGB: 200},
		Runtimes:     []RuntimeInfo{{Name: "node", Version: "v18.19.0"}},
		NetworkPorts: []OpenPort{{Port: 8080, Process: "source-app"}},
		Databases:    []DatabaseInfo{{Type: "mysql", Version: "8.0.36"}},
	}
	target := &ServerSnapshot{
		OS:           OSInfo{Distro: "CentOS Stream 9", Architecture: "aarch64", Kernel: "6.5.0-1018-aws"},
		Hardware:     HardwareInfo{DiskTotalGB: 50, DiskUsedGB: 25},
		Runtimes:     []RuntimeInfo{{Name: "python", Version: "3.12.2"}},
		NetworkPorts: []OpenPort{{Port: 8080, Process: "target-app"}},
		Databases:    []DatabaseInfo{{Type: "mysql", Version: "5.7.42"}},
		Firewall:     &FirewallInfo{Type: "ufw", Active: true, Rules: []string{"allow 22/tcp"}},
		SELinux:      &SELinuxInfo{Enabled: true, Mode: "enforcing"},
		AppArmor:     &AppArmorInfo{Enabled: true},
		Packages:     []string{"nginx", "certbot"},
	}

	issues := RunAllCompatibilityChecks(source, target)
	if len(issues) == 0 {
		t.Fatal("expected combined compatibility issues for risk score validation")
	}
	for _, issue := range issues {
		if issue.RiskScore < 0 || issue.RiskScore > 100 {
			t.Fatalf("risk score out of range: %#v", issue)
		}
	}
}

func hasIssueCategory(issues []CompatibilityIssue, category string) bool {
	for _, issue := range issues {
		if issue.Category == category {
			return true
		}
	}
	return false
}

func hasSeverity(issues []CompatibilityIssue, severity string) bool {
	for _, issue := range issues {
		if issue.Severity == severity {
			return true
		}
	}
	return false
}

func hasBlockingIssue(issues []CompatibilityIssue) bool {
	for _, issue := range issues {
		if issue.Blocking {
			return true
		}
	}
	return false
}
