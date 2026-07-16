package migration

import (
	"strings"
)

// depmap.go (Phase 6B2) — dependency resolution for parity items.
//
// Deps are advisory in the UI: a hard-dep unsatisfied disables the apply toggle
// for the parent; recommended/verify_post deps are warnings. Satisfaction is
// computed against the live TARGET inventory collected during ComputeParity, so
// it reflects reality, not the plan.
//
// The rules below are deliberately conservative and bounded to the relationships
// the migration engine can actually verify from collected inventory. They are not
// exhaustive (e.g. arbitrary package→service maps are not modeled); the FE must
// never treat an absent dep edge as "no dependency". Adding a rule here is the
// only change needed to teach the engine a new relationship.

// targetInventory is the flattened live target state used to resolve dep
// satisfaction. It is built once per ComputeParity call from the live target
// collect and reused across all items.
type targetInventory struct {
	packages map[string]bool
	configs  map[string]bool // normalized absolute paths
	services map[string]bool // enabled units
	images   map[string]bool // docker image repo:tag
	volumes  map[string]bool
}

func newTargetInventory() *targetInventory {
	return &targetInventory{
		packages: map[string]bool{},
		configs:  map[string]bool{},
		services: map[string]bool{},
		images:   map[string]bool{},
		volumes:  map[string]bool{},
	}
}

// depsFor resolves the dependency edges for one parity item against the live
// target inventory. Returns nil when the item has no modeled dependencies.
func depsFor(category, itemKey string, inv *targetInventory) []DependencyRef {
	switch category {
	case "configs":
		// A config file typically backs a service. Recommended (not blocking): the
		// service may be enabled-by-hand on the target. Link by unit name derived
		// from the path.
		if unit := serviceFromConfigPath(itemKey); unit != "" {
			key := "service:" + unit
			return []DependencyRef{{
				ItemKey:    key,
				Kind:       DepRecommended,
				Note:       "config backs " + unit + "; ensure the service is enabled on the target",
				Satisfied: inv.services[key],
			}}
		}
	case "services":
		// An enabled service needs its backing config present on the target to
		// start cleanly — hard dep. Plus a recommended dep on its runtime when the
		// unit name implies one (pm2/node/nginx/docker/redis/postgres/mysql/mongo).
		deps := make([]DependencyRef, 0, 2)
		if path := configForService(itemKey); path != "" {
			key := "config:" + path
			deps = append(deps, DependencyRef{
				ItemKey:    key,
				Kind:       DepHard,
				Note:       unitName(itemKey) + " needs " + path + " present and correct on the target",
				Satisfied: inv.configs[key],
			})
		}
		if rt := runtimeForUnit(unitName(itemKey)); rt != "" {
			key := "runtime:" + rt
			deps = append(deps, DependencyRef{
				ItemKey:    key,
				Kind:       DepRecommended,
				Note:       unitName(itemKey) + " needs runtime " + rt,
				Satisfied: inv.packages[rt] || inv.services[key] || inv.images[key],
			})
		}
		return deps
	case "docker":
		// Containers hard-depend on their image and (when part of a compose
		// project) the compose project definition.
		if name := containerNameFromKey(itemKey); name != "" {
			deps := []DependencyRef{{
				ItemKey:    "docker-image:" + name,
				Kind:       DepHard,
				Note:       "container " + name + " requires its image present",
				Satisfied: inv.images["docker-image:"+name],
			}}
			if proj := composeProjectFromContainer(name); proj != "" {
				deps = append(deps, DependencyRef{
					ItemKey:    "compose-project:" + proj,
					Kind:       DepHard,
					Note:       "container " + name + " belongs to compose project " + proj,
					Satisfied: inv.configs["compose-project:"+proj],
				})
			}
			return deps
		}
	case "database":
		// A database needs its engine runtime present on the target.
		if engine := engineFromDBKey(itemKey); engine != "" {
			rt := engineRuntime(engine)
			if rt != "" {
				key := "runtime:" + rt
				return []DependencyRef{{
					ItemKey:    key,
					Kind:       DepHard,
					Note:       "database needs " + rt + " runtime on the target",
					Satisfied: inv.packages[rt] || inv.services[key],
				}}
			}
		}
	}
	return nil
}

