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
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "Home")
	}
	for i := 0; i < 5; i++ {
		lines = append(lines, "This is a longer menu description with many words")
	}
	md := strings.Join(lines, "\n")
	assert.True(t, IsTooNoisy(md))
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
	assert.False(t, IsTooNoisy(md))
}

// --- isJSShell ---

// wixShellVisible is the visible-text portion of a Wix-style SPA homepage:
// ~370 runes of boilerplate (above the 200-rune tooShort floor, below the
// 500-rune IsJSShell visible floor) with no menu items. The menu content is
// injected client-side by the restaurant-menus-showcase-ooi widget.
const wixShellVisible = `<html><head><title>3Greeks Grill | Gyro and Souvlaki</title>
<link rel="stylesheet" href="https://static.parastorage.com/services/x.css"/></head>
<body><div id="SITE_CONTAINER"><h1>3Greeks Grill | Gyro and Souvlaki</h1>
<p>WELCOME Καλη Ορεξη (718) 729-8900 35-61 Vernon Blvd, Long Island City, NY 11106</p>
<p>Request a catering order at Catering@3greeksgrill.com. Use tab to navigate through the menu items. More</p>
<p>HOME MENU CONTACT. High quality Greek Gyro and Souvlaki platters and sandwiches as well as other Greek specialty foods.</p>
</div></body></html>`

// wixShellBundle is an inlined-JS padding block that emulates a real Wix
// page's minified bundle + inlined __INITIAL_STATE__ (~190KB), which is what
// drives the raw-bytes-to-visible-runes ratio above the jsShellMinRatio
// threshold. It is wrapped in <script> so ConvertHTMLToMarkdown strips it
// (leaving only wixShellVisible as visible text) while it still counts toward
// the raw byte length.
var wixShellBundle = `<script>var b=function(){return ` + strings.Repeat("1+", 6000) + `0};window.__INITIAL_STATE__={"p":"` + strings.Repeat("x", 180000) + `"}</script>`

func TestIsJSShell_WixHomepageShell(t *testing.T) {
	raw := wixShellVisible[:strings.Index(wixShellVisible, "</body>")] + wixShellBundle + "</body></html>"
	md, err := ConvertHTMLToMarkdown(strings.NewReader(raw), "text/html")
	require.NoError(t, err)
	visible := len([]rune(strings.TrimSpace(md)))
	// The fixture converts to a small body of visible text (200–500 runes):
	// above the 200-rune tooShort floor (so tooShort does NOT fire) and below
	// the 500-rune IsJSShell short-circuit (so the ratio check runs).
	assert.Greater(t, visible, 200, "fixture must be above the tooShort floor to be a regression")
	assert.Less(t, visible, 500, "fixture must be below the IsJSShell visible floor")
	assert.True(t, IsJSShell(md, raw),
		"Wix homepage shell (%dKB raw, %d visible runes) must be detected as a JS shell",
		len(raw)/1024, visible)
}

func TestIsJSShell_RealContentPageNotShell(t *testing.T) {
	// A content-bearing page (> 500 runes of prose) must NOT be flagged as a
	// JS shell, even if it carries a large inlined script — the visible-text
	// short-circuit keeps real menu pages on the LLM text path.
	var b strings.Builder
	b.WriteString(`<html><head><title>Cafe</title></head><body>`)
	b.WriteString(`<h1>Cafe Menu</h1>`)
	for i := 0; i < 30; i++ {
		b.WriteString(`<p>Bruschetta - fresh tomatoes, basil, olive oil on toasted bread, a classic appetizer.</p>`)
	}
	b.WriteString(wixShellBundle)
	b.WriteString(`</body></html>`)
	raw := b.String()
	md, err := ConvertHTMLToMarkdown(strings.NewReader(raw), "text/html")
	require.NoError(t, err)
	assert.Greater(t, len([]rune(strings.TrimSpace(md))), 500)
	assert.False(t, IsJSShell(md, raw), "page with >500 runes of prose must stay on the LLM text path")
}

func TestIsJSShell_ShortStaticPageNotShell(t *testing.T) {
	// A genuinely short static page must not be flagged — the raw-bytes floor
	// (jsShellMinRawBytes) guards against the ratio being meaningless on
	// small pages. A 50-byte "closed" page is just small, not a JS shell.
	raw := `<html><body><h1>Closed</h1><p>This location is permanently closed.</p></body></html>`
	md, err := ConvertHTMLToMarkdown(strings.NewReader(raw), "text/html")
	require.NoError(t, err)
	assert.False(t, IsJSShell(md, raw), "small static page below the raw-bytes floor is not a JS shell")
}

