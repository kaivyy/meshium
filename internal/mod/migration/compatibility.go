package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// CompatibilityEngine checks compatibility between source and target servers.
// It evaluates CPU architecture, RAM, disk, OS, Docker version, and more.
type CompatibilityEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
}

// NewCompatibilityEngine creates a new compatibility engine.
func NewCompatibilityEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *CompatibilityEngine {
	return &CompatibilityEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
	}
}

// Severity represents the severity of a compatibility issue.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// CompatibilityCheckResult represents the result of a single compatibility check.
type CompatibilityCheckResult struct {
	CheckName string   `json:"checkName"`
	Severity  Severity `json:"severity"`
	Passed    bool     `json:"passed"`
	Message   string   `json:"message"`
}

// CheckCompatibility runs all compatibility checks between source and target.
func (e *CompatibilityEngine) CheckCompatibility(ctx context.Context, migrationID int) ([]CompatibilityCheckResult, error) {
	var results []CompatibilityCheckResult

	// Collect source info
	sourceInfo, err := e.collectServerInfo(ctx, e.sourceSSH)
	if err != nil {
		return nil, fmt.Errorf("collect source info: %w", err)
	}

	// Collect target info
	targetInfo, err := e.collectServerInfo(ctx, e.targetSSH)
	if err != nil {
		return nil, fmt.Errorf("collect target info: %w", err)
	}

	// Run checks
	results = append(results, e.checkArchitecture(sourceInfo, targetInfo))
	results = append(results, e.checkRAM(sourceInfo, targetInfo))
	results = append(results, e.checkDisk(sourceInfo, targetInfo))
	results = append(results, e.checkDockerVersion(sourceInfo, targetInfo))
	results = append(results, e.checkKernel(sourceInfo, targetInfo))
	results = append(results, e.checkPackageManager(sourceInfo, targetInfo))
	results = append(results, e.checkTimezone(sourceInfo, targetInfo))
	results = append(results, e.checkOpenSSL(sourceInfo, targetInfo))
	results = append(results, e.checkDockerStorageDriver(sourceInfo, targetInfo))
	results = append(results, e.checkNetworkPorts(ctx, sourceInfo))
	results = append(results, e.checkSELinux(sourceInfo, targetInfo))

	// Record verification results
	for _, r := range results {
		e.repo.CreateVerificationResult(ctx, VerificationResult{
			MigrationID:      migrationID,
			VerificationType: "compatibility",
			Target:           r.CheckName,
			Expected:         "compatible",
			Actual:           r.Message,
			Passed:           r.Passed,
			ErrorMessage:     func() string { if r.Passed { return "" }; return r.Message }(),
		})
	}

	return results, nil
}

// HasBlockers returns true if any check has Critical severity.
func HasBlockers(results []CompatibilityCheckResult) bool {
	for _, r := range results {
		if r.Severity == SeverityCritical && !r.Passed {
			return true
		}
	}
	return false
}

// serverInfo holds collected server information.
type serverInfo struct {
	Arch             string
	CPUCores         int
	RAMMB            int
	DiskGB           float64
	Kernel           string
	OS               string
	DockerVersion    string
	ComposeVersion   string
	PackageManager   string
	Timezone         string
	OpenSSLVersion   string
	StorageDriver    string
	SELinux          string
	Distro           string
	OpenPorts        []string
}

