package transfer

import (
	"errors"
	"fmt"
)

// Phase 2B transfer errors. Failures are typed so callers can map them to
// terminal states (failed vs degraded vs manual-intervention) instead of
// collapsing every failure into one opaque error.
//
// Errors added incrementally across commits 3–4; all are declared here so the
// package has a single error vocabulary.

// ErrTransferStrategyUnavailable means no usable direct transfer strategy exists
// (e.g. rsync missing on both sides and degraded fallback not permitted).
var ErrTransferStrategyUnavailable = errors.New("transfer strategy unavailable")

// ErrTransferSourceUnreachable means the source host could not be reached over
// SSH (or rsync could not be invoked there).
var ErrTransferSourceUnreachable = errors.New("transfer source unreachable")

// ErrTransferTargetUnreachable means the target host could not be reached over
// SSH (or rsync could not be invoked there).
var ErrTransferTargetUnreachable = errors.New("transfer target unreachable")

// ErrTransferInvalidPath means a transfer endpoint path is empty, both endpoints
// are local, or a remote endpoint lacks an SSH client.
var ErrTransferInvalidPath = errors.New("transfer path invalid")

// ErrTransferAuth means SSH/rsync authentication to the source or target
// failed (rejected key, expired cred, or password-required push).
var ErrTransferAuth = errors.New("transfer auth failed")

// ErrTransferOutputCap means a transfer command produced more captured output
// than the safety bound allows (would otherwise risk OOM). Large transfers
// must stream, never buffer.
var ErrTransferOutputCap = errors.New("transfer output cap exceeded")

// ErrTransferInactivity means a transfer stalled: no stdin/stdout/stderr
// activity for longer than the configured inactivity window.
var ErrTransferInactivity = errors.New("transfer inactivity timeout")

// ErrTransferChecksumMismatch means the post-transfer checksum of source and
// target differ. This is terminal: never reported as success.
var ErrTransferChecksumMismatch = errors.New("transfer checksum mismatch")

// ErrTransferTopologyUnsupported means the requested source/target topology
// (e.g. a bastion-only path, or both-local relay through meshium) cannot
// be represented safely and no explicit degraded fallback was permitted.
var ErrTransferTopologyUnsupported = errors.New("transfer topology unsupported")

// rsyncExitErrors maps notable rsync exit codes to typed transfer errors.
// Unknown non-zero codes fall back to ErrTransferStrategyUnavailable so a
// transfer never silently "succeeds" on a partial exit.
var rsyncExitErrors = map[int]error{
	0:  nil,  // success
	23: ErrTransferPartialTransfer, // partial transfer (e.g. some files vanished)
	24: ErrTransferPartialTransfer, // partial transfer due to vanished source files
	12: ErrTransferProtocol,        // protocol incompatibility
	13: ErrTransferProtocol,        // errors selecting input/output files / dirs
	14: ErrTransferProtocol,        // request/action not supported
	21: ErrTransferAuth,            // shell/command/exec failure (often auth)
	255: ErrTransferRemoteFailure,  // unspecified remote-shell failure
}

// ErrTransferPartialTransfer means rsync exited 23/24 — some files were not
// transferred (e.g. deleted mid-transfer). Treat as incomplete, not success.
var ErrTransferPartialTransfer = errors.New("transfer partial (some files not copied)")

// ErrTransferProtocol means rsync exited 12/13/14 — protocol or arg error.
var ErrTransferProtocol = errors.New("transfer protocol error")

// ErrTransferRemoteFailure means rsync exited 255 — remote shell/connection
// failure; the target partial may be inconsistent.
var ErrTransferRemoteFailure = errors.New("transfer remote failure")

// classifyRsyncExit maps an rsync exit code to a typed error. Code 0 is
// success (nil). Unmapped non-zero codes fall back to
// ErrTransferStrategyUnavailable so a partial/unknown exit is never swallowed.
func classifyRsyncExit(code int) error {
	if e, ok := rsyncExitErrors[code]; ok {
		return e
	}
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%w: rsync exit %d", ErrTransferStrategyUnavailable, code)
}
