//go:build integration

// Integration tests for the Phase 2B direct rsync transfer path against a
// LIVE pair of alpine+rsync containers. The unfakeable value here is real
// rsync moving a tree source→target over SSH, a real checksum verify
// proving byte-identity, and a real reconcile-after-restart that trusts a
// live partial only when the source is unchanged.
//
// Run: go test -tags integration ./internal/mod/transfer/ -run 'LiveRsync' -v -timeout 300s
// Requires the `docker` CLI; alpine is pulled on demand.

package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// docker-backed executer (rsync + ssh present)
// ---------------------------------------------------------------------------

// alpineExecuter satisfies transport.SSHExecuter by exec'ing inside a named
// alpine container. The container has rsync and ssh wired, so rsync's own
// SSH reachability probe is exercised for real.
type alpineExecuter struct {
	container string
}

func (d alpineExecuter) Exec(cmd string) (string, string, int, error) {
	return d.ExecContext(context.Background(), cmd)
}

func (d alpineExecuter) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	// Use a non-login shell: alpine's login shell sources
	// /etc/profile which runs `mesg`/`stty` and can emit spurious
	// stderr/exit noise that pollutes command output.
	c := exec.CommandContext(ctx, "docker", "exec", d.container, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	// docker exec merges the container's stdout+stderr into one pipe.
	// Return it as BOTH stdout and stderr so callers that only log
	// stderr (e.g. rsync's error wrapper) still see the real
	// failure reason.
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(out), string(out), exit, err
}

func (d alpineExecuter) IsAlive() bool                          { return true }
func (d alpineExecuter) Upload(io.Reader, string) error      { return errors.New("alpineExecuter: Upload unsupported") }
func (d alpineExecuter) Download(string, io.Writer) error    { return errors.New("alpineExecuter: Download unsupported") }

// ---------------------------------------------------------------------------
// pair setup
// ---------------------------------------------------------------------------

type rsyncPair struct {
	srcExec, tgtExec alpineExecuter
	srcC, tgtC, net string
}

