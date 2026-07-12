package transfer

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// withRsync makes the mock report rsync as present (used by both source/target
// availability probes, which run `command -v rsync ... && echo ok`).
func withRsync(m *mockSSH) {
	m.execOutput["command -v rsync"] = "ok"
}

// withReachability makes the mock report that the source can SSH to the target
// without a password (BatchMode probe → exit 0).
func withReachability(m *mockSSH) {
	m.execOutput["ssh -o BatchMode=yes"] = ""
}

// blockReachability makes the mock report that the source CANNOT SSH to the
// target (BatchMode probe → exit non-zero). The default mock returns success
// for unmatched commands, so an explicit failure is required to exercise the
// reachability gate.
func blockReachability(m *mockSSH) {
	// The mock only honors execErr for a prefix that also has an execOutput
	// entry, so set both: prefix match finds the output key, then returns the
	// error (exit 1), which makes the reachability probe fail.
	m.execOutput["ssh -o BatchMode=yes"] = ""
	m.execErr["ssh -o BatchMode=yes"] = fmt.Errorf("no route to host")
}

func TestRsyncAvailabilityBothSidesOK(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH()
	withRsync(src)
	withRsync(dst)
	withReachability(src)

	s := NewRsyncStrategy()
	a := s.Availability(context.Background(),
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
	)
	if !a.OK() {
		t.Fatalf("expected OK; got %+v", a)
	}
	if a.Mode() != "direct" {
		t.Fatalf("mode=%q want direct", a.Mode())
	}
}

func TestRsyncAvailabilitySourceRsyncMissing(t *testing.T) {
	src := newMockSSH() // rsync absent (default mock)
	dst := newMockSSH()
	withRsync(dst)
	withReachability(src)

	s := NewRsyncStrategy()
	a := s.Availability(context.Background(),
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
	)
	if a.SourceRsync {
		t.Fatal("source rsync should be reported missing")
	}
	if a.OK() {
		t.Fatal("availability must not be OK when source rsync missing")
	}
	if a.Err == nil || !errors.Is(a.Err, ErrTransferSourceUnreachable) {
		t.Fatalf("expected source-unreachable error, got %v", a.Err)
	}
}

func TestRsyncAvailabilityTargetRsyncMissing(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH() // rsync absent
	withRsync(src)
	withReachability(src)

	s := NewRsyncStrategy()
	a := s.Availability(context.Background(),
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
	)
	if a.TargetRsync {
		t.Fatal("target rsync should be reported missing")
	}
	if a.OK() {
		t.Fatal("availability must not be OK when target rsync missing")
	}
	if a.Err == nil || !errors.Is(a.Err, ErrTransferTargetUnreachable) {
		t.Fatalf("expected target-unreachable error, got %v", a.Err)
	}
}

func TestRsyncAvailabilityReachabilityGate(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH()
	withRsync(src)
	withRsync(dst)
	blockReachability(src) // source CANNOT SSH to target

	s := NewRsyncStrategy()
	a := s.Availability(context.Background(),
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
	)
	if a.Reachable {
		t.Fatal("reachability must be false when source cannot SSH to target")
	}
	if a.OK() {
		t.Fatal("availability must not be OK without reachability")
	}
}

func TestRsyncAvailabilityBothLocalInvalid(t *testing.T) {
	s := NewRsyncStrategy()
	a := s.Availability(context.Background(),
		TransferTarget{IsLocal: true, Path: "/a"},
		TransferTarget{IsLocal: true, Path: "/b"},
	)
	if a.Err == nil || !errors.Is(a.Err, ErrTransferInvalidPath) {
		t.Fatalf("expected invalid-path error for both-local, got %v", a.Err)
	}
}

func TestRsyncAvailabilityModeReporting(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH()
	withRsync(src)
	withRsync(dst)
	withReachability(src)

	s := NewRsyncStrategy()
	if got := s.Mode(context.Background(),
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
	); got != "direct" {
		t.Fatalf("mode=%q want direct", got)
	}
}

func TestSelectWithFallbackDirectPreferred(t *testing.T) {
	src := newMockSSH()
	dst := newMockSSH()
	withRsync(src)
	withRsync(dst)
	withReachability(src)

	sel := NewStrategySelector()
	res, err := sel.SelectWithFallback(context.Background(), 2<<30, TransferOptions{},
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Degraded {
		t.Fatal("direct path chosen must not be flagged degraded")
	}
	if res.Mode != "direct" || res.Strategy.Name() != "rsync" {
		t.Fatalf("expected direct rsync, got mode=%q strategy=%s", res.Mode, res.Strategy.Name())
	}
}

func TestSelectWithFallbackUnavailableFailsClosed(t *testing.T) {
	src := newMockSSH() // rsync absent both sides
	dst := newMockSSH()
	withReachability(src)

	sel := NewStrategySelector()
	_, err := sel.SelectWithFallback(context.Background(), 2<<30, TransferOptions{},
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		false, // no degraded allowed
	)
	if err == nil {
		t.Fatal("expected fail-closed error when direct unavailable and degraded not allowed")
	}
	if !errors.Is(err, ErrTransferStrategyUnavailable) {
		t.Fatalf("expected ErrTransferStrategyUnavailable, got %v", err)
	}
}

func TestSelectWithFallbackDegradedPermitted(t *testing.T) {
	src := newMockSSH() // rsync absent → direct unavailable
	dst := newMockSSH()
	withReachability(src)

	sel := NewStrategySelector()
	res, err := sel.SelectWithFallback(context.Background(), 1024, TransferOptions{},
		TransferTarget{Host: "s", User: "u", SSHClient: src},
		TransferTarget{Host: "t", User: "u", SSHClient: dst},
		true, // degraded allowed
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Degraded {
		t.Fatal("expected degraded=true when falling back to SCP")
	}
	if res.Mode != "degraded" || res.Strategy.Name() != "scp" {
		t.Fatalf("expected degraded scp, got mode=%q strategy=%s", res.Mode, res.Strategy.Name())
	}
}
