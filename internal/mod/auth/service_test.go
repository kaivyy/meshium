package auth

import (
	"database/sql"
	"testing"

	"meshium/internal/db"
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
