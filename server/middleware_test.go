package server

import (
	"fodmap/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userID, ok := r.Context().Value(userContextKey).(string); ok {
			w.Header().Set("X-User-ID", userID)
		}
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

// ---- jwtAuth ----

func TestJwtAuth_ValidToken(t *testing.T) {
	secret := "jwt-secret"
	userID := "user-123"
	token, _, _ := auth.GenerateTokens(userID, secret)

	h := jwtAuth(secret)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("X-User-ID"); got != userID {
		t.Errorf("X-User-ID = %q, want %q", got, userID)
	}
}

func TestJwtAuth_InvalidToken(t *testing.T) {
	h := jwtAuth("jwt-secret")(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---- combinedAuth ----

func TestCombinedAuth(t *testing.T) {
	jwtSecret := "jwt-secret"
	bearerToken := "bearer-token"
	userID := "user-123"
	jwtToken, _, _ := auth.GenerateTokens(userID, jwtSecret)

	h := combinedAuth(jwtSecret, bearerToken)(okHandler())

	t.Run("Valid JWT", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("X-User-ID"); got != userID {
			t.Errorf("X-User-ID = %q, want %q", got, userID)
		}
	})

	t.Run("Valid Bearer", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer "+bearerToken)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		// Bearer auth doesn't set user ID in context in this implementation
		if got := rec.Header().Get("X-User-ID"); got != "" {
			t.Errorf("X-User-ID = %q, want empty", got)
		}
	})

	t.Run("Invalid Both", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
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

func TestRateLimiter_SetsHeaders(t *testing.T) {
	rl := newIPRateLimiter(10, 10)
	h := rateLimitMiddleware(rl)(okHandler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want %q", got, "10")
	}
	got := rec.Header().Get("X-RateLimit-Remaining")
	if got == "" {
		t.Error("X-RateLimit-Remaining is empty, expected a value")
	}
}

func TestRateLimiter_SetsRetryAfterOn429(t *testing.T) {
	rl := newIPRateLimiter(0.001, 1) // very low rate, burst of 1
	h := rateLimitMiddleware(rl)(okHandler())

	// First request succeeds.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec.Code)
	}

	// Second request should be rate-limited.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/chat/pizza", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header is empty on 429, expected a value")
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

// ---- corsMiddleware ----

func TestCorsMiddleware_AllowedOrigin(t *testing.T) {
	allowedOrigins := []string{"http://localhost:3000"}
	h := corsMiddleware(allowedOrigins)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
}

func TestCorsMiddleware_DisallowedOrigin(t *testing.T) {
	allowedOrigins := []string{"http://localhost:3000"}
	h := corsMiddleware(allowedOrigins)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://disallowed.com")

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty string", got)
	}
}

func TestCorsMiddleware_Preflight(t *testing.T) {
	allowedOrigins := []string{"http://localhost:3000"}
	h := corsMiddleware(allowedOrigins)(okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "POST, GET, OPTIONS, PUT, DELETE" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "POST, GET, OPTIONS, PUT, DELETE")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization" {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
	}
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
