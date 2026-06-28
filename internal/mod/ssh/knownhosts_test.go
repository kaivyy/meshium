package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"testing"

	"meshium/internal/db"

	cryptossh "golang.org/x/crypto/ssh"
)

func makeTestAuthorizedKey(t *testing.T) (string, cryptossh.PublicKey) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	pubKey, err := cryptossh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("NewPublicKey failed: %v", err)
	}

	return string(cryptossh.MarshalAuthorizedKey(pubKey)), pubKey
}

func TestKnownHostsSaveAndGet(t *testing.T) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	store := NewKnownHostsStore(d)

	_, known, err := store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if known {
		t.Error("host should not be known initially")
	}

	if err := store.Save("example.com", 22, "ssh-rsa AAAA...", 1); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	key, known, err := store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !known {
		t.Error("host should be known after Save")
	}
	if key != "ssh-rsa AAAA..." {
		t.Errorf("expected %q, got %q", "ssh-rsa AAAA...", key)
	}
}

func TestKnownHostsHostKeyCallback(t *testing.T) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	store := NewKnownHostsStore(d)
	keyString, pubKey := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 2222, keyString, 9); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	callback := store.MakeHostKeyCallback(9)
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, pubKey); err != nil {
		t.Fatalf("callback rejected matching host key: %v", err)
	}

	_, mismatchPub := makeTestAuthorizedKey(t)
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, mismatchPub); err == nil {
		t.Fatal("expected callback to reject mismatched host key")
	}

	// Unknown host should be auto-accepted (not rejected)
	if err := callback("unknown.example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}, pubKey); err != nil {
		t.Fatalf("expected callback to auto-accept unknown host, got error: %v", err)
	}
}
