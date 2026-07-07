package migration

import (
	"context"
	"strings"
	"testing"
)

func TestDetectDistroDebian(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
ID=debian
VERSION_CODENAME=bookworm`
	info, err := DetectDistro(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DetectDistro failed: %v", err)
	}
	if info.Name != "debian" {
		t.Errorf("expected name 'debian', got %q", info.Name)
	}
	if info.Family != "debian" {
		t.Errorf("expected family 'debian', got %q", info.Family)
	}
	if info.PackageManager != "apt" {
		t.Errorf("expected package manager 'apt', got %q", info.PackageManager)
	}
	if info.Version != "12" {
		t.Errorf("expected version '12', got %q", info.Version)
	}
}

func TestDetectDistroUbuntu(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `PRETTY_NAME="Ubuntu 22.04.4 LTS"
NAME="Ubuntu"
VERSION_ID="22.04"
ID=ubuntu`
	info, _ := DetectDistro(context.Background(), ssh)
	if info.Name != "ubuntu" || info.Family != "debian" || info.PackageManager != "apt" {
		t.Errorf("ubuntu detection wrong: %+v", info)
	}
}

func TestDetectDistroAlpine(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.19.1`
	info, _ := DetectDistro(context.Background(), ssh)
	if info.Name != "alpine" || info.Family != "alpine" || info.PackageManager != "apk" {
		t.Errorf("alpine detection wrong: %+v", info)
	}
}

func TestDetectDistroArch(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `NAME="Arch Linux"
ID=arch
PRETTY_NAME="Arch Linux"`
	info, _ := DetectDistro(context.Background(), ssh)
	if info.Name != "arch" || info.Family != "arch" || info.PackageManager != "pacman" {
		t.Errorf("arch detection wrong: %+v", info)
	}
}

func TestDetectDistroRHEL(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `NAME="Red Hat Enterprise Linux"
ID="rhel"
VERSION_ID="9.3"`
	info, _ := DetectDistro(context.Background(), ssh)
	if info.Name != "rhel" || info.Family != "rhel" || info.PackageManager != "dnf" {
		t.Errorf("rhel detection wrong: %+v", info)
	}
}

func TestGetAdapter(t *testing.T) {
	tests := []struct {
		family string
		want   string
	}{
		{"debian", "apt"},
		{"rhel", "dnf"},
		{"arch", "pacman"},
		{"alpine", "apk"},
		{"suse", "zypper"},
	}
	for _, tt := range tests {
		adapter, err := GetAdapter(DistroInfo{Family: tt.family})
		if err != nil {
			t.Errorf("family %q: unexpected error: %v", tt.family, err)
			continue
		}
		if adapter.PackageManager() != tt.want {
			t.Errorf("family %q: expected %q, got %q", tt.family, tt.want, adapter.PackageManager())
		}
	}
}

// TestGetAdapterUnknownFailsClosed proves GetAdapter refuses an unrecognized
// distro family instead of silently returning the apt adapter. The old fallback
// ran `apt-get install` on hosts with no apt (Amazon Linux, Gentoo, etc.),
// which would fail or corrupt the target while reporting nothing wrong.
func TestGetAdapterUnknownFailsClosed(t *testing.T) {
	cases := []DistroInfo{
		{Name: "amzn", Family: "unknown"},   // Amazon Linux — not recognized by distroFamily
		{Name: "gentoo", Family: "unknown"}, // Gentoo — no adapter
		{Family: ""},                        // family never determined
	}
	for _, info := range cases {
		adapter, err := GetAdapter(info)
		if err == nil {
			t.Errorf("GetAdapter(%+v) returned no error; unknown distros must fail closed", info)
		}
		if adapter != nil {
			t.Errorf("GetAdapter(%+v) returned a non-nil adapter %T; must be nil on failure", info, adapter)
		}
	}
}

// TestDetectDistroAmazonLinux documents current behavior: Amazon Linux
// (ID=amzn) is not in the recognized set, so it resolves to family "unknown"
// and no package manager. Combined with TestGetAdapterUnknownFailsClosed, this
// means an Amazon Linux target is safely refused rather than mistakenly driven
// with apt. (Adding real amzn→dnf support is deliberately out of scope until a
// dnf-based migration path is verified end-to-end.)
func TestDetectDistroAmazonLinux(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = `NAME="Amazon Linux"
ID="amzn"
VERSION_ID="2023"`
	info, err := DetectDistro(context.Background(), ssh)
	if err != nil {
		t.Fatalf("DetectDistro failed: %v", err)
	}
	if info.Family != "unknown" {
		t.Errorf("expected family 'unknown' for amzn, got %q", info.Family)
	}
	if _, err := GetAdapter(info); err == nil {
		t.Error("expected GetAdapter to fail closed for Amazon Linux, got nil error")
	}
}

func TestAptAdapterCommands(t *testing.T) {
	a := &aptAdapter{}
	// Shell-quoted: 'nginx' 'redis'
	installCmd := a.InstallPackages([]string{"nginx", "redis"})
	if !strings.Contains(installCmd, "'nginx'") || !strings.Contains(installCmd, "'redis'") {
		t.Errorf("apt install command should contain shell-quoted package names, got: %s", installCmd)
	}
	if !strings.Contains(a.EnableService("nginx"), "systemctl enable 'nginx'") {
		t.Error("apt enable service command should shell-quote service name")
	}
}

func TestApkAdapterCommands(t *testing.T) {
	a := &apkAdapter{}
	if !strings.Contains(a.EnableService("sshd"), "rc-update add 'sshd'") {
		t.Error("apk enable service should use rc-update with shell-quoted name")
	}
	if !strings.Contains(a.StartService("sshd"), "rc-service 'sshd' start") {
		t.Error("apk start service should use rc-service with shell-quoted name")
	}
}

func TestMapPackageNameSameFamily(t *testing.T) {
	result := MapPackageName(
		DistroInfo{Family: "debian"},
		DistroInfo{Family: "debian"},
		"nginx",
	)
	if result != "nginx" {
		t.Errorf("same family should return same package: got %q", result)
	}
}

func TestMapPackageNameDebianToRHEL(t *testing.T) {
	result := MapPackageName(
		DistroInfo{Family: "debian"},
		DistroInfo{Family: "rhel"},
		"python3-dev",
	)
	if result != "python3-devel" {
		t.Errorf("expected python3-devel, got %q", result)
	}
}

func TestMapPackageNameUnmapped(t *testing.T) {
	result := MapPackageName(
		DistroInfo{Family: "debian"},
		DistroInfo{Family: "rhel"},
		"nginx",
	)
	if result != "nginx" {
		t.Errorf("unmapped package should return original: got %q", result)
	}
}
