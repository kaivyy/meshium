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
	execOutput   map[string]string // cmd -> stdout
	execExit     map[string]int    // cmd prefix -> nonzero exit code
	execErr      map[string]error  // cmd -> error
	uploadData   map[string][]byte // remotePath -> uploaded data
	downloadData map[string][]byte // remotePath -> data to serve on download
	commands     []string
	downloadCalls int
	alive        bool
}

func newMockSSH() *mockSSH {
	return &mockSSH{
		execOutput:   make(map[string]string),
		execExit:     make(map[string]int),
		execErr:      make(map[string]error),
		uploadData:   make(map[string][]byte),
		downloadData: make(map[string][]byte),
		alive:        true,
	}
}

func (m *mockSSH) Exec(cmd string) (string, string, int, error) {
	m.commands = append(m.commands, cmd)
	// Try exact match first
	if out, ok := m.execOutput[cmd]; ok {
		if err, ok := m.execErr[cmd]; ok {
			return out, "", 1, err
		}
		return out, "", m.exitFor(cmd), nil
	}
	// Try prefix match (for commands with variable arguments)
	for key, out := range m.execOutput {
		if strings.HasPrefix(cmd, key) {
			if err, ok := m.execErr[key]; ok {
				return out, "", 1, err
			}
			return out, "", m.exitFor(key), nil
		}
	}
	return "", "", 0, nil
}

// exitFor returns the configured nonzero exit code for a command (exact or
// prefix), else 0. Models e.g. sha256sum exiting 1 when a path is missing.
func (m *mockSSH) exitFor(cmd string) int {
	if c, ok := m.execExit[cmd]; ok {
		return c
	}
	for key, c := range m.execExit {
		if strings.HasPrefix(cmd, key) {
			return c
		}
	}
	return 0
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
	m.downloadCalls++
	if data, ok := m.downloadData[remotePath]; ok {
		dst.Write(data)
		return nil
	}
	return nil
}

func containsCommand(commands []string, want string) bool {
	for _, cmd := range commands {
		if cmd == want {
			return true
		}
	}
	return false
}

func containsCommandPrefix(commands []string, prefix string) bool {
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, prefix) {
			return true
		}
	}
	return false
}
