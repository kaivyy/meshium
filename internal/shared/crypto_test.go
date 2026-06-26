package shared

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes for AES-256
	plaintext := []byte("super-secret-password")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := []byte("same-input")

	ct1, _ := Encrypt(key, plaintext)
	ct2, _ := Encrypt(key, plaintext)

	if bytes.Equal(ct1, ct2) {
		t.Error("ciphertext should differ due to random nonce")
	}
}

func TestEncryptDecryptRejectInvalidKeyLengths(t *testing.T) {
	shortKey := []byte("0123456789abcdef")

	if _, err := Encrypt(shortKey, []byte("plaintext")); err == nil {
		t.Fatal("Encrypt should reject keys that are not 32 bytes")
	}

	if _, err := Decrypt(shortKey, []byte("ciphertext")); err == nil {
		t.Fatal("Decrypt should reject keys that are not 32 bytes")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	password := "my-master-password"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !strings.HasPrefix(hash, "$2a$14$") {
		t.Fatalf("expected bcrypt hash with cost 14, got %q", hash)
	}

	if !VerifyPassword(password, hash) {
		t.Error("VerifyPassword should return true for correct password")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Error("VerifyPassword should return false for wrong password")
	}
}

func TestDeriveKey(t *testing.T) {
	password := "my-master-password"
	salt := []byte("0123456789abcdef")

	key := DeriveKey(password, salt)

	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}

	// Same input should produce same key
	key2 := DeriveKey(password, salt)
	if !bytes.Equal(key, key2) {
		t.Error("DeriveKey should be deterministic")
	}
}
