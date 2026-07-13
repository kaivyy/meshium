package migration

import (
	"testing"
)

// TestSupportedProviderGuardrail asserts the API guardrail accepts the supported
// traffic providers and rejects anything outside the proven fail-closed set, so
// a client cannot select a provider with no fenced switcher.
func TestSupportedProviderGuardrail(t *testing.T) {
	ok := []TrafficProvider{
		TrafficProviderNginx, TrafficProviderHAProxy, TrafficProviderTraefik,
		TrafficProviderCloudflare, TrafficProviderCaddy, TrafficProviderDocker,
		TrafficProviderDNS, "",
	}
	for _, p := range ok {
		if err := validateConfigSupport(&MigrationConfig{TrafficProvider: p}); err != nil {
			t.Fatalf("provider %q should be supported: %v", p, err)
		}
	}

	bad := []TrafficProvider{"aws-alb", "istio", "magic-wand"}
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
