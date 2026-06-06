package scraper

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ConvertHTMLToMarkdown ---

func TestConvertHTMLToMarkdown_PreservesMenuStructure(t *testing.T) {
	html := `<html><head><title>Test</title></head><body>
<nav><a href="/">Home</a><a href="/about">About</a></nav>
<h1>Joe's Pizza</h1>
<h2>Appetizers</h2>
<ul><li>Bruschetta - tomato, basil, garlic</li><li>Mozzarella sticks</li></ul>
<h2>Mains</h2>
<ul><li>Margherita Pizza - tomato sauce, mozzarella</li></ul>
<footer>123 Main St</footer>
</body></html>`

	md, err := ConvertHTMLToMarkdown(strings.NewReader(html), "text/html")
	require.NoError(t, err)

	assert.Contains(t, md, "Joe's Pizza")
	assert.Contains(t, md, "Appetizers")
	assert.Contains(t, md, "Bruschetta")
	assert.Contains(t, md, "Margherita Pizza")
	// nav and footer should be stripped
	assert.NotContains(t, md, "Home")
	assert.NotContains(t, md, "123 Main St")
}

func TestConvertHTMLToMarkdown_HeadingLevels(t *testing.T) {
	html := `<html><body><h1>Restaurant</h1><h2>Section</h2><h3>Sub</h3></body></html>`
	md, err := ConvertHTMLToMarkdown(strings.NewReader(html), "text/html")
	require.NoError(t, err)
	assert.Contains(t, md, "# Restaurant")
	assert.Contains(t, md, "## Section")
	assert.Contains(t, md, "### Sub")
}

// --- isTooNoisy ---

func TestIsTooNoisy_NoisyInput(t *testing.T) {
	// Mostly short nav links
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "Home")
	}
	for i := 0; i < 5; i++ {
		lines = append(lines, "This is a longer menu description with many words")
	}
	md := strings.Join(lines, "\n")
	assert.True(t, isTooNoisy(md))
}

func TestIsTooNoisy_CleanInput(t *testing.T) {
	lines := []string{
		"## Appetizers",
		"Bruschetta - fresh tomatoes, basil, olive oil on toasted bread",
		"Calamari - lightly breaded and fried squid served with marinara",
		"## Mains",
		"Margherita Pizza - san marzano tomatoes, fresh mozzarella, basil",
		"Carbonara - spaghetti, guanciale, eggs, pecorino, black pepper",
	}
	md := strings.Join(lines, "\n")
	assert.False(t, isTooNoisy(md))
}

// --- truncateText ---

func TestTruncateText_ShortInput(t *testing.T) {
	assert.Equal(t, "hello", truncateText("hello", 100))
}

func TestTruncateText_LongInput(t *testing.T) {
	s := strings.Repeat("a", 200)
	result := truncateText(s, 100)
	assert.Equal(t, 100, len([]rune(result)))
}

// --- isPrivateHost ---

func TestIsPrivateHost(t *testing.T) {
	assert.True(t, isPrivateHost("localhost"))
	assert.True(t, isPrivateHost("127.0.0.1"))
	assert.True(t, isPrivateHost("192.168.1.1"))
	assert.True(t, isPrivateHost("10.0.0.1"))
	assert.True(t, isPrivateHost("169.254.169.254"))
	assert.True(t, isPrivateHost("172.16.0.1"))
	assert.False(t, isPrivateHost("8.8.8.8"))
	assert.False(t, isPrivateHost("example.com"))
}

// --- ValidateAPIURL ---

func TestValidateAPIURL_SameHost(t *testing.T) {
	err := ValidateAPIURL("https://example.com/api/menu", "example.com")
	assert.NoError(t, err)
}

func TestValidateAPIURL_DifferentHost(t *testing.T) {
	err := ValidateAPIURL("https://evil.com/api/menu", "example.com")
	assert.Error(t, err)
}

func TestValidateAPIURL_PrivateIP(t *testing.T) {
	err := ValidateAPIURL("http://192.168.1.1/api", "192.168.1.1")
	assert.Error(t, err)
}

func TestValidateAPIURL_MetadataService(t *testing.T) {
	err := ValidateAPIURL("http://169.254.169.254/latest/meta-data/", "169.254.169.254")
	assert.Error(t, err)
}

func TestValidateAPIURL_Localhost(t *testing.T) {
	err := ValidateAPIURL("http://localhost:9999/admin", "localhost:9999")
	assert.Error(t, err)
}

// --- BusinessID ---

func TestBusinessID_SameHostDifferentPaths(t *testing.T) {
	id1 := BusinessID("https://example.com/menu/lunch")
	id2 := BusinessID("https://example.com/menu/dinner")
	assert.Equal(t, id1, id2, "same host should produce same business ID")
}

func TestBusinessID_DifferentHosts(t *testing.T) {
	id1 := BusinessID("https://joes-pizza.com/menu")
	id2 := BusinessID("https://marios-pasta.com/menu")
	assert.NotEqual(t, id1, id2)
}

// --- robots.txt ---

func TestRobotsTxt_Disallowed(t *testing.T) {
	err := parseRobotsTxt("User-agent: *\nDisallow: /menu", "fodmap-detector/0.1", "/menu/lunch")
	assert.Error(t, err)
}

func TestRobotsTxt_Allowed(t *testing.T) {
	err := parseRobotsTxt("User-agent: *\nDisallow: /admin", "fodmap-detector/0.1", "/menu/lunch")
	assert.NoError(t, err)
}

func TestRobotsTxt_Empty(t *testing.T) {
	err := parseRobotsTxt("", "fodmap-detector/0.1", "/anything")
	assert.NoError(t, err)
}

