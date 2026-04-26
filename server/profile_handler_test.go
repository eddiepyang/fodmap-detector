package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fodmap/auth"

	"google.golang.org/genai"
)

func TestProfileHandler_Update(t *testing.T) {
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The mock Gemini server returns a dummy profile in the expected candidate format
		_, _ = io.WriteString(w, `{"candidates": [{"content": {"parts": [{"text": "{\"preferences\": [\"vegan\"]}"}]}}]}`)
	}))
	defer geminiServer.Close()

	store := newMockStore()
	store.users["test@example.com"] = &auth.User{ID: "u1", Email: "test@example.com", Status: "active"}

	s := &Server{
		userStore: store,
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      "test",
		HTTPOptions: genai.HTTPOptions{BaseURL: geminiServer.URL + "/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.genaiClient = client
	s.chatModel = "gemini-2.5-flash"

	reqBody := `{"input": "I am vegan"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(reqBody))

	// Add userContextKey
	ctx := context.WithValue(req.Context(), userContextKey, "u1")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify profile is stored
	profile, err := store.GetDietaryProfile(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if string(profile) != `{"preferences": ["vegan"]}` {
		t.Errorf("unexpected profile: %s", profile)
	}
}

func TestProfileHandler_Update_Unauthorized(t *testing.T) {
	s := &Server{}
	reqBody := `{"input": "I am vegan"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestProfileHandler_Get(t *testing.T) {
	store := newMockStore()
	store.profiles["u1"] = []byte(`{"preferences": ["vegan"]}`)

	s := &Server{
		userStore: store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
	ctx := context.WithValue(req.Context(), userContextKey, "u1")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.getProfileHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	if rec.Body.String() != `{"preferences": ["vegan"]}` {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestProfileHandler_Get_Empty(t *testing.T) {
	store := newMockStore()
	s := &Server{
		userStore: store,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
	ctx := context.WithValue(req.Context(), userContextKey, "u2")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.getProfileHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	if rec.Body.String() != `{}` {
		t.Errorf("expected {}, got %s", rec.Body.String())
	}
}

func TestProfileHandler_Get_Unauthorized(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
	rec := httptest.NewRecorder()

	s.getProfileHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestProfileHandler_Update_EmptyInput(t *testing.T) {
	store := newMockStore()
	store.users["test@example.com"] = &auth.User{ID: "u1", Email: "test@example.com", Status: "active"}

	s := &Server{userStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(`{"input": ""}`))
	ctx := context.WithValue(req.Context(), userContextKey, "u1")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestProfileHandler_Update_InvalidJSON(t *testing.T) {
	store := newMockStore()
	store.users["test@example.com"] = &auth.User{ID: "u1", Email: "test@example.com", Status: "active"}

	s := &Server{userStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(`not json`))
	ctx := context.WithValue(req.Context(), userContextKey, "u1")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestProfileHandler_Update_UserNotFound(t *testing.T) {
	store := newMockStore()
	// No user with ID "u-missing" in the store

	s := &Server{userStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(`{"input": "I am vegan"}`))
	ctx := context.WithValue(req.Context(), userContextKey, "u-missing")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestProfileHandler_Update_ChatServiceNotConfigured(t *testing.T) {
	store := newMockStore()
	store.users["test@example.com"] = &auth.User{ID: "u1", Email: "test@example.com", Status: "active"}

	// genaiClient is nil — simulates missing GOOGLE_API_KEY
	s := &Server{userStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile", strings.NewReader(`{"input": "I am vegan"}`))
	ctx := context.WithValue(req.Context(), userContextKey, "u1")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.updateProfileHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}
