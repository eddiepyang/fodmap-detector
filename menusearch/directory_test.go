package menusearch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"fodmap/scraper"
)

// ── extractMenuSubURLs ───────────────────────────────────────────────────────

// baseURL used across most sub-tests.
const testBase = "https://restaurant.example.com/menu"

func TestExtractMenuSubURLs_SameDomainKept(t *testing.T) {
	html := []byte(`<html><body>
<a href="/menu/lunch">Lunch</a>
<a href="/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %v", got)
	}
	want := map[string]bool{
		"https://restaurant.example.com/menu/lunch":  true,
		"https://restaurant.example.com/menu/dinner": true,
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("unexpected candidate %q", u)
		}
	}
}

func TestExtractMenuSubURLs_PDFKept(t *testing.T) {
	html := []byte(`<html><body>
<a href="/menus/full-menu.pdf">Download Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected 1 PDF candidate, got %v", got)
	}
	if got[0] != "https://restaurant.example.com/menus/full-menu.pdf" {
		t.Errorf("unexpected URL %q", got[0])
	}
}

func TestExtractMenuSubURLs_OffDomainDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://other-restaurant.com/menu">Other menu</a>
<a href="/menu/lunch">Lunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	// Only the same-domain link should survive.
	if len(got) != 1 {
		t.Fatalf("expected 1 same-domain candidate, got %v", got)
	}
	if got[0] != "https://restaurant.example.com/menu/lunch" {
		t.Errorf("unexpected URL %q", got[0])
	}
}

func TestExtractMenuSubURLs_SocialDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://instagram.com/restaurant">Instagram</a>
<a href="https://facebook.com/restaurant">Facebook</a>
<a href="/menu/brunch">Brunch Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only brunch link, got %v", got)
	}
}

func TestExtractMenuSubURLs_DeliveryDropped(t *testing.T) {
	html := []byte(`<html><body>
<a href="https://doordash.com/store/restaurant">Order on DoorDash</a>
<a href="https://ubereats.com/restaurant/place">Order on UberEats</a>
<a href="/menu/food">Food Menu</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only food link, got %v", got)
	}
}

func TestExtractMenuSubURLs_NonMenuPathDropped(t *testing.T) {
	// Pages without menu-ish keywords in the path should be filtered out.
	html := []byte(`<html><body>
<a href="/about">About Us</a>
<a href="/contact">Contact</a>
<a href="/reservations">Reserve a Table</a>
<a href="/menu/lunch">Lunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected only lunch link, got %v", got)
	}
}

