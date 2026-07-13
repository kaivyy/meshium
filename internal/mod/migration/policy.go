package migration

import (
	"fmt"
	"sort"
)

// Phase 4E — server-side policy engine.
//
// The support/guardrail decisions were previously scattered across three
// package-level maps (supportedTrafficProviders, supportedReplicationModes,
// supportedExecutionModes) plus the cutoverEngineType() switch, and were only
// consulted at API validation time (validateConfigSupport). That left a gap: a
// config that passed validation could still reach the auto-cutover stage where
// a different code path re-derived the same decision by a different mechanism
// (cutoverEngineType + newTrafficSwitcher). The honesty contract (Phase 1-3,
// directive rules #4/#8) requires ONE enforcement surface that is consulted
// both at the API boundary AND at execution time.
//
// PolicyEngine is that single surface. It wraps the existing support maps (the
// source of truth does not move) and adds:
//   - CheckAutoCutover: the execution-time gate driven by runAutoCutover, so the
//     engine/provider/topology/automatic decision is enforced where the cutover
//     actually happens, not only at POST time.
//   - Matrix(): an introspectable view of the current policy, exposed via the
//     /api/pipeline/policy endpoint so operators can audit exactly what the
//     server will permit (honest, no surprises).

// PolicyEngine is the single support/guardrail enforcement surface for
// Meshium cutover. A nil receiver behaves as the default policy (see
// DefaultPolicy) so callers that need a value can pass it explicitly.
type PolicyEngine struct {
	AllowedTrafficProviders map[TrafficProvider]bool
	AllowedReplicationModes map[ReplicationMode]bool
	AllowedExecutionModes   map[ExecutionMode]bool
	// AllowedCutoverEngines lists engines that have a fenced auto-cutover
	// contract. Mirrors cutoverEngineType's ok-set (postgres, mysql, redis).
	AllowedCutoverEngines map[string]bool
}

// DefaultPolicy is the process-wide policy assembled from the existing support
// maps. It is consulted by validateConfigSupport (API boundary) and
// CheckAutoCutover (execution time), so the two cannot drift.
var DefaultPolicy = NewPolicyEngine()

// NewPolicyEngine builds a PolicyEngine from the existing support maps. Keeping
// the maps as the source of truth means existing tests that set/inspect them
// continue to hold, and there is exactly one place to change a support decision.
func NewPolicyEngine() *PolicyEngine {
	engines := map[string]bool{}
	for _, e := range []string{"postgres", "mysql", "redis"} {
		engines[e] = true
	}
	return &PolicyEngine{
		AllowedTrafficProviders: copyBoolMap(policyTrafficKeys()),
		AllowedReplicationModes: copyReplKeys(),
		AllowedExecutionModes:   copyExecKeys(),
		AllowedCutoverEngines:   engines,
	}
}

// policyTrafficKeys returns the supported traffic providers as a plain map so
// NewPolicyEngine does not alias the shared package var (callers must not mutate
// the engine's maps; they are read-only references to current policy).
func policyTrafficKeys() map[TrafficProvider]bool {
	out := map[TrafficProvider]bool{}
	for k, v := range supportedTrafficProviders {
		out[k] = v
	}
	return out
}

func copyReplKeys() map[ReplicationMode]bool {
	out := map[ReplicationMode]bool{}
	for k, v := range supportedReplicationModes {
		out[k] = v
	}
	return out
}

func copyExecKeys() map[ExecutionMode]bool {
	out := map[ExecutionMode]bool{}
	for k, v := range supportedExecutionModes {
		out[k] = v
	}
	return out
}

