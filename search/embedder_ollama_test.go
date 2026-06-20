package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedder_EmbedSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("expected path /api/embed, got %s", r.URL.Path)
		}
		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		if req.Model != "nomic-embed-text" {
			t.Errorf("expected model nomic-embed-text, got %s", req.Model)
		}
		// Expect prefix
		if req.Input != "search_query: hello" {
			t.Errorf("expected 'search_query: hello', got %v", req.Input)
		}

		resp := ollamaResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	embedder := NewOllamaEmbedder(srv.URL, "nomic-embed-text")
	defer func() { _ = embedder.Close() }()

	res, err := embedder.EmbedSingle(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 3 || res[0] != 0.1 {
		t.Errorf("unexpected response: %v", res)
	}
}

func TestOllamaEmbedder_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}

		inputs, ok := req.Input.([]any)
		if !ok || len(inputs) != 2 {
			t.Fatalf("expected 2 inputs, got %v", req.Input)
		}
		if inputs[0].(string) != "search_document: one" {
			t.Errorf("unexpected input 0: %v", inputs[0])
		}

		resp := ollamaResponse{
			Embeddings: [][]float32{{0.1}, {0.2}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	embedder := NewOllamaEmbedder(srv.URL, "nomic-embed-text")
	res, err := embedder.EmbedBatch(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("expected 2 embeddings, got %d", len(res))
	}
}

func TestOllamaEmbedder_Errors(t *testing.T) {
	t.Run("404 NotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		embedder := NewOllamaEmbedder(srv.URL, "test-model")
		_, err := embedder.EmbedSingle(context.Background(), "test")
		if err == nil || err.Error() != "ollama returned 404: ensure model 'test-model' is pulled (run 'ollama pull test-model')" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("500 ServerError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		embedder := NewOllamaEmbedder(srv.URL, "test-model")
		_, err := embedder.EmbedSingle(context.Background(), "test")
		if err == nil || err.Error() != "unexpected status from ollama: 500 Internal Server Error" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Empty Response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(ollamaResponse{Embeddings: [][]float32{}})
		}))
		defer srv.Close()
		embedder := NewOllamaEmbedder(srv.URL, "test-model")
		_, err := embedder.EmbedSingle(context.Background(), "test")
		if err == nil || err.Error() != "no embeddings returned" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Mismatch Count", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(ollamaResponse{Embeddings: [][]float32{{0.1}}})
		}))
		defer srv.Close()
		embedder := NewOllamaEmbedder(srv.URL, "test-model")
		_, err := embedder.EmbedBatch(context.Background(), []string{"one", "two"})
		if err == nil || err.Error() != "expected 2 embeddings, got 1" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
