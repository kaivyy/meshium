package migration

import (
	"context"
	"encoding/json"
	"testing"
)

func TestServicesCollector(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null || rc-update show 2>/dev/null"] =
		"nginx.service      enabled\nssh.service        enabled\n"

	collector := &ServicesCollector{}
	data, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var sd ServicesData
	json.Unmarshal(data.Data, &sd)
	if sd.Count != 2 {
		t.Errorf("expected 2 services, got %d", sd.Count)
	}
	if sd.Services[0] != "nginx" {
		t.Errorf("expected first service 'nginx', got '%s'", sd.Services[0])
	}
	if sd.Services[1] != "ssh" {
		t.Errorf("expected second service 'ssh', got '%s'", sd.Services[1])
	}
}

func TestServicesApplierBackup(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null"] =
		"nginx.service      enabled\n"

	applier := &ServicesApplier{}
	backup, err := applier.Backup(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	var sb ServicesBackup
	json.Unmarshal(backup.Data, &sb)
	if len(sb.Services) != 1 || sb.Services[0] != "nginx" {
		t.Errorf("expected ['nginx'], got %v", sb.Services)
	}
}

// TestServicesApplierBackupFailsClosedOnOpenRC proves that service backup
// refuses an Alpine/OpenRC target instead of recording an empty systemd backup.
// Backup is the mandatory gate before Apply, so a false-success here would let
// the migration enable services on the target with nothing to roll back to.
func TestServicesApplierBackupFailsClosedOnOpenRC(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.19.1"
	// systemctl would return empty on Alpine; even so, backup must not "succeed".
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null"] = ""

	applier := &ServicesApplier{}
	if _, err := applier.Backup(context.Background(), ssh); err == nil {
		t.Fatal("expected Backup to fail closed on an OpenRC (Alpine) target, got nil error")
	}
}

func TestServicesApplierApply(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"

	sd := ServicesData{
		Services: []string{"nginx", "redis"},
	}
	raw, _ := json.Marshal(sd)

	var progressMsgs []WSMessage
	applier := &ServicesApplier{}
	err := applier.Apply(context.Background(), ssh, CategoryData{Type: "services", Data: raw}, func(msg WSMessage) {
		progressMsgs = append(progressMsgs, msg)
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	last := progressMsgs[len(progressMsgs)-1]
	if last.Status != "success" {
		t.Errorf("expected last status 'success', got '%s'", last.Status)
	}
}

func TestServicesApplierRollbackQuotesServiceNames(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null"] =
		"nginx.service      enabled\n"

	applier := &ServicesApplier{}
	backup, _ := json.Marshal(ServicesBackup{Services: []string{}})
	if err := applier.Rollback(context.Background(), ssh, BackupData{Type: "services", Data: backup}); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if !containsCommand(ssh.commands, "systemctl disable --now 'nginx' 2>/dev/null") {
		t.Fatal("expected rollback to shell-quote the service name")
	}
}
