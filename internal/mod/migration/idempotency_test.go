package migration

import (
	"context"
	"testing"
)

// TestIdempotencyKeyRoundTrips verifies a keyed audit entry is retrievable by
// its idempotency key, and that an unknown key returns nil (no false hit).
func TestIdempotencyKeyRoundTrips(t *testing.T) {
	repo := newTestRepo(t).(*sqliteRepo)
	seedServers(t, repo)
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"docker"}, "")
	if err != nil {
		t.Fatalf("seed migration: %v", err)
	}

	// Write a terminal audit entry under a client-supplied idempotency key.
	if _, err := repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID:   migID,
		EventType:     "migration_paused",
		NewState:      "paused",
		Actor:         "operator",
		IdempotencyKey: "idem-abc-123",
		Result:        "success",
	}); err != nil {
		t.Fatalf("create audit: %v", err)
	}

	got, err := repo.GetAuditEntryByIdempotencyKey(ctx, migID, "idem-abc-123")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Fatal("expected audit entry for idempotency key, got nil")
	}
	if got.Result != "success" || got.NewState != "paused" {
		t.Fatalf("lookup returned wrong entry: %+v", got)
	}

	none, err := repo.GetAuditEntryByIdempotencyKey(ctx, migID, "does-not-exist")
	if err != nil {
		t.Fatalf("lookup missing: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for unknown key, got %+v", none)
	}
}

// TestAuditInheritsIdempotencyKey proves the CreateAuditEntry chokepoint
// derives the idempotency key from context when the caller did not set it —
// so a REST mutation carrying Idempotency-Key records it on its audit row
// without every call site threading it manually.
func TestAuditInheritsIdempotencyKey(t *testing.T) {
	repo := newTestRepo(t).(*sqliteRepo)
	seedServers(t, repo)
	ctx := context.Background()

	migID, err := repo.CreateMigration(1, 2, []string{"docker"}, "")
	if err != nil {
		t.Fatalf("seed migration: %v", err)
	}

	ctx = WithIdempotencyKey(ctx, "idem-from-ctx")
	if _, err := repo.CreateAuditEntry(ctx, AuditEntry{
		MigrationID: migID,
		EventType:   "migration_resumed",
		Actor:       "operator",
	}); err != nil {
		t.Fatalf("create audit: %v", err)
	}

	got, err := repo.GetAuditEntryByIdempotencyKey(ctx, migID, "idem-from-ctx")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.IdempotencyKey != "idem-from-ctx" {
		t.Fatalf("idempotency key not inherited from ctx: %+v", got)
	}
}
