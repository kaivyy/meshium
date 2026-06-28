package migration

import (
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
	Detect(ssh SSHExecuter) (DistroInfo, error)
	PackageManager() string
	ListPackages() string
	InstallPackages(pkgs []string) string
	RemovePackages(pkgs []string) string
	EnableService(name string) string
	StartService(name string) string
}

// DetectDistro reads /etc/os-release and returns DistroInfo.
func DetectDistro(ssh SSHExecuter) (DistroInfo, error) {
	stdout, _, _, err := ssh.Exec("cat /etc/os-release")
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
func GetAdapter(info DistroInfo) DistroAdapter {
	switch info.Family {
	case "debian":
		return &aptAdapter{}
	case "rhel":
		return &dnfAdapter{}
	case "arch":
		return &pacmanAdapter{}
	case "alpine":
		return &apkAdapter{}
	case "suse":
		return &zypperAdapter{}
	default:
		return &aptAdapter{} // fallback
	}
}

// --- apt (Debian/Ubuntu) ---

type aptAdapter struct{}

func (a *aptAdapter) Detect(ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ssh)
}
func (a *aptAdapter) PackageManager() string { return "apt" }
func (a *aptAdapter) ListPackages() string {
	return "dpkg -l | awk 'NR>5 {print $2}'"
}
func (a *aptAdapter) InstallPackages(pkgs []string) string {
	return fmt.Sprintf("apt-get install -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *aptAdapter) RemovePackages(pkgs []string) string {
	return fmt.Sprintf("apt-get remove -y %s", shared.ShellQuoteArgs(pkgs))
}
func (a *aptAdapter) EnableService(name string) string {
	return fmt.Sprintf("systemctl enable %s", shared.ShellQuote(name))
}
func (a *aptAdapter) StartService(name string) string {
	return fmt.Sprintf("systemctl start %s", shared.ShellQuote(name))
}

// --- dnf/yum (RHEL/CentOS) ---

type dnfAdapter struct{}

func (a *dnfAdapter) Detect(ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ssh)
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

func (a *pacmanAdapter) Detect(ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ssh)
}
func (a *pacmanAdapter) PackageManager() string { return "pacman" }
func (a *pacmanAdapter) ListPackages() string {
	return "pacman -Q --qf '%n\\n'"
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

func (a *apkAdapter) Detect(ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ssh)
}
func (a *apkAdapter) PackageManager() string { return "apk" }
func (a *apkAdapter) ListPackages() string {
	return "apk info -v | awk '{print $1}'"
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

func (a *zypperAdapter) Detect(ssh SSHExecuter) (DistroInfo, error) {
	return DetectDistro(ssh)
}
func (a *zypperAdapter) PackageManager() string { return "zypper" }
func (a *zypperAdapter) ListPackages() string {
	return "zypper se --installed-only | awk 'NR>2 {print $3}'"
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
