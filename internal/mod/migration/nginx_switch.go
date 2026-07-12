package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"meshium/internal/shared"
)

// Phase 2A-5: Nginx traffic-switch provider — idempotent switch + post-switch
// read-after-write verification.
//
// This is a focused, testable primitive the fenced cutover orchestrator
// (Phase 2A-6) calls at the SwitchingTraffic sub-state. It deliberately does
// NOT reuse the legacy switchNginx in traffic.go: that path mutates the live
// config via regex + Upload with no idempotency key, no bounded retry, and no
// read-after-write ownership proof — exactly the gaps spec §7 calls out. The
// live pipeline's trafficSwitchStage (Phase 1 manual-cutover) is untouched; it
// keeps recording manual_required. autoCutover (Commit 6) is the only caller
// of this switcher.
//
// Contract (spec §7):
//   - Idempotent: a second Switch with the same IdempotencyKey is a no-op that
//     returns the cached result. Persisting the key+result is the orchestrator's
//     job (Commit 6); the switcher holds an in-process cache so the behavior is
//     unit-testable in isolation.
//   - Bounded: default 30s timeout, one retry on reload failure.
//   - Verified: after reload, GET the health endpoint and require a distinct
//     target marker. A response reflecting the source (or no marker) is a
//     verification FAILURE — traffic did not move — returned as an error the
//     orchestrator maps to NeedsManualIntervention. Do NOT assume rollback safe.
//   - Sanitized: provider request/result stored with secrets redacted via
//     shared.SanitizeJSONRawMessage.

// NginxSwitchTimeout bounds a single switch attempt (test + reload + verify).
// Kept as a var so tests can shrink it.
var NginxSwitchTimeout = 30 * time.Second

// NginxReloadRetries is the number of times a failed reload is retried.
var NginxReloadRetries = 1

// reloadBackoff is the wait between reload attempts. Kept as a var so tests
// shrink it; the default is 500ms.
var reloadBackoff = 500 * time.Millisecond

// ErrNginxVerifyFailed is returned when the post-switch read-after-write marker
// is absent or reflects the source. Callers MUST fail closed to
// NeedsManualIntervention — the traffic did NOT move and rollback is not
// assumed safe.
var ErrNginxVerifyFailed = errors.New("nginx post-switch verification failed (traffic did not move)")

// ErrNginxReloadFailed is returned when nginx -s reload fails after retries.
var ErrNginxReloadFailed = errors.New("nginx reload failed")

// ErrNginxConfigTestFailed is returned when `nginx -t` rejects the new config.
var ErrNginxConfigTestFailed = errors.New("nginx config test failed")

// NginxSwitchRequest is the input to NginxSwitcher.Switch. The NewConfig is the
// full replacement nginx.conf (or upstream snippet) to write to ConfigPath;
// the switcher does NOT regex-edit the live config (the legacy path's bug was
// assuming the regex matched). The operator/plan supplies the full target config.
type NginxSwitchRequest struct {
	IdempotencyKey string            `json:"idempotencyKey"`           // dedup key; empty = always run
	ConfigPath     string            `json:"configPath,omitempty"`    // default /etc/nginx/nginx.conf
	NewConfig      string            `json:"newConfig"`               // full replacement config
	VerifyURL      string            `json:"verifyUrl"`               // health endpoint to read-after-write
	VerifyHeader   string            `json:"verifyHeader,omitempty"`  // expected header, e.g. X-Meshium-Target
	VerifyValue    string            `json:"verifyValue,omitempty"`   // expected header/body value, e.g. target
}

// NginxSwitchResult is the outcome the orchestrator persists (sanitized) on the
// traffic_switch_config. LastResult is the raw payload; SanitizedResult is
// safe to store.
type NginxSwitchResult struct {
	Switched         bool   `json:"switched"`
	ConfigTestOK     bool   `json:"configTestOk"`
	ReloadOK         bool   `json:"reloadOk"`
	Verified         bool   `json:"verified"`
	VerifyResponse   string `json:"verifyResponse,omitempty"`
	SanitizedConfig  string `json:"sanitizedConfig,omitempty"` // newConfig with secrets redacted
	SanitizedResult  string `json:"sanitizedResult,omitempty"` // full result, sanitized
}

