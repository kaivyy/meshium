package transfer

import (
	"context"
	"io"

	"meshium/internal/mod/transport"
)

// LongTransferExecuter moves a file with NO total-time cap — bounded only by
// the parent context. The default Upload/Download (transport.SSHExecuter) apply
// a FileTransfer total-timeout (stall protection) that a multi-GB DB dump over a
// slow link can exceed, aborting a valid large transfer. SCPStrategy prefers
// this interface when available and falls back to the capped Upload/Download
// otherwise.
//
// The concrete *ssh.Client satisfies it via DownloadLong/UploadLong (bounded by
// the parent context only, no wall-clock SFTP cap). Kept OFF transport.SSHExecuter
// so the ~9 test mocks that only need capped transfer are untouched.
type LongTransferExecuter interface {
	DownloadLong(ctx context.Context, remotePath string, dst io.Writer) error
	UploadLong(ctx context.Context, src io.Reader, remotePath string) error
}

// longDownload transfers a remote source file to a local writer using the cap-free
// path when the SSH client supports LongTransferExecuter, else the capped Download.
func longDownload(ctx context.Context, ssh transport.SSHExecuter, remotePath string, dst io.Writer) error {
	if lt, ok := ssh.(LongTransferExecuter); ok {
		return lt.DownloadLong(ctx, remotePath, dst)
	}
	return ssh.Download(remotePath, dst)
}

// longUpload transfers a local reader to a remote path using the cap-free path
// when available, else the capped Upload.
func longUpload(ctx context.Context, ssh transport.SSHExecuter, src io.Reader, remotePath string) error {
	if lt, ok := ssh.(LongTransferExecuter); ok {
		return lt.UploadLong(ctx, src, remotePath)
	}
	return ssh.Upload(src, remotePath)
}
