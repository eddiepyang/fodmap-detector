package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTEIEmbedder_EmbedSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("expected path /embed, got %s", r.URL.Path)
		}
		var req teiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		if req.Truncate != true {
			t.Errorf("expected truncate=true, got %v", req.Truncate)
		}
		if len(req.Inputs) != 1 || req.Inputs[0] != "search_query: hello" {
			t.Errorf("expected inputs=['search_query: hello'], got %v", req.Inputs)
		}
		// Object shape: {"embeddings": [...]}
		_ = json.NewEncoder(w).Encode(teiResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer srv.Close()

	e := NewTEIEmbedder(srv.URL, "nomic-embed-text")
	defer func() { _ = e.Close() }()

	res, err := e.EmbedSingle(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 3 || res[0] != 0.1 {
		t.Errorf("unexpected response: %v", res)
	}
}

func TestTEIEmbedder_EmbedBatch_BareArray(t *testing.T) {
	// Newer TEI builds return a bare array [[...]] instead of {"embeddings": [...]}.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req teiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		if len(req.Inputs) != 2 {
			t.Fatalf("expected 2 inputs, got %d", len(req.Inputs))
		}
		if req.Inputs[0] != "search_document: one" {
			t.Errorf("unexpected input 0: %v", req.Inputs[0])
		}
		if req.Inputs[1] != "search_document: two" {
			t.Errorf("unexpected input 1: %v", req.Inputs[1])
		}
		// Bare array shape.
		_ = json.NewEncoder(w).Encode([][]float32{{0.1, 0.2}, {0.3, 0.4}})
	}))
	defer srv.Close()

	e := NewTEIEmbedder(srv.URL, "nomic-embed-text")
	res, err := e.EmbedBatch(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(res))
	}
	if res[0][0] != 0.1 || res[1][0] != 0.3 {
		t.Errorf("unexpected vectors: %v", res)
	}
}

func TestTEIEmbedder_EmbedBatch_OrderPreserved(t *testing.T) {
	// The Embedder contract requires vectors to be returned in input order.
	// Return distinct per-text vectors and verify the mapping.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req teiRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Echo back one distinct vector per input, derived from the input
		// index so order is detectable.
		out := make([][]float32, len(req.Inputs))
		for i := range req.Inputs {
			out[i] = []float32{float32(i), float32(i + 1)}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	e := NewTEIEmbedder(srv.URL, "nomic-embed-text")
	res, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("expected 4 vectors, got %d", len(res))
	}
	for i, v := range res {
		if v[0] != float32(i) {
			t.Errorf("vector %d: expected first elem %d, got %v", i, i, v[0])
		}
	}
}

func TestTEIEmbedder_EmptyBatch(t *testing.T) {
	e := NewTEIEmbedder("http://unused", "nomic-embed-text")
	res, err := e.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Errorf("expected nil for empty batch, got %v", res)
	}
}

func TestTEIEmbedder_CountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return fewer vectors than requested.
		_ = json.NewEncoder(w).Encode([][]float32{{0.1}})
	}))
	defer srv.Close()

	e := NewTEIEmbedder(srv.URL, "nomic-embed-text")
	_, err := e.EmbedBatch(context.Background(), []string{"one", "two"})
	if err == nil {
		t.Fatal("expected error for count mismatch")
	}
}

func TestTEIEmbedder_Errors(t *testing.T) {
	t.Run("500 ServerError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("model not loaded"))
		}))
		defer srv.Close()
		e := NewTEIEmbedder(srv.URL, "nomic-embed-text")
		_, err := e.EmbedSingle(context.Background(), "test")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestNewEmbedder_Factory(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown type", func(t *testing.T) {
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "bogus"})
		if err == nil {
			t.Fatal("expected error for unknown embedder type")
		}
	})

	t.Run("ollama missing url", func(t *testing.T) {
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "ollama"})
		if err == nil {
			t.Fatal("expected error for missing ollama url")
		}
	})

	t.Run("tei missing url", func(t *testing.T) {
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "tei"})
		if err == nil {
			t.Fatal("expected error for missing tei url")
		}
	})

	t.Run("vectorizer missing url", func(t *testing.T) {
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "vectorizer"})
		if err == nil {
			t.Fatal("expected error for missing vectorizer url")
		}
	})

	t.Run("startup ping wrong dim", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(teiResponse{Embeddings: [][]float32{{0.1, 0.2}}}) // 2-dim, not 768
		}))
		defer srv.Close()
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "tei", TEIURL: srv.URL, TEIModel: "test"})
		if err == nil {
			t.Fatal("expected error for wrong dim")
		}
	})

	t.Run("startup ping ok with 768-dim", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vec := make([]float32, ExpectedEmbeddingDim)
			vec[0] = 0.5
			_ = json.NewEncoder(w).Encode(teiResponse{Embeddings: [][]float32{vec}})
		}))
		defer srv.Close()
		e, err := NewEmbedder(ctx, EmbedderConfig{Type: "tei", TEIURL: srv.URL, TEIModel: "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer func() { _ = e.Close() }()
	})

	t.Run("startup ping failure (bad backend)", func(t *testing.T) {
		// Point at a port that's almost certainly not listening.
		_, err := NewEmbedder(ctx, EmbedderConfig{Type: "tei", TEIURL: "http://127.0.0.1:1"})
		if err == nil {
			t.Fatal("expected error for unreachable backend")
		}
	})
}
