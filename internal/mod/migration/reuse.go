package migration

import (
	"context"
	"fmt"
	"time"

	"meshium/internal/mod/discovery"
)

// planReuseMaxAge bounds how old an onboarding snapshot may be and still be
// trusted to plan a migration. Beyond this window the source could have drifted
// (package upgrades, new services, DB writes), so we re-collect live rather than
// plan from a stale scan. Declared as a var (not const) so tests can shorten it.
var planReuseMaxAge = 30 * time.Minute

// planCategoryMeta computes the planner-time honesty metadata for one category.
// It is the freshness-aware reuse gate: if a FRESH onboarding snapshot exists
// AND the category's snapshot fields map FAITHFULLY to what the category's
// applier consumes, reuse is selected and the live collection is skipped.
// Otherwise collection runs live and the reason is recorded in Warnings so the
// UI can honestly explain why "discovery ran again".
//
// Today NO category reuses the snapshot, because every category's snapshot
// shape diverges from what its applier consumes (see reuseRefusedReason). That
// gap is real, not a shortcut: reusing any of them would be a silent skip or a
// fake-success, which Phase 5B forbids. As each category's onboarding snapshot
// becomes faithfully populated, flipping its reuseRefusedReason to "" is the
// ONLY change needed to enable reuse for it — the gate is already wired.
func (p *Planner) planCategoryMeta(ctx context.Context, serverID int, catName string, zeroCapable bool) *CategoryMeta {
	m := CategoryMetaFor(catName, zeroCapable)
	m.CollectionStrategy = CollectRefresh
	m.ReuseSource = ReuseNone

	if p.snapStore == nil {
		m.Warnings = append(m.Warnings, "no snapshot store configured — collected live")
		return &m
	}

	snap, err := p.snapStore.LoadSnapshot(serverID)
	if err != nil {
		m.Warnings = append(m.Warnings, "no onboarding snapshot captured for this source — collected live")
		return &m
	}

	age := time.Since(snap.CapturedAt)
	if age > planReuseMaxAge {
		m.Warnings = append(m.Warnings, fmt.Sprintf(
			"onboarding snapshot is %.0f min old (beyond the %s freshness window) — collected live",
			age.Minutes(), planReuseMaxAge))
		return &m
	}

	if reason := reuseRefusedReason(catName, *snap); reason != "" {
		// Fresh snapshot exists, but its shape is NOT faithful for this category.
		// Refuse reuse — never silently skip or fake a match.
		m.Warnings = append(m.Warnings, reason)
		return &m
	}

	// Faithful + fresh: reuse is permitted (no category qualifies today).
	m.CollectionStrategy = CollectReuse
	m.ReuseSource = ReuseDiscoverySnap
	return &m
}

// reuseRefusedReason returns the honesty reason reuse is refused for a category
// given the onboarding snapshot, or "" if reuse would be faithful. The reasons
// are the concrete divergences between each category's snapshot fields and the
// data its applier actually consumes at execute time.
//
// Every branch must state the real mismatch — this is an audit surface, not a
// stub. When a category's onboarding collector starts populating the matching
// fields in a shape the applier consumes, return "" to enable reuse.
func reuseRefusedReason(cat string, snap discovery.ServerSnapshot) string {
	switch cat {
	case "packages":
		// Onboarding's ServerSnapshot.Packages is never populated by the
		// discovery collectors — reusing it would plan "0 packages".
		if len(snap.Packages) == 0 {
			return "onboarding snapshot has no package list (discovery does not populate packages) — collected live from source"
		}
	case "services":
		// Onboarding collects ACTIVE units; migration plans ENABLED units.
		// Reusing would silently drop enabled-but-inactive services.
		return "onboarding snapshot lists active services, not enabled services — collected live to avoid silently dropping enabled-but-inactive units"
	case "database":
		// Snapshot stores only detection metadata (type/version/port/running);
		// it never carries the enumerated user-DB list (which requires live
		// auth) nor the credentials the applier needs at execute time.
		if len(snap.Databases) == 0 {
			return "no databases detected in onboarding snapshot — collected live (credentials are also required, and the snapshot does not carry them)"
		}
		return "database collection needs live enumeration + credentials the snapshot does not carry — collected live"
	case "users":
		// Applier needs raw /etc/passwd, per-user crontabs and firewall rules;
		// the snapshot stores only structured summaries (UID/GID, SSL days, etc).
		return "user migration needs raw /etc/passwd, crontabs and firewall rules; the onboarding snapshot stores only summaries — collected live"
	case "docker":
		// Applier consumes container IDs, env and volume mounts; the snapshot's
		// DockerInfo lacks IDs and Env, and lists images as "repo:tag" strings
		// without digests the transfer engine needs.
		if snap.Docker == nil {
			return "no docker info in onboarding snapshot — collected live"
		}
		return "docker migration needs container IDs, env and volumes the snapshot does not carry — collected live"
	case "configs":
		// Applier transfers actual file bodies; the snapshot does not capture them.
		return "config migration needs the actual file bodies; the onboarding snapshot does not capture them — collected live"
	}
	return ""
}

// zeroDowntimeCapable reports whether the global zero-downtime chain is wired
// (live replication + freeze/fencing + traffic switch + observation). It is the
// gate fed into DowntimeClassFor. Today the chain is not wired, so it returns
// false — no category may legitimately claim zero_downtime. Flips to true in a
// later phase when those pieces land (Parts 4–8).
func (p *Planner) zeroDowntimeCapable() bool { return false }
