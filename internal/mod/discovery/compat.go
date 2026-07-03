package discovery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- Compatibility Types ---

// Severity indicates the severity of a compatibility issue.
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityBlocker Severity = "blocker"
)

// CompatibilityWarning is a non-blocking issue that the user should be aware of.
type CompatibilityWarning struct {
	// Category is the type of check (e.g., "ram", "disk", "docker", "port", "os").
	Category string `json:"category"`
	// Message describes the warning.
	Message string `json:"message"`
}

// CompatibilityBlocker is a blocking issue that prevents migration.
// If any blockers exist, the migration should not proceed.
type CompatibilityBlocker struct {
	// Category is the type of check.
	Category string `json:"category"`
	// Message describes the blocker.
	Message string `json:"message"`
}

// CompatibilityReport is the result of comparing a source snapshot
// against a target snapshot. It determines whether the target VPS
// can accommodate the workload from the source.
type CompatibilityReport struct {
	// Compatible is true if there are no blockers.
	Compatible bool `json:"compatible"`
	// Warnings are non-blocking issues.
	Warnings []CompatibilityWarning `json:"warnings,omitempty"`
	// Blockers are blocking issues that prevent migration.
	Blockers []CompatibilityBlocker `json:"blockers,omitempty"`
}

// HasBlockers returns true if there are any blocking issues.
func (r *CompatibilityReport) HasBlockers() bool {
	return len(r.Blockers) > 0
}

// --- Compatibility Checker ---

// CheckCompatibility compares a source snapshot against a target snapshot
// and returns a compatibility report.
//
// Checks performed:
//   - RAM: target total RAM must be >= source used RAM
//   - Disk: target total disk must be >= source used disk
//   - Docker: if source has Docker, target must have Docker with compatible version
//   - Port conflicts: target ports must not conflict with source ports
//   - OS: source and target OS should be compatible
func CheckCompatibility(source, target *ServerSnapshot) *CompatibilityReport {
	report := &CompatibilityReport{Compatible: true}

	if source == nil {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "source",
			Message:  "source snapshot is nil",
		})
		report.Compatible = false
		return report
	}
	if target == nil {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "target",
			Message:  "target snapshot is nil",
		})
		report.Compatible = false
		return report
	}

	checkRAM(source, target, report)
	checkDisk(source, target, report)
	checkDocker(source, target, report)
	checkPortConflicts(source, target, report)
	checkOSCompatibility(source, target, report)

	report.Compatible = len(report.Blockers) == 0
	return report
}

// checkRAM verifies that the target has enough RAM for the source workload.
func checkRAM(source, target *ServerSnapshot, report *CompatibilityReport) {
	sourceUsed := source.Hardware.RAMUsedMB
	targetTotal := target.Hardware.RAMTotalMB

	if targetTotal == 0 {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "ram",
			Message:  "target RAM information unavailable, cannot verify capacity",
		})
		return
	}

	if targetTotal < sourceUsed {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "ram",
			Message:  fmt.Sprintf("target RAM %dMB is less than source used RAM %dMB", targetTotal, sourceUsed),
		})
		return
	}

	// Warning if target RAM is less than source total RAM
	if targetTotal < source.Hardware.RAMTotalMB {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "ram",
			Message:  fmt.Sprintf("target RAM %dMB is less than source total RAM %dMB (may be tight)", targetTotal, source.Hardware.RAMTotalMB),
		})
	}
}

// checkDisk verifies that the target has enough disk space.
func checkDisk(source, target *ServerSnapshot, report *CompatibilityReport) {
	sourceUsed := source.Hardware.DiskUsedGB
	targetTotal := target.Hardware.DiskTotalGB

	if targetTotal == 0 {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "disk",
			Message:  "target disk information unavailable, cannot verify capacity",
		})
		return
	}

	if targetTotal < sourceUsed {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "disk",
			Message:  fmt.Sprintf("target disk %.1fGB is less than source used disk %.1fGB", targetTotal, sourceUsed),
		})
		return
	}

	// Warning if target disk is less than source total disk
	if targetTotal < source.Hardware.DiskTotalGB {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "disk",
			Message:  fmt.Sprintf("target disk %.1fGB is less than source total disk %.1fGB (may be tight)", targetTotal, source.Hardware.DiskTotalGB),
		})
	}
}

// checkDocker verifies Docker compatibility.
func checkDocker(source, target *ServerSnapshot, report *CompatibilityReport) {
	if source.Docker == nil {
		return // No Docker on source — nothing to check
	}

	if target.Docker == nil {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "docker",
			Message:  "source has Docker but target does not have Docker installed",
		})
		return
	}

	// Check version compatibility
	if source.Docker.Version != "" && target.Docker.Version != "" {
		cmp := compareVersions(source.Docker.Version, target.Docker.Version)
		if cmp < 0 {
			// Target is newer than source — OK
		} else if cmp > 0 {
			// Target is older than source — warning
			report.Warnings = append(report.Warnings, CompatibilityWarning{
				Category: "docker",
				Message:  fmt.Sprintf("target Docker %s is older than source Docker %s", target.Docker.Version, source.Docker.Version),
			})
		}
		// cmp == 0 means same version — OK
	}

	// Check container count
	if len(source.Docker.Containers) > 0 && target.Docker.Version == "" {
		report.Blockers = append(report.Blockers, CompatibilityBlocker{
			Category: "docker",
			Message:  fmt.Sprintf("source has %d Docker containers but target Docker version is unknown", len(source.Docker.Containers)),
		})
	}
}

// checkPortConflicts checks if target ports conflict with source ports.
func checkPortConflicts(source, target *ServerSnapshot, report *CompatibilityReport) {
	if len(source.NetworkPorts) == 0 || len(target.NetworkPorts) == 0 {
		return
	}

	// Build a set of target ports
	targetPorts := make(map[int]string) // port → process
	for _, p := range target.NetworkPorts {
		targetPorts[p.Port] = p.Process
	}

	// Check if any source port is already in use on target
	for _, sp := range source.NetworkPorts {
		if tp, ok := targetPorts[sp.Port]; ok {
			// Only flag as blocker if the target process is different
			// from the source process (same process = same service, not a conflict)
			if tp != sp.Process {
				report.Blockers = append(report.Blockers, CompatibilityBlocker{
					Category: "port",
					Message:  fmt.Sprintf("port %d is used by %q on target and %q on source", sp.Port, tp, sp.Process),
				})
			}
		}
	}
}

