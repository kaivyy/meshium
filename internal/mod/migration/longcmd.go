package migration

import (
	"context"
	"io"
)

// LongCommandExecuter runs a command bounded ONLY by the parent context — no
// hard wall-clock cap. A multi-GB database dump or restore legitimately
// outlasts the default 5m Command timeout; capping it would kill real transfers
// (the exact "large transfer unsafe" failure). The concrete *ssh.Client
// satisfies this via ExecContextWithTimeout(ctx, cmd, 0) (0 = no explicit
// timeout, only the parent context applies).
//
// Kept OFF the core SSHExecuter interface (mirroring StreamExecuter/
// WriteExecuter) so the ~9 test mocks that only need the capped ExecContext are
// not forced to implement it. applyFile type-asserts for LongCommandExecuter and
// falls back to ExecContext when the implementation does not provide it (mocks),
// so behavior is unchanged where the interface is absent and large transfers are
// safe where it is present. ponytail: once mocks implement it, drop the fallback.
type LongCommandExecuter interface {
	ExecLongCommand(ctx context.Context, cmd string) (stdout, stderr string, exitCode int, err error)
}

// execLongOrContext runs cmd with no hard cap when the SSH implementation
// supports LongCommandExecuter (real *ssh.Client, where large DB dump/restore
// must outlast the 5m Command cap), and gracefully falls back to the capped
// ExecContext otherwise (test mocks). Behavior is unchanged for callers that
// only provide the capped interface.
func execLongOrContext(ctx context.Context, ssh SSHExecuter, cmd string) (string, string, int, error) {
	if lc, ok := ssh.(LongCommandExecuter); ok {
		return lc.ExecLongCommand(ctx, cmd)
	}
	return ssh.ExecContext(ctx, cmd)
}

// LongTransferExecuter moves a file with NO total-time cap — bounded only by the
// parent context. The default Upload/Download apply a FileTransfer total-timeout
// (stall protection) that a multi-GB DB dump over a slow link can exceed. For
// the database file path that would abort a valid large transfer, so applyFile
// uses this when available and falls back to Upload/Download otherwise. Off the
// core SSHExecuter interface so the ~9 capped mocks are untouched.
type LongTransferExecuter interface {
	DownloadLong(ctx context.Context, remotePath string, dst io.Writer) error
	UploadLong(ctx context.Context, src io.Reader, remotePath string) error
}

// transferLongOrFallback uses cap-free transfer when supported, else the capped
// Upload/Download.
func transferLongOrFallback(ctx context.Context, ssh SSHExecuter, src io.Reader, remotePath string) error {
	if lt, ok := ssh.(LongTransferExecuter); ok {
		return lt.UploadLong(ctx, src, remotePath)
	}
	return ssh.Upload(src, remotePath)
}

func transferDownloadLongOrFallback(ctx context.Context, ssh SSHExecuter, remotePath string, dst io.Writer) error {
	if lt, ok := ssh.(LongTransferExecuter); ok {
		return lt.DownloadLong(ctx, remotePath, dst)
	}
	return ssh.Download(remotePath, dst)
}