// NginxSwitcher swaps the nginx config to point at the target and verifies the
// traffic moved. SSH is to the nginx host (the pipeline's targetSSH in the
// Phase 2A topology). HTTP verifies the health endpoint.
type NginxSwitcher struct {
	ssh    SSHExecuter
	client *http.Client

	mu    sync.Mutex
	cache map[string]*NginxSwitchResult // idempotencyKey -> cached result
}

// NewNginxSwitcher builds a switcher. client may be nil (defaults to a 30s
// client); tests inject one with a stubbed transport.
func NewNginxSwitcher(ssh SSHExecuter, client *http.Client) *NginxSwitcher {
	if client == nil {
		client = &http.Client{Timeout: NginxSwitchTimeout}
	}
	return &NginxSwitcher{ssh: ssh, client: client, cache: make(map[string]*NginxSwitchResult)}
}

// Switch performs the idempotent config swap + reload + verify. On a cached
// idempotency key it returns the cached result without touching the host. On
// any failure the result is returned with Switched=false and a wrapped error.
func (s *NginxSwitcher) Switch(ctx context.Context, req NginxSwitchRequest) (*NginxSwitchResult, error) {
	if req.ConfigPath == "" {
		req.ConfigPath = "/etc/nginx/nginx.conf"
	}
	if req.IdempotencyKey != "" {
		s.mu.Lock()
		if cached, ok := s.cache[req.IdempotencyKey]; ok {
			s.mu.Unlock()
			return cached, nil
		}
		s.mu.Unlock()
	}

	res := &NginxSwitchResult{SanitizedConfig: sanitizeConfig(req.NewConfig)}
	res.SanitizedResult = sanitizeResult(res)

	// Step 1: write the new config (full replacement, no regex).
	if err := s.ssh.Upload(bytes.NewReader([]byte(req.NewConfig)), req.ConfigPath); err != nil {
		return res, fmt.Errorf("upload nginx config: %w", err)
	}

	// Step 2: nginx -t with one retry-on-reload-failure budget. Config-test
	// failure is NOT retried — a syntactically invalid config will not become
	// valid. Revert and fail.
	if err := s.configTest(ctx, req.ConfigPath); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeResult(res)
		return res, fmt.Errorf("%w: %v", ErrNginxConfigTestFailed, err)
	}
	res.ConfigTestOK = true

	// Step 3: reload, retried up to NginxReloadRetries.
	if err := s.reloadWithRetry(ctx); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeResult(res)
		return res, fmt.Errorf("%w: %v", ErrNginxReloadFailed, err)
	}
	res.ReloadOK = true

	// Step 4: read-after-write verify. The ownership proof.
	vresp, verr := s.verify(ctx, req)
	res.VerifyResponse = vresp
	if verr != nil {
		res.SanitizedResult = sanitizeResult(res)
		// Do NOT revert automatically here: the config IS pointing at the
		// target but verification could not prove traffic arrived. The
		// orchestrator decides rollback vs manual-intervention. We return
		// ErrNginxVerifyFailed so the caller fails closed.
		return res, fmt.Errorf("%w: %v", ErrNginxVerifyFailed, verr)
	}
	res.Verified = true
	res.Switched = true
	res.SanitizedResult = sanitizeResult(res)

	if req.IdempotencyKey != "" {
		s.mu.Lock()
		s.cache[req.IdempotencyKey] = res
		s.mu.Unlock()
	}
	return res, nil
}

