package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func parsePort(addr string) int {
	_, port, _ := net.SplitHostPort(addr)
	var p int
	fmt.Sscanf(port, "%d", &p)
	return p
}

func startMockSSHServer(t *testing.T) (string, *ssh.ServerConfig) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}

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
			ch, reqs, err := newChan.Accept()
			if err != nil {
				continue
			}
			go func(in <-chan *ssh.Request) {
				for req := range in {
					if req.Type == "exec" {
						req.Reply(true, nil)
						ch.Write([]byte("mock-output\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
						ch.Close()
						return
					}
					req.Reply(false, nil)
				}
			}(reqs)
		}
	}()

	return listener.Addr().String(), config
}

func TestExecCommand(t *testing.T) {
	addr, _ := startMockSSHServer(t)
	host, _, _ := net.SplitHostPort(addr)

	cfg := ServerConfig{
		Host:     host,
		Port:     parsePort(addr),
		Username: "test",
	}

	client, err := connect(cfg, ssh.InsecureIgnoreHostKey())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	stdout, _, exitCode, err := client.Exec("echo hello")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "mock-output") {
		t.Errorf("expected 'mock-output', got %q", stdout)
	}
}
