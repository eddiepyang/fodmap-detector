package pipeline

import (
	"context"
	"io"
	"strings"
	"testing"

	"fodmap/scraper"
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
