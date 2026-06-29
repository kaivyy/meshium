package discovery

import (
	"fmt"
	"strconv"
	"strings"
)

// --- Compatibility Types ---

// Severity indicates the severity of a compatibility issue.
type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityBlocker  Severity = "blocker"
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
