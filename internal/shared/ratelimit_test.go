package shared

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(5, time.Minute)
	for i := 0; i < 5; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiterBlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		rl.Allow("10.0.0.1")
	}
	if rl.Allow("10.0.0.1") {
		t.Error("4th request should be blocked")
	}
	if rl.Allow("10.0.0.1") {
		t.Error("5th request should be blocked")
	}
}

func TestRateLimiterDifferentIPsIndependent(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	// Different IP should still be allowed
	if !rl.Allow("10.0.0.2") {
		t.Error("different IP should be allowed")
	}
	if !rl.Allow("10.0.0.2") {
		t.Error("different IP second request should be allowed")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := NewRateLimiter(2, 50*time.Millisecond)
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	if rl.Allow("10.0.0.1") {
		t.Error("3rd request should be blocked")
	}

	// Wait for window to reset
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("10.0.0.1") {
		t.Error("request after window reset should be allowed")
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(1, 50*time.Millisecond)
	rl.Allow("10.0.0.1")
	time.Sleep(60 * time.Millisecond)
	rl.Cleanup()

	rl.mu.Lock()
	_, exists := rl.visitors["10.0.0.1"]
	rl.mu.Unlock()
	if exists {
		t.Error("expired entry should be cleaned up")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	handlerCalled := 0
	handler := RateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests should pass
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if handlerCalled != 2 {
		t.Errorf("handler should have been called 2 times, got %d", handlerCalled)
	}
}

func TestRateLimitMiddlewareDifferentIPs(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	handler := RateLimitMiddleware(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First IP should pass
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first IP: expected 200, got %d", w.Code)
	}

	// Second IP should also pass
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.200:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("second IP: expected 200, got %d", w.Code)
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"direct connection", "192.168.1.1:12345", "", "192.168.1.1"},
		{"with X-Forwarded-For", "10.0.0.1:12345", "203.0.113.1", "203.0.113.1"},
		{"XFF with multiple IPs", "10.0.0.1:12345", "203.0.113.1, 10.0.0.2", "203.0.113.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			got := ExtractIP(req)
			if got != tt.want {
				t.Errorf("extractIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
