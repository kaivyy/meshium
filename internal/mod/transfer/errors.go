package transfer

import "errors"

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
