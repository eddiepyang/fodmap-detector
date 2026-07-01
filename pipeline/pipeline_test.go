package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"fodmap/scraper"
)

// stubFetcher implements scraper.Fetcher and returns a preset result/error.
type stubFetcher struct {
	result scraper.FetchResult
	err    error
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	return s.result, s.err
}

// stubExtractor is a minimal scraper.Extractor (no-op).
type stubExtractor struct{}

func (s *stubExtractor) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	return scraper.MenuExtractionResult{}, nil
}

// rendererExtractor implements both scraper.Extractor and scraper.HTMLRenderer.
type rendererExtractor struct {
	stubExtractor
	renderResult scraper.FetchResult
	renderErr    error
	called       bool
}

func (r *rendererExtractor) FetchRenderedHTML(_ context.Context, _ string, _ scraper.RenderOptions) (scraper.FetchResult, error) {
	r.called = true
	return r.renderResult, r.renderErr
}

// jsShellExtractor implements scraper.Extractor + scraper.HTMLRenderer for
// the G2 re-cascade test. The first Extract call (on the static shell text)
// returns 0 items; the second (on the hydrated HTML) returns real items. It
// captures the text passed to each call so the test can assert the re-cascade
// re-ran the LLM on the rendered HTML, not the shell.
type jsShellExtractor struct {
	calls      []string
	renderHTML string
	renderErr  error
	rendered   bool
}

func (e *jsShellExtractor) Extract(_ context.Context, text string) (scraper.MenuExtractionResult, error) {
	e.calls = append(e.calls, text)
	if len(e.calls) == 1 {
		// First call: static shell text → 0 items (triggers re-cascade).
		return scraper.MenuExtractionResult{}, nil
	}
	// Second call: hydrated HTML → real items.
	return scraper.MenuExtractionResult{
		RestaurantName: "Spa Cafe",
		Items: []scraper.MenuEntry{
			{DishName: "Espresso", StatedIngredients: []string{}},
			{DishName: "Latte", StatedIngredients: []string{}},
		},
	}, nil
}

func (e *jsShellExtractor) FetchRenderedHTML(_ context.Context, _ string, _ scraper.RenderOptions) (scraper.FetchResult, error) {
	e.rendered = true
	if e.renderErr != nil {
		return scraper.FetchResult{}, e.renderErr
	}
	return scraper.FetchResult{
		Body:        io.NopCloser(strings.NewReader(e.renderHTML)),
		ContentType: "text/html",
	}, nil
}

// jsShellHTML returns a realistic JS-framework shell: ~370 runes of visible
// boilerplate (above the 200-rune tooShort floor, below the 500-rune IsJSShell
// visible floor) padded with a large inlined-JS bundle (~190KB) so the
// raw-to-visible ratio crosses jsShellMinRatio. The static body has no menu.
func jsShellHTML() string {
	visible := `<html><head><title>Spa Cafe</title></head><body>` +
		`<h1>Spa Cafe</h1>` +
		`<p>Welcome to Spa Cafe. Open daily 7am to 7pm. Call (555) 123-4567 for orders.</p>` +
		`<p>HOME MENU CONTACT. Quality coffee and pastries served fresh every morning.</p>`
	bundle := `<script>var b=function(){return ` + strings.Repeat("1+", 6000) +
		`0};window.__INITIAL_STATE__={"p":"` + strings.Repeat("x", 180000) + `"}</script>`
	return visible + bundle + `</body></html>`
}

// hydratedHTML is the rendered DOM after JS runs — the menu appeared.
const hydratedHTML = `<html><body><h1>Spa Cafe</h1><h2>Drinks</h2><ul>` +
	`<li>Espresso $3</li><li>Latte $4</li><li>Cappuccino $4.5</li></ul></body></html>`

func TestExtractMenu_JSShellReCascadeOnEmptyText(t *testing.T) {
	// G2: a JS-framework shell (IsJSShell) whose static HTML yields 0 items on
	// the text pass must trigger a rendered-fetch re-cascade. The LLM re-runs
	// on the hydrated HTML and extracts the real menu.
	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(jsShellHTML())),
			ContentType: "text/html",
		},
	}
	ex := &jsShellExtractor{renderHTML: hydratedHTML}

	res, _, err := ExtractMenu(context.Background(), "https://spa.example.com", fetcher, ex, false, false, "")
	if err != nil {
		t.Fatalf("ExtractMenu: %v", err)
	}
	if !ex.rendered {
		t.Error("FetchRenderedHTML was not called (re-cascade must run on empty text + JS shell)")
	}
	if len(ex.calls) != 2 {
		t.Fatalf("Extract called %d times; want 2 (static shell then hydrated HTML)", len(ex.calls))
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items from hydrated HTML, got %d", len(res.Items))
	}
	if res.ExtractionTier != TierWebagent {
		t.Errorf("ExtractionTier = %q, want %q", res.ExtractionTier, TierWebagent)
	}
}

