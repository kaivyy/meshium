package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"meshium/internal/shared"
)

// Phase 2C-5: HAProxy traffic-switch provider — the SECOND supported auto-switch
// provider (Nginx is the first). It mirrors NginxSwitcher exactly: idempotent
// (idempotency key + in-process cache), bounded (30s, one retry), verified
// (read-after-write health marker → ErrTrafficVerifyFailed fails closed),
// sanitized (shared.SanitizeJSONRawMessage). HAProxy is config-file based like
// nginx, so the switch is a server-line edit + reload; ownership is proven with
// the same post-switch GET marker as Nginx.
//
// The legacy switchHAProxy in traffic.go edits server lines via a regex +
// reload with NO idempotency key, no read-after-write ownership proof, and no
// sanitized persistence — exactly the gaps spec §7 calls out. This switcher
// replaces it for the fenced-autocutover path. The live pipeline's
// trafficSwitchStage (manual cutover) is untouched.

// HAProxySwitchTimeout bounds a single switch attempt.
var HAProxySwitchTimeout = 30 * time.Second

// HAProxyReloadRetries is the number of reload retries on failure.
var HAProxyReloadRetries = 1

// haproxyReloadBackoff is the wait between reload attempts.
var haproxyReloadBackoff = 500 * time.Millisecond

// ErrHAProxyVerifyFailed is returned when the post-switch read-after-write marker
// is absent or reflects the source. Callers MUST fail closed to
// NeedsManualIntervention — the traffic did NOT move and rollback is not assumed
// safe. HAProxySwitcher reuses the shared ErrTrafficVerifyFailed below.

// HAProxySwitcher swaps the active server in the HAProxy backend to point at the
// target and verifies the traffic moved. SSH is to the haproxy host; HTTP
// verifies the health endpoint (the same read-after-write marker contract as
// Nginx).
type HAProxySwitcher struct {
	ssh    SSHExecuter
	client *http.Client

	mu    sync.Mutex
	cache map[string]*TrafficSwitchResult // idempotencyKey -> cached result
}

// NewHAProxySwitcher builds a switcher. client may be nil (defaults to a 30s
// client); tests inject one with a stubbed transport.
func NewHAProxySwitcher(ssh SSHExecuter, client *http.Client) *HAProxySwitcher {
	if client == nil {
		client = &http.Client{Timeout: HAProxySwitchTimeout}
	}
	return &HAProxySwitcher{ssh: ssh, client: client, cache: make(map[string]*TrafficSwitchResult)}
}

// Switch performs the idempotent config swap + reload + verify. On a cached
// idempotency key it returns the cached result without touching the host. On any
// failure the result is returned with Switched=false and a wrapped error.
func (s *HAProxySwitcher) Switch(ctx context.Context, req TrafficSwitchRequest) (*TrafficSwitchResult, error) {
	if req.ConfigPath == "" {
		req.ConfigPath = "/etc/haproxy/haproxy.cfg"
	}
	if req.IdempotencyKey != "" {
		s.mu.Lock()
		if cached, ok := s.cache[req.IdempotencyKey]; ok {
			s.mu.Unlock()
			return cached, nil
		}
		s.mu.Unlock()
	}

	res := &TrafficSwitchResult{SanitizedConfig: sanitizeTrafficConfig(req.NewConfig)}
	res.SanitizedResult = sanitizeTrafficResult(res)

	// Step 1: write the full replacement config (no regex edit — the operator
	// supplies the target config; we do not assume our regex matched).
	if err := s.ssh.Upload(bytes.NewReader([]byte(req.NewConfig)), req.ConfigPath); err != nil {
		return res, fmt.Errorf("upload haproxy config: %w", err)
	}

	// Step 2: haproxy -c config test. A syntactically invalid config will not
	// become valid, so no retry — revert and fail.
	if err := s.configTest(ctx, req.ConfigPath); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeTrafficResult(res)
		return res, fmt.Errorf("%w: %v", ErrHAProxyConfigTestFailed, err)
	}
	res.ConfigTestOK = true

	// Step 3: reload, retried up to HAProxyReloadRetries.
	if err := s.reloadWithRetry(ctx); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeTrafficResult(res)
		return res, fmt.Errorf("%w: %v", ErrHAProxyReloadFailed, err)
	}
	res.ReloadOK = true

	// Step 4: read-after-write verify — the ownership proof.
	vresp, verr := s.verify(ctx, req)
	res.VerifyResponse = vresp
	if verr != nil {
		res.SanitizedResult = sanitizeTrafficResult(res)
		// Config IS pointing at the target but verification could not prove
		// traffic arrived. The orchestrator decides rollback vs manual. Return
		// the shared ErrTrafficVerifyFailed so it fails closed.
		return res, fmt.Errorf("%w: %v", ErrTrafficVerifyFailed, verr)
	}
	res.Verified = true
	res.Switched = true
	res.SanitizedResult = sanitizeTrafficResult(res)

	if req.IdempotencyKey != "" {
		s.mu.Lock()
		s.cache[req.IdempotencyKey] = res
		s.mu.Unlock()
	}
	return res, nil
}

func (s *HAProxySwitcher) configTest(ctx context.Context, configPath string) error {
	out, stderr, exit, err := s.ssh.ExecContext(ctx, "haproxy -c -f "+shared.ShellQuote(configPath)+" 2>&1")
	if err != nil {
		return fmt.Errorf("haproxy -c exec: %w (out=%s)", err, strings.TrimSpace(out))
	}
	if exit != 0 {
		return fmt.Errorf("exit %d: %s", exit, strings.TrimSpace(stderr))
	}
	return nil
}

func (s *HAProxySwitcher) reloadWithRetry(ctx context.Context) error {
	var last error
	for attempt := 0; attempt <= HAProxyReloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(haproxyReloadBackoff):
			}
		}
		// Prefer a seamless reload; fall back to restart on older setups.
		out, _, exit, err := s.ssh.ExecContext(ctx, "systemctl reload haproxy 2>&1 || systemctl restart haproxy 2>&1")
		if err == nil && exit == 0 {
			return nil
		}
		last = fmt.Errorf("exit %d err=%v out=%s", exit, err, strings.TrimSpace(out))
	}
	return last
}

// verify is the read-after-write ownership proof, identical to NginxSwitcher's.
func (s *HAProxySwitcher) verify(ctx context.Context, req TrafficSwitchRequest) (string, error) {
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

	if req.VerifyHeader != "" {
		got := resp.Header.Get(req.VerifyHeader)
		if got != req.VerifyValue {
			return bodyStr, fmt.Errorf("header %q=%q want %q", req.VerifyHeader, got, req.VerifyValue)
		}
		return bodyStr, nil
	}
	if req.VerifyValue == "" {
		return bodyStr, errors.New("verifyValue is empty (no marker to prove traffic moved)")
	}
	if !strings.Contains(bodyStr, req.VerifyValue) {
		return bodyStr, fmt.Errorf("body does not contain marker %q", req.VerifyValue)
	}
	return bodyStr, nil
}

func (s *HAProxySwitcher) revert(ctx context.Context, req TrafficSwitchRequest) {
	// Best-effort reload; the orchestrator owns rollback policy. The original
	// config is restored by the stage if a prior good config is known.
	s.ssh.ExecContext(ctx, "systemctl reload haproxy 2>&1")
}