// --- key/name helpers (all pure, no I/O) ---

func unitName(itemKey string) string {
	s := strings.TrimPrefix(itemKey, "service:")
	return s
}

// serviceFromConfigPath derives the likely backing service unit from a config
// path (e.g. /etc/nginx/nginx.conf → nginx). Empty when no obvious mapping.
func serviceFromConfigPath(itemKey string) string {
	path := strings.TrimPrefix(itemKey, "config:")
	path = strings.TrimPrefix(path, "/")
	// configs live under /etc/<svc>/... (or /etc/<svc>), so the service name is
	// the SECOND path segment (the first is "etc").
	seg := path
	if i := strings.Index(path, "/"); i >= 0 {
		rest := path[i+1:]
		if j := strings.Index(rest, "/"); j >= 0 {
			seg = rest[:j]
		} else {
			seg = rest
		}
	}
	switch {
	case strings.HasPrefix(seg, "nginx"):
		return "nginx"
	case strings.HasPrefix(seg, "postgresql") || strings.HasPrefix(seg, "pgsql"):
		return "postgresql"
	case strings.HasPrefix(seg, "mysql") || strings.HasPrefix(seg, "mariadb"):
		return "mysql"
	case strings.HasPrefix(seg, "redis"):
		return "redis-server"
	case strings.HasPrefix(seg, "mongod"):
		return "mongod"
	case strings.HasPrefix(seg, "pm2"):
		return "pm2"
	case strings.HasPrefix(seg, "docker"):
		return "docker"
	}
	return ""
}

// configForService returns the most likely primary config PATH (bare absolute
// path, no "config:" prefix) for a unit, used to build the hard-dep edge. Empty
// when unknown. depsFor prepends the "config:" namespace to match inventory keys.
func configForService(itemKey string) string {
	unit := unitName(itemKey)
	switch unit {
	case "nginx":
		return "/etc/nginx/nginx.conf"
	case "postgresql":
		return "/etc/postgresql"
	case "mysql", "mariadb":
		return "/etc/mysql"
	case "redis-server":
		return "/etc/redis/redis.conf"
	case "mongod":
		return "/etc/mongod.conf"
	}
	return ""
}

// runtimeForUnit maps a service unit to the runtime package that must be
// installed (used for the recommended runtime dep). "" when N/A.
func runtimeForUnit(unit string) string {
	switch unit {
	case "pm2", "node":
		return "nodejs"
	case "nginx":
		return "nginx"
	case "redis-server":
		return "redis-server"
	case "postgresql":
		return "postgresql"
	case "mysql", "mariadb":
		return "mysql-server"
	case "mongod":
		return "mongodb"
	case "docker":
		return "docker.io"
	}
	return ""
}

// containerNameFromKey extracts the container name from a docker-container key.
func containerNameFromKey(itemKey string) string {
	s := strings.TrimPrefix(itemKey, "docker-container:")
	if s == "" || s == itemKey {
		return ""
	}
	return s
}

// composeProjectFromContainer is a best-effort mapping. Today we cannot reliably
// infer the compose project from the container alone without deeper inspect; the
// live inventory carries compose paths as compose-project keys, and the parity
// builder can attach that edge when it has the data. This returns "" so we never
// fabricate a wrong project edge. The docker builder passes the known project
// explicitly via buildDeps.
func composeProjectFromContainer(string) string { return "" }

func engineFromDBKey(itemKey string) string {
	// database:<engine>:<db>
	s := strings.TrimPrefix(itemKey, "database:")
	if s == "" || s == itemKey {
		return ""
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func engineRuntime(engine string) string {
	switch engine {
	case "postgres":
		return "postgresql"
	case "mysql":
		return "mysql-server"
	case "mongodb":
		return "mongodb"
	case "redis":
		return "redis-server"
	}
	return ""
}

// buildDeps merges the category heuristics above with any explicitly-known edges
// (e.g. the docker builder knows compose-project membership). It is the single
// entry point used by ComputeParity so every item goes through depsFor.
func buildDeps(category, itemKey string, inv *targetInventory, extra []DependencyRef) []DependencyRef {
	base := depsFor(category, itemKey, inv)
	if len(extra) == 0 {
		return base
	}
	return append(base, extra...)
}
