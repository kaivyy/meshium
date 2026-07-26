package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// `apt-get install a b c` is all-or-nothing: if ONE name is not in the
// target's repositories, apt exits 100 and installs NOTHING.
//
// Found by the first live full migration (Ubuntu 20.04 → 22.04): three
// packages from the source's own vendor repos — aliyun-assist,
// cloudflare-warp, crda — do not exist on the target, so all 465 installable
// packages were blocked and the whole migration rolled back. A cross-version
// or cross-vendor migration will essentially always contain a few such names,
// which made the packages category unusable in practice.
//
// The fix is to resolve availability on the TARGET first, install what can be
// installed, and report the rest explicitly as manual follow-up — never to
// pretend the unavailable ones succeeded.

func TestPartitionByAvailabilitySplitsInstallableFromMissing(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	// apt-cache policy prints a stanza per known package; unknown names yield
	// "N: Unable to locate package …" on stderr and nothing on stdout.
	ssh.addOutput("apt-cache policy", "nginx:\n  Candidate: 1.18.0\ncurl:\n  Candidate: 7.81.0\ncrda:\n  Candidate: (none)\n")

	want := []string{"nginx", "curl", "crda", "aliyun-assist"}
	available, missing := partitionByAvailability(context.Background(), ssh, want)

	if len(available) != 2 {
		t.Errorf("available = %v, want [nginx curl]", available)
	}
	for _, m := range []string{"crda", "aliyun-assist"} {
		found := false
		for _, x := range missing {
			if x == m {
				found = true
			}
		}
		if !found {
			t.Errorf("%s should be reported missing; missing=%v", m, missing)
		}
	}
}

// A package present in the index but with no installation candidate (removed
// in the target release, like crda on 22.04) counts as missing: apt cannot
// install it.
func TestPartitionTreatsNoCandidateAsMissing(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("apt-cache policy", "crda:\n  Installed: (none)\n  Candidate: (none)\n")

	available, missing := partitionByAvailability(context.Background(), ssh, []string{"crda"})
	if len(available) != 0 {
		t.Errorf("a package with no candidate must not be treated as installable: %v", available)
	}
	if len(missing) != 1 {
		t.Errorf("missing = %v, want [crda]", missing)
	}
}

// If the availability probe itself cannot run, do NOT silently drop every
// package — fall back to attempting the install so behaviour degrades to the
// old path rather than to a no-op that reports success.
func TestPartitionFallsBackWhenProbeUnavailable(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("apt-cache policy", "")
	ssh.execExit["apt-cache policy"] = 127

	available, missing := partitionByAvailability(context.Background(), ssh, []string{"nginx", "curl"})
	if len(available) != 2 {
		t.Errorf("probe failure should fall back to attempting all: %v", available)
	}
	if len(missing) != 0 {
		t.Errorf("probe failure must not invent missing packages: %v", missing)
	}
}

// End to end: the applier installs what it can and reports what it cannot,
// instead of failing the whole category.
func TestPackagesApplyInstallsAvailableAndReportsMissing(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  bash\n")
	ssh.addOutput("apt-cache policy", "nginx:\n  Candidate: 1.18.0\n")

	pd := PackagesData{Distro: "apt", Packages: []string{"nginx", "aliyun-assist"}, Count: 2}
	raw, _ := json.Marshal(pd)

	var msgs []WSMessage
	err := (&PackagesApplier{}).Apply(context.Background(), ssh,
		CategoryData{Type: "packages", Data: raw},
		func(m WSMessage) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("Apply failed instead of installing what it could: %v", err)
	}

	var install string
	for _, c := range ssh.commands {
		if strings.Contains(c, "apt-get install") {
			install = c
		}
	}
	if !strings.Contains(install, "nginx") {
		t.Errorf("available package was not installed: %q", install)
	}
	if strings.Contains(install, "aliyun-assist") {
		t.Errorf("unavailable package was still passed to apt, which fails the whole batch: %q", install)
	}

	var reported bool
	for _, m := range msgs {
		if m.Status == "warning" && strings.Contains(m.Value, "aliyun-assist") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("unavailable package was dropped silently; messages=%v", msgs)
	}
}