// checkOSCompatibility checks if the source and target OS are compatible.
func checkOSCompatibility(source, target *ServerSnapshot, report *CompatibilityReport) {
	sourceOS := strings.ToLower(source.OS.Distro)
	targetOS := strings.ToLower(target.OS.Distro)

	if sourceOS == "" || targetOS == "" {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "os",
			Message:  "OS information unavailable, cannot verify compatibility",
		})
		return
	}

	// Check if both are same family (Debian-based, RHEL-based, etc.)
	sourceFamily := osFamily(sourceOS)
	targetFamily := osFamily(targetOS)

	if sourceFamily != targetFamily {
		report.Warnings = append(report.Warnings, CompatibilityWarning{
			Category: "os",
			Message:  fmt.Sprintf("source OS family %q differs from target OS family %q — package names may differ", sourceFamily, targetFamily),
		})
	}

	// Check architecture compatibility
	if source.OS.Architecture != "" && target.OS.Architecture != "" {
		if source.OS.Architecture != target.OS.Architecture {
			report.Warnings = append(report.Warnings, CompatibilityWarning{
				Category: "os",
				Message:  fmt.Sprintf("source architecture %s differs from target architecture %s — binaries may not be compatible", source.OS.Architecture, target.OS.Architecture),
			})
		}
	}
}

// osFamily determines the OS family from a distro string.
func osFamily(distro string) string {
	switch {
	case strings.Contains(distro, "ubuntu"), strings.Contains(distro, "debian"):
		return "debian"
	case strings.Contains(distro, "centos"), strings.Contains(distro, "rhel"), strings.Contains(distro, "rocky"), strings.Contains(distro, "alma"):
		return "rhel"
	case strings.Contains(distro, "fedora"):
		return "fedora"
	case strings.Contains(distro, "alpine"):
		return "alpine"
	case strings.Contains(distro, "arch"):
		return "arch"
	default:
		return "unknown"
	}
}

