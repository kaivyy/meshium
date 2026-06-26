package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// GenerateKeyPair creates a new RSA 4096-bit key pair.
// It returns the private key as PEM and the public key in authorized_keys format.
func GenerateKeyPair() (privatePEM, publicSSH []byte, err error) {
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

// InstallPublicKey appends a public key to the remote authorized_keys file.
func InstallPublicKey(client *Client, publicKey []byte) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	key := strings.TrimSpace(string(publicKey))
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
