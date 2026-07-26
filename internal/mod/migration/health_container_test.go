package migration

import (
	"context"
	"testing"
)

func containerHealthResult(t *testing.T, stdout string, exit int) HealthCheckResult {
	t.Helper()
	ssh := newMockSSH()
	ssh.addOutput("docker inspect", stdout)
	if exit != 0 {
		ssh.execExit["docker inspect"] = exit
	}
	e := NewHealthEngine(ssh, ssh, nil)
	return e.checkContainer(context.Background(), HealthCheckConfig{
		Type:   HealthCheckContainer,
		Target: "web",
	})
}

// A container with no HEALTHCHECK makes the bare {{.State.Health.Status}}
// template fail: docker inspect prints "template parsing error: ... map has no
// entry for key Health" (verified live) and exits 1, with 2>&1 folding the
// error onto stdout. The output contained no "running", so every
// healthcheck-less container was scored unhealthy. The template now guards
// with {{if .State.Health}} and prints "none".
func TestCheckContainerHealthyWithoutHealthcheck(t *testing.T) {
	res := containerHealthResult(t, "running none\n", 0)
	if res.Status != "healthy" {
		t.Errorf("running container without a healthcheck scored %q: %s", res.Status, res.ErrorMessage)
	}
}

// strings.Contains(output, "healthy") is also true for "unhealthy", so the old
// substring parser passed a genuinely failing container as healthy.
func TestCheckContainerUnhealthyIsNotHealthy(t *testing.T) {
	res := containerHealthResult(t, "running unhealthy\n", 0)
	if res.Status != "unhealthy" {
		t.Errorf("container reporting unhealthy scored %q", res.Status)
	}
}

func TestCheckContainerHealthyWithHealthcheck(t *testing.T) {
	res := containerHealthResult(t, "running healthy\n", 0)
	if res.Status != "healthy" {
		t.Errorf("running+healthy container scored %q: %s", res.Status, res.ErrorMessage)
	}
}

func TestCheckContainerStoppedIsUnhealthy(t *testing.T) {
	res := containerHealthResult(t, "exited none\n", 0)
	if res.Status != "unhealthy" {
		t.Errorf("exited container scored %q", res.Status)
	}
}

// A failing inspect (container missing, daemon down) must not read as healthy.
func TestCheckContainerInspectErrorIsUnhealthy(t *testing.T) {
	res := containerHealthResult(t, "Error: No such object: web\n", 1)
	if res.Status == "healthy" {
		t.Error("failed docker inspect scored healthy")
	}
}
