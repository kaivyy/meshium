package migration

import (
	"context"
	"errors"
	"io"
	"testing"
)

// fakeModeSSH returns a fixed output for a single probed command.
type fakeModeSSH struct {
	out   string
	rc    int
	err   error
	cmds  []string
}

func (f *fakeModeSSH) Exec(cmd string) (string, string, int, error) {
	return f.ExecContext(context.Background(), cmd)
}
func (f *fakeModeSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	f.cmds = append(f.cmds, cmd)
	return f.out, "", f.rc, f.err
}
func (fakeModeSSH) IsAlive() bool { return true }
func (fakeModeSSH) Upload(io.Reader, string) error  { return errors.New("unsupported") }
func (fakeModeSSH) Download(string, io.Writer) error { return errors.New("unsupported") }

func TestClassifyExecutionMode(t *testing.T) {
	cases := map[string]ExecutionMode{
		"host":     ModeHost,
		"HOST":     ModeHost,
		" container ": ModeContainer,
		"compose":  ModeCompose,
		"BASTION":  ModeBastion,
		"kubernetes": ModeUnknown,
		"":         ModeUnknown,
	}
	for in, want := range cases {
		if got := classifyExecutionMode(in); got != want {
			t.Fatalf("classifyExecutionMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCertifyTopologyBlocksUnknownMode(t *testing.T) {
	ssh := &fakeModeSSH{out: "n/a", rc: 0}
	ev, err := CertifyTopology(context.Background(), ssh, "kubernetes", "src", "tgt", "", "")
	if !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("unknown mode must fail closed: %v", err)
	}
	if ev.SupportStatus != "blocked" {
		t.Fatalf("unknown mode must be blocked, got %q", ev.SupportStatus)
	}
	// No probe should have been issued against an un-certifiable mode.
	if len(ssh.cmds) != 0 {
		t.Fatalf("unsupported mode must not probe transport: %v", ssh.cmds)
	}
}

func TestCertifyTopologyHostOK(t *testing.T) {
	ssh := &fakeModeSSH{out: "OpenSSH_9.0", rc: 0}
	ev, err := CertifyTopology(context.Background(), ssh, "host", "10.0.0.1", "10.0.0.2", "", "")
	if err != nil {
		t.Fatalf("host mode should certify: %v", err)
	}
	if ev.Mode != ModeHost || !ev.ToolAvailable || ev.Connectivity != "ok" {
		t.Fatalf("unexpected evidence: %+v", ev)
	}
	if ev.SupportStatus != "automatic" {
		t.Fatalf("host should be automatic, got %q", ev.SupportStatus)
	}
}

func TestCertifyTopologyComposeOK(t *testing.T) {
	ssh := &fakeModeSSH{out: "Docker Compose version v2.20.0", rc: 0}
	ev, err := CertifyTopology(context.Background(), ssh, "compose", "pg-src", "pg-tgt", "meshium", "")
	if err != nil {
		t.Fatalf("compose mode should certify: %v", err)
	}
	if ev.Mode != ModeCompose || ev.ComposeProject != "meshium" {
		t.Fatalf("compose evidence wrong: %+v", ev)
	}
}

func TestCertifyTopologyBlocksMissingTool(t *testing.T) {
	// Tool probe fails (rc=1) → mode blocked, fail closed.
	ssh := &fakeModeSSH{out: "", rc: 1, err: errors.New("docker: command not found")}
	ev, err := CertifyTopology(context.Background(), ssh, "container", "c1", "c2", "", "")
	if !errors.Is(err, ErrUnsupportedMode) {
		t.Fatalf("missing docker must fail closed: %v", err)
	}
	if ev.SupportStatus != "blocked" || ev.Connectivity == "" {
		t.Fatalf("blocked evidence incomplete: %+v", ev)
	}
}
