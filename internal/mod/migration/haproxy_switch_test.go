package migration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// haproxySwitchReq builds a canonical HAProxy switch request. The NewConfig
// embeds a secret to prove the sanitizer redacts it.
func haproxySwitchReq(verifyURL string) TrafficSwitchRequest {
	return TrafficSwitchRequest{
		IdempotencyKey: "mig1-cut-1",
		ConfigPath:     "/etc/haproxy/haproxy.cfg",
		NewConfig:      "server target 10.0.0.2:80 check # password=Hunter2",
		VerifyURL:      verifyURL,
		VerifyHeader:   "X-Meshium-Target",
		VerifyValue:    "target",
	}
}

// haproxyOKMock accepts `haproxy -c -f ...` and `systemctl reload haproxy`.
func haproxyOKMock() *mockSSH {
	m := newMockSSH()
	m.execOutput["haproxy -c -f '/etc/haproxy/haproxy.cfg' 2>&1"] = "Configuration file is valid"
	m.execOutput["systemctl reload haproxy 2>&1 || systemctl restart haproxy 2>&1"] = ""
	return m
}

// TestHAProxySwitchSuccess: happy path — upload, haproxy -c ok, reload ok,
// verify header matches → Switched+Verified true, nil error.
func TestHAProxySwitchSuccess(t *testing.T) {
	ssh := haproxyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), haproxySwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if !res.Switched || !res.Verified || !res.ConfigTestOK || !res.ReloadOK {
		t.Fatalf("flags: %+v", res)
	}
	if _, ok := ssh.uploadData["/etc/haproxy/haproxy.cfg"]; !ok {
		t.Fatal("new config not uploaded")
	}
}

// TestHAProxySwitchIdempotentReentry: same IdempotencyKey → cached result, no
// new SSH commands.
func TestHAProxySwitchIdempotentReentry(t *testing.T) {
	ssh := haproxyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))
	req := haproxySwitchReq("http://verify/health")

	if _, err := s.Switch(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	callsBefore := len(ssh.commands)
	rtCallsBefore := atomic.LoadInt32(&rt.calls)

	res, err := s.Switch(context.Background(), req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !res.Switched || !res.Verified {
		t.Fatalf("cached flags lost: %+v", res)
	}
	if len(ssh.commands) != callsBefore {
		t.Fatalf("idempotent reentry issued SSH: before=%d after=%d", callsBefore, len(ssh.commands))
	}
	if atomic.LoadInt32(&rt.calls) != rtCallsBefore {
		t.Fatal("idempotent reentry hit verify endpoint")
	}
}

// TestHAProxySwitchConfigTestFailFailsClosed: `haproxy -c` rejects →
// ErrHAProxyConfigTestFailed, Switched false.
func TestHAProxySwitchConfigTestFailFailsClosed(t *testing.T) {
	ssh := haproxyOKMock()
	ssh.execExit["haproxy -c -f '/etc/haproxy/haproxy.cfg' 2>&1"] = 1
	ssh.execOutput["haproxy -c -f '/etc/haproxy/haproxy.cfg' 2>&1"] = "haproxy: [ALERT] config error"
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), haproxySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrHAProxyConfigTestFailed) {
		t.Fatalf("config-test fail: err=%v want ErrHAProxyConfigTestFailed", err)
	}
	if res.Switched || res.ConfigTestOK {
		t.Fatalf("flags: %+v", res)
	}
	if res.ReloadOK {
		t.Fatal("reload ok after config-test failure")
	}
}

// TestHAProxySwitchReloadFailFailsClosed: reload fails → ErrHAProxyReloadFailed,
// Switched false.
func TestHAProxySwitchReloadFailFailsClosed(t *testing.T) {
	ssh := haproxyOKMock()
	ssh.execExit["systemctl reload haproxy 2>&1 || systemctl restart haproxy 2>&1"] = 1
	ssh.execOutput["systemctl reload haproxy 2>&1 || systemctl restart haproxy 2>&1"] = "failed"
	HAProxyReloadRetries = 1
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), haproxySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrHAProxyReloadFailed) {
		t.Fatalf("reload fail: err=%v want ErrHAProxyReloadFailed", err)
	}
	if res.Switched || res.ReloadOK {
		t.Fatalf("flags: %+v", res)
	}
}

// TestHAProxySwitchVerifyMismatchFailsClosed: response lacks target marker →
// ErrTrafficVerifyFailed (shared, not HAProxy-specific). Traffic did not move.
func TestHAProxySwitchVerifyMismatchFailsClosed(t *testing.T) {
	ssh := haproxyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"source"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))

	res, err := s.Switch(context.Background(), haproxySwitchReq("http://verify/health"))
	if !errors.Is(err, ErrTrafficVerifyFailed) {
		t.Fatalf("verify mismatch: err=%v want ErrTrafficVerifyFailed", err)
	}
	if res.Switched {
		t.Fatal("marked switched on verify failure")
	}
	if !res.ReloadOK {
		t.Fatal("reload ok but verify failed — reload flag must be set")
	}
}

// TestHAProxySwitchSanitizesSecret: the persisted result redacts the password.
func TestHAProxySwitchSanitizesSecret(t *testing.T) {
	ssh := haproxyOKMock()
	rt := &stubTransport{status: 200, header: http.Header{"X-Meshium-Target": []string{"target"}}}
	s := NewHAProxySwitcher(ssh, newClient(rt))
	res, err := s.Switch(context.Background(), haproxySwitchReq("http://verify/health"))
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if strings.Contains(res.SanitizedResult, "Hunter2") || strings.Contains(res.SanitizedConfig, "Hunter2") {
		t.Fatalf("secret leaked: result=%s config=%s", res.SanitizedResult, res.SanitizedConfig)
	}
	if !strings.Contains(res.SanitizedConfig, "[REDACTED]") {
		t.Fatalf("SanitizedConfig missing redaction: %s", res.SanitizedConfig)
	}
}

// TestHAProxySwitchEmptyVerifyURLFailsClosed: no ownership proof possible.
func TestHAProxySwitchEmptyVerifyURLFailsClosed(t *testing.T) {
	ssh := haproxyOKMock()
	s := NewHAProxySwitcher(ssh, nil)
	req := haproxySwitchReq("")
	_, err := s.Switch(context.Background(), req)
	if err == nil {
		t.Fatal("empty verifyURL returned no error; must fail closed")
	}
}
