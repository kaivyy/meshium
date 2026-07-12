package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrFenceLeaseNotHeld is returned when a renew/release/assert targets a lease
// whose holder or token does not match, or that no longer exists. Callers MUST
// fail closed to StateNeedsManualIntervention on this error.
var ErrFenceLeaseNotHeld = errors.New("fence lease not held by caller")

// ErrFenceLeaseConflict is returned when Acquire finds an existing non-expired,
// non-released lease held by another holder. Callers MUST fail closed to
// StateNeedsManualIntervention (a concurrent cutover attempt must not proceed).
var ErrFenceLeaseConflict = errors.New("fence lease held by another holder")

// ErrFenceLeaseStale is returned when AssertHolds/Load finds a lease that has
// expired without being released — the cutover was interrupted and its state
// is untrustworthy. Callers MUST fail closed to StateNeedsManualIntervention.
var ErrFenceLeaseStale = errors.New("fence lease expired without release (stale)")

// FenceTTL is the default lease lifetime. A lease that is not renewed within
// this window is stale → NeedsManualIntervention. It is deliberately short:
// the cutover must complete (or renew) within minutes, or it is not safe to
// continue automatically.
const FenceTTL = 5 * time.Minute

// fenceLeaseRepo is the persistence surface the authority needs. Narrowed from
// PipelineRepo so the authority's failure semantics are testable in isolation
// and so the contract is explicit.
type fenceLeaseRepo interface {
	CreateFenceLease(ctx context.Context, l FenceLease) (int64, error)
	UpdateFenceLeaseState(ctx context.Context, id int64, state string) error
	RenewFenceLease(ctx context.Context, id int64, holder string, token int, expiresAt time.Time) error
	ReleaseFenceLease(ctx context.Context, id int64, holder string, token int) error
	GetFenceLease(migrationID int) (*FenceLease, error)
	reactivateFenceLease(ctx context.Context, migrationID int, holder string, token int, state, acquiredAt, expiresAt string) error
}

// Clock is injected so tests control expiry without sleeping. Production uses
// time.Now.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// FencingAuthority is the durable, fail-closed fencing authority for the
// Phase 2A fenced cutover. Every mutating cutover step (freeze, promote,
// switch traffic, unfreeze, rollback) MUST call AssertHolds first. A missing,
// expired, conflicting, or stale lease yields an error that the orchestrator
// translates to StateNeedsManualIntervention — never a mutation.
//
// Hard fence contract: while this authority holds a lease, the target PG node
// is a standby (read-only by PostgreSQL itself). The lease does NOT enforce
// read-only on the source; that is defense-in-depth (source read-only GUC).
// The lease enforces that only the lease holder may proceed with cutover
// mutations, and that an interrupted (stale) cutover cannot resume blindly.
type FencingAuthority struct {
	repo  fenceLeaseRepo
	clock Clock
	ttl   time.Duration
}

// NewFencingAuthority builds a production authority with the given repo and
// the default FenceTTL.
func NewFencingAuthority(repo fenceLeaseRepo) *FencingAuthority {
	return &FencingAuthority{repo: repo, clock: realClock{}, ttl: FenceTTL}
}

// Acquire takes a fresh lease for the migration in the given cutover sub-state.
// If a non-expired, non-released lease exists for another holder → conflict.
// An expired-but-unreleased (stale) existing lease is overwritten only after
// the caller has explicitly recorded the stale state elsewhere; Acquire
// itself refuses to silently replace a stale lease and returns ErrFenceLeaseStale
// so the orchestrator surfaces NeedsManualIntervention with the stale evidence.
// The fence_token is monotonically increasing per migration (max existing +1),
// so a later lease always has a higher token than any prior one.
func (a *FencingAuthority) Acquire(ctx context.Context, migrationID int, holder, state string) (*FenceLease, error) {
	existing, err := a.repo.GetFenceLease(migrationID)
	if err != nil {
		return nil, err
	}
	now := a.clock.Now()
	if existing != nil && existing.ReleasedAt == "" {
		expires, perr := parseTime(existing.ExpiresAt)
		if perr != nil {
			// Unparseable expiry → untrustworthy → fail closed.
			return nil, ErrFenceLeaseStale
		}
		if expires.After(now) {
			// Active lease. Same holder re-acquiring after expiry is impossible
			// here (we are before expiry); a different holder must not take over.
			if existing.Holder != holder {
				return nil, ErrFenceLeaseConflict
			}
			// Same holder already holds an active lease: return it as-is.
			return existing, nil
		}
		// Expired without release → stale. Do NOT silently replace.
		return nil, ErrFenceLeaseStale
	}

	token := 1
	if existing != nil && existing.FenceToken >= token {
		token = existing.FenceToken + 1
	}
	lease := FenceLease{
		MigrationID: migrationID,
		Holder:      holder,
		FenceToken:  token,
		State:       state,
		AcquiredAt:  now.Format(time.RFC3339),
		ExpiresAt:   now.Add(a.ttl).Format(time.RFC3339),
	}
	if existing != nil {
		// Row already exists (UNIQUE(migration_id)). existing is either
		// released (reactivate) — stale/unreleased was rejected above. Recycle
		// the row with the new holder/token/state.
		if err := a.repo.reactivateFenceLease(ctx, migrationID, holder, token, state, lease.AcquiredAt, lease.ExpiresAt); err != nil {
			return nil, err
		}
		lease.ID = existing.ID
		return &lease, nil
	}
	id, err := a.repo.CreateFenceLease(ctx, lease)
	if err != nil {
		// UNIQUE(migration_id) collision with a concurrent Acquire → conflict.
		return nil, fmt.Errorf("%w: %v", ErrFenceLeaseConflict, err)
	}
	lease.ID = id
	return &lease, nil
}

