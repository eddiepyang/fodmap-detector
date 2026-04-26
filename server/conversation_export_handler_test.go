package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fodmap/auth"
)

func TestExportConversationHandler_JSON(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:           "conv-export-1",
		UserID:       "user-1",
		BusinessID:   "biz-1",
		BusinessName: "Test Restaurant",
		Title:        "Chat about Test Restaurant",
		SearchCity:   "Las Vegas",
		SearchState:  "NV",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	msgs := []*auth.Message{
		{ID: "msg-1", ConversationID: conv.ID, Role: "user", Content: "Is the pasta low FODMAP?", Sequence: 1, CreatedAt: time.Now()},
		{ID: "msg-2", ConversationID: conv.ID, Role: "model", Content: "Pasta contains wheat which is high in fructans.", Sequence: 2, CreatedAt: time.Now()},
	}
	for _, m := range msgs {
		if err := store.AddMessage(t.Context(), m); err != nil {
			t.Fatalf("add message: %v", err)
		}
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-export-1/export?format=json", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Conversation *auth.Conversation `json:"conversation"`
		Messages     []*auth.Message    `json:"messages"`
		ExportedAt   time.Time          `json:"exported_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Conversation.ID != "conv-export-1" {
		t.Errorf("conversation ID = %q, want %q", body.Conversation.ID, "conv-export-1")
	}
	if len(body.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(body.Messages))
	}
	if body.ExportedAt.IsZero() {
		t.Error("exported_at is zero")
	}
}

func TestExportConversationHandler_Markdown(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:           "conv-export-md",
		UserID:       "user-1",
		BusinessID:   "biz-1",
		BusinessName: "Pasta Place",
		Title:        "Chat about Pasta Place",
		SearchCity:   "Reno",
		SearchState:  "NV",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	msgs := []*auth.Message{
		{ID: "msg-md-1", ConversationID: conv.ID, Role: "user", Content: "What about the risotto?", Sequence: 1, CreatedAt: time.Now()},
		{ID: "msg-md-2", ConversationID: conv.ID, Role: "model", Content: "Risotto contains rice which is low FODMAP.", Sequence: 2, CreatedAt: time.Now()},
	}
	for _, m := range msgs {
		if err := store.AddMessage(t.Context(), m); err != nil {
			t.Fatalf("add message: %v", err)
		}
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-export-md/export?format=markdown", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "# Chat about Pasta Place") {
		t.Error("markdown missing title heading")
	}
	if !strings.Contains(body, "👤 User") {
		t.Error("markdown missing user heading")
	}
	if !strings.Contains(body, "🤖 Assistant") {
		t.Error("markdown missing assistant heading")
	}
	if !strings.Contains(body, "What about the risotto?") {
		t.Error("markdown missing user message content")
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("content-type = %q, want text/markdown", ct)
	}
}

func TestExportConversationHandler_UnsupportedFormat(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:        "conv-export-err",
		UserID:    "user-1",
		Title:     "Test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-export-err/export?format=csv", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestExportConversationHandler_ForbiddenUser(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:        "conv-export-forbid",
		UserID:    "user-1",
		Title:     "Test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	// Different user
	token, _, _ := auth.GenerateTokens("user-2", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-export-forbid/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestExportConversationHandler_NotFound(t *testing.T) {
	store := newMockStore()
	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/nonexistent/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user", "User"},
		{"model", "Model"},
		{"", ""},
		{"a", "A"},
		{"hello world", "Hello world"},
	}
	for _, tt := range tests {
		got := capitalize(tt.input)
		if got != tt.want {
			t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExportConversationHandler_DefaultFormatIsJSON(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:           "conv-default-fmt",
		UserID:       "user-1",
		BusinessName: "Default Format Cafe",
		Title:        "Chat about Default Format Cafe",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	// No format parameter — should default to JSON
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-default-fmt/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestExportConversationHandler_MarkdownAlias(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:           "conv-md-alias",
		UserID:       "user-1",
		BusinessName: "Alias Cafe",
		Title:        "Chat about Alias Cafe",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	// "md" is an alias for "markdown"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-md-alias/export?format=md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("content-type = %q, want text/markdown", ct)
	}
}

func TestExportConversationHandler_MarkdownWithToolCalls(t *testing.T) {
	store := newMockStore()
	conv := &auth.Conversation{
		ID:           "conv-toolcall",
		UserID:       "user-1",
		BusinessName: "Tool Call Diner",
		Title:        "Chat about Tool Call Diner",
		SearchCity:   "NYC",
		SearchState:  "NY",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	msgs := []*auth.Message{
		{ID: "msg-tc-1", ConversationID: conv.ID, Role: "user", Content: "Is garlic low FODMAP?", Sequence: 1, CreatedAt: time.Now()},
		{ID: "msg-tc-2", ConversationID: conv.ID, Role: "tool_call", Content: `{"name": "lookup_fodmap", "args": {"ingredient": "garlic"}}`, Sequence: 2, CreatedAt: time.Now()},
		{ID: "msg-tc-3", ConversationID: conv.ID, Role: "tool_response", Content: `{"level": "high", "groups": ["fructans"]}`, Sequence: 3, CreatedAt: time.Now()},
		{ID: "msg-tc-4", ConversationID: conv.ID, Role: "model", Content: "Garlic is high FODMAP due to fructans.", Sequence: 4, CreatedAt: time.Now()},
	}
	for _, m := range msgs {
		if err := store.AddMessage(t.Context(), m); err != nil {
			t.Fatalf("add message: %v", err)
		}
	}

	s := &Server{userStore: store, jwtSecret: "test-secret"}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/conversations/{id}/export", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.exportConversationHandler)))

	token, _, _ := auth.GenerateTokens("user-1", "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/conv-toolcall/export?format=markdown", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "🔧 Tool Call") {
		t.Error("markdown missing tool call heading")
	}
	if !strings.Contains(body, "📋 Tool Response") {
		t.Error("markdown missing tool response heading")
	}
	if !strings.Contains(body, "```json") {
		t.Error("markdown missing json code block for tool content")
	}
}
