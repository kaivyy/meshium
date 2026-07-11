package planner

import (
	"context"
	"io"
	"strings"
	"testing"

	"meshium/internal/mod/migration"
)

// stubSSH is a minimal transport.SSHExecuter recording issued commands.
type stubSSH struct {
	commands []string
}

func (s *stubSSH) Exec(cmd string) (string, string, int, error) {
	s.commands = append(s.commands, cmd)
	return "", "", 0, nil
}
func (s *stubSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	s.commands = append(s.commands, cmd)
	return "", "", 0, nil
}
func (s *stubSSH) IsAlive() bool                          { return true }
func (s *stubSSH) Upload(src io.Reader, remotePath string) error { return nil }
func (s *stubSSH) Download(remotePath string, dst io.Writer) error { return nil }

// P0-10: hostile volume/container names must be rejected before any shell
// interpolation, and clean names must pass.
func TestValidateVolNameRejectsInjection(t *testing.T) {
	bad := []string{"foo; rm -rf /", "foo$(reboot)", "foo`x`", "foo|x", "foo && echo", "  ", ""}
	for _, name := range bad {
		if err := validateVolName(name); err == nil {
			t.Fatalf("expected rejection for %q", name)
		}
	}
	good := []string{"data", "pg_data-1", "vol.a:b", "/var/lib/x", "C0nfig_2"}
	for _, name := range good {
		if err := validateVolName(name); err != nil {
			t.Fatalf("expected accept for %q: %v", name, err)
		}
	}
}

// P0-10: clean volume names must be shell-quoted in every remote command, and a
// hostile container name must short-circuit Apply with an error (no command
// issued for it).
func TestVolumePathShellQuoted(t *testing.T) {
	src := &stubSSH{}
	dst := &stubSSH{}
	step := &DockerVolumeMigrationStep{
		StepName:      "vol",
		ContainerName: "web",
		Volumes:       []string{"data"},
		SourceSSH:     src,
		TargetSSH:    dst,
	}
	if _, err := step.Apply(migration.StepContext{Ctx: context.Background()}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, c := range src.commands {
		if strings.Contains(c, "tar cf") && !strings.Contains(c, "'/tmp/meshium-vol-web.tar'") {
			t.Fatalf("tar source path not quoted: %s", c)
		}
		if strings.Contains(c, "docker stop") && !strings.Contains(c, "'web'") {
			t.Fatalf("container name not quoted in docker stop: %s", c)
		}
	}
}

// P0-10: a hostile container name is rejected before any volume command runs.
func TestVolumeHostileContainerRejected(t *testing.T) {
	src := &stubSSH{}
	dst := &stubSSH{}
	step := &DockerVolumeMigrationStep{
		StepName:      "vol",
		ContainerName: "web; rm -rf /",
		Volumes:       []string{"data"},
		SourceSSH:     src,
		TargetSSH:    dst,
	}
	_, err := step.Apply(migration.StepContext{Ctx: context.Background()})
	if err == nil {
		t.Fatal("expected error for hostile container name")
	}
	if len(src.commands) != 0 {
		t.Fatalf("no commands should run for a hostile name; got %v", src.commands)
	}
}
