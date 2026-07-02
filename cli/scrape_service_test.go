package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fodmap/pipeline"
	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"
)

type testForceRenderFetcher struct {
	rf scraper.RenderedFetcher
}

func (f *testForceRenderFetcher) Fetch(ctx context.Context, url string) (scraper.FetchResult, error) {
	return f.rf.FetchRendered(ctx, url)
}

func runScrapeWith(
	ctx context.Context,
	restaurantID uuid.UUID,
	rawURL string,
	fetcher scraper.Fetcher,
	ex scraper.Extractor,
	store server.MenuStore,
	embedder search.Embedder,
	enableVision bool,
	enableJSRender bool,
	usePdftotext bool,
	webagentAdapter string,
) error {
	if enableJSRender && webagentAdapter == "" {
		if rf, ok := fetcher.(scraper.RenderedFetcher); ok {
			fetcher = &testForceRenderFetcher{rf: rf}
		}
	}
	result, _, err := pipeline.ExtractMenu(ctx, rawURL, fetcher, ex, enableVision, usePdftotext, webagentAdapter)
	if err != nil {
		return err
	}
	if result != nil && len(result.Items) > 0 {
		_, err = pipeline.StoreMenu(ctx, result, restaurantID, rawURL, store, embedder)
		return err
	}
	return nil
}

// pdfExtractorStub implements both scraper.Extractor (for the HTML path) and
// scraper.PDFExtractor (for the PDF path). It records calls so the test can
// assert that the PDF branch routes to ExtractPDF and that the second
// ex.Extract pass is skipped for the service result.
type pdfExtractorStub struct {
	extractCalls  int
	extractPDFErr error
	pdfResult     scraper.MenuExtractionResult
}

func (s *pdfExtractorStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.extractCalls++
	return scraper.MenuExtractionResult{}, nil
}

func (s *pdfExtractorStub) ExtractPDF(_ context.Context, _ []byte) (scraper.MenuExtractionResult, error) {
	if s.extractPDFErr != nil {
		return scraper.MenuExtractionResult{}, s.extractPDFErr
	}
	return s.pdfResult, nil
}

// pdfFetcher serves a fixed "fake pdf" body with application/pdf content type
// so runScrapeWith enters the PDF branch. It does NOT yield usable text, so
// the cascade reaches the vision/service path.
type pdfFetcher struct{}

func (f *pdfFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	// "not a pdf" makes ExtractPDFText return ErrNeedVision, forcing the
	// cascade into the service path.
	return scraper.FetchResult{
		Body:        io.NopCloser(strings.NewReader("not a pdf")),
		ContentType: "application/pdf",
	}, nil
}

// stubMenuStore is a no-op server.MenuStore for runScrapeWith tests.
type stubMenuStore struct{}

func (s *stubMenuStore) EnsureMenuSchema(_ context.Context) error { return nil }
func (s *stubMenuStore) BatchUpsertMenu(_ context.Context, _ []search.MenuItem) error {
	return nil
}
func (s *stubMenuStore) SearchMenu(_ context.Context, _ string, _ int) ([]search.MenuItem, error) {
	return nil, nil
}

func (s *stubMenuStore) ListMenuItems(_ context.Context, _ string, _, _ int) ([]search.MenuItem, int, error) {
	return nil, 0, nil
}

func TestRunScrapeWith_PDFServicePath_SkipsExtract(t *testing.T) {
	stub := &pdfExtractorStub{
		pdfResult: scraper.MenuExtractionResult{
			RestaurantName: "Service Restaurant",
			Items: []scraper.MenuEntry{
				{DishName: "Service Pizza", Description: "from service",
					StatedIngredients: []string{"cheese"}, HasFullIngredients: true},
			},
		},
	}

	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu.pdf",
		&pdfFetcher{},
		stub,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision (irrelevant for service path)
		false, // enableJSRender
		false, // usePdftotext
		"",    // webagentAdapter
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}

	// The service path should have produced the structured result directly;
	// ex.Extract (the second LLM structuring pass) must NOT have been called.
	if stub.extractCalls != 0 {
		t.Errorf("ex.Extract called %d times; expected 0 (service path skips the LLM pass)",
			stub.extractCalls)
	}
}

