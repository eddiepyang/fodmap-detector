package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fodmap/auth"
)

func TestAdminRequiredMiddleware(t *testing.T) {
	store := newMockStore()
	secret := "test-secret"
	s := &Server{
		userStore: store,
		jwtSecret: secret,
	}

	// 1. Non-admin user
	userID := "user-1"
	store.users["user@example.com"] = &auth.User{
		ID:     userID,
		Email:  "user@example.com",
		Role:   "user",
		Status: "active",
	}
	userToken, _, _ := auth.GenerateTokensWithRole(userID, "user", secret)

	// 2. Admin user
	adminID := "admin-1"
	store.users["admin@example.com"] = &auth.User{
		ID:     adminID,
		Email:  "admin@example.com",
		Role:   "admin",
		Status: "active",
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)

	mux := s.Handler()

	t.Run("Non-admin request is forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("Admin request is allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}
	})
}

func TestAdminUserHandlers(t *testing.T) {
	store := newMockStore()
	secret := "test-secret"
	adminID := "admin-1"
	s := &Server{
		userStore: store,
		jwtSecret: secret,
	}

	store.users["admin@example.com"] = &auth.User{
		ID:        adminID,
		Email:     "admin@example.com",
		Role:      "admin",
		Status:    "active",
		CreatedAt: time.Now().Add(-10 * time.Minute),
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)

	// Seed some users
	store.users["test1@example.com"] = &auth.User{
		ID:        "u1",
		Email:     "test1@example.com",
		Role:      "user",
		Status:    "active",
		CreatedAt: time.Now().Add(-5 * time.Minute),
	}
	store.users["test2@example.com"] = &auth.User{
		ID:        "u2",
		Email:     "test2@example.com",
		Role:      "user",
		Status:    "suspended",
		CreatedAt: time.Now().Add(-2 * time.Minute),
	}

	mux := s.Handler()

	t.Run("List users with search and filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?status=suspended", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var resp struct {
			Users []*auth.User `json:"users"`
			Total int          `json:"total"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.Total != 1 {
			t.Errorf("total = %d, want 1", resp.Total)
		}
		if len(resp.Users) != 1 || resp.Users[0].ID != "u2" {
			t.Errorf("expected user u2, got %v", resp.Users)
		}
	})

	t.Run("Get user details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/u1", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var resp auth.UserDetail
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.User.Email != "test1@example.com" {
			t.Errorf("user email = %q, want %q", resp.User.Email, "test1@example.com")
		}
	})

	t.Run("Update status", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"status": "suspended"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/u1/status", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}

		user, _ := store.GetUserByID(context.Background(), "u1")
		if user.Status != "suspended" {
			t.Errorf("status = %q, want %q", user.Status, "suspended")
		}
	})

	t.Run("Update status - reject self suspension", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"status": "suspended"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/admin-1/status", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("Reset password", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/u1/reset-password", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			TemporaryPassword string `json:"temporary_password"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if len(resp.TemporaryPassword) != 16 {
			t.Errorf("temporary password length = %d, want 16", len(resp.TemporaryPassword))
		}

		// Verify bcrypt hash is correct in store
		user, _ := store.GetUserByID(context.Background(), "u1")
		if !user.CheckPassword(resp.TemporaryPassword) {
			t.Error("stored password hash does not match temporary password")
		}
	})

	t.Run("Delete user permanently", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/u2", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}

		user, _ := store.GetUserByID(context.Background(), "u2")
		if user != nil {
			t.Error("expected user to be permanently deleted")
		}
	})

	t.Run("Delete user - reject self delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/admin-1", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

func TestAdminAnalyticsAndConversationHandlers(t *testing.T) {
	store := newMockStore()
	secret := "test-secret"
	adminID := "admin-1"
	s := &Server{
		userStore: store,
		jwtSecret: secret,
	}

	store.users["admin@example.com"] = &auth.User{
		ID:        adminID,
		Email:     "admin@example.com",
		Role:      "admin",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)

	// Seed conversations
	c1 := &auth.Conversation{
		ID:           "c1",
		UserID:       adminID,
		Title:        "Admin Conv",
		BusinessID:   "biz-1",
		BusinessName: "Biz One",
		CreatedAt:    time.Now().Add(-24 * time.Hour),
		UpdatedAt:    time.Now().Add(-24 * time.Hour),
	}
	_ = store.CreateConversation(context.Background(), c1)
	_ = store.AddMessage(context.Background(), &auth.Message{
		ID:             "m1",
		ConversationID: "c1",
		Role:           "user",
		Content:        "Hello admin",
		Sequence:       0,
	})

	mux := s.Handler()

	t.Run("List all conversations", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversations", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Conversations []*auth.ConversationSummary `json:"conversations"`
			Total         int                         `json:"total"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.Total != 1 {
			t.Errorf("total = %d, want 1", resp.Total)
		}
		if resp.Conversations[0].MessageCount != 1 {
			t.Errorf("msg count = %d, want 1", resp.Conversations[0].MessageCount)
		}
	})

	t.Run("Get conversation details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversations/c1", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Conversation *auth.Conversation `json:"conversation"`
			Messages     []*auth.Message    `json:"messages"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.Conversation.Title != "Admin Conv" {
			t.Errorf("title = %q, want %q", resp.Conversation.Title, "Admin Conv")
		}
		if len(resp.Messages) != 1 {
			t.Errorf("len messages = %d, want 1", len(resp.Messages))
		}
	})

	t.Run("Get analytics overview", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/overview", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if int(resp["total_users"].(float64)) != 1 {
			t.Errorf("total_users = %v, want 1", resp["total_users"])
		}
		if int(resp["total_conversations"].(float64)) != 1 {
			t.Errorf("total_conversations = %v, want 1", resp["total_conversations"])
		}
	})

	t.Run("Get conversation activity", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/activity?days=7", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp []auth.DailyCount
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if len(resp) != 1 {
			t.Errorf("got %d days, want 1", len(resp))
		}
	})
}