// compareVersions compares two version strings (e.g., "24.0.7" vs "23.0.3").
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Falls back to string comparison if parsing fails.
func compareVersions(a, b string) int {
	aParts := parseVersionParts(a)
	bParts := parseVersionParts(b)

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(aParts) {
			av = aParts[i]
		}
		if i < len(bParts) {
			bv = bParts[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// parseVersionParts extracts numeric parts from a version string.
func parseVersionParts(version string) []int {
	version = strings.TrimSpace(version)
	// Remove leading non-digit characters (e.g., "v" prefix)
	for i, c := range version {
		if c >= '0' && c <= '9' {
			version = version[i:]
			break
		}
	}

	var parts []int
	for _, part := range strings.Split(version, ".") {
		part = strings.TrimSpace(part)
		// Extract leading digits
		digits := ""
		for _, c := range part {
			if c >= '0' && c <= '9' {
				digits += string(c)
			} else {
				break
			}
		}
		if digits == "" {
			break
		}
		n, _ := strconv.Atoi(digits)
		parts = append(parts, n)
	}
	return parts
}

// CompatibilityIssue is a detailed compatibility finding produced by the
// expanded discovery compatibility checker.
type CompatibilityIssue struct {
	Category       string  `json:"category"`
	Severity       string  `json:"severity"`
	Message        string  `json:"message"`
	Recommendation string  `json:"recommendation,omitempty"`
	Blocking       bool    `json:"blocking"`
	RiskScore      float64 `json:"riskScore"`
	ManualAction   string  `json:"manualAction,omitempty"`
	SourceValue    string  `json:"sourceValue,omitempty"`
	TargetValue    string  `json:"targetValue,omitempty"`
}

// CheckOSCompatibility compares operating system family, version, and
// architecture compatibility between source and target snapshots.
func CheckOSCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	var issues []CompatibilityIssue

	sourceDistro := strings.TrimSpace(source.OS.Distro)
	targetDistro := strings.TrimSpace(target.OS.Distro)
	if sourceDistro == "" || targetDistro == "" {
		return issues
	}

	sourceFamily := osFamily(strings.ToLower(sourceDistro))
	targetFamily := osFamily(strings.ToLower(targetDistro))
	if sourceFamily != targetFamily {
		issues = append(issues, newCompatibilityIssue(
			"os",
			"warning",
			fmt.Sprintf("source OS family %q differs from target OS family %q", sourceFamily, targetFamily),
			false,
			sourceFamily,
			targetFamily,
			"Prefer the same base distribution family before migrating workloads.",
			"Validate package repositories, service names, and init-system behavior on the target OS.",
		))
	}

	sourceMajor, sourceOK := versionMajor(sourceDistro)
	targetMajor, targetOK := versionMajor(targetDistro)
	if sourceOK && targetOK && sourceDistro != targetDistro {
		diff := absInt(sourceMajor - targetMajor)
		severity := "info"
		recommendation := "Test package and service behavior on the target release before cutover."
		if diff > 2 {
			severity = "warning"
			recommendation = "Plan extra validation for package compatibility, service defaults, and upgrade steps."
		}
		issues = append(issues, newCompatibilityIssue(
			"os",
			severity,
			fmt.Sprintf("source OS %q differs from target OS %q", sourceDistro, targetDistro),
			false,
			sourceDistro,
			targetDistro,
			recommendation,
			"Review distro-specific package names and default configuration paths.",
		))
	}

	if strings.TrimSpace(source.OS.Architecture) != "" && strings.TrimSpace(target.OS.Architecture) != "" && !strings.EqualFold(strings.TrimSpace(source.OS.Architecture), strings.TrimSpace(target.OS.Architecture)) {
		issues = append(issues, newCompatibilityIssue(
			"architecture",
			"warning",
			fmt.Sprintf("source architecture %s differs from target architecture %s", source.OS.Architecture, target.OS.Architecture),
			false,
			source.OS.Architecture,
			target.OS.Architecture,
			"Use the same CPU architecture or validate multi-architecture container images.",
			"Rebuild native binaries for the target architecture if needed.",
		))
	}

	return issues
}

// CheckKernelCompatibility compares kernel family and major release version.
func CheckKernelCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	sourceKernel := strings.TrimSpace(source.OS.Kernel)
	targetKernel := strings.TrimSpace(target.OS.Kernel)
	if sourceKernel == "" || targetKernel == "" {
		return nil
	}

	var issues []CompatibilityIssue

	sourceFamily := kernelFamily(sourceKernel)
	targetFamily := kernelFamily(targetKernel)
	if sourceFamily != "" && targetFamily != "" && !strings.EqualFold(sourceFamily, targetFamily) {
		issues = append(issues, newCompatibilityIssue(
			"kernel",
			"error",
			fmt.Sprintf("kernel family mismatch: source=%s target=%s", sourceFamily, targetFamily),
			true,
			sourceFamily,
			targetFamily,
			"Match kernel flavors when possible to avoid module and driver compatibility issues.",
			"Install a compatible kernel flavor on the target or validate all required modules and drivers.",
		))
	}

	sourceMajor, sourceOK := versionMajor(sourceKernel)
	targetMajor, targetOK := versionMajor(targetKernel)
	if sourceOK && targetOK {
		diff := absInt(sourceMajor - targetMajor)
		if diff > 2 {
			issues = append(issues, newCompatibilityIssue(
				"kernel",
				"warning",
				fmt.Sprintf("kernel major versions are far apart: source=%s target=%s", sourceKernel, targetKernel),
				false,
				sourceKernel,
				targetKernel,
				"Validate drivers, cgroups behavior, and any kernel-dependent services.",
				"Test kernel-dependent workloads in a staging environment before migration.",
			))
		} else if diff > 0 {
			issues = append(issues, newCompatibilityIssue(
				"kernel",
				"info",
				fmt.Sprintf("kernel major versions differ: source=%s target=%s", sourceKernel, targetKernel),
				false,
				sourceKernel,
				targetKernel,
				"Verify kernel-dependent features such as filesystems, eBPF, or security modules.",
				"Validate any out-of-tree drivers or kernel modules on the target host.",
			))
		}
	}

	return issues
}

// CheckFilesystemCompatibility compares mounted filesystems and mount options.
func CheckFilesystemCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if len(source.Filesystems) == 0 {
		return nil
	}

	var issues []CompatibilityIssue
	targetByMount := filesystemByMountPoint(target.Filesystems)
	targetTypes := filesystemTypes(target.Filesystems)

	for _, srcFS := range source.Filesystems {
		if strings.TrimSpace(srcFS.MountPoint) == "" && strings.TrimSpace(srcFS.FSType) == "" {
			continue
		}

		tgtFS, ok := targetByMount[strings.TrimSpace(srcFS.MountPoint)]
		if !ok {
			issues = append(issues, newCompatibilityIssue(
				"filesystem",
				"warning",
				fmt.Sprintf("source filesystem %s mounted at %s is missing on target", srcFS.FSType, srcFS.MountPoint),
				false,
				srcFS.FSType,
				"",
				"Recreate the same mount point or migrate the backing volume before cutover.",
				"Provision matching storage and mount it at the same path on the target.",
			))
			continue
		}

		srcType := strings.ToLower(strings.TrimSpace(srcFS.FSType))
		tgtType := strings.ToLower(strings.TrimSpace(tgtFS.FSType))
		if srcType != "" && tgtType != "" && srcType != tgtType {
			severity := "warning"
			recommendation := "Validate filesystem-specific features and mount options before migrating."
			if srcType == "ext4" && tgtType == "xfs" {
				severity = "info"
				recommendation = "ext4 to xfs is usually workable, but validate filesystem-specific behavior first."
			} else if srcType == "btrfs" && !targetTypes["btrfs"] {
				severity = "warning"
				recommendation = "btrfs filesystems should be recreated or migrated to a target that supports btrfs."
			}
			issues = append(issues, newCompatibilityIssue(
				"filesystem",
				severity,
				fmt.Sprintf("filesystem type differs at %s: source=%s target=%s", srcFS.MountPoint, srcFS.FSType, tgtFS.FSType),
				false,
				srcFS.FSType,
				tgtFS.FSType,
				recommendation,
				"Recreate the mount with the same semantics if the workload depends on filesystem behavior.",
			))
		}

		srcOpts := optionSet(srcFS.MountOptions)
		tgtOpts := optionSet(tgtFS.MountOptions)
		missingOpts := missingOptions(srcOpts, tgtOpts)
		if len(missingOpts) > 0 {
			issues = append(issues, newCompatibilityIssue(
				"filesystem",
				"warning",
				fmt.Sprintf("target mount %s is missing source mount options: %s", srcFS.MountPoint, strings.Join(missingOpts, ", ")),
				false,
				srcFS.MountOptions,
				tgtFS.MountOptions,
				"Recreate the target mount with the required options or adjust the workload expectations.",
				"Remount the filesystem with compatible mount flags.",
			))
		}
	}

	return issues
}

