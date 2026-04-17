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
	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

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

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

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
	mockEmb := &mockEmbedder{vec: []float32{0.0, 0.0, 0.0}}

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

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

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

// mockVecEmbedder returns a mockEmbedder that always returns a fixed vector.
func mockVecEmbedder() *mockEmbedder {
	return &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}
}

func TestPineconeClient_GetBusinesses(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := PineconeQueryResponse{
			Matches: []struct {
				ID       string         `json:"id"`
				Score    float64        `json:"score"`
				Metadata map[string]any `json:"metadata"`
			}{
				{
					ID: "rev-1", Score: 0.95,
					Metadata: map[string]any{
						"business_id":   "biz-1",
						"business_name": "Pizza Place",
						"city":          "NYC",
						"state":         "NY",
					},
				},
				{
					ID: "rev-2", Score: 0.90,
					Metadata: map[string]any{
						"business_id":   "biz-1", // same business, should be deduped
						"business_name": "Pizza Place",
						"city":          "NYC",
						"state":         "NY",
					},
				},
				{
					ID: "rev-3", Score: 0.85,
					Metadata: map[string]any{
						"business_id":   "biz-2",
						"business_name": "Taco Shop",
						"city":          "LA",
						"state":         "CA",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	result, err := client.GetBusinesses(context.Background(), "pizza", 5, SearchFilter{})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}

	if len(result.Businesses) != 2 {
		t.Fatalf("got %d businesses, want 2 (duplicate biz-1 should be deduped)", len(result.Businesses))
	}
	if result.Businesses[0].ID != "biz-1" {
		t.Errorf("first business ID = %q, want %q", result.Businesses[0].ID, "biz-1")
	}
	if result.Businesses[0].Name != "Pizza Place" {
		t.Errorf("first business name = %q, want %q", result.Businesses[0].Name, "Pizza Place")
	}
	if result.Businesses[1].ID != "biz-2" {
		t.Errorf("second business ID = %q, want %q", result.Businesses[1].ID, "biz-2")
	}
}

func TestPineconeClient_GetBusinesses_LimitRespected(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := PineconeQueryResponse{
			Matches: []struct {
				ID       string         `json:"id"`
				Score    float64        `json:"score"`
				Metadata map[string]any `json:"metadata"`
			}{
				{ID: "r1", Score: 0.9, Metadata: map[string]any{"business_id": "b1", "business_name": "A", "city": "C", "state": "S"}},
				{ID: "r2", Score: 0.8, Metadata: map[string]any{"business_id": "b2", "business_name": "B", "city": "C", "state": "S"}},
				{ID: "r3", Score: 0.7, Metadata: map[string]any{"business_id": "b3", "business_name": "C", "city": "C", "state": "S"}},
			},
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	result, err := client.GetBusinesses(context.Background(), "food", 2, SearchFilter{})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}
	if len(result.Businesses) != 2 {
		t.Errorf("got %d businesses, want 2 (limit=2)", len(result.Businesses))
	}
}

func TestPineconeClient_GetReviews(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := PineconeQueryResponse{
			Matches: []struct {
				ID       string         `json:"id"`
				Score    float64        `json:"score"`
				Metadata map[string]any `json:"metadata"`
			}{
				{
					ID: "rev-1", Score: 0.95,
					Metadata: map[string]any{
						"review_id":     "r1",
						"business_name": "Pizza Place",
						"city":          "NYC",
						"state":         "NY",
						"text":          "Amazing pizza",
						"stars":         float64(5),
					},
				},
				{
					ID: "rev-2", Score: 0.88,
					Metadata: map[string]any{
						"review_id":     "r2",
						"business_name": "Pizza Place",
						"city":          "NYC",
						"state":         "NY",
						"text":          "Good crust",
						"stars":         float64(4),
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	result, err := client.GetReviews(context.Background(), "pizza", 10, SearchFilter{BusinessID: "biz-1"})
	if err != nil {
		t.Fatalf("GetReviews failed: %v", err)
	}

	if len(result.BusinessReviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(result.BusinessReviews))
	}
	if result.BusinessReviews[0].Review.Review.Text != "Amazing pizza" {
		t.Errorf("first review text = %q, want %q", result.BusinessReviews[0].Review.Review.Text, "Amazing pizza")
	}
	if result.BusinessReviews[0].Score != 0.95 {
		t.Errorf("first review score = %f, want 0.95", result.BusinessReviews[0].Score)
	}
}

func TestPineconeClient_GetReviews_NoMatches(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(PineconeQueryResponse{})
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	result, err := client.GetReviews(context.Background(), "xyz", 10, SearchFilter{})
	if err != nil {
		t.Fatalf("GetReviews failed: %v", err)
	}
	if len(result.BusinessReviews) != 0 {
		t.Errorf("got %d reviews, want 0", len(result.BusinessReviews))
	}
}

// TestPineconeClient_GetReviews_HybridBlend verifies that when Alpha<1, returned scores
// are blended between the dense score and BM25 keyword score.
// Match A: high dense score (0.95) but text doesn't match query.
// Match B: low dense score (0.50) but text perfectly matches query.
// With Alpha=0.0 (pure BM25), B should rank above A.
func TestPineconeClient_GetReviews_HybridBlend(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := PineconeQueryResponse{
			Matches: []struct {
				ID       string         `json:"id"`
				Score    float64        `json:"score"`
				Metadata map[string]any `json:"metadata"`
			}{
				{
					ID: "rev-A", Score: 0.95,
					Metadata: map[string]any{
						"review_id":     "rA",
						"business_name": "Sushi Bar",
						"city":          "NYC",
						"state":         "NY",
						"text":          "excellent sushi and sake selection",
						"stars":         float64(5),
					},
				},
				{
					ID: "rev-B", Score: 0.50,
					Metadata: map[string]any{
						"review_id":     "rB",
						"business_name": "Pizza Place",
						"city":          "NYC",
						"state":         "NY",
						"text":          "gluten free pizza gluten free crust gluten free options",
						"stars":         float64(4),
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	// Pure BM25 (alpha=0): B should rank above A because B's text matches "gluten free".
	result, err := client.GetReviews(context.Background(), "gluten free", 10, SearchFilter{Alpha: 0.01})
	if err != nil {
		t.Fatalf("GetReviews failed: %v", err)
	}
	if len(result.BusinessReviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(result.BusinessReviews))
	}
	if result.BusinessReviews[0].Review.Review.ReviewID != "rB" {
		t.Errorf("expected BM25-boosted review B to rank first, got %q", result.BusinessReviews[0].Review.Review.ReviewID)
	}

	// Pure vector (alpha=1): A should rank first due to higher dense score.
	result2, err := client.GetReviews(context.Background(), "gluten free", 10, SearchFilter{Alpha: 1.0})
	if err != nil {
		t.Fatalf("GetReviews (alpha=1) failed: %v", err)
	}
	if result2.BusinessReviews[0].Review.Review.ReviewID != "rA" {
		t.Errorf("expected dense-score review A to rank first with alpha=1, got %q", result2.BusinessReviews[0].Review.Review.ReviewID)
	}
}
