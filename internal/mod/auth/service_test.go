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

	svc.Setup("my-password")
	svc.Lock()

	key := svc.GetAESKey()
	if key != nil {
		t.Error("AES key should be nil after Lock()")
	}
}
