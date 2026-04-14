package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fodmap/data"
	"fodmap/data/schemas"

	"github.com/weaviate/weaviate/entities/models"
)

// makeData constructs the shape of data returned by the Weaviate GraphQL response.
// items is a slice of {businessId, businessName, certainty} triples.
func makeData(items []struct {
	businessID   string
	businessName string
	certainty    float64
}) map[string]models.JSONObject {
	rawItems := make([]any, len(items))
	for i, item := range items {
		rawItems[i] = map[string]any{
			"businessId":   item.businessID,
			"businessName": item.businessName,
			"_additional": map[string]any{
				"certainty": item.certainty,
			},
		}
	}
	return map[string]models.JSONObject{
		"Get": map[string]any{
			collectionName: rawItems,
		},
	}
}

// --- extractScore tests ---

func TestExtractScore_HybridScorePreferred(t *testing.T) {
	additional := map[string]any{"score": 0.8, "certainty": 0.9}
	got := extractScore(additional)
	if got != 0.8 {
		t.Errorf("got %f, want 0.8 (hybrid score should take priority over certainty)", got)
	}
}

func TestExtractScore_FallsBackToCertainty(t *testing.T) {
	additional := map[string]any{"certainty": 0.7}
	got := extractScore(additional)
	if got != 0.7 {
		t.Errorf("got %f, want 0.7", got)
	}
}

func TestExtractScore_DefaultsToOne(t *testing.T) {
	got := extractScore(map[string]any{})
	if got != 1.0 {
		t.Errorf("got %f, want 1.0", got)
	}
}

// --- aggregateTopK tests ---

func TestAggregateTopK_Empty(t *testing.T) {
	result := aggregateTopK(map[string]models.JSONObject{}, 5)
	if len(result.Businesses) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.Businesses))
	}
}

func TestAggregateTopK_SingleBusiness(t *testing.T) {
	data := makeData([]struct {
		businessID   string
		businessName string
		certainty    float64
	}{
		{"biz1", "Foo Bar", 0.9},
	})
	result := aggregateTopK(data, 5)
	if len(result.Businesses) != 1 {
		t.Fatalf("got %d results, want 1", len(result.Businesses))
	}
	if result.Businesses[0].ID != "biz1" {
		t.Errorf("got %q, want %q", result.Businesses[0].ID, "biz1")
	}
	if result.Businesses[0].Name != "Foo Bar" {
		t.Errorf("got name %q, want %q", result.Businesses[0].Name, "Foo Bar")
	}
}

func TestAggregateTopK_RankedByScore(t *testing.T) {
	data := makeData([]struct {
		businessID   string
		businessName string
		certainty    float64
	}{
		{"low", "Low Place", 0.5},
		{"high", "High Place", 0.9},
		{"mid", "Mid Place", 0.7},
	})
	result := aggregateTopK(data, 3)
	if len(result.Businesses) != 3 {
		t.Fatalf("got %d results, want 3", len(result.Businesses))
	}
	if result.Businesses[0].ID != "high" {
		t.Errorf("first result = %q, want %q", result.Businesses[0].ID, "high")
	}
	if result.Businesses[2].ID != "low" {
		t.Errorf("last result = %q, want %q", result.Businesses[2].ID, "low")
	}
}

func TestAggregateTopK_LimitTruncates(t *testing.T) {
	data := makeData([]struct {
		businessID   string
		businessName string
		certainty    float64
	}{
		{"biz1", "Biz One", 0.9},
		{"biz2", "Biz Two", 0.8},
		{"biz3", "Biz Three", 0.7},
	})
	result := aggregateTopK(data, 2)
	if len(result.Businesses) != 2 {
		t.Errorf("got %d results, want 2 (limit=2)", len(result.Businesses))
	}
}

func TestAggregateTopK_TopKAveraging(t *testing.T) {
	// biz1 has 6 reviews; only the top topKReviews(=5) should count.
	// Top 5 scores: 0.9, 0.8, 0.7, 0.6, 0.5 → avg = 0.70
	// biz2 has 1 review with score 0.72 → avg = 0.72
	// biz2 should rank higher.
	items := []struct {
		businessID   string
		businessName string
		certainty    float64
	}{
		{"biz1", "Biz One", 0.9},
		{"biz1", "Biz One", 0.8},
		{"biz1", "Biz One", 0.7},
		{"biz1", "Biz One", 0.6},
		{"biz1", "Biz One", 0.5},
		{"biz1", "Biz One", 0.1}, // 6th review — should be excluded from top-K
		{"biz2", "Biz Two", 0.72},
	}
	data := makeData(items)
	result := aggregateTopK(data, 2)
	if len(result.Businesses) != 2 {
		t.Fatalf("got %d results, want 2", len(result.Businesses))
	}
	if result.Businesses[0].ID != "biz2" {
		t.Errorf("first result = %q, want biz2 (higher top-K avg)", result.Businesses[0].ID)
	}
}

