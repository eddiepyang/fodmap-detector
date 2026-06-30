package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"
)

func TestExtractMenu_JSONLD(t *testing.T) {
	// JSON-LD should be extracted directly without calling the LLM extractor
	jsonldHTML := `
	<html>
	<script type="application/ld+json">
	{
	  "@context": "https://schema.org",
	  "@type": "Restaurant",
	  "name": "Test Restaurant",
	  "hasMenu": {
	    "@type": "Menu",
	    "hasMenuItem": [
	      {
	        "@type": "MenuItem",
	        "name": "Pizza",
	        "description": "Cheese pizza",
	        "offers": {
	          "@type": "Offer",
	          "price": "10.00"
	        }
	      }
	    ]
	  }
	}
	</script>
	</html>`

	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(jsonldHTML)),
			ContentType: "text/html",
		},
	}
	// extractor will not be called for items because JSON-LD handles it
	ex := &stubExtractor{}

	res, _, err := ExtractMenu(context.Background(), "https://example.com", fetcher, ex, false, false, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RestaurantName != "Test Restaurant" {
		t.Errorf("expected Test Restaurant, got %v", res.RestaurantName)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "Pizza" {
		t.Errorf("expected 1 item 'Pizza', got %v", res.Items)
	}
}

// mockExtractor is a spy to verify the extractor was called
type mockExtractor struct {
	called bool
	err    error
}

func (m *mockExtractor) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	m.called = true
	return scraper.MenuExtractionResult{Items: []scraper.MenuEntry{{DishName: "LLM Burger"}}}, m.err
}

func TestExtractMenu_FallbackToExtractor(t *testing.T) {
	plainHTML := `<html><body><p>Menu: Burger $5</p></body></html>`

	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(plainHTML)),
			ContentType: "text/html",
		},
	}

	ex := &mockExtractor{}

	res, _, err := ExtractMenu(context.Background(), "https://example.com", fetcher, ex, false, false, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ex.called {
		t.Error("expected LLM extractor to be called when JSON-LD is absent")
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "LLM Burger" {
		t.Errorf("expected 1 item 'LLM Burger', got %v", res.Items)
	}
}

// ── ToMenuItems ───────────────────────────────────────────────────────────────

type stubEmbedder struct{ err error }

func (e *stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = stubVec768(0.1, 0.2)
	}
	return out, nil
}

func (e *stubEmbedder) EmbedSingle(_ context.Context, _ string) ([]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	return stubVec768(0.1, 0.2), nil
}

func (e *stubEmbedder) Close() error { return nil }

// stubVec768 returns a 768-dim vector matching menu_items.embedding.
func stubVec768(a, b float32) []float32 {
	v := make([]float32, 768)
	v[0] = a
	v[1] = b
	return v
}

func TestToMenuItems_Basic(t *testing.T) {
	result := scraper.MenuExtractionResult{
		RestaurantName: "Test Resto",
		Items: []scraper.MenuEntry{
			{DishName: "Burger", Description: "Juicy", StatedIngredients: []string{"beef", "bun"}},
			{DishName: "Salad"},
		},
	}
	items, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com/menu", &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].DishName != "Burger" {
		t.Errorf("item[0] DishName = %q, want Burger", items[0].DishName)
	}
	if items[0].RestaurantName != "Test Resto" {
		t.Errorf("item[0] RestaurantName = %q, want Test Resto", items[0].RestaurantName)
	}
	if len(items[0].Vector) == 0 {
		t.Error("expected non-empty Vector")
	}
	if items[0].MenuItemID == "" {
		t.Error("expected non-empty MenuItemID")
	}
	// IDs must be stable (deterministic from URL + dish name).
	items2, _ := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com/menu", &stubEmbedder{})
	if items[0].MenuItemID != items2[0].MenuItemID {
		t.Error("MenuItemID must be deterministic")
	}
}

func TestToMenuItems_Empty(t *testing.T) {
	result := scraper.MenuExtractionResult{RestaurantName: "Resto"}
	items, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty result, got %d", len(items))
	}
}

