package ssh

import (
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestFailClosedHostKeyCallbackRejects verifies the fail-closed callback rejects
// every host key with the actionable ErrNoHostKeyVerification, never accepting.
func TestFailClosedHostKeyCallbackRejects(t *testing.T) {
	cb := FailClosedHostKeyCallback()
	_, pub := makeTestAuthorizedKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22}

	err := cb("host", addr, pub)
	if err == nil {
		t.Fatal("fail-closed callback accepted a host key; it must reject")
	}
	if !errors.Is(err, ErrNoHostKeyVerification) {
		t.Errorf("got %v, want ErrNoHostKeyVerification", err)
	}
}

// TestWizardGetHostKeyCallbackFailsClosedWithoutStore ensures a wizard with no
// known-hosts store does NOT fall back to InsecureIgnoreHostKey on a credentialed
// dial: the callback it hands out must reject rather than accept any key.
func TestWizardGetHostKeyCallbackFailsClosedWithoutStore(t *testing.T) {
	w := NewWizardRunner(nil)
	cb := w.getHostKeyCallback(1)
	if cb == nil {
		t.Fatal("getHostKeyCallback returned nil; a nil callback would let ssh.Dial fail unpredictably")
	}

	_, pub := makeTestAuthorizedKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22}
	if err := cb("host", addr, pub); !errors.Is(err, ErrNoHostKeyVerification) {
		t.Errorf("without a store the callback must fail closed; got %v", err)
	}
}

// TestWizardGetHostKeyCallbackVerifiesWithStore ensures that when a store IS
// present, the wizard uses the verifying callback (unknown keys are rejected as
// not-trusted, not silently accepted).
func TestWizardGetHostKeyCallbackVerifiesWithStore(t *testing.T) {
	store, _ := newKnownHostsTestStore(t)
	w := NewWizardRunner(store)
	cb := w.getHostKeyCallback(1)

	_, pub := makeTestAuthorizedKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 2222}
	err := cb("unknown-host", addr, pub)
	if !errors.Is(err, ErrHostKeyNotTrusted) {
		t.Errorf("unknown host with a store present should be rejected as not-trusted; got %v", err)
	}
}

// Compile-time assertion that FailClosedHostKeyCallback satisfies the ssh type.
var _ ssh.HostKeyCallback = FailClosedHostKeyCallback()