func TestAggregateTopK_MultipleReviewsSameBusiness(t *testing.T) {
	data := makeData([]struct {
		businessID   string
		businessName string
		certainty    float64
	}{
		{"biz1", "Biz One", 0.8},
		{"biz1", "Biz One", 0.6},
		{"biz2", "Biz Two", 0.7},
	})
	result := aggregateTopK(data, 5)
	// biz1 avg = (0.8 + 0.6) / 2 = 0.7, biz2 avg = 0.7 — order may vary but both present
	if len(result.Businesses) != 2 {
		t.Errorf("got %d results, want 2", len(result.Businesses))
	}
}

// --- buildWhereFilter tests ---

func TestBuildWhereFilter_AllEmpty(t *testing.T) {
	f := buildWhereFilter(SearchFilter{})
	if f != nil {
		t.Error("expected nil filter for empty SearchFilter")
	}
}

func TestBuildWhereFilter_CategoryOnly(t *testing.T) {
	f := buildWhereFilter(SearchFilter{Category: "pizza"})
	if f == nil {
		t.Fatal("expected non-nil filter for category")
	}
}

func TestBuildWhereFilter_CityOnly(t *testing.T) {
	f := buildWhereFilter(SearchFilter{City: "Austin"})
	if f == nil {
		t.Fatal("expected non-nil filter for city")
	}
}

func TestBuildWhereFilter_StateOnly(t *testing.T) {
	f := buildWhereFilter(SearchFilter{State: "TX"})
	if f == nil {
		t.Fatal("expected non-nil filter for state")
	}
}

func TestBuildWhereFilter_BusinessID(t *testing.T) {
	f := buildWhereFilter(SearchFilter{BusinessID: "biz123"})
	if f == nil {
		t.Fatal("expected non-nil filter for BusinessID")
	}
}

func TestBuildWhereFilter_AllThree(t *testing.T) {
	f := buildWhereFilter(SearchFilter{Category: "pizza", City: "Austin", State: "TX"})
	if f == nil {
		t.Fatal("expected non-nil compound filter")
	}
}

// --- ParseFodmapResult tests ---

func TestParseFodmapResult_Empty(t *testing.T) {
	result, certainty, ok := ParseFodmapResult(map[string]models.JSONObject{})
	if ok {
		t.Errorf("expected not ok for empty result, got ok with %v (certainty %f)", result, certainty)
	}
}

func TestParseFodmapResult_Valid(t *testing.T) {
	data := map[string]models.JSONObject{
		"Get": map[string]any{
			"FodmapIngredient": []any{
				map[string]any{
					"ingredient": "garlic",
					"level":      "high",
					"groups":     []any{"fructans"},
					"notes":      "Keep away",
					"_additional": map[string]any{
						"certainty": 0.95,
					},
				},
			},
		},
	}
	result, certainty, ok := ParseFodmapResult(data)
	if !ok {
		t.Fatal("expected ok for valid result")
	}
	if result.Ingredient != "garlic" {
		t.Errorf("got ingredient %q, want %q", result.Ingredient, "garlic")
	}
	if result.Level != "high" {
		t.Errorf("got level %q, want %q", result.Level, "high")
	}
	if len(result.Groups) != 1 || result.Groups[0] != "fructans" {
		t.Errorf("got groups %v, want [fructans]", result.Groups)
	}
	if result.Notes != "Keep away" {
		t.Errorf("got notes %q, want %q", result.Notes, "Keep away")
	}
	if certainty != 0.95 {
		t.Errorf("got certainty %f, want 0.95", certainty)
	}
}

// --- getReviews tests ---

