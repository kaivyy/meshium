package migration

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPackagesCollectorApt(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg -l"] = "ii  nginx    1.18.0-0ubuntu1   amd64   [installed]\nii  curl     7.81.0-1   amd64   [installed]\n"

	collector := &PackagesCollector{}
	data, err := collector.Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var pd PackagesData
	json.Unmarshal(data.Data, &pd)
	if pd.Distro != "apt" {
		t.Errorf("expected distro 'apt', got '%s'", pd.Distro)
	}
	if pd.Count != 2 {
		t.Errorf("expected 2 packages, got %d", pd.Count)
	}
	if pd.Packages[0] != "nginx" {
		t.Errorf("expected first package 'nginx', got '%s'", pd.Packages[0])
	}
}

func TestParsePackageNameApt(t *testing.T) {
	result := parsePackageName("ii  nginx    1.18.0-0ubuntu1   amd64", "apt")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}

func TestParsePackageNamePacman(t *testing.T) {
	result := parsePackageName("nginx 1.18.0", "pacman")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}

func TestParsePackageNameRpm(t *testing.T) {
	result := parsePackageName("nginx-1.18.0-1.el9.x86_64", "dnf")
	if result != "nginx" {
		t.Errorf("expected 'nginx', got '%s'", result)
	}
}

func TestPackagesApplierBackup(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg -l"] = "ii  curl     7.81.0-1   amd64\n"

	applier := &PackagesApplier{}
	backup, err := applier.Backup(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	var pb PackagesBackup
	json.Unmarshal(backup.Data, &pb)
	if pb.Distro != "apt" {
		t.Errorf("expected distro 'apt', got '%s'", pb.Distro)
	}
	if len(pb.Packages) != 1 || pb.Packages[0] != "curl" {
		t.Errorf("expected ['curl'], got %v", pb.Packages)
	}
}

func TestPackagesApplierApply(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.execOutput["dpkg -l"] = "ii  curl     7.81.0-1   amd64\n"

	pd := PackagesData{
		Distro:   "apt",
		Packages: []string{"nginx", "redis"},
	}
	raw, _ := json.Marshal(pd)

	var progressMsgs []WSMessage
	applier := &PackagesApplier{}
	err := applier.Apply(context.Background(), ssh, CategoryData{Type: "packages", Data: raw}, func(msg WSMessage) {
		progressMsgs = append(progressMsgs, msg)
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Should have progress and success messages
	if len(progressMsgs) == 0 {
		t.Error("expected progress messages")
	}

	// Last message should be success
	last := progressMsgs[len(progressMsgs)-1]
	if last.Status != "success" {
		t.Errorf("expected last status 'success', got '%s'", last.Status)
	}
}
