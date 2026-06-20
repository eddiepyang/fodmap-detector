package cli

import (
	"context"
	"errors"
	"testing"

	"fodmap/scraper"
)

// stubEmbedder returns one deterministic vector per input text, or an error.
type stubEmbedder struct {
	err error
}

func (s stubEmbedder) EmbedSingle(_ context.Context, _ string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []float32{0.1, 0.2}, nil
}

func (s stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = []float32{float32(i), float32(i) + 0.5}
	}
	return vectors, nil
}

func (s stubEmbedder) Close() error { return nil }

func sampleResult() scraper.MenuExtractionResult {
	return scraper.MenuExtractionResult{
		RestaurantName: "Testaurant",
		City:           "Austin",
		State:          "TX",
		ScrapedAtUTC:   "2026-06-13T00:00:00Z",
		Items: []scraper.MenuEntry{
			{
				DishName:           "Margherita Pizza",
				Description:        "Classic",
				StatedIngredients:  []string{"tomato", "basil"},
				HasFullIngredients: true,
			},
			{
				DishName: "House Salad",
			},
		},
	}
}

func TestToMenuItems(t *testing.T) {
	rawURL := "https://example.com/menu"
	items, err := toMenuItems(context.Background(), sampleResult(), rawURL, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	first := items[0]
	if first.DishName != "Margherita Pizza" {
		t.Errorf("DishName: got %q", first.DishName)
	}
	if first.RestaurantName != "Testaurant" {
		t.Errorf("RestaurantName: got %q", first.RestaurantName)
	}
	if first.City != "Austin" || first.State != "TX" {
		t.Errorf("location: got %q, %q", first.City, first.State)
	}
	if first.SourceURL != rawURL {
		t.Errorf("SourceURL: got %q", first.SourceURL)
	}
	if !first.HasFullIngredients {
		t.Error("HasFullIngredients: got false, want true")
	}
	if len(first.Vector) == 0 {
		t.Error("Vector not populated")
	}
	if first.MenuItemID == "" {
		t.Error("MenuItemID not set")
	}

	// IDs are deterministic per (businessID + dishName) and must be distinct
	// for distinct dishes.
	if items[0].MenuItemID == items[1].MenuItemID {
		t.Error("expected distinct MenuItemIDs for distinct dishes")
	}
	if items[0].BusinessID != items[1].BusinessID {
		t.Error("expected the same BusinessID for items from the same URL")
	}
}

func TestToMenuItems_Deterministic(t *testing.T) {
	rawURL := "https://example.com/menu"
	a, err := toMenuItems(context.Background(), sampleResult(), rawURL, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems (a): %v", err)
	}
	b, err := toMenuItems(context.Background(), sampleResult(), rawURL, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems (b): %v", err)
	}
	if a[0].MenuItemID != b[0].MenuItemID {
		t.Errorf("MenuItemID not deterministic: %q vs %q", a[0].MenuItemID, b[0].MenuItemID)
	}
}

func TestToMenuItems_EmbedError(t *testing.T) {
	items, err := toMenuItems(context.Background(), sampleResult(), "https://example.com/menu", stubEmbedder{err: errors.New("boom")})
	if err == nil {
		t.Fatal("expected error from embedder, got nil")
	}
	if items != nil {
		t.Errorf("expected nil items on error, got %d", len(items))
	}
}