// makeReviewData constructs the shape of GraphQL data for review-based queries.
func makeReviewData(items []struct {
	businessID   string
	reviewID     string
	businessName string
	city         string
	state        string
	text         string
	certainty    float64
}) map[string]models.JSONObject {
	rawItems := make([]any, len(items))
	for i, item := range items {
		rawItems[i] = map[string]any{
			"businessId":   item.businessID,
			"reviewId":     item.reviewID,
			"businessName": item.businessName,
			"city":         item.city,
			"state":        item.state,
			"text":         item.text,
			"_additional": map[string]any{
				"certainty": item.certainty,
			},
		}
	}
	return map[string]models.JSONObject{
		"Get": map[string]any{
			collectionName: rawItems,
		},
	}
}

func TestGetReviews_HappyPath(t *testing.T) {
	data := makeReviewData([]struct {
		businessID   string
		reviewID     string
		businessName string
		city         string
		state        string
		text         string
		certainty    float64
	}{
		{"biz1", "rev1", "Pizza Place", "NYC", "NY", "Great pizza", 0.9},
		{"biz2", "rev2", "Taco Shop", "LA", "CA", "Good tacos", 0.8},
	})
	result := getReviews(data, 10)
	if len(result.BusinessReviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(result.BusinessReviews))
	}
	if result.BusinessReviews[0].Review.Review.Text != "Great pizza" {
		t.Errorf("first review text = %q, want %q", result.BusinessReviews[0].Review.Review.Text, "Great pizza")
	}
}

func TestGetReviews_EmptyData(t *testing.T) {
	result := getReviews(map[string]models.JSONObject{}, 10)
	if len(result.BusinessReviews) != 0 {
		t.Errorf("expected 0 reviews for empty data, got %d", len(result.BusinessReviews))
	}
}

func TestGetReviews_LimitTruncation(t *testing.T) {
	data := makeReviewData([]struct {
		businessID   string
		reviewID     string
		businessName string
		city         string
		state        string
		text         string
		certainty    float64
	}{
		{"biz1", "r1", "A", "C", "S", "review 1", 0.9},
		{"biz2", "r2", "B", "C", "S", "review 2", 0.8},
		{"biz3", "r3", "C", "C", "S", "review 3", 0.7},
	})
	result := getReviews(data, 2)
	if len(result.BusinessReviews) != 2 {
		t.Errorf("got %d reviews, want 2 (limit=2)", len(result.BusinessReviews))
	}
}

func TestGetReviews_SortedByScore(t *testing.T) {
	data := makeReviewData([]struct {
		businessID   string
		reviewID     string
		businessName string
		city         string
		state        string
		text         string
		certainty    float64
	}{
		{"biz1", "r1", "A", "C", "S", "low score", 0.3},
		{"biz2", "r2", "B", "C", "S", "high score", 0.9},
		{"biz3", "r3", "C", "C", "S", "mid score", 0.6},
	})
	result := getReviews(data, 10)
	if len(result.BusinessReviews) != 3 {
		t.Fatalf("got %d reviews, want 3", len(result.BusinessReviews))
	}
	if result.BusinessReviews[0].Score != 0.9 {
		t.Errorf("first review score = %f, want 0.9", result.BusinessReviews[0].Score)
	}
	if result.BusinessReviews[2].Score != 0.3 {
		t.Errorf("last review score = %f, want 0.3", result.BusinessReviews[2].Score)
	}
}

func TestGetReviews_MissingGetKey(t *testing.T) {
	data := map[string]models.JSONObject{
		"NotGet": map[string]any{},
	}
	result := getReviews(data, 10)
	if len(result.BusinessReviews) != 0 {
		t.Errorf("expected 0 reviews for missing Get key, got %d", len(result.BusinessReviews))
	}
}

func TestGetReviews_SkipsEmptyBusinessID(t *testing.T) {
	rawItems := []any{
		map[string]any{
			"businessId":   "",
			"reviewId":     "r1",
			"businessName": "NoID",
			"text":         "should be skipped",
			"_additional":  map[string]any{"certainty": 0.9},
		},
		map[string]any{
			"businessId":   "biz1",
			"reviewId":     "r2",
			"businessName": "HasID",
			"text":         "included",
			"_additional":  map[string]any{"certainty": 0.8},
		},
	}
	data := map[string]models.JSONObject{
		"Get": map[string]any{
			collectionName: rawItems,
		},
	}
	result := getReviews(data, 10)
	if len(result.BusinessReviews) != 1 {
		t.Fatalf("got %d reviews, want 1 (empty businessId should be skipped)", len(result.BusinessReviews))
	}
}

// --- Mocked Client Tests ---

func TestClient_EnsureSchema(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/v1/schema/"+collectionName) {
			w.WriteHeader(http.StatusNotFound) // assume not exists
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v1/schema" {
			created = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	if err := client.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}
	if !created {
		t.Error("expected schema to be created")
	}
}

