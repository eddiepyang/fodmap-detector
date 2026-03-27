package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fodmap/auth"
	"fodmap/chat"
	"fodmap/data"
	"fodmap/data/schemas"
	"fodmap/search"

	"google.golang.org/genai"
)

func TestChatHandler_Streaming(t *testing.T) {
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Answer with exactly") {
			// Topic filter (GenerateContent)
			_, _ = io.WriteString(w, `{"candidates": [{"content": {"parts": [{"text": "yes"}]}}]}`)
		} else {
			// Actual chat (GenerateContentStream) expects SSE format
			_, _ = io.WriteString(w, "data: " + `{"candidates": [{"content": {"parts": [{"text": "Hello"}]}}]}` + "\n\n")
		}
	}))
	defer geminiServer.Close()

	// Setup mock searcher
	mockSearcher := &chatMockSearcher{
		FodmapResult: &search.FodmapResult{
			Ingredient: "garlic",
			Level:      "High",
		},
	}

	// Setup mock server for our application
	s := NewServer(mockSearcher, 8081)
	s.userStore = newMockStore()
	s.chatAPIKey = "test-key"
	s.geminiApiKey = "test-key"
	s.chatRateLimiter = newIPRateLimiter(100, 100)
	s.chatMaxConcurrent = 10
	
	// Create the mock client
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey: "test-key",
		HTTPOptions: genai.HTTPOptions{
			BaseURL: geminiServer.URL + "/",
		},
	})
	if err != nil {
		t.Fatalf("failed to create mock genai client: %v", err)
	}
	s.genaiClient = client
	
	// geminiFactory is used to check if chat is enabled
	s.geminiFactory = func(ctx context.Context, prompt string) (*genai.Client, *genai.Chat, error) {
		return client, nil, nil
	}


	appServer := httptest.NewServer(s.Handler())
	defer appServer.Close()

	// Perform request with streaming enabled
	reqBody := `{"message": "hello"}`
	req, err := http.NewRequest("POST", appServer.URL+"/chat/test?stream=true", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Assertions
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(body))
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("expected content type text/event-stream, got %s", contentType)
	}

	scanner := bufio.NewScanner(resp.Body)
	var foundChunk, foundDone bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatalf("failed to unmarshal event: %v", err)
			}
			if event["type"] == "chunk" {
				foundChunk = true
			}
			if event["type"] == "done" {
				foundDone = true
			}
		}
	}

	if !foundChunk {
		t.Error("expected to find at least one 'chunk' event")
	}
	if !foundDone {
		t.Error("expected to find a 'done' event")
	}

	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

// chatMockSearcher is a stub Searcher for tests.
type chatMockSearcher struct {
	FodmapResult *search.FodmapResult
}

func (m *chatMockSearcher) GetBusinesses(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchResult, error) {
	return search.SearchResult{Businesses: []search.BusinessResult{{ID: "1", Name: "Test Biz"}}}, nil
}

func (m *chatMockSearcher) GetReviews(ctx context.Context, query string, limit int, filter search.SearchFilter) (search.SearchReviews, error) {
	return search.SearchReviews{
		BusinessReviews: []search.RankedReview{
			{
				Score: 5,
				Review: search.IndexItem{
					Review: schemas.Review{BusinessID: "1", Text: "Good"},
				},
			},
		},
	}, nil
}

func (m *chatMockSearcher) SearchFodmap(ctx context.Context, ingredient string) (search.FodmapResult, float64, error) {
	if m.FodmapResult != nil {
		return *m.FodmapResult, 1.0, nil
	}
	return search.FodmapResult{}, 0, nil
}

func (m *chatMockSearcher) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	return nil
}

// ---- messagesToContent ----

func TestMessagesToContent_TextMessages(t *testing.T) {
	msgs := []*auth.Message{
		{ID: "1", Role: "user", Content: "Hello"},
		{ID: "2", Role: "model", Content: "Hi there"},
	}
	result := messagesToContent(msgs)
	if len(result) != 2 {
		t.Fatalf("got %d contents, want 2", len(result))
	}
	if result[0].Role != "user" || result[0].Parts[0].Text != "Hello" {
		t.Errorf("first content = %+v", result[0])
	}
	if result[1].Role != "model" || result[1].Parts[0].Text != "Hi there" {
		t.Errorf("second content = %+v", result[1])
	}
}

func TestMessagesToContent_SkipsToolMessages(t *testing.T) {
	msgs := []*auth.Message{
		{ID: "1", Role: "user", Content: "What about garlic?"},
		{ID: "2", Role: "tool_call", Content: "lookup_fodmap(garlic)"},
		{ID: "3", Role: "tool_response", Content: `{"found": true}`},
		{ID: "4", Role: "model", Content: "Garlic is high FODMAP"},
	}
	result := messagesToContent(msgs)
	if len(result) != 2 {
		t.Fatalf("got %d contents, want 2 (tool messages should be skipped)", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("first role = %q, want %q", result[0].Role, "user")
	}
	if result[1].Role != "model" {
		t.Errorf("second role = %q, want %q", result[1].Role, "model")
	}
}

func TestMessagesToContent_Empty(t *testing.T) {
	result := messagesToContent(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

// ---- saveModelResponse ----

func TestSaveModelResponse(t *testing.T) {
	store := newMockStore()
	convID := "conv-save-test"
	_ = store.CreateConversation(context.Background(), &auth.Conversation{
		ID: convID, UserID: "u1", BusinessID: "b1", Title: "Test",
	})

	result := chat.SendResult{Text: "Model says hello", ToolCalls: []string{"lookup_fodmap(garlic)"}}
	saveModelResponse(context.Background(), store, convID, result, 1)

	msgs, err := store.GetMessages(context.Background(), convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Role != "model" {
		t.Errorf("role = %q, want %q", msgs[0].Role, "model")
	}
	if msgs[0].Content != "Model says hello" {
		t.Errorf("content = %q, want %q", msgs[0].Content, "Model says hello")
	}
}
