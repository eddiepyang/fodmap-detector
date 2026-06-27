package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"fodmap/scraper"
	"fodmap/search"
)

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
	body := "<html><body><h1>Menu</h1><ul><li>Pizza</li></ul></body></html>"
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

func TestRunScrapeWith_NoisyHTMLWithoutJSRenderFallsBackToExtract(t *testing.T) {
	ex := &extractorStub{
		result: scraper.MenuExtractionResult{Items: []scraper.MenuEntry{{DishName: "x"}}},
	}
	// enableJSRender=false → noisy HTML should NOT route to webagent; it should
	// fall through to the normal ex.Extract path (with trafilatura fallback).
	err := runScrapeWith(
		context.Background(),
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