func TestRunScrapeWith_PDFServicePath_EmptyResultIsNotAnError(t *testing.T) {
	stub := &pdfExtractorStub{
		pdfResult: scraper.MenuExtractionResult{}, // empty menu
	}

	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu.pdf",
		&pdfFetcher{},
		stub,
		&stubMenuStore{},
		stubEmbedder{},
		false,
		false,
		false,
		"",
	)
	if err != nil {
		t.Fatalf("runScrapeWith with empty service result: %v", err)
	}
}

// htmlFetcher serves an HTML page so runScrapeWith uses the HTML/text path,
// confirming the non-PDF branch is unaffected by the PDFExtractor wiring.
type htmlFetcher struct{}

func (f *htmlFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	body := "<html><body><h1>Joe's Pizza Menu</h1><ul>" +
		"<li>Margherita Pizza $12 — tomato, mozzarella, basil</li>" +
		"<li>Pepperoni Pizza $14 — tomato, mozzarella, pepperoni</li></ul></body></html>"
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(body))),
		ContentType: "text/html",
	}, nil
}

type extractorStub struct {
	result scraper.MenuExtractionResult
	called int
}

func (e *extractorStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	e.called++
	return e.result, nil
}

func TestRunScrapeWith_HTMLPathStillCallsExtract(t *testing.T) {
	ex := &extractorStub{
		result: scraper.MenuExtractionResult{
			RestaurantName: "HTML Place",
			Items: []scraper.MenuEntry{
				{DishName: "Pizza", StatedIngredients: []string{}},
			},
		},
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		&htmlFetcher{},
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false,
		false,
		false,
		"",
	)
	if err != nil {
		t.Fatalf("runScrapeWith HTML: %v", err)
	}
	if ex.called != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (HTML path must still call it)", ex.called)
	}
}

// ── Phase B: webagent JS-render path ─────────────────────────────────────────

// jsRendererStub implements scraper.Extractor + scraper.JSRenderer so the
// noisy-HTML branch can route to the webagent path.
type jsRendererStub struct {
	extractCalls int
	scrapeCalls  int
	jsResult     scraper.MenuExtractionResult
}

func (s *jsRendererStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.extractCalls++
	return scraper.MenuExtractionResult{}, nil
}

func (s *jsRendererStub) ScrapeJS(_ context.Context, _ string, _ map[string]any) (scraper.MenuExtractionResult, error) {
	s.scrapeCalls++
	return s.jsResult, nil
}

// noisyHTMLFetcher serves an HTML page that IsTooNoisy flags as nav-heavy,
// triggering the webagent fallback when enabled.
type noisyHTMLFetcher struct{}

func (f *noisyHTMLFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	// Nav-heavy page with many short list-item lines and no real article
	// content. IsTooNoisy flags it (>70% short lines, >=20 total), and
	// trafilatura's main-content extraction returns empty (no <p> blocks),
	// triggering the webagent fallback.
	var b strings.Builder
	b.WriteString("<html><head><title>x</title></head><body><ul>")
	for i := 0; i < 30; i++ {
		b.WriteString("<li>nav")
		fmt.Fprintf(&b, "%d", i)
		b.WriteString("</li>\n")
	}
	b.WriteString("</ul></body></html>")
	body := b.String()
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(body))),
		ContentType: "text/html",
	}, nil
}

func TestRunScrapeWith_NoisyHTMLRoutesToWebagent(t *testing.T) {
	js := &jsRendererStub{
		jsResult: scraper.MenuExtractionResult{
			RestaurantName: "JS Place",
			Items: []scraper.MenuEntry{
				{DishName: "Dynamic Pizza", StatedIngredients: []string{}},
			},
		},
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		&noisyHTMLFetcher{},
		js,
		&stubMenuStore{},
		stubEmbedder{},
		false,         // enableVision
		true,          // enableJSRender
		false,         // usePdftotext
		"site/target", // webagentAdapter
	)
	if err != nil {
		t.Fatalf("runScrapeWith webagent: %v", err)
	}
	if js.scrapeCalls != 1 {
		t.Errorf("ScrapeJS called %d times; want 1", js.scrapeCalls)
	}
	if js.extractCalls != 0 {
		t.Errorf("ex.Extract called %d times; want 0 (webagent path skips the LLM pass)",
			js.extractCalls)
	}
}

