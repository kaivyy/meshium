package auth

import (
	"database/sql"
	"strings"
	"testing"

	"meshium/internal/db"
	"meshium/internal/shared"
)

func setupTestDB(t *testing.T) *sql.DB {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return d
}

func TestSetupCreatesMasterPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	// Before setup
	setup, _ := svc.IsSetup()
	if setup {
		t.Error("should not be setup before Setup()")
	}

	// Setup
	err := svc.Setup("my-password")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// After setup
	setup, _ = svc.IsSetup()
	if !setup {
		t.Error("should be setup after Setup()")
	}

	// Should be unlocked after setup
	if svc.IsLocked() {
		t.Error("should be unlocked after Setup()")
	}
}

func TestSetupRejectsSecondCall(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if err := svc.Setup("first-password"); err != nil {
		t.Fatalf("first Setup failed: %v", err)
	}

	if err := svc.Setup("second-password"); err == nil {
		t.Fatal("second Setup should fail once master password is already set")
	}
}

func TestUnlockWithCorrectPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")
	svc.Lock()

	if !svc.IsLocked() {
		t.Error("should be locked after Lock()")
	}

	err := svc.Unlock("my-password")
	if err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	if svc.IsLocked() {
		t.Error("should be unlocked after correct Unlock()")
	}
}

func TestUnlockWithWrongPassword(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")
	svc.Lock()

	err := svc.Unlock("wrong-password")
	if err == nil {
		t.Error("Unlock with wrong password should fail")
	}
}

func TestGetAESKey(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	svc.Setup("my-password")

	key := svc.GetAESKey()
	if len(key) != 32 {
		t.Errorf("expected 32-byte AES key, got %d", len(key))
	}
}

func TestLockClearsKey(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if err := svc.Setup("my-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	key := svc.aesKey
	if len(key) != 32 {
		t.Fatalf("expected 32-byte AES key before Lock(), got %d", len(key))
	}

	svc.Lock()

	for i, b := range key {
		if b != 0 {
			t.Fatalf("AES key byte %d was not wiped: got %d", i, b)
		}
	}

	if got := svc.GetAESKey(); got != nil {
		t.Error("AES key should be nil after Lock()")
	}
}

func TestSetupGeneratesAndRegeneratesSSHKey(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if err := svc.Setup("my-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	public1, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(public1), "ssh-rsa") {
		t.Fatalf("expected ssh-rsa public key, got %q", public1)
	}

	encrypted1, err := repo.GetEncryptedSSHKey()
	if err != nil {
		t.Fatalf("GetEncryptedSSHKey failed: %v", err)
	}
	decrypted1, err := shared.Decrypt(svc.GetAESKey(), []byte(encrypted1))
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !strings.Contains(string(decrypted1), "RSA PRIVATE KEY") {
		t.Fatalf("expected PEM private key, got %q", string(decrypted1))
	}

	public2, err := svc.RegenerateSSHKey()
	if err != nil {
		t.Fatalf("RegenerateSSHKey failed: %v", err)
	}
	if public2 == public1 {
		t.Fatal("expected a new public key after regeneration")
	}

	storedPublic, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if storedPublic != public2 {
		t.Fatalf("expected stored public key to match regenerated key")
	}

	encrypted2, err := repo.GetEncryptedSSHKey()
	if err != nil {
		t.Fatalf("GetEncryptedSSHKey failed: %v", err)
	}
	decrypted2, err := shared.Decrypt(svc.GetAESKey(), []byte(encrypted2))
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !strings.Contains(string(decrypted2), "RSA PRIVATE KEY") {
		t.Fatalf("expected PEM private key after regeneration, got %q", string(decrypted2))
	}
}
