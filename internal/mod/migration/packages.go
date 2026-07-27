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

	// Drop names the target cannot install BEFORE handing the list to apt:
	// `apt-get install` is all-or-nothing, so one unavailable package (a
	// vendor-repo agent like aliyun-assist, or one dropped in the target
	// release like crda) blocks every other package in the batch. Report them
	// rather than silently omitting them — they are real migration gaps the
	// operator has to close by hand.
	available, unavailable := partitionByAvailability(ctx, ssh, packagesToInstall)
	packagesToInstall = available
	if len(unavailable) > 0 && onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "warning",
			Value: fmt.Sprintf("%d package(s) are not available in the target's repositories and were NOT installed: %s — add the matching repository or install them manually",
				len(unavailable), strings.Join(unavailable, ", ")),
		})
	}

	if onProgress != nil {
		onProgress(WSMessage{
			Step:   "packages:apply",
			Status: "progress",
			Value:  fmt.Sprintf("Installing %d packages (%d already present, %d unavailable on target)", len(packagesToInstall), skipped, len(unavailable)),
		})
	}

	// Install in batches, falling back to one-at-a-time when a batch aborts.
	//
	// `apt-get install a b c` is one transaction: a single package whose
	// postinst fails takes the entire batch with it. On the live 20.04 → 22.04
	// run that cost the other 899 packages — after four retries the whole
	// migration rolled back because of one broken package. Retrying the
	// members individually confines the damage to the package that is actually
	// broken.
	installedCount, failedPkgs := installPackages(ctx, ssh, adapter, packagesToInstall, onProgress)

	for _, f := range failedPkgs {
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "packages:apply",
				Status: "warning",
				Value:  fmt.Sprintf("%s could not be installed on the target and was skipped: %s", f.name, f.reason),
			})
		}
	}

	// An individual package that will not install is NOT a category failure.
	// It is recorded against its own item (reconcileApplied re-reads the target
	// afterwards) and surfaced in the migration's final status as a manual gap.
	//
	// Returning an error here instead fails the stage, retries it, and
	// eventually rolls back a migration whose other 839 packages installed
	// perfectly — observed live once the only packages left were the handful
	// that are genuinely broken on the target (nginx, golang, ntfs-3g …).
	//
	// Systemic failures — unreachable target, undetectable distro, unusable
	// package manager — already return errors above, before anything is
	// attempted, so they are not swallowed here.
	_ = installedCount

	if onProgress != nil {
		summary := fmt.Sprintf("%d packages installed", installedCount)
		if len(failedPkgs) > 0 {
			summary += fmt.Sprintf("; %d failed to install", len(failedPkgs))
		}
		if len(unavailable) > 0 {
			summary += fmt.Sprintf("; %d unavailable on the target and left for manual follow-up", len(unavailable))
		}
		onProgress(WSMessage{Step: "packages:apply", Status: "success", Value: summary})
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

// partitionByAvailability splits the wanted packages into those the TARGET can
// actually install and those it cannot.
//
// `apt-get install a b c` is all-or-nothing: one unknown name makes apt exit
// 100 and install nothing. A cross-version or cross-vendor migration almost
// always carries a few names that only existed in the source's own repos
// (aliyun-assist, cloudflare-warp) or were dropped in the target release
// (crda), so without this one such package blocks every other one.
//
// Fails OPEN, deliberately: if the availability probe itself cannot run, every
// package is reported available so the install is still attempted. Degrading
// to the old all-or-nothing behaviour is acceptable; silently installing
// nothing and calling it success is not.
func partitionByAvailability(ctx context.Context, ssh SSHExecuter, want []string) (available, missing []string) {
	if len(want) == 0 {
		return nil, nil
	}
	info, err := DetectDistro(ctx, ssh)
	if err != nil {
		return want, nil
	}
	adapter, err := GetAdapter(info)
	if err != nil {
		return want, nil
	}
	// Only apt exposes a cheap batch availability query today. Other managers
	// keep the previous behaviour rather than gain an unverified code path.
	if adapter.PackageManager() != "apt" {
		return want, nil
	}

	installable := map[string]bool{}
	const batch = 300
	probed := false
	for start := 0; start < len(want); start += batch {
		end := start + batch
		if end > len(want) {
			end = len(want)
		}
		quoted := make([]string, 0, end-start)
		for _, p := range want[start:end] {
			quoted = append(quoted, shared.ShellQuote(p))
		}
		out, _, exit, err := ssh.ExecContext(ctx, "apt-cache policy "+strings.Join(quoted, " ")+" 2>/dev/null")
		if err != nil || exit != 0 {
			continue
		}
		probed = true
		// Output is a stanza per known package:
		//   nginx:
		//     Installed: (none)
		//     Candidate: 1.18.0
		// A name absent from the output, or present with "Candidate: (none)",
		// cannot be installed.
		var current string
		for _, line := range strings.Split(out, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(line, " ") {
				current = strings.TrimSuffix(trimmed, ":")
				continue
			}
			if current != "" && strings.HasPrefix(trimmed, "Candidate:") {
				cand := strings.TrimSpace(strings.TrimPrefix(trimmed, "Candidate:"))
				if cand != "" && cand != "(none)" {
					installable[current] = true
				}
				current = ""
			}
		}
	}
	if !probed {
		// The probe never ran anywhere — fall back to attempting all.
		return want, nil
	}

	for _, p := range want {
		// apt reports multi-arch names as "pkg:arch"; the policy stanza uses
		// the bare name.
		base := p
		if i := strings.IndexByte(base, ':'); i > 0 {
			base = base[:i]
		}
		if installable[base] {
			available = append(available, p)
		} else {
			missing = append(missing, p)
		}
	}
	return available, missing
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

// pkgFailure records one package that could not be installed, with the reason
// the target gave.
type pkgFailure struct {
	name   string
	reason string
}

// installPackages installs the given packages, tolerating individual failures.
//
// It first tries whole batches, which is fast and lets apt resolve
// dependencies together. When a batch aborts — one member's postinst failing
// takes the whole transaction down — its members are retried one at a time so
// only the genuinely broken package is lost. Returns how many were installed
// and which ones failed.
func installPackages(ctx context.Context, ssh SSHExecuter, adapter DistroAdapter, pkgs []string, onProgress StepCallback) (installed int, failures []pkgFailure) {
	const batchSize = 50
	for i := 0; i < len(pkgs); i += batchSize {
		end := i + batchSize
		if end > len(pkgs) {
			end = len(pkgs)
		}
		batch := pkgs[i:end]

		// Cap-free (bounded by ctx): a batch install downloads and configures
		// dozens of packages, well past the pooled connection's command cap,
		// and killing dpkg mid-flight leaves the package database broken.
		_, _, exit, err := execLongOrContext(ctx, ssh, adapter.InstallPackages(batch))
		if err == nil && exit == 0 {
			installed += len(batch)
			if onProgress != nil {
				onProgress(WSMessage{
					Step:   "packages:apply",
					Status: "progress",
					Value:  fmt.Sprintf("Installed %d/%d", installed, len(pkgs)),
				})
			}
			continue
		}

		// The batch aborted. Retry each member alone so one bad package does
		// not cost the rest of the batch.
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "packages:apply",
				Status: "warning",
				Value:  fmt.Sprintf("A batch of %d packages aborted; retrying them individually to isolate the failure", len(batch)),
			})
		}
		for _, p := range batch {
			if ctx.Err() != nil {
				return installed, failures
			}
			_, stderrRaw, exit, err := execLongOrContext(ctx, ssh, adapter.InstallPackages([]string{p}))
			if err == nil && exit == 0 {
				installed++
				continue
			}
			reason := shared.SanitizeString(strings.TrimSpace(firstLine(stderrRaw)))
			if reason == "" {
				reason = fmt.Sprintf("exit %d", exit)
			}
			failures = append(failures, pkgFailure{name: p, reason: reason})
		}
		if onProgress != nil {
			onProgress(WSMessage{
				Step:   "packages:apply",
				Status: "progress",
				Value:  fmt.Sprintf("Installed %d/%d", installed, len(pkgs)),
			})
		}
	}
	return installed, failures
}