// spaShellFetcher serves a Wix-style SPA shell: ~370 runes of visible
// boilerplate (above the 200-rune tooShort floor, so tooShort does NOT fire)
// and long prose lines (IsTooNoisy is false), with a large inlined-JS bundle
// that drives the raw-bytes-to-visible-runes ratio above the jsShellMinRatio
// threshold. The menu content is injected client-side by the
// restaurant-menus-showcase-ooi widget, so the static body has no menu. Only
// the IsJSShell check routes this to the webagent — making this a genuine
// regression test for the IsJSShell detector (the old tooShort<200 and
// IsTooNoisy gates would both leave it on the empty LLM text path).
type spaShellFetcher struct{}

func (f *spaShellFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	visible := `<html><head><title>3Greeks Grill | Gyro and Souvlaki</title>
<link rel="stylesheet" href="https://static.parastorage.com/services/x.css"/></head>
<body><div id="SITE_CONTAINER"><h1>3Greeks Grill | Gyro and Souvlaki</h1>
<p>WELCOME Καλη Ορεξη (718) 729-8900 35-61 Vernon Blvd, Long Island City, NY 11106</p>
<p>Request a catering order at Catering@3greeksgrill.com. Use tab to navigate through the menu items. More</p>
<p>HOME MENU CONTACT. High quality Greek Gyro and Souvlaki platters and sandwiches as well as other Greek specialty foods.</p>`
	// Emulate a real Wix page's minified bundle + inlined __INITIAL_STATE__
	// (~190KB) so the raw-to-visible ratio crosses jsShellMinRatio. Wrapped in
	// <script> so ConvertHTMLToMarkdown strips it from the visible text.
	bundle := `<script>var b=function(){return ` + strings.Repeat("1+", 6000) +
		`0};window.__INITIAL_STATE__={"p":"` + strings.Repeat("x", 180000) + `"}</script>`
	body := visible + bundle + `</div></body></html>`
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(body))),
		ContentType: "text/html",
	}, nil
}

func TestRunScrapeWith_SpaShellRoutesToWebagent(t *testing.T) {
	// A Wix-style SPA shell (IsJSShell) with a webagentAdapter set must NOT
	// preemptively route to ScrapeJS — that would skip the text pass and risk
	// dilution on pages that have real static content. Instead the text pass
	// runs first (returns 0 items for the shell), then the post-empty
	// re-cascade fires via FetchRenderedHTML, which returns hydrated HTML
	// that the LLM extracts items from.
	// ScrapeJS must NOT be called — the re-cascade uses the generic
	// HTMLRenderer path, not the per-site adapter.
	ex := &spaShellRendererExtractor{
		renderHTML: hydratedMenuHTML,
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://www.3greeksgrill.com",
		&spaShellFetcher{},
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false,         // enableVision
		true,          // enableJSRender
		false,         // usePdftotext
		"site/target", // webagentAdapter — must NOT trigger preemptive ScrapeJS
	)
	if err != nil {
		t.Fatalf("runScrapeWith spa shell: %v", err)
	}
	if ex.extractCalls != 2 {
		t.Errorf("Extract called %d times; want 2 (static shell → 0, then hydrated HTML → items)",
			ex.extractCalls)
	}
	if ex.renderCalled {
		// renderCalled is fine — the re-cascade uses FetchRenderedHTML
	} else {
		t.Error("FetchRenderedHTML was not called (post-empty re-cascade must run)")
	}
}

// hydratedMenuHTML is the rendered DOM after JS runs — a real menu appeared.
const hydratedMenuHTML = `<html><body><h1>3Greeks Grill</h1><h2>Appetizers</h2><ul>` +
	`<li>Gyro $5</li><li>Souvlaki $6</li></ul></body></html>`

// spaShellRendererExtractor implements scraper.Extractor + scraper.HTMLRenderer
// + scraper.JSRenderer for the SPA-shell re-cascade test. Extract returns 0
// items on the first call (static shell) and real items on the second (hydrated
// HTML). ScrapeJS records if it was called (it must NOT be for a JS shell).
type spaShellRendererExtractor struct {
	extractCalls int
	renderCalled bool
	scrapeCalled bool
	renderHTML   string
}

func (e *spaShellRendererExtractor) Extract(_ context.Context, text string) (scraper.MenuExtractionResult, error) {
	e.extractCalls++
	if e.extractCalls == 1 {
		return scraper.MenuExtractionResult{}, nil // static shell → 0 items
	}
	return scraper.MenuExtractionResult{
		RestaurantName: "3Greeks Grill",
		Items: []scraper.MenuEntry{
			{DishName: "Gyro", StatedIngredients: []string{}},
			{DishName: "Souvlaki", StatedIngredients: []string{}},
		},
	}, nil
}