// Renew extends the lease expiry. Holder+token must match and the lease must
// not be released. Any failure (mismatch, expired, released) → error; the
// orchestrator fails closed.
func (a *FencingAuthority) Renew(ctx context.Context, lease *FenceLease) error {
	if lease == nil {
		return ErrFenceLeaseNotHeld
	}
	return a.repo.RenewFenceLease(ctx, lease.ID, lease.Holder, lease.FenceToken, a.clock.Now().Add(a.ttl))
}

// Release marks the lease released (best-effort). Does NOT unfreeze the source.
func (a *FencingAuthority) Release(ctx context.Context, lease *FenceLease) error {
	if lease == nil {
		return ErrFenceLeaseNotHeld
	}
	return a.repo.ReleaseFenceLease(ctx, lease.ID, lease.Holder, lease.FenceToken)
}

// SetState advances the persisted cutover sub-state on the held lease.
func (a *FencingAuthority) SetState(ctx context.Context, lease *FenceLease, state string) error {
	if lease == nil {
		return ErrFenceLeaseNotHeld
	}
	return a.repo.UpdateFenceLeaseState(ctx, lease.ID, state)
}

// AssertHolds verifies the caller still holds a live, matching lease in the
// expected sub-state. Called before every mutating cutover step. Returns nil
// only when: the lease exists, is unreleased, not expired, holder+token match,
// and state == expectedState (empty expectedState skips the state check).
func (a *FencingAuthority) AssertHolds(_ context.Context, migrationID int, holder string, token int, expectedState string) (*FenceLease, error) {
	l, err := a.repo.GetFenceLease(migrationID)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, ErrFenceLeaseNotHeld
	}
	if l.ReleasedAt != "" {
		return nil, ErrFenceLeaseNotHeld
	}
	if l.Holder != holder || l.FenceToken != token {
		return nil, ErrFenceLeaseConflict
	}
	expires, perr := parseTime(l.ExpiresAt)
	if perr != nil {
		return nil, ErrFenceLeaseStale
	}
	if !expires.After(a.clock.Now()) {
		return nil, ErrFenceLeaseStale
	}
	if expectedState != "" && l.State != expectedState {
		return nil, fmt.Errorf("%w: state=%q want=%q", ErrFenceLeaseNotHeld, l.State, expectedState)
	}
	return l, nil
}

// Load returns the current lease for a migration (nil if none), for
// restart reconciliation. A non-nil lease that is expired-and-unreleased is
// returned along with ErrFenceLeaseStale so the orchestrator can record
// NeedsManualIntervention with the stale evidence.
func (a *FencingAuthority) Load(migrationID int) (*FenceLease, error) {
	l, err := a.repo.GetFenceLease(migrationID)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, nil
	}
	if l.ReleasedAt != "" {
		return l, nil // released cleanly; not stale.
	}
	expires, perr := parseTime(l.ExpiresAt)
	if perr != nil {
		return l, ErrFenceLeaseStale
	}
	if !expires.After(a.clock.Now()) {
		return l, ErrFenceLeaseStale
	}
	return l, nil
}

// GenerateHolder returns a random holder id (process/instance identity).
// crypto/rand so it is not guessable — a holder id is a capability.
func GenerateHolder() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "meshium-" + hex.EncodeToString(b), nil
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