// --- HTTPFetcher (via httptest) ---

func TestHTTPFetcher_200OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>hello</body></html>"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(false)
	res, err := f.Fetch(context.Background(), srv.URL+"/page")
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	assert.Contains(t, res.ContentType, "text/html")
}

func TestHTTPFetcher_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(false)
	_, err := f.Fetch(context.Background(), srv.URL+"/page")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestHTTPFetcher_RobotsDisallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /secret"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(false)
	_, err := f.Fetch(context.Background(), srv.URL+"/secret/stuff")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "robots.txt")
}

func TestHTTPFetcher_IgnoreRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /secret"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(true) // ignore robots
	res, err := f.Fetch(context.Background(), srv.URL+"/secret/stuff")
	require.NoError(t, err)
	_ = res.Body.Close()
}

// --- ExtractJSONLD ---

func TestExtractJSONLD_MenuType(t *testing.T) {
	htmlBody := `<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Menu",
  "hasMenuSection": [{
    "name": "Appetizers",
    "hasMenuItem": [
      {"@type": "MenuItem", "name": "Bruschetta", "description": "With tomato and basil"},
      {"@type": "MenuItem", "name": "Calamari"}
    ]
  }]
}
</script>
</head><body></body></html>`

	items, meta, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.True(t, ok)
	assert.Len(t, items, 2)
	assert.Equal(t, "Bruschetta", items[0].DishName)
	assert.True(t, items[0].HasFullIngredients)
	assert.Equal(t, "Calamari", items[1].DishName)
	assert.False(t, items[1].HasFullIngredients)
	_ = meta
}

func TestExtractJSONLD_RestaurantWithMenu(t *testing.T) {
	htmlBody := `<html><head>
<script type="application/ld+json">
{
  "@type": "Restaurant",
  "name": "Joe's Pizza",
  "address": {"addressLocality": "New York", "addressRegion": "NY"},
  "hasMenu": {
    "@type": "Menu",
    "hasMenuSection": [{
      "hasMenuItem": [
        {"@type": "MenuItem", "name": "Margherita", "description": "Tomato, mozzarella"}
      ]
    }]
  }
}
</script>
</head><body></body></html>`

	items, meta, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.True(t, ok)
	assert.Equal(t, "Joe's Pizza", meta.RestaurantName)
	assert.Equal(t, "New York", meta.City)
	assert.Equal(t, "NY", meta.State)
	assert.Len(t, items, 1)
	assert.Equal(t, "Margherita", items[0].DishName)
}

func TestExtractJSONLD_RestaurantNoMenu_HarvestsMeta(t *testing.T) {
	htmlBody := `<html><head>
<script type="application/ld+json">
{
  "@type": "Restaurant",
  "name": "Mario's",
  "address": {"addressLocality": "Chicago", "addressRegion": "IL"}
}
</script>
</head><body></body></html>`

	items, meta, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.False(t, ok, "no menu items, should return ok=false")
	assert.Equal(t, "Mario's", meta.RestaurantName)
	assert.Equal(t, "Chicago", meta.City)
	assert.Empty(t, items)
}

func TestExtractJSONLD_NoScript(t *testing.T) {
	htmlBody := `<html><body><p>Just a page</p></body></html>`
	items, _, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.False(t, ok)
	assert.Empty(t, items)
}

func TestExtractJSONLD_GraphArray(t *testing.T) {
	htmlBody := `<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@graph": [
    {"@type": "WebSite", "name": "Site"},
    {
      "@type": "Restaurant",
      "name": "Graph Restaurant",
      "hasMenu": {
        "hasMenuSection": [{
          "hasMenuItem": [{"@type": "MenuItem", "name": "Pasta", "description": "With sauce"}]
        }]
      }
    }
  ]
}
</script>
</head><body></body></html>`

	items, meta, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.True(t, ok)
	assert.Equal(t, "Graph Restaurant", meta.RestaurantName)
	assert.Len(t, items, 1)
	assert.Equal(t, "Pasta", items[0].DishName)
}

// --- stub Extractor for pipeline tests ---

type stubExtractor struct {
	result MenuExtractionResult
	err    error
}

func (s *stubExtractor) Extract(_ context.Context, _ string) (MenuExtractionResult, error) {
	return s.result, s.err
}

// --- stub Fetcher ---

type stubFetcher struct {
	body        string
	contentType string
	err         error
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (FetchResult, error) {
	if s.err != nil {
		return FetchResult{}, s.err
	}
	return FetchResult{
		Body:        io.NopCloser(strings.NewReader(s.body)),
		ContentType: s.contentType,
	}, nil
}

// --- stub-based pipeline tests ---

func TestStubFetcher_ReturnsBody(t *testing.T) {
	f := &stubFetcher{body: "<html><body>hello</body></html>", contentType: "text/html"}
	res, err := f.Fetch(context.Background(), "http://example.com")
	require.NoError(t, err)
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	assert.Contains(t, string(data), "hello")
}

func TestStubExtractor_ReturnsResult(t *testing.T) {
	want := MenuExtractionResult{
		RestaurantName: "Test Restaurant",
		Items: []MenuEntry{
			{DishName: "Pizza", Description: "tomato sauce, cheese", HasFullIngredients: true},
		},
	}
	ex := &stubExtractor{result: want}
	got, err := ex.Extract(context.Background(), "some text")
	require.NoError(t, err)
	assert.Equal(t, want.RestaurantName, got.RestaurantName)
	assert.Len(t, got.Items, 1)
	assert.True(t, got.Items[0].HasFullIngredients)
}
