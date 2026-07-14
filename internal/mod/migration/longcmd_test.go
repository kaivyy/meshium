package migration

import (
	"context"
	"io"
	"testing"
)

// longSSH is a mock that implements BOTH the capped SSHExecuter and the
// cap-free LongCommandExecuter, with counters so we can assert which path a
// large DB dump/restore took.
type longSSH struct {
	cappedCmd int
	longCmd   int
}

func (s *longSSH) Exec(cmd string) (string, string, int, error) { return "", "", 0, nil }
func (s *longSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	s.cappedCmd++
	return "", "", 0, nil
}
func (s *longSSH) IsAlive() bool { return true }
func (s *longSSH) Upload(src io.Reader, remotePath string) error            { return nil }
func (s *longSSH) Download(remotePath string, dst io.Writer) error          { return nil }
func (s *longSSH) ExecLongCommand(ctx context.Context, cmd string) (string, string, int, error) {
	s.longCmd++
	return "", "", 0, nil
}

// cappedOnlySSH implements only the capped interface — the fallback path.
type cappedOnlySSH struct{ longCalled int }

func (s *cappedOnlySSH) Exec(cmd string) (string, string, int, error) { return "", "", 0, nil }
func (s *cappedOnlySSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return "", "", 0, nil
}
func (s *cappedOnlySSH) IsAlive() bool { return true }
func (s *cappedOnlySSH) Upload(src io.Reader, remotePath string) error   { return nil }
func (s *cappedOnlySSH) Download(remotePath string, dst io.Writer) error { return nil }

// TestExecLongOrContextPrefersLongPath proves large DB dumps/restores take the
// cap-free path when available (no 5m Command ceiling), and fall back to the
// capped ExecContext only when the implementation lacks LongCommandExecuter.
func TestExecLongOrContextPrefersLongPath(t *testing.T) {
	long := &longSSH{}
	if _, _, _, err := execLongOrContext(context.Background(), long, "dump x"); err != nil {
		t.Fatalf("long exec: %v", err)
	}
	if long.longCmd != 1 || long.cappedCmd != 0 {
		t.Errorf("expected long path, got long=%d capped=%d", long.longCmd, long.cappedCmd)
	}

	capped := &cappedOnlySSH{}
	if _, _, _, err := execLongOrContext(context.Background(), capped, "dump x"); err != nil {
		t.Fatalf("capped exec: %v", err)
	}
	if capped.longCalled != 0 {
		t.Errorf("capped-only impl must not hit long path")
	}
}
