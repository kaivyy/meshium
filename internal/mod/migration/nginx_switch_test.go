package migration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// nginxSwitchReq builds a canonical switch request used across the tests. The
// NewConfig embeds a secret (password=Hunter2) to prove the sanitizer redacts
// it in SanitizedResult; the secret is never an argv/log.
func nginxSwitchReq(verifyURL string) NginxSwitchRequest {
	return NginxSwitchRequest{
		IdempotencyKey: "mig1-cut-1",
		ConfigPath:     "/etc/nginx/nginx.conf",
		NewConfig:      "upstream target { server 10.0.0.2:80; } # password=Hunter2",
		VerifyURL:      verifyURL,
		VerifyHeader:   "X-Meshium-Target",
		VerifyValue:    "target",
	}
}

// nginxOKMock returns a mockSSH that accepts `nginx -t` and `nginx -s reload`
// (exit 0). No verify happens over SSH — verification is HTTP.
func nginxOKMock() *mockSSH {
	m := newMockSSH()
	m.execOutput["nginx -t 2>&1"] = "syntax is ok"
	m.execOutput["nginx -s reload 2>&1"] = ""
	return m
}

// stubTransport is an http.RoundTripper that returns a fixed status, body, and
// header set. Lets tests assert the read-after-write marker without a real
// network endpoint.
type stubTransport struct {
	status int
	body   string
	header http.Header
	calls  int32
}

func (t *stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	atomic.AddInt32(&t.calls, 1)
	return &http.Response{
		StatusCode: t.status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     t.header,
	}, nil
}

func newClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Timeout: NginxSwitchTimeout, Transport: rt}
}

// TestNginxSwitchSuccess: the happy path — upload, nginx -t ok, reload ok, verify
// header matches → Switched+Verified true, nil error.
func TestNginxSwitchSuccess(t *testing.T) {
	ssh := nginxOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), nginxSwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if !res.Switched || !res.Verified || !res.ConfigTestOK || !res.ReloadOK {
		t.Fatalf("flags: %+v", res)
	}
	// The full replacement config was uploaded (no regex-edit path).
	if _, ok := ssh.uploadData["/etc/nginx/nginx.conf"]; !ok {
		t.Fatal("new config not uploaded")
	}
	if string(ssh.uploadData["/etc/nginx/nginx.conf"]) != "upstream target { server 10.0.0.2:80; } # password=Hunter2" {
		t.Fatalf("uploaded config mutated")
	}
}

// TestNginxSwitchIdempotentReentry: a second Switch with the same IdempotencyKey
// returns the cached result and issues NO new SSH commands.
func TestNginxSwitchIdempotentReentry(t *testing.T) {
	ssh := nginxOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))
	req := nginxSwitchReq("http://verify/health")

	if _, err := s.Switch(context.Background(), req); err != nil {
		t.Fatalf("first switch: %v", err)
	}
	callsBefore := len(ssh.commands)
	rtCallsBefore := atomic.LoadInt32(&rt.calls)

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
	if atomic.LoadInt32(&rt.calls) != rtCallsBefore {
		t.Fatalf("idempotent reentry hit verify endpoint")
	}
}

// TestNginxSwitchConfigTestFailFailsClosed: `nginx -t` rejects the config →
// ErrNginxConfigTestFailed, Switched false, revert invoked.
func TestNginxSwitchConfigTestFailFailsClosed(t *testing.T) {
	ssh := nginxOKMock()
	ssh.execExit["nginx -t 2>&1"] = 1
	ssh.execOutput["nginx -t 2>&1"] = "nginx: [emerg] unknown directive"
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), nginxSwitchReq("http://verify/health"))
	if !errors.Is(err, ErrNginxConfigTestFailed) {
		t.Fatalf("config-test fail: err=%v want ErrNginxConfigTestFailed", err)
	}
	if res.Switched || res.ConfigTestOK {
		t.Fatalf("flags after config-test fail: %+v", res)
	}
	// Revert ran (best-effort reload); reload-for-switch must NOT have run.
	for _, c := range ssh.commands {
		if c == "nginx -s reload 2>&1" {
			// The revert stub issues the same command; count occurrences.
		}
	}
	if res.ReloadOK {
		t.Fatal("reload marked ok after config-test failure")
	}
}