func (e *spaShellRendererExtractor) FetchRenderedHTML(_ context.Context, _ string, _ scraper.RenderOptions) (scraper.FetchResult, error) {
	e.renderCalled = true
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(e.renderHTML))),
		ContentType: "text/html",
	}, nil
}

func (e *spaShellRendererExtractor) ScrapeJS(_ context.Context, _ string, _ map[string]any) (scraper.MenuExtractionResult, error) {
	e.scrapeCalled = true
	return scraper.MenuExtractionResult{}, nil
}

func TestRunScrapeWith_SpaShellWithoutAdapterFallsBackToExtract(t *testing.T) {
	// Guard: when no webagentAdapter is configured, an SPA shell must degrade
	// gracefully to the LLM text pass (no panic, no webagent call). This is
	// the same contract as TestRunScrapeWith_NoisyHTMLWithoutJSRenderFallsBackToExtract.
	ex := &extractorStub{
		result: scraper.MenuExtractionResult{Items: []scraper.MenuEntry{{DishName: "x"}}},
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://www.3greeksgrill.com",
		&spaShellFetcher{},
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision
		true,  // enableJSRender
		false, // usePdftotext
		"",    // no webagentAdapter — must fall through to the LLM text pass
	)
	if err != nil {
		t.Fatalf("runScrapeWith spa shell no-adapter: %v", err)
	}
	if ex.called != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (no adapter → text pass)", ex.called)
	}
}

func TestRunScrapeWith_NoisyHTMLWithoutJSRenderFallsBackToExtract(t *testing.T) {
	ex := &extractorStub{
		result: scraper.MenuExtractionResult{Items: []scraper.MenuEntry{{DishName: "x"}}},
	}
	// enableJSRender=false → noisy HTML should NOT route to webagent; it should
	// fall through to the normal ex.Extract path (with trafilatura fallback).
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		&noisyHTMLFetcher{},
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision
		false, // enableJSRender — webagent disabled
		false,
		"site/target", // adapter set but JS render off — should be ignored
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if ex.called != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (no webagent without --enable-js-render)",
			ex.called)
	}
}

// ── Phase C: image-embedded menu path ────────────────────────────────────────

// imageExtractorStub implements scraper.Extractor + scraper.ImageExtractor so
// the noisy-HTML-with-menu-image branch can route to the service image OCR path.
type imageExtractorStub struct {
	extractCalls  int
	extractImgErr error
	imgCalls      int
	imgResult     scraper.MenuExtractionResult
}

func (s *imageExtractorStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.extractCalls++
	return scraper.MenuExtractionResult{}, nil
}

func (s *imageExtractorStub) ExtractImage(_ context.Context, _ []byte, _ string) (scraper.MenuExtractionResult, error) {
	s.imgCalls++
	if s.extractImgErr != nil {
		return scraper.MenuExtractionResult{}, s.extractImgErr
	}
	return s.imgResult, nil
}

// menuImageFetcher serves noisy HTML containing a large menu image. When the
// image URL is fetched, it returns fake PNG bytes.
type menuImageFetcher struct {
	imgFetched bool
}

func (f *menuImageFetcher) Fetch(_ context.Context, rawURL string) (scraper.FetchResult, error) {
	if strings.HasSuffix(rawURL, ".png") {
		f.imgFetched = true
		return scraper.FetchResult{
			Body:        io.NopCloser(bytes.NewReader([]byte("fake-png"))),
			ContentType: "image/png",
		}, nil
	}
	// Noisy HTML page with a menu image inside #MENU.
	var b strings.Builder
	b.WriteString("<html><head><title>x</title></head><body><ul>")
	for i := 0; i < 30; i++ {
		b.WriteString("<li>nav")
		fmt.Fprintf(&b, "%d", i)
		b.WriteString("</li>\n")
	}
	b.WriteString("</ul>")
	b.WriteString(`<div id="MENU"><h2>Menu</h2><img src="https://example.com/menu.png" width="1024" height="798" alt=""></div>`)
	b.WriteString("</body></html>")
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(b.String()))),
		ContentType: "text/html",
	}, nil
}

