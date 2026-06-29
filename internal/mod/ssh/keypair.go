package ssh

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

// GenerateKeyPair creates a new RSA 4096-bit key pair.
// It returns the private key as PEM and the public key in authorized_keys format.
func GenerateKeyPair() (privatePEM, publicSSH []byte, err error) {
	return GenerateKeyPairWithAlgorithm(KeyTypeRSA)
}

// GenerateKeyPairWithAlgorithm creates a key pair of the specified type.
// Supported types: "rsa" (4096-bit), "ed25519", "ecdsa" (P-256).
func GenerateKeyPairWithAlgorithm(keyType string) (privatePEM, publicSSH []byte, err error) {
	switch strings.ToLower(keyType) {
	case KeyTypeED25519:
		return generateED25519KeyPair()
	case KeyTypeECDSA:
		return generateECDSAKeyPair()
	case KeyTypeRSA, "":
		return generateRSAKeyPair()
	default:
		return nil, nil, fmt.Errorf("unsupported key type: %s", keyType)
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
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	// Encode private key in OpenSSH PEM format
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyDER,
	})

	publicKey, err := ssh.NewPublicKey(pub)
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

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyDER,
	})

	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicSSH = ssh.MarshalAuthorizedKey(publicKey)
	return privatePEM, publicSSH, nil
}

// FingerprintSHA256 returns the SHA256 fingerprint of a public key.
func FingerprintSHA256(pubKey ssh.PublicKey) (string, error) {
	if pubKey == nil {
		return "", fmt.Errorf("public key is nil")
	}
	return ssh.FingerprintSHA256(pubKey), nil
}

// FingerprintMD5 returns the MD5 fingerprint of a public key.
func FingerprintMD5(pubKey ssh.PublicKey) (string, error) {
	if pubKey == nil {
		return "", fmt.Errorf("public key is nil")
	}
	return ssh.FingerprintLegacyMD5(pubKey), nil
}

// ParsePublicKey parses an authorized_keys format public key and returns the ssh.PublicKey.
func ParsePublicKey(authorizedKey []byte) (ssh.PublicKey, error) {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(authorizedKey)
	return pubKey, err
}

// PublicKeyFromPrivateKey derives the public key from a private key PEM.
func PublicKeyFromPrivateKey(privatePEM []byte, passphrase string) (ssh.PublicKey, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(privatePEM, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(privatePEM)
	}
	if err != nil {
		return nil, err
	}
	return signer.PublicKey(), nil
}

// KeyType returns the SSH key type string (e.g. "ssh-rsa", "ssh-ed25519", "ecdsa-sha2-nistp256").
func KeyTypeString(pubKey ssh.PublicKey) string {
	if pubKey == nil {
		return ""
	}
	return pubKey.Type()
}

// InstallPublicKey appends a public key to the remote authorized_keys file.
// It is idempotent: if the key is already present, it does nothing.
func InstallPublicKey(client *Client, publicKey []byte) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	key := strings.TrimSpace(string(publicKey))
	if key == "" {
		return fmt.Errorf("public key is empty")
	}

	// Idempotent: check if key already exists before appending
	checkCmd := fmt.Sprintf(`grep -qF '%s' ~/.ssh/authorized_keys 2>/dev/null`, key)
	_, _, exitCode, _ := client.Exec(checkCmd)
	if exitCode == 0 {
		return nil // Key already installed
	}

	cmd := fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && cat <<'EOF' >> ~/.ssh/authorized_keys
%s
EOF
chmod 600 ~/.ssh/authorized_keys`, key)

	stdout, stderr, exitCode, err := client.Exec(cmd)
	if err != nil {
		return fmt.Errorf("failed to install public key: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("install public key failed: %s %s", stderr, stdout)
	}
	return nil
}

// RemovePublicKey removes a specific public key from the remote authorized_keys file.
func RemovePublicKey(client *Client, publicKey []byte) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	key := strings.TrimSpace(string(publicKey))
	if key == "" {
		return fmt.Errorf("public key is empty")
	}

	// Use sed to remove the line matching the key
	cmd := fmt.Sprintf(`sed -i '/%s/d' ~/.ssh/authorized_keys 2>/dev/null; true`,
		strings.ReplaceAll(key, "/", "\\/"))

	_, _, _, err := client.Exec(cmd)
	if err != nil {
		return fmt.Errorf("failed to remove public key: %w", err)
	}
	return nil
}

// IsKeyInstalled checks whether a public key is already in the remote authorized_keys file.
func IsKeyInstalled(client *Client, publicKey []byte) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("client is nil")
	}

	key := strings.TrimSpace(string(publicKey))
	if key == "" {
		return false, fmt.Errorf("public key is empty")
	}

	checkCmd := fmt.Sprintf(`grep -qF '%s' ~/.ssh/authorized_keys 2>/dev/null`, key)
	_, _, exitCode, _ := client.Exec(checkCmd)
	return exitCode == 0, nil
}
