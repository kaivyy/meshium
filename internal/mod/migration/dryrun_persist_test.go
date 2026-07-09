package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// Stage A regression: dryRunConfigs must compare config files WITHOUT one
// SFTP Download per file. It should issue a single sha256sum command and
// flag add/modify from the hashes alone. This is the fix that removed the
// N+1 round-trip slowness.
func TestDryRunConfigsUsesSingleHashNotPerFileDownload(t *testing.T) {
	ssh := newMockSSH()

	// Source plans two files.
	//  - /etc/a.conf: target has identical content -> no change.
	//  - /etc/b.conf: target is missing -> add.
	//  - /etc/c.conf: target content differs -> modify.
	aContent := "alpha"
	bContent := "beta"
	cSource := "gamma-source"
	cTarget := "gamma-target"

	// sha256sum output for the two files that exist on the target.
	sumA := sha256.Sum256([]byte(aContent))
	sumC := sha256.Sum256([]byte(cTarget))
	ssh.execOutput["sha256sum "] = hex.EncodeToString(sumA[:]) + "  /etc/a.conf\n" +
		hex.EncodeToString(sumC[:]) + "  /etc/c.conf\n"

	cd := ConfigsData{
		Files: map[string][]byte{
			"/etc/a.conf": []byte(aContent),
			"/etc/b.conf": []byte(bContent),
			"/etc/c.conf": []byte(cSource),
		},
	}
	data := CategoryData{Data: json.RawMessage(mustJSON(cd))}

	e := &Executor{}
	changes := e.dryRunConfigs(context.Background(), ssh, data)

	// Only /etc/b.conf (add) and /etc/c.conf (modify) should appear; /etc/a.conf
	// matches and must not.
	var got []string
	for _, c := range changes {
		got = append(got, c.Resource+":"+c.Type)
	}

	addB := "file:/etc/b.conf:add"
	modC := "file:/etc/c.conf:modify"
	if !containsStrExact(got, addB) {
		t.Fatalf("expected %q in changes %v", addB, got)
	}
	if !containsStrExact(got, modC) {
		t.Fatalf("expected %q in changes %v", modC, got)
	}
	if containsStrExact(got, "file:/etc/a.conf:add") || containsStrExact(got, "file:/etc/a.conf:modify") {
		t.Fatalf("unchanged file /etc/a.conf must not appear, got %v", got)
	}

	// The decisive assertion: no per-file Download was issued.
	if ssh.downloadCalls != 0 {
		t.Fatalf("dryRunConfigs must not Download files; downloadCalls=%d", ssh.downloadCalls)
	}
	// And it shelled out exactly one hash command covering all paths.
	if !containsCommandPrefix(ssh.commands, "sha256sum ") {
		t.Fatalf("expected a single sha256sum command, got %v", ssh.commands)
	}
}

// Regression: sha256sum exits 1 when any listed path is missing — the normal
// "add" case. The old code treated exit != 0 as failure and fell back to the
// per-file Download path, restoring the N+1 round-trip the optimization was
// meant to kill. Only exit 127/126 (binary absent) may fall back.
func TestDryRunConfigsToleratesSha256ExitOneOnMissingFile(t *testing.T) {
	ssh := newMockSSH()

	// Plan one missing file -> sha256sum prints nothing for it and exits 1.
	aContent := "alpha"
	sumA := sha256.Sum256([]byte(aContent))
	ssh.execOutput["sha256sum "] = hex.EncodeToString(sumA[:]) + "  /etc/a.conf\n"
	ssh.execExit["sha256sum "] = 1 // missing /etc/b.conf forces this

	cd := ConfigsData{Files: map[string][]byte{
		"/etc/a.conf": []byte(aContent), // matches -> no change
		"/etc/b.conf": []byte("beta"),   // missing on target -> add
	}}
	data := CategoryData{Data: json.RawMessage(mustJSON(cd))}

	e := &Executor{}
	changes := e.dryRunConfigs(context.Background(), ssh, data)

	var got []string
	for _, c := range changes {
		got = append(got, c.Resource+":"+c.Type)
	}
	if !containsStrExact(got, "file:/etc/b.conf:add") {
		t.Fatalf("expected /etc/b.conf add despite sha256sum exit 1, got %v", got)
	}
	// Decisive: exit 1 must NOT route to the per-file fallback.
	if ssh.downloadCalls != 0 {
		t.Fatalf("exit 1 routed to per-file Download fallback; downloadCalls=%d (N+1 regression)", ssh.downloadCalls)
	}
}
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func containsStrExact(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}