// collectServerInfo gathers system information from a server via SSH.
func (e *CompatibilityEngine) collectServerInfo(ctx context.Context, ssh SSHExecuter) (*serverInfo, error) {
	info := &serverInfo{}

	// Architecture
	output, _, _, _ := ssh.ExecContext(ctx, "uname -m 2>&1")
	info.Arch = strings.TrimSpace(output)

	// CPU cores
	output, _, _, _ = ssh.ExecContext(ctx, "nproc 2>&1")
	info.CPUCores, _ = strconv.Atoi(strings.TrimSpace(output))

	// RAM
	output, _, _, _ = ssh.ExecContext(ctx, "free -m 2>/dev/null | awk '/Mem:/ {print $2}'")
	info.RAMMB, _ = strconv.Atoi(strings.TrimSpace(output))

	// Disk
	output, _, _, _ = ssh.ExecContext(ctx, "df -BG / 2>/dev/null | awk 'NR==2 {print $2}'")
	info.DiskGB, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(output), "G"), 64)

	// Kernel
	output, _, _, _ = ssh.ExecContext(ctx, "uname -r 2>&1")
	info.Kernel = strings.TrimSpace(output)

	// OS
	output, _, _, _ = ssh.ExecContext(ctx, "cat /etc/os-release 2>/dev/null | grep '^PRETTY_NAME=' | cut -d'=' -f2 | tr -d '\"'")
	info.OS = strings.TrimSpace(output)

	// Docker version
	output, _, _, _ = ssh.ExecContext(ctx, "docker --version 2>&1")
	info.DockerVersion = strings.TrimSpace(output)

	// Compose version
	output, _, _, _ = ssh.ExecContext(ctx, "docker compose version 2>&1")
	info.ComposeVersion = strings.TrimSpace(output)

	// Package manager
	output, _, _, _ = ssh.ExecContext(ctx, "which apt dnf yum apk pacman 2>/dev/null | head -1")
	switch {
	case strings.Contains(output, "apt"):
		info.PackageManager = "apt"
	case strings.Contains(output, "dnf"):
		info.PackageManager = "dnf"
	case strings.Contains(output, "yum"):
		info.PackageManager = "yum"
	case strings.Contains(output, "apk"):
		info.PackageManager = "apk"
	case strings.Contains(output, "pacman"):
		info.PackageManager = "pacman"
	default:
		info.PackageManager = "unknown"
	}

	// Timezone
	output, _, _, _ = ssh.ExecContext(ctx, "timedatectl show -p Timezone 2>/dev/null | cut -d= -f2 || cat /etc/timezone 2>/dev/null || echo UTC")
	info.Timezone = strings.TrimSpace(output)

	// OpenSSL
	output, _, _, _ = ssh.ExecContext(ctx, "openssl version 2>&1")
	info.OpenSSLVersion = strings.TrimSpace(output)

	// Docker storage driver
	output, _, _, _ = ssh.ExecContext(ctx, "docker info --format '{{.Driver}}' 2>&1")
	info.StorageDriver = strings.TrimSpace(output)

	// SELinux
	output, _, _, _ = ssh.ExecContext(ctx, "getenforce 2>/dev/null || echo Disabled")
	info.SELinux = strings.TrimSpace(output)

	// Open ports
	output, _, _, _ = ssh.ExecContext(ctx, "ss -tlnp 2>/dev/null | awk 'NR>1 {print $4}' | rev | cut -d: -f1 | rev | sort -u")
	for _, port := range strings.Split(output, "\n") {
		port = strings.TrimSpace(port)
		if port != "" {
			info.OpenPorts = append(info.OpenPorts, port)
		}
	}

	return info, nil
}

// --- Compatibility checks ---

func (e *CompatibilityEngine) checkArchitecture(source, target *serverInfo) CompatibilityCheckResult {
	if source.Arch == target.Arch {
		return CompatibilityCheckResult{
			CheckName: "cpu_architecture",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Both servers use %s architecture", source.Arch),
		}
	}
	// Cross-arch is possible with Docker but risky for native code
	return CompatibilityCheckResult{
		CheckName: "cpu_architecture",
		Severity:  SeverityHigh,
		Passed:    false,
		Message:   fmt.Sprintf("Architecture mismatch: source=%s target=%s (Docker may work, native code will not)", source.Arch, target.Arch),
	}
}

