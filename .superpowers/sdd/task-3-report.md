# Task 3 Report: Crypto Package

## What I implemented

Added `internal/shared/crypto.go` with the crypto helpers required by the task:

- `Encrypt(key, plaintext) ([]byte, error)` using AES-256-GCM with a random nonce prepended to the ciphertext
- `Decrypt(key, ciphertext) ([]byte, error)` for nonce-prefixed AES-256-GCM ciphertext
- `HashPassword(password string) (string, error)` using bcrypt
- `VerifyPassword(password, hash string) bool` using bcrypt comparison
- `DeriveKey(password string, salt []byte) []byte` using PBKDF2-SHA256 to produce a 32-byte key

Added `internal/shared/crypto_test.go` with the four requested tests:

- encrypt/decrypt round trip
- different ciphertexts for the same plaintext due to random nonce
- bcrypt hash and verify behavior
- deterministic 32-byte PBKDF2 key derivation

I also updated module dependencies so the package builds cleanly.

## TDD evidence

### RED

Before implementation, `go test ./internal/shared -v` failed as expected because the functions did not exist:

- `undefined: Encrypt`
- `undefined: Decrypt`
- `undefined: HashPassword`
- `undefined: VerifyPassword`
- `undefined: DeriveKey`

### GREEN

After implementing the package, the test suite passed:

- `go test ./internal/shared -v` ✅
- `go test ./...` ✅

## Files changed

- `internal/shared/crypto.go` — added the crypto implementation
- `internal/shared/crypto_test.go` — added the requested TDD tests
- `go.mod` — added `golang.org/x/crypto` and kept the module on Go 1.24.4
- `go.sum` — added dependency checksums

## Self-review findings

- The implementation matches the task brief and uses standard library crypto primitives plus `golang.org/x/crypto` helpers.
- I kept the module on `go 1.24.4` and selected `golang.org/x/crypto v0.48.0` because newer releases required Go 1.25 in this environment.
- The tests cover the intended happy paths and nonce randomness, but they do not add extra edge-case coverage for invalid key lengths or malformed ciphertext beyond the required scope.
- No functional issues were found during review; both package-level and full-repo tests passed.

## Review fixes applied

- Enforced a strict 32-byte key length check in both `Encrypt` and `Decrypt` to avoid accidental AES-128/192 use.
- Documented that callers are responsible for zeroing key material returned by `DeriveKey`.
- Added a test assertion that bcrypt hashes use cost 14 (`$2a$14$`) and added invalid-key-length coverage for the AES helpers.

## Verification

- `go test ./internal/shared/ -v` ✅
