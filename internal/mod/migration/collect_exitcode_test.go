package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// PackagesApplier.Backup ignored the list command's exit code, so a failed
// listing (missing tool, permission denied) produced an empty Packages slice
// recorded as a successful backup — and Rollback removes every package on the
// target that is not in the backup. An empty backup therefore meant "remove
// every package on the target", the same blast-radius shape as the database
// rollback bug.
func TestPackagesBackupFailsOnListError(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "")
	ssh.execExit["dpkg-query"] = 127

	if _, err := (&PackagesApplier{}).Backup(context.Background(), ssh); err == nil {
		t.Fatal("Backup succeeded with an empty package list from a failed command")
	}
}

func TestPackagesBackupFailsOnEmptyList(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "\n")

	if _, err := (&PackagesApplier{}).Backup(context.Background(), ssh); err == nil {
		t.Fatal("Backup recorded an empty package list on a live server as success")
	}
}

// Defense in depth for backups recorded before the fix: a backup holding zero
// packages cannot distinguish "target had nothing installed" (impossible on a
// live server) from "the listing failed" — removing its complement would strip
// the entire target.
func TestPackagesRollbackRefusesEmptyBackup(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "ii  nginx\nii  curl\nii  openssh-server\n")

	raw, _ := json.Marshal(PackagesBackup{Distro: "apt", Packages: nil})
	err := (&PackagesApplier{}).Rollback(context.Background(), ssh, BackupData{Type: "packages", Data: raw})
	if err == nil {
		t.Fatal("Rollback proceeded from an empty backup — it would remove every package on the target")
	}
	for _, cmd := range ssh.commands {
		if strings.Contains(cmd, "remove") {
			t.Fatalf("Rollback issued a remove from an empty backup: %q", cmd)
		}
	}
}

// ServicesCollector stored zero services as a successful collect when both
// systemctl and rc-update failed (the `||` chain still exits via the last
// command). An empty service list on a live server is a failed probe, not an
// answer.
func TestServicesCollectRejectsEmptyResult(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("systemctl list-unit-files", "")
	ssh.execExit["systemctl list-unit-files"] = 127

	if _, err := (&ServicesCollector{}).Collect(context.Background(), ssh); err == nil {
		t.Fatal("Collect recorded zero services as success")
	}
}

func TestServicesCollectStillParsesSystemd(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("systemctl list-unit-files", "nginx.service enabled\nsshd.service enabled\n")

	data, err := (&ServicesCollector{}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var sd ServicesData
	if err := json.Unmarshal(data.Data, &sd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sd.Count != 2 || sd.Services[0] != "nginx" {
		t.Errorf("parsed %d services %v, want [nginx sshd]", sd.Count, sd.Services)
	}
}
