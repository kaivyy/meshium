package migration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// gzTarBase64WithMeta builds the collect pipeline's output carrying real
// ownership/permission metadata, which tar records and the collector used to
// discard.
func gzTarBase64WithMeta(t *testing.T, hdrs []tar.Header, bodies []string) string {
	t.Helper()
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	for i, h := range hdrs {
		h.Size = int64(len(bodies[i]))
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("hdr: %v", err)
		}
		if _, err := tw.Write([]byte(bodies[i])); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	tw.Close()
	zw.Close()
	return base64.StdEncoding.EncodeToString(raw.Bytes())
}

// Config apply wrote file CONTENT only. A source /etc/ssh/sshd_config owned
// root:root mode 0600, or a private key at 0400, landed on the target with
// whatever the SFTP default umask produced — silently widening secrets to
// world-readable. tar already carries mode/uid/gid; the collector must keep
// them and the applier must restore them.
func TestConfigsCollectorCapturesFileMetadata(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("tar -czf", gzTarBase64WithMeta(t,
		[]tar.Header{
			{Name: "etc/ssh/sshd_config", Mode: 0o600, Uid: 0, Gid: 0, Uname: "root", Gname: "root"},
			{Name: "etc/nginx/nginx.conf", Mode: 0o644, Uid: 0, Gid: 33, Uname: "root", Gname: "www-data"},
		},
		[]string{"PermitRootLogin no\n", "worker_processes auto;\n"},
	))

	data, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	m, ok := cd.Meta["/etc/ssh/sshd_config"]
	if !ok {
		t.Fatalf("no metadata captured for sshd_config; Meta=%v", cd.Meta)
	}
	if m.Mode != 0o600 {
		t.Errorf("sshd_config mode = %o, want 600", m.Mode)
	}
	if m.Uname != "root" || m.Gname != "root" {
		t.Errorf("sshd_config owner = %s:%s, want root:root", m.Uname, m.Gname)
	}
	if n := cd.Meta["/etc/nginx/nginx.conf"]; n.Gname != "www-data" || n.Mode != 0o644 {
		t.Errorf("nginx.conf meta = %o %s:%s, want 644 root:www-data", n.Mode, n.Uname, n.Gname)
	}
}

// Apply must chmod/chown to the captured values. Without this a 0600 secret
// becomes 0644 on the target.
func TestConfigsApplyRestoresModeAndOwner(t *testing.T) {
	cd := ConfigsData{
		Files: map[string][]byte{"/etc/ssh/sshd_config": []byte("PermitRootLogin no\n")},
		Meta: map[string]FileMeta{
			"/etc/ssh/sshd_config": {Mode: 0o600, Uname: "root", Gname: "root"},
		},
		Count: 1,
	}
	raw, _ := json.Marshal(cd)

	ssh := newMockSSH()
	if err := (&ConfigsApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "configs", Data: raw}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var sawChmod, sawChown bool
	for _, cmd := range ssh.commands {
		if strings.Contains(cmd, "chmod 600") && strings.Contains(cmd, "/etc/ssh/sshd_config") {
			sawChmod = true
		}
		if strings.Contains(cmd, "chown") && strings.Contains(cmd, "root:root") && strings.Contains(cmd, "/etc/ssh/sshd_config") {
			sawChown = true
		}
	}
	if !sawChmod {
		t.Errorf("Apply never chmod'ed the file to its source mode; commands=%v", ssh.commands)
	}
	if !sawChown {
		t.Errorf("Apply never chown'ed the file to its source owner; commands=%v", ssh.commands)
	}
}

// A payload without metadata (migration planned before this change) must still
// apply — no chmod/chown attempted, no failure.
func TestConfigsApplyToleratesMissingMetadata(t *testing.T) {
	cd := ConfigsData{Files: map[string][]byte{"/etc/hosts.allow": []byte("ALL: LOCAL\n")}, Count: 1}
	raw, _ := json.Marshal(cd)

	ssh := newMockSSH()
	if err := (&ConfigsApplier{}).Apply(context.Background(), ssh, CategoryData{Type: "configs", Data: raw}, nil); err != nil {
		t.Fatalf("Apply on legacy payload: %v", err)
	}
	for _, cmd := range ssh.commands {
		if strings.Contains(cmd, "chmod") || strings.Contains(cmd, "chown") {
			t.Errorf("legacy payload should not attempt metadata restore, got %q", cmd)
		}
	}
}

// Backup must capture the target's own mode/owner so rollback restores them
// too — otherwise rollback re-widens the very permissions it should restore.
func TestConfigsBackupCapturesMetadata(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("find /etc", "/etc/ssh/sshd_config\n")
	ssh.downloadData["/etc/ssh/sshd_config"] = []byte("old\n")
	// `stat -c '%a %U %G %n'` over the collected paths.
	ssh.addOutput("stat -c", "600 root root /etc/ssh/sshd_config\n")

	backup, err := (&ConfigsApplier{}).Backup(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	var cb ConfigsBackup
	if err := json.Unmarshal(backup.Data, &cb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m, ok := cb.Meta["/etc/ssh/sshd_config"]
	if !ok {
		t.Fatalf("backup captured no metadata; Meta=%v", cb.Meta)
	}
	if m.Mode != 0o600 || m.Uname != "root" {
		t.Errorf("backup meta = %o %s:%s, want 600 root:root", m.Mode, m.Uname, m.Gname)
	}
}
