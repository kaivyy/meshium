package transfer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RsyncAvailability captures the three conditions needed for a *direct*
// rsync source→target push: rsync on the source, rsync on the target, and
// source can reach target over SSH without a password prompt. Any one missing
// means the direct strategy cannot be safely represented and must not be
// selected silently.
type RsyncAvailability struct {
	SourceRsync bool
	TargetRsync bool
	Reachable   bool
	Err         error
}

// OK reports whether the direct rsync path is fully usable.
func (a RsyncAvailability) OK() bool {
	return a.SourceRsync && a.TargetRsync && a.Reachable && a.Err == nil
}

// Mode returns the strategy mode for reporting: "direct" when the direct
// source→target path is available, otherwise "blocked".
func (a RsyncAvailability) Mode() string {
	if a.OK() {
		return "direct"
	}
	return "blocked"
}

// runOnTarget runs cmd on either a remote SSH target or the local meshium
// host. It mirrors how the transfer itself executes: remote→remote runs on the
// source; local→remote runs locally.
func runOnTarget(ctx context.Context, t TransferTarget, cmd string) (string, string, int, error) {
	if t.IsLocal || t.SSHClient == nil {
		return runLocal(ctx, cmd)
	}
	return t.SSHClient.ExecContext(ctx, cmd)
}

// runLocal runs cmd on the meshium host via os/exec (no shell). Used for
// local→remote probes where the meshium host itself must SSH to the target.
func runLocal(ctx context.Context, cmd string) (string, string, int, error) {
	// Split into argv on single spaces; probes are static templates with no
	// user-injected values, so a plain split is safe here.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", "", 1, fmt.Errorf("empty probe command")
	}
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	out, err := c.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), "", exitErr.ExitCode(), nil
		}
		return string(out), "", 1, err
	}
	return string(out), "", 0, nil
}

// probeRsyncPresent reports whether `command -v rsync` succeeds on the target.
func probeRsyncPresent(ctx context.Context, t TransferTarget) bool {
	out, _, code, err := runOnTarget(ctx, t, "command -v rsync >/dev/null 2>&1 && echo ok")
	if err != nil || code != 0 {
		return false
	}
	return strings.Contains(out, "ok")
}

// probeReachability reports whether the source can open an SSH connection to
// the target without a password prompt (BatchMode fails fast on missing keys).
// For a local source, the meshium host runs the probe; for a remote source,
// the probe runs on the source via its SSH client.
func probeReachability(ctx context.Context, src, dst TransferTarget) bool {
	if dst.IsLocal || dst.SSHClient == nil {
		// Reaching a local target is trivially true; rsync writes locally.
		return true
	}
	probe := fmt.Sprintf(
		"ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 %s@%s 'true'",
		dst.User, dst.Host,
	)
	if dst.Port != 0 && dst.Port != 22 {
		probe = fmt.Sprintf(
			"ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5 -p %d %s@%s 'true'",
			dst.Port, dst.User, dst.Host,
		)
	}
	_, _, code, err := runOnTarget(ctx, src, probe)
	return err == nil && code == 0
}

// Availability checks all three conditions for a direct rsync source→target
// push. It is the gate used before selecting/executing rsync direct; a
// non-OK result must route to manual intervention or an explicit degraded
// fallback — never a silent downgrade.
func (s *RsyncStrategy) Availability(ctx context.Context, src, dst TransferTarget) RsyncAvailability {
	a := RsyncAvailability{}
	if src.IsLocal && dst.IsLocal {
		a.Err = ErrTransferInvalidPath
		return a
	}
	if !src.IsLocal && src.SSHClient == nil {
		a.Err = ErrTransferInvalidPath
		return a
	}
	if !dst.IsLocal && dst.SSHClient == nil {
		a.Err = ErrTransferInvalidPath
		return a
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	a.SourceRsync = probeRsyncPresent(ctx, src)
	if !a.SourceRsync {
		a.Err = fmt.Errorf("%w: rsync not present on source", ErrTransferSourceUnreachable)
		return a
	}
	a.TargetRsync = probeRsyncPresent(ctx, dst)
	if !a.TargetRsync {
		a.Err = fmt.Errorf("%w: rsync not present on target", ErrTransferTargetUnreachable)
		return a
	}
	a.Reachable = probeReachability(ctx, src, dst)
	if !a.Reachable {
		a.Err = fmt.Errorf("%w: source cannot reach target over SSH", ErrTransferTargetUnreachable)
		return a
	}
	return a
}

// Mode reports the rsync strategy mode for the given endpoints.
func (s *RsyncStrategy) Mode(ctx context.Context, src, dst TransferTarget) string {
	return s.Availability(ctx, src, dst).Mode()
}
