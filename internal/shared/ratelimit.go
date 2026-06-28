package shared

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter provides per-IP rate limiting for HTTP endpoints.
// It uses a sliding window approach with a fixed number of requests per window.
type RateLimiter struct {
	mu      sync.Mutex
	visitors map[string]*visitorInfo
	maxRequests int
	window      time.Duration

	stopCh chan struct{} // closed by Stop to signal the cleanup goroutine to exit
}

type visitorInfo struct {
	count     int
	windowStart time.Time
	blockedUntil time.Time
}

// NewRateLimiter creates a rate limiter that allows maxRequests per window per IP.
// After exceeding the limit, the IP is blocked for the blockDuration.
func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		visitors:    make(map[string]*visitorInfo),
		maxRequests: maxRequests,
		window:      window,
	}
}

// Allow checks if the request from the given IP should be allowed.
// Returns true if allowed, false if rate limited.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, exists := rl.visitors[ip]
	if !exists {
		v = &visitorInfo{windowStart: now}
		rl.visitors[ip] = v
	}

	// Check if currently blocked
	if now.Before(v.blockedUntil) {
		return false
	}

	// Reset window if expired
	if now.Sub(v.windowStart) > rl.window {
		v.count = 0
		v.windowStart = now
	}

	v.count++

	// If exceeded, block for the window duration
	if v.count > rl.maxRequests {
		v.blockedUntil = now.Add(rl.window)
		return false
	}

	return true
}

// Cleanup removes expired entries to prevent memory growth.
// Should be called periodically (e.g., via a goroutine).
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, v := range rl.visitors {
		if now.Sub(v.windowStart) > rl.window && now.After(v.blockedUntil) {
			delete(rl.visitors, ip)
		}
	}
}

// StartCleanup starts a background goroutine that periodically cleans up
// expired entries. Call Stop to terminate the goroutine. It is safe to
// call StartCleanup only once per RateLimiter instance.
func (rl *RateLimiter) StartCleanup(interval time.Duration) {
	rl.stopCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-rl.stopCh:
				return
			case <-ticker.C:
				rl.Cleanup()
			}
		}
	}()
}

// Stop terminates the cleanup goroutine started by StartCleanup.
// It is safe to call multiple times or when StartCleanup was not called.
func (rl *RateLimiter) Stop() {
	if rl.stopCh != nil {
		select {
		case <-rl.stopCh:
			// already closed
		default:
			close(rl.stopCh)
		}
	}
}

// RateLimitMiddleware returns HTTP middleware that rate limits requests by IP.
// If the rate limit is exceeded, it returns 429 Too Many Requests.
func RateLimitMiddleware(limiter *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ExtractIP(r)
		if !limiter.Allow(ip) {
			WriteError(w, http.StatusTooManyRequests, "rate limit exceeded — too many requests", "RATE_LIMITED")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ExtractIP extracts the client IP from the request, handling X-Forwarded-For.
func ExtractIP(r *http.Request) string {
	// Check X-Forwarded-For header (for reverse proxy setups)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (original client)
		ips := net.ParseIP(xff)
		if ips != nil {
			return ips.String()
		}
		// Handle comma-separated list
		for _, ip := range splitCSV(xff) {
			parsed := net.ParseIP(ip)
			if parsed != nil {
				return parsed.String()
			}
		}
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// splitCSV splits a comma-separated string and trims whitespace from each element.
func splitCSV(s string) []string {
	var result []string
	start := 0
	for i, c := range s {
		if c == ',' {
			result = append(result, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	result = append(result, trimSpace(s[start:]))
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