func copyBoolMap(in map[TrafficProvider]bool) map[TrafficProvider]bool {
	out := map[TrafficProvider]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

// EngineSupportedForAutomaticCutover reports whether the engine has a fenced
// auto-cutover contract. mongodb has none (no replica-set lag measurement) and
// fails closed everywhere — this is the honest, single decision point.
func (p *PolicyEngine) EngineSupportedForAutomaticCutover(engine string) bool {
	e, ok := cutoverEngineType(engine)
	if !ok {
		return false
	}
	return p.AllowedCutoverEngines[e]
}

// ProviderSupportedForAutomaticCutover reports whether the traffic provider has
// a real fenced switcher with read-after-write ownership verification.
func (p *PolicyEngine) ProviderSupportedForAutomaticCutover(provider TrafficProvider) bool {
	return p.AllowedTrafficProviders[provider]
}

// ReplicationModeSupported reports whether the replication strategy is in scope.
func (p *PolicyEngine) ReplicationModeSupported(mode ReplicationMode) bool {
	return p.AllowedReplicationModes[mode]
}

// ExecutionModeSupported reports whether the topology execution mode is
// certified (see CertifyTopology / supportedExecutionModes).
func (p *PolicyEngine) ExecutionModeSupported(mode ExecutionMode) bool {
	return p.AllowedExecutionModes[mode]
}

// CheckConfigSupport mirrors validateConfigSupport but on the engine, so the
// API boundary and execution time share one implementation.
func (p *PolicyEngine) CheckConfigSupport(config *MigrationConfig) error {
	if config == nil {
		return nil
	}
	if config.TrafficProvider != "" && !p.ProviderSupportedForAutomaticCutover(config.TrafficProvider) {
		return fmt.Errorf("unsupported traffic provider %q: must be one of the supported providers", config.TrafficProvider)
	}
	if config.ReplicationMode != "" && !p.ReplicationModeSupported(config.ReplicationMode) {
		return fmt.Errorf("unsupported replication mode %q: must be one of the supported modes", config.ReplicationMode)
	}
	return nil
}

// CheckAutoCutover is the execution-time gate. It is called by runAutoCutover
// BEFORE the lease is acquired, so an unsupported engine/provider fails closed
// with an explicit, actionable error and never reaches a mutation. The empty
// provider ("") is rejected because an auto-cutover requires a fenced switcher.
//
// The topology execution mode is NOT re-checked here: it is enforced separately
// by CertifyTopology (which fails closed) before any cutover mutation. The
// policy engine still exposes ExecutionModeSupported for the API-boundary
// certification path, but adding an empty mode value here would risk a false
// reject with no added safety.
func (p *PolicyEngine) CheckAutoCutover(engine string, provider TrafficProvider) error {
	if !p.EngineSupportedForAutomaticCutover(engine) {
		return fmt.Errorf("autoCutover: engine %q has no fenced-cutover contract (supported: postgres, mysql, redis; mongodb is not safe)", engine)
	}
	if provider == "" {
		return fmt.Errorf("autoCutover: a traffic provider with a fenced switcher is required (supported: nginx, haproxy, caddy)")
	}
	if !p.ProviderSupportedForAutomaticCutover(provider) {
		return fmt.Errorf("autoCutover: traffic provider %q has no fenced switcher (supported: nginx, haproxy, caddy)", provider)
	}
	return nil
}

// PolicyMatrix is the introspectable snapshot of current policy. It is returned
// by the /api/pipeline/policy endpoint so operators can audit exactly what the
// server will permit.
type PolicyMatrix struct {
	AutoCutoverDefault        bool     `json:"autoCutoverDefault"`
	SupportedEngines          []string `json:"supportedEngines"`
	SupportedTrafficProviders []string `json:"supportedTrafficProviders"`
	SupportedReplicationModes []string `json:"supportedReplicationModes"`
	SupportedExecutionModes   []string `json:"supportedExecutionModes"`
	// Notes records honest caveats (e.g. mongodb not safe, redis degraded).
	Notes []string `json:"notes"`
}

// Matrix returns the current policy as an introspectable snapshot.
func (p *PolicyEngine) Matrix() PolicyMatrix {
	m := PolicyMatrix{
		AutoCutoverDefault: AutoCutoverDefault,
		Notes: []string{
			"mongodb has no safe automatic cutover contract (no replica-set lag measurement) — manual cutover only",
			"redis has no source-freeze primitive — degraded (minimal, not zero, downtime); never automatic",
			"automatic cutover is opt-in (autoCutover=false by default); the default path is manual_required",
		},
	}
	for e := range p.AllowedCutoverEngines {
		m.SupportedEngines = append(m.SupportedEngines, e)
	}
	sort.Strings(m.SupportedEngines)
	for pr := range p.AllowedTrafficProviders {
		m.SupportedTrafficProviders = append(m.SupportedTrafficProviders, string(pr))
	}
	sort.Strings(m.SupportedTrafficProviders)
	for rm := range p.AllowedReplicationModes {
		m.SupportedReplicationModes = append(m.SupportedReplicationModes, string(rm))
	}
	sort.Strings(m.SupportedReplicationModes)
	for em := range p.AllowedExecutionModes {
		m.SupportedExecutionModes = append(m.SupportedExecutionModes, string(em))
	}
	sort.Strings(m.SupportedExecutionModes)
	return m
}
