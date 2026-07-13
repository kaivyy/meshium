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

// Phase 4B: Caddy traffic-switch provider — the third fenced switcher, mirroring
// NginxSwitcher / HAProxySwitcher exactly (idempotent full-replacement upload +
// config validate + reload + read-after-write verify + sanitized persist). It
// shares the generic TrafficSwitchRequest / TrafficSwitchResult contract.
//
// Why Caddy: the lowest-risk 4B candidate is a second reverse-proxy adapter
// with the identical contract already proven for nginx/haproxy, so the
// read-after-write ownership proof runs end-to-end without external credentials.
//
// Contract (identical to nginx/haproxy):
//   - Idempotent: a second Switch with the same IdempotencyKey is a no-op
//     returning the cached result.
//   - Bounded: CaddySwitchTimeout per attempt, one reload retry.
//   - Verified: after reload, GET VerifyURL and require a distinct target marker.
//     A response reflecting the source (or no marker) is a verification FAILURE —
//     traffic did not move; fail closed to NeedsManualIntervention. Do NOT assume
//     rollback safe.
//   - Sanitized: request/result stored with secrets redacted.

// CaddySwitchTimeout bounds a single switch attempt (validate + reload + verify).
var CaddySwitchTimeout = 30 * time.Second

// CaddyReloadRetries is the number of times a failed reload is retried.
var CaddyReloadRetries = 1

// reloadBackoffCaddy is the wait between reload attempts (var so tests shrink it).
var reloadBackoffCaddy = 500 * time.Millisecond

// ErrCaddyVerifyFailed is returned when the post-switch read-after-write marker is
// absent or reflects the source. Callers MUST fail closed to
// NeedsManualIntervention.
var ErrCaddyVerifyFailed = errors.New("caddy post-switch verification failed (traffic did not move)")

// ErrCaddyReloadFailed is returned when `caddy reload` fails after retries.
var ErrCaddyReloadFailed = errors.New("caddy reload failed")

// ErrCaddyConfigTestFailed is returned when `caddy validate` rejects the config.
var ErrCaddyConfigTestFailed = errors.New("caddy config validate failed")

// CaddySwitcher swaps the Caddyfile to point at the target and verifies the
// traffic moved. SSH is to the caddy host; HTTP verifies the health endpoint.
type CaddySwitcher struct {
	ssh    SSHExecuter
	client *http.Client

	mu    sync.Mutex
	cache map[string]*TrafficSwitchResult // idempotencyKey -> cached result
}

// NewCaddySwitcher builds a switcher. client may be nil (defaults to a 30s
// client); tests inject one with a stubbed transport.
func NewCaddySwitcher(ssh SSHExecuter, client *http.Client) *CaddySwitcher {
	if client == nil {
		client = &http.Client{Timeout: CaddySwitchTimeout}
	}
	return &CaddySwitcher{ssh: ssh, client: client, cache: make(map[string]*TrafficSwitchResult)}
}

// Switch performs the idempotent config swap + validate + reload + verify. See
// the NginxSwitcher contract (this mirrors it). On any failure the result is
// returned with Switched=false and a wrapped error.
func (s *CaddySwitcher) Switch(ctx context.Context, req TrafficSwitchRequest) (*TrafficSwitchResult, error) {
	if req.ConfigPath == "" {
		req.ConfigPath = "/etc/caddy/Caddyfile"
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

	// Step 1: write the new config (full replacement).
	if err := s.ssh.Upload(bytes.NewReader([]byte(req.NewConfig)), req.ConfigPath); err != nil {
		return res, fmt.Errorf("upload caddy config: %w", err)
	}

	// Step 2: `caddy validate` (config test). Failure is NOT retried; revert.
	if err := s.configTest(ctx, req.ConfigPath); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeTrafficResult(res)
		return res, fmt.Errorf("%w: %v", ErrCaddyConfigTestFailed, err)
	}
	res.ConfigTestOK = true

	// Step 3: reload, retried up to CaddyReloadRetries.
	if err := s.reloadWithRetry(ctx, req.ConfigPath); err != nil {
		s.revert(ctx, req)
		res.SanitizedResult = sanitizeTrafficResult(res)
		return res, fmt.Errorf("%w: %v", ErrCaddyReloadFailed, err)
	}
	res.ReloadOK = true

	// Step 4: read-after-write verify — the ownership proof.
	vresp, verr := s.verify(ctx, req)
	res.VerifyResponse = vresp
	if verr != nil {
		res.SanitizedResult = sanitizeTrafficResult(res)
		// Do NOT auto-revert: the config IS pointing at the target but we could
		// not prove traffic arrived. The orchestrator decides rollback vs manual.
		return res, fmt.Errorf("%w: %v", ErrCaddyVerifyFailed, verr)
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

// configTest runs `caddy validate`. Nonzero exit or SSH error → error.
func (s *CaddySwitcher) configTest(ctx context.Context, configPath string) error {
	out, stderr, exit, err := s.ssh.ExecContext(ctx, fmt.Sprintf("caddy validate --config %s 2>&1", shared.ShellQuote(configPath)))
	if err != nil {
		return fmt.Errorf("caddy validate exec: %w (out=%s)", err, shared.SanitizeString(strings.TrimSpace(out)))
	}
	if exit != 0 {
		return fmt.Errorf("exit %d: %s", exit, shared.SanitizeString(strings.TrimSpace(stderr)))
	}
	return nil
}

// reloadWithRetry runs `caddy reload` once, retrying on failure up to
// CaddyReloadRetries times.
func (s *CaddySwitcher) reloadWithRetry(ctx context.Context, configPath string) error {
	var last error
	for attempt := 0; attempt <= CaddyReloadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(reloadBackoffCaddy):
			}
		}
		cmd := fmt.Sprintf("caddy reload --config %s --adapter caddyfile 2>&1", shared.ShellQuote(configPath))
		out, _, exit, err := s.ssh.ExecContext(ctx, cmd)
		if err == nil && exit == 0 {
			return nil
		}
		last = fmt.Errorf("exit %d err=%v out=%s", exit, err, shared.SanitizeString(strings.TrimSpace(out)))
	}
	return last
}

// verify is the read-after-write ownership proof (identical to NginxSwitcher).
func (s *CaddySwitcher) verify(ctx context.Context, req TrafficSwitchRequest) (string, error) {
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

// revert is best-effort: re-runs `caddy reload` to pick up a pre-existing prior
// config (the orchestrator owns the full rollback policy / last-known-good).
func (s *CaddySwitcher) revert(ctx context.Context, req TrafficSwitchRequest) {
	path := req.ConfigPath
	if path == "" {
		path = "/etc/caddy/Caddyfile"
	}
	_, _, _, _ = s.ssh.ExecContext(ctx,
		fmt.Sprintf("caddy reload --config %s --adapter caddyfile 2>&1", shared.ShellQuote(path)))
}
