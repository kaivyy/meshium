package transfer

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyRsyncExit(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{0, nil},
		{23, ErrTransferPartialTransfer},
		{24, ErrTransferPartialTransfer},
		{12, ErrTransferProtocol},
		{13, ErrTransferProtocol},
		{14, ErrTransferProtocol},
		{21, ErrTransferAuth},
		{255, ErrTransferRemoteFailure},
		{1, ErrTransferStrategyUnavailable}, // unmapped → fail closed
		{127, ErrTransferStrategyUnavailable},
	}
	for _, c := range cases {
		got := classifyRsyncExit(c.code)
		if c.want == nil {
			if got != nil {
				t.Fatalf("exit %d: expected nil, got %v", c.code, got)
			}
			continue
		}
		if !errors.Is(got, c.want) {
			t.Fatalf("exit %d: got %v, want %v", c.code, got, c.want)
		}
	}
}

func TestClassifyRsyncExitUnmappedNotSwallowed(t *testing.T) {
	err := classifyRsyncExit(99)
	if err == nil {
		t.Fatal("unmapped non-zero exit must not be nil")
	}
	if !errors.Is(err, ErrTransferStrategyUnavailable) {
		t.Fatalf("unmapped exit should wrap ErrTransferStrategyUnavailable, got %v", err)
	}
}

func TestTransferInactivityFromContextCancel(t *testing.T) {
	// A cancelled parent context must surface as ErrTransferInactivity rather
	// than a bare context error in remoteToRemote.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	src := newMockSSH()
	// Make ExecContext return the context error (mock needs both the prefix
	// output key and the matching error entry).
	src.execOutput["rsync"] = ""
	src.execErr["rsync"] = context.Canceled

	s := NewRsyncStrategy()
	_, err := s.remoteToRemote(ctx,
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: newMockSSH()},
		TransferOptions{}, []string{}, nil,
	)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, ErrTransferInactivity) {
		t.Fatalf("expected ErrTransferInactivity, got %v", err)
	}
}
