package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The collector runs ListPackages() through a real shell and feeds the result
// to parsePackageList. These two halves are written in different files and
// drifted apart: the commands were changed to pre-extract the package name
// (awk/--qf) while the parser kept expecting each tool's raw table output.
// mockSSH.Exec prefix-matches, so a test keyed on "dpkg -l" also answers the
// real "dpkg -l | awk ...", hiding the drift behind green tests.
//
// These cases pin the contract end to end: the string is what the command
// actually prints on a live host, and the parser must recover the names.
func TestListPackagesOutputMatchesParser(t *testing.T) {
	cases := []struct {
		name     string
		pm       string
		adapter  DistroAdapter
		stdout   string // literally what ListPackages() prints on a real host
		expected []string
	}{
		{
			name:    "apt keeps installed, drops config-only",
			pm:      "apt",
			adapter: &aptAdapter{},
			// dpkg-query -W -f='${db:Status-Abbrev} ${binary:Package}\n'
			stdout:   "ii  adduser\nii  nginx\nic  ifupdown\nrc  oldpkg\nhi  held-pkg\n",
			expected: []string{"adduser", "nginx", "held-pkg"},
		},
		{
			name:    "dnf keeps dashes in name",
			pm:      "dnf",
			adapter: &dnfAdapter{},
			// rpm -qa --qf '%{NAME}\n'
			stdout:   "bash\npython3-libs\nkernel-core\n",
			expected: []string{"bash", "python3-libs", "kernel-core"},
		},
		{
			name:    "pacman bare names",
			pm:      "pacman",
			adapter: &pacmanAdapter{},
			// pacman -Qq
			stdout:   "bash\nlinux-firmware\n",
			expected: []string{"bash", "linux-firmware"},
		},
		{
			name:    "apk names without version suffix",
			pm:      "apk",
			adapter: &apkAdapter{},
			// apk info
			stdout:   "busybox\nmusl-utils\n",
			expected: []string{"busybox", "musl-utils"},
		},
		{
			name:    "zypper bare names",
			pm:      "zypper",
			adapter: &zypperAdapter{},
			// rpm -qa --qf '%{NAME}\n'
			stdout:   "bash\nlibz1\nsystemd-sysvinit\n",
			expected: []string{"bash", "libz1", "systemd-sysvinit"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.adapter.PackageManager(); got != tc.pm {
				t.Fatalf("PackageManager() = %q, want %q", got, tc.pm)
			}
			got := parsePackageList(tc.stdout, tc.pm)
			if len(got) != len(tc.expected) {
				t.Fatalf("parsePackageList(%q) = %v (%d), want %v (%d)",
					tc.adapter.ListPackages(), got, len(got), tc.expected, len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("package[%d] = %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// The apt list command must carry the install-status column, because that is
// the only thing distinguishing an installed package from one that was removed
// but left its config behind (rc/ic). Piping through `awk '{print $2}'` threw
// that column away and left the parser nothing to match on.
func TestAptListPackagesCarriesStatusColumn(t *testing.T) {
	cmd := (&aptAdapter{}).ListPackages()
	if strings.Contains(cmd, "awk") {
		t.Errorf("apt ListPackages pre-extracts the name with awk, which strips the\n"+
			"install-status column the parser filters on: %s", cmd)
	}
	if !strings.Contains(cmd, "Status-Abbrev") {
		t.Errorf("apt ListPackages must emit the status column, got: %s", cmd)
	}
}

// A list command that fails (missing tool, permission denied, wrong distro)
// exits non-zero with empty stdout. Collect ignored the exit code, so the step
// was stored as a successful collect of zero packages and the migration
// silently carried an empty source snapshot forward.
func TestPackagesCollectFailsLoudlyOnCommandError(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg-query"] = ""
	ssh.execExit["dpkg-query"] = 127

	_, err := (&PackagesCollector{}).Collect(context.Background(), ssh)
	if err == nil {
		t.Fatal("Collect returned nil error when the list command exited 127")
	}
}

// Same failure with a zero exit but no output (e.g. the tool printed only to
// stderr): an empty package set on a live server is not a real answer.
func TestPackagesCollectRejectsEmptyResult(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg-query"] = "\n"

	_, err := (&PackagesCollector{}).Collect(context.Background(), ssh)
	if err == nil {
		t.Fatal("Collect returned nil error for an empty package list")
	}
}

// Regression for the live failure on migration #21: apt source, plan reported
// success, stored data was {"distro":"apt","packages":null,"count":0}.
func TestPackagesCollectAptEndToEnd(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg-query"] = "ii  nginx\nii  curl\nrc  removed-pkg\n"

	data, err := (&PackagesCollector{}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	var pd PackagesData
	if err := json.Unmarshal(data.Data, &pd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pd.Count != 2 {
		t.Fatalf("expected 2 packages, got %d (%v)", pd.Count, pd.Packages)
	}
	if pd.Packages[0] != "nginx" || pd.Packages[1] != "curl" {
		t.Errorf("expected [nginx curl], got %v", pd.Packages)
	}
}
