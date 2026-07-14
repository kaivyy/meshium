package transfer

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// longSSH is a mock that implements BOTH the capped transport.SSHExecuter and
// the cap-free LongTransferExecuter, with counters so we can assert which path
// a large DB dump/restore transfer took.
type longSSH struct {
	cappedUp  int
	cappedDn  int
	longUp    int
	longDn    int
	upData    []byte
	dnServe   []byte
}

func (s *longSSH) Exec(cmd string) (string, string, int, error) { return "", "", 0, nil }
func (s *longSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return "", "", 0, nil
}
func (s *longSSH) IsAlive() bool { return true }
func (s *longSSH) Upload(src io.Reader, remotePath string) error {
	s.cappedUp++
	buf := new(bytes.Buffer)
	io.Copy(buf, src)
	s.upData = buf.Bytes()
	return nil
}
func (s *longSSH) Download(remotePath string, dst io.Writer) error {
	s.cappedDn++
	if s.dnServe != nil {
		dst.Write(s.dnServe)
	}
	return nil
}
func (s *longSSH) DownloadLong(ctx context.Context, remotePath string, dst io.Writer) error {
	s.longDn++
	if s.dnServe != nil {
		dst.Write(s.dnServe)
	}
	return nil
}
func (s *longSSH) UploadLong(ctx context.Context, src io.Reader, remotePath string) error {
	s.longUp++
	buf := new(bytes.Buffer)
	io.Copy(buf, src)
	s.upData = buf.Bytes()
	return nil
}

// cappedOnlySSH implements only the capped interface — the fallback path.
type cappedOnlySSH struct {
	cappedUp int
	cappedDn int
	upData   []byte
	dnServe  []byte
}

func (s *cappedOnlySSH) Exec(cmd string) (string, string, int, error) { return "", "", 0, nil }
func (s *cappedOnlySSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return "", "", 0, nil
}
func (s *cappedOnlySSH) IsAlive() bool { return true }
func (s *cappedOnlySSH) Upload(src io.Reader, remotePath string) error {
	s.cappedUp++
	buf := new(bytes.Buffer)
	io.Copy(buf, src)
	s.upData = buf.Bytes()
	return nil
}
func (s *cappedOnlySSH) Download(remotePath string, dst io.Writer) error {
	s.cappedDn++
	if s.dnServe != nil {
		dst.Write(s.dnServe)
	}
	return nil
}

// TestLongDownloadPrefersCapFreePath proves the SFTP legs of a large DB
// dump/restore use the cap-free DownloadLong/UploadLong when available (no 10m
// SFTP ceiling), and fall back to capped Download/Upload otherwise — preserving
// the large-transfer-safe behavior now owned by the transfer package.
func TestLongDownloadPrefersCapFreePath(t *testing.T) {
	payload := []byte("multi-gb-dump-bytes")
	long := &longSSH{dnServe: payload}
	if err := longDownload(context.Background(), long, "/src/dump", bytes.NewBuffer(nil)); err != nil {
		t.Fatalf("download: %v", err)
	}
	if err := longUpload(context.Background(), long, bytes.NewReader(payload), "/tgt/dump"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if long.longDn != 1 || long.longUp != 1 || long.cappedDn != 0 || long.cappedUp != 0 {
		t.Errorf("expected cap-free path, got dn=%d up=%d cappedDn=%d cappedUp=%d",
			long.longDn, long.longUp, long.cappedDn, long.cappedUp)
	}

	capped := &cappedOnlySSH{dnServe: payload}
	if err := longDownload(context.Background(), capped, "/src/dump", bytes.NewBuffer(nil)); err != nil {
		t.Fatalf("download fallback: %v", err)
	}
	if err := longUpload(context.Background(), capped, bytes.NewReader(payload), "/tgt/dump"); err != nil {
		t.Fatalf("upload fallback: %v", err)
	}
	if capped.cappedDn != 1 || capped.cappedUp != 1 {
		t.Errorf("capped-only impl must use the capped path, got dn=%d up=%d", capped.cappedDn, capped.cappedUp)
	}
	if !bytes.Equal(capped.upData, payload) {
		t.Errorf("capped upload lost data")
	}
}
