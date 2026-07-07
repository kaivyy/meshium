package ai

import (
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"

	modssh "meshium/internal/mod/ssh"
)

// TestServiceHostKeyCallbackFailsClosedWithoutStore verifies the AI assistant
// does NOT silently fall back to InsecureIgnoreHostKey when it has no known-hosts
// store. These dials carry decrypted credentials, so the callback must reject.
func TestServiceHostKeyCallbackFailsClosedWithoutStore(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil) // knownHosts == nil
	cb := s.hostKeyCallback(1)
	if cb == nil {
		t.Fatal("hostKeyCallback returned nil")
	}

	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22}
	var dummyKey ssh.PublicKey // a nil PublicKey is fine; the callback rejects before touching it
	err := cb("host", addr, dummyKey)
	if err == nil {
		t.Fatal("hostKeyCallback accepted a connection without a store; it must fail closed")
	}
	if !errors.Is(err, modssh.ErrNoHostKeyVerification) {
		t.Errorf("got %v, want ErrNoHostKeyVerification", err)
	}
}
