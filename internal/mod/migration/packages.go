package migration

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PackagesData holds the collected package list from the source server.
type PackagesData struct {
	Distro   string   `json:"distro"`
	Packages []string `json:"packages"`
	Count    int      `json:"count"`
}

// PackagesBackup holds the target server's package list before migration.
type PackagesBackup struct {
	Distro   string   `json:"distro"`
	Packages []string `json:"packages"`
}

// PackagesCollector collects the package list from the source server.
type PackagesCollector struct{}

// Collect detects the distro and runs the distro-specific list command.
func (c *PackagesCollector) Collect(ssh SSHExecuter) (CategoryData, error) {
	info, err := DetectDistro(ssh)
	if err != nil {
		return CategoryData{}, err
	}
	adapter := GetAdapter(info)

	data := PackagesData{
		Distro: adapter.PackageManager(),
	}

	stdout, _, _, err := ssh.Exec(adapter.ListPackages())
	if err != nil {
		return CategoryData{}, err
	}

	data.Packages = parsePackageList(stdout, adapter.PackageManager())
	data.Count = len(data.Packages)

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "packages", Data: raw}, nil
}

// parsePackageName extracts the package name from a list command output line.
func parsePackageName(line, pm string) string {
	switch pm {
	case "apt":
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ii" || fields[0] == "hi") {
			return fields[1]
		}
		return ""
	case "dnf", "yum":
		idx := strings.LastIndex(line, "-")
		if idx > 0 {
			line = line[:idx]
			idx = strings.LastIndex(line, "-")
			if idx > 0 {
				return line[:idx]
			}
		}
		return line
	case "pacman":
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			return fields[0]
		}
		return ""
	case "apk":
		idx := strings.LastIndex(line, "-")
		if idx > 0 {
			return line[:idx]
		}
		return line
	case "zypper":
		fields := strings.Split(line, "|")
		if len(fields) >= 2 {
			return strings.TrimSpace(fields[1])
		}
		return ""
	default:
		return strings.TrimSpace(line)
	}
}

func parsePackageList(stdout, pm string) []string {
	var packages []string
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg := parsePackageName(line, pm)
		if pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages
}

// PackagesApplier installs packages on the target server.
type PackagesApplier struct{}

// Backup saves the target's current package list.
func (a *PackagesApplier) Backup(ssh SSHExecuter) (BackupData, error) {
	info, err := DetectDistro(ssh)
	if err != nil {
		return BackupData{}, err
	}
	adapter := GetAdapter(info)

	stdout, _, _, err := ssh.Exec(adapter.ListPackages())
	if err != nil {
		return BackupData{}, err
	}

	backup := PackagesBackup{
		Distro:   adapter.PackageManager(),
		Packages: parsePackageList(stdout, adapter.PackageManager()),
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "packages", Data: raw}, nil
}

// Apply installs packages on the target, skipping already-installed ones.
func (a *PackagesApplier) Apply(ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		return err
	}

	info, err := DetectDistro(ssh)
	if err != nil {
		return err
	}
	adapter := GetAdapter(info)
	targetPM := adapter.PackageManager()

	// Get currently installed packages on target
	stdout, _, _, _ := ssh.Exec(adapter.ListPackages())
	installed := make(map[string]bool)
	for _, pkg := range parsePackageList(stdout, targetPM) {
		installed[pkg] = true
	}

	// Map package names if distros differ
	packagesToInstall := make([]string, 0, len(pd.Packages))
	skipped := 0
	for _, pkg := range pd.Packages {
		if installed[pkg] {
			skipped++
			continue
		}
		mapped := MapPackageName(
			DistroInfo{Family: distroFamilyFromPM(pd.Distro)},
			DistroInfo{Family: distroFamilyFromPM(targetPM)},
			pkg,
		)
		packagesToInstall = append(packagesToInstall, mapped)
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Installing %d packages (%d skipped)", len(packagesToInstall), skipped),
		})
	}

	// Install in batches of 50
	batchSize := 50
	for i := 0; i < len(packagesToInstall); i += batchSize {
		end := i + batchSize
		if end > len(packagesToInstall) {
			end = len(packagesToInstall)
		}
		batch := packagesToInstall[i:end]
		cmd := adapter.InstallPackages(batch)
		_, stderr, exitCode, err := ssh.Exec(cmd)
		if err != nil || exitCode != 0 {
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "packages:apply",
					Status: "error",
					Error:  fmt.Sprintf("install failed (exit %d): %s", exitCode, stderr),
				})
			}
			return fmt.Errorf("package install failed: %s", stderr)
		}
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "packages:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Installed %d/%d", end, len(packagesToInstall)),
			})
		}
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "success",
			Value:  fmt.Sprintf("%d packages installed", len(packagesToInstall)),
		})
	}

	return nil
}

// Rollback removes packages that were installed by the migration.
func (a *PackagesApplier) Rollback(ssh SSHExecuter, backup BackupData) error {
	var pb PackagesBackup
	if err := json.Unmarshal(backup.Data, &pb); err != nil {
		return err
	}

	info, err := DetectDistro(ssh)
	if err != nil {
		return err
	}
	adapter := GetAdapter(info)

	stdout, _, _, _ := ssh.Exec(adapter.ListPackages())
	currentPackages := make(map[string]bool)
	for _, pkg := range parsePackageList(stdout, adapter.PackageManager()) {
		currentPackages[pkg] = true
	}

	// Find packages that exist now but weren't in the backup
	var toRemove []string
	for pkg := range currentPackages {
		if !contains(pb.Packages, pkg) {
			toRemove = append(toRemove, pkg)
		}
	}

	if len(toRemove) == 0 {
		return nil
	}

	cmd := adapter.RemovePackages(toRemove)
	_, _, _, err = ssh.Exec(cmd)
	return err
}

// distroFamilyFromPM returns the distro family from a package manager name.
func distroFamilyFromPM(pm string) string {
	switch pm {
	case "apt":
		return "debian"
	case "dnf", "yum":
		return "rhel"
	case "pacman":
		return "arch"
	case "apk":
		return "alpine"
	case "zypper":
		return "suse"
	default:
		return "unknown"
	}
}