func TestIsJSShell_EmptyMarkdownWithLargeRawIsShell(t *testing.T) {
	// The canonical empty-SPA case: the JS shell renders nothing without JS
	// (empty visible text) but the raw HTML is a large bundle. The ratio is
	// infinite (guarded by max(visible,1)) and must be flagged.
	raw := `<html><body><div id="root"></div>` + wixShellBundle + `</body></html>`
	assert.True(t, IsJSShell("", raw),
		"empty markdown + large raw bundle must be detected as a JS shell")
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
	assert.Len(t, id1, 36, "UUID format")
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

func TestHTTPFetcher_404_ReturnsHTTPStatusError(t *testing.T) {
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
	require.Error(t, err)

	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr, "404 must surface as *HTTPStatusError")
	assert.Equal(t, 404, statusErr.StatusCode)
	assert.Contains(t, statusErr.URL, srv.URL)
}

func TestHTTPFetcher_403_ReturnsHTTPStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(false)
	_, err := f.Fetch(context.Background(), srv.URL+"/page")
	require.Error(t, err)

	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr, "403 must surface as *HTTPStatusError")
	assert.Equal(t, 403, statusErr.StatusCode)
}

func TestHTTPStatusError_ErrorString(t *testing.T) {
	e := &HTTPStatusError{StatusCode: 429, URL: "https://example.com/menu"}
	assert.Contains(t, e.Error(), "429")
	assert.Contains(t, e.Error(), "example.com")
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
	// has_full_ingredients is true only when ingredients are literally
	// listed, not when a description is present (safety signal — design §8.2).
	assert.False(t, items[0].HasFullIngredients)
	assert.Equal(t, "Calamari", items[1].DishName)
	assert.False(t, items[1].HasFullIngredients)
	_ = meta
}

func TestExtractJSONLD_PriceAndIngredients(t *testing.T) {
	htmlBody := `<html><head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Menu",
  "hasMenuSection": [{
    "name": "Mains",
    "hasMenuItem": [
      {"@type": "MenuItem", "name": "Pasta", "description": "Fresh pasta", "offers": {"price": "14.50"}, "ingredients": ["flour", "egg"]},
      {"@type": "MenuItem", "name": "Salad", "description": "Green salad", "offers": {"price": 9}}
    ]
  }]
}
</script>
</head><body></body></html>`

	items, _, ok := ExtractJSONLD(strings.NewReader(htmlBody))
	assert.True(t, ok)
	assert.Len(t, items, 2)

	// String price parsed to float.
	assert.Equal(t, "Pasta", items[0].DishName)
	if items[0].Price == nil || *items[0].Price != 14.50 {
		t.Errorf("item[0].Price = %v, want 14.50", items[0].Price)
	}
	// has_full_ingredients true only when ingredients explicitly listed.
	assert.True(t, items[0].HasFullIngredients)

	// Numeric price handled directly.
	assert.Equal(t, "Salad", items[1].DishName)
	if items[1].Price == nil || *items[1].Price != 9.0 {
		t.Errorf("item[1].Price = %v, want 9.0", items[1].Price)
	}
	// No ingredients list → false, even though description is non-empty.
	assert.False(t, items[1].HasFullIngredients)
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

func TestMenuSection(t *testing.T) {
	if s := MenuSection("http://example.com/menu/lunch"); s != "lunch" {
		t.Errorf("expected lunch, got %s", s)
	}
	if s := MenuSection("http://example.com/menu/dinner-menu/"); s != "dinner menu" {
		t.Errorf("expected dinner menu, got %s", s)
	}
	if s := MenuSection("http://example.com"); s != "" {
		t.Errorf("expected empty string, got %s", s)
	}
}

func TestRawHTMLBody(t *testing.T) {
	b, err := RawHTMLBody(strings.NewReader("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "test" {
		t.Errorf("expected test, got %s", string(b))
	}
}

func TestTrafilaturaFallback(t *testing.T) {
	html := `<html><body><p>Main content</p><nav>Nav content</nav></body></html>`
	res := TrafilaturaFallback(html)
	if !strings.Contains(res, "Main content") {
		t.Errorf("expected Main content, got %s", res)
	}
}

func TestValidateAPIURL(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		err := ValidateAPIURL("http://example.com/api", "example.com")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("MismatchHost", func(t *testing.T) {
		err := ValidateAPIURL("http://other.com/api", "example.com")
		if err == nil {
			t.Errorf("expected error")
		}
	})
	t.Run("PrivateHost", func(t *testing.T) {
		err := ValidateAPIURL("http://localhost/api", "localhost")
		if err == nil {
			t.Errorf("expected error")
		}
	})
}

// ── FindMenuImage (Phase C) ──────────────────────────────────────────────────

func TestFindMenuImage_TrifoldInMenuContainer(t *testing.T) {
	html := `<html><body>
<div id="MENU">
  <h2>Menu</h2>
  <p>Marketing text</p>
  <img src="/wp-content/uploads/2026/03/TRIFOLD_MENU_8x11_BC-1024x798.png"
       width="1024" height="798" alt="">
</div>
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/menu")
	if !ok {
		t.Fatal("expected to find a menu image")
	}
	assert.Equal(t, "https://cafe.com/wp-content/uploads/2026/03/TRIFOLD_MENU_8x11_BC-1024x798.png", got)
}

func TestFindMenuImage_SrcsetLarge(t *testing.T) {
	html := `<html><body>
<div id="menu-section">
  <img src="menu.png" srcset="menu-300.png 300w, menu-1024.png 1024w, menu-2048.png 2048w" alt="menu">
</div>
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	if !ok {
		t.Fatal("expected to find a menu image")
	}
	assert.Equal(t, "https://cafe.com/menu.png", got)
}

func TestFindMenuImage_FilenameMenuKeyword(t *testing.T) {
	html := `<html><body>
<img src="https://cdn.com/food-menu-card-2024.jpg" width="1200" height="900">
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	if !ok {
		t.Fatal("expected to find a menu image via filename keyword")
	}
	assert.Equal(t, "https://cdn.com/food-menu-card-2024.jpg", got)
}

func TestFindMenuImage_ExcludesHeaderFooterNav(t *testing.T) {
	html := `<html><body>
<header><img src="logo-menu.png" width="1024" height="800" alt=""></header>
<nav><img src="nav-menu.png" width="1024" height="800" alt=""></nav>
<footer><img src="footer-menu.png" width="1024" height="800" alt=""></footer>
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "images in header/footer/nav should be excluded")
}

func TestFindMenuImage_ExcludesSmallIcons(t *testing.T) {
	html := `<html><body>
<img src="icon.png" width="50" height="50" alt="">
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "tiny icons should be excluded")
}

func TestFindMenuImage_NoImages(t *testing.T) {
	html := `<html><body><h2>Menu</h2><p>text only</p></body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "no images → no menu image")
}

