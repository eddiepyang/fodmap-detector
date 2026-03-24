package search

import (
	"testing"

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
