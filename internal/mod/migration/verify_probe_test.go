package migration

import (
	"context"
	"strings"
	"testing"
)

// Verification depth (VPS-B5). Before this, health_verification proved only
// that the TARGET WAS REACHABLE (`echo ok`) and then attested every applied
// item as infra_verified. Nothing checked that the package was actually
// installed, the config actually matched, or the service was actually running
// — so "verified" carried barely more information than "applied".
//
// probeItems runs one batched probe per category against the target and
// returns a per-item result, so each item earns its own level.

func TestProbePackagesReportsPerItemPresence(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	// nginx installed, redis absent.
	ssh.addOutput("dpkg-query", "ii  nginx\n")

	items := []ItemResult{
		{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecApplied},
		{ItemKey: "package:redis", Category: "packages", ExecutionState: ExecApplied},
	}

	got := probeItems(context.Background(), ssh, "packages", items)

	if v := got["package:nginx"]; !v.OK {
		t.Errorf("nginx should verify present: %+v", v)
	}
	if v := got["package:redis"]; v.OK {
		t.Errorf("redis is absent on the target but verified OK: %+v", v)
	}
	if v := got["package:redis"]; !strings.Contains(v.Detail, "not installed") {
		t.Errorf("redis failure should say why, got %q", v.Detail)
	}
}

// A package probe that cannot run at all must not silently mark everything
// verified — it must mark nothing OK.
func TestProbePackagesFailsClosedWhenProbeFails(t *testing.T) {
	ssh := newMockSSH()
	ssh.execOutput["cat /etc/os-release"] = "ID=ubuntu\nVERSION_ID=22.04"
	ssh.addOutput("dpkg-query", "")
	ssh.execExit["dpkg-query"] = 127

	items := []ItemResult{{ItemKey: "package:nginx", Category: "packages", ExecutionState: ExecApplied}}
	got := probeItems(context.Background(), ssh, "packages", items)

	if v := got["package:nginx"]; v.OK {
		t.Error("probe command failed but the item verified OK — fail closed")
	}
}

// Configs verify by content hash, which is the only evidence that the file on
// the target is the file the migration intended to write.
func TestProbeConfigsComparesContentHash(t *testing.T) {
	ssh := newMockSSH()
	// sha256 of "hello\n"
	const helloSum = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	ssh.addOutput("sha256sum", helloSum+"  /etc/app.conf\n")

	items := []ItemResult{
		{ItemKey: "config:/etc/app.conf", Category: "configs", ExecutionState: ExecApplied,
			VerifyEvidence: "sha256:" + helloSum},
		{ItemKey: "config:/etc/other.conf", Category: "configs", ExecutionState: ExecApplied,
			VerifyEvidence: "sha256:deadbeef"},
	}

	got := probeItems(context.Background(), ssh, "configs", items)

	if v := got["config:/etc/app.conf"]; !v.OK {
		t.Errorf("matching hash should verify: %+v", v)
	}
	if v := got["config:/etc/other.conf"]; v.OK {
		t.Errorf("absent/mismatched file verified OK: %+v", v)
	}
}

// Services verify as RUNTIME: is-active is the difference between "the unit
// file was written" and "the service is actually running".
func TestProbeServicesUsesIsActive(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("is-active", "active\ninactive\n")

	items := []ItemResult{
		{ItemKey: "service:nginx", Category: "services", ExecutionState: ExecApplied},
		{ItemKey: "service:redis", Category: "services", ExecutionState: ExecApplied},
	}

	got := probeItems(context.Background(), ssh, "services", items)

	if v := got["service:nginx"]; !v.OK || v.Level != VerifyRuntime {
		t.Errorf("active service should be runtime-verified: %+v", v)
	}
	if v := got["service:redis"]; v.OK {
		t.Errorf("inactive service verified OK: %+v", v)
	}
}

func TestProbeUsersChecksAccountExists(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("getent passwd", "deploy:x:1000:1000::/home/deploy:/bin/bash\n")

	items := []ItemResult{
		{ItemKey: "user:deploy", Category: "users", ExecutionState: ExecApplied},
		{ItemKey: "user:ghost", Category: "users", ExecutionState: ExecApplied},
	}

	got := probeItems(context.Background(), ssh, "users", items)
	if v := got["user:deploy"]; !v.OK {
		t.Errorf("existing user should verify: %+v", v)
	}
	if v := got["user:ghost"]; v.OK {
		t.Errorf("absent user verified OK: %+v", v)
	}
}

// An unknown category must return no verdicts rather than inventing one.
func TestProbeUnknownCategoryReturnsNothing(t *testing.T) {
	ssh := newMockSSH()
	items := []ItemResult{{ItemKey: "database:app", Category: "database", ExecutionState: ExecApplied}}
	if got := probeItems(context.Background(), ssh, "database", items); len(got) != 0 {
		t.Errorf("unknown category should yield no verdicts, got %v", got)
	}
}