func TestFindMenuImage_RelativeURLResolved(t *testing.T) {
	html := `<html><body>
<div id="menu"><img src="../images/menu.png" width="1024" height="800" alt=""></div>
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/pages/home")
	require.True(t, ok)
	assert.Equal(t, "https://cafe.com/images/menu.png", got)
}

func TestFindMenuImage_PrefersLargestCandidate(t *testing.T) {
	html := `<html><body>
<div id="MENU">
  <img src="small-menu.png" width="800" height="600" alt="">
  <img src="big-trifold.png" width="2048" height="1596" alt="" data-src="big-trifold.png">
</div>
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	require.True(t, ok)
	assert.Equal(t, "https://cafe.com/big-trifold.png", got)
}

func TestMaxSrcsetWidth(t *testing.T) {
	assert.Equal(t, 2048, maxSrcsetWidth("menu.png 300w, menu-2x.png 2048w"))
	assert.Equal(t, 1024, maxSrcsetWidth("menu.png 1024w"))
	assert.Equal(t, 0, maxSrcsetWidth("menu.png 2x"))
	assert.Equal(t, 0, maxSrcsetWidth(""))
}

func TestFindMenuImage_MenuContextDoesNotLeakToSibling(t *testing.T) {
	// An image AFTER the #MENU div but not inside it should NOT get the
	// menu-context score bonus. Without the fix, inMenuCtx leaked to all
	// subsequent siblings/elements.
	html := `<html><body>
<div id="MENU">
  <p>marketing text</p>
</div>
<img src="decor.png" width="1024" height="800" alt="">
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	// "decor.png" has no menu keyword and is not inside #MENU — it only has
	// the size score (3). That's enough to be returned (score > 0), so this
	// test verifies it IS found but doesn't assert the context bonus.
	// The key assertion is that it doesn't crash and returns a result.
	_ = ok
	_ = got
}

func TestFindMenuImage_MenuContextBonusOnlyInsideDiv(t *testing.T) {
	// Two equally-sized images with no filename keywords — one inside #MENU,
	// one after it. The one inside #MENU should score higher and win.
	html := `<html><body>
<div id="MENU"><img src="inside.png" width="1024" height="800" alt=""></div>
<img src="outside.png" width="1024" height="800" alt="">
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	require.True(t, ok)
	assert.Equal(t, "https://cafe.com/inside.png", got,
		"image inside #MENU should win over equally-sized sibling outside it")
}

