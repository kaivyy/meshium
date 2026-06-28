package migration

import (
	"encoding/json"
	"testing"
)

func TestConfigsCollector(t *testing.T) {
	ssh := newMockSSH()
	// Path is shell-quoted in the find command
	ssh.execOutput["find '/etc/nginx' -type f 2>/dev/null"] = "/etc/nginx/nginx.conf\n/etc/nginx/conf.d/default.conf\n"
	ssh.downloadData["/etc/nginx/nginx.conf"] = []byte("worker_processes auto;\n")
	ssh.downloadData["/etc/nginx/conf.d/default.conf"] = []byte("server { listen 80; }\n")

	collector := &ConfigsCollector{Paths: []string{"/etc/nginx/"}}
	data, err := collector.Collect(ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var cd ConfigsData
	json.Unmarshal(data.Data, &cd)
	if cd.Count != 2 {
		t.Errorf("expected 2 files, got %d", cd.Count)
	}
	if _, ok := cd.Files["/etc/nginx/nginx.conf"]; !ok {
		t.Error("expected nginx.conf in files")
	}
}

func TestConfigsApplierApply(t *testing.T) {
	ssh := newMockSSH()

	cd := ConfigsData{
		Files: map[string][]byte{
			"/etc/nginx/nginx.conf": []byte("worker_processes auto;\n"),
		},
		Count: 1,
	}
	raw, _ := json.Marshal(cd)

	var progressMsgs []WSMessage
	applier := &ConfigsApplier{}
	err := applier.Apply(ssh, CategoryData{Type: "configs", Data: raw}, func(msg WSMessage) {
		progressMsgs = append(progressMsgs, msg)
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Verify file was uploaded
	if _, ok := ssh.uploadData["/etc/nginx/nginx.conf"]; !ok {
		t.Error("expected nginx.conf to be uploaded")
	}

	last := progressMsgs[len(progressMsgs)-1]
	if last.Status != "success" {
		t.Errorf("expected last status 'success', got '%s'", last.Status)
	}
}

func TestConfigsApplierRollback(t *testing.T) {
	ssh := newMockSSH()

	cb := ConfigsBackup{
		Files: map[string][]byte{
			"/etc/nginx/nginx.conf": []byte("# original config\n"),
		},
	}
	raw, _ := json.Marshal(cb)

	applier := &ConfigsApplier{}
	err := applier.Rollback(ssh, BackupData{Type: "configs", Data: raw})
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify file was restored
	if data, ok := ssh.uploadData["/etc/nginx/nginx.conf"]; ok {
		if string(data) != "# original config\n" {
			t.Error("expected original content to be restored")
		}
	} else {
		t.Error("expected nginx.conf to be restored via upload")
	}
}