// preRenderExtractor implements scraper.Extractor + scraper.HTMLRenderer and
// returns items only when given hydrated text (contains "Espresso").
type preRenderExtractor struct {
	calls      []string
	renderHTML string
	rendered   bool
}

func (e *preRenderExtractor) Extract(_ context.Context, text string) (scraper.MenuExtractionResult, error) {
	e.calls = append(e.calls, text)
	if strings.Contains(text, "Espresso") {
		return scraper.MenuExtractionResult{
			RestaurantName: "Spa Cafe",
			Items:          []scraper.MenuEntry{{DishName: "Espresso", StatedIngredients: []string{}}},
		}, nil
	}
	return scraper.MenuExtractionResult{}, nil
}

func (e *preRenderExtractor) FetchRenderedHTML(_ context.Context, _ string, _ scraper.RenderOptions) (scraper.FetchResult, error) {
	e.rendered = true
	return scraper.FetchResult{
		Body:        io.NopCloser(strings.NewReader(e.renderHTML)),
		ContentType: "text/html",
	}, nil
}

// trivialShellHTML returns a JS shell whose visible static text is near-empty
// (the swick2go.dine.online shape): a JS bundle plus almost no body text.
func trivialShellHTML() string {
	return `<html><head><title>Order Online</title></head><body><div id="root"></div>` +
		`<script>var b=function(){return ` + strings.Repeat("1+", 6000) +
		`0};window.__INITIAL_STATE__={"p":"` + strings.Repeat("x", 180000) + `"}</script></body></html>`
}

func TestExtractMenu_JSShellTrivialTextPreRenders(t *testing.T) {
	// A JS shell with near-empty static text must be rendered BEFORE the text
	// pass — the LLM must never see the shell (hallucination risk).
	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(trivialShellHTML())),
			ContentType: "text/html",
		},
	}
	ex := &preRenderExtractor{renderHTML: hydratedHTML}

	res, _, err := ExtractMenu(context.Background(), "https://spa.example.com", fetcher, ex, false, false, "")
	if err != nil {
		t.Fatalf("ExtractMenu: %v", err)
	}
	if !ex.rendered {
		t.Error("FetchRenderedHTML was not called (pre-render must run on JS shell with trivial text)")
	}
	if len(ex.calls) != 1 {
		t.Fatalf("Extract called %d times; want 1 (hydrated HTML only, never the shell)", len(ex.calls))
	}
	if !strings.Contains(ex.calls[0], "Espresso") {
		t.Errorf("Extract input was not the hydrated HTML: %q", ex.calls[0][:min(len(ex.calls[0]), 120)])
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item from hydrated HTML, got %d", len(res.Items))
	}
	if res.ExtractionTier != TierWebagent {
		t.Errorf("ExtractionTier = %q, want %q", res.ExtractionTier, TierWebagent)
	}
}

func TestExtractMenu_TrivialTextRefusesLLM(t *testing.T) {
	// Near-empty page text with no render path available must refuse the LLM
	// call instead of inviting hallucination (observed: a 1-rune page produced
	// 34 invented menu items).
	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(`<html><body><p>Hi</p></body></html>`)),
			ContentType: "text/html",
		},
	}
	ex := &mockExtractor{}

	_, _, err := ExtractMenu(context.Background(), "https://example.com", fetcher, ex, false, false, "")
	if err == nil {
		t.Fatal("expected refusal error for trivial page text, got nil")
	}
	if !strings.Contains(err.Error(), "refusing LLM call") {
		t.Errorf("error = %q, want refusal error", err)
	}
	if ex.called {
		t.Error("Extract must not be called with trivial page text")
	}
}

func TestExtractMenu_JSShellReCascadeSkippedWhenTextHasItems(t *testing.T) {
	// Regression guard: a JS-shell page whose static HTML DOES yield items on
	// the text pass must NOT trigger the re-cascade. This prevents the
	// dilution risk — a small real menu on a JS-heavy site extracts fine from
	// the static text and never pays for a render.
	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(jsShellHTML())),
			ContentType: "text/html",
		},
	}
	// This extractor returns items on every call (simulating a small real menu
	// in the static HTML). The re-cascade must not fire.
	ex := &mockExtractor{}

	res, _, err := ExtractMenu(context.Background(), "https://spa.example.com", fetcher, ex, false, false, "")
	if err != nil {
		t.Fatalf("ExtractMenu: %v", err)
	}
	if !ex.called {
		t.Error("Extract was not called on the static text")
	}
	if len(res.Items) == 0 {
		t.Fatal("expected items from the static text pass (no re-cascade needed)")
	}
}

