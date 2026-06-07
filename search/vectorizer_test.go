package search

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVectorizerClient_EmbedSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vectors" {
			t.Errorf("expected path /vectors, got %s", r.URL.Path)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode req: %v", err)
		}
		if req["text"] != "hello" {
			t.Errorf("expected 'hello', got %v", req["text"])
		}

		resp := map[string][]float32{
			"vector": {0.1, 0.2, 0.3},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewVectorizerClient(srv.URL)
	defer func() { _ = client.Close() }()

	res, err := client.EmbedSingle(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 3 || res[0] != 0.1 {
		t.Errorf("unexpected response: %v", res)
	}
}

func TestVectorizerClient_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vectors/batch" {
			t.Errorf("expected path /vectors/batch, got %s", r.URL.Path)
		}

		// Write binary response
		// 2 rows, 2 dims
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header[0:4], 2)
		binary.LittleEndian.PutUint32(header[4:8], 2)
		_, _ = w.Write(header)

		// 2x2 float32 array
		_ = binary.Write(w, binary.LittleEndian, []float32{0.1, 0.2, 0.3, 0.4})
	}))
	defer srv.Close()

	client := NewVectorizerClient(srv.URL)

	res, err := client.EmbedBatch(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("expected 2 vectors, got %d", len(res))
	}
	if len(res[0]) != 2 || res[0][0] != 0.1 {
		t.Errorf("unexpected first vector: %v", res[0])
	}
}

func TestVectorizerClient_Errors(t *testing.T) {
	t.Run("Single 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		client := NewVectorizerClient(srv.URL)
		_, err := client.EmbedSingle(context.Background(), "test")
		if err == nil || err.Error() != "vectorizer error (status 500): " {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Batch 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		client := NewVectorizerClient(srv.URL)
		_, err := client.EmbedBatch(context.Background(), []string{"test"})
		if err == nil || err.Error() != "vectorizer error (status 500): " {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Batch Empty Input", func(t *testing.T) {
		client := NewVectorizerClient("http://localhost")
		res, err := client.EmbedBatch(context.Background(), []string{})
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if res != nil {
			t.Errorf("expected nil res, got %v", res)
		}
	})
	
	t.Run("Batch Wrong Row Count", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := make([]byte, 8)
			binary.LittleEndian.PutUint32(header[0:4], 1) // Only 1 row but expected 2
			binary.LittleEndian.PutUint32(header[4:8], 2)
			_, _ = w.Write(header)
		}))
		defer srv.Close()

		client := NewVectorizerClient(srv.URL)
		_, err := client.EmbedBatch(context.Background(), []string{"one", "two"})
		if err == nil || err.Error() != "unexpected row count: got 1, want 2" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
