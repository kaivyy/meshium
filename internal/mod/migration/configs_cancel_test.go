package migration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Found by the VPS-B1 live matrix (C6): with a cancelled context the configs
// collect returned SUCCESS carrying zero files. collectArchive fails, the
// bounded SFTP fallback bails out on ctx.Done, and Collect then marshals an
// empty ConfigsData as a healthy result — the same silent-empty class already
// fixed for packages and services, reached here through cancellation.
//
// A cancelled migration must surface the cancellation, not a clean empty
// snapshot that a later apply would treat as "the source has no configs".
func TestConfigsCollectHonorsCancelledContext(t *testing.T) {
	ssh := newMockSSH()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(ctx, ssh)
	if err == nil {
		t.Fatal("Collect returned success for a cancelled context")
	}
	if !isContextErr(err) {
		t.Errorf("error should report cancellation, got: %v", err)
	}
}

// Independently of cancellation, a collect that yields no files at all is a
// failed probe, not an answer — the source always has config files.
func TestConfigsCollectRejectsEmptyResult(t *testing.T) {
	ssh := newMockSSH() // answers nothing: archive fails, fallback finds nothing
	_, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(context.Background(), ssh)
	if err == nil {
		t.Fatal("Collect recorded an empty config set as success")
	}
}

// An explicitly narrow path that legitimately holds files must still succeed.
func TestConfigsCollectSucceedsWithFiles(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("tar -czf", gzTarBase64(t, map[string]string{
		"etc/nginx/nginx.conf": "worker_processes auto;\n",
	}))

	data, err := (&ConfigsCollector{Paths: []string{"/etc/nginx/"}}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var cd ConfigsData
	if err := json.Unmarshal(data.Data, &cd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cd.Count != 1 {
		t.Errorf("Count = %d, want 1", cd.Count)
	}
}

// isContextErr reports whether err wraps a context cancellation/deadline.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