// CheckDockerCompatibility compares Docker engine, storage, and compose support.
func CheckDockerCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if source.Docker == nil && target.Docker == nil {
		return nil
	}

	var issues []CompatibilityIssue
	if source.Docker != nil && target.Docker == nil {
		issues = append(issues, newCompatibilityIssue(
			"docker",
			"error",
			"source has Docker workloads but target does not have Docker installed",
			true,
			"installed on source",
			"missing on target",
			"Install Docker on the target before cutover.",
			"Install Docker Engine and verify the compose plugin on the target host.",
		))
		return issues
	}
	if source.Docker == nil || target.Docker == nil {
		return issues
	}

	sourceVersion := strings.TrimSpace(source.Docker.Version)
	targetVersion := strings.TrimSpace(target.Docker.Version)
	if sourceVersion != "" && targetVersion != "" {
		sourceMajor, sOK := versionMajor(sourceVersion)
		targetMajor, tOK := versionMajor(targetVersion)
		if sOK && tOK && sourceMajor != targetMajor {
			issues = append(issues, newCompatibilityIssue(
				"docker",
				"warning",
				fmt.Sprintf("Docker major versions differ: source=%s target=%s", sourceVersion, targetVersion),
				false,
				sourceVersion,
				targetVersion,
				"Validate Docker API behavior, image compatibility, and compose support.",
				"Test the workload with the target Docker engine before migration.",
			))
		}
	} else if sourceVersion != targetVersion {
		issues = append(issues, newCompatibilityIssue(
			"docker",
			"warning",
			"Docker version information is incomplete on one or both servers",
			false,
			sourceVersion,
			targetVersion,
			"Install or query Docker on both hosts to verify version compatibility.",
			"Run `docker version` on the target and confirm the engine and compose plugin versions.",
		))
	}

	sourceDriver := strings.TrimSpace(source.StorageDriver)
	targetDriver := strings.TrimSpace(target.StorageDriver)
	if sourceDriver != "" && targetDriver != "" && !strings.EqualFold(sourceDriver, targetDriver) {
		issues = append(issues, newCompatibilityIssue(
			"docker",
			"warning",
			fmt.Sprintf("Docker storage driver differs: source=%s target=%s", sourceDriver, targetDriver),
			false,
			sourceDriver,
			targetDriver,
			"Prefer the same storage driver when moving container layers and volumes.",
			"Plan for image and layer re-pulls or a storage-driver migration.",
		))
	}

	if source.DockerRoot != "" || target.DockerRoot != "" {
		srcRootFS := filesystemForPath(source.Filesystems, source.DockerRoot)
		tgtRootFS := filesystemForPath(target.Filesystems, target.DockerRoot)
		if srcRootFS != nil && tgtRootFS != nil && tgtRootFS.AvailGB < srcRootFS.UsedGB {
			issues = append(issues, newCompatibilityIssue(
				"docker",
				"warning",
				fmt.Sprintf("Docker root filesystem on target has less free space than the source uses (%s)", target.DockerRoot),
				false,
				fmt.Sprintf("source used %.1f GB", srcRootFS.UsedGB),
				fmt.Sprintf("target available %.1f GB", tgtRootFS.AvailGB),
				"Free up space or move Docker root to a larger filesystem before migration.",
				"Relocate Docker data-root or expand the target filesystem.",
			))
		}
	}

	if len(source.Docker.ComposeProjects) > 0 {
		if targetVersion == "" {
			issues = append(issues, newCompatibilityIssue(
				"docker",
				"warning",
				"source uses docker compose projects but target compose availability could not be verified",
				false,
				"compose projects present on source",
				"compose version unavailable on target",
				"Install Docker Compose v2 on the target and validate project files.",
				"Run `docker compose version` on the target host and confirm plugin availability.",
			))
		} else {
			issues = append(issues, newCompatibilityIssue(
				"docker",
				"info",
				fmt.Sprintf("source has %d compose project(s); target Docker %s should support compose operations", len(source.Docker.ComposeProjects), targetVersion),
				false,
				strconv.Itoa(len(source.Docker.ComposeProjects)),
				targetVersion,
				"Validate compose project syntax and plugin availability on the target host.",
				"Run `docker compose config` or `docker compose version` on the target before cutover.",
			))
		}
	}

	return issues
}

// CheckRuntimeCompatibility verifies that all source runtimes are present on
// the target and that major versions do not diverge unexpectedly.
func CheckRuntimeCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if len(source.Runtimes) == 0 {
		return nil
	}

	var issues []CompatibilityIssue
	targetRuntimes := runtimeByName(target.Runtimes)
	for _, srcRuntime := range source.Runtimes {
		tgtRuntime, ok := targetRuntimes[strings.ToLower(strings.TrimSpace(srcRuntime.Name))]
		if !ok {
			issues = append(issues, newCompatibilityIssue(
				"runtime",
				"error",
				fmt.Sprintf("required runtime %s is missing on the target", srcRuntime.Name),
				true,
				srcRuntime.Version,
				"missing",
				"Install the runtime on the target before migrating the workload.",
				"Install the same runtime family and confirm the executable path.",
			))
			continue
		}

		sourceMajor, sOK := versionMajor(srcRuntime.Version)
		targetMajor, tOK := versionMajor(tgtRuntime.Version)
		if sOK && tOK && sourceMajor != targetMajor {
			issues = append(issues, newCompatibilityIssue(
				"runtime",
				"warning",
				fmt.Sprintf("runtime %s major versions differ: source=%s target=%s", srcRuntime.Name, srcRuntime.Version, tgtRuntime.Version),
				false,
				srcRuntime.Version,
				tgtRuntime.Version,
				"Validate language/runtime-specific dependencies and package managers.",
				"Recreate the runtime environment on the target or align version managers.",
			))
		}
	}

	return issues
}

// CheckPortConflicts reports any ports used on the source that are already in
// use on the target.
func CheckPortConflicts(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if len(source.NetworkPorts) == 0 || len(target.NetworkPorts) == 0 {
		return nil
	}

	var issues []CompatibilityIssue
	targetPorts := portByNumber(target.NetworkPorts)
	seen := make(map[int]struct{})
	for _, srcPort := range source.NetworkPorts {
		if srcPort.Port == 0 {
			continue
		}
		if _, ok := seen[srcPort.Port]; ok {
			continue
		}
		seen[srcPort.Port] = struct{}{}
		if tgtPort, ok := targetPorts[srcPort.Port]; ok {
			issues = append(issues, newCompatibilityIssue(
				"port",
				"error",
				fmt.Sprintf("port %d is already in use on the target", srcPort.Port),
				true,
				srcPort.Process,
				tgtPort.Process,
				"Stop or rebind the conflicting service before migrating the workload.",
				"Move the target service off the port or allocate a different listen port.",
			))
		}
	}

	return issues
}

