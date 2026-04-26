package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"fodmap/data"
	"fodmap/data/schemas"
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

func TestPineconeClient_EnsureSchema(t *testing.T) {
	client := NewPineconeClient("test-key", "http://localhost:1234", &mockEmbedder{vec: []float32{0.1}})
	// EnsureSchema is a no-op for Pinecone
	if err := client.EnsureSchema(context.Background()); err != nil {
		t.Errorf("EnsureSchema returned unexpected error: %v", err)
	}
}

func TestPineconeClient_GetBusinesses_WithFilters(t *testing.T) {
	mockEmb := mockVecEmbedder()

	var requestPayload map[string]any
	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query" {
			_ = json.NewDecoder(r.Body).Decode(&requestPayload)
			res := PineconeQueryResponse{
				Matches: []struct {
					ID       string         `json:"id"`
					Score    float64        `json:"score"`
					Metadata map[string]any `json:"metadata"`
				}{
					{ID: "r1", Score: 0.9, Metadata: map[string]any{
						"business_id": "biz-1", "business_name": "Pizza Place",
						"city": "Las Vegas", "state": "NV", "categories": "Italian",
					}},
				},
			}
			_ = json.NewEncoder(w).Encode(res)
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	_, err := client.GetBusinesses(context.Background(), "pizza", 5, SearchFilter{City: "Las Vegas", State: "NV"})
	if err != nil {
		t.Fatalf("GetBusinesses with filters failed: %v", err)
	}

	filter, ok := requestPayload["filter"].(map[string]any)
	if !ok {
		t.Fatal("expected filter in request payload")
	}
	cityFilter, _ := filter["city"].(map[string]any)
	if cityFilter["$eq"] != "Las Vegas" {
		t.Errorf("expected city filter $eq 'Las Vegas', got %v", cityFilter)
	}
	stateFilter, _ := filter["state"].(map[string]any)
	if stateFilter["$eq"] != "NV" {
		t.Errorf("expected state filter $eq 'NV', got %v", stateFilter)
	}
}

func TestPineconeClient_GetBusinesses_EmptyResult(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(PineconeQueryResponse{})
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	result, err := client.GetBusinesses(context.Background(), "nonexistent", 5, SearchFilter{})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}
	if len(result.Businesses) != 0 {
		t.Errorf("expected 0 businesses, got %d", len(result.Businesses))
	}
}

func TestPineconeClient_SearchFodmap_WithSubstitutions(t *testing.T) {
	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

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
						Score: 0.97,
						Metadata: map[string]any{
							"ingredient":    "garlic",
							"level":         "high",
							"groups":        []any{"fructans"},
							"notes":         "Even small amounts are high FODMAP",
							"substitutions": []any{"garlic-infused olive oil", "chives"},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(res)
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	res, score, err := client.SearchFodmap(context.Background(), "garlic")
	if err != nil {
		t.Fatalf("SearchFodmap failed: %v", err)
	}
	if res.Ingredient != "garlic" {
		t.Errorf("ingredient = %q, want %q", res.Ingredient, "garlic")
	}
	if res.Level != "high" {
		t.Errorf("level = %q, want %q", res.Level, "high")
	}
	if len(res.Substitutions) != 2 {
		t.Fatalf("substitutions length = %d, want 2", len(res.Substitutions))
	}
	if res.Substitutions[0] != "garlic-infused olive oil" {
		t.Errorf("substitutions[0] = %q, want %q", res.Substitutions[0], "garlic-infused olive oil")
	}
	if res.Notes != "Even small amounts are high FODMAP" {
		t.Errorf("notes = %q, want %q", res.Notes, "Even small amounts are high FODMAP")
	}
	if score != 0.97 {
		t.Errorf("score = %f, want 0.97", score)
	}
}

func TestPineconeClient_SearchFodmap_NotFound(t *testing.T) {
	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/query" {
			_ = json.NewEncoder(w).Encode(PineconeQueryResponse{Matches: nil})
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	_, _, err := client.SearchFodmap(context.Background(), "xyz")
	if err == nil {
		t.Error("expected error for not found ingredient")
	}
}

func TestPineconeClient_EnsureFodmapSchema(t *testing.T) {
	client := NewPineconeClient("test-key", "http://localhost:1234", &mockEmbedder{vec: []float32{0.1}})
	// EnsureFodmapSchema is a no-op for Pinecone; should return nil
	if err := client.EnsureFodmapSchema(context.Background()); err != nil {
		t.Errorf("EnsureFodmapSchema returned unexpected error: %v", err)
	}
}

func TestPineconeClient_BatchUpsertFodmap_WithSubstitutions(t *testing.T) {
	mockEmb := &mockEmbedder{vec: []float32{0.0, 0.0, 0.0}}

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
		"garlic": {Level: "high", Groups: []string{"fructans"}, Substitutions: []string{"garlic-infused olive oil", "chives"}},
	})
	if err != nil {
		t.Fatalf("BatchUpsertFodmap failed: %v", err)
	}

	vectors := upsertedBody["vectors"].([]any)
	if len(vectors) != 1 {
		t.Fatalf("got %d vectors, want 1", len(vectors))
	}
	vec := vectors[0].(map[string]any)
	meta := vec["metadata"].(map[string]any)
	subs, ok := meta["substitutions"].([]any)
	if !ok {
		t.Fatal("substitutions not found in metadata")
	}
	if len(subs) != 2 {
		t.Errorf("got %d substitutions, want 2", len(subs))
	}
}

