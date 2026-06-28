package migration

import (
	"bytes"
	"context"
	"io"
	"strings"
)

// mockSSH is a configurable SSHExecuter for testing.
// It supports per-command output mapping and upload/download capture.
type mockSSH struct {
	execOutput map[string]string // cmd -> stdout
	execErr   map[string]error   // cmd -> error
	uploadData map[string][]byte // remotePath -> uploaded data
	downloadData map[string][]byte // remotePath -> data to serve on download
	alive     bool
}

func newMockSSH() *mockSSH {
	return &mockSSH{
		execOutput:   make(map[string]string),
		execErr:      make(map[string]error),
		uploadData:   make(map[string][]byte),
		downloadData: make(map[string][]byte),
		alive:        true,
	}
}

func (m *mockSSH) Exec(cmd string) (string, string, int, error) {
	// Try exact match first
	if out, ok := m.execOutput[cmd]; ok {
		if err, ok := m.execErr[cmd]; ok {
			return out, "", 1, err
		}
		return out, "", 0, nil
	}
	// Try prefix match (for commands with variable arguments)
	for key, out := range m.execOutput {
		if strings.HasPrefix(cmd, key) {
			if err, ok := m.execErr[key]; ok {
				return out, "", 1, err
			}
			return out, "", 0, nil
		}
	}
	return "", "", 0, nil
}

func (m *mockSSH) ExecContext(ctx context.Context, cmd string) (string, string, int, error) {
	return m.Exec(cmd)
}

func (m *mockSSH) IsAlive() bool { return m.alive }

func (m *mockSSH) Upload(src io.Reader, remotePath string) error {
	buf := new(bytes.Buffer)
	io.Copy(buf, src)
	m.uploadData[remotePath] = buf.Bytes()
	return nil
}

func (m *mockSSH) Download(remotePath string, dst io.Writer) error {
	if data, ok := m.downloadData[remotePath]; ok {
		dst.Write(data)
		return nil
	}
	return nil
}
