package ssh

import (
	"crypto/rsa"
	"strings"
	"testing"

	cryptossh "golang.org/x/crypto/ssh"
)

func TestGenerateKeyPair(t *testing.T) {
	privatePEM, publicSSH, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if len(privatePEM) == 0 {
		t.Error("private key should not be empty")
	}
	if len(publicSSH) == 0 {
		t.Error("public key should not be empty")
	}

	if !strings.HasPrefix(string(publicSSH), "ssh-rsa") {
		t.Error("public key should start with 'ssh-rsa'")
	}
	if !strings.Contains(string(privatePEM), "RSA PRIVATE KEY") {
		t.Error("private key should contain 'RSA PRIVATE KEY'")
	}
}

func TestGenerateKeyPairIsRSA4096(t *testing.T) {
	privatePEM, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	key, err := cryptossh.ParseRawPrivateKey(privatePEM)
	if err != nil {
		t.Fatalf("ParseRawPrivateKey failed: %v", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("key should be RSA")
	}
	if rsaKey.N.BitLen() != 4096 {
		t.Errorf("expected 4096-bit key, got %d", rsaKey.N.BitLen())
	}
}
