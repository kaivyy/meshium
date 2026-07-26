package migration

import (
	"context"
	"strings"
	"testing"
)

// Probes that ran `cmd 2>&1` folded the shell's "command not found" into
// stdout and ignored the exit code, so a missing tool produced a non-empty
// "version". checkDockerVersion's critical blocker ("Docker not installed on
// target but present on source") fires only when target.DockerVersion == "",
// which could therefore never happen — a migration carrying Docker workloads
// was declared compatible against a target with no Docker at all.
//
// Live check: `sh -c 'PATH=/nonexistent docker --version 2>&1'` prints
// "sh: 1: docker: not found" on stdout and exits 127.
func TestCollectServerInfoTreatsMissingToolsAsAbsent(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("uname -m", "x86_64\n")
	ssh.addOutput("uname -r", "6.8.0\n")
	// Every optional tool is missing: shell prints "not found" AND exits 127.
	for _, probe := range []string{"docker --version", "docker compose version", "docker info", "openssl version"} {
		ssh.addOutput(probe, "sh: 1: not found\n")
		ssh.execExit[probe] = 127
	}

	e := &CompatibilityEngine{}
	info, err := e.collectServerInfo(context.Background(), ssh)
	if err != nil {
		t.Fatalf("collectServerInfo: %v", err)
	}

	if info.DockerVersion != "" {
		t.Errorf("DockerVersion = %q for a host without docker — the 'Docker missing on target' critical blocker can never fire", info.DockerVersion)
	}
	if info.ComposeVersion != "" {
		t.Errorf("ComposeVersion = %q for a host without compose", info.ComposeVersion)
	}
	if info.StorageDriver != "" {
		t.Errorf("StorageDriver = %q for a host without docker — checkDockerStorageDriver would compare two error strings and emit a bogus mismatch warning", info.StorageDriver)
	}
	if info.OpenSSLVersion != "" {
		t.Errorf("OpenSSLVersion = %q for a host without openssl", info.OpenSSLVersion)
	}
}

// The critical blocker must actually fire once DockerVersion is honest.
func TestCheckDockerVersionBlocksMissingTargetDocker(t *testing.T) {
	e := &CompatibilityEngine{}
	res := e.checkDockerVersion(
		&serverInfo{DockerVersion: "Docker version 26.1.0"},
		&serverInfo{DockerVersion: ""},
	)
	if res.Passed {
		t.Fatal("target without Docker passed the docker_version check while the source has Docker")
	}
	if res.Severity != SeverityCritical {
		t.Errorf("severity = %q, want critical", res.Severity)
	}
}

// `timedatectl ... | cut ... || cat /etc/timezone || echo UTC`: a pipeline's
// exit status is the LAST command's, so `cut` exits 0 even when timedatectl is
// absent and neither fallback ever runs. On a non-systemd host Timezone was ""
// and checkTimezone compared "" == "" -> "Both servers use  timezone", passing.
// The command must be structured so an empty primary result actually reaches
// the fallbacks.
func TestTimezoneProbeReachesFallbacks(t *testing.T) {
	cmd := timezoneProbeCmd()
	if strings.Contains(cmd, "| cut -d= -f2 ||") {
		t.Errorf("timezone probe chains || after a pipeline ending in cut, whose exit is always 0, so the fallbacks are dead: %s", cmd)
	}
	if !strings.Contains(cmd, "/etc/timezone") || !strings.Contains(cmd, "UTC") {
		t.Errorf("timezone probe lost its fallbacks: %s", cmd)
	}
}

// Empty-on-both-sides must not read as a match: it means the probe failed, not
// that the servers agree.
func TestCheckTimezoneDoesNotPassOnEmptyProbe(t *testing.T) {
	e := &CompatibilityEngine{}
	res := e.checkTimezone(&serverInfo{Timezone: ""}, &serverInfo{Timezone: ""})
	if res.Passed && strings.Contains(res.Message, "Both servers use  timezone") {
		t.Errorf("empty timezone probe reported as a match: %q", res.Message)
	}
}