// CheckFirewallCompatibility verifies that the target firewall allows the
// ports required by the source.
func CheckFirewallCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if target.Firewall == nil && len(source.NetworkPorts) == 0 {
		return nil
	}

	var issues []CompatibilityIssue
	if source.Firewall != nil && target.Firewall != nil && !strings.EqualFold(strings.TrimSpace(source.Firewall.Type), strings.TrimSpace(target.Firewall.Type)) {
		issues = append(issues, newCompatibilityIssue(
			"firewall",
			"info",
			fmt.Sprintf("firewall type differs: source=%s target=%s", source.Firewall.Type, target.Firewall.Type),
			false,
			source.Firewall.Type,
			target.Firewall.Type,
			"Review firewall rule syntax and rule import procedures for the target firewall.",
			"Translate source firewall rules to the target firewall format if needed.",
		))
	}

	if len(source.NetworkPorts) == 0 {
		return issues
	}

	if target.Firewall == nil || !target.Firewall.Active {
		issues = append(issues, newCompatibilityIssue(
			"firewall",
			"warning",
			"target firewall is inactive or unavailable, so required source ports cannot be verified",
			false,
			"source ports present",
			"firewall inactive",
			"Enable and configure the target firewall before cutover.",
			"Open the required ports in the target firewall and confirm the rules persist after reboot.",
		))
		return issues
	}

	requiredPorts := uniqueSourcePorts(source.NetworkPorts)
	for _, port := range requiredPorts {
		if !firewallAllowsPort(target.Firewall.Rules, port) {
			issues = append(issues, newCompatibilityIssue(
				"firewall",
				"warning",
				fmt.Sprintf("target firewall does not explicitly allow required port %d", port),
				false,
				strconv.Itoa(port),
				strings.Join(target.Firewall.Rules, " | "),
				"Add a firewall rule for the required port before migration.",
				"Open the port on the target firewall and verify it after reload.",
			))
		}
	}

	return issues
}

// CheckSecurityCompatibility compares SELinux and AppArmor posture.
func CheckSecurityCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	var issues []CompatibilityIssue
	if source.SELinux != nil && target.SELinux != nil {
		sourceMode := strings.TrimSpace(source.SELinux.Mode)
		targetMode := strings.TrimSpace(target.SELinux.Mode)
		if target.SELinux.Enabled && !source.SELinux.Enabled {
			issues = append(issues, newCompatibilityIssue(
				"security",
				"warning",
				"SELinux is enforcing on the target but not on the source",
				false,
				sourceMode,
				targetMode,
				"Validate application labels and policies on the target before cutover.",
				"Adjust SELinux policies or disable enforcement only if your security requirements permit it.",
			))
		}
		if !strings.EqualFold(sourceMode, targetMode) || source.SELinux.Enabled != target.SELinux.Enabled {
			issues = append(issues, newCompatibilityIssue(
				"security",
				"info",
				fmt.Sprintf("SELinux context differs: source=%s target=%s", sourceMode, targetMode),
				false,
				sourceMode,
				targetMode,
				"Review the SELinux policy and labeling requirements for the migrated workload.",
				"Confirm file contexts, booleans, and local policy modules on the target.",
			))
		}
	}

	if source.AppArmor != nil {
		sourceEnabled := source.AppArmor.Enabled
		targetEnabled := target.AppArmor != nil && target.AppArmor.Enabled
		if sourceEnabled && (!targetEnabled || len(target.AppArmor.Profiles) == 0) {
			issues = append(issues, newCompatibilityIssue(
				"security",
				"warning",
				"AppArmor profiles from the source are missing on the target",
				false,
				strings.Join(source.AppArmor.Profiles, ", "),
				func() string {
					if target.AppArmor == nil {
						return "disabled"
					}
					return strings.Join(target.AppArmor.Profiles, ", ")
				}(),
				"Copy or recreate AppArmor profiles on the target if the workload depends on them.",
				"Install the missing profiles and reload AppArmor before migration.",
			))
		}
		if sourceEnabled != targetEnabled {
			issues = append(issues, newCompatibilityIssue(
				"security",
				"info",
				"AppArmor enforcement differs between source and target",
				false,
				strconv.FormatBool(sourceEnabled),
				strconv.FormatBool(targetEnabled),
				"Check application confinement rules on the target host.",
				"Verify any custom profiles and their load status.",
			))
		}
	}

	return issues
}

// CheckDatabaseCompatibility compares database engine types and versions.
func CheckDatabaseCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	if len(source.Databases) == 0 {
		return nil
	}

	var issues []CompatibilityIssue
	targetDBs := databaseByType(target.Databases)
	for _, srcDB := range source.Databases {
		dbType := strings.ToLower(strings.TrimSpace(srcDB.Type))
		if dbType == "" {
			continue
		}

		tgtDB, ok := targetDBs[dbType]
		if !ok {
			issues = append(issues, newCompatibilityIssue(
				"database",
				"error",
				fmt.Sprintf("required database %s is missing on the target", srcDB.Type),
				true,
				srcDB.Version,
				"missing",
				"Provision the database on the target before migrating the data.",
				"Install the database engine and restore or replicate the data set.",
			))
			continue
		}

		sourceMajor, sOK := versionMajor(srcDB.Version)
		targetMajor, tOK := versionMajor(tgtDB.Version)
		switch dbType {
		case "redis":
			if srcDB.Version != "" && tgtDB.Version != "" && compareVersions(srcDB.Version, tgtDB.Version) != 0 {
				issues = append(issues, newCompatibilityIssue(
					"database",
					"warning",
					fmt.Sprintf("Redis version differs: source=%s target=%s", srcDB.Version, tgtDB.Version),
					false,
					srcDB.Version,
					tgtDB.Version,
					"Validate persistence format and replication behavior across Redis versions.",
					"Test cache warm-up and replication settings on the target.",
				))
			}
		case "mysql", "postgresql":
			if sOK && tOK {
				diff := targetMajor - sourceMajor
				if diff < 0 {
					issues = append(issues, newCompatibilityIssue(
						"database",
						"error",
						fmt.Sprintf("%s downgrade is not supported: source=%s target=%s", strings.Title(dbType), srcDB.Version, tgtDB.Version),
						true,
						srcDB.Version,
						tgtDB.Version,
						"Upgrade or equalize the target database version before restoring data.",
						"Use a supported replication or dump/restore path for the version pair.",
					))
				} else if diff > 0 {
					issues = append(issues, newCompatibilityIssue(
						"database",
						"info",
						fmt.Sprintf("%s upgrade path detected: source=%s target=%s", strings.Title(dbType), srcDB.Version, tgtDB.Version),
						false,
						srcDB.Version,
						tgtDB.Version,
						"Verify extension support, SQL modes, and replication settings for the upgrade path.",
						"Run a full staging restore with production data before cutover.",
					))
				}
			}
		default:
			if sOK && tOK {
				if targetMajor < sourceMajor {
					issues = append(issues, newCompatibilityIssue(
						"database",
						"error",
						fmt.Sprintf("database downgrade is not supported for %s: source=%s target=%s", srcDB.Type, srcDB.Version, tgtDB.Version),
						true,
						srcDB.Version,
						tgtDB.Version,
						"Avoid downgrading database engines during migration.",
						"Upgrade the target engine or keep the source version until after migration.",
					))
				} else if targetMajor > sourceMajor {
					issues = append(issues, newCompatibilityIssue(
						"database",
						"info",
						fmt.Sprintf("database version differs for %s: source=%s target=%s", srcDB.Type, srcDB.Version, tgtDB.Version),
						false,
						srcDB.Version,
						tgtDB.Version,
						"Validate driver compatibility, extensions, and schema migrations.",
						"Test a replica or restore in a staging environment first.",
					))
				}
			}
		}
	}

	return issues
}

