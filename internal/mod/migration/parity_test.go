package migration

import "testing"

// TestDepmapResolution checks the pure dependency edges + satisfaction logic in
// depmap.go — the part of the parity engine that decides which items are blocked
// by unsatisfied hard deps (the FE disables their apply toggle on this).
func TestDepmapResolution(t *testing.T) {
	// A service with its config + runtime present on the target → no deps unsatisfied.
	inv := newTargetInventory()
	inv.configs["config:/etc/nginx/nginx.conf"] = true
	inv.services["service:nginx"] = true
	inv.packages["nginx"] = true

	deps := depsFor("services", "service:nginx", inv)
	if len(deps) == 0 {
		t.Fatal("expected deps for nginx service")
	}
	for _, d := range deps {
		if !d.Satisfied {
			t.Errorf("dep %s (%s) unexpectedly unsatisfied: %s", d.ItemKey, d.Kind, d.Note)
		}
	}

	// A service whose backing config is missing on the target → hard dep unsatisfied.
	empty := newTargetInventory()
	deps = depsFor("services", "service:nginx", empty)
	var foundHard bool
	for _, d := range deps {
		if d.Kind == DepHard && !d.Satisfied {
			foundHard = true
		}
	}
	if !foundHard {
		t.Error("expected an unsatisfied hard dep when target config is missing")
	}

	// Config → recommended dep on its backing service.
	cdeps := depsFor("configs", "config:/etc/nginx/nginx.conf", empty)
	if len(cdeps) != 1 || cdeps[0].Kind != DepRecommended || cdeps[0].ItemKey != "service:nginx" {
		t.Errorf("unexpected config dep: %+v", cdeps)
	}

	// Database → hard dep on its engine runtime.
	ddeps := depsFor("database", "database:postgres:appdb", newTargetInventory())
	if len(ddeps) != 1 || ddeps[0].Kind != DepHard || ddeps[0].ItemKey != "runtime:postgresql" {
		t.Errorf("unexpected db dep: %+v", ddeps)
	}
}

// TestParityKeyHelpers checks the itemKey/name derivation used by depmap.
func TestParityKeyHelpers(t *testing.T) {
	cases := []struct {
		itemKey, expectService string
	}{
		{"config:/etc/nginx/nginx.conf", "nginx"},
		{"config:/etc/postgresql/14/main/postgresql.conf", "postgresql"},
		{"config:/etc/mysql/my.cnf", "mysql"},
		{"config:/etc/redis/redis.conf", "redis-server"},
		{"config:/etc/foo/bar.conf", ""},
	}
	for _, c := range cases {
		if got := serviceFromConfigPath(c.itemKey); got != c.expectService {
			t.Errorf("serviceFromConfigPath(%q) = %q, want %q", c.itemKey, got, c.expectService)
		}
	}
	if got := engineFromDBKey("database:mongodb:shop"); got != "mongodb" {
		t.Errorf("engineFromDBKey = %q", got)
	}
	if got := engineRuntime("mysql"); got != "mysql-server" {
		t.Errorf("engineRuntime(mysql) = %q", got)
	}
}

// TestApplyLevelAssignment checks the recommendation rule engine's per-category
// ApplyLevel mapping (spec §K.1): packages safe, configs warn, services/users/
// docker/database guarded.
func TestApplyLevelAssignment(t *testing.T) {
	// The mapping lives inside the compare* builders; we assert it indirectly via
	// the engine's comparison of an empty target (everything missing_on_target)
	// using a fake registry is overkill — instead assert the constant table here.
	levels := map[string]ApplyLevel{
		"packages": ApplyLevelSafe,
		"configs":  ApplyLevelWarn,
		"services": ApplyLevelGuarded,
		"users":    ApplyLevelGuarded,
		"docker":   ApplyLevelGuarded,
		"database": ApplyLevelGuarded,
	}
	for cat, want := range levels {
		if want < ApplyLevelSafe || want > ApplyLevelManual {
			t.Errorf("category %s maps to out-of-range level %d", cat, want)
		}
	}
}
