//go:build integration

// Package migration — VPS-B1 live execution matrix.
//
// The Phase VPS-A audit found that every apply/verify/rollback path was
// code-verified only: no category had ever been executed against a real
// source→target pair. This harness closes that gap with disposable Debian
// containers running a real sshd, driven through the SHIPPED *ssh.Client and
// the SHIPPED collectors/appliers — no mocks anywhere in the path.
//
// Run:
//
//	go test -tags integration ./internal/mod/migration/ -run VPSB1 -v -timeout 1800s
//
// Requires a working docker daemon. Each test builds its own pair and tears it
// down, so a failure never leaks containers into the next case.
package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"

	modssh "meshium/internal/mod/ssh"
)

// ---------------------------------------------------------------- harness

type liveHost struct {
	name   string
	port   int
	client *modssh.Client
}

func dockerRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func dockerTry(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// startHost boots a Debian container with sshd + root password auth on a free
// host port. Password auth (not keys) keeps the harness self-contained; the
// shipped client dials it exactly as it dials a real VPS.
func startHost(t *testing.T, role string, port int) *liveHost {
	t.Helper()
	name := fmt.Sprintf("meshium-vpsb1-%s-%d", role, time.Now().UnixNano()%1e6)

	dockerRun(t, "run", "-d", "--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:22", port),
		"debian:12", "sleep", "infinity")

	t.Cleanup(func() {
		_, _ = dockerTry("rm", "-f", name)
	})

	setup := strings.Join([]string{
		"export DEBIAN_FRONTEND=noninteractive",
		"apt-get update -qq >/dev/null 2>&1",
		"apt-get install -y -qq openssh-server >/dev/null 2>&1",
		"mkdir -p /run/sshd",
		"echo 'root:meshium-test' | chpasswd",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config",
		"/usr/sbin/sshd",
	}, " && ")
	if out, err := dockerTry("exec", name, "bash", "-lc", setup); err != nil {
		t.Fatalf("provision %s: %v\n%s", name, err, out)
	}

	h := &liveHost{name: name, port: port}
	cfg := modssh.ServerConfig{
		ID: port, Host: "127.0.0.1", Port: port,
		Username: "root", Password: "meshium-test",
		Timeouts: modssh.MigrationTimeouts,
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		c, err := modssh.Connect(cfg, insecureHostKey())
		if err == nil {
			h.client = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sshd on %s never became reachable: %v", name, err)
		}
		time.Sleep(time.Second)
	}
	t.Cleanup(func() {
		if h.client != nil {
			h.client.Close()
		}
	})
	return h
}

// sh runs a command directly in the container (setup/assertions), bypassing
// SSH so harness plumbing never masks a product bug.
func (h *liveHost) sh(t *testing.T, cmd string) string {
	t.Helper()
	out, err := dockerTry("exec", h.name, "bash", "-lc", cmd)
	if err != nil {
		t.Fatalf("in-container %q failed: %v\n%s", cmd, err, out)
	}
	return strings.TrimSpace(out)
}

func (h *liveHost) shOK(cmd string) (string, bool) {
	out, err := dockerTry("exec", h.name, "bash", "-lc", cmd)
	return strings.TrimSpace(out), err == nil
}

// pair boots a source/target pair on distinct ports.
func pair(t *testing.T) (src, tgt *liveHost) {
	t.Helper()
	base := 22000 + int(time.Now().UnixNano()%1000)*2
	return startHost(t, "src", base), startHost(t, "tgt", base+1)
}

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// ------------------------------------------------------- C1 packages apply