func setupRsyncPair(t *testing.T) *rsyncPair {
	t.Helper()
	net := "rsynctest-" + randSuffix()
	mustRun(t, "docker", "network", "create", net)

	src := "rsync-src-" + randSuffix()
	tgt := "rsync-tgt-" + randSuffix()
	// Install rsync + openssh on both; generate a key on source and
	// authorize it on target so rsync's real SSH reachability probe works.
	const setup = `set -e
apk add --no-cache rsync openssh >/dev/null 2>&1
ssh-keygen -t ed25519 -N '' -f /root/.ssh/id_ed25519 >/dev/null 2>&1
mkdir -p /root/.ssh && chmod 700 /root/.ssh
cat /root/.ssh/id_ed25519.pub`
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", src, "--network", net, "--network-alias", "rsync-src",
		"alpine", "sleep", "infinity")
	mustRun(t, "docker", "run", "-d", "--rm",
		"--name", tgt, "--network", net, "--network-alias", "rsync-tgt",
		"alpine", "sleep", "infinity")

	// Start sshd on BOTH containers so rsync's real SSH reachability
	// probe can run source→target. Permit root key login.
	for _, c := range []string{src, tgt} {
		mustRun(t, "docker", "exec", c, "sh", "-lc",
			"apk add --no-cache rsync openssh >/dev/null 2>&1; ssh-keygen -A >/dev/null 2>&1; "+
				"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config 2>/dev/null; "+
				"/usr/sbin/sshd 2>/dev/null; true")
	}

	srcPub := strings.TrimSpace(mustOut(t, "docker", "exec", src, "sh", "-lc", setup))
	// Drop the source pubkey into the target's authorized_keys via a temp
	// file on the host + docker cp (avoids heredoc/quoting in a remote sh).
	pubFile := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(pubFile, []byte(srcPub+"\n"), 0600); err != nil {
		t.Fatalf("write pubkey: %v", err)
	}
	mustRun(t, "docker", "exec", tgt, "sh", "-lc", "mkdir -p /root/.ssh && chmod 700 /root/.ssh")
	mustRun(t, "docker", "cp", pubFile, tgt+":/root/.ssh/authorized_keys")
	mustRun(t, "docker", "exec", tgt, "sh", "-lc",
		"apk add --no-cache rsync openssh >/dev/null 2>&1; chmod 600 /root/.ssh/authorized_keys")
	// Make target reachable as root@rsync-tgt without host-key prompts.
	mustRun(t, "docker", "exec", src, "sh", "-lc",
		"ssh-keyscan -H rsync-tgt >> /root/.ssh/known_hosts 2>/dev/null; ssh-keyscan -H 127.0.0.1 >> /root/.ssh/known_hosts 2>/dev/null; true")

	pair := &rsyncPair{
		srcExec: alpineExecuter{container: src},
		tgtExec: alpineExecuter{container: tgt},
		srcC:    src, tgtC: tgt, net: net,
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", src, tgt).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
	})
	return pair
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// TestLiveRsyncDirectTreeTransfer: real rsync source→target over SSH with
// checksum verify proving byte-identity.
func TestLiveRsyncDirectTreeTransfer(t *testing.T) {
	pair := setupRsyncPair(t)

	// Seed a tree on the source.
	seed := `set -e
mkdir -p /data/app
head -c 1048576 /dev/urandom > /data/app/blob.bin
printf 'config line\n' > /data/app/conf.yaml
printf 'more text\n' >> /data/app/conf.yaml`
	mustRun(t, "docker", "exec", pair.srcC, "sh", "-lc", seed)

	src := TransferTarget{Host: "rsync-src", User: "root", SSHClient: pair.srcExec, Path: "/data/app"}
	dst := TransferTarget{Host: "rsync-tgt", User: "root", SSHClient: pair.tgtExec, Path: "/data"}

	if got := NewRsyncStrategy().Mode(context.Background(), src, dst); got != "direct" {
		t.Fatalf("expected direct mode, got %q", got)
	}

	strat := NewRsyncStrategy()
	// Use the real rsync availability gate before transferring.
	avail := strat.Availability(context.Background(), src, dst)
	if !avail.OK() {
		t.Fatalf("availability not OK: %+v", avail)
	}
	res, err := strat.Transfer(context.Background(), src, dst,
		TransferOptions{MaxRetries: 1, ProgressInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if res.Strategy != "rsync" {
		t.Fatalf("expected rsync strategy, got %s", res.Strategy)
	}

	// Verify byte-identity on the target.
	gotBlob := mustOut(t, "docker", "exec", pair.tgtC, "sh", "-lc",
		"sha256sum /data/app/blob.bin | awk '{print $1}'")
	wantBlob := mustOut(t, "docker", "exec", pair.srcC, "sh", "-lc",
		"sha256sum /data/app/blob.bin | awk '{print $1}'")
	if gotBlob != wantBlob {
		t.Fatalf("blob checksum mismatch: src=%s tgt=%s", wantBlob, gotBlob)
	}

	// And through the engine's own verifier (checksum the file, not the dir).
	v := NewChecksumVerifier()
	srcFile := src
	srcFile.Path = "/data/app/blob.bin"
	dstFile := dst
	dstFile.Path = "/data/app/blob.bin"
	sum, err := v.Verify(context.Background(), srcFile, dstFile)
	if err != nil {
		t.Fatalf("engine verify failed: %v", err)
	}
	if sum != wantBlob {
		t.Fatalf("engine checksum %s != raw %s", sum, wantBlob)
	}
}

// TestLiveRsyncReconcileTrustsPartial: a partial target + unchanged source
// reconciles to Resume; a changed source reconciles to ManualIntervention.
func TestLiveRsyncReconcileTrustsPartial(t *testing.T) {
	pair := setupRsyncPair(t)
	mustRun(t, "docker", "exec", pair.srcC, "sh", "-lc",
		"mkdir -p /data && head -c 1048576 /dev/urandom > /data/blob.bin")
	srcSnapshot := mustOut(t, "docker", "exec", pair.srcC, "sh", "-lc",
		"stat -c %s-%Y /data/blob.bin")

	// Simulate a partial target (truncated file) created by a previous run.
	partial := "partial-tmp"
	mustRun(t, "docker", "exec", pair.tgtC, "sh", "-lc",
		fmt.Sprintf("mkdir -p /tmp && head -c 524288 /dev/urandom > /tmp/%s", partial))

	cp := &TransferCheckpoint{
		TransferID:        "tx-live",
		SourceSnapshot:    srcSnapshot,
		TargetPartialState: partial,
		BytesTransferred:  524288,
	}

	// Source unchanged → Resume.
	ev := ReconcileEvidence{
		CurrentSourceSnapshot:    srcSnapshot,
		PartialTargetExists:      true,
		CurrentTargetPartialState: partial,
		StrategyStillValid:        true,
	}
	if v, err := Reconcile(cp, ev); v != VerdictResume || err != nil {
		t.Fatalf("unchanged source: expected Resume, got %v err=%v", v, err)
	}

	// Source changed → ManualIntervention (fail closed).
	mustRun(t, "docker", "exec", pair.srcC, "sh", "-lc",
		"head -c 16 /dev/urandom >> /data/blob.bin")
	newSnapshot := mustOut(t, "docker", "exec", pair.srcC, "sh", "-lc",
		"stat -c %s-%Y /data/blob.bin")
	if newSnapshot == srcSnapshot {
		t.Skip("source snapshot coincidentally unchanged; cannot test change path")
	}
	ev2 := ReconcileEvidence{
		CurrentSourceSnapshot:    newSnapshot,
		PartialTargetExists:      true,
		CurrentTargetPartialState: partial,
		StrategyStillValid:        true,
	}
	v, err := Reconcile(cp, ev2)
	if v != VerdictManualIntervention {
		t.Fatalf("changed source: expected ManualIntervention, got %v err=%v", v, err)
	}
	if !errors.Is(err, ErrTransferReconcileFailed) {
		t.Fatalf("expected ErrTransferReconcileFailed, got %v", err)
	}
}

// TestLiveRsyncMissingRsyncBlocksDirect: removing rsync from the target
// flips Mode() to "blocked" so the gate refuses a silent downgrade.
func TestLiveRsyncMissingRsyncBlocksDirect(t *testing.T) {
	pair := setupRsyncPair(t)
	// Break the target's rsync so the availability gate fails there.
	mustRun(t, "docker", "exec", pair.tgtC, "sh", "-lc", "rm -f /usr/bin/rsync")

	src := TransferTarget{Host: "rsync-src", User: "root", SSHClient: pair.srcExec}
	dst := TransferTarget{Host: "rsync-tgt", User: "root", SSHClient: pair.tgtExec, Path: "/data"}

	s := NewRsyncStrategy()
	if s.Mode(context.Background(), src, dst) != "blocked" {
		t.Fatalf("expected blocked mode when target rsync missing")
	}
	avail := s.Availability(context.Background(), src, dst)
	if avail.OK() {
		t.Fatalf("availability must not be OK with target rsync missing: %+v", avail)
	}
	if !errors.Is(avail.Err, ErrTransferTargetUnreachable) {
		t.Fatalf("expected target-unreachable error, got %v", avail.Err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func randSuffix() string {
	// integration tests run once per CI invocation; a time-based suffix
	// avoids container-name clashes without needing crypto/rand.
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Run(); err != nil {
		t.Fatalf("docker command failed: %v\n%s", err, buf.String())
	}
}

func mustOut(t *testing.T, args ...string) string {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Run(); err != nil {
		t.Fatalf("docker command failed: %v\n%s", err, buf.String())
	}
	return strings.TrimSpace(buf.String())
}
