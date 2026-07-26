package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"meshium/internal/shared"
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
func (c *PackagesCollector) Collect(ctx context.Context, ssh SSHExecuter) (CategoryData, error) {
	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return CategoryData{}, err
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return CategoryData{}, err
	}

	data := PackagesData{
		Distro: adapter.PackageManager(),
	}

	stdout, stderr, exit, err := ssh.ExecContext(ctx, adapter.ListPackages())
	if err != nil {
		return CategoryData{}, err
	}
	// A missing tool or a permission failure exits non-zero with empty stdout.
	// Ignoring the exit code stored that as a successful collect of zero
	// packages, and the migration carried an empty source snapshot forward
	// with no warning.
	if exit != 0 {
		return CategoryData{}, fmt.Errorf("listing %s packages failed (exit %d): %s",
			adapter.PackageManager(), exit, strings.TrimSpace(firstLine(stderr)))
	}

	data.Packages = parsePackageList(stdout, adapter.PackageManager())
	data.Count = len(data.Packages)
	if data.Count == 0 {
		return CategoryData{}, fmt.Errorf("listing %s packages returned no packages — "+
			"refusing to record an empty source snapshot (command: %s)",
			adapter.PackageManager(), adapter.ListPackages())
	}

	raw, _ := json.Marshal(data)
	return CategoryData{Type: "packages", Data: raw}, nil
}

// parsePackageName extracts the package name from one line of the
// corresponding adapter's ListPackages() output.
//
// Every manager except apt is asked for bare names (rpm --qf '%{NAME}',
// pacman -Qq, apk info), so the name is the line. Only apt carries a leading
// install-status column, which must be filtered: `rc`/`ic` rows are packages
// removed but still holding config, and reinstalling them on the target would
// be wrong.
//
// Do not add version-stripping here. Package names legitimately contain
// dashes ("python3-libs", "musl-utils"), so trimming dash-separated suffixes
// silently corrupts them — that is what this function used to do.
func parsePackageName(line, pm string) string {
	switch pm {
	case "apt":
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "ii" || fields[0] == "hi") {
			return fields[1]
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
func (a *PackagesApplier) Backup(ctx context.Context, ssh SSHExecuter) (BackupData, error) {
	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return BackupData{}, err
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return BackupData{}, err
	}

	stdout, _, exit, err := ssh.ExecContext(ctx, adapter.ListPackages())
	if err != nil {
		return BackupData{}, err
	}
	// Same guards as Collect. Rollback removes every package NOT in this
	// backup, so an empty backup recorded as success would later mean
	// "remove every package on the target".
	if exit != 0 {
		return BackupData{}, fmt.Errorf("listing %s packages for backup failed (exit %d)",
			adapter.PackageManager(), exit)
	}

	backup := PackagesBackup{
		Distro:   adapter.PackageManager(),
		Packages: parsePackageList(stdout, adapter.PackageManager()),
	}
	if len(backup.Packages) == 0 {
		return BackupData{}, fmt.Errorf("listing %s packages for backup returned no packages — "+
			"refusing to record an empty baseline that rollback would treat as \"remove everything\"",
			adapter.PackageManager())
	}

	raw, _ := json.Marshal(backup)
	return BackupData{Type: "packages", Data: raw}, nil
}

// Apply installs packages on the target, skipping already-installed ones.
func (a *PackagesApplier) Apply(ctx context.Context, ssh SSHExecuter, data CategoryData, onProgress StepCallback) error {
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		return err
	}

	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return err
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return err
	}
	targetPM := adapter.PackageManager()

	// Get currently installed packages on target
	stdout, _, _, _ := ssh.ExecContext(ctx, adapter.ListPackages())
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
		packagesToInstall = append(packagesToInstall, strings.Fields(mapped)...)
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
		// Cap-free (bounded by ctx): a batch install downloads and configures
		// dozens of packages, which easily outlasts the pooled connection's
		// Command timeout — that cap is whatever profile the FIRST module to
		// dial this server froze in (15s discovery / 30s default), and a
		// mid-flight kill leaves dpkg/rpm in a broken half-configured state.
		_, stderrRaw, exitCode, err := execLongOrContext(ctx, ssh, cmd)
		stderr := shared.SanitizeString(stderrRaw)
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
func (a *PackagesApplier) Rollback(ctx context.Context, ssh SSHExecuter, backup BackupData) error {
	var pb PackagesBackup
	if err := json.Unmarshal(backup.Data, &pb); err != nil {
		return err
	}
	// Rollback removes the complement of the backup. A backup with zero
	// packages cannot mean "the target had nothing installed" on a live server
	// — it means the baseline listing failed (records from before Backup
	// guarded its exit code). Its complement is every package on the target.
	if len(pb.Packages) == 0 {
		return fmt.Errorf("packages rollback: refusing to remove packages — the pre-migration " +
			"backup recorded zero packages, so removing its complement would strip the entire target")
	}

	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return err
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return err
	}

	stdout, _, _, _ := ssh.ExecContext(ctx, adapter.ListPackages())
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
	// Cap-free for the same reason as Apply's install: removing a large set
	// outlasts the pooled Command timeout, and killing dpkg/rpm mid-flight
	// corrupts the package database.
	_, _, _, err = execLongOrContext(ctx, ssh, cmd)
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
