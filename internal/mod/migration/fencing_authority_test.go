package migration

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock lets tests advance time without sleeping.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

// newFenceRepo builds a migrated sqlite DB and returns it as a fenceLeaseRepo.
// A migration row (FK target) is pre-created.
func newFenceRepo(t *testing.T) (fenceLeaseRepo, *fakeClock, *FencingAuthority) {
	t.Helper()
	database, _ := newTestDB(t)
	repo := NewRepo(database)
	sqlite := repo.(*sqliteRepo)
	// Parent rows so the FK constraints pass: source/target servers, then migration.
	for _, sid := range []int{1, 2} {
		if _, err := database.Exec(
			`INSERT INTO servers (id, name, host, port, username) VALUES (?, ?, ?, ?, ?)`,
			sid, "srv", "127.0.0.1", 22, "u",
		); err != nil {
			t.Fatalf("seed server %d: %v", sid, err)
		}
	}
	if _, err := database.Exec(
		`INSERT INTO migrations (id, source_id, target_id, categories, status)
		 VALUES (1, 1, 2, ?, ?)`,
		"postgres", "planned",
	); err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
	auth := &FencingAuthority{repo: sqlite, clock: clock, ttl: FenceTTL}
	return sqlite, clock, auth
}

func TestFenceAcquireThenAssertHolds(t *testing.T) {
	repo, _, auth := newFenceRepo(t)
	_ = repo
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, 1, "meshium-holderA", "FencingSource")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lease.FenceToken != 1 {
		t.Fatalf("token=%d want 1", lease.FenceToken)
	}
	if lease.State != "FencingSource" {
		t.Fatalf("state=%q", lease.State)
	}
	// AssertHolds with matching holder/token/state passes.
	got, err := auth.AssertHolds(ctx, 1, "meshium-holderA", 1, "FencingSource")
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if got.ID != lease.ID {
		t.Fatalf("id mismatch %d != %d", got.ID, lease.ID)
	}
}

func TestFenceAssertRejectsWrongHolder(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	if _, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_, err := auth.AssertHolds(ctx, 1, "meshium-B", 1, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseConflict) {
		t.Fatalf("wrong holder: err=%v want ErrFenceLeaseConflict", err)
	}
}

func TestFenceAssertRejectsWrongToken(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	if _, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_, err := auth.AssertHolds(ctx, 1, "meshium-A", 99, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseConflict) {
		t.Fatalf("wrong token: err=%v want ErrFenceLeaseConflict", err)
	}
}

func TestFenceAssertRejectsMissing(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	_, err := auth.AssertHolds(context.Background(), 1, "meshium-A", 1, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseNotHeld) {
		t.Fatalf("missing: err=%v want ErrFenceLeaseNotHeld", err)
	}
}

func TestFenceAssertRejectsWrongState(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	if _, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := auth.SetState(ctx, &FenceLease{ID: 1, Holder: "meshium-A", FenceToken: 1}, "SwitchingTraffic"); err != nil {
		t.Fatalf("setstate: %v", err)
	}
	_, err := auth.AssertHolds(ctx, 1, "meshium-A", 1, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseNotHeld) {
		t.Fatalf("wrong state: err=%v want ErrFenceLeaseNotHeld", err)
	}
}

func TestFenceAcquireConflictWithActiveLease(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	if _, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource"); err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	_, err := auth.Acquire(ctx, 1, "meshium-B", "FencingSource")
	if !errors.Is(err, ErrFenceLeaseConflict) {
		t.Fatalf("second acquire: err=%v want ErrFenceLeaseConflict", err)
	}
}

func TestFenceAcquireSameHolderIdempotent(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	first, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	second, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if first.ID != second.ID || first.FenceToken != second.FenceToken {
		t.Fatalf("same-holder re-acquire must return same lease: %+v vs %+v", first, second)
	}
}

func TestFenceRenewExtendsExpiry(t *testing.T) {
	_, clock, auth := newFenceRepo(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock.t = clock.t.Add(2 * time.Minute) // within TTL
	if err := auth.Renew(ctx, lease); err != nil {
		t.Fatalf("renew: %v", err)
	}
	// Advance past the original TTL but within the renewed window.
	clock.t = clock.t.Add(4 * time.Minute) // total +6min from acquire; original TTL=5min expired, renewed extends.
	if _, err := auth.AssertHolds(ctx, 1, "meshium-A", 1, "FencingSource"); err != nil {
		t.Fatalf("renewed assert: %v", err)
	}
}

func TestFenceRenewRejectsWrongHolder(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.Holder = "meshium-B"
	if err := auth.Renew(ctx, lease); !errors.Is(err, ErrFenceLeaseNotHeld) {
		t.Fatalf("renew wrong holder: err=%v want ErrFenceLeaseNotHeld", err)
	}
}

func TestFenceStaleExpiredUnreleased(t *testing.T) {
	_, clock, auth := newFenceRepo(t)
	ctx := context.Background()
	if _, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock.t = clock.t.Add(FenceTTL + time.Second) // expired, not released
	_, err := auth.AssertHolds(ctx, 1, "meshium-A", 1, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseStale) {
		t.Fatalf("stale assert: err=%v want ErrFenceLeaseStale", err)
	}
	// Acquire must refuse to silently replace a stale lease.
	_, err = auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if !errors.Is(err, ErrFenceLeaseStale) {
		t.Fatalf("stale acquire: err=%v want ErrFenceLeaseStale", err)
	}
	// Load reports stale with evidence.
	l, err := auth.Load(1)
	if !errors.Is(err, ErrFenceLeaseStale) || l == nil {
		t.Fatalf("stale load: l=%v err=%v want stale+non-nil", l, err)
	}
}

func TestFenceReleaseThenAssertFailsClosed(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	ctx := context.Background()
	lease, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := auth.Release(ctx, lease); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, err = auth.AssertHolds(ctx, 1, "meshium-A", 1, "FencingSource")
	if !errors.Is(err, ErrFenceLeaseNotHeld) {
		t.Fatalf("released assert: err=%v want ErrFenceLeaseNotHeld", err)
	}
	// A new acquire after release gets a fresh, higher token.
	lease2, err := auth.Acquire(ctx, 1, "meshium-A", "SwitchingTraffic")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if lease2.FenceToken != 2 {
		t.Fatalf("token=%d want 2 (monotonic)", lease2.FenceToken)
	}
}

func TestFenceTokenMonotonicAcrossAcquires(t *testing.T) {
	_, clock, auth := newFenceRepo(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		lease, err := auth.Acquire(ctx, 1, "meshium-A", "FencingSource")
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if lease.FenceToken != i {
			t.Fatalf("round %d token=%d want %d", i, lease.FenceToken, i)
		}
		// Expire + release path: release requires being held, so expire then
		// manually clear by releasing via repo is not possible (stale blocks).
		// Instead release cleanly while still valid, then next acquire is +1.
		if err := auth.Release(ctx, lease); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
		clock.t = clock.t.Add(time.Minute)
	}
}

func TestFenceLoadNilWhenNoLease(t *testing.T) {
	_, _, auth := newFenceRepo(t)
	l, err := auth.Load(1)
	if err != nil || l != nil {
		t.Fatalf("empty load: l=%v err=%v want nil/nil", l, err)
	}
}

func TestGenerateHolderUniqueAndPrefixed(t *testing.T) {
	a, _ := GenerateHolder()
	b, _ := GenerateHolder()
	if a == b {
		t.Fatal("holders must be unique per call")
	}
	if len(a) < len("meshium-")+32 {
		t.Fatalf("holder too short: %q", a)
	}
}