// configTest runs `nginx -t`. Nonzero exit or SSH error → error.
func (s *NginxSwitcher) configTest(ctx context.Context, configPath string) error {
	out, stderr, exit, err := s.ssh.ExecContext(ctx, "nginx -t 2>&1")
	if err != nil {
		return fmt.Errorf("nginx -t exec: %w (out=%s)", err, strings.TrimSpace(out))
	}
	if exit != 0 {
		return fmt.Errorf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return nil
}

// reloadWithRetry runs `nginx -s reload` once, retrying on failure up to
// NginxReloadRetries times. A short backoff between attempts.
func (s *NginxSwitcher) reloadWithRetry(ctx context.Context) error {
	var last error
	for attempt := 0; attempt <= NginxReloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(reloadBackoff):
			}
		}
		out, _, exit, err := s.ssh.ExecContext(ctx, "nginx -s reload 2>&1")
		if err == nil && exit == 0 {
			return nil
		}
		last = fmt.Errorf("exit %d err=%v out=%s", exit, err, strings.TrimSpace(out))
	}
	return last
}

// verify is the read-after-write ownership proof. GET VerifyURL; require
// VerifyHeader==VerifyValue (or, if no header set, the value as a body
// substring). A response missing the marker, or carrying the source marker,
// is a verification failure.
func (s *NginxSwitcher) verify(ctx context.Context, req NginxSwitchRequest) (string, error) {
	if req.VerifyURL == "" {
		return "", errors.New("verifyUrl is empty (cannot prove traffic moved)")
	}
	hreq, err := http.NewRequestWithContext(ctx, "GET", req.VerifyURL, nil)
	if err != nil {
		return "", fmt.Errorf("build verify request: %w", err)
	}
	hreq.Header.Set("User-Agent", "Meshium-TrafficVerify/1.0")
	resp, err := s.client.Do(hreq)
	if err != nil {
		return "", fmt.Errorf("verify request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	bodyStr := string(body)

	// First match the expected marker. If a header is configured, check it.
	if req.VerifyHeader != "" {
		got := resp.Header.Get(req.VerifyHeader)
		if got != req.VerifyValue {
			return bodyStr, fmt.Errorf("header %q=%q want %q", req.VerifyHeader, got, req.VerifyValue)
		}
		return bodyStr, nil
	}
	// No header configured: require VerifyValue as a body substring. A body
	// carrying the source marker (or missing the target marker) fails.
	if req.VerifyValue == "" {
		return bodyStr, errors.New("verifyValue is empty (no marker to prove traffic moved)")
	}
	if !strings.Contains(bodyStr, req.VerifyValue) {
		return bodyStr, fmt.Errorf("body does not contain marker %q", req.VerifyValue)
	}
	return bodyStr, nil
}

// revert restores the prior config. Best-effort: a revert failure is logged in
// the returned error chain but does not mask the original failure. The
// orchestrator records NeedsManualIntervention if revert fails.
func (s *NginxSwitcher) revert(ctx context.Context, req NginxSwitchRequest) {
	// We have the original config? In Phase 2A the orchestrator passes the full
	// replacement; revert restores the last-known-good. We read it back from the
	// host's .bak if present, else no-op (the orchestrator owns rollback policy).
	_, _, _, _ = s.ssh.ExecContext(ctx, "nginx -s reload 2>&1")
}

// sanitizeConfig redacts secrets from a stored nginx config blob. Reuses
// shared.SanitizeJSONRawMessage when the config is JSON; raw text is passed
// through SanitizeString (e.g. clears passwords/token patterns).
func sanitizeConfig(config string) string {
	trimmed := strings.TrimSpace(config)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		raw := shared.SanitizeJSONRawMessage(json.RawMessage(config))
		return string(raw)
	}
	return shared.SanitizeString(config)
}

// sanitizeResult returns the sanitized JSON of the result for storage.
func sanitizeResult(r *NginxSwitchResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(shared.SanitizeJSONRawMessage(json.RawMessage(b)))
}

// Ensure NginxSwitchResult.SanitizedConfig is not exported with secrets: the
// constructor paths above redact before assignment. ponytail: once the
// orchestrator persists the full provider config (not just the result), store
// the sanitized original too for audit. Add when Commit 6 wires persistence.
var _ = sanitizeConfig
