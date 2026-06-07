package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"google.golang.org/genai"
)

type mockRoundTripper struct {
	roundTripFunc func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestGeminiBackend_Generate(t *testing.T) {
	mockRT := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			body := `data: {"candidates": [{"content": {"parts": [{"text": "Hello from Gemini mock"},{"functionCall": {"name": "lookup_fodmap", "args": {"food": "onion"}}}]}}]}
` + "\n"

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		},
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     "test-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: &http.Client{Transport: mockRT},
	})
	if err != nil {
		t.Fatalf("failed to create genai client: %v", err)
	}

	backend := NewGeminiBackend(client, "gemini-2.5-flash")

	opts := GenerateOpts{
		SystemPrompt: "You are a helpful assistant.",
		History: []Message{
			{Role: "user", Text: "Hello"},
			{Role: "model", FunctionCalls: []FunctionCall{{Name: "lookup_fodmap", Args: map[string]any{"food": "onion"}, ThoughtSignature: "thought"}}},
			{Role: "tool", FunctionResults: []FunctionResult{{Name: "lookup_fodmap", Result: map[string]any{"status": "high"}}}},
		},
		Tools: []ToolDeclaration{
			{Name: "lookup_fodmap", Description: "Lookup FODMAP", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}

	msg, err := backend.Generate(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Text != "Hello from Gemini mock" {
		t.Errorf("expected text 'Hello from Gemini mock', got '%s'", msg.Text)
	}

	if len(msg.FunctionCalls) != 1 {
		t.Fatalf("expected 1 function call, got %d", len(msg.FunctionCalls))
	}
	if msg.FunctionCalls[0].Name != "lookup_fodmap" {
		t.Errorf("expected function call lookup_fodmap, got %s", msg.FunctionCalls[0].Name)
	}
}

func TestGeminiBackend_Generate_Error(t *testing.T) {
	mockRT := &mockRoundTripper{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewBufferString(`{"error": {"message": "internal error"}}`)),
				Header:     make(http.Header),
			}, nil
		},
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     "test-key",
		HTTPClient: &http.Client{Transport: mockRT},
	})
	if err != nil {
		t.Fatalf("failed to create genai client: %v", err)
	}

	backend := NewGeminiBackend(client, "gemini-2.5-flash")

	_, err = backend.Generate(context.Background(), GenerateOpts{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
