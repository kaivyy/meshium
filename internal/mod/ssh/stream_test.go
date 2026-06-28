package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// startMockSSHServerWithStderr starts an SSH server that writes to both
// stdout and stderr in response to exec requests.
func startMockSSHServerWithStderr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{NoClientAuth: true}
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	config.AddHostKey(signer)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer sshConn.Close()
		go ssh.DiscardRequests(reqs)
		for newChan := range chans {
			if newChan.ChannelType() != "session" {
				newChan.Reject(ssh.UnknownChannelType, "unknown")
				continue
			}
			ch, chReqs, err := newChan.Accept()
			if err != nil {
				continue
			}
			go func(in <-chan *ssh.Request) {
				for req := range in {
					if req.Type == "exec" {
						req.Reply(true, nil)
						// Write to stdout
						ch.Write([]byte("stdout-line-1\n"))
						ch.Write([]byte("stdout-line-2\n"))
						// Write to stderr via extended data
						ch.SendRequest("stderr", false, []byte("stderr-line-1\n"))
						// Actually, SSH stderr is sent as extended data type 1
						// The ssh.Channel interface has Stderr() method
						// But in the server-side, we need to use WriteExtended
						// Let's use the channel's stderr writer
						stderr := ch.Stderr()
						stderr.Write([]byte("stderr-line-1\n"))
						stderr.Write([]byte("stderr-line-2\n"))
						// Write more stdout
						ch.Write([]byte("stdout-line-3\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
						ch.Close()
						return
					}
					req.Reply(false, nil)
				}
			}(chReqs)
		}
	}()

	return listener.Addr().String()
}

// TestExecStreamLinesCapturesStdoutAndStderr verifies that ExecStreamLines
// captures both stdout and stderr output.
func TestExecStreamLinesCapturesStdoutAndStderr(t *testing.T) {
	addr := startMockSSHServerWithStderr(t)
	port := parsePort(addr)

	cfg := ServerConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "test",
	}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	var mu sync.Mutex
	var stdoutLines, stderrLines []string

	err = client.ExecStreamLines("test", func(source StreamSource, line string) {
		mu.Lock()
		defer mu.Unlock()
		switch source {
		case StreamStdout:
			stdoutLines = append(stdoutLines, line)
		case StreamStderr:
			stderrLines = append(stderrLines, line)
		}
	})
	if err != nil {
		t.Fatalf("ExecStreamLines failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(stdoutLines) != 3 {
		t.Fatalf("expected 3 stdout lines, got %d: %v", len(stdoutLines), stdoutLines)
	}
	if stdoutLines[0] != "stdout-line-1" {
		t.Fatalf("expected first stdout line 'stdout-line-1', got %q", stdoutLines[0])
	}
	if stdoutLines[2] != "stdout-line-3" {
		t.Fatalf("expected last stdout line 'stdout-line-3', got %q", stdoutLines[2])
	}

	if len(stderrLines) != 2 {
		t.Fatalf("expected 2 stderr lines, got %d: %v", len(stderrLines), stderrLines)
	}
	if stderrLines[0] != "stderr-line-1" {
		t.Fatalf("expected first stderr line 'stderr-line-1', got %q", stderrLines[0])
	}
}

// TestExecStreamOnlyStdout verifies that the deprecated ExecStream method
// only passes stdout lines to the callback (backward compatible).
func TestExecStreamOnlyStdout(t *testing.T) {
	addr := startMockSSHServerWithStderr(t)
	port := parsePort(addr)

	cfg := ServerConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "test",
	}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	var mu sync.Mutex
	var lines []string

	err = client.ExecStream("test", func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("ExecStream failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Should only have stdout lines, not stderr
	if len(lines) != 3 {
		t.Fatalf("expected 3 stdout-only lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "stderr") {
			t.Fatalf("stderr line leaked into stdout-only callback: %q", line)
		}
	}
}

// TestExecStreamLinesContextCancellation verifies that streaming is
// cancelled when the context is cancelled.
func TestExecStreamLinesContextCancellation(t *testing.T) {
	addr := startMockSSHServerWithStderr(t)
	port := parsePort(addr)

	cfg := ServerConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "test",
	}
	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	var lines []string
	err = client.ExecStreamLinesContext(ctx, "test", func(source StreamSource, line string) {
		lines = append(lines, line)
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestStreamSourceConstants verifies the stream source constants.
func TestStreamSourceConstants(t *testing.T) {
	if StreamStdout != 0 {
		t.Fatalf("expected StreamStdout to be 0, got %d", StreamStdout)
	}
	if StreamStderr != 1 {
		t.Fatalf("expected StreamStderr to be 1, got %d", StreamStderr)
	}
}

// TestMaxScanBufferSize verifies that the scanner buffer is large enough
// for typical output.
func TestMaxScanBufferSize(t *testing.T) {
	if maxScanBufferSize < 64*1024 {
		t.Fatalf("maxScanBufferSize (%d) should be at least 64KB", maxScanBufferSize)
	}
}
