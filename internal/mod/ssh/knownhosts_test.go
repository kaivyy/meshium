package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"errors"
	"net"
	"strconv"
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

func newKnownHostsTestStore(t *testing.T) (*KnownHostsStore, *sql.DB) {
	t.Helper()

	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	return NewKnownHostsStore(d), d
}

func insertTestServer(t *testing.T, d *sql.DB, host string, port int, username string) int {
	t.Helper()

	res, err := d.Exec(
		`INSERT INTO servers (name, host, port, username) VALUES (?, ?, ?, ?)`,
		"test-server",
		host,
		port,
		username,
	)
	if err != nil {
		t.Fatalf("insert server failed: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}
	return int(id)
}

func TestKnownHostsUnknownHostReturnsNotTrusted(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	_, pubKey := makeTestAuthorizedKey(t)
	callback := store.MakeHostKeyCallback(1)
	if err := callback("example.com", &net.TCPAddr{Port: 22}, pubKey); !errors.Is(err, ErrHostKeyNotTrusted) {
		t.Fatalf("expected ErrHostKeyNotTrusted, got %v", err)
	}
}

func TestKnownHostsMatchingHostKeyIsAccepted(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	keyString, pubKey := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 2222, keyString, 9); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	callback := store.MakeHostKeyCallback(9)
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, pubKey); err != nil {
		t.Fatalf("callback rejected matching host key: %v", err)
	}
}

func TestKnownHostsMismatchedHostKeyReturnsMismatch(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	keyString, _ := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 2222, keyString, 9); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	_, mismatchPub := makeTestAuthorizedKey(t)
	callback := store.MakeHostKeyCallback(9)
	if err := callback("example.com", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, mismatchPub); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("expected ErrHostKeyMismatch, got %v", err)
	}
}

// Reproduces the first-connection failure: TrustHostKey stores the key under
// the bare host (Save(host, port)), but the SSH library invokes the callback
// with hostname as "host:port". The callback must strip the port to match.
// Before the fix this returned ErrHostKeyNotTrusted after trusting.
func TestKnownHostsCallbackStripsPortFromHostname(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	keyString, pubKey := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 2222, keyString, 9); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	callback := store.MakeHostKeyCallback(9)
	if err := callback("example.com:2222", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, pubKey); err != nil {
		t.Fatalf("callback rejected host key addressed as host:port: %v", err)
	}
}

func TestKnownHostsTrustHostKeySavesAndReturnsFingerprint(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	addr := startPoolTestSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort failed: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	serverID := insertTestServer(t, d, host, port, "test")

	trusted, err := store.IsTrusted(host, port)
	if err != nil {
		t.Fatalf("IsTrusted before trust failed: %v", err)
	}
	if trusted {
		t.Fatal("expected server to be untrusted before TrustHostKey")
	}

	fingerprint, err := store.TrustHostKey(serverID)
	if err != nil {
		t.Fatalf("TrustHostKey failed: %v", err)
	}
	if fingerprint == "" {
		t.Fatal("expected non-empty fingerprint")
	}

	storedFingerprint, err := store.GetFingerprint(serverID)
	if err != nil {
		t.Fatalf("GetFingerprint failed: %v", err)
	}
	if storedFingerprint != fingerprint {
		t.Fatalf("expected fingerprint %q, got %q", fingerprint, storedFingerprint)
	}

	trusted, err = store.IsTrusted(host, port)
	if err != nil {
		t.Fatalf("IsTrusted after trust failed: %v", err)
	}
	if !trusted {
		t.Fatal("expected server to be trusted after TrustHostKey")
	}

	keyText, known, err := store.Get(host, port)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !known {
		t.Fatal("expected known host entry after TrustHostKey")
	}
	if keyText == "" {
		t.Fatal("expected stored host key")
	}
}

func TestKnownHostsGetFingerprintReturnsStoredFingerprint(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	keyString, pubKey := makeTestAuthorizedKey(t)
	serverID := insertTestServer(t, d, "example.com", 22, "root")
	if err := store.Save("example.com", 22, keyString, serverID); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	fingerprint, err := store.GetFingerprint(serverID)
	if err != nil {
		t.Fatalf("GetFingerprint failed: %v", err)
	}
	if fingerprint != cryptossh.FingerprintSHA256(pubKey) {
		t.Fatalf("expected fingerprint %q, got %q", cryptossh.FingerprintSHA256(pubKey), fingerprint)
	}
}

func TestKnownHostsIsTrustedReturnsCorrectBool(t *testing.T) {
	store, d := newKnownHostsTestStore(t)
	defer d.Close()

	trusted, err := store.IsTrusted("missing.example.com", 22)
	if err != nil {
		t.Fatalf("IsTrusted failed: %v", err)
	}
	if trusted {
		t.Fatal("expected missing host to be untrusted")
	}

	keyString, _ := makeTestAuthorizedKey(t)
	if err := store.Save("example.com", 22, keyString, 1); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	trusted, err = store.IsTrusted("example.com", 22)
	if err != nil {
		t.Fatalf("IsTrusted failed: %v", err)
	}
	if !trusted {
		t.Fatal("expected saved host to be trusted")
	}
}
