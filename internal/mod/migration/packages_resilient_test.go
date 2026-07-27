package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// VPS-B20. `apt-get install a b c` aborts the whole transaction when any one
// package fails its postinst. On the live 20.04 → 22.04 run this cost the
// other 899 packages: after four retries the entire migration rolled back
// because of a single broken package.
//
// Apply now falls back to installing the members of a failed batch one at a
// time, so one bad package costs only itself.

func TestApplyRetriesBatchMembersIndividually(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  bash\n")
	ssh.addOutput("apt-cache policy", "nginx:\n  Candidate: 1.1\nredis:\n  Candidate: 2.2\nbroken:\n  Candidate: 3.3\n")
	// The whole-batch install fails; individual installs succeed except "broken".
	ssh.addOutput("apt-get install -y -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef 'nginx' 'redis' 'broken'", "batch aborted")
	ssh.execExit["apt-get install -y -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef 'nginx' 'redis' 'broken'"] = 100
	ssh.addOutput("--force-confdef 'broken'", "dpkg: error processing broken")
	ssh.execExit["--force-confdef 'broken'"] = 100

	pd := PackagesData{Distro: "apt", Packages: []string{"nginx", "redis", "broken"}, Count: 3}
	raw, _ := json.Marshal(pd)

	var msgs []WSMessage
	err := (&PackagesApplier{}).Apply(context.Background(), ssh,
		CategoryData{Type: "packages", Data: raw},
		func(m WSMessage) { msgs = append(msgs, m) })
	if err != nil {
		t.Fatalf("one broken package failed the whole apply: %v", err)
	}

	var sawNginx, sawRedis bool
	for _, c := range ssh.commands {
		if strings.Contains(c, "--force-confdef 'nginx'") {
			sawNginx = true
		}
		if strings.Contains(c, "--force-confdef 'redis'") {
			sawRedis = true
		}
	}
	if !sawNginx || !sawRedis {
		t.Errorf("healthy packages were not retried individually; commands=%v", ssh.commands)
	}

	var reported bool
	for _, m := range msgs {
		if strings.Contains(m.Value, "broken") && m.Status == "warning" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the failing package was not reported; msgs=%v", msgs)
	}
}

// An individual package that cannot install is recorded against its own item,
// never raised as a category failure.
//
// The first version of this test asserted the opposite ("if nothing installed,
// fail"). The live run showed why that is wrong: once the only packages left
// were the handful genuinely broken on the target, every retry installed zero,
// the stage failed, and a migration whose other 839 packages had installed
// perfectly was rolled back.
func TestApplyDoesNotFailCategoryForIndividualPackageFailures(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  bash\n")
	ssh.addOutput("apt-cache policy", "nginx:\n  Candidate: 1.1\n")
	ssh.addOutput("apt-get install", "everything failed")
	ssh.execExit["apt-get install"] = 100

	pd := PackagesData{Distro: "apt", Packages: []string{"nginx"}, Count: 1}
	raw, _ := json.Marshal(pd)

	if err := (&PackagesApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "packages", Data: raw}, nil); err != nil {
		t.Fatalf("an individual package failure failed the whole category: %v", err)
	}
}

// Systemic failures must still error, before anything is attempted.
func TestApplyFailsOnSystemicError(t *testing.T) {
	ssh := newMockSSH() // no os-release ⇒ distro undetectable
	pd := PackagesData{Distro: "apt", Packages: []string{"nginx"}, Count: 1}
	raw, _ := json.Marshal(pd)
	if err := (&PackagesApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "packages", Data: raw}, nil); err == nil {
		t.Fatal("apply succeeded against an undetectable target")
	}
}

// Nothing to do at all is success, not failure.
func TestApplySucceedsWhenEverythingAlreadyPresent(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\n")

	pd := PackagesData{Distro: "apt", Packages: []string{"nginx"}, Count: 1}
	raw, _ := json.Marshal(pd)

	if err := (&PackagesApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "packages", Data: raw}, nil); err != nil {
		t.Fatalf("no-op apply failed: %v", err)
	}
}
