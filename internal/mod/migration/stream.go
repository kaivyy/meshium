package migration

import (
	"context"
	"io"
)

// StreamExecuter is the optional streaming-capable surface for reading a remote
// command's stdout as a live stream. The concrete *ssh.Client satisfies it; the
// core SSHExecuter interface does not, so test mocks fall back to the file path.
// Kept off the core interface (transport.go) to avoid forcing ~9 test mocks to
// implement streaming.
//
// A dump that would OOM if buffered (e.g. a multi-GB pg_dump) must go through
// here, not ExecContext.
type StreamExecuter interface {
	// ExecPipe runs cmd on the remote host and returns a reader for its stdout.
	// The reader must be Closed by the caller (which also waits for the session
	// to finish and surfaces a non-zero exit as an error on Close).
	ExecPipe(ctx context.Context, cmd string) (io.ReadCloser, error)
}

// WriteExecuter is the optional streaming-capable surface for feeding a remote
// command's stdin. Paired with StreamExecuter, this lets a source dump stream
// straight into a target restore with no local temp file:
//
//	r, _ := source.ExecPipe(ctx, dumpCmd)
//	_, _, _ = target.ExecWithStdin(ctx, restoreCmd, r)
type WriteExecuter interface {
	// ExecWithStdin runs cmd on the remote host, reading from stdin. Returns
	// stderr output and the exit code. A non-zero exit is reported as an error
	// (unlike ExecContext, callers do not have to inspect the exit code).
	ExecWithStdin(ctx context.Context, cmd string, stdin io.Reader) (stderr string, exitCode int, err error)
}
