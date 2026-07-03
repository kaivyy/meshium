package shared

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

const (
	argon2SaltLength  = 16
	argon2MemoryKiB   = 64 * 1024
	argon2Iterations  = 3
	argon2Parallelism = 2
	argon2KeyLength   = 32
)

// Encrypt encrypts plaintext using AES-256-GCM with the given key.
// Returns nonce prepended to ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return append(nonce, ciphertext...), nil
}

// Decrypt decrypts ciphertext (nonce-prepended) using AES-256-GCM.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

// HashPassword hashes a password using Argon2id.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism, argon2KeyLength)
	return fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2MemoryKiB,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword compares a password against a stored hash.
// It supports the new Argon2id format, legacy PBKDF2 hashes, and bcrypt hashes.
func VerifyPassword(password, hash string) bool {
	switch {
	case strings.HasPrefix(hash, "argon2id$"):
		return verifyArgon2IDPassword(password, hash)
	case strings.HasPrefix(hash, "pbkdf2$") || strings.HasPrefix(hash, "pbkdf2-") || strings.HasPrefix(hash, "pbkdf2_"):
		return verifyPBKDF2Password(password, hash)
	default:
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
}

func verifyArgon2IDPassword(password, hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}

	var memoryKiB uint32
	var iterations uint32
	var parallelism uint8

	for _, param := range strings.Split(parts[2], ",") {
		key, value, ok := strings.Cut(param, "=")
		if !ok {
			return false
		}

		switch key {
		case "m":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return false
			}
			memoryKiB = uint32(n)
		case "t":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return false
			}
			iterations = uint32(n)
		case "p":
			n, err := strconv.ParseUint(value, 10, 8)
			if err != nil {
				return false
			}
			parallelism = uint8(n)
		default:
			return false
		}
	}

	if memoryKiB == 0 || iterations == 0 || parallelism == 0 {
		return false
	}
	if memoryKiB < 8*uint32(parallelism) {
		return false
	}

	salt, err := decodeBase64Component(parts[3])
	if err != nil {
		return false
	}
	storedHash, err := decodeBase64Component(parts[4])
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, parallelism, uint32(len(storedHash)))
	return subtle.ConstantTimeCompare(computed, storedHash) == 1
}

func verifyPBKDF2Password(password, hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) == 0 {
		return false
	}

	if parts[0] == "" {
		parts = parts[1:]
	}
	if len(parts) < 4 {
		return false
	}

	idx := 0
	variant := parts[idx]
	idx++

	algorithm := "sha256"
	switch {
	case variant == "pbkdf2":
		if idx < len(parts) && isHashAlgorithm(parts[idx]) {
			algorithm = parts[idx]
			idx++
		}
	case strings.HasPrefix(variant, "pbkdf2-"):
		algorithm = strings.TrimPrefix(variant, "pbkdf2-")
	case strings.HasPrefix(variant, "pbkdf2_"):
		algorithm = strings.TrimPrefix(variant, "pbkdf2_")
	default:
		return false
	}

	if algorithm != "sha256" {
		return false
	}
	if idx >= len(parts) {
		return false
	}

	iterations, err := strconv.Atoi(parts[idx])
	if err != nil || iterations <= 0 {
		return false
	}
	idx++
	if idx+1 >= len(parts) {
		return false
	}

	salt, err := decodeBase64Component(parts[idx])
	if err != nil {
		return false
	}
	storedHash, err := decodeBase64Component(parts[idx+1])
	if err != nil {
		return false
	}

	computed := pbkdf2.Key([]byte(password), salt, iterations, len(storedHash), sha256.New)
	return subtle.ConstantTimeCompare(computed, storedHash) == 1
}

func isHashAlgorithm(value string) bool {
	switch value {
	case "sha1", "sha256", "sha512":
		return true
	default:
		return false
	}
}

func decodeBase64Component(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

// DeriveKey derives a 32-byte AES key from a password and salt using PBKDF2.
// Callers are responsible for zeroing the returned key once they are done with it.
func DeriveKey(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, 600000, 32, sha256.New)
}
