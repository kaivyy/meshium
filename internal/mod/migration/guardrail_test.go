package migration

import (
	"testing"
)

// TestSupportedProviderGuardrail asserts the API guardrail accepts ONLY the
// providers that have a real fenced switcher with read-after-write ownership
// verification (nginx, haproxy, caddy — Phase 4B). Everything else is rejected
// at the API boundary (finding A.2-1): traefik/cloudflare/caddy/docker/dns were
// once selectable but had no switcher and failed closed only at cutover runtime.
// The empty provider ("") is permitted (means "no traffic switch configured").
func TestSupportedProviderGuardrail(t *testing.T) {
	ok := []TrafficProvider{
		TrafficProviderNginx, TrafficProviderHAProxy, TrafficProviderCaddy, "",
	}
	for _, p := range ok {
		if err := validateConfigSupport(&MigrationConfig{TrafficProvider: p}); err != nil {
			t.Fatalf("provider %q should be supported: %v", p, err)
		}
	}

	// Providers with no fenced switcher: rejected at the API boundary, fail closed.
	bad := []TrafficProvider{
		TrafficProviderTraefik, TrafficProviderCloudflare, TrafficProviderDocker,
		TrafficProviderDNS, "aws-alb", "istio", "magic-wand",
	}
	for _, p := range bad {
		if err := validateConfigSupport(&MigrationConfig{TrafficProvider: p}); err == nil {
			t.Fatalf("provider %q must be rejected at the API boundary", p)
		}
	}
}

// TestSupportedReplicationGuardrail asserts the API guardrail accepts the
// supported replication modes and rejects unsupported engines.
func TestSupportedReplicationGuardrail(t *testing.T) {
	ok := []ReplicationMode{
		ReplicationModeNone, ReplicationModeStreaming, ReplicationModeLogical,
		ReplicationModeReplica, ReplicationModeDump, "",
	}
	for _, m := range ok {
		if err := validateConfigSupport(&MigrationConfig{ReplicationMode: m}); err != nil {
			t.Fatalf("replication mode %q should be supported: %v", m, err)
		}
	}

	bad := []ReplicationMode{"sharding", "galera", "physical-block"}
	for _, m := range bad {
		if err := validateConfigSupport(&MigrationConfig{ReplicationMode: m}); err == nil {
			t.Fatalf("replication mode %q must be rejected at the API boundary", m)
		}
	}
}
