package migration

import (
	"context"
	"fmt"
	"strings"

	"meshium/internal/shared"
)

// DistroInfo holds detected distribution information.
type DistroInfo struct {
	Name           string `json:"name"`           // "debian", "ubuntu", "rhel", etc.
	Family         string `json:"family"`         // "debian", "rhel", "arch", "alpine", "suse"
	Version        string `json:"version"`
	PackageManager string `json:"packageManager"` // "apt", "dnf", "yum", "pacman", "apk", "zypper"
}

// DistroAdapter abstracts package and service management across distros.
type DistroAdapter interface {
	Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error)
	PackageManager() string
	ListPackages() string
	InstallPackages(pkgs []string) string
	RemovePackages(pkgs []string) string
	EnableService(name string) string
	StartService(name string) string
}

// DetectDistro reads /etc/os-release and returns DistroInfo.
func DetectDistro(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	stdout, _, _, err := ssh.ExecContext(ctx, "cat /etc/os-release")
	if err != nil {
		return DistroInfo{}, fmt.Errorf("failed to read /etc/os-release: %w", err)
	}

	info := DistroInfo{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			info.Name = strings.Trim(line[3:], `"`)
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			info.Version = strings.Trim(line[11:], `"`)
		}
	}

	if info.Name == "" {
		return DistroInfo{}, fmt.Errorf("could not detect distro from /etc/os-release")
	}

	info.Family = distroFamily(info.Name)
	info.PackageManager = distroPackageManager(info.Name)

	return info, nil
}

func distroFamily(id string) string {
	switch id {
	case "debian", "ubuntu", "linuxmint", "raspbian":
		return "debian"
	case "rhel", "centos", "rocky", "almalinux", "fedora", "ol":
		return "rhel"
	case "arch", "manjaro", "garuda":
		return "arch"
	case "alpine":
		return "alpine"
	case "opensuse-leap", "opensuse-tumbleweed", "suse", "sles":
		return "suse"
	default:
		return "unknown"
	}
}

func distroPackageManager(id string) string {
	switch id {
	case "debian", "ubuntu", "linuxmint", "raspbian":
		return "apt"
	case "rhel", "centos", "rocky", "almalinux", "fedora", "ol":
		return "dnf"
	case "arch", "manjaro", "garuda":
		return "pacman"
	case "alpine":
		return "apk"
	case "opensuse-leap", "opensuse-tumbleweed", "suse", "sles":
		return "zypper"
	default:
		return "unknown"
	}
}

// GetAdapter returns the appropriate DistroAdapter for the detected distro.
// It fails closed: an unrecognized family returns an error rather than silently
// defaulting to apt. The old apt fallback would run `apt-get install` on hosts
// that have no apt (e.g. Amazon Linux, Gentoo), corrupting the target instead
// of reporting an unsupported distro. Callers must surface this error and not
// proceed with package/service migration.
func GetAdapter(info DistroInfo) (DistroAdapter, error) {
	switch info.Family {
	case "debian":
		return &aptAdapter{}, nil
	case "rhel":
		return &dnfAdapter{}, nil
	case "arch":
		return &pacmanAdapter{}, nil
	case "alpine":
		return &apkAdapter{}, nil
	case "suse":
		return &zypperAdapter{}, nil
	default:
		name := info.Name
		if name == "" {
			name = "unknown"
		}
		return nil, fmt.Errorf("unsupported distro %q (family %q): no package manager adapter — automatic package/service migration is not supported for this distribution", name, info.Family)
	}
}

// --- apt (Debian/Ubuntu) ---

type aptAdapter struct{}

func (a *aptAdapter) Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ctx, ssh)
}
func (a *aptAdapter) PackageManager() string { return "apt" }
func (a *aptAdapter) ListPackages() string {
	// Must keep the status column: `rc`/`ic` entries are packages that were
	// removed but left config behind, and they must not be reinstalled on the
	// target. parsePackageName filters on it. dpkg-query is used over `dpkg -l`
	// because the latter pads/truncates its Name column to the output width.
	return `dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n'`
}
func (a *aptAdapter) InstallPackages(pkgs []string) string {
	// Over SSH exec there is no TTY and no stdin. Without DEBIAN_FRONTEND any
	// package carrying a debconf prompt tries Dialog, then Readline, then
	// Teletype, and dpkg finally fails with "unable to re-open stdin" — which
	// aborts the WHOLE batch (observed live migrating Ubuntu 20.04 → 22.04).
	// </dev/null gives dpkg a readable stdin; --force-confold keeps the
	// target's existing config files rather than prompting about them, which
	// is also the safer default when migrating configs separately.
	return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get install -y -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef %s </dev/null",
		shared.ShellQuoteArgs(pkgs))
}
func (a *aptAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get remove -y %s </dev/null", shared.ShellQuoteArgs(pkgs))
}
func (a *aptAdapter) EnableService(name string) string {
	return fmt.Sprintf("systemctl enable %s", shared.ShellQuote(name))
}
func (a *aptAdapter) StartService(name string) string {
	return fmt.Sprintf("systemctl start %s", shared.ShellQuote(name))
}

// --- dnf/yum (RHEL/CentOS) ---

type dnfAdapter struct{}

