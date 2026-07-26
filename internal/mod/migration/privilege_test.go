package migration

import (
	"context"
	"strings"
	"testing"
)

// Every collector and applier runs commands that need root: reading
// /etc/shadow, writing under /etc, installing packages, restarting services.
// Root was silently ASSUMED — a non-root login produced a migration that
// half-worked, with permission errors surfacing as individual step failures
// deep into the run rather than as one clear precondition failure up front.
//
// The privilege probe answers it once, before anything is applied.

func TestPrivilegeProbeDetectsRoot(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("id -u", "0\n")

	p := probePrivilege(context.Background(), ssh)
	if !p.IsRoot {
		t.Error("uid 0 not detected as root")
	}
	if !p.Sufficient {
		t.Error("root must be sufficient")
	}
}

// A non-root user with passwordless sudo can do the work.
func TestPrivilegeProbeAcceptsPasswordlessSudo(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("id -u", "1000\n")
	ssh.addOutput("sudo -n true", "")

	p := probePrivilege(context.Background(), ssh)
	if p.IsRoot {
		t.Error("uid 1000 reported as root")
	}
	if !p.PasswordlessSudo {
		t.Error("passwordless sudo not detected")
	}
	if !p.Sufficient {
		t.Error("non-root with NOPASSWD sudo should be sufficient")
	}
}

// A non-root user WITHOUT passwordless sudo cannot complete a migration.
// Meshium has no interactive sudo-password path, so this must be stated as a
// precondition failure rather than discovered mid-apply.
func TestPrivilegeProbeRejectsUnprivilegedUser(t *testing.T) {
	ssh := newMockSSH()
	ssh.addOutput("id -u", "1000\n")
	ssh.addOutput("sudo -n true", "sudo: a password is required")
	ssh.execExit["sudo -n true"] = 1

	p := probePrivilege(context.Background(), ssh)
	if p.Sufficient {
		t.Error("a user with neither root nor NOPASSWD sudo was accepted")
	}
	if p.Reason == "" {
		t.Error("insufficient privilege must explain itself")
	}
	if !strings.Contains(strings.ToLower(p.Reason), "sudo") {
		t.Errorf("reason should name the missing capability: %q", p.Reason)
	}
}

// A probe that cannot run at all must fail closed, not assume root.
func TestPrivilegeProbeFailsClosed(t *testing.T) {
	ssh := newMockSSH() // answers nothing
	p := probePrivilege(context.Background(), ssh)
	if p.Sufficient {
		t.Error("an unanswerable privilege probe was treated as sufficient")
	}
}

// The compatibility engine must surface this as a CRITICAL blocker so the
// wizard refuses rather than warns.
func TestPrivilegeCheckIsCriticalWhenInsufficient(t *testing.T) {
	e := &CompatibilityEngine{}
	res := e.checkPrivilege(
		&serverInfo{Privilege: PrivilegeInfo{Sufficient: true, IsRoot: true}},
		&serverInfo{Privilege: PrivilegeInfo{Sufficient: false, Reason: "needs root or passwordless sudo"}},
	)
	if res.Passed {
		t.Fatal("insufficient target privilege passed the check")
	}
	if res.Severity != SeverityCritical {
		t.Errorf("severity = %q, want critical", res.Severity)
	}
}

func TestPrivilegeCheckPassesWhenBothSufficient(t *testing.T) {
	e := &CompatibilityEngine{}
	res := e.checkPrivilege(
		&serverInfo{Privilege: PrivilegeInfo{Sufficient: true, IsRoot: true}},
		&serverInfo{Privilege: PrivilegeInfo{Sufficient: true, PasswordlessSudo: true}},
	)
	if !res.Passed {
		t.Errorf("both sides privileged but check failed: %s", res.Message)
	}
}
