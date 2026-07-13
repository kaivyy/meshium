package migration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPolicyEngineUnifiesGuardrails proves the four previously-scattered
// guardrails (supportedTrafficProviders, supportedReplicationModes,
// supportedExecutionModes, cutoverEngineType) are now exposed through ONE
// source of truth, and that it matches the legacy support maps exactly.
func TestPolicyEngineUnifiesGuardrails(t *testing.T) {
	p := NewPolicyEngine()

	// Traffic providers: engine mirrors supportedTrafficProviders.
	for pr, want := range supportedTrafficProviders {
		if got := p.ProviderSupportedForAutomaticCutover(pr); got != want {
			t.Errorf("provider %q support mismatch: policy=%v legacy=%v", pr, got, want)
		}
	}
	// Replication modes mirror supportedReplicationModes.
	for m, want := range supportedReplicationModes {
		if got := p.ReplicationModeSupported(m); got != want {
			t.Errorf("mode %q support mismatch: policy=%v legacy=%v", m, got, want)
		}
	}
	// Execution modes mirror supportedExecutionModes.
	for m, want := range supportedExecutionModes {
		if got := p.ExecutionModeSupported(m); got != want {
			t.Errorf("exec mode %q support mismatch: policy=%v legacy=%v", m, got, want)
		}
	}
}

// TestPolicyEngineAutoCutoverEngineGate proves the engine decision is the single
// fenced-cutover contract: postgres/mysql/redis allowed, mongodb NOT (no
// replica-set lag), and an unknown engine is rejected.
func TestPolicyEngineAutoCutoverEngineGate(t *testing.T) {
	p := NewPolicyEngine()

	for _, e := range []string{"postgres", "mysql", "redis"} {
		if !p.EngineSupportedForAutomaticCutover(e) {
			t.Errorf("engine %q must be supported for automatic cutover", e)
		}
		if err := p.CheckAutoCutover(e, TrafficProviderNginx); err != nil {
			t.Errorf("engine %q + nginx should pass: %v", e, err)
		}
	}
	for _, e := range []string{"mongodb", "oracle", ""} {
		if p.EngineSupportedForAutomaticCutover(e) {
			t.Errorf("engine %q must NOT be supported for automatic cutover", e)
		}
		if err := p.CheckAutoCutover(e, TrafficProviderNginx); err == nil {
			t.Errorf("engine %q must fail CheckAutoCutover", e)
		}
	}
}

// TestPolicyEngineAutoCutoverProviderGate proves an empty provider (no fenced
// switcher) and an unsupported provider both fail the execution-time gate, even
// for a supported engine.
func TestPolicyEngineAutoCutoverProviderGate(t *testing.T) {
	p := NewPolicyEngine()

	if err := p.CheckAutoCutover("postgres", ""); err == nil {
		t.Fatal("empty provider must fail auto-cutover (a fenced switcher is required)")
	}
	if err := p.CheckAutoCutover("postgres", TrafficProviderTraefik); err == nil {
		t.Fatal("traefik must fail auto-cutover (no fenced switcher)")
	}
	for _, pr := range []TrafficProvider{TrafficProviderNginx, TrafficProviderHAProxy, TrafficProviderCaddy} {
		if err := p.CheckAutoCutover("postgres", pr); err != nil {
			t.Errorf("provider %q should pass: %v", pr, err)
		}
	}
}

// TestPolicyEngineConfigSupportMirrorsValidate proves the API-boundary check
// delegates to the same PolicyEngine as execution time (no drift), so a config
// that passes POST cannot later reach a different decision at the cutover stage.
func TestPolicyEngineConfigSupportMirrorsValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config *MigrationConfig
		ok     bool
	}{
		{"nginx ok", &MigrationConfig{TrafficProvider: TrafficProviderNginx}, true},
		{"traefik rejected", &MigrationConfig{TrafficProvider: TrafficProviderTraefik}, false},
		{"unknown provider rejected", &MigrationConfig{TrafficProvider: "magic-wand"}, false},
		{"logical mode ok", &MigrationConfig{ReplicationMode: ReplicationModeLogical}, true},
		{"bogus mode rejected", &MigrationConfig{ReplicationMode: ReplicationMode("warp")}, false},
	} {
		if err := DefaultPolicy.CheckConfigSupport(tc.config); (err == nil) != tc.ok {
			t.Errorf("%s: CheckConfigSupport ok=%v want %v (err=%v)", tc.name, err == nil, tc.ok, err)
		}
		if err := validateConfigSupport(tc.config); (err == nil) != tc.ok {
			t.Errorf("%s: validateConfigSupport ok=%v want %v (err=%v)", tc.name, err == nil, tc.ok, err)
		}
	}
}

// TestPolicyMatrixIntrospection proves the operator-facing /api/pipeline/policy
// snapshot is served and lists the supported engines/providers/modes, with the
// honest caveat notes (mongodb not safe, redis degraded, opt-in default).
func TestPolicyMatrixIntrospection(t *testing.T) {
	h := &PipelineHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/pipeline/policy", nil)
	w := httptest.NewRecorder()
	h.handlePolicy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("policy endpoint status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"\"supportedEngines\"",
		"\"supportedTrafficProviders\"",
		"\"supportedReplicationModes\"",
		"\"supportedExecutionModes\"",
		"nginx", "haproxy", "caddy",
		"postgres", "mysql", "redis",
		"mongodb has no safe automatic cutover contract",
		"redis has no source-freeze primitive",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("policy matrix missing %q\nbody=%s", want, body)
		}
	}
}