func (e *CompatibilityEngine) checkRAM(source, target *serverInfo) CompatibilityCheckResult {
	if target.RAMMB >= source.RAMMB {
		return CompatibilityCheckResult{
			CheckName: "ram_capacity",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Target has sufficient RAM: %dMB >= %dMB", target.RAMMB, source.RAMMB),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "ram_capacity",
		Severity:  SeverityWarning,
		Passed:    false,
		Message:   fmt.Sprintf("Target has less RAM: %dMB < %dMB", target.RAMMB, source.RAMMB),
	}
}

func (e *CompatibilityEngine) checkDisk(source, target *serverInfo) CompatibilityCheckResult {
	if target.DiskGB >= source.DiskGB {
		return CompatibilityCheckResult{
			CheckName: "disk_capacity",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Target has sufficient disk: %.0fGB >= %.0fGB", target.DiskGB, source.DiskGB),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "disk_capacity",
		Severity:  SeverityCritical,
		Passed:    false,
		Message:   fmt.Sprintf("Target has less disk: %.0fGB < %.0fGB", target.DiskGB, source.DiskGB),
	}
}

func (e *CompatibilityEngine) checkDockerVersion(source, target *serverInfo) CompatibilityCheckResult {
	if source.DockerVersion == "" && target.DockerVersion == "" {
		return CompatibilityCheckResult{
			CheckName: "docker_version",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   "Docker not detected on either server",
		}
	}
	if target.DockerVersion == "" && source.DockerVersion != "" {
		return CompatibilityCheckResult{
			CheckName: "docker_version",
			Severity:  SeverityCritical,
			Passed:    false,
			Message:   "Docker not installed on target but present on source",
		}
	}
	return CompatibilityCheckResult{
		CheckName: "docker_version",
		Severity:  SeverityInfo,
		Passed:    true,
		Message:   fmt.Sprintf("Source: %s, Target: %s", source.DockerVersion, target.DockerVersion),
	}
}

func (e *CompatibilityEngine) checkKernel(source, target *serverInfo) CompatibilityCheckResult {
	return CompatibilityCheckResult{
		CheckName: "kernel_version",
		Severity:  SeverityInfo,
		Passed:    true,
		Message:   fmt.Sprintf("Source: %s, Target: %s", source.Kernel, target.Kernel),
	}
}

func (e *CompatibilityEngine) checkPackageManager(source, target *serverInfo) CompatibilityCheckResult {
	if source.PackageManager == target.PackageManager {
		return CompatibilityCheckResult{
			CheckName: "package_manager",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Both servers use %s", source.PackageManager),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "package_manager",
		Severity:  SeverityWarning,
		Passed:    false,
		Message:   fmt.Sprintf("Different package managers: source=%s target=%s", source.PackageManager, target.PackageManager),
	}
}

func (e *CompatibilityEngine) checkTimezone(source, target *serverInfo) CompatibilityCheckResult {
	if source.Timezone == target.Timezone {
		return CompatibilityCheckResult{
			CheckName: "timezone",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Both servers use %s timezone", source.Timezone),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "timezone",
		Severity:  SeverityWarning,
		Passed:    false,
		Message:   fmt.Sprintf("Timezone mismatch: source=%s target=%s (may affect scheduled jobs)", source.Timezone, target.Timezone),
	}
}

func (e *CompatibilityEngine) checkOpenSSL(source, target *serverInfo) CompatibilityCheckResult {
	return CompatibilityCheckResult{
		CheckName: "openssl_version",
		Severity:  SeverityInfo,
		Passed:    true,
		Message:   fmt.Sprintf("Source: %s, Target: %s", source.OpenSSLVersion, target.OpenSSLVersion),
	}
}

func (e *CompatibilityEngine) checkDockerStorageDriver(source, target *serverInfo) CompatibilityCheckResult {
	if source.StorageDriver == "" || target.StorageDriver == "" {
		return CompatibilityCheckResult{
			CheckName: "docker_storage_driver",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   "Docker storage driver not detected",
		}
	}
	if source.StorageDriver == target.StorageDriver {
		return CompatibilityCheckResult{
			CheckName: "docker_storage_driver",
			Severity:  SeverityInfo,
			Passed:    true,
			Message:   fmt.Sprintf("Both servers use %s storage driver", source.StorageDriver),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "docker_storage_driver",
		Severity:  SeverityWarning,
		Passed:    false,
		Message:   fmt.Sprintf("Storage driver mismatch: source=%s target=%s", source.StorageDriver, target.StorageDriver),
	}
}

func (e *CompatibilityEngine) checkNetworkPorts(ctx context.Context, info *serverInfo) CompatibilityCheckResult {
	return CompatibilityCheckResult{
		CheckName: "network_ports",
		Severity:  SeverityInfo,
		Passed:    true,
		Message:   fmt.Sprintf("%d ports in use on source", len(info.OpenPorts)),
	}
}

func (e *CompatibilityEngine) checkSELinux(source, target *serverInfo) CompatibilityCheckResult {
	if source.SELinux == "Enforcing" && target.SELinux != "Enforcing" {
		return CompatibilityCheckResult{
			CheckName: "selinux",
			Severity:  SeverityWarning,
			Passed:    false,
			Message:   fmt.Sprintf("SELinux enforcing on source but %s on target", target.SELinux),
		}
	}
	return CompatibilityCheckResult{
		CheckName: "selinux",
		Severity:  SeverityInfo,
		Passed:    true,
		Message:   fmt.Sprintf("Source: %s, Target: %s", source.SELinux, target.SELinux),
	}
}
