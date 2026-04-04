package search

import (
	"testing"
)

func TestBm25Score_ExactMatch(t *testing.T) {
	score := bm25Score("gluten free pizza", "gluten free pizza is great here")
	if score <= 0 {
		t.Errorf("expected positive score for exact match, got %f", score)
	}
}

func TestBm25Score_NoOverlap(t *testing.T) {
	score := bm25Score("gluten free", "excellent sushi and sake")
	if score != 0 {
		t.Errorf("expected 0 for no overlap, got %f", score)
	}
}

func TestBm25Score_PartialMatch(t *testing.T) {
	full := bm25Score("gluten free pizza", "gluten free pizza pasta")
	partial := bm25Score("gluten free pizza", "gluten free sushi")
	none := bm25Score("gluten free pizza", "excellent sushi")
	if !(full > partial && partial > none) {
		t.Errorf("expected full(%f) > partial(%f) > none(%f)", full, partial, none)
	}
}

func TestBm25Score_CaseInsensitive(t *testing.T) {
	lower := bm25Score("pizza", "great pizza here")
	upper := bm25Score("Pizza", "great pizza here")
	if lower != upper {
		t.Errorf("bm25Score should be case-insensitive: lower=%f upper=%f", lower, upper)
	}
}

func TestBm25Score_EmptyQuery(t *testing.T) {
	score := bm25Score("", "great pizza here")
	if score != 0 {
		t.Errorf("expected 0 for empty query, got %f", score)
	}
}
