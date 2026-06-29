package shared

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
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

	if !strings.HasPrefix(hash, "argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("expected argon2id hash with embedded parameters, got %q", hash)
	}

	if !VerifyPassword(password, hash) {
		t.Error("VerifyPassword should return true for correct password")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Error("VerifyPassword should return false for wrong password")
	}

	legacyBcrypt, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	if err != nil {
		t.Fatalf("GenerateFromPassword failed: %v", err)
	}
	if !VerifyPassword(password, string(legacyBcrypt)) {
		t.Error("VerifyPassword should accept legacy bcrypt hashes")
	}

	salt := []byte("0123456789abcdef")
	legacyPBKDF2 := "pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(DeriveKey(password, salt))
	if !VerifyPassword(password, legacyPBKDF2) {
		t.Error("VerifyPassword should accept legacy PBKDF2 hashes")
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

func TestCheckWebSocketOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	req.Host = "example.com"

	if !CheckWebSocketOrigin(req) {
		t.Fatal("expected missing Origin header to be allowed")
	}

	req.Header.Set("Origin", "http://example.com")
	if !CheckWebSocketOrigin(req) {
		t.Fatal("expected same-origin websocket request to be allowed")
	}

	req.Header.Set("Origin", "http://evil.example")
	if CheckWebSocketOrigin(req) {
		t.Fatal("expected cross-origin websocket request to be rejected")
	}

	req.Header.Set("Origin", "http://[")
	if CheckWebSocketOrigin(req) {
		t.Fatal("expected malformed origin to be rejected")
	}
}
