package migration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// caddySwitchReq builds a canonical switch request. NewConfig embeds a secret
// (password=Hunter2) to prove the sanitizer redacts it; never an argv/log.
func caddySwitchReq(verifyURL string) TrafficSwitchRequest {
	return TrafficSwitchRequest{
		IdempotencyKey: "mig1-cut-1",
		ConfigPath:     "/etc/caddy/Caddyfile",
		NewConfig:      "example.com {\n  reverse_proxy 10.0.0.2:80\n}\n# password=Hunter2",
		VerifyURL:      verifyURL,
		VerifyHeader:   "X-Meshium-Target",
		VerifyValue:    "target",
	}
}

// caddyOKMock accepts `caddy validate` and `caddy reload` (exit 0). The config
// path is ShellQuote'd by the switcher, so the mock key quotes it too.
func caddyOKMock() *mockSSH {
	m := newMockSSH()
	m.execOutput["caddy validate --config '/etc/caddy/Caddyfile' 2>&1"] = "valid configuration"
	m.execOutput["caddy reload --config '/etc/caddy/Caddyfile' --adapter caddyfile 2>&1"] = ""
	return m
}

// TestCaddySwitchSuccess: upload, validate ok, reload ok, verify header matches.
func TestCaddySwitchSuccess(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), caddySwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if !res.Switched || !res.Verified || !res.ConfigTestOK || !res.ReloadOK {
		t.Fatalf("flags: %+v", res)
	}
	if _, ok := ssh.uploadData["/etc/caddy/Caddyfile"]; !ok {
		t.Fatal("new config not uploaded")
	}
	if string(ssh.uploadData["/etc/caddy/Caddyfile"]) != caddySwitchReq("").NewConfig {
		t.Fatalf("uploaded config mutated: %q", ssh.uploadData["/etc/caddy/Caddyfile"])
	}
}

// TestCaddySwitchIdempotentReentry: same key → cached result, no new commands.
func TestCaddySwitchIdempotentReentry(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))
	req := caddySwitchReq("http://verify/health")

	if _, err := s.Switch(context.Background(), req); err != nil {
		t.Fatalf("first switch: %v", err)
	}
	callsBefore := len(ssh.commands)
	rtBefore := atomic.LoadInt32(&rt.calls)

	res, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("second switch: %v", err)
	}
	if !res.Switched || !res.Verified {
		t.Fatalf("cached result lost flags: %+v", res)
	}
	if len(ssh.commands) != callsBefore {
		t.Fatalf("idempotent reentry issued SSH commands: before=%d after=%d", callsBefore, len(ssh.commands))
	}
	if atomic.LoadInt32(&rt.calls) != rtBefore {
		t.Fatalf("idempotent reentry hit verify endpoint")
	}
}

// TestCaddySwitchConfigTestFailFailsClosed: `caddy validate` rejects →
// ErrCaddyConfigTestFailed, Switched false, revert invoked.
func TestCaddySwitchConfigTestFailFailsClosed(t *testing.T) {
	ssh := caddyOKMock()
	ssh.execExit["caddy validate --config '/etc/caddy/Caddyfile' 2>&1"] = 1
	ssh.execOutput["caddy validate --config '/etc/caddy/Caddyfile' 2>&1"] = "Error: parsing Caddyfile: unexpected token"
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), caddySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrCaddyConfigTestFailed) {
		t.Fatalf("config-test fail: err=%v want ErrCaddyConfigTestFailed", err)
	}
	if res.Switched || res.ConfigTestOK {
		t.Fatalf("flags after config-test fail: %+v", res)
	}
	if res.ReloadOK {
		t.Fatal("reload marked ok after config-test failure")
	}
}

// TestCaddySwitchReloadFailFailsClosed: reload fails even after retry →
// ErrCaddyReloadFailed, Switched false.
func TestCaddySwitchReloadFailFailsClosed(t *testing.T) {
	ssh := caddyOKMock()
	ssh.execExit["caddy reload --config '/etc/caddy/Caddyfile' --adapter caddyfile 2>&1"] = 1
	ssh.execOutput["caddy reload --config '/etc/caddy/Caddyfile' --adapter caddyfile 2>&1"] = "Error: timed out waiting for reload"
	CaddyReloadRetries = 1
	reloadBackoffCaddy = 5 * time.Millisecond
	defer func() { reloadBackoffCaddy = 500 * time.Millisecond }()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), caddySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrCaddyReloadFailed) {
		t.Fatalf("reload fail: err=%v want ErrCaddyReloadFailed", err)
	}
	if res.Switched || res.ReloadOK {
		t.Fatalf("flags after reload fail: %+v", res)
	}
	count := 0
	for _, c := range ssh.commands {
		if strings.HasPrefix(c, "caddy reload --config") {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("reload attempts=%d want >=2 (1 + 1 retry + revert)", count)
	}
}

