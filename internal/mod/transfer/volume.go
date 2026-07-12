package transfer

import (
	"context"
	"strings"
)

// VolumeKind classifies a Docker volume source/target path so the transfer
// engine can decide a safe strategy. A path is only "materialized" (directly
// rsync-able) when it is an absolute, authoritatively-known host/bind path
// or a docker materialized mountpoint. Anything ambiguous MUST NOT be
// silently downgraded to a weaker transfer — it routes to manual intervention.
type VolumeKind string

const (
	// VolumeBind is a host bind-mount whose path is authoritative on the host.
	VolumeBind VolumeKind = "bind"
	// VolumeNamed is a docker named-volume materialized under
	// /var/lib/docker/volumes/<name>/_data (path must be confirmed live).
	VolumeNamed VolumeKind = "named"
	// VolumeUnknown is anything we cannot authoritatively verify.
	VolumeUnknown VolumeKind = "unknown"
)

// ClassifyVolumePath decides what kind of Docker volume path we have. It is a
// pure, no-I/O classifier: the caller confirms a "named" path exists on the
// host before trusting it. Paths that are genuinely uninterpretable stay
// "unknown" and must NOT be silently transferred.
func ClassifyVolumePath(path string) VolumeKind {
	p := strings.TrimSpace(path)
	if p == "" {
		return VolumeUnknown
	}
	// A bare token with no slash is unambiguously a Docker NAMED volume
	// (not a host path). The caller confirms its materialized path exists.
	if !strings.Contains(p, "/") {
		return VolumeNamed
	}
	// Docker named volumes are materialized under this prefix.
	if strings.HasPrefix(p, "/var/lib/docker/volumes/") && strings.HasSuffix(p, "/_data") {
		return VolumeNamed
	}
	// An absolute path is an authoritative host/bind mount.
	if strings.HasPrefix(p, "/") {
		return VolumeBind
	}
	// Anything else (relative paths, odd tokens) is unverifiable.
	return VolumeUnknown
}

// ShouldCompress decides whether to pass -z to rsync for this volume. We
// compress only clearly-text-like trees; binary/DB volumes compress poorly
// and waste CPU on large transfers, so they default to uncompressed.
func ShouldCompress(category, samplePath string) bool {
	if category == "docker-volume" {
		// Volumes often hold DB files / binaries — skip compression by default.
		return false
	}
	if category == "configs" {
		return true
	}
	// Heuristic fallback by extension (configs-style text).
	lower := strings.ToLower(samplePath)
	for _, ext := range []string{".txt", ".json", ".yaml", ".yml", ".conf", ".cfg", ".ini", ".xml", ".log", ".html", ".css", ".js", ".md"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// DockerVolumePlan bundles the routing decision for one volume transfer: whether
// to use the direct rsync path, whether it is a degraded/operator-visible
// fallback, and the classification (for honest reporting).
type DockerVolumePlan struct {
	Kind     VolumeKind
	Direct   bool // rsync source→target available
	Degraded bool // tar-over-SSH/SFTP fallback, only when explicitly allowed
	Mode     string
}

// PlanDockerVolume decides how to transfer a single Docker volume between two
// hosts. allowDegraded gates the weaker tar-over-SSH path: when false and
// direct rsync is unavailable, it fails closed (Direct=false, Degraded=false)
// so the caller routes to manual intervention rather than a silent downgrade.
//
// The function never performs I/O; it composes ClassifyVolumePath with the
// rsync availability gate. Unknown path kinds are NOT sent down the degraded
// path automatically.
func PlanDockerVolume(ctx context.Context, category string, path string, src, dst TransferTarget, allowDegraded bool) DockerVolumePlan {
	kind := ClassifyVolumePath(path)
	plan := DockerVolumePlan{Kind: kind}

	if kind == VolumeUnknown {
		// Ambiguous path: do not auto-downgrade. Caller must verify manually.
		return plan
	}

	sel := NewStrategySelector()
	res, err := sel.SelectWithFallback(ctx, 0, TransferOptions{}, src, dst, allowDegraded)
	if err != nil {
		return plan
	}
	if res.Degraded {
		plan.Degraded = true
		plan.Mode = "degraded"
		return plan
	}
	plan.Direct = true
	plan.Mode = "direct"
	return plan
}