func TestPineconeClient_BatchUpsertFodmap_Empty(t *testing.T) {
	mockEmb := &mockEmbedder{vec: []float32{0.1}}
	client := NewPineconeClient("test-key", "http://localhost:1234", mockEmb)

	err := client.BatchUpsertFodmap(context.Background(), map[string]data.FodmapEntry{})
	if err != nil {
		t.Errorf("BatchUpsertFodmap with empty map should be no-op, got error: %v", err)
	}
}

func TestPineconeClient_BatchUpsert(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vectors/upsert" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"upsertedCount": 1})
		}
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	items := []IndexItem{
		{
			Review:       schemas.Review{ReviewID: "r1", BusinessID: "b1", Stars: 5, Text: "great"},
			BusinessName: "Pizza Place",
			City:         "NYC",
			State:        "NY",
			Categories:   "Italian",
			Vector:       []float32{0.1, 0.2, 0.3},
		},
	}
	if err := client.BatchUpsert(context.Background(), items); err != nil {
		t.Fatalf("BatchUpsert failed: %v", err)
	}
}

func TestPineconeClient_BatchUpsert_Empty(t *testing.T) {
	mockEmb := mockVecEmbedder()
	client := NewPineconeClient("test-key", "http://localhost:1234", mockEmb)

	err := client.BatchUpsert(context.Background(), nil)
	if err != nil {
		t.Errorf("BatchUpsert with nil items should succeed, got: %v", err)
	}
}

func TestPineconeClient_GetBusinesses_EmbedError(t *testing.T) {
	mockEmb := &mockEmbedder{err: fmt.Errorf("embed failed")}
	client := NewPineconeClient("test-key", "http://localhost:1234", mockEmb)

	_, err := client.GetBusinesses(context.Background(), "pizza", 5, SearchFilter{})
	if err == nil {
		t.Error("expected error when embedder fails")
	}
}

func TestPineconeClient_SearchFodmap_EmbedError(t *testing.T) {
	mockEmb := &mockEmbedder{err: fmt.Errorf("embed failed")}
	client := NewPineconeClient("test-key", "http://localhost:1234", mockEmb)

	_, _, err := client.SearchFodmap(context.Background(), "garlic")
	if err == nil {
		t.Error("expected error when embedder fails")
	}
}

func TestPineconeClient_GetBusinesses_ServerError(t *testing.T) {
	mockEmb := mockVecEmbedder()

	pineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer pineServer.Close()

	client := NewPineconeClient("test-key", pineServer.URL, mockEmb)

	_, err := client.GetBusinesses(context.Background(), "pizza", 5, SearchFilter{})
	if err == nil {
		t.Error("expected error when server returns 500")
	}
}
