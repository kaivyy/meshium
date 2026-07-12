package transfer

import (
	"context"
	"testing"
)

func TestClassifyVolumePath(t *testing.T) {
	cases := []struct {
		path string
		want VolumeKind
	}{
		{"data", VolumeNamed},                                 // bare token = docker named volume
		{"pg_data-1", VolumeNamed},                           // bare token
		{"/var/lib/docker/volumes/data/_data", VolumeNamed},   // materialized named
		{"/srv/app/data", VolumeBind},                         // host/bind mount
		{"/etc/nginx", VolumeBind},
		{"", VolumeUnknown},
		{"relative/path", VolumeUnknown}, // no leading slash
	}
	for _, c := range cases {
		if got := ClassifyVolumePath(c.path); got != c.want {
			t.Fatalf("path=%q: got %q want %q", c.path, got, c.want)
		}
	}
}

func TestShouldCompress(t *testing.T) {
	if !ShouldCompress("configs", "/etc/nginx/nginx.conf") {
		t.Fatal("configs text should compress")
	}
	if ShouldCompress("docker-volume", "/var/lib/docker/volumes/pg/_data") {
		t.Fatal("docker-volume should NOT default-compress (binary/DB risk)")
	}
	if ShouldCompress("files", "/var/lib/app/blob.bin") {
		t.Fatal("unknown binary extension should not compress")
	}
	if !ShouldCompress("files", "/data/readme.md") {
		t.Fatal("markdown should compress")
	}
}

func TestPlanDockerVolumeDirectWhenRsyncOK(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH()
	withRsync(src)
	withRsync(dst)
	withReachability(src)

	plan := PlanDockerVolume(context.Background(), "docker-volume",
		"/var/lib/docker/volumes/data/_data",
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		false, // degraded not needed when direct available
	)
	if !plan.Direct {
		t.Fatalf("expected direct rsync plan, got %+v", plan)
	}
	if plan.Degraded {
		t.Fatal("direct plan must not be flagged degraded")
	}
}

func TestPlanDockerVolumeDegradedPermitted(t *testing.T) {
	src := newMockSSH() // rsync absent → direct unavailable
	dst := newMockSSH()
	withReachability(src)

	plan := PlanDockerVolume(context.Background(), "docker-volume",
		"/var/lib/docker/volumes/data/_data",
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		true, // degraded allowed
	)
	if !plan.Degraded {
		t.Fatalf("expected degraded plan when rsync absent, got %+v", plan)
	}
	if plan.Mode != "degraded" {
		t.Fatalf("mode=%q want degraded", plan.Mode)
	}
}

func TestPlanDockerVolumeDegradedDeniedFailsClosed(t *testing.T) {
	src := newMockSSH() // rsync absent
	dst := newMockSSH()
	withReachability(src)

	plan := PlanDockerVolume(context.Background(), "docker-volume",
		"/var/lib/docker/volumes/data/_data",
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		false, // degraded NOT allowed
	)
	if plan.Direct || plan.Degraded {
		t.Fatalf("neither direct nor degraded must be set when denied, got %+v", plan)
	}
}
