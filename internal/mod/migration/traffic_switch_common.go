package migration

import (
	"encoding/json"
	"strings"

	"meshium/internal/shared"
)

// Phase 2C-5: generic traffic-switch types shared by NginxSwitcher and
// HAProxySwitcher. The two switchers have identical contracts (idempotent
// switch + read-after-write verify + sanitized persist), so they share one
// request/result type. NginxSwitchRequest / HAProxySwitchRequest are aliases of
// TrafficSwitchRequest so existing callers (pipeline.go NginxSwitchRequest,
// tests) keep compiling; the orchestrator only references the generic form.

// ErrHAProxyConfigTestFailed is returned when `haproxy -c` rejects the new config.
var ErrHAProxyConfigTestFailed = errorString("haproxy config test failed")

// ErrHAProxyReloadFailed is returned when the haproxy reload fails after retries.
var ErrHAProxyReloadFailed = errorString("haproxy reload failed")

// ErrTrafficVerifyFailed is the shared read-after-write verification failure
// returned by both the Nginx and HAProxy switchers. Callers MUST fail closed to
// NeedsManualIntervention — the traffic did NOT move and rollback is not assumed
// safe.
var ErrTrafficVerifyFailed = errorString("traffic post-switch verification failed (traffic did not move)")

// errorString is a tiny error type so the HAProxy errors mirror the Nginx ones
// (plain fmt.Errorf-shaped strings) without importing errors twice.
type errorString string

func (e errorString) Error() string { return string(e) }

// TrafficSwitchRequest is the generic input to a traffic switcher (Nginx or
// HAProxy). NewConfig is the full replacement config written to ConfigPath;
// switchers do NOT regex-edit the live config. VerifyURL + VerifyHeader/
// VerifyValue are the read-after-write ownership proof.
type TrafficSwitchRequest struct {
	IdempotencyKey string `json:"idempotencyKey"`          // dedup key; empty = always run
	ConfigPath     string `json:"configPath,omitempty"`   // default per provider
	NewConfig      string `json:"newConfig"`              // full replacement config
	VerifyURL      string `json:"verifyUrl"`              // health endpoint to read-after-write
	VerifyHeader   string `json:"verifyHeader,omitempty"` // expected header, e.g. X-Meshium-Target
	VerifyValue    string `json:"verifyValue,omitempty"`  // expected header/body value, e.g. target
}

// TrafficSwitchResult is the generic outcome a switcher persists (sanitized).
type TrafficSwitchResult struct {
	Switched        bool   `json:"switched"`
	ConfigTestOK    bool   `json:"configTestOk"`
	ReloadOK        bool   `json:"reloadOk"`
	Verified        bool   `json:"verified"`
	VerifyResponse  string `json:"verifyResponse,omitempty"`
	SanitizedConfig  string `json:"sanitizedConfig,omitempty"`  // newConfig with secrets redacted
	SanitizedResult  string `json:"sanitizedResult,omitempty"`  // full result, sanitized
}

// NginxSwitchRequest is a type alias of TrafficSwitchRequest (Phase 2A caller
// compatibility). The NginxSwitcher.Switch method accepts TrafficSwitchRequest,
// so the alias is purely for call-site readability.
type NginxSwitchRequest = TrafficSwitchRequest

// NginxSwitchResult is a type alias of TrafficSwitchResult.
type NginxSwitchResult = TrafficSwitchResult

// HAProxySwitchRequest is a type alias of TrafficSwitchRequest.
type HAProxySwitchRequest = TrafficSwitchRequest

// HAProxySwitchResult is a type alias of TrafficSwitchResult.
type HAProxySwitchResult = TrafficSwitchResult

// sanitizeTrafficConfig redacts secrets from a stored config blob, reusing the
// Nginx sanitiser's logic (JSON-aware via shared.SanitizeJSONRawMessage, else
// shared.SanitizeString).
func sanitizeTrafficConfig(config string) string {
	trimmed := strings.TrimSpace(config)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		raw := shared.SanitizeJSONRawMessage(json.RawMessage(config))
		return string(raw)
	}
	return shared.SanitizeString(config)
}

// sanitizeTrafficResult returns the sanitized JSON of the result for storage.
func sanitizeTrafficResult(r *TrafficSwitchResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(shared.SanitizeJSONRawMessage(json.RawMessage(b)))
}
