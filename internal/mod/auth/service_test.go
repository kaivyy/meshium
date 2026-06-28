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

	token, err := svc.Unlock("my-password")
	if err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
	if token == "" {
		t.Error("Unlock should return a non-empty session token")
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

	_, err := svc.Unlock("wrong-password")
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

func TestSetupIsAtomicOnSSHKeyFailure(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if _, err := d.Exec(`
		CREATE TRIGGER reject_ssh_key_public_insert
		BEFORE INSERT ON app_config
		WHEN NEW.key = 'ssh_key_public'
		BEGIN
			SELECT RAISE(ABORT, 'reject ssh public key write');
		END;
	`); err != nil {
		t.Fatalf("create insert trigger failed: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TRIGGER reject_ssh_key_public_update
		BEFORE UPDATE ON app_config
		WHEN NEW.key = 'ssh_key_public'
		BEGIN
			SELECT RAISE(ABORT, 'reject ssh public key write');
		END;
	`); err != nil {
		t.Fatalf("create update trigger failed: %v", err)
	}

	if err := svc.Setup("my-password"); err == nil {
		t.Fatal("Setup should fail when SSH key storage is rejected")
	}

	setup, err := repo.HasMasterPassword()
	if err != nil {
		t.Fatalf("HasMasterPassword failed: %v", err)
	}
	if setup {
		t.Fatal("master password should not be persisted after failed setup")
	}

	encrypted, err := repo.GetEncryptedSSHKey()
	if err != nil {
		t.Fatalf("GetEncryptedSSHKey failed: %v", err)
	}
	if encrypted != "" {
		t.Fatal("encrypted SSH key should not be persisted after failed setup")
	}

	public, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if public != "" {
		t.Fatal("public SSH key should not be persisted after failed setup")
	}

	if !svc.IsLocked() {
		t.Fatal("service should remain locked after failed setup")
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

func TestRegenerateSSHKeyIsAtomic(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepo(d)
	svc := NewService(repo)

	if err := svc.Setup("my-password"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	originalPublic, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	originalEncrypted, err := repo.GetEncryptedSSHKey()
	if err != nil {
		t.Fatalf("GetEncryptedSSHKey failed: %v", err)
	}

	if _, err := d.Exec(`
		CREATE TRIGGER reject_ssh_key_public_insert
		BEFORE INSERT ON app_config
		WHEN NEW.key = 'ssh_key_public'
		BEGIN
			SELECT RAISE(ABORT, 'reject ssh public key write');
		END;
	`); err != nil {
		t.Fatalf("create insert trigger failed: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TRIGGER reject_ssh_key_public_update
		BEFORE UPDATE ON app_config
		WHEN NEW.key = 'ssh_key_public'
		BEGIN
			SELECT RAISE(ABORT, 'reject ssh public key write');
		END;
	`); err != nil {
		t.Fatalf("create update trigger failed: %v", err)
	}

	if _, err := svc.RegenerateSSHKey(); err == nil {
		t.Fatal("expected regeneration to fail when public key write is rejected")
	}

	publicAfter, err := repo.GetSSHPublicKey()
	if err != nil {
		t.Fatalf("GetSSHPublicKey failed: %v", err)
	}
	if publicAfter != originalPublic {
		t.Fatalf("expected public key to remain unchanged after rollback")
	}

	encryptedAfter, err := repo.GetEncryptedSSHKey()
	if err != nil {
		t.Fatalf("GetEncryptedSSHKey failed: %v", err)
	}
	if encryptedAfter != originalEncrypted {
		t.Fatalf("expected encrypted private key to remain unchanged after rollback")
	}
}