// CheckStorageCompatibility compares available target storage against source data needs.
func CheckStorageCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	sourceDataSize := source.Hardware.DiskUsedGB
	if dbSize := totalDatabaseSizeGB(source.Databases); dbSize > sourceDataSize {
		sourceDataSize = dbSize
	}
	targetFree := target.Hardware.DiskTotalGB - target.Hardware.DiskUsedGB

	var issues []CompatibilityIssue
	if sourceDataSize > 0 && targetFree < sourceDataSize {
		issues = append(issues, newCompatibilityIssue(
			"storage",
			"error",
			fmt.Sprintf("target disk space is insufficient: source needs %.1f GB and target has %.1f GB free", sourceDataSize, targetFree),
			true,
			fmt.Sprintf("source requires %.1f GB", sourceDataSize),
			fmt.Sprintf("target free %.1f GB", targetFree),
			"Increase target storage or reduce the source data footprint before migration.",
			"Expand the target disk or move large data sets to dedicated storage.",
		))
	}

	if source.StorageDriver != "" && target.StorageDriver != "" && !strings.EqualFold(source.StorageDriver, target.StorageDriver) {
		issues = append(issues, newCompatibilityIssue(
			"storage",
			"warning",
			fmt.Sprintf("storage driver differs: source=%s target=%s", source.StorageDriver, target.StorageDriver),
			false,
			source.StorageDriver,
			target.StorageDriver,
			"Prefer the same storage driver to minimize layer and volume migration issues.",
			"Plan for driver-specific data migration or image repulls.",
		))
	}

	if volumeLayoutCompatible(source.Filesystems, target.Filesystems) {
		issues = append(issues, newCompatibilityIssue(
			"storage",
			"info",
			"source volume layout appears to be reproducible on the target",
			false,
			strconv.Itoa(len(source.Filesystems)),
			strconv.Itoa(len(target.Filesystems)),
			"Validate that mount points and fstab entries are recreated on the target.",
			"Mirror the source volume layout and confirm the mounts after boot.",
		))
	}

	return issues
}

// CheckNetworkCompatibility compares bind addresses and DNS configuration.
func CheckNetworkCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	var issues []CompatibilityIssue
	sourceAddrs := addressSet(source.NetworkPorts)
	targetAddrs := addressSet(target.NetworkPorts)
	missingAddrs := setDifference(sourceAddrs, targetAddrs)
	if len(missingAddrs) > 0 {
		issues = append(issues, newCompatibilityIssue(
			"network",
			"warning",
			fmt.Sprintf("target is missing some source bind addresses: %s", strings.Join(missingAddrs, ", ")),
			false,
			strings.Join(missingAddrs, ", "),
			strings.Join(setToSortedList(targetAddrs), ", "),
			"Confirm the target has the same network interfaces and binding addresses.",
			"Adjust listeners, interface bindings, or cloud security groups as needed.",
		))
	}

	if source.DNS != nil && target.DNS != nil {
		if !dnsEqual(source.DNS, target.DNS) {
			issues = append(issues, newCompatibilityIssue(
				"network",
				"info",
				"DNS configuration differs between source and target",
				false,
				strings.Join(source.DNS.Nameservers, ", "),
				strings.Join(target.DNS.Nameservers, ", "),
				"Verify resolvers and search domains before cutover.",
				"Synchronize resolv.conf, DHCP, or network manager settings as required.",
			))
		}
	}

	return issues
}

// CheckSSLCompatibility compares certificate transfer needs and renewal tooling.
func CheckSSLCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	var issues []CompatibilityIssue
	sourceCerts := sslCertsFromSnapshot(source)
	if len(sourceCerts) == 0 {
		return nil
	}

	if packageAvailable(target.Packages, "certbot") {
		issues = append(issues, newCompatibilityIssue(
			"ssl",
			"info",
			"certbot is available on the target",
			false,
			"certbot installed",
			"available on target",
			"Use certbot to recreate or renew certificates on the target.",
			"Confirm ACME account data and renewal timers after migration.",
		))
	}

	for _, cert := range sourceCerts {
		if isSelfSignedCert(cert) {
			issues = append(issues, newCompatibilityIssue(
				"ssl",
				"warning",
				fmt.Sprintf("certificate for %s appears to be self-signed", cert.Domain),
				false,
				cert.Path,
				cert.Issuer,
				"Plan to transfer or recreate self-signed certificates manually.",
				"Copy the certificate chain and private key securely or replace the certificate on the target.",
			))
		}
	}

	return issues
}

