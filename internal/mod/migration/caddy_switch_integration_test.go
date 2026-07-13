//go:build integration

// Live integration test for the Phase 4B Caddy fenced traffic switcher. It
// stands up a real Caddy container, writes a Caddyfile pointing at a "source"
// health endpoint, then drives NewCaddySwitcher to swap the config to a
// "target" endpoint and PROVES the traffic moved via read-after-write: after a
// `caddy reload`, a GET to the live health port returns the target marker.
//
// Run: go test -tags integration ./internal/mod/migration/ -run Caddy -v
// Requires the `caddy:2-alpine` image.

package migration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// caddyContainerExecuter satisfies SSHExecuter by exec'ing into a Caddy
// container. Caddy runs as root and has no sudo, so we run commands directly.
type caddyContainerExecuter struct {
	container string
}

func (e caddyContainerExecuter) Exec(cmd string) (string, string, int, error) {
	return e.ExecContext(context.Background(), cmd)
}

func (e caddyContainerExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	full := fmt.Sprintf("docker exec %s sh -c %s", e.container, shQuote(cmd))
	out, err := exec.CommandContext(ctx, "bash", "-lc", full).CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(out), "", exit, err
}

func (caddyContainerExecuter) IsAlive() bool { return true }

func (e caddyContainerExecuter) Upload(r io.Reader, p string) error {
	buf := new(strings.Builder)
	io.Copy(buf, r)
	// Write via a heredoc so binary-safe content lands at the path.
	cmd := fmt.Sprintf("docker exec -i %s sh -c 'cat > %s'", e.container, shQuote(p))
	c := exec.Command("bash", "-lc", cmd)
	c.Stdin = strings.NewReader(buf.String())
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("upload to %s: %w (%s)", p, err, out)
	}
	return nil
}
func (caddyContainerExecuter) Download(string, io.Writer) error {
	return fmt.Errorf("caddyContainerExecuter: Download unsupported")
}

// TestCaddySwitcherLive proves the Caddy fenced switcher end-to-end against a
// real Caddy process: config validate, reload, and read-after-write ownership
// verification through a live health endpoint.
func TestCaddySwitcherLive(t *testing.T) {
	const cname = "meshium-caddy-it"
	const hostPort = "8090" // host:8080 in container is taken by another service
	cfgPath := "/etc/caddy/Caddyfile"
	// A minimal Caddyfile that serves a marker on :8080.
	srcCaddyfile := "http://localhost:8080 {\n  respond \"source\"\n}\n"
	tgtCaddyfile := "http://localhost:8080 {\n  respond \"target\"\n}\n"

	mustRun(t, "docker", "rm", "-f", cname)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", cname).Run() })

	// Hold the container up without caddy, so we can seed the config first.
	mustRun(t, "docker", "run", "-d", "--rm", "--name", cname,
		"-p", hostPort+":8080", "caddy:2-alpine", "tail", "-f", "/dev/null")
	ex := caddyContainerExecuter{container: cname}

	if err := ex.Upload(strings.NewReader(srcCaddyfile), cfgPath); err != nil {
		t.Fatalf("seed caddyfile: %v", err)
	}
	// Start caddy as a background daemon (admin API on localhost:2019).
	if out, _, rc, err := ex.ExecContext(context.Background(),
		"caddy start --config "+shQuote(cfgPath)+" --adapter caddyfile 2>&1"); err != nil || rc != 0 {
		t.Fatalf("caddy start: rc=%d err=%v out=%s", rc, err, out)
	}
	waitHealthy(t, "http://localhost:"+hostPort, "source")

	// Drive the fenced switcher: swap config to point at "target".
	req := TrafficSwitchRequest{
		IdempotencyKey: "caddy-live-1",
		ConfigPath:     cfgPath,
		NewConfig:      tgtCaddyfile,
		VerifyURL:      "http://localhost:" + hostPort,
		VerifyValue:    "target",
	}
	s := NewCaddySwitcher(ex, &http.Client{Timeout: 15 * time.Second})
	res, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("caddy switch: %v (res=%+v)", err, res)
	}
	if !res.Switched || !res.Verified || !res.ConfigTestOK || !res.ReloadOK {
		t.Fatalf("caddy switch flags: %+v", res)
	}
	// Read-after-write proof: a fresh GET shows the target marker.
	if m := curlMarker(t, "http://localhost:"+hostPort); m != "target" {
		t.Fatalf("traffic did not move to target: marker=%q", m)
	}

	// Idempotent reentry returns cached result (no error, flags intact).
	res2, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("caddy switch reentry: %v", err)
	}
	if !res2.Switched || !res2.Verified {
		t.Fatalf("caddy reentry flags: %+v", res2)
	}
}

// waitHealthy polls GET url until the trimmed body equals want or times out.
func waitHealthy(t *testing.T, url, want string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if m := curlMarker(t, url); m == want {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("%s never reported %q", url, want)
}

// curlMarker GETs a URL and returns the trimmed body (the marker).
func curlMarker(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return "" // not ready yet
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(b))
}