func TestToMenuItems_EmbedError(t *testing.T) {
	result := scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{{DishName: "Pasta"}},
	}
	_, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubEmbedder{err: errors.New("embed failed")})
	if err == nil {
		t.Error("expected error when embedder fails")
	}
}

func TestToMenuItems_SectionFromExtraction(t *testing.T) {
	// The section name from the extracted MenuEntry should be used, not the
	// URL-derived fallback.
	result := scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{
			{DishName: "Soup", Section: "Starters"},
		},
	}
	items, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com/menu", &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].MenuSection != "Starters" {
		t.Errorf("MenuSection = %q, want %q", items[0].MenuSection, "Starters")
	}
}

func TestToMenuItems_SectionFallsBackToURL(t *testing.T) {
	// When the extracted section is empty, the URL-derived section is used.
	result := scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{
			{DishName: "Soup"},
		},
	}
	items, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com/lunch-menu", &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].MenuSection != "lunch menu" {
		t.Errorf("MenuSection = %q, want %q", items[0].MenuSection, "lunch menu")
	}
}

func TestToMenuItems_PriceAndModifiers(t *testing.T) {
	price := 15.0
	modPrice := 2.5
	result := scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{{
			DishName:  "Burger",
			Price:     &price,
			Section:   "Mains",
			Modifiers: []scraper.Modifier{{Name: "Large", Price: &modPrice}},
		}},
	}
	items, err := ToMenuItems(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com/menu", &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].Price == nil || *items[0].Price != 15.0 {
		t.Errorf("Price = %v, want 15.0", items[0].Price)
	}
	if len(items[0].Modifiers) != 1 || items[0].Modifiers[0].Name != "Large" {
		t.Errorf("Modifiers = %+v, want [Large]", items[0].Modifiers)
	}
	if items[0].Modifiers[0].Price == nil || *items[0].Modifiers[0].Price != 2.5 {
		t.Errorf("modifier Price = %v, want 2.5", items[0].Modifiers[0].Price)
	}
}

// compile-time check that stubEmbedder satisfies the interface.
var _ search.Embedder = (*stubEmbedder)(nil)

// ── StoreMenu ─────────────────────────────────────────────────────────────────

type stubMenuStore struct {
	err error
}

func (s *stubMenuStore) EnsureMenuSchema(_ context.Context) error { return nil }
func (s *stubMenuStore) BatchUpsertMenu(_ context.Context, _ []search.MenuItem) error {
	return s.err
}
func (s *stubMenuStore) SearchMenu(_ context.Context, _ string, _ int) ([]search.MenuItem, error) {
	return nil, nil
}

var _ server.MenuStore = (*stubMenuStore)(nil)

func TestStoreMenu_EmptyItems(t *testing.T) {
	result := &scraper.MenuExtractionResult{RestaurantName: "Resto"}
	n, err := StoreMenu(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubMenuStore{}, &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 items stored, got %d", n)
	}
}

func TestStoreMenu_StoresItems(t *testing.T) {
	result := &scraper.MenuExtractionResult{
		RestaurantName: "Test Resto",
		Items:          []scraper.MenuEntry{{DishName: "Pasta"}, {DishName: "Pizza"}},
	}
	n, err := StoreMenu(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubMenuStore{}, &stubEmbedder{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 items stored, got %d", n)
	}
}

func TestStoreMenu_EmbedError(t *testing.T) {
	result := &scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{{DishName: "Pasta"}},
	}
	_, err := StoreMenu(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubMenuStore{}, &stubEmbedder{err: errors.New("embed failed")})
	if err == nil {
		t.Error("expected error when embedder fails")
	}
}

func TestStoreMenu_UpsertError(t *testing.T) {
	result := &scraper.MenuExtractionResult{
		Items: []scraper.MenuEntry{{DishName: "Pasta"}},
	}
	_, err := StoreMenu(context.Background(), result, uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), "https://example.com", &stubMenuStore{err: errors.New("db down")}, &stubEmbedder{})
	if err == nil {
		t.Error("expected error when store fails")
	}
}
