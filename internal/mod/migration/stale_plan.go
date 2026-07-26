package migration

import (
	"fmt"
	"time"
)

// Plan freshness windows.
//
// A plan is a snapshot of the SOURCE taken at plan time. Applying it later
// writes that snapshot to the target, so the older the plan, the more likely
// the target receives state the source no longer has. The audit found this
// surfaced only as a frontend warning — an operator could apply a day-old
// snapshot with nothing standing in the way.
const (
	// planSoftWindow — beyond this the plan is reported stale and the operator
	// is told to re-plan, but execution proceeds.
	planSoftWindow = 1 * time.Hour
	// planHardWindow — beyond this execution is refused. Re-planning is cheap
	// (a full three-category plan takes ~10s against real hosts); applying a
	// day-old snapshot is not.
	planHardWindow = 24 * time.Hour
)

// PlanFreshness is the verdict on whether a plan is still safe to apply.
type PlanFreshness struct {
	Age      time.Duration
	Stale    bool   // not fresh — operator should re-plan
	Blocking bool   // too old to apply at all
	Reason   string // why, in operator-facing terms; empty when fresh
}

// evaluatePlanFreshness decides whether a plan captured at plannedAt may still
// be applied at now.
//
// Two cases deliberately do NOT read as fresh:
//   - a zero plannedAt (legacy migration with no recorded time) — absence of
//     evidence is not evidence of freshness, so it warns;
//   - a plannedAt in the future (clock skew) — which would otherwise compute a
//     negative age and look infinitely fresh.
//
// Neither hard-blocks: both are data-quality problems rather than proof the
// plan is old, and blocking every legacy migration would be a worse failure.
func evaluatePlanFreshness(plannedAt, now time.Time) PlanFreshness {
	if plannedAt.IsZero() {
		return PlanFreshness{
			Stale:  true,
			Reason: "this migration has no recorded plan time, so its freshness cannot be established — re-plan before applying",
		}
	}

	age := now.Sub(plannedAt)
	if age < 0 {
		return PlanFreshness{
			Age:    age,
			Stale:  true,
			Reason: "the plan is timestamped in the future (clock skew between meshium and the source?) — re-plan before applying",
		}
	}

	switch {
	case age > planHardWindow:
		return PlanFreshness{
			Age:      age,
			Stale:    true,
			Blocking: true,
			Reason: fmt.Sprintf("the plan is %s old (limit %s): it describes the source as it was then, and applying it now would write a stale snapshot to the target — re-plan first",
				roundAge(age), roundAge(planHardWindow)),
		}
	case age > planSoftWindow:
		return PlanFreshness{
			Age:   age,
			Stale: true,
			Reason: fmt.Sprintf("the plan is %s old; the source may have changed since — consider re-planning before applying",
				roundAge(age)),
		}
	default:
		return PlanFreshness{Age: age}
	}
}

// roundAge renders a duration in operator-friendly units.
func roundAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	default:
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
}
