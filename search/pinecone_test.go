package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/data"
)

func TestPineconeClient_SearchFodmap(t *testing.T) {
	// 1. Mock Vectorizer
	vecServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(map[string]any{
			"vector": []float32{0.1, 0.2, 0.3},
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer vecServer.Close()
	v := NewVectorizerClient(vecServer.URL)

	// 2. Mock Pinecone
	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query" {
			res := PineconeQueryResponse{
				Matches: []struct {
					ID       string         `json:"id"`
					Score    float64        `json:"score"`
					Metadata map[string]any `json:"metadata"`
				}{
					{
						ID:    "fodmap-garlic",
						Score: 0.99,
						Metadata: map[string]any{
							"ingredient": "garlic",
							"level":      "high",
							"groups":     []any{"fructans"},
							"notes":      "Keep away",
						},
					},
				},
			}
			err := json.NewEncoder(w).Encode(res)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, v)

	res, score, err := client.SearchFodmap(context.Background(), "garlic")
	if err != nil {
		t.Fatalf("SearchFodmap failed: %v", err)
	}

	if res.Ingredient != "garlic" {
		t.Errorf("got ingredient %q, want %q", res.Ingredient, "garlic")
	}
	if res.Level != "high" {
		t.Errorf("got level %q, want %q", res.Level, "high")
	}
	if score != 0.99 {
		t.Errorf("got score %f, want %f", score, 0.99)
	}
}

func TestPineconeClient_BatchUpsertFodmap(t *testing.T) {
	// 1. Mock Vectorizer (Batch)
	vecServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock binary response header (rows=1, dim=3)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{1, 0, 0, 0, 3, 0, 0, 0}) // LE rows=1, dim=3
		// Mock float32 data
		_, _ = w.Write([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}) // zeroes
	}))
	defer vecServer.Close()
	v := NewVectorizerClient(vecServer.URL)

	// 2. Mock Pinecone (Upsert)
	var upsertedBody map[string]any
	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vectors/upsert" {
			if err := json.NewDecoder(r.Body).Decode(&upsertedBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, v)

	err := client.BatchUpsertFodmap(context.Background(), map[string]data.FodmapEntry{
		"garlic": {Level: "high", Groups: []string{"fructans"}},
	})
	if err != nil {
		t.Fatalf("BatchUpsertFodmap failed: %v", err)
	}

	if upsertedBody["namespace"] != pineconeFodmapNamespace {
		t.Errorf("got namespace %q, want %q", upsertedBody["namespace"], pineconeFodmapNamespace)
	}
	vectors := upsertedBody["vectors"].([]any)
	if len(vectors) != 1 {
		t.Errorf("got %d vectors, want 1", len(vectors))
	}
}
