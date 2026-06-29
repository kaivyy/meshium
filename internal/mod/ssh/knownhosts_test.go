package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
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

func createTestServer(t *testing.T, d *sql.DB) int {
	t.Helper()

	res, err := d.Exec(`INSERT INTO servers (name, host, username) VALUES (?, ?, ?)`, "server-1", "example.com", "root")
	if err != nil {
		t.Fatalf("create server failed: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}
	return int(id)
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
	serverID := createTestServer(t, d)

	_, known, err := store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if known {
		t.Error("host should not be known initially")
	}

	if err := store.AddServerKey(serverID, "example.com", 22, "ssh-rsa AAAA..."); err != nil {
		t.Fatalf("AddServerKey failed: %v", err)
	}

	key, known, err := store.GetServerKey(serverID)
	if err != nil {
		t.Fatalf("GetServerKey failed: %v", err)
	}
	if !known {
		t.Error("server key should be known after AddServerKey")
	}
	if key != "ssh-rsa AAAA..." {
		t.Errorf("expected %q, got %q", "ssh-rsa AAAA...", key)
	}

	key, known, err = store.Get("example.com", 22)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !known {
		t.Error("host should be known after AddServerKey")
	}
	if key != "ssh-rsa AAAA..." {
		t.Errorf("expected %q, got %q", "ssh-rsa AAAA...", key)
	}

	if err := store.RemoveServerKey(serverID); err != nil {
		t.Fatalf("RemoveServerKey failed: %v", err)
	}

	_, known, err = store.GetServerKey(serverID)
	if err != nil {
		t.Fatalf("GetServerKey after remove failed: %v", err)
	}
	if known {
		t.Error("server key should be removed after RemoveServerKey")
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
	serverID := createTestServer(t, d)
	keyString, pubKey := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 2222, keyString, serverID); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	callback := store.MakeHostKeyCallback()
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, pubKey); err != nil {
		t.Fatalf("callback rejected matching host key: %v", err)
	}

	_, mismatchPub := makeTestAuthorizedKey(t)
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, mismatchPub); err == nil {
		t.Fatal("expected callback to reject mismatched host key")
	}

	if err := callback("unknown.example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}, pubKey); err == nil {
		t.Fatal("expected callback to reject unknown host")
	}
}
