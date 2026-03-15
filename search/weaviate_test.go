package search

import (
	"testing"

	"github.com/weaviate/weaviate/entities/models"
)

// makeData constructs the shape of data returned by the Weaviate GraphQL response.
// items is a slice of {businessId, certainty} pairs.
func makeData(items []struct {
	businessID string
	certainty  float64
}) map[string]models.JSONObject {
	rawItems := make([]interface{}, len(items))
	for i, item := range items {
		rawItems[i] = map[string]interface{}{
			"businessId": item.businessID,
			"_additional": map[string]interface{}{
				"certainty": item.certainty,
			},
		}
	}
	return map[string]models.JSONObject{
		"Get": map[string]interface{}{
			collectionName: rawItems,
		},
	}
}

// --- aggregateTopK tests ---

func TestAggregateTopK_Empty(t *testing.T) {
	result := aggregateTopK(map[string]models.JSONObject{}, 5)
	if len(result.BusinessIDs) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.BusinessIDs))
	}
}

func TestAggregateTopK_SingleBusiness(t *testing.T) {
	data := makeData([]struct {
		businessID string
		certainty  float64
	}{
		{"biz1", 0.9},
	})
	result := aggregateTopK(data, 5)
	if len(result.BusinessIDs) != 1 {
		t.Fatalf("got %d results, want 1", len(result.BusinessIDs))
	}
	if result.BusinessIDs[0] != "biz1" {
		t.Errorf("got %q, want %q", result.BusinessIDs[0], "biz1")
	}
}

func TestAggregateTopK_RankedByScore(t *testing.T) {
	data := makeData([]struct {
		businessID string
		certainty  float64
	}{
		{"low", 0.5},
		{"high", 0.9},
		{"mid", 0.7},
	})
	result := aggregateTopK(data, 3)
	if len(result.BusinessIDs) != 3 {
		t.Fatalf("got %d results, want 3", len(result.BusinessIDs))
	}
	if result.BusinessIDs[0] != "high" {
		t.Errorf("first result = %q, want %q", result.BusinessIDs[0], "high")
	}
	if result.BusinessIDs[2] != "low" {
		t.Errorf("last result = %q, want %q", result.BusinessIDs[2], "low")
	}
}

func TestAggregateTopK_LimitTruncates(t *testing.T) {
	data := makeData([]struct {
		businessID string
		certainty  float64
	}{
		{"biz1", 0.9},
		{"biz2", 0.8},
		{"biz3", 0.7},
	})
	result := aggregateTopK(data, 2)
	if len(result.BusinessIDs) != 2 {
		t.Errorf("got %d results, want 2 (limit=2)", len(result.BusinessIDs))
	}
}

func TestAggregateTopK_TopKAveraging(t *testing.T) {
	// biz1 has 6 reviews; only the top topKReviews(=5) should count.
	// Top 5 scores: 0.9, 0.8, 0.7, 0.6, 0.5 → avg = 0.70
	// biz2 has 1 review with score 0.72 → avg = 0.72
	// biz2 should rank higher.
	items := []struct {
		businessID string
		certainty  float64
	}{
		{"biz1", 0.9},
		{"biz1", 0.8},
		{"biz1", 0.7},
		{"biz1", 0.6},
		{"biz1", 0.5},
		{"biz1", 0.1}, // 6th review — should be excluded from top-K
		{"biz2", 0.72},
	}
	data := makeData(items)
	result := aggregateTopK(data, 2)
	if len(result.BusinessIDs) != 2 {
		t.Fatalf("got %d results, want 2", len(result.BusinessIDs))
	}
	if result.BusinessIDs[0] != "biz2" {
		t.Errorf("first result = %q, want biz2 (higher top-K avg)", result.BusinessIDs[0])
	}
}

func TestAggregateTopK_MultipleReviewsSameBusiness(t *testing.T) {
	data := makeData([]struct {
		businessID string
		certainty  float64
	}{
		{"biz1", 0.8},
		{"biz1", 0.6},
		{"biz2", 0.7},
	})
	result := aggregateTopK(data, 5)
	// biz1 avg = (0.8 + 0.6) / 2 = 0.7, biz2 avg = 0.7 — order may vary but both present
	if len(result.BusinessIDs) != 2 {
		t.Errorf("got %d results, want 2", len(result.BusinessIDs))
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

func TestBuildWhereFilter_AllThree(t *testing.T) {
	f := buildWhereFilter(SearchFilter{Category: "pizza", City: "Austin", State: "TX"})
	if f == nil {
		t.Fatal("expected non-nil compound filter")
	}
}
