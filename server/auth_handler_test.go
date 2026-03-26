package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/auth"
)

// TestAuthHandlers uses the centralized mockUserStore from mock_store.go
func TestAuthHandlers(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
		jwtSecret: "test-secret",
	}

	t.Run("Register success", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"email":    "test@example.com",
			"password": "password123",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.registerHandler(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Register duplicate email", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"email":    "test@example.com",
			"password": "password123",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.registerHandler(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("Login success", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"email":    "test@example.com",
			"password": "password123",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.loginHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp["access_token"] == "" || resp["refresh_token"] == "" {
			t.Errorf("missing tokens in response: %v", resp)
		}
	})

	t.Run("Login failure wrong password", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"email":    "test@example.com",
			"password": "wrongpassword",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.loginHandler(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("Refresh success", func(t *testing.T) {
		// First get a refresh token
		_, refreshToken, _ := auth.GenerateTokens("user-123", "test-secret")
		
		// Setup store for refresh (normally it would check DB for user existence)
		store.users["refresh@example.com"] = &auth.User{ID: "user-123", Email: "refresh@example.com"}

		reqBody, _ := json.Marshal(map[string]string{
			"refresh_token": refreshToken,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.refreshHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