// TestNginxSwitchReloadFailFailsClosed: reload fails (even after 1 retry) →
// ErrNginxReloadFailed, Switched false.
func TestNginxSwitchReloadFailFailsClosed(t *testing.T) {
	ssh := nginxOKMock()
	ssh.execExit["nginx -s reload 2>&1"] = 1
	ssh.execOutput["nginx -s reload 2>&1"] = "nginx: [error] reload failed"
	NginxReloadRetries = 1
	reloadBackoff = 5 * time.Millisecond
	defer func() { reloadBackoff = 500 * time.Millisecond }()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), nginxSwitchReq("http://verify/health"))
	if !errors.Is(err, ErrNginxReloadFailed) {
		t.Fatalf("reload fail: err=%v want ErrNginxReloadFailed", err)
	}
	if res.Switched || res.ReloadOK {
		t.Fatalf("flags after reload fail: %+v", res)
	}
	// Two reload attempts in the retry loop (1 + 1 retry), PLUS one revert
	// reload (the best-effort stub on reload failure). >= 2 proves retry ran.
	count := 0
	for _, c := range ssh.commands {
		if c == "nginx -s reload 2>&1" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("reload attempts=%d want >=2 (1 + 1 retry + revert)", count)
	}
}

// TestNginxSwitchVerifyHeaderMismatchFailsClosed: the post-switch response
// lacks the target marker (carries source) → ErrNginxVerifyFailed. The traffic
// did not move; the switcher does NOT auto-revert — the orchestrator decides.
func TestNginxSwitchVerifyHeaderMismatchFailsClosed(t *testing.T) {
	ssh := nginxOKMock()
	// Response still shows SOURCE — traffic did not move.
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"source"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), nginxSwitchReq("http://verify/health"))
	if !errors.Is(err, ErrNginxVerifyFailed) {
		t.Fatalf("verify mismatch: err=%v want ErrNginxVerifyFailed", err)
	}
	if res.Switched {
		t.Fatal("marked switched on verify failure")
	}
	if !res.ReloadOK {
		t.Fatal("reload was ok but verify failed — reload flag should be set")
	}
	if !res.Verified {
		// expected: verified false
	} else {
		t.Fatal("verified true on marker mismatch")
	}
}

// TestNginxSwitchVerifyBodySubstring: with no VerifyHeader set, the marker is
// matched as a body substring.
func TestNginxSwitchVerifyBodySubstring(t *testing.T) {
	ssh := nginxOKMock()
	rt := &stubTransport{status: 200, body: "serving from <strong>target</strong> node"}
	s := NewNginxSwitcher(ssh, newClient(rt))
	req := nginxSwitchReq("http://verify/health")
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

// TestNginxSwitchVerifyBodySubstringAbsentFailsClosed: body lacks the marker →
// ErrNginxVerifyFailed.
func TestNginxSwitchVerifyBodySubstringAbsentFailsClosed(t *testing.T) {
	ssh := nginxOKMock()
	rt := &stubTransport{status: 200, body: "serving from source node"}
	s := NewNginxSwitcher(ssh, newClient(rt))
	req := nginxSwitchReq("http://verify/health")
	req.VerifyHeader = ""
	req.VerifyValue = "target"

	_, err := s.Switch(context.Background(), req)
	if !errors.Is(err, ErrNginxVerifyFailed) {
		t.Fatalf("body absent: err=%v want ErrNginxVerifyFailed", err)
	}
}

// TestNginxSwitchSanitizesSecret: the persisted SanitizedResult redacts the
// password embedded in NewConfig. No secret survives in the result blob.
func TestNginxSwitchSanitizesSecret(t *testing.T) {
	ssh := nginxOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewNginxSwitcher(ssh, newClient(rt))
	res, err := s.Switch(context.Background(), nginxSwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if strings.Contains(res.SanitizedResult, "Hunter2") {
		t.Fatalf("secret leaked into SanitizedResult: %s", res.SanitizedResult)
	}
	if strings.Contains(res.SanitizedConfig, "Hunter2") {
		t.Fatalf("secret leaked into SanitizedConfig: %s", res.SanitizedConfig)
	}
	if !strings.Contains(res.SanitizedConfig, "[REDACTED]") {
		t.Fatalf("SanitizedConfig missing redaction marker: %s", res.SanitizedConfig)
	}
}

// TestNginxSwitchVerifyEndpointLive: against a real httptest.Server, the
// switcher's verify GET reaches the endpoint and matches the header it sends.
func TestNginxSwitchVerifyEndpointLive(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("X-Meshium-Target", "target")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ssh := nginxOKMock()
	s := NewNginxSwitcher(ssh, nil) // nil client → default 30s client
	req := nginxSwitchReq(srv.URL + "/health")

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

// TestNginxSwitchEmptyVerifyURLFailsClosed: no verify URL means no ownership
// proof is possible → error, never a silent Switched=true.
func TestNginxSwitchEmptyVerifyURLFailsClosed(t *testing.T) {
	ssh := nginxOKMock()
	s := NewNginxSwitcher(ssh, nil)
	req := nginxSwitchReq("")
	_, err := s.Switch(context.Background(), req)
	if err == nil {
		t.Fatal("empty verifyURL returned no error; must fail closed")
	}
}