// CheckCloudCompatibility compares likely cloud provider characteristics.
func CheckCloudCompatibility(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	sourceCloud := cloudLabel(source)
	targetCloud := cloudLabel(target)
	if sourceCloud == "" && targetCloud == "" {
		return nil
	}

	var issues []CompatibilityIssue
	if sourceCloud != "" && targetCloud != "" && sourceCloud != targetCloud {
		issues = append(issues, newCompatibilityIssue(
			"cloud",
			"info",
			fmt.Sprintf("cloud provider characteristics differ: source=%s target=%s", sourceCloud, targetCloud),
			false,
			sourceCloud,
			targetCloud,
			"Validate provider-specific networking, IAM, and storage defaults on the target.",
			"Recreate any provider-specific integrations before migration.",
		))
	}

	if sourceCloud == "baremetal" && targetCloud != "" && targetCloud != "baremetal" {
		issues = append(issues, newCompatibilityIssue(
			"cloud",
			"info",
			"source appears to be on-premises while target appears to be cloud-hosted",
			false,
			"on-premises",
			targetCloud,
			"Review cloud-init, security groups, metadata service access, and IAM dependencies.",
			"Prepare cloud-specific bootstrapping and access controls before cutover.",
		))
	}

	if isCloudLike(targetCloud) {
		issues = append(issues, newCompatibilityIssue(
			"cloud",
			"warning",
			fmt.Sprintf("target appears to rely on cloud-specific capabilities (%s)", targetCloud),
			false,
			sourceCloud,
			targetCloud,
			"Validate metadata service access, IAM roles, and ephemeral storage behavior.",
			"Recreate cloud-native dependencies and instance metadata assumptions on the target.",
		))
	}

	return issues
}

// RunAllCompatibilityChecks executes every discovery compatibility check and
// returns the combined issues in a deterministic order.
func RunAllCompatibilityChecks(source, target *ServerSnapshot) []CompatibilityIssue {
	if source == nil || target == nil {
		return nil
	}

	checks := [][]CompatibilityIssue{
		CheckOSCompatibility(source, target),
		CheckKernelCompatibility(source, target),
		CheckFilesystemCompatibility(source, target),
		CheckDockerCompatibility(source, target),
		CheckRuntimeCompatibility(source, target),
		CheckPortConflicts(source, target),
		CheckFirewallCompatibility(source, target),
		CheckSecurityCompatibility(source, target),
		CheckDatabaseCompatibility(source, target),
		CheckStorageCompatibility(source, target),
		CheckNetworkCompatibility(source, target),
		CheckSSLCompatibility(source, target),
		CheckCloudCompatibility(source, target),
	}

	var issues []CompatibilityIssue
	for _, group := range checks {
		issues = append(issues, group...)
	}
	return issues
}

func newCompatibilityIssue(category, severity, message string, blocking bool, sourceValue, targetValue, recommendation, manualAction string) CompatibilityIssue {
	issue := CompatibilityIssue{
		Category:       category,
		Severity:       severity,
		Message:        message,
		Recommendation: recommendation,
		Blocking:       blocking,
		RiskScore:      riskScoreForSeverity(severity, blocking),
		ManualAction:   manualAction,
		SourceValue:    sourceValue,
		TargetValue:    targetValue,
	}
	return issue
}

func riskScoreForSeverity(severity string, blocking bool) float64 {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "info":
		return 10
	case "warning":
		if blocking {
			return 80
		}
		return 40
	case "error":
		return 80
	case "critical":
		return 95
	default:
		if blocking {
			return 80
		}
		return 20
	}
}