func (a *dnfAdapter) Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ctx, ssh)
}
func (a *dnfAdapter) PackageManager() string { return "dnf" }
func (a *dnfAdapter) ListPackages() string {
	return "rpm -qa --qf '%{NAME}\\n'"
}
func (a *dnfAdapter) InstallPackages(pkgs []string) string {
	return fmt.Sprintf("dnf install -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *dnfAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("dnf remove -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *dnfAdapter) EnableService(name string) string {
	return fmt.Sprintf("systemctl enable %s", shared.ShellQuote(name))
}
func (a *dnfAdapter) StartService(name string) string {
	return fmt.Sprintf("systemctl start %s", shared.ShellQuote(name))
}

// --- pacman (Arch) ---

type pacmanAdapter struct{}

func (a *pacmanAdapter) Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ctx, ssh)
}
func (a *pacmanAdapter) PackageManager() string { return "pacman" }
func (a *pacmanAdapter) ListPackages() string {
	// pacman has no --qf (that is rpm syntax); -Qq prints bare names.
	return "pacman -Qq"
}
func (a *pacmanAdapter) InstallPackages(pkgs []string) string {
	return fmt.Sprintf("pacman -S --noconfirm %s", shared.ShellQuoteArgs(pkgs))
}
func (a *pacmanAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("pacman -Rns --noconfirm %s", shared.ShellQuoteArgs(pkgs))
}
func (a *pacmanAdapter) EnableService(name string) string {
	return fmt.Sprintf("systemctl enable %s", shared.ShellQuote(name))
}
func (a *pacmanAdapter) StartService(name string) string {
	return fmt.Sprintf("systemctl start %s", shared.ShellQuote(name))
}

// --- apk (Alpine) ---

type apkAdapter struct{}

func (a *apkAdapter) Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ctx, ssh)
}
func (a *apkAdapter) PackageManager() string { return "apk" }
func (a *apkAdapter) ListPackages() string {
	// `apk info` prints bare names; `apk info -v` appends "-<version>-r<rel>",
	// which no amount of suffix-trimming can strip unambiguously from names
	// that themselves contain dashes.
	return "apk info"
}
func (a *apkAdapter) InstallPackages(pkgs []string) string {
	return fmt.Sprintf("apk add %s", shared.ShellQuoteArgs(pkgs))
}
func (a *apkAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("apk del %s", shared.ShellQuoteArgs(pkgs))
}
func (a *apkAdapter) EnableService(name string) string {
	return fmt.Sprintf("rc-update add %s", shared.ShellQuote(name))
}
func (a *apkAdapter) StartService(name string) string {
	return fmt.Sprintf("rc-service %s start", shared.ShellQuote(name))
}

// --- zypper (SUSE) ---

type zypperAdapter struct{}

func (a *zypperAdapter) Detect(ctx context.Context, ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ctx, ssh)
}
func (a *zypperAdapter) PackageManager() string { return "zypper" }
func (a *zypperAdapter) ListPackages() string {
	// SUSE is rpm-based. `zypper se` needs repo metadata and prefixes its table
	// with a variable number of "Loading repository data..." lines, so the
	// fixed `NR>2` skip silently dropped or kept the wrong rows.
	return "rpm -qa --qf '%{NAME}\\n'"
}
func (a *zypperAdapter) InstallPackages(pkgs []string) string {
	return fmt.Sprintf("zypper install -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *zypperAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("zypper remove -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *zypperAdapter) EnableService(name string) string {
	return fmt.Sprintf("systemctl enable %s", shared.ShellQuote(name))
}
func (a *zypperAdapter) StartService(name string) string {
	return fmt.Sprintf("systemctl start %s", shared.ShellQuote(name))
}

// --- Package name mapping ---

// packageMap maps package names across distro families.
// Key format: "sourceFamily->targetFamily" -> map of source package -> target package.
var packageMap = map[string]map[string]string{
	"debian->rhel": {
		"python3-dev":             "python3-devel",
		"python3-pip":             "python3-pip",
		"libssl-dev":              "openssl-devel",
		"libcurl4-openssl-dev":    "libcurl-devel",
		"build-essential":         "gcc make",
		"libffi-dev":              "libffi-devel",
		"libxml2-dev":             "libxml2-devel",
		"libxslt-dev":             "libxslt-devel",
		"default-libmysqlclient-dev": "mysql-devel",
		"libpq-dev":               "postgresql-devel",
	},
	"rhel->debian": {
		"python3-devel":   "python3-dev",
		"openssl-devel":    "libssl-dev",
		"libcurl-devel":   "libcurl4-openssl-dev",
		"libffi-devel":    "libffi-dev",
		"libxml2-devel":   "libxml2-dev",
		"libxslt-devel":   "libxslt-dev",
		"mysql-devel":     "default-libmysqlclient-dev",
		"postgresql-devel": "libpq-dev",
	},
}

// MapPackageName attempts to map a package name from source distro to target distro.
// Returns the mapped name, or the original if no mapping exists.
func MapPackageName(source, target DistroInfo, pkg string) string {
	if source.Family == target.Family {
		return pkg
	}
	key := source.Family + "->" + target.Family
	if mappings, ok := packageMap[key]; ok {
		if mapped, ok := mappings[pkg]; ok {
			return mapped
		}
	}
	return pkg
}
