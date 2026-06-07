package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatBackend_Generate(t *testing.T) {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "Hello from OpenAI mock",
						"tool_calls": []map[string]any{
							{
								"id": "call_123",
								"type": "function",
								"function": map[string]any{
									"name": "lookup_fodmap",
									"arguments": `{"food":"garlic"}`,
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	backend := NewOpenAICompatBackend(ts.URL, "gpt-3.5-turbo", "test-key")

	ctx := context.Background()
	opts := GenerateOpts{
		SystemPrompt: "You are a helpful assistant.",
		History: []Message{
			{Role: "user", Text: "Hello"},
			{Role: "tool", FunctionResults: []FunctionResult{{Name: "lookup_fodmap", Result: map[string]any{"status": "high"}}}},
			{Role: "model", FunctionCalls: []FunctionCall{{Name: "lookup_fodmap", Args: map[string]any{"food": "onion"}}}},
		},
		Tools: []ToolDeclaration{
			{Name: "lookup_fodmap", Description: "Lookup FODMAP"},
		},
	}

	msg, err := backend.Generate(ctx, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Text != "Hello from OpenAI mock" {
		t.Errorf("expected text 'Hello from OpenAI mock', got '%s'", msg.Text)
	}

	if len(msg.FunctionCalls) != 1 {
		t.Fatalf("expected 1 function call, got %d", len(msg.FunctionCalls))
	}
	if msg.FunctionCalls[0].Name != "lookup_fodmap" {
		t.Errorf("expected function call lookup_fodmap, got %s", msg.FunctionCalls[0].Name)
	}
}

func TestOpenAICompatBackend_Generate_Stream(t *testing.T) {
	mux := http.NewServeMux()
	
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		
		chunks := []string{
			`{"choices": [{"delta": {"content": "Hello ", "tool_calls": [{"index": 0, "id": "call_456", "type": "function", "function": {"name": "lookup_fodmap", "arguments": "{\"food\": \"apple\"}"}}]}}]}`,
			`{"choices": [{"delta": {"content": "world!"}}]}`,
			`[DONE]`,
		}
		
		for _, chunk := range chunks {
			if chunk == "[DONE]" {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			} else {
				_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
			}
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	backend := NewOpenAICompatBackend(ts.URL, "gpt-3.5-turbo", "test-key")

	ctx := context.Background()
	var streamText string
	opts := GenerateOpts{
		History: []Message{{Role: "user", Text: "Hello stream"}},
		OnText: func(s string) {
			streamText += s
		},
	}

	msg, err := backend.Generate(ctx, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.Text != "Hello world!" {
		t.Errorf("expected text 'Hello world!', got '%s'", msg.Text)
	}
	
	if streamText != "Hello world!" {
		t.Errorf("expected stream text 'Hello world!', got '%s'", streamText)
	}

	if len(msg.FunctionCalls) != 1 {
		t.Fatalf("expected 1 function call, got %d", len(msg.FunctionCalls))
	}
	if msg.FunctionCalls[0].Name != "lookup_fodmap" {
		t.Errorf("expected function call lookup_fodmap, got %s", msg.FunctionCalls[0].Name)
	}
}

func TestOpenAICompatBackend_Generate_Error(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	backend := NewOpenAICompatBackend(ts.URL, "gpt-3.5-turbo", "test-key")

	_, err := backend.Generate(context.Background(), GenerateOpts{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