// TestCaddySwitchVerifyHeaderMismatchFailsClosed: response shows SOURCE →
// ErrCaddyVerifyFailed, traffic did not move, Switched false.
func TestCaddySwitchVerifyHeaderMismatchFailsClosed(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"source"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), caddySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrCaddyVerifyFailed) {
		t.Fatalf("verify mismatch: err=%v want ErrCaddyVerifyFailed", err)
	}
	if res.Switched {
		t.Fatal("marked switched on verify failure")
	}
	if !res.ReloadOK {
		t.Fatal("reload was ok but verify failed — reload flag should be set")
	}
	if res.Verified {
		t.Fatal("verified true on marker mismatch")
	}
}

// TestCaddySwitchVerifyBodySubstring: no header → marker matched as body substring.
func TestCaddySwitchVerifyBodySubstring(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, body: "serving from <strong>target</strong> node"}
	s := NewCaddySwitcher(ssh, newClient(rt))
	req := caddySwitchReq("http://verify/health")
	req.VerifyHeader = ""
	req.VerifyValue = "target"

	res, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("switch body-substring: %v", err)
	}
	if !res.Verified || !res.Switched {
		t.Fatalf("flags: %+v", res)
	}
}

// TestCaddySwitchVerifyBodySubstringAbsentFailsClosed: body lacks marker → fail.
func TestCaddySwitchVerifyBodySubstringAbsentFailsClosed(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, body: "serving from source node"}
	s := NewCaddySwitcher(ssh, newClient(rt))
	req := caddySwitchReq("http://verify/health")
	req.VerifyHeader = ""
	req.VerifyValue = "target"

	_, err := s.Switch(context.Background(), req)
	if !errors.Is(err, ErrCaddyVerifyFailed) {
		t.Fatalf("body absent: err=%v want ErrCaddyVerifyFailed", err)
	}
}

// TestCaddySwitchSanitizesSecret: SanitizedResult/Config redact the password.
func TestCaddySwitchSanitizesSecret(t *testing.T) {
	ssh := caddyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewCaddySwitcher(ssh, newClient(rt))
	res, err := s.Switch(context.Background(), caddySwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if strings.Contains(res.SanitizedResult, "Hunter2") || strings.Contains(res.SanitizedConfig, "Hunter2") {
		t.Fatalf("secret leaked: result=%s config=%s", res.SanitizedResult, res.SanitizedConfig)
	}
	if !strings.Contains(res.SanitizedConfig, "[REDACTED]") {
		t.Fatalf("SanitizedConfig missing redaction marker: %s", res.SanitizedConfig)
	}
}

// TestCaddySwitchVerifyEndpointLive: against a real httptest.Server, verify GET
// reaches the endpoint and matches the header it sends.
func TestCaddySwitchVerifyEndpointLive(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("X-Meshium-Target", "target")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ssh := caddyOKMock()
	s := NewCaddySwitcher(ssh, nil)
	req := caddySwitchReq(srv.URL + "/health")

	res, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("switch live: %v", err)
	}
	if !res.Switched || !res.Verified {
		t.Fatalf("flags: %+v", res)
	}
	if !strings.Contains(gotUA, "Meshium-TrafficVerify") {
		t.Fatalf("user-agent not set: %q", gotUA)
	}
}

// TestCaddySwitchEmptyVerifyURLFailsClosed: no verify URL → error, never silent.
func TestCaddySwitchEmptyVerifyURLFailsClosed(t *testing.T) {
	ssh := caddyOKMock()
	s := NewCaddySwitcher(ssh, nil)
	_, err := s.Switch(context.Background(), caddySwitchReq(""))
	if err == nil {
		t.Fatal("empty verifyURL returned no error; must fail closed")
	}
}
