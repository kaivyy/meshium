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
	execOutputSeq map[string][]string // cmd -> ordered outputs, consumed per call
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
		execOutputSeq: make(map[string][]string),
		execExit:     make(map[string]int),
		execErr:      make(map[string]error),
		uploadData:   make(map[string][]byte),
		downloadData: make(map[string][]byte),
		alive:        true,
	}
}

func (m *mockSSH) Exec(cmd string) (string, string, int, error) {
	m.commands = append(m.commands, cmd)
	// Ordered outputs (e.g. a probe that must return two different values across
	// calls) take precedence when available; they are consumed one per call.
	if seq, ok := m.execOutputSeq[cmd]; ok && len(seq) > 0 {
		out := seq[0]
		m.execOutputSeq[cmd] = seq[1:]
		if err, ok := m.execErr[cmd]; ok {
			return out, "", 1, err
		}
		return out, "", m.exitFor(cmd), nil
	}
	// Try exact match first
	if out, ok := m.execOutput[cmd]; ok {
		if err, ok := m.execErr[cmd]; ok {
			return out, "", 1, err
		}
		return out, "", m.exitFor(cmd), nil
	}
	// Try prefix match (for commands with variable arguments), then substring
	// (for probes whose command varies but a stable token identifies the
	// expected output, e.g. "pgrep -x postgres").
	//
	// Both pick the LONGEST matching key. Go randomizes map iteration, so
	// returning the first match made these tests nondeterministic whenever two
	// stubs matched one command. TestDatabaseCollectResumeNote stubs both
	// "pgrep -x mysql" and "mysql", and the mysql probe
	// "(pgrep -x mysqld ... || pgrep -x mariadbd ...) && echo yes" contains
	// both — so roughly one run in six answered the detect probe with the
	// database listing and the test failed. Longest-match is also the right
	// semantics: the more specific stub should win.
	if key, ok := m.longestMatch(cmd, strings.HasPrefix); ok {
		return m.resultFor(key)
	}
	if key, ok := m.longestMatch(cmd, strings.Contains); ok {
		return m.resultFor(key)
	}
	return "", "", 0, nil
}

// longestMatch returns the longest key in execOutput satisfying match(cmd, key).
func (m *mockSSH) longestMatch(cmd string, match func(s, substr string) bool) (string, bool) {
	best := ""
	found := false
	for key := range m.execOutput {
		if !match(cmd, key) {
			continue
		}
		if !found || len(key) > len(best) {
			best, found = key, true
		}
	}
	return best, found
}

// resultFor renders the stubbed response for an already-selected key.
func (m *mockSSH) resultFor(key string) (string, string, int, error) {
	out := m.execOutput[key]
	if err, ok := m.execErr[key]; ok {
		return out, "", 1, err
	}
	return out, "", m.exitFor(key), nil
}

// addOutput registers a substring→stdout mapping (lowest-priority, substring
// match). Useful for probes like "pgrep -x postgres" whose exact command is
// awkward to key by prefix.
func (m *mockSSH) addOutput(substr, out string) {
	m.execOutput[substr] = out
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
