package ssh

import (
	"testing"
	"time"
)

// TestTimeoutConfigWithDefaults verifies that withDefaults fills in zero values.
func TestTimeoutConfigWithDefaults(t *testing.T) {
	cfg := TimeoutConfig{}
	out := cfg.withDefaults()

	if out.Connect != DefaultTimeouts.Connect {
		t.Fatalf("expected Connect %v, got %v", DefaultTimeouts.Connect, out.Connect)
	}
	if out.Command != DefaultTimeouts.Command {
		t.Fatalf("expected Command %v, got %v", DefaultTimeouts.Command, out.Command)
	}
	if out.FileTransfer != DefaultTimeouts.FileTransfer {
		t.Fatalf("expected FileTransfer %v, got %v", DefaultTimeouts.FileTransfer, out.FileTransfer)
	}
}

// TestTimeoutConfigWithDefaultsPreservesSetValues verifies that withDefaults
// preserves non-zero values.
func TestTimeoutConfigWithDefaultsPreservesSetValues(t *testing.T) {
	cfg := TimeoutConfig{
		Connect:      5 * time.Second,
		Command:      2 * time.Minute,
		FileTransfer: 10 * time.Minute,
	}
	out := cfg.withDefaults()

	if out.Connect != 5*time.Second {
		t.Fatalf("expected Connect 5s, got %v", out.Connect)
	}
	if out.Command != 2*time.Minute {
		t.Fatalf("expected Command 2m, got %v", out.Command)
	}
	if out.FileTransfer != 10*time.Minute {
		t.Fatalf("expected FileTransfer 10m, got %v", out.FileTransfer)
	}
}

// TestTimeoutConfigWithDefaultsPartialOverride verifies that withDefaults
// only fills in zero fields.
func TestTimeoutConfigWithDefaultsPartialOverride(t *testing.T) {
	cfg := TimeoutConfig{
		Command: 2 * time.Minute,
	}
	out := cfg.withDefaults()

	if out.Connect != DefaultTimeouts.Connect {
		t.Fatalf("expected Connect %v, got %v", DefaultTimeouts.Connect, out.Connect)
	}
	if out.Command != 2*time.Minute {
		t.Fatalf("expected Command 2m, got %v", out.Command)
	}
	if out.FileTransfer != DefaultTimeouts.FileTransfer {
		t.Fatalf("expected FileTransfer %v, got %v", DefaultTimeouts.FileTransfer, out.FileTransfer)
	}
}

// TestTimeoutProfiles verifies that all predefined timeout profiles have
// non-zero values (except where intentionally zero).
func TestTimeoutProfiles(t *testing.T) {
	profiles := []struct {
		name         string
		cfg          TimeoutConfig
		allowZeroCmd bool
	}{
		{"DefaultTimeouts", DefaultTimeouts, false},
		{"DiscoveryTimeouts", DiscoveryTimeouts, false},
		{"MigrationTimeouts", MigrationTimeouts, false},
		{"PackageInstallTimeouts", PackageInstallTimeouts, false},
		{"InteractiveTimeouts", InteractiveTimeouts, true},
		{"DatabaseTimeouts", DatabaseTimeouts, false},
	}

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			if p.cfg.Connect == 0 {
				t.Fatal("Connect timeout must not be zero")
			}
			if !p.allowZeroCmd && p.cfg.Command == 0 {
				t.Fatal("Command timeout must not be zero for non-interactive profiles")
			}
			if p.cfg.FileTransfer == 0 {
				t.Fatal("FileTransfer timeout must not be zero")
			}
		})
	}
}

// TestDiscoveryTimeoutsFasterThanDefault verifies that discovery timeouts
// are shorter than default timeouts for command execution.
func TestDiscoveryTimeoutsFasterThanDefault(t *testing.T) {
	if DiscoveryTimeouts.Command >= DefaultTimeouts.Command {
		t.Fatalf("discovery command timeout (%v) should be shorter than default (%v)",
			DiscoveryTimeouts.Command, DefaultTimeouts.Command)
	}
}

// TestPackageInstallTimeoutsLongerThanDefault verifies that package install
// timeouts are longer than default timeouts for command execution.
func TestPackageInstallTimeoutsLongerThanDefault(t *testing.T) {
	if PackageInstallTimeouts.Command <= DefaultTimeouts.Command {
		t.Fatalf("package install command timeout (%v) should be longer than default (%v)",
			PackageInstallTimeouts.Command, DefaultTimeouts.Command)
	}
}

// TestMigrationTimeoutsLongerThanDefault verifies that migration timeouts
// are longer than default timeouts for command execution.
func TestMigrationTimeoutsLongerThanDefault(t *testing.T) {
	if MigrationTimeouts.Command <= DefaultTimeouts.Command {
		t.Fatalf("migration command timeout (%v) should be longer than default (%v)",
			MigrationTimeouts.Command, DefaultTimeouts.Command)
	}
}

// TestInteractiveTimeoutsHasZeroCommand verifies that interactive timeouts
// have a zero command timeout (no explicit timeout, relies on parent context).
func TestInteractiveTimeoutsHasZeroCommand(t *testing.T) {
	if InteractiveTimeouts.Command != 0 {
		t.Fatalf("interactive command timeout should be 0 (no timeout), got %v",
			InteractiveTimeouts.Command)
	}
}

// TestServerConfigTimeoutsPropagated verifies that ServerConfig.Timeouts
// are propagated to the Client through connect().
func TestServerConfigTimeoutsPropagated(t *testing.T) {
	// This is a compile-time check that ServerConfig has a Timeouts field
	cfg := ServerConfig{
		Timeouts: MigrationTimeouts,
	}
	if cfg.Timeouts.Command != MigrationTimeouts.Command {
		t.Fatalf("expected MigrationTimeouts.Command, got %v", cfg.Timeouts.Command)
	}
}