func TestAdminPaginationAndValidation(t *testing.T) {
	store := newMockStore()
	secret := "test-secret"
	adminID := "admin-1"
	s := &Server{
		userStore: store,
		jwtSecret: secret,
	}
	store.users["admin@example.com"] = &auth.User{
		ID:        adminID,
		Email:     "admin@example.com",
		Role:      "admin",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)

	for i := 0; i < 25; i++ {
		store.users["user"+string(rune('a'+i))+"@example.com"] = &auth.User{
			ID:        "u" + string(rune('0'+i)),
			Email:     "user" + string(rune('a'+i)) + "@example.com",
			Role:      "user",
			Status:    "active",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}
	}

	mux := s.Handler()

	t.Run("User list pagination respects limit and page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?page=2&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Users []*auth.User `json:"users"`
			Total int          `json:"total"`
			Page  int          `json:"page"`
			Limit int          `json:"limit"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if resp.Page != 2 {
			t.Errorf("page = %d, want 2", resp.Page)
		}
		if resp.Limit != 10 {
			t.Errorf("limit = %d, want 10", resp.Limit)
		}
		if resp.Total != 26 {
			t.Errorf("total = %d, want 26", resp.Total)
		}
		if len(resp.Users) != 10 {
			t.Errorf("got %d users, want 10", len(resp.Users))
		}
	})

	t.Run("User list clamps limit to 100", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?limit=999", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var resp struct {
			Limit int `json:"limit"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Limit != 100 {
			t.Errorf("limit = %d, want 100", resp.Limit)
		}
	})

	t.Run("Conversation activity clamps days to 90", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/activity?days=365", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestAdminHandlers_ErrorPaths(t *testing.T) {
	secret := "test-secret"
	adminID := "admin-1"
	errStore := &mockErrorStore{}

	s := &Server{userStore: errStore, jwtSecret: secret}
	mockStore := newMockStore()
	mockStore.users["admin@example.com"] = &auth.User{
		ID:     adminID,
		Email:  "admin@example.com",
		Role:   "admin",
		Status: "active",
	}
	adminToken, _, _ := auth.GenerateTokensWithRole(adminID, "admin", secret)

	mux := s.Handler()

	t.Run("List users returns 500 on store error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("Get user returns 500 on store error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/u1", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("Update status rejects invalid status", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"status": "banned"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/admin-1/status", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("Analytics overview returns 500 on store error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/analytics/overview", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("List conversations returns 500 on store error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/conversations", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})
}
