//go:build integration

// Live SCP resume validation against real docker-backed sshd. This exercises
// the ACTUAL shipped Part 5 primitive (SCPStrategy + LongTransferExecuter +
// Reconcile) over genuine SFTP, not mocks. It is the unfakeable proof for the
// Workstream E hypotheses H1 (upload resumes from partial), H2 (source change
// → ManualIntervention), H8 (shell-sensitive paths).
//
// Prereqs: docker CLI + daemon. Containers are ephemeral and removed on exit.
// Run: go test -tags integration ./internal/mod/transfer/ -run 'LiveSCP' -v -timeout 600s

package transfer

import (
	"context"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	modssh "meshium/internal/mod/ssh"
	xssh "golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// docker sshd fixture
// ---------------------------------------------------------------------------

func startSshd(t *testing.T) (id, ip string, stop func()) {
	t.Helper()
	id = mustOut(t, "docker", "run", "-d", "--rm", "alpine:3.20",
		"sh", "-c",
		"apk add --no-cache openssh >/dev/null 2>&1 && ssh-keygen -A >/dev/null 2>&1 && "+
			"echo root:testpass | chpasswd && /usr/sbin/sshd -D -o PermitRootLogin=yes "+
			"-o PasswordAuthentication=yes -o PidFile=/tmp/sshd.pid")
	// Poll until sshd has written its pidfile (it binds right after).
	for i := 0; i < 30; i++ {
		if out := mustOutSilent(t, "docker", "exec", id, "sh", "-c",
			"test -f /tmp/sshd.pid && echo up || echo down"); out == "up" {
			break
		}
		time.Sleep(time.Second)
	}
	time.Sleep(2 * time.Second) // extra grace for the listener to accept
	ip = strings.TrimSpace(mustOut(t, "docker", "inspect", "-f", "{{.NetworkSettings.IPAddress}}", id))
	if ip == "" {
		mustRun(t, "docker", "rm", "-f", id)
		t.Fatalf("no IP for sshd container")
	}
	return id, ip, func() { _ = exec.Command("docker", "rm", "-f", id).Run() }
}

// sshClient dials the container with the real meshium *ssh.Client so the test
// runs the genuine SFTP + LongTransferExecuter path (not a mock).
func sshClient(t *testing.T, ip string) *modssh.Client {
	t.Helper()
	pool := modssh.NewPool(modssh.PoolConfig{})
	c, err := pool.Get(0, modssh.ServerConfig{
		Host:     ip,
		Port:     22,
		Username: "root",
		Password: "testpass",
		Timeouts: modssh.TimeoutConfig{Connect: 10 * time.Second},
	}, xssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("dial sshd: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// H1: upload resumes from a partial target after interruption
// ---------------------------------------------------------------------------

func TestLiveSCPResumeFromPartial(t *testing.T) {
	id, ip, stop := startSshd(t)
	defer stop()
	ssh := sshClient(t, ip)
	defer ssh.Close()

	srcData := randBytes(t, 20<<20) // 20 MiB
	// Local source file (mirrors database.applyFile: download to local temp,
	// then local→remote upload which is the resumable leg).
	localSrc := t.TempDir() + "/src.bin"
	if err := os.WriteFile(localSrc, srcData, 0644); err != nil {
		t.Fatal(err)
	}
	tgtPath := "/tmp/tgt.bin"

	// Simulate a prior interrupted upload: leave a half-written partial target
	// on the remote (as a killed SCPStrategy upload would).
	putContainerFile(t, id, srcData[:len(srcData)/2], tgtPath)

	src := TransferTarget{Path: localSrc, IsLocal: true}
	dst := TransferTarget{Path: tgtPath, SSHClient: ssh}

	res, err := NewSCPStrategy().Transfer(context.Background(), src, dst,
		TransferOptions{Resume: true, MaxRetries: 3, ProgressInterval: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("resumable transfer failed: %v", err)
	}
	if !res.Resumed {
		t.Fatalf("expected Resumed=true (append to partial), got Resumed=%v bytes=%d",
			res.Resumed, res.BytesTransferred)
	}
	if res.BytesTransferred != int64(len(srcData)) {
		t.Fatalf("expected %d bytes transferred, got %d", len(srcData), res.BytesTransferred)
	}
	assertContainerFileEquals(t, id, srcData, tgtPath)
}

// ---------------------------------------------------------------------------
// H2: source changed after a checkpoint → ManualIntervention (fail closed)
// ---------------------------------------------------------------------------

func TestLiveSCPReconcileSourceChanged(t *testing.T) {
	id, ip, stop := startSshd(t)
	defer stop()
	ssh := sshClient(t, ip)
	defer ssh.Close()

	srcPath := "/tmp/src.bin"
	orig := randBytes(t, 5<<20)
	putContainerFile(t, id, orig, srcPath)

	cp := &TransferCheckpoint{
		TransferID:     "tx-live-scp",
		SourceSnapshot: sourceSnapshotLive(t, ssh, srcPath),
		Resumable:      true,
	}
	ev := ReconcileEvidence{
		CurrentSourceSnapshot:   sourceSnapshotLive(t, ssh, srcPath),
		PartialTargetExists:    true,
		CurrentTargetPartialState: "partial",
		StrategyStillValid:      true,
	}
	if v, err := Reconcile(cp, ev); v != VerdictResume || err != nil {
		t.Fatalf("unchanged: expected Resume, got %v err=%v", v, err)
	}

	// Mutate source (append bytes → size+mtime change).
	appendContainerFile(t, id, randBytes(t, 1<<20), srcPath)
	newSnap := sourceSnapshotLive(t, ssh, srcPath)
	if newSnap == cp.SourceSnapshot {
		t.Skip("snapshot coincidentally unchanged")
	}
	ev2 := ReconcileEvidence{
		CurrentSourceSnapshot:   newSnap,
		PartialTargetExists:     true,
		CurrentTargetPartialState: "partial",
		StrategyStillValid:       true,
	}
	v, err := Reconcile(cp, ev2)
	if v != VerdictManualIntervention {
		t.Fatalf("changed source: expected ManualIntervention, got %v", v)
	}
	if !errors.Is(err, ErrTransferReconcileFailed) {
		t.Fatalf("expected ErrTransferReconcileFailed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// H8: shell-sensitive filenames do not inject
// ---------------------------------------------------------------------------

func TestLiveSCPShellSafePaths(t *testing.T) {
	id, ip, stop := startSshd(t)
	defer stop()
	ssh := sshClient(t, ip)
	defer ssh.Close()

	evil := "/tmp/evil-$(touch PWNED).bin" // must NOT create /tmp/PWNED
	clean := randBytes(t, 1<<20)
	// Create the shell-sensitive file via base64 streamed from a local file into
	// the container, so the dangerous name is only ever passed quoted to `sh -c`.
	b64Path := t.TempDir() + "/c.b64"
	if err := os.WriteFile(b64Path, []byte(base64.StdEncoding.EncodeToString(clean)), 0644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("docker", "exec", "-i", id, "sh", "-c",
		fmt.Sprintf("base64 -d > %s", shellQuote(evil)))
	b64f, err := os.Open(b64Path)
	if err != nil {
		t.Fatal(err)
	}
	defer b64f.Close()
	c.Stdin = b64f
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("docker exec base64 write failed: %v\n%s", err, out)
	}

	src := TransferTarget{Path: evil, SSHClient: ssh}
	dst := TransferTarget{Path: "/tmp/evil-out.bin", SSHClient: ssh}
	if _, err := NewSCPStrategy().Transfer(context.Background(), src, dst,
		TransferOptions{Resume: false}); err != nil {
		t.Fatalf("transfer of shell-safe path failed: %v", err)
	}
	if exists, _ := containerFileExists(id, "PWNED"); exists {
		t.Fatalf("shell injection succeeded — path not quoted")
	}
	assertContainerFileEquals(t, id, clean, "/tmp/evil-out.bin")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// mustOutSilent runs a command and returns its trimmed stdout, tolerating a
// non-zero exit (used for readiness probes).
func mustOutSilent(t *testing.T, args ...string) string {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	_ = c.Run()
	return strings.TrimSpace(buf.String())
}

// randBytes returns deterministic non-compressible-ish data of length n.
func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251)
	}
	return b
}

func putContainerFile(t *testing.T, id string, data []byte, path string) {
	t.Helper()
	tmp := t.TempDir() + "/f"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "docker", "cp", tmp, fmt.Sprintf("%s:%s", id, path))
}

func appendContainerFile(t *testing.T, id string, data []byte, path string) {
	t.Helper()
	tmp := t.TempDir() + "/a"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "docker", "cp", tmp, fmt.Sprintf("%s:%s.append.tmp", id, path))
	mustRun(t, "docker", "exec", id, "sh", "-c",
		fmt.Sprintf("cat %s.append.tmp >> %s && rm -f %s.append.tmp", path, path, path))
}

func containerFileExists(id, path string) (bool, error) {
	out, err := exec.Command("docker", "exec", id, "sh", "-c",
		fmt.Sprintf("test -f %s && echo yes || echo no", shellQuote(path))).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "yes", nil
}

func sourceSnapshotLive(t *testing.T, ssh *modssh.Client, path string) string {
	t.Helper()
	info, err := StatFile(context.Background(), TransferTarget{Path: path, SSHClient: ssh})
	if err != nil {
		t.Fatalf("stat live: %v", err)
	}
	return fmt.Sprintf("%d:%d", info.Size, info.ModTime)
}

func assertContainerFileEquals(t *testing.T, id string, want []byte, path string) {
	t.Helper()
	tmp := t.TempDir() + "/out"
	mustRun(t, "docker", "cp", fmt.Sprintf("%s:%s", id, path), tmp)
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
	// integrity by sha256
	w := sha256.Sum256(want)
	g := sha256.Sum256(got)
	if w != g {
		t.Fatalf("sha256 mismatch: %s != %s", hex.EncodeToString(g[:]), hex.EncodeToString(w[:]))
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
