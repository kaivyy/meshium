package migration

import (
	"encoding/json"
	"testing"
)

func TestServicesCollector(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null || rc-update show 2>/dev/null"] =
		"nginx.service      enabled\nssh.service        enabled\n"

	collector := &ServicesCollector{}
	data, err := collector.Collect(ssh)
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
	ssh.execOutput["systemctl list-unit-files --type=service --state=enabled --no-legend 2>/dev/null"] =
		"nginx.service      enabled\n"

	applier := &ServicesApplier{}
	backup, err := applier.Backup(ssh)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	var sb ServicesBackup
	json.Unmarshal(backup.Data, &sb)
	if len(sb.Services) != 1 || sb.Services[0] != "nginx" {
		t.Errorf("expected ['nginx'], got %v", sb.Services)
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
	err := applier.Apply(ssh, CategoryData{Type: "services", Data: raw}, func(msg WSMessage) {
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
