package ssh

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// KeyType identifies the algorithm used to generate an SSH key pair.
type KeyType string

const (
	KeyTypeRSA     KeyType = "rsa"
	KeyTypeED25519 KeyType = "ed25519"
	KeyTypeECDSA   KeyType = "ecdsa"
)

// GenerateKeyPair creates a new RSA 4096-bit key pair.
// It returns the private key as PEM and the public key in authorized_keys format.
// This is a backward-compatible wrapper around GenerateKeyPairWithAlgorithm.
func GenerateKeyPair() (privatePEM, publicSSH []byte, err error) {
	return GenerateKeyPairWithAlgorithm(string(KeyTypeRSA))
}

// GenerateKeyPairWithAlgorithm creates a new key pair using the specified algorithm.
// algorithm: "rsa" (4096-bit), "ed25519", "ecdsa" (P-256)
// Returns private key as PEM and public key in authorized_keys format.
// Defaults to RSA if the algorithm is empty or unrecognized.
func GenerateKeyPairWithAlgorithm(algorithm string) (privatePEM, publicSSH []byte, err error) {
	switch KeyType(algorithm) {
	case KeyTypeED25519:
		return generateED25519KeyPair()
	case KeyTypeECDSA:
		return generateECDSAKeyPair()
	default:
		return generateRSAKeyPair()
	}
}

func generateRSAKeyPair() (privatePEM, publicSSH []byte, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	privateKeyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyDER,
	})

	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicSSH = ssh.MarshalAuthorizedKey(publicKey)

	return privatePEM, publicSSH, nil
}

func generateED25519KeyPair() (privatePEM, publicSSH []byte, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	// Marshal as PKCS8 for broad compatibility
	keyDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, nil, err
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	publicKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, nil, err
	}
	publicSSH = ssh.MarshalAuthorizedKey(publicKey)

	return privatePEM, publicSSH, nil
}

func generateECDSAKeyPair() (privatePEM, publicSSH []byte, err error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicSSH = ssh.MarshalAuthorizedKey(publicKey)

	return privatePEM, publicSSH, nil
}

// FingerprintSHA256 returns the SHA256 fingerprint of a public key.
// Format: "SHA256:base64encodedhash"
func FingerprintSHA256(publicKey []byte) (string, error) {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}
	return ssh.FingerprintSHA256(parsed), nil
}

// FingerprintMD5 returns the MD5 fingerprint of a public key.
// Format: "MD5:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx:xx"
func FingerprintMD5(publicKey []byte) (string, error) {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}
	return "MD5:" + ssh.FingerprintLegacyMD5(parsed), nil
}

// IsKeyInstalled checks if a public key is already in the remote authorized_keys file.
// It compares parsed key blobs (not raw strings) to be resilient to comments
// and formatting differences.
func IsKeyInstalled(client *Client, publicKey []byte) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("client is nil")
	}

	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return false, fmt.Errorf("parse public key: %w", err)
	}
	targetBlob := parsed.Marshal()

	stdout, _, _, err := client.Exec("cat ~/.ssh/authorized_keys 2>/dev/null || true")
	if err != nil {
		return false, fmt.Errorf("read authorized_keys: %w", err)
	}

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			continue // skip unparseable lines
		}
		if string(parsed.Marshal()) == string(targetBlob) {
			return true, nil
		}
	}

	return false, nil
}

// InstallPublicKey appends a public key to the remote authorized_keys file.
// This operation is idempotent: if the key is already present, it returns nil
// without modifying the file.
func InstallPublicKey(client *Client, publicKey []byte) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Check if already installed (idempotent)
	installed, err := IsKeyInstalled(client, publicKey)
	if err != nil {
		// If we can't check, proceed with installation anyway
		installed = false
	}
	if installed {
		return nil
	}

	key := strings.TrimSpace(string(publicKey))
	// Use base64 encoding to safely write the key without heredoc injection risk
	encoded := base64.StdEncoding.EncodeToString([]byte(key))
	cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo '%s' | base64 -d >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`, encoded)

	stdout, stderr, exitCode, err := client.Exec(cmd)
	if err != nil {
		return fmt.Errorf("failed to install public key: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("install public key failed: %s %s", stderr, stdout)
	}
	return nil
}

// RemovePublicKey removes a public key from the remote authorized_keys file.
// It writes the filtered content atomically via a temp file to avoid corruption.
func RemovePublicKey(client *Client, publicKey []byte) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	targetBlob := string(parsed.Marshal())

	stdout, _, _, err := client.Exec("cat ~/.ssh/authorized_keys 2>/dev/null || true")
	if err != nil {
		return fmt.Errorf("read authorized_keys: %w", err)
	}

	var filteredLines []string
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
		if err != nil {
			// Keep unparseable lines (don't delete what we can't understand)
			filteredLines = append(filteredLines, line)
			continue
		}
		if string(parsed.Marshal()) != targetBlob {
			filteredLines = append(filteredLines, line)
		}
	}

	filteredContent := strings.Join(filteredLines, "\n")
	if len(filteredLines) > 0 {
		filteredContent += "\n"
	}

	// Write atomically via base64-encoded temp file
	encoded := base64.StdEncoding.EncodeToString([]byte(filteredContent))
	cmd := fmt.Sprintf(`echo '%s' | base64 -d > ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`, encoded)

	stdout, stderr, exitCode, err := client.Exec(cmd)
	if err != nil {
		return fmt.Errorf("failed to write authorized_keys: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("remove public key failed: %s %s", stderr, stdout)
	}
	return nil
}