// ── fetchWithFallback tests ───────────────────────────────────────────────────

func TestFetchWithFallback_SuccessNoFallback(t *testing.T) {
	want := "hello html"
	fetcher := &stubFetcher{
		result: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(want)),
			ContentType: "text/html",
		},
	}
	ex := &stubExtractor{}

	body, ct, err := fetchWithFallback(context.Background(), "https://example.com", fetcher, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != want {
		t.Errorf("body = %q, want %q", string(body), want)
	}
	if ct != "text/html" {
		t.Errorf("ct = %q", ct)
	}
}

func TestFetchWithFallback_404_CallsFallback(t *testing.T) {
	// Any fetch error should now trigger the rendered-fetch fallback.
	wantHTML := "<html>rendered 404 page</html>"
	renderer := &rendererExtractor{
		renderResult: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(wantHTML)),
			ContentType: "text/html",
		},
	}
	fetcher := &stubFetcher{err: &scraper.HTTPStatusError{StatusCode: 404, URL: "https://example.com"}}

	body, ct, err := fetchWithFallback(context.Background(), "https://example.com", fetcher, renderer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !renderer.called {
		t.Error("rendered-fetch must be called on 404")
	}
	if string(body) != wantHTML {
		t.Errorf("body = %q, want %q", string(body), wantHTML)
	}
	if ct != "text/html" {
		t.Errorf("content-type = %q, want text/html", ct)
	}
}

func TestFetchWithFallback_403_CallsFallback(t *testing.T) {
	wantHTML := "<html>rendered</html>"
	renderer := &rendererExtractor{
		renderResult: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(wantHTML)),
			ContentType: "text/html; charset=utf-8",
		},
	}
	fetcher := &stubFetcher{err: &scraper.HTTPStatusError{StatusCode: 403, URL: "https://blocked.com"}}

	body, ct, err := fetchWithFallback(context.Background(), "https://blocked.com", fetcher, renderer)
	if err != nil {
		t.Fatalf("unexpected error after fallback: %v", err)
	}
	if !renderer.called {
		t.Error("rendered-fetch must be called on 403")
	}
	if string(body) != wantHTML {
		t.Errorf("body = %q, want %q", string(body), wantHTML)
	}
	if ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestFetchWithFallback_429_CallsFallback(t *testing.T) {
	wantHTML := "<html>rate-limited page</html>"
	renderer := &rendererExtractor{
		renderResult: scraper.FetchResult{
			Body:        io.NopCloser(strings.NewReader(wantHTML)),
			ContentType: "text/html",
		},
	}
	fetcher := &stubFetcher{err: &scraper.HTTPStatusError{StatusCode: 429, URL: "https://throttled.com"}}

	body, _, err := fetchWithFallback(context.Background(), "https://throttled.com", fetcher, renderer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !renderer.called {
		t.Error("rendered-fetch must be called on 429")
	}
	if string(body) != wantHTML {
		t.Errorf("body = %q", string(body))
	}
}

func TestFetchWithFallback_403_NoRenderer_ReturnsOriginalError(t *testing.T) {
	// When ex does not implement HTMLRenderer, a 403 must return the original
	// HTTPStatusError, not a generic "fetch failed" message.
	origErr := &scraper.HTTPStatusError{StatusCode: 403, URL: "https://blocked.com"}
	fetcher := &stubFetcher{err: origErr}
	ex := &stubExtractor{} // no HTMLRenderer

	_, _, err := fetchWithFallback(context.Background(), "https://blocked.com", fetcher, ex)
	if err == nil {
		t.Fatal("expected error")
	}
	var statusErr *scraper.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Errorf("expected *HTTPStatusError wrapped in the return error, got: %v", err)
	}
}

func TestFetchWithFallback_403_FallbackFails_ReturnsFallbackError(t *testing.T) {
	renderErr := errors.New("browser busy")
	renderer := &rendererExtractor{renderErr: renderErr}
	fetcher := &stubFetcher{err: &scraper.HTTPStatusError{StatusCode: 403, URL: "https://blocked.com"}}

	_, _, err := fetchWithFallback(context.Background(), "https://blocked.com", fetcher, renderer)
	if err == nil {
		t.Fatal("expected error when fallback itself fails")
	}
	if !strings.Contains(err.Error(), "rendered-fetch fallback") {
		t.Errorf("error should mention rendered-fetch fallback, got: %v", err)
	}
}
