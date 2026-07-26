package migration

import (
	"context"
	"strings"
)

// PrivilegeInfo records whether an SSH login can actually perform a migration.
//
// Every collector and applier runs privileged work: reading /etc/shadow,
// writing under /etc, installing packages, restarting services. Root was
// silently assumed, so a non-root login produced a migration that half-worked,
// with permission errors surfacing as scattered step failures deep into the
// run instead of one clear precondition failure before anything was touched.
type PrivilegeInfo struct {
	IsRoot           bool   `json:"isRoot"`
	PasswordlessSudo bool   `json:"passwordlessSudo"`
	Sufficient       bool   `json:"sufficient"`
	Reason           string `json:"reason,omitempty"`
}

// probePrivilege determines what the login can do on the host.
//
// Fails closed: a probe that cannot be answered reports insufficient rather
// than assuming root, because assuming root is what produced the half-finished
// migrations this check exists to prevent.
func probePrivilege(ctx context.Context, ssh SSHExecuter) PrivilegeInfo {
	uid, _, exit, err := ssh.ExecContext(ctx, "id -u 2>/dev/null")
	if err != nil || exit != 0 || strings.TrimSpace(uid) == "" {
		return PrivilegeInfo{
			Reason: "could not determine the login's user id on this host, so its privileges cannot be confirmed",
		}
	}
	if strings.TrimSpace(uid) == "0" {
		return PrivilegeInfo{IsRoot: true, Sufficient: true}
	}

	// -n: never prompt. Success means sudo is available without a password,
	// which is the only non-root mode meshium supports — there is no
	// interactive sudo-password path.
	_, _, sudoExit, sudoErr := ssh.ExecContext(ctx, "sudo -n true 2>/dev/null")
	if sudoErr == nil && sudoExit == 0 {
		return PrivilegeInfo{PasswordlessSudo: true, Sufficient: true}
	}

	return PrivilegeInfo{
		Reason: "the login is not root and has no passwordless sudo; meshium has no interactive sudo-password path, " +
			"so package installs, /etc writes and service restarts would fail partway through the migration",
	}
}

// checkPrivilege blocks a migration whose source or target login cannot do the
// work. Critical rather than a warning: proceeding guarantees a partial
// migration, and a partially applied target is worse than one never touched.
func (e *CompatibilityEngine) checkPrivilege(source, target *serverInfo) CompatibilityCheckResult {
	res := CompatibilityCheckResult{CheckName: "privilege", Severity: SeverityCritical}

	switch {
	case source != nil && !source.Privilege.Sufficient:
		res.Passed = false
		res.Message = "source: " + source.Privilege.Reason
	case target != nil && !target.Privilege.Sufficient:
		res.Passed = false
		res.Message = "target: " + target.Privilege.Reason
	default:
		res.Passed = true
		res.Severity = SeverityInfo
		res.Message = "source and target logins can perform privileged operations (" +
			privilegeDescription(source) + " / " + privilegeDescription(target) + ")"
	}
	return res
}

func privilegeDescription(s *serverInfo) string {
	if s == nil {
		return "unknown"
	}
	switch {
	case s.Privilege.IsRoot:
		return "root"
	case s.Privilege.PasswordlessSudo:
		return "passwordless sudo"
	default:
		return "insufficient"
	}
}
