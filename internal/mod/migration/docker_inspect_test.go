package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// `docker ps --format '{{.ID}}'` prints the 12-char SHORT id, but
// `docker inspect --format '{{.Id}}'` prints the 64-char FULL id. The batch
// inspect map was keyed by the full id and looked up by the short one, so it
// never matched and Env/Labels stayed nil for every container.
//
// Verified against a live daemon:
//
//	ps  .ID  = e63bab5cdab5                                                     (12)
//	insp .Id = e63bab5cdab5c292a286ab6c3e4df605442fa62f8a5f48c97bd5dd5f3f4ce5f7 (64)
//
// The consequence is silent: DockerApplier.Apply's direct-recreate path issues
// `docker run` with no -e and no --label, so containers come up stripped of
// their database URLs, secrets and compose metadata — and the step reports
// success.
func TestDockerCollectPopulatesEnvAndLabels(t *testing.T) {
	const shortID = "e63bab5cdab5"

	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")
	ssh.addOutput("docker ps -a --format", shortID+"|web|nginx:1.25|Up 2 hours|80/tcp\n")
	// The inspect stubs answer with whatever id the production format asks for.
	ssh.addOutput("Config.Env", shortID+"|DATABASE_URL=postgres://app\nSECRET_KEY=s3cr3t\n|||\n")
	ssh.addOutput("Config.Labels", shortID+"|com.example.team=payments\n|||\n")

	data, err := (&DockerCollector{}).Collect(context.Background(), ssh)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var dd DockerData
	if err := json.Unmarshal(data.Data, &dd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dd.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(dd.Containers))
	}

	c := dd.Containers[0]
	if len(c.Env) == 0 {
		t.Error("container Env is empty — env vars are dropped on recreate")
	}
	if len(c.Labels) == 0 {
		t.Error("container Labels is empty — labels are dropped on recreate")
	}

	if got := c.Env["DATABASE_URL"]; got != "postgres://app" {
		t.Errorf("Env[DATABASE_URL] = %q, want %q (full Env: %v)", got, "postgres://app", c.Env)
	}
	if got := c.Labels["com.example.team"]; got != "payments" {
		t.Errorf("Labels[com.example.team] = %q, want %q (full Labels: %v)", got, "payments", c.Labels)
	}
}

// The inspect commands must ask for an id in the same form `docker ps` returns,
// otherwise the lookup silently misses.
func TestDockerInspectRequestsShortID(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")
	ssh.addOutput("docker ps -a --format", "abc123def456|web|nginx|Up|80/tcp\n")

	if _, err := (&DockerCollector{}).Collect(context.Background(), ssh); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, cmd := range ssh.commands {
		if !strings.Contains(cmd, "docker inspect") {
			continue
		}
		if strings.Contains(cmd, "{{.Id}}|") {
			t.Errorf("inspect asks for the full 64-char id, which never matches the\n"+
				"12-char id from `docker ps`:\n  %s", cmd)
		}
	}
}

// `{{println $k "=" $v}}` is Sprintln, which space-separates its operands and
// yields `key = value`. Splitting that on the first `=` produces a key with a
// trailing space and a value with a leading space, so the recreate emits
// `--label 'com.example.team '=' payments'`.
//
// Verified live: `com.docker.compose.config-hash = 45a15baf...`
func TestDockerLabelFormatHasNoSpacesAroundEquals(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")
	ssh.addOutput("docker ps -a --format", "abc123def456|web|nginx|Up|80/tcp\n")

	if _, err := (&DockerCollector{}).Collect(context.Background(), ssh); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, cmd := range ssh.commands {
		if strings.Contains(cmd, "Config.Labels") && strings.Contains(cmd, `println $k "=" $v`) {
			t.Errorf("label template space-separates around '=', corrupting keys and values:\n  %s", cmd)
		}
	}
}

// Docker installed but daemon down: `docker ps` exits non-zero with empty
// stdout. That was recorded as a successful "no containers" collect, so the
// migration proceeded with zero workloads and reported success.
func TestDockerCollectFailsWhenDaemonDown(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("which docker", "/usr/bin/docker\n")
	ssh.addOutput("docker ps -a --format", "")
	ssh.execExit["docker ps -a --format"] = 1

	if _, err := (&DockerCollector{}).Collect(context.Background(), ssh); err == nil {
		t.Fatal("Collect recorded 'no containers' while the docker daemon was down")
	}
}

// A container with no labels renders as `<id>||||`. The parser's `line == "|||"`
// guard never fires for that shape, so currentMap was left holding the PREVIOUS
// container's labels and they were stored under this container's id.
func TestParseBatchInspectDoesNotLeakBetweenContainers(t *testing.T) {
	out := strings.Join([]string{
		"aaaaaaaaaaaa|FOO=1",
		"BAR=2",
		"|||",
		"bbbbbbbbbbbb|",
		"|||",
		"",
	}, "\n")

	m := parseBatchInspect(out)

	if got := m["aaaaaaaaaaaa"]; len(got) != 2 {
		t.Fatalf("first container: expected 2 entries, got %v", got)
	}
	_ = context.Background
	if got, ok := m["bbbbbbbbbbbb"]; ok && len(got) > 0 {
		t.Errorf("second container has no entries but inherited %v from the first", got)
	}
}