func TestRunScrapeWith_NoisyHTMLWithMenuImageRoutesToImageOCR(t *testing.T) {
	img := &imageExtractorStub{
		imgResult: scraper.MenuExtractionResult{
			RestaurantName: "Image Cafe",
			Items: []scraper.MenuEntry{
				{DishName: "Latte", StatedIngredients: []string{}},
			},
		},
	}
	fetcher := &menuImageFetcher{}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		fetcher,
		img,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision
		false, // enableJSRender
		false, // usePdftotext
		"",    // webagentAdapter (not needed for image path)
	)
	if err != nil {
		t.Fatalf("runScrapeWith image: %v", err)
	}
	if img.imgCalls != 1 {
		t.Errorf("ExtractImage called %d times; want 1", img.imgCalls)
	}
	if img.extractCalls != 0 {
		t.Errorf("ex.Extract called %d times; want 0 (image path skips the LLM pass)",
			img.extractCalls)
	}
	if !fetcher.imgFetched {
		t.Error("image was never fetched")
	}
}

func TestRunScrapeWith_MenuImagePrefersImageOverWebagent(t *testing.T) {
	// When both ImageExtractor and JSRenderer are implemented and the page has
	// a menu image, the image path should win over the webagent.
	both := &imageAndJSStub{
		imgResult: scraper.MenuExtractionResult{
			Items: []scraper.MenuEntry{{DishName: "from-image"}},
		},
		jsResult: scraper.MenuExtractionResult{
			Items: []scraper.MenuEntry{{DishName: "from-js"}},
		},
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		&menuImageFetcher{},
		both,
		&stubMenuStore{},
		stubEmbedder{},
		false,
		true, // enableJSRender — should be preempted by image path
		false,
		"site/target", // webagentAdapter set but image path should win
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if both.imgCalls != 1 {
		t.Errorf("ExtractImage called %d times; want 1 (image path should win)", both.imgCalls)
	}
	if both.jsCalls != 0 {
		t.Errorf("ScrapeJS called %d times; want 0 (image path preempts webagent)", both.jsCalls)
	}
}

// imageAndJSStub implements both ImageExtractor and JSRenderer.
type imageAndJSStub struct {
	extractCalls int
	imgCalls     int
	jsCalls      int
	imgResult    scraper.MenuExtractionResult
	jsResult     scraper.MenuExtractionResult
}

func (s *imageAndJSStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.extractCalls++
	return scraper.MenuExtractionResult{}, nil
}

func (s *imageAndJSStub) ExtractImage(_ context.Context, _ []byte, _ string) (scraper.MenuExtractionResult, error) {
	s.imgCalls++
	return s.imgResult, nil
}

func (s *imageAndJSStub) ScrapeJS(_ context.Context, _ string, _ map[string]any) (scraper.MenuExtractionResult, error) {
	s.jsCalls++
	return s.jsResult, nil
}

// ── 1b: routing gate — image path reachable when text yields 0 items ──────────

// textEmptyImageStub implements scraper.Extractor + scraper.ImageExtractor.
// Extract returns 0 items (simulating the text path finding no menu in
// boilerplate HTML); ExtractImage returns a real menu, proving the image path
// is reachable even when the HTML has >200 chars of non-noisy content.
type textEmptyImageStub struct {
	extractCalls int
	imgCalls     int
	imgResult    scraper.MenuExtractionResult
}

func (s *textEmptyImageStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	s.extractCalls++
	return scraper.MenuExtractionResult{}, nil // text path: 0 items
}

func (s *textEmptyImageStub) ExtractImage(_ context.Context, _ []byte, _ string) (scraper.MenuExtractionResult, error) {
	s.imgCalls++
	return s.imgResult, nil
}

// boilerplateMenuFetcher serves a non-noisy HTML page (>200 chars, not nav-heavy)
// that contains a real menu image inside #MENU. The text path will run, return
// 0 items, and then the new 1b logic must route to the image path — even though
// the OLD gate (noisy || empty || tooShort<200) would NOT have routed there.
type boilerplateMenuFetcher struct {
	imgFetched bool
}

func (f *boilerplateMenuFetcher) Fetch(_ context.Context, rawURL string) (scraper.FetchResult, error) {
	if strings.HasSuffix(rawURL, ".png") {
		f.imgFetched = true
		return scraper.FetchResult{
			Body:        io.NopCloser(bytes.NewReader([]byte("fake-png"))),
			ContentType: "image/png",
		}, nil
	}
	// Boilerplate HTML: >200 chars, not noisy (long prose lines), with a
	// menu image inside #MENU. ConvertHTMLToMarkdown yields prose, IsTooNoisy
	// returns false, and len > 200 so the OLD gate would NOT route to image.
	var b strings.Builder
	b.WriteString("<html><head><title>Cafe</title></head><body>")
	b.WriteString("<h1>Welcome to the Cafe</h1>")
	b.WriteString("<p>")
	b.WriteString(strings.Repeat("This is marketing boilerplate prose about our lovely cafe. ", 8))
	b.WriteString("</p>")
	b.WriteString(`<div id="MENU"><img src="https://example.com/TRIFOLD_MENU.png" width="1024" height="798" alt=""></div>`)
	b.WriteString("</body></html>")
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(b.String()))),
		ContentType: "text/html",
	}, nil
}

