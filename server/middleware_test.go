package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// ---- bearerAuth ----

func TestBearerAuth_ValidToken(t *testing.T) {
	h := bearerAuth("secret-token")(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	req.Header.Set("Authorization", "Bearer secret-token")

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestBearerAuth_MissingHeader(t *testing.T) {
	h := bearerAuth("secret-token")(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_WrongToken(t *testing.T) {
	h := bearerAuth("secret-token")(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_NoBearerPrefix(t *testing.T) {
	h := bearerAuth("secret-token")(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	req.Header.Set("Authorization", "Basic abc123")

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---- rateLimitMiddleware ----

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := newIPRateLimiter(10, 10)
	h := rateLimitMiddleware(rl)(okHandler())

	for range 5 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}
}

func TestRateLimiter_BlocksExcess(t *testing.T) {
	// Burst of 2, so third request should be blocked.
	rl := newIPRateLimiter(0.001, 2) // very low rate to ensure no refill
	h := rateLimitMiddleware(rl)(okHandler())

	for i := range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
		h.ServeHTTP(rec, req)
		if i < 2 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rec.Code)
		}
		if i == 2 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("request %d: expected 429, got %d", i, rec.Code)
		}
	}
}

// ---- concurrencyLimiter ----

func TestConcurrencyLimiter_RejectsWhenFull(t *testing.T) {
	// maxConcurrent=1, block first request so second gets 503.
	started := make(chan struct{})
	release := make(chan struct{})
	blocking := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := concurrencyLimiter(1)(blocking)

	// Start first request in background.
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	}()
	<-started // first request is holding the slot

	// Second request should get 503.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	close(release)
}

// ---- chain ----

func TestChain_AppliesInOrder(t *testing.T) {
	// Auth should run before rate limiter — unauthenticated requests don't consume rate limit tokens.
	rl := newIPRateLimiter(0.001, 1) // burst of 1
	h := chain(okHandler(), bearerAuth("tok"), rateLimitMiddleware(rl))

	// Unauthenticated request → 401, should NOT consume a rate limit token.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// Authenticated request should still succeed (burst not consumed by the 401).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
