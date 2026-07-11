package ssh

import (
	"errors"
	"testing"
	"time"
)

// P0-4: DefaultTimeouts.FileTransfer must be raised to 30m as an interim
// compatibility value (not "large file solved"), and must remain configurable —
// a caller-supplied FileTransfer overrides the default.
func TestFileTransferTimeoutConfigurable(t *testing.T) {
	if DefaultTimeouts.FileTransfer != 30*time.Minute {
		t.Fatalf("DefaultTimeouts.FileTransfer must be 30m interim, got %v", DefaultTimeouts.FileTransfer)
	}
	cfg := TimeoutConfig{Connect: 1 * time.Second, Command: 2 * time.Second, FileTransfer: 2 * time.Hour}
	got := cfg.withDefaults()
	if got.FileTransfer != 2*time.Hour {
		t.Fatalf("custom FileTransfer must be honored, not overwritten by the default; got %v", got.FileTransfer)
	}
	if got.Command != 2*time.Second {
		t.Fatalf("custom Command must be honored; got %v", got.Command)
	}
}

// P0-4: a zero FileTransfer falls back to the 30m default.
func TestFileTransferZeroFallsBackToDefault(t *testing.T) {
	cfg := TimeoutConfig{Connect: 1 * time.Second, Command: 2 * time.Second}
	got := cfg.withDefaults()
	if got.FileTransfer != DefaultTimeouts.FileTransfer {
		t.Fatalf("zero FileTransfer must fall back to default; got %v", got.FileTransfer)
	}
}

// P0-9: Inactivity is a new configurable field; zero (default) means disabled —
// ExecWithStdin bounds lifetime only by the parent ctx when Inactivity==0.
func TestInactivityFieldConfigurable(t *testing.T) {
	cfg := TimeoutConfig{Connect: 1 * time.Second, Command: 2 * time.Second, Inactivity: 90 * time.Second}
	got := cfg.withDefaults()
	if got.Inactivity != 90*time.Second {
		t.Fatalf("custom Inactivity must survive withDefaults; got %v", got.Inactivity)
	}
	// Zero stays zero (disabled) — not replaced by any default.
	zero := TimeoutConfig{}.withDefaults()
	if zero.Inactivity != 0 {
		t.Fatalf("zero Inactivity must remain zero (disabled); got %v", zero.Inactivity)
	}
}

// ErrInactivityTimeout is the typed error returned by ExecWithStdin on a stall.
func TestErrInactivityTimeoutTyped(t *testing.T) {
	if !errors.Is(ErrInactivityTimeout, ErrInactivityTimeout) {
		t.Fatal("ErrInactivityTimeout must satisfy errors.Is")
	}
}
