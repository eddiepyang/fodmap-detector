package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"fodmap/scraper"
	"fodmap/search"
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

// stubVisionExtractor is a test double for scraper.VisionExtractor.
type stubVisionExtractor struct {
	result  scraper.MenuExtractionResult
	payload json.RawMessage
	err     error
	called  bool
}

func (s *stubVisionExtractor) ExtractDocument(_ context.Context, _ []byte, _ string) (scraper.MenuExtractionResult, json.RawMessage, error) {
	s.called = true
	return s.result, s.payload, s.err
}

// stubMenuStore is a test double for server.MenuStore.
type stubMenuStore struct {
	upserted []search.MenuItem
	err      error
}

func (s *stubMenuStore) EnsureMenuSchema(_ context.Context) error { return nil }
func (s *stubMenuStore) BatchUpsertMenu(_ context.Context, items []search.MenuItem) error {
	if s.err != nil {
		return s.err
	}
	s.upserted = append(s.upserted, items...)
	return nil
}
func (s *stubMenuStore) SearchMenu(_ context.Context, _ string, _ int) ([]search.MenuItem, error) {
	return nil, nil
}

// stubFetcher is a test double for scraper.Fetcher.
type stubFetcher struct {
	body        []byte
	contentType string
	err         error
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	if s.err != nil {
		return scraper.FetchResult{}, s.err
	}
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader(s.body)),
		ContentType: s.contentType,
	}, nil
}

// stubExtractor is a test double for scraper.Extractor.
type stubExtractor struct {
	result scraper.MenuExtractionResult
	err    error
	called bool
}

func (s *stubExtractor) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.called = true
	return s.result, s.err
}

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
	items, err := toMenuItems(context.Background(), sampleResult(), rawURL, nil, stubEmbedder{})
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
	a, err := toMenuItems(context.Background(), sampleResult(), rawURL, nil, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems (a): %v", err)
	}
	b, err := toMenuItems(context.Background(), sampleResult(), rawURL, nil, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems (b): %v", err)
	}
	if a[0].MenuItemID != b[0].MenuItemID {
		t.Errorf("MenuItemID not deterministic: %q vs %q", a[0].MenuItemID, b[0].MenuItemID)
	}
}

func TestToMenuItems_EmbedError(t *testing.T) {
	items, err := toMenuItems(context.Background(), sampleResult(), "https://example.com/menu", nil, stubEmbedder{err: errors.New("boom")})
	if err == nil {
		t.Fatal("expected error from embedder, got nil")
	}
	if items != nil {
		t.Errorf("expected nil items on error, got %d", len(items))
	}
}

func TestToMenuItems_WithPayload(t *testing.T) {
	rawURL := "https://example.com/menu"
	payload := json.RawMessage(`{"k":"v"}`)
	items, err := toMenuItems(context.Background(), sampleResult(), rawURL, payload, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected items, got none")
	}
	for i, item := range items {
		if string(item.Payload) != `{"k":"v"}` {
			t.Errorf("item[%d].Payload: got %q, want %q", i, item.Payload, payload)
		}
	}
}

func TestToMenuItems_NilPayload(t *testing.T) {
	rawURL := "https://example.com/menu"
	items, err := toMenuItems(context.Background(), sampleResult(), rawURL, nil, stubEmbedder{})
	if err != nil {
		t.Fatalf("toMenuItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected items, got none")
	}
	if items[0].Payload != nil {
		t.Errorf("expected nil Payload, got %q", items[0].Payload)
	}
}

func TestRunScrapeWith_VisionPath(t *testing.T) {
	result := sampleResult()
	payload := json.RawMessage(`{"schema_revision":"v1"}`)
	visionEx := &stubVisionExtractor{result: result, payload: payload}
	ex := &stubExtractor{}
	store := &stubMenuStore{}
	fetcher := &stubFetcher{
		body:        []byte("%PDF-1.4 not a real pdf"),
		contentType: "application/pdf",
	}

	err := runScrapeWith(
		context.Background(),
		"https://example.com/menu.pdf",
		fetcher,
		ex,
		visionEx,
		store,
		stubEmbedder{},
		false,
		false,
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}

	// Vision extractor must have been called.
	if !visionEx.called {
		t.Error("visionEx.ExtractDocument was not called")
	}

	// Text-path extractor must NOT have been called.
	if ex.called {
		t.Error("ex.Extract was called on the vision branch (must not be)")
	}

	// Store must have received items with the expected payload.
	if len(store.upserted) == 0 {
		t.Fatal("no items were upserted")
	}
	for i, item := range store.upserted {
		if string(item.Payload) != `{"schema_revision":"v1"}` {
			t.Errorf("item[%d].Payload: got %q, want %q", i, item.Payload, payload)
		}
	}
}

func TestRunScrapeWith_VisionPath_Error(t *testing.T) {
	visionEx := &stubVisionExtractor{err: errors.New("service unavailable")}
	ex := &stubExtractor{}
	store := &stubMenuStore{}
	fetcher := &stubFetcher{
		body:        []byte("%PDF-1.4 not a real pdf"),
		contentType: "application/pdf",
	}

	err := runScrapeWith(
		context.Background(),
		"https://example.com/menu.pdf",
		fetcher,
		ex,
		visionEx,
		store,
		stubEmbedder{},
		false,
		false,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "vision extraction") {
		t.Errorf("error should contain %q, got %q", "vision extraction", err.Error())
	}
}

func TestRunScrapeWith_NilVisionEx_ScanError(t *testing.T) {
	ex := &stubExtractor{}
	store := &stubMenuStore{}
	// Send bytes that are not a valid PDF; ExtractPDFText will return ErrNeedVision.
	fetcher := &stubFetcher{
		body:        []byte("not a pdf at all"),
		contentType: "application/pdf",
	}

	err := runScrapeWith(
		context.Background(),
		"https://example.com/menu.pdf",
		fetcher,
		ex,
		nil, // no vision extractor
		store,
		stubEmbedder{},
		false,
		false,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no usable text layer") {
		t.Errorf("error should contain %q, got %q", "no usable text layer", err.Error())
	}
}