func versionMajor(version string) (int, bool) {
	parts := parseVersionParts(version)
	if len(parts) == 0 {
		return 0, false
	}
	return parts[0], true
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func kernelFamily(kernel string) string {
	kernel = strings.TrimSpace(kernel)
	if kernel == "" {
		return ""
	}
	parts := strings.Split(kernel, "-")
	if len(parts) < 2 {
		return "generic"
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return "generic"
	}
	if _, err := strconv.Atoi(last); err == nil {
		return "generic"
	}
	return last
}

func filesystemByMountPoint(filesystems []FilesystemInfo) map[string]FilesystemInfo {
	result := make(map[string]FilesystemInfo, len(filesystems))
	for _, fs := range filesystems {
		mount := strings.TrimSpace(fs.MountPoint)
		if mount == "" {
			continue
		}
		result[mount] = fs
	}
	return result
}

func filesystemTypes(filesystems []FilesystemInfo) map[string]bool {
	result := make(map[string]bool, len(filesystems))
	for _, fs := range filesystems {
		t := strings.ToLower(strings.TrimSpace(fs.FSType))
		if t != "" {
			result[t] = true
		}
	}
	return result
}

func filesystemForPath(filesystems []FilesystemInfo, path string) *FilesystemInfo {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	var (
		best    *FilesystemInfo
		bestLen int
	)
	for i := range filesystems {
		mount := strings.TrimSpace(filesystems[i].MountPoint)
		if mount == "" {
			continue
		}
		if !strings.HasPrefix(path, mount) {
			continue
		}
		if len(mount) > bestLen {
			best = &filesystems[i]
			bestLen = len(mount)
		}
	}
	return best
}

func optionSet(options string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, opt := range strings.Split(options, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		result[opt] = struct{}{}
	}
	return result
}

func missingOptions(source, target map[string]struct{}) []string {
	var missing []string
	for opt := range source {
		if _, ok := target[opt]; !ok {
			missing = append(missing, opt)
		}
	}
	sort.Strings(missing)
	return missing
}

func runtimeByName(runtimes []RuntimeInfo) map[string]RuntimeInfo {
	result := make(map[string]RuntimeInfo, len(runtimes))
	for _, runtime := range runtimes {
		name := strings.ToLower(strings.TrimSpace(runtime.Name))
		if name == "" {
			continue
		}
		result[name] = runtime
	}
	return result
}

func portByNumber(ports []OpenPort) map[int]OpenPort {
	result := make(map[int]OpenPort, len(ports))
	for _, port := range ports {
		if port.Port == 0 {
			continue
		}
		if _, ok := result[port.Port]; !ok {
			result[port.Port] = port
		}
	}
	return result
}

func uniqueSourcePorts(ports []OpenPort) []int {
	seen := make(map[int]struct{})
	var result []int
	for _, port := range ports {
		if port.Port == 0 {
			continue
		}
		if _, ok := seen[port.Port]; ok {
			continue
		}
		seen[port.Port] = struct{}{}
		result = append(result, port.Port)
	}
	sort.Ints(result)
	return result
}

func firewallAllowsPort(rules []string, port int) bool {
	needle := strconv.Itoa(port)
	for _, rule := range rules {
		normalized := strings.NewReplacer(",", " ", ":", " ", "/", " ", "(", " ", ")", " ", "[", " ", "]", " ", "=", " ").Replace(strings.ToLower(rule))
		for _, field := range strings.Fields(normalized) {
			if field == needle {
				return true
			}
		}
	}
	return false
}

func databaseByType(databases []DatabaseInfo) map[string]DatabaseInfo {
	result := make(map[string]DatabaseInfo, len(databases))
	for _, db := range databases {
		t := strings.ToLower(strings.TrimSpace(db.Type))
		if t == "" {
			continue
		}
		if _, ok := result[t]; !ok {
			result[t] = db
		}
	}
	return result
}

func totalDatabaseSizeGB(databases []DatabaseInfo) float64 {
	var total float64
	for _, db := range databases {
		if db.SizeMB <= 0 {
			continue
		}
		total += float64(db.SizeMB) / 1024.0
	}
	return total
}

func addressSet(ports []OpenPort) map[string]struct{} {
	result := make(map[string]struct{})
	for _, port := range ports {
		addr := strings.TrimSpace(port.Address)
		if addr == "" {
			continue
		}
		result[addr] = struct{}{}
	}
	return result
}

func setDifference(source, target map[string]struct{}) []string {
	var missing []string
	for value := range source {
		if _, ok := target[value]; !ok {
			missing = append(missing, value)
		}
	}
	sort.Strings(missing)
	return missing
}

func setToSortedList(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func dnsEqual(source, target *DNSInfo) bool {
	if source == nil || target == nil {
		return source == target
	}
	if strings.TrimSpace(source.SearchDomain) != strings.TrimSpace(target.SearchDomain) {
		return false
	}
	if len(source.Nameservers) != len(target.Nameservers) {
		return false
	}
	for i := range source.Nameservers {
		if strings.TrimSpace(source.Nameservers[i]) != strings.TrimSpace(target.Nameservers[i]) {
			return false
		}
	}
	return true
}

func sslCertsFromSnapshot(snapshot *ServerSnapshot) []SSLCertInfo {
	if snapshot == nil {
		return nil
	}
	var certs []SSLCertInfo
	certs = append(certs, snapshot.SSL...)
	if snapshot.Nginx != nil {
		for _, cert := range snapshot.Nginx.SSLCerts {
			certs = append(certs, SSLCertInfo{
				Domain:        cert.Domain,
				Path:          cert.Path,
				Expiry:        cert.Expiry.Format(time.RFC3339),
				DaysRemaining: cert.DaysRemaining,
				Issuer:        cert.Issuer,
			})
		}
	}
	return certs
}

func packageAvailable(packages []string, name string) bool {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return false
	}
	for _, pkg := range packages {
		pkg = strings.ToLower(strings.TrimSpace(pkg))
		if pkg == "" {
			continue
		}
		if pkg == needle || strings.Contains(pkg, needle) {
			return true
		}
	}
	return false
}

func isSelfSignedCert(cert SSLCertInfo) bool {
	issuer := strings.ToLower(strings.TrimSpace(cert.Issuer))
	if strings.Contains(issuer, "self-signed") || strings.Contains(issuer, "self signed") {
		return true
	}
	path := strings.ToLower(strings.TrimSpace(cert.Path))
	return strings.Contains(path, "self-signed") || strings.Contains(path, "self_signed")
}

func cloudLabel(snapshot *ServerSnapshot) string {
	if snapshot == nil {
		return ""
	}
	hint := strings.ToLower(strings.TrimSpace(snapshot.OS.Virtualization))
	switch {
	case hint == "" || hint == "none" || hint == "baremetal" || hint == "bare metal":
		return "baremetal"
	case strings.Contains(hint, "aws") || strings.Contains(hint, "ec2"):
		return "aws"
	case strings.Contains(hint, "azure"):
		return "azure"
	case strings.Contains(hint, "gce") || strings.Contains(hint, "gcp") || strings.Contains(hint, "google"):
		return "gcp"
	case strings.Contains(hint, "openstack"):
		return "openstack"
	case strings.Contains(hint, "vmware"):
		return "vmware"
	case strings.Contains(hint, "kvm") || strings.Contains(hint, "qemu") || strings.Contains(hint, "hyperv") || strings.Contains(hint, "virtualbox") || strings.Contains(hint, "xen"):
		return "virtualized"
	case strings.Contains(hint, "cloud"):
		return "cloud"
	default:
		return hint
	}
}

func isCloudLike(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "aws", "azure", "gcp", "openstack", "cloud":
		return true
	default:
		return false
	}
}

func volumeLayoutCompatible(source, target []FilesystemInfo) bool {
	if len(source) == 0 || len(target) == 0 {
		return false
	}
	targetMounts := filesystemByMountPoint(target)
	matched := 0
	for _, fs := range source {
		if strings.TrimSpace(fs.MountPoint) == "" {
			continue
		}
		if _, ok := targetMounts[strings.TrimSpace(fs.MountPoint)]; ok {
			matched++
		}
	}
	return matched > 0 && matched == len(nonEmptyMounts(source))
}

func nonEmptyMounts(filesystems []FilesystemInfo) []FilesystemInfo {
	result := make([]FilesystemInfo, 0, len(filesystems))
	for _, fs := range filesystems {
		if strings.TrimSpace(fs.MountPoint) != "" {
			result = append(result, fs)
		}
	}
	return result
}
