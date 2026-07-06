package ssh

import (
	"net"
	"testing"
)

func listenForTest(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("socket listen unavailable in this environment: %v", err)
	}
	return listener
}

func requireSocketForTest(t *testing.T) {
	t.Helper()
	listener := listenForTest(t)
	_ = listener.Close()
}
