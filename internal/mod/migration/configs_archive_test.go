package migration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// gzTarBase64 builds what the remote `find ... | tar -czf - -T - | base64`
// pipeline produces, so the test drives the primary collect path rather than
// the SFTP fallback.
func gzTarBase64(t *testing.T, files map[string]string) string {
	t.Helper()
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw.Bytes())
}

// The existing collector test only ever exercised collectSlow, because the mock
// does not answer the tar command — so the fast path that runs in production
// had no coverage at all. That is how the find expression drifted into
// collecting nothing without a test noticing.
func TestConfigsCollectorParsesGzippedArchive(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("tar -czf", gzTarBase64(t, map[string]string{
		"etc/nginx/nginx.conf":         "worker_processes auto;\n",
		"etc/nginx/conf.d/default.conf": "server { listen 80; }\n",
	}))

	data, err := (&ConfigsCollector{Paths: []string{"/etc/nginx/"}}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cd.Count != 2 {
		t.Fatalf("expected 2 files from the archive path, got %d (%v)", cd.Count, keysOf(cd.Files))
	}
	// Keys are normalised to absolute so exclusion checks and the dry-run,
	// which both reason in absolute system paths, actually match.
	body, ok := cd.Files["/etc/nginx/nginx.conf"]
	if !ok {
		t.Fatalf("nginx.conf missing or not absolute; got %v", keysOf(cd.Files))
	}
	if string(body) != "worker_processes auto;\n" {
		t.Errorf("nginx.conf content = %q", string(body))
	}
}

// isExcluded compares against absolute paths like "/etc/fstab". tar members
// arrive as "etc/fstab", so before normalisation the exclusion list — the guard
// that stops OS-critical files being overwritten on the target — matched
// nothing on the archive path.
func TestConfigsCollectorAppliesExclusionsToArchivePaths(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("tar -czf", gzTarBase64(t, map[string]string{
		"etc/fstab":        "UUID=... / ext4 defaults 0 1\n",
		"etc/passwd":       "root:x:0:0::/root:/bin/bash\n",
		"etc/nginx/ok.conf": "server {}\n",
	}))

	data, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, banned := range []string{"/etc/fstab", "etc/fstab", "/etc/passwd", "etc/passwd"} {
		if _, ok := cd.Files[banned]; ok {
			t.Errorf("collected excluded file %q — it would overwrite the target's own", banned)
		}
	}
	if _, ok := cd.Files["/etc/nginx/ok.conf"]; !ok {
		t.Errorf("dropped a legitimate config; got %v", keysOf(cd.Files))
	}
}

// A plain (non-gzipped) archive must not be silently accepted as empty — the
// collector should fall back rather than record a successful empty collect.
func TestConfigsCollectorRejectsNonGzipArchive(t *testing.T) {
	if _, err := gunzip([]byte("this is not gzip")); err == nil {
		t.Fatal("gunzip accepted non-gzip input")
	}
}

func TestGunzipRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("hello config"))
	zw.Close()

	out, err := gunzip(buf.Bytes())
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if string(out) != "hello config" {
		t.Errorf("round trip = %q", string(out))
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