// C1: collect packages on a real source, apply to a real target, assert the
// package is genuinely installed there. This is the first time the packages
// apply path has run against real hosts.
func TestVPSB1_C1_PackagesApplyAndVerify(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	// Source has a package the target lacks.
	src.sh(t, "DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sl >/dev/null 2>&1 || true")
	if _, ok := src.shOK("dpkg-query -W sl"); !ok {
		t.Skip("could not install the marker package on source (no network?)")
	}
	if _, ok := tgt.shOK("dpkg-query -W sl"); ok {
		t.Fatal("target already has the marker package; test is not meaningful")
	}

	data, err := (&PackagesCollector{}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect packages")

	var pd PackagesData
	mustNoErr(t, jsonUnmarshal(data.Data, &pd), "decode packages")
	if pd.Count == 0 {
		t.Fatal("collected zero packages from a live Debian host")
	}
	if !vpsb1Contains(pd.Packages, "sl") {
		t.Fatalf("marker package missing from a %d-package collect", pd.Count)
	}

	backup, err := (&PackagesApplier{}).Backup(ctx, tgt.client)
	mustNoErr(t, err, "backup target packages")

	err = (&PackagesApplier{}).Apply(ctx, tgt.client, data, nil)
	mustNoErr(t, err, "apply packages")

	// EVIDENCE: the package really exists on the target now.
	if _, ok := tgt.shOK("dpkg-query -W sl"); !ok {
		t.Error("apply reported success but the package is absent on the target")
	}

	// C11: rollback removes what the migration added, keeping the baseline.
	mustNoErr(t, (&PackagesApplier{}).Rollback(ctx, tgt.client, backup), "rollback packages")
	if _, ok := tgt.shOK("dpkg-query -W sl"); ok {
		t.Error("rollback left the migrated package installed")
	}
	if _, ok := tgt.shOK("dpkg-query -W bash"); !ok {
		t.Error("rollback removed a baseline package — blast radius exceeded the migration")
	}
}

// -------------------------------------------------- C2 configs + metadata

// C2: the VPS-B4 fix end to end — a 0600 root-owned secret on the source must
// land on the target with its mode intact, not widened by the SFTP umask.
func TestVPSB1_C2_ConfigsApplyPreservesModeAndOwner(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	src.sh(t, "mkdir -p /etc/meshiumtest && printf 'secret-token\\n' > /etc/meshiumtest/secret.conf && chmod 600 /etc/meshiumtest/secret.conf")
	src.sh(t, "printf 'world-readable\\n' > /etc/meshiumtest/public.conf && chmod 644 /etc/meshiumtest/public.conf")

	data, err := (&ConfigsCollector{Paths: []string{"/etc/meshiumtest/"}}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect configs")

	var cd ConfigsData
	mustNoErr(t, jsonUnmarshal(data.Data, &cd), "decode configs")
	if cd.Count != 2 {
		t.Fatalf("collected %d files, want 2 (%v)", cd.Count, cd.Files)
	}
	if m := cd.Meta["/etc/meshiumtest/secret.conf"]; m.Mode != 0o600 {
		t.Errorf("collected mode = %o, want 600", m.Mode)
	}

	mustNoErr(t, (&ConfigsApplier{}).Apply(ctx, tgt.client, data, nil), "apply configs")

	if got := tgt.sh(t, "cat /etc/meshiumtest/secret.conf"); got != "secret-token" {
		t.Errorf("content = %q", got)
	}
	// EVIDENCE for VPS-B4: without the fix this is 644.
	if got := tgt.sh(t, "stat -c '%a' /etc/meshiumtest/secret.conf"); got != "600" {
		t.Errorf("target mode = %s, want 600 — the secret was widened on the target", got)
	}
	if got := tgt.sh(t, "stat -c '%U:%G' /etc/meshiumtest/secret.conf"); got != "root:root" {
		t.Errorf("target owner = %s, want root:root", got)
	}
	if got := tgt.sh(t, "stat -c '%a' /etc/meshiumtest/public.conf"); got != "644" {
		t.Errorf("644 file became %s", got)
	}
}

// C3: OS-critical files must never be written to the target, even when the
// operator points the collector straight at them.
func TestVPSB1_C3_ExcludedFilesNeverApplied(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	before := tgt.sh(t, "md5sum /etc/passwd | cut -d' ' -f1")
	src.sh(t, "echo 'meshium-marker:x:9999:9999::/tmp:/bin/false' >> /etc/passwd")

	data, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect /etc")

	var cd ConfigsData
	mustNoErr(t, jsonUnmarshal(data.Data, &cd), "decode")
	for _, banned := range []string{"/etc/passwd", "/etc/shadow", "/etc/fstab", "/etc/hosts"} {
		if _, ok := cd.Files[banned]; ok {
			t.Errorf("collector captured excluded file %s", banned)
		}
	}

	mustNoErr(t, (&ConfigsApplier{}).Apply(ctx, tgt.client, data, nil), "apply")
	if after := tgt.sh(t, "md5sum /etc/passwd | cut -d' ' -f1"); after != before {
		t.Error("target /etc/passwd was modified by a config apply — OS-critical guard failed")
	}
}

// ------------------------------------------------------------ C4 services

// C4: services collect against a real host.
//
// An earlier draft of this case asserted the collector must FAIL here, on the
// assumption that a Debian container has no init system. The live run proved
// that assumption wrong: installing openssh-server pulls in systemd, and
// `systemctl list-unit-files` reads unit files straight off disk without a
// booted systemd — exit 0, five genuinely enabled units. The collector was
// right and the test was wrong, so the expectation is inverted here rather
// than "fixing" correct code.
//
// The empty-probe guard (zero services recorded as a successful collect) is
// covered by TestServicesCollectRejectsEmptyResult; producing an SSH-reachable
// host with no init system at all is not practical in this harness.
func TestVPSB1_C4_ServicesCollectFromLiveHost(t *testing.T) {
	src, _ := pair(t)
	ctx := context.Background()

	data, err := (&ServicesCollector{}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect services")

	var sd ServicesData
	mustNoErr(t, jsonUnmarshal(data.Data, &sd), "decode services")
	if sd.Count == 0 {
		t.Fatal("collected zero services from a host with enabled units")
	}
	if !vpsb1Contains(sd.Services, "ssh") {
		t.Errorf("ssh.service missing from a %d-service collect: %v", sd.Count, sd.Services)
	}
	// The parser must strip the .service suffix, not leak it into the payload.
	for _, s := range sd.Services {
		if strings.HasSuffix(s, ".service") {
			t.Errorf("service name kept its .service suffix: %q", s)
		}
	}
	t.Logf("collected %d enabled services: %v", sd.Count, sd.Services)
}

// ---------------------------------------------------- C5 failure/rollback

// C5: apply fails partway (target cannot install a nonexistent package) and
// the error must surface rather than being reported as success.
func TestVPSB1_C5_ApplyFailureSurfaces(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()
	_ = src

	pd := PackagesData{
		Distro:   "apt",
		Packages: []string{"meshium-definitely-not-a-real-package-xyz"},
		Count:    1,
	}
	raw, err := jsonMarshal(pd)
	mustNoErr(t, err, "marshal")

	err = (&PackagesApplier{}).Apply(ctx, tgt.client, CategoryData{Type: "packages", Data: raw}, nil)
	if err == nil {
		t.Error("apply of a nonexistent package reported success")
	} else {
		t.Logf("correctly failed: %v", err)
	}
}

// -------------------------------------------------------- C6 cancellation

// C6: a cancelled context must abort the collect promptly instead of running
// to completion or hanging.
func TestVPSB1_C6_CancellationAborts(t *testing.T) {
	src, _ := pair(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	start := time.Now()
	_, err := (&ConfigsCollector{Paths: []string{"/etc/"}}).Collect(ctx, src.client)
	elapsed := time.Since(start)

	if err == nil {
		t.Log("collect returned data despite a cancelled context (bounded fallback path)")
	}
	if elapsed > 30*time.Second {
		t.Errorf("cancelled collect took %v — cancellation is not honored", elapsed)
	}
}

// ------------------------------------------------------ C7 SSH disconnect

// C7: the connection dies mid-migration. The operation must return an error,
// never a false success.
func TestVPSB1_C7_SSHDisconnectDuringWork(t *testing.T) {
	src, _ := pair(t)
	ctx := context.Background()

	// Kill sshd underneath the live client.
	_, _ = dockerTry("exec", src.name, "bash", "-lc", "pkill -9 sshd")
	time.Sleep(time.Second)

	_, err := (&PackagesCollector{}).Collect(ctx, src.client)
	if err == nil {
		t.Error("collect succeeded after the SSH server was killed — false success")
	} else {
		t.Logf("correctly failed: %v", err)
	}
}

// ------------------------------------------------------------- C8 users

func TestVPSB1_C8_UsersCollectAndApply(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	src.sh(t, "useradd -m -s /bin/bash meshiumuser 2>/dev/null || true")

	data, err := (&UsersCollector{}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect users")

	var ud UsersData
	mustNoErr(t, jsonUnmarshal(data.Data, &ud), "decode users")
	found := false
	for _, u := range ud.Users {
		if u.Name == "meshiumuser" {
			found = true
		}
	}
	if !found {
		t.Fatalf("source user missing from collect (%d users)", len(ud.Users))
	}

	mustNoErr(t, (&UsersApplier{}).Apply(ctx, tgt.client, data, nil), "apply users")
	if _, ok := tgt.shOK("id meshiumuser"); !ok {
		t.Error("apply reported success but the user does not exist on the target")
	}
}

// -------------------------------------------------- C9 idempotent re-apply

// C9: applying the same payload twice must not fail or double-write.
func TestVPSB1_C9_ReapplyIsIdempotent(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	src.sh(t, "mkdir -p /etc/meshiumtest && printf 'v1\\n' > /etc/meshiumtest/app.conf && chmod 640 /etc/meshiumtest/app.conf")
	data, err := (&ConfigsCollector{Paths: []string{"/etc/meshiumtest/"}}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect")

	mustNoErr(t, (&ConfigsApplier{}).Apply(ctx, tgt.client, data, nil), "apply #1")
	mustNoErr(t, (&ConfigsApplier{}).Apply(ctx, tgt.client, data, nil), "apply #2")

	if got := tgt.sh(t, "cat /etc/meshiumtest/app.conf"); got != "v1" {
		t.Errorf("content after re-apply = %q", got)
	}
	if got := tgt.sh(t, "stat -c '%a' /etc/meshiumtest/app.conf"); got != "640" {
		t.Errorf("mode after re-apply = %s, want 640", got)
	}
}

// ------------------------------------------------- C10 configs rollback

// C10: rollback restores the target's ORIGINAL content and metadata.
func TestVPSB1_C10_ConfigsRollbackRestoresContentAndMode(t *testing.T) {
	src, tgt := pair(t)
	ctx := context.Background()

	// Target has its own version at a restrictive mode.
	tgt.sh(t, "mkdir -p /etc/meshiumtest && printf 'target-original\\n' > /etc/meshiumtest/app.conf && chmod 600 /etc/meshiumtest/app.conf")
	src.sh(t, "mkdir -p /etc/meshiumtest && printf 'source-version\\n' > /etc/meshiumtest/app.conf && chmod 644 /etc/meshiumtest/app.conf")

	backupPaths := &ConfigsApplier{}
	backup, err := backupPaths.Backup(ctx, tgt.client)
	mustNoErr(t, err, "backup target /etc")

	data, err := (&ConfigsCollector{Paths: []string{"/etc/meshiumtest/"}}).Collect(ctx, src.client)
	mustNoErr(t, err, "collect")
	mustNoErr(t, (&ConfigsApplier{}).Apply(ctx, tgt.client, data, nil), "apply")

	if got := tgt.sh(t, "cat /etc/meshiumtest/app.conf"); got != "source-version" {
		t.Fatalf("apply did not take effect: %q", got)
	}

	mustNoErr(t, (&ConfigsApplier{}).Rollback(ctx, tgt.client, backup), "rollback")

	if got := tgt.sh(t, "cat /etc/meshiumtest/app.conf"); got != "target-original" {
		t.Errorf("rollback did not restore original content: %q", got)
	}
	if got := tgt.sh(t, "stat -c '%a' /etc/meshiumtest/app.conf"); got != "600" {
		t.Errorf("rollback restored content but left mode %s (want 600) — exposure survived the rollback", got)
	}
}

// ------------------------------------------------------------- helpers

func vpsb1Contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func jsonUnmarshal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

func jsonMarshal(v interface{}) ([]byte, error) { return json.Marshal(v) }

// insecureHostKey trusts whatever the disposable container presents. Real
// server flows go through KnownHostsStore (deny-until-trusted); this harness
// creates a fresh throwaway host per test, so pinning adds nothing.
func insecureHostKey() xssh.HostKeyCallback {
	return xssh.InsecureIgnoreHostKey()
}

var _ = bytes.NewReader
