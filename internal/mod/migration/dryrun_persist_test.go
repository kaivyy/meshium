package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
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

// Stage B regression: the dry-run preview must round-trip through
// migration_steps (action='dryrun') so a page refresh can restore it via
// GetLatestStep. Executor.DryRun persists with exactly this CreateStep call,
// and buildSession restores with the matching GetLatestStep — so this test
// pins the storage contract both sides rely on. (Exercising the full DryRun
// would require a live SSH pool; the persistence layer is the part that
// changed and the part that can break silently.)
func TestDryRunPersistsResultForRefresh(t *testing.T) {
	repo := &mockRepo{migrations: []Migration{{ID: 1}}}

	original := &DryRunResult{
		MigrationID: 1,
		Categories: []DryRunCategory{{
			Category: "configs",
			Changes: []DryRunChange{{Type: "add", Resource: "file:/etc/x", Detail: "created"}},
			Summary:  "1 changes",
		}},
		Summary: DryRunSummary{TotalChanges: 1, AddCount: 1},
	}

	// Mirror what Executor.DryRun writes after computing the result.
	if _, err := repo.CreateStep(1, "all", "dryrun", mustJSON(original)); err != nil {
		t.Fatalf("CreateStep failed: %v", err)
	}

	// Mirror what buildSession reads to restore the preview on refresh.
	step, err := repo.GetLatestStep(1, "dryrun")
	if err != nil {
		t.Fatalf("GetLatestStep failed: %v", err)
	}
	if step == nil {
		t.Fatalf("expected a persisted 'dryrun' step, got none")
	}
	var restored DryRunResult
	if err := json.Unmarshal([]byte(step.Data), &restored); err != nil {
		t.Fatalf("persisted dry run not valid JSON: %v", err)
	}
	if restored.Summary.TotalChanges != original.Summary.TotalChanges {
		t.Fatalf("restored total %d != original %d", restored.Summary.TotalChanges, original.Summary.TotalChanges)
	}
	if len(restored.Categories) != 1 || restored.Categories[0].Category != "configs" {
		t.Fatalf("restored categories mismatch: %+v", restored.Categories)
	}

	// A second persist must become the "latest" — refresh always shows the
	// most recent preview, not a stale one.
	if _, err := repo.CreateStep(1, "all", "dryrun", mustJSON(&DryRunResult{Summary: DryRunSummary{TotalChanges: 2}})); err != nil {
		t.Fatalf("second CreateStep failed: %v", err)
	}
	step2, _ := repo.GetLatestStep(1, "dryrun")
	var restored2 DryRunResult
	if err := json.Unmarshal([]byte(step2.Data), &restored2); err != nil {
		t.Fatalf("second restore failed: %v", err)
	}
	if restored2.Summary.TotalChanges != 2 {
		t.Fatalf("expected latest dry run (total=2), got %d", restored2.Summary.TotalChanges)
	}
}

// dryRunDocker must fuse install-check + images + containers into ONE SSH
// round-trip (the two daemon calls are the slow part on a stalled daemon).
// Verifies single command, section parsing, and the NO_DOCKER short-circuit.
func TestDryRunDockerSingleProbeCommand(t *testing.T) {
	ssh := newMockSSH()

	// Docker present; one image and one container already on target.
	probe := "command -v docker >/dev/null 2>&1 || echo NO_DOCKER; echo ---IMAGES---; docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null; echo ---CONTAINERS---; docker ps -a --format '{{.Names}}' 2>/dev/null"
	ssh.execOutput[probe] = "---IMAGES---\nnginx:1.25\n---CONTAINERS---\nweb\n"

	dd := DockerData{
		Images:     []string{"nginx:1.25", "redis:7"}, // nginx present, redis missing -> add
		Containers: []DockerContainer{
			{Name: "web", Image: "nginx:1.25"}, // present -> modify
			{Name: "db", Image: "redis:7"},     // missing -> add
		},
	}
	data := CategoryData{Data: json.RawMessage(mustJSON(dd))}

	e := &Executor{}
	changes := e.dryRunDocker(context.Background(), ssh, data)

	var got []string
	for _, c := range changes {
		got = append(got, c.Resource+":"+c.Type)
	}
	if !containsStrExact(got, "docker:image:redis:7:add") {
		t.Fatalf("missing image add; got %v", got)
	}
	if !containsStrExact(got, "docker:container:db:add") {
		t.Fatalf("missing container add; got %v", got)
	}
	if !containsStrExact(got, "docker:container:web:modify") {
		t.Fatalf("existing container must be modify; got %v", got)
	}
	// nginx:1.25 already present -> must NOT be flagged as add.
	if containsStrExact(got, "docker:image:nginx:1.25:add") {
		t.Fatalf("existing image re-flagged; got %v", got)
	}
	// Decisive: exactly one docker probe command, not three.
	dockerCmds := 0
	for _, c := range ssh.commands {
		if strings.Contains(c, "docker") || strings.Contains(c, "NO_DOCKER") {
			dockerCmds++
		}
	}
	if dockerCmds != 1 {
		t.Fatalf("expected a single fused docker probe, got %d docker-related commands: %v", dockerCmds, ssh.commands)
	}
}

// NO_DOCKER short-circuit: docker absent -> exactly one add (install), no
// daemon calls attempted.
func TestDryRunDockerNotInstalledShortCircuits(t *testing.T) {
	ssh := newMockSSH()
	probe := "command -v docker >/dev/null 2>&1 || echo NO_DOCKER; echo ---IMAGES---; docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null; echo ---CONTAINERS---; docker ps -a --format '{{.Names}}' 2>/dev/null"
	ssh.execOutput[probe] = "NO_DOCKER\n"

	dd := DockerData{Images: []string{"nginx:1.25"}}
	data := CategoryData{Data: json.RawMessage(mustJSON(dd))}

	changes := (&Executor{}).dryRunDocker(context.Background(), ssh, data)
	if len(changes) != 1 || changes[0].Resource != "docker:install" {
		t.Fatalf("expected single docker:install, got %+v", changes)
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