func TestClient_BatchUpsert(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/batch/objects" {
			var body struct {
				Objects []any `json:"objects"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			count = len(body.Objects)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	items := []IndexItem{
		{Review: schemas.Review{ReviewID: "r1"}},
		{Review: schemas.Review{ReviewID: "r2"}},
	}
	if err := client.BatchUpsert(context.Background(), items); err != nil {
		t.Fatalf("BatchUpsert failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 items, got %d", count)
	}
}

func TestClient_GetBusinesses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			data := makeData([]struct {
				businessID   string
				businessName string
				certainty    float64
			}{
				{"biz1", "Pizza", 0.9},
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	res, err := client.GetBusinesses(context.Background(), "pizza", 1, SearchFilter{})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}
	if len(res.Businesses) != 1 || res.Businesses[0].ID != "biz1" {
		t.Errorf("got %v", res)
	}
}

func TestClient_SearchFodmap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			data := map[string]any{
				"Get": map[string]any{
					fodmapCollectionName: []any{
						map[string]any{
							"ingredient":  "garlic",
							"level":       "high",
							"_additional": map[string]any{"certainty": 0.99},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	res, cert, err := client.SearchFodmap(context.Background(), "garlic")
	if err != nil {
		t.Fatalf("SearchFodmap failed: %v", err)
	}
	if res.Ingredient != "garlic" || cert != 0.99 {
		t.Errorf("got %+v, cert %f", res, cert)
	}
}

// --- Hybrid query tests ---

func TestClient_GetBusinesses_HybridQuery(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			data := makeData([]struct {
				businessID   string
				businessName string
				certainty    float64
			}{{"biz1", "Pizza", 0.9}})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	_, err := client.GetBusinesses(context.Background(), "gluten free", 5, SearchFilter{Alpha: 0.75})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}
	if !strings.Contains(body, "hybrid:") {
		t.Errorf("expected hybrid query in GraphQL body, got: %s", body)
	}
	if strings.Contains(body, "nearText:") {
		t.Errorf("expected no nearText query when Alpha>0, got: %s", body)
	}
}

func TestClient_GetBusinesses_HybridAlphaValue(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": makeData(nil)})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	_, _ = client.GetBusinesses(context.Background(), "ramen", 5, SearchFilter{Alpha: 0.6})
	if !strings.Contains(body, "0.6") {
		t.Errorf("expected alpha value 0.6 in GraphQL body, got: %s", body)
	}
}

func TestClient_GetBusinesses_NearTextFallback(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			data := makeData([]struct {
				businessID   string
				businessName string
				certainty    float64
			}{{"biz1", "Pizza", 0.9}})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	// Alpha=0 (default zero value) → should use nearText
	_, err := client.GetBusinesses(context.Background(), "pizza", 1, SearchFilter{})
	if err != nil {
		t.Fatalf("GetBusinesses failed: %v", err)
	}
	if strings.Contains(body, "hybrid:") {
		t.Errorf("expected nearText query when Alpha=0, got hybrid in body: %s", body)
	}
	if !strings.Contains(body, "nearText:") {
		t.Errorf("expected nearText query when Alpha=0, body: %s", body)
	}
}

func TestClient_GetReviews_HybridQuery(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/graphql" {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			data := makeReviewData([]struct {
				businessID   string
				reviewID     string
				businessName string
				city         string
				state        string
				text         string
				certainty    float64
			}{{"biz1", "r1", "Pizza", "NYC", "NY", "great pizza", 0.9}})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	_, err := client.GetReviews(context.Background(), "gluten free", 5, SearchFilter{Alpha: 0.75})
	if err != nil {
		t.Fatalf("GetReviews failed: %v", err)
	}
	if !strings.Contains(body, "hybrid:") {
		t.Errorf("expected hybrid query in GraphQL body, got: %s", body)
	}
}

func TestClient_BatchUpsertFodmap(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/batch/objects" {
			var body struct {
				Objects []any `json:"objects"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			count = len(body.Objects)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	client, _ := NewClient(host, "http", "")

	items := map[string]data.FodmapEntry{
		"garlic": {Level: "high"},
		"onion":  {Level: "high"},
	}
	if err := client.BatchUpsertFodmap(context.Background(), items); err != nil {
		t.Fatalf("BatchUpsertFodmap failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 items, got %d", count)
	}
}
