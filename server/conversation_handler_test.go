package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/auth"
)

func TestConversationHandlers(t *testing.T) {
	store := newMockStore()
	secret := "test-secret"

	// Create a test user and generate a JWT.
	userID := "conv-test-user"
	store.users["user@example.com"] = &auth.User{ID: userID, Email: "user@example.com"}
	token, _, _ := auth.GenerateTokens(userID, secret)

	otherUserID := "other-user"
	store.users["other@example.com"] = &auth.User{ID: otherUserID, Email: "other@example.com"}
	otherToken, _, _ := auth.GenerateTokens(otherUserID, secret)

	s := &Server{
		userStore: store,
		jwtSecret: secret,
	}
	mux := s.Handler()

	// Seed a conversation owned by userID.
	conv := &auth.Conversation{
		ID:         "conv-1",
		UserID:     userID,
		BusinessID: "biz-1",
		Title:      "Test Chat",
	}
	_ = store.CreateConversation(context.Background(), conv)

	// Seed a message.
	_ = store.AddMessage(context.Background(), &auth.Message{
		ID:             "msg-1",
		ConversationID: "conv-1",
		Role:           "user",
		Content:        "Hello",
		Sequence:       0,
	})

	t.Run("List conversations — authenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var convs []*auth.Conversation
		if err := json.NewDecoder(rec.Body).Decode(&convs); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(convs) != 1 {
			t.Errorf("got %d conversations, want 1", len(convs))
		}
	})

	t.Run("List conversations — unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("Get conversation — owner", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var resp struct {
			Conversation *auth.Conversation `json:"conversation"`
			Messages     []*auth.Message    `json:"messages"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Conversation.ID != "conv-1" {
			t.Errorf("conv ID = %q, want %q", resp.Conversation.ID, "conv-1")
		}
		if len(resp.Messages) != 1 {
			t.Errorf("got %d messages, want 1", len(resp.Messages))
		}
	})

	t.Run("Get conversation — wrong user (forbidden)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-1", nil)
		req.Header.Set("Authorization", "Bearer "+otherToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("Get conversation — not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/nonexistent", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("Delete conversation — wrong user (forbidden)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/conv-1", nil)
		req.Header.Set("Authorization", "Bearer "+otherToken)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("Delete conversation — owner", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/conv-1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}

		// Verify it's gone.
		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-1", nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		rec2 := httptest.NewRecorder()
		mux.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusNotFound {
			t.Errorf("after delete: status = %d, want %d", rec2.Code, http.StatusNotFound)
		}
	})
}

func TestConversationHandlers_Errors(t *testing.T) {
	secret := "test-secret"
	userID := "err-user"
	token, _, _ := auth.GenerateTokens(userID, secret)

	t.Run("List error", func(t *testing.T) {
		s := &Server{
			userStore: &mockErrorStore{},
			jwtSecret: secret,
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("Get error", func(t *testing.T) {
		s := &Server{
			userStore: &mockErrorStore{},
			jwtSecret: secret,
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/c1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("Delete error", func(t *testing.T) {
		s := &Server{
			userStore: &mockErrorStore{},
			jwtSecret: secret,
		}
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/c1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})
}

type mockErrorStore struct {
	auth.Store // composition for interface satisfaction
}

var errMock = fmt.Errorf("internal store error")

func (m *mockErrorStore) CreateUser(ctx context.Context, u *auth.User) error { return errMock }
func (m *mockErrorStore) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	return nil, errMock
}
func (m *mockErrorStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	return nil, errMock
}
func (m *mockErrorStore) CreateConversation(ctx context.Context, c *auth.Conversation) error {
	return errMock
}
func (m *mockErrorStore) ListConversations(ctx context.Context, userID string) ([]*auth.Conversation, error) {
	return nil, errMock
}
func (m *mockErrorStore) GetConversation(ctx context.Context, id string) (*auth.Conversation, error) {
	return nil, errMock
}
func (m *mockErrorStore) DeleteConversation(ctx context.Context, id string) error { return errMock }
func (m *mockErrorStore) AddMessage(ctx context.Context, msg *auth.Message) error { return errMock }
func (m *mockErrorStore) GetMessages(ctx context.Context, convID string) ([]*auth.Message, error) {
	return nil, errMock
}
func (m *mockErrorStore) Close() error { return nil }

func TestAuthHandler_LoginNonExistentUser(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
		jwtSecret: "test-secret",
	}

	reqBody, _ := json.Marshal(map[string]string{
		"email":    "nonexistent@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.loginHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthHandler_RegisterSetsUUID(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
		jwtSecret: "test-secret",
	}

	reqBody, _ := json.Marshal(map[string]string{
		"email":    "new-user@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.registerHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Verify the user was created with a non-empty ID.
	user, err := store.GetUserByEmail(context.Background(), "new-user@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if user.ID == "" {
		t.Error("User.ID is empty — UUID was not generated")
	}
}
