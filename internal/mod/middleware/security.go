package middleware

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiPathPrefix      = "/api/"
	authSetupPath      = "/api/auth/setup"
	authUnlockPath     = "/api/auth/unlock"
	apiRequestsPerMin  = 60
	authRequestsPerMin = 5
	requestBodyLimit   = 1_000_000 // 1 MB
)

// Middleware is an HTTP middleware constructor.
type Middleware func(http.Handler) http.Handler

// Chain wraps the given handler with the provided middleware in order.
// The first middleware in the list becomes the outermost wrapper.
func Chain(next http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		if middleware[i] == nil {
			continue
		}
		next = middleware[i](next)
	}
	return next
}

// SecurityHeaders sets security-related response headers for every request.
// scriptHashes are CSP sha256 source expressions (e.g. "'sha256-...'") for the
// inline scripts in the served HTML; they are added to script-src so the
// SvelteKit bootstrap and theme scripts can execute under the policy.
func SecurityHeaders(scriptHashes ...string) Middleware {
	scriptSrc := "'self'"
	if len(scriptHashes) > 0 {
		scriptSrc += " " + strings.Join(scriptHashes, " ")
	}
	csp := "default-src 'self'; script-src " + scriptSrc + "; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:; font-src 'self'; object-src 'none'; base-uri 'self'"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers := w.Header()
			headers.Set("X-Content-Type-Options", "nosniff")
			headers.Set("X-Frame-Options", "DENY")
			headers.Set("X-XSS-Protection", "1; mode=block")
			headers.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			headers.Set("Content-Security-Policy", csp)
			if r.TLS != nil {
				headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit applies sliding-window request limits for API and auth endpoints.
func RateLimit() Middleware {
	limiter := &slidingWindowLimiter{
		window:       time.Minute,
		apiRequests:  make(map[string][]time.Time),
		authRequests: make(map[string][]time.Time),
		apiLimit:     apiRequestsPerMin,
		authLimit:    authRequestsPerMin,
	}
	return limiter.middleware
}

type slidingWindowLimiter struct {
	mu           sync.Mutex
	window       time.Duration
	apiRequests  map[string][]time.Time
	authRequests map[string][]time.Time
	apiLimit     int
	authLimit    int
}

func (l *slidingWindowLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIRequest(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		now := time.Now()

		l.mu.Lock()

		if isAuthRequest(r.URL.Path) {
			if allowed, retryAfter := l.checkBucketLocked(l.authRequests, ip, now, l.authLimit); !allowed {
				l.mu.Unlock()
				writeRateLimitResponse(w, retryAfter)
				return
			}
			if allowed, retryAfter := l.checkBucketLocked(l.apiRequests, ip, now, l.apiLimit); !allowed {
				l.mu.Unlock()
				writeRateLimitResponse(w, retryAfter)
				return
			}

			l.recordLocked(l.authRequests, ip, now)
			l.recordLocked(l.apiRequests, ip, now)
			l.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		if allowed, retryAfter := l.checkBucketLocked(l.apiRequests, ip, now, l.apiLimit); !allowed {
			l.mu.Unlock()
			writeRateLimitResponse(w, retryAfter)
			return
		}

		l.recordLocked(l.apiRequests, ip, now)
		l.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (l *slidingWindowLimiter) checkBucketLocked(bucket map[string][]time.Time, key string, now time.Time, limit int) (bool, time.Duration) {
	timestamps := pruneTimestamps(bucket[key], now, l.window)
	bucket[key] = timestamps
	if len(timestamps) < limit {
		return true, 0
	}

	oldest := timestamps[0]
	retryAfter := l.window - now.Sub(oldest)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return false, retryAfter
}

func (l *slidingWindowLimiter) recordLocked(bucket map[string][]time.Time, key string, now time.Time) {
	bucket[key] = append(bucket[key], now)
}

func pruneTimestamps(timestamps []time.Time, now time.Time, window time.Duration) []time.Time {
	if len(timestamps) == 0 {
		return timestamps
	}

	cutoff := now.Add(-window)
	kept := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	return kept
}

func clientIP(r *http.Request) string {
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil && host != "" {
		return host
	}

	if remoteAddr := strings.TrimSpace(r.RemoteAddr); remoteAddr != "" {
		return remoteAddr
	}

	return "unknown"
}

func isAPIRequest(path string) bool {
	return strings.HasPrefix(path, apiPathPrefix)
}

func isAuthRequest(path string) bool {
	return path == authSetupPath || path == authUnlockPath
}

func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = io.Copy(w, bytes.NewBufferString(`{"error":"too many requests","code":"RATE_LIMITED"}`))
}

// RequestSizeLimit enforces a 1 MiB body limit for API requests.
func RequestSizeLimit() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAPIRequest(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Multipart uploads enforce their own (larger) cap via
			// http.MaxBytesReader in the handler; don't clamp them to 1 MiB.
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				next.ServeHTTP(w, r)
				return
			}

			defer r.Body.Close()

			body, err := io.ReadAll(io.LimitReader(r.Body, requestBodyLimit+1))
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if len(body) > requestBodyLimit {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = io.Copy(w, bytes.NewBufferString(`{"error":"request body too large","code":"REQUEST_TOO_LARGE"}`))
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}