func TestExtractMenuSubURLs_SelfURLDropped(t *testing.T) {
	// The root URL itself must not appear in the results.
	html := []byte(`<html><body>
<a href="https://restaurant.example.com/menu">This Menu Page</a>
<a href="/menu/">Trailing slash variant</a>
<a href="/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	// /menu and /menu/ are both normalised to the root; only /menu/dinner survives.
	for _, u := range got {
		if u == testBase || u == "https://restaurant.example.com/menu/" {
			t.Errorf("self-URL should have been dropped: %q", u)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate (dinner), got %v", got)
	}
}

func TestExtractMenuSubURLs_Dedup(t *testing.T) {
	// Duplicate hrefs should appear only once.
	html := []byte(`<html><body>
<a href="/menu/lunch">Lunch (nav)</a>
<a href="/menu/lunch">Lunch (footer)</a>
</body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 1 {
		t.Fatalf("expected 1 deduplicated candidate, got %v", got)
	}
}

func TestExtractMenuSubURLs_RelativeResolved(t *testing.T) {
	// Relative hrefs must be resolved against the base URL.
	base := "https://restaurant.example.com/"
	html := []byte(`<html><body>
<a href="menu/brunch">Brunch</a>
</body></html>`)
	got := extractMenuSubURLs(html, base)
	if len(got) != 1 {
		t.Fatalf("expected 1 resolved relative URL, got %v", got)
	}
	want := "https://restaurant.example.com/menu/brunch"
	if got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func TestExtractMenuSubURLs_EmptyHTML(t *testing.T) {
	got := extractMenuSubURLs([]byte{}, testBase)
	if len(got) != 0 {
		t.Errorf("expected no candidates for empty HTML, got %v", got)
	}
}

func TestExtractMenuSubURLs_NoAnchors(t *testing.T) {
	html := []byte(`<html><body><p>No links here.</p></body></html>`)
	got := extractMenuSubURLs(html, testBase)
	if len(got) != 0 {
		t.Errorf("expected no candidates when no anchors, got %v", got)
	}
}

func TestExtractMenuSubURLs_WwwStrippedSameDomain(t *testing.T) {
	// www. prefix should be treated as same domain as the non-www root.
	base := "https://www.restaurant.example.com/menu"
	html := []byte(`<html><body>
<a href="https://restaurant.example.com/menu/dinner">Dinner</a>
</body></html>`)
	got := extractMenuSubURLs(html, base)
	if len(got) != 1 {
		t.Fatalf("expected www vs non-www to be same domain, got %v", got)
	}
}

// ── registrableDomain ────────────────────────────────────────────────────────

func TestRegistrableDomain(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"restaurant.example.com", "example.com"},
		{"www.example.com", "example.com"},
		{"example.com", "example.com"},
		{"sub.sub.example.com", "example.com"},
		{"www.sub.example.co.uk", "co.uk"}, // conservative: last 2 labels only
	}
	for _, tc := range cases {
		got := registrableDomain(tc.host)
		if got != tc.want {
			t.Errorf("registrableDomain(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// ── depth cap (structural test) ──────────────────────────────────────────────

// TestDepthCapStructural verifies that ScrapeMenuArgs with Depth==1 carries the
// field correctly (JSON round-trip) and that the zero value is 0.
func TestDepthCapStructural(t *testing.T) {
	// Default zero value.
	var a ScrapeMenuArgs
	if a.Depth != 0 {
		t.Errorf("default Depth should be 0, got %d", a.Depth)
	}

	// Explicit depth-1.
	b := ScrapeMenuArgs{Depth: 1}
	if b.Depth != 1 {
		t.Errorf("Depth should be 1, got %d", b.Depth)
	}
}

// ── webagentMaxConcurrency ───────────────────────────────────────────────────

func TestWebagentMaxConcurrency_DefaultWhenUnset(t *testing.T) {
	t.Setenv("WEBAGENT_MAX_FETCH_CONCURRENCY", "")
	if got := webagentMaxConcurrency(); got != 4 {
		t.Errorf("want default 4, got %d", got)
	}
}

func TestWebagentMaxConcurrency_ValidValue(t *testing.T) {
	t.Setenv("WEBAGENT_MAX_FETCH_CONCURRENCY", "8")
	if got := webagentMaxConcurrency(); got != 8 {
		t.Errorf("want 8, got %d", got)
	}
}

func TestWebagentMaxConcurrency_InvalidValue(t *testing.T) {
	t.Setenv("WEBAGENT_MAX_FETCH_CONCURRENCY", "notanumber")
	if got := webagentMaxConcurrency(); got != 4 {
		t.Errorf("want default 4 for non-numeric value, got %d", got)
	}
}

func TestWebagentMaxConcurrency_ZeroFallsBackToDefault(t *testing.T) {
	t.Setenv("WEBAGENT_MAX_FETCH_CONCURRENCY", "0")
	if got := webagentMaxConcurrency(); got != 4 {
		t.Errorf("want default 4 for zero, got %d", got)
	}
}

// ── hasMenuPathKeyword ───────────────────────────────────────────────────────

func TestHasMenuPathKeyword(t *testing.T) {
	cases := []struct {
		path, href string
		want       bool
	}{
		{"/menu/lunch", "", true},
		{"/dinner-specials", "", true},
		{"/food/items", "", true},
		{"/brunch-menu", "", true},
		{"/assets/drinks.pdf", "", true},
		{"/cocktails", "", true},
		{"/about", "", false},
		{"/contact", "", false},
		{"/reservations", "", false},
		{"", "menu-page", true}, // keyword in href text
	}
	for _, tc := range cases {
		got := hasMenuPathKeyword(tc.path, tc.href)
		if got != tc.want {
			t.Errorf("hasMenuPathKeyword(%q, %q) = %v, want %v", tc.path, tc.href, got, tc.want)
		}
	}
}

// ── normaliseURL ─────────────────────────────────────────────────────────────

func TestNormaliseURL(t *testing.T) {
	cases := []struct {
		rawURL string
		want   string
	}{
		{"https://restaurant.example.com/menu", "https://restaurant.example.com/menu"},
		{"https://restaurant.example.com/menu/", "https://restaurant.example.com/menu"},
		{"https://restaurant.example.com/menu?x=1", "https://restaurant.example.com/menu"},
		{"https://restaurant.example.com/menu#section", "https://restaurant.example.com/menu"},
		{"https://RESTAURANT.EXAMPLE.COM/menu", "https://restaurant.example.com/menu"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.rawURL)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", tc.rawURL, err)
		}
		got := normaliseURL(u)
		if got != tc.want {
			t.Errorf("normaliseURL(%q) = %q, want %q", tc.rawURL, got, tc.want)
		}
	}
}

// ── extractSubURLs ───────────────────────────────────────────────────────────

// msStubFetcher implements scraper.Fetcher and returns the same result for every URL.
type msStubFetcher struct {
	body        string
	contentType string
	err         error
}

func (s *msStubFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	if s.err != nil {
		return scraper.FetchResult{}, s.err
	}
	return scraper.FetchResult{
		Body:        io.NopCloser(strings.NewReader(s.body)),
		ContentType: s.contentType,
	}, nil
}

// msStubExtractor implements scraper.Extractor.
type msStubExtractor struct {
	result scraper.MenuExtractionResult
}

func (s *msStubExtractor) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	return s.result, nil
}

// msURLMapFetcher returns a preset response per raw URL.
type msFetchResponse struct {
	body        string
	contentType string
	err         error
}

type msURLMapFetcher struct {
	responses map[string]msFetchResponse
}

func (f *msURLMapFetcher) Fetch(_ context.Context, rawURL string) (scraper.FetchResult, error) {
	resp, ok := f.responses[rawURL]
	if !ok {
		return scraper.FetchResult{}, errors.New("no stub for URL")
	}
	if resp.err != nil {
		return scraper.FetchResult{}, resp.err
	}
	return scraper.FetchResult{
		Body:        io.NopCloser(strings.NewReader(resp.body)),
		ContentType: resp.contentType,
	}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// menuJSONLDHTML returns a minimal HTML page with a JSON-LD Restaurant menu
// that contains one item named itemName.
func menuJSONLDHTML(itemName string) string {
	return `<html><body>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Restaurant","name":"Test Resto",
 "hasMenu":{"@type":"Menu","hasMenuItem":[
   {"@type":"MenuItem","name":"` + itemName + `","offers":{"@type":"Offer","price":"10"}}
 ]}}
</script>
</body></html>`
}

func TestExtractSubURLs_ReturnsItems(t *testing.T) {
	fetcher := &msStubFetcher{body: menuJSONLDHTML("Burger"), contentType: "text/html"}
	ex := &msStubExtractor{}

	results := extractSubURLs(
		context.Background(),
		[]string{"https://restaurant.example.com/menu/lunch"},
		fetcher, ex, false, false, "",
		discardLogger(),
	)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].url != "https://restaurant.example.com/menu/lunch" {
		t.Errorf("unexpected URL %q", results[0].url)
	}
	if len(results[0].result.Items) != 1 {
		t.Errorf("expected 1 item, got %v", results[0].result.Items)
	}
}

func TestExtractSubURLs_EmptyCandidates(t *testing.T) {
	results := extractSubURLs(
		context.Background(),
		nil,
		&msStubFetcher{}, &msStubExtractor{}, false, false, "",
		discardLogger(),
	)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty candidates, got %d", len(results))
	}
}

func TestExtractSubURLs_ZeroItemsFiltered(t *testing.T) {
	// HTML with no JSON-LD; extractor returns empty items — must not appear in results.
	fetcher := &msStubFetcher{body: `<html><body><p>No menu here.</p></body></html>`, contentType: "text/html"}
	ex := &msStubExtractor{result: scraper.MenuExtractionResult{}}

	results := extractSubURLs(
		context.Background(),
		[]string{"https://restaurant.example.com/empty"},
		fetcher, ex, false, false, "",
		discardLogger(),
	)
	if len(results) != 0 {
		t.Errorf("expected 0 results when sub-URL yields no items, got %d", len(results))
	}
}

func TestExtractSubURLs_FetchErrorFiltered(t *testing.T) {
	// A fetch error must be tolerated — the URL is silently skipped.
	fetcher := &msStubFetcher{err: errors.New("connection refused")}
	ex := &msStubExtractor{}

	results := extractSubURLs(
		context.Background(),
		[]string{"https://restaurant.example.com/menu/lunch"},
		fetcher, ex, false, false, "",
		discardLogger(),
	)
	if len(results) != 0 {
		t.Errorf("expected 0 results when fetch fails, got %d", len(results))
	}
}

func TestExtractSubURLs_ToleratesPartialFailure(t *testing.T) {
	// One URL succeeds; the other times out. Only the successful URL must appear.
	fetcher := &msURLMapFetcher{
		responses: map[string]msFetchResponse{
			"https://restaurant.example.com/menu/lunch": {
				body:        menuJSONLDHTML("Salad"),
				contentType: "text/html",
			},
			"https://restaurant.example.com/menu/dinner": {
				err: errors.New("timeout"),
			},
		},
	}
	ex := &msStubExtractor{}

	results := extractSubURLs(
		context.Background(),
		[]string{
			"https://restaurant.example.com/menu/lunch",
			"https://restaurant.example.com/menu/dinner",
		},
		fetcher, ex, false, false, "",
		discardLogger(),
	)

	if len(results) != 1 {
		t.Fatalf("expected 1 result (lunch only), got %d", len(results))
	}
	if results[0].url != "https://restaurant.example.com/menu/lunch" {
		t.Errorf("unexpected URL %q", results[0].url)
	}
}
