package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

// ErrOutputLimitExceeded is returned by ExecContextWithTimeout when captured
// stdout or stderr exceeds the configured cap. Output a parser relies on is
// never silently truncated: the caller learns the capture was incomplete and
// the remote command is terminated where possible.
var ErrOutputLimitExceeded = errors.New("output limit exceeded")

// ErrInactivityTimeout is returned by ExecWithStdin when no stdin write,
// stdout/stderr activity, or progress marker is observed within the configured
// Inactivity window. Long restores with steady flow never trip it.
var ErrInactivityTimeout = errors.New("inactivity timeout")

// stdoutCap / stderrCap bound the per-command capture in ExecContextWithTimeout.
// Metadata commands (the intended use of ExecContext) stay well under these; the
// cap is a guard against a multi-GB dump misrouted through ExecContext. Streaming
// commands (ExecPipe/ExecWithStdin) bypass this entirely.
const (
	stdoutCap = 1 << 20 // 1 MiB
	stderrCap = 256 << 10 // 256 KiB
)

// boundedWriter is a cap-bounded bytes.Buffer replacement. Once the cap is
// exceeded, further writes are discarded and overflowed is set; the writer
// reports the full byte count (so ssh session accounting isn't confused) but
// retains only the first cap bytes. It is safe for concurrent use by Stdout/
// Stderr writers.
type boundedWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	cap        int
	overflowed bool
}

// newBoundedWriter returns a boundedWriter with the given byte cap.
func newBoundedWriter(cap, _ int) *boundedWriter {
	if cap <= 0 {
		cap = stdoutCap
	}
	return &boundedWriter{cap: cap}
}

// Write implements io.Writer. It always reports the full number of bytes offered
// (n == len(p)) and never returns an error — overflow is signalled via the
// overflowed flag, not by failing the SSH session's stdout capture.
func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.buf.Len() >= w.cap {
		w.overflowed = true
		return n, nil
	}
	room := w.cap - w.buf.Len()
	if len(p) > room {
		w.buf.Write(p[:room])
		w.overflowed = true
	} else {
		w.buf.Write(p)
	}
	return n, nil
}

// String returns the captured (possibly truncated) output.
func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// Overflowed reports whether the cap was exceeded.
func (w *boundedWriter) Overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}

// outputLimitError wraps ErrOutputLimitExceeded with which stream overflowed.
type outputLimitError struct{ stream string }

func (e *outputLimitError) Error() string {
	return fmt.Sprintf("%s: %s", ErrOutputLimitExceeded, e.stream)
}

// Unwrap allows errors.Is(err, ErrOutputLimitExceeded).
func (e *outputLimitError) Unwrap() error { return ErrOutputLimitExceeded }