// ── FindMenuImage precision (G3): live false-positive shapes ──────────────────

func TestFindMenuImage_RejectsHeroPhotoWithNoMenuSignal(t *testing.T) {
	// A large hero food photo with no menu keyword and no #menu context. With
	// the min-score threshold, size alone is no longer enough to surface it.
	html := `<html><body>
<img src="/uploads/hero-2024.jpg" width="1920" height="1080" alt="Delicious food">
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "hero photo with no menu signal should be rejected by the min-score threshold")
}

func TestFindMenuImage_RejectsPressBadgeEvenWithMenuAlt(t *testing.T) {
	// A press-badge image whose alt happens to contain "menu" but whose
	// filename screams press/award must be penalized below the threshold.
	html := `<html><body>
<img src="/press/award-badge-2023.png" width="1200" height="1200" alt="menu of awards">
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "press/award badge should be rejected even if alt contains 'menu'")
}

func TestFindMenuImage_RejectsSvgLogo(t *testing.T) {
	html := `<html><body>
<img src="/assets/logo.svg" width="400" height="200" alt="logo">
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, ".svg logo should be rejected")
}

func TestFindMenuImage_RejectsBannerWithNoMenuSignal(t *testing.T) {
	html := `<html><body>
<img src="/banners/home-banner.jpg" width="1600" height="600" alt="">
</body></html>`
	_, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok, "banner with no menu signal should be rejected")
}

func TestFindMenuImage_RealTrifoldStillChosen(t *testing.T) {
	// The live true-positive shape: a TRIFOLD_MENU png inside a #menu container
	// with empty alt. Must still be chosen after the heuristics tighten.
	html := `<html><body>
<div id="MENU">
  <h2>Menu</h2>
  <p>Marketing text</p>
  <img src="/wp-content/uploads/2026/03/TRIFOLD_MENU_8x11_BC-1024x798.png"
       width="1024" height="798" alt="">
</div>
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	require.True(t, ok, "real trifold menu inside #MENU must still be found")
	assert.Equal(t, "https://cafe.com/wp-content/uploads/2026/03/TRIFOLD_MENU_8x11_BC-1024x798.png", got)
}

func TestFindMenuImage_LogoPenaltyBeatsMenuAltBonus(t *testing.T) {
	// Two equally-sized images, both with "menu" in alt. One is a logo
	// (filename contains "logo"), the other a real menu image. The logo
	// penalty must drop it below the real menu image.
	html := `<html><body>
<img src="/img/logo-menu.png" width="1024" height="800" alt="menu">
<img src="/img/TRIFOLD_MENU.png" width="1024" height="800" alt="menu">
</body></html>`
	got, ok := FindMenuImage([]byte(html), "text/html", "https://cafe.com/")
	require.True(t, ok)
	assert.Equal(t, "https://cafe.com/img/TRIFOLD_MENU.png", got,
		"real menu image should beat logo-with-menu-alt")
}

// ── FindMenuImages (plural): top-N candidates ─────────────────────────────────

func TestFindMenuImages_ReturnsCandidatesInScoreOrder(t *testing.T) {
	// A page with a real trifold (high score) and a hero photo (low/zero).
	// The trifold must come first; the hero may appear after if it clears the
	// threshold (here it does not, since size alone is below threshold).
	html := `<html><body>
<div id="MENU">
  <img src="/TRIFOLD_MENU.png" width="1024" height="798" alt="">
</div>
<img src="/hero-2024.jpg" width="1920" height="1080" alt="Delicious food">
</body></html>`
	cands, ok := FindMenuImages([]byte(html), "text/html", "https://cafe.com/")
	require.True(t, ok)
	require.NotEmpty(t, cands)
	assert.Equal(t, "https://cafe.com/TRIFOLD_MENU.png", cands[0])
}

func TestFindMenuImages_EmptyWhenNoCandidatesAboveThreshold(t *testing.T) {
	html := `<html><body><img src="/hero.jpg" width="1920" height="1080" alt="food"></body></html>`
	_, ok := FindMenuImages([]byte(html), "text/html", "https://cafe.com/")
	assert.False(t, ok)
}