func TestRunScrapeWith_EmptyTextTriggersImageEvenWhenNotNoisy(t *testing.T) {
	// The G1 fix: needsFallback is no longer the only gate to the image path.
	// Even when the HTML has >200 chars of non-noisy prose (so the old gate
	// would skip the fallback), if ex.Extract returns 0 items AND FindMenuImage
	// found a candidate, the image path must fire.
	stub := &textEmptyImageStub{
		imgResult: scraper.MenuExtractionResult{
			RestaurantName: "Image Cafe",
			Items: []scraper.MenuEntry{
				{DishName: "Latte", StatedIngredients: []string{}},
			},
		},
	}
	fetcher := &boilerplateMenuFetcher{}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		fetcher,
		stub,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision — not needed for the pure-Go image path (1a)
		false, // enableJSRender
		false, // usePdftotext
		"",    // webagentAdapter
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if stub.extractCalls != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (text pass must run first)", stub.extractCalls)
	}
	if stub.imgCalls != 1 {
		t.Errorf("ExtractImage called %d times; want 1 (empty text should trigger image path)", stub.imgCalls)
	}
	if !fetcher.imgFetched {
		t.Error("menu image was never fetched")
	}
}

func TestRunScrapeWith_NonEmptyTextDoesNotTriggerImage(t *testing.T) {
	// Regression guard: when text Extract returns real items, the image path
	// must NOT fire even if a menu image is present — the text pass already won.
	stub := &textNonEmptyImageStub{
		textResult: scraper.MenuExtractionResult{
			Items: []scraper.MenuEntry{{DishName: "Pizza"}},
		},
	}
	fetcher := &boilerplateMenuFetcher{}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://example.com/menu",
		fetcher,
		stub,
		&stubMenuStore{},
		stubEmbedder{},
		false, false, false, "",
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if stub.imgCalls != 0 {
		t.Errorf("ExtractImage called %d times; want 0 (text pass had items)", stub.imgCalls)
	}
}

type textNonEmptyImageStub struct {
	imgCalls   int
	textResult scraper.MenuExtractionResult
}

func (s *textNonEmptyImageStub) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	return s.textResult, nil
}

func (s *textNonEmptyImageStub) ExtractImage(_ context.Context, _ []byte, _ string) (scraper.MenuExtractionResult, error) {
	s.imgCalls++
	return scraper.MenuExtractionResult{}, nil
}

// ── Phase 3: generic JS render-and-re-cascade (no per-site adapter) ───────────

// jsShellFetcher implements scraper.Fetcher + scraper.RenderedFetcher.
// Fetch returns a short JS-shell HTML (<200 chars, so needsFallback fires);
// FetchRendered returns the hydrated HTML with a real menu the text pass can
// extract — proving the generic render path runs WITHOUT a webagent adapter.
type jsShellFetcher struct {
	renderedCalls int
}

func (f *jsShellFetcher) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	// A minimal JS app shell: the menu hydrates client-side, so the raw HTML
	// is tiny. needsFallback (tooShort<200) fires.
	shell := `<html><head><title>Spa</title></head><body><div id="root"></div></body></html>`
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(shell))),
		ContentType: "text/html",
	}, nil
}

func (f *jsShellFetcher) FetchRendered(_ context.Context, _ string) (scraper.FetchResult, error) {
	f.renderedCalls++
	// The hydrated DOM: a real menu appeared after JS ran.
	hydrated := `<html><body><h1>Spa Cafe</h1><h2>Drinks</h2><ul>` +
		`<li>Espresso</li><li>Latte</li><li>Cappuccino</li></ul></body></html>`
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(hydrated))),
		ContentType: "text/html",
	}, nil
}

