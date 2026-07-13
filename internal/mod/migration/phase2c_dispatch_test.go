package migration

import (
	"context"
	"testing"
)

// Phase 2C dispatcher contract: engines are mapped to a supported
// ReplicationConfig.DatabaseType, and MongoDB (no replica-set lag measurement)
// is refused with ok=false. Unsupported/unknown engines also refuse.

func TestCutoverEngineTypeSupported(t *testing.T) {
	cases := map[string]struct {
		wantType string
		wantOK   bool
	}{
		"postgres":   {"postgres", true},
		"PostgreSQL": {"postgres", true},
		"mysql":      {"mysql", true},
		"MariaDB":    {"mysql", true},
		"redis":      {"redis", true},
		"mongodb":    {"", false},
		"oracle":     {"", false},
		"":           {"", false},
	}
	for in, want := range cases {
		got, ok := cutoverEngineType(in)
		if ok != want.wantOK || got != want.wantType {
			t.Fatalf("cutoverEngineType(%q) = (%q,%v), want (%q,%v)", in, got, ok, want.wantType, want.wantOK)
		}
	}
}

// TestNewTrafficSwitcherSupported: nginx, haproxy, and caddy (Phase 4B) have
// real fenced switchers; any other provider fails closed.
func TestNewTrafficSwitcherSupported(t *testing.T) {
	for _, p := range []TrafficProvider{TrafficProviderNginx, TrafficProviderHAProxy, TrafficProviderCaddy} {
		s, err := newTrafficSwitcher(p, nil)
		if err != nil || s == nil {
			t.Fatalf("provider %q should be supported: err=%v", p, err)
		}
	}
	for _, p := range []TrafficProvider{TrafficProviderCloudflare, TrafficProviderTraefik, TrafficProviderDocker, TrafficProviderDNS, ""} {
		_, err := newTrafficSwitcher(p, nil)
		if err == nil {
			t.Fatalf("provider %q should be unsupported (fail closed)", p)
		}
	}
}

// TestPreflightMongoDBFailsClosed confirms the engine dispatch refuses Mongo
// before any mutating command — no replica-set lag measurement means catch-up
// cannot be verified, so automated cutover must not proceed.
func TestPreflightMongoDBFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	_, err := e.CutoverPreflight(context.Background(), ReplicationConfig{DatabaseType: "mongodb"}, false)
	if err == nil {
		t.Fatal("MongoDB CutoverPreflight must fail closed")
	}
}

func TestCutoverPromoteMongoDBFailsClosed(t *testing.T) {
	e := &ReplicationEngine{}
	if err := e.CutoverPromote(context.Background(), ReplicationConfig{DatabaseType: "mongodb"}); err == nil {
		t.Fatal("MongoDB CutoverPromote must fail closed")
	}
}
