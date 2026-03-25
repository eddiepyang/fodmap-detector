package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// bearerAuth returns middleware that validates a Bearer token in the
// Authorization header. Returns 401 if the token is missing or wrong.
func bearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
				return
			}
			got := auth[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimiter tracks per-IP rate limiters.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rate     rate.Limit
	burst    int
}

func newIPRateLimiter(r rate.Limit, burst int) *ipRateLimiter {
	return &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     r,
		burst:    burst,
	}
}

func (rl *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if l, ok := rl.limiters[ip]; ok {
		return l
	}
	l := rate.NewLimiter(rl.rate, rl.burst)
	rl.limiters[ip] = l
	return l
}

// rateLimitMiddleware returns middleware that enforces per-IP rate limits.
func rateLimitMiddleware(rl *ipRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			// Use X-Forwarded-For if present (first entry).
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				ip = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			}
			limiter := rl.getLimiter(ip)
			if !limiter.Allow() {
				slog.Warn("rate limit exceeded", "ip", ip)
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// concurrencyLimiter returns middleware that bounds simultaneous requests using
// a buffered channel as a semaphore. Returns 503 when all slots are occupied.
func concurrencyLimiter(maxConcurrent int) func(http.Handler) http.Handler {
	sem := make(chan struct{}, maxConcurrent)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				http.Error(w, `{"error":"server busy, try again later"}`, http.StatusServiceUnavailable)
			}
		})
	}
}

// chain applies middleware in order: first listed wraps outermost.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