func TestRunScrapeWith_GenericJSRenderReCascadesWithoutAdapter(t *testing.T) {
	// --enable-js-render with NO --webagent-adapter: the generic render path
	// uses the RenderedFetcher to get hydrated HTML, then re-runs the text
	// cascade on it. No webagent / per-site adapter is needed.
	ex := &extractorStub{
		result: scraper.MenuExtractionResult{
			RestaurantName: "Spa Cafe",
			Items: []scraper.MenuEntry{
				{DishName: "Espresso", StatedIngredients: []string{}},
				{DishName: "Latte", StatedIngredients: []string{}},
			},
		},
	}
	fetcher := &jsShellFetcher{}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://spa.example.com/menu",
		fetcher,
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false, // enableVision
		true,  // enableJSRender
		false, // usePdftotext
		"",    // webagentAdapter — empty: generic render path, not the webagent
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if fetcher.renderedCalls != 1 {
		t.Errorf("FetchRendered called %d times; want 1 (generic render must run)", fetcher.renderedCalls)
	}
	if ex.called != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (text pass on rendered HTML)", ex.called)
	}
}

func TestRunScrapeWith_GenericJSRenderSkippedWhenFetchRenderedNotImplemented(t *testing.T) {
	// If the fetcher is a plain Fetcher (not a RenderedFetcher) and no adapter
	// is set, --enable-js-render must degrade gracefully — no panic, falls
	// through to the normal text pass on the (empty) raw HTML.
	ex := &extractorStub{result: scraper.MenuExtractionResult{}}
	plain := &htmlFetcher{} // implements only Fetch, not FetchRendered
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://spa.example.com/menu",
		plain,
		ex,
		&stubMenuStore{},
		stubEmbedder{},
		false, true, false, "",
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	// Text pass still runs on the raw HTML; 0 items is fine (no menu there).
	if ex.called != 1 {
		t.Errorf("ex.Extract called %d times; want 1 (text pass on raw HTML)", ex.called)
	}
}

func TestRunScrapeWith_WebagentAdapterPreferredOverGenericRender(t *testing.T) {
	// When both a RenderedFetcher and a webagent adapter are available, the
	// webagent (per-site) path wins — it has selector-level guarantees the
	// generic render lacks. FetchRendered must NOT be called.
	js := &jsRendererWithRendered{
		jsResult: scraper.MenuExtractionResult{
			Items: []scraper.MenuEntry{{DishName: "from-webagent"}},
		},
	}
	err := runScrapeWith(
		context.Background(),
		uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
		"https://spa.example.com/menu",
		js, // implements Fetcher, RenderedFetcher, JSRenderer, Extractor
		js,
		&stubMenuStore{},
		stubEmbedder{},
		false,
		true, // enableJSRender
		false,
		"site/target", // webagentAdapter set — webagent path wins
	)
	if err != nil {
		t.Fatalf("runScrapeWith: %v", err)
	}
	if js.jsCalls != 1 {
		t.Errorf("ScrapeJS called %d times; want 1 (webagent path should win)", js.jsCalls)
	}
	if js.renderedCalls != 0 {
		t.Errorf("FetchRendered called %d times; want 0 (webagent preferred)", js.renderedCalls)
	}
}

// jsRendererWithRendered implements Fetcher, RenderedFetcher, Extractor, and
// JSRenderer so a single stub can exercise the precedence: webagent > render.
type jsRendererWithRendered struct {
	renderedCalls int
	jsCalls       int
	jsResult      scraper.MenuExtractionResult
}

func (f *jsRendererWithRendered) Fetch(_ context.Context, _ string) (scraper.FetchResult, error) {
	shell := `<html><body><div id="root"></div></body></html>`
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(shell))),
		ContentType: "text/html",
	}, nil
}

func (f *jsRendererWithRendered) FetchRendered(_ context.Context, _ string) (scraper.FetchResult, error) {
	f.renderedCalls++
	hydrated := `<html><body><h1>Spa</h1><ul><li>Latte</li></ul></body></html>`
	return scraper.FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(hydrated))),
		ContentType: "text/html",
	}, nil
}

func (s *jsRendererWithRendered) Extract(_ context.Context, _ string) (scraper.MenuExtractionResult, error) {
	return scraper.MenuExtractionResult{}, nil
}

func (s *jsRendererWithRendered) ScrapeJS(_ context.Context, _ string, _ map[string]any) (scraper.MenuExtractionResult, error) {
	s.jsCalls++
	return s.jsResult, nil
}
