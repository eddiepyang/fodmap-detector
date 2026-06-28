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

func (r *rendererExtractor) FetchRenderedHTML(_ context.Context, _ string) (scraper.FetchResult, error) {
	r.called = true
	return r.renderResult, r.renderErr
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

func TestFetchWithFallback_404_NoFallback(t *testing.T) {
	// 404 must not trigger the rendered-fetch fallback.
	renderer := &rendererExtractor{}
	fetcher := &stubFetcher{err: &scraper.HTTPStatusError{StatusCode: 404, URL: "https://example.com"}}

	_, _, err := fetchWithFallback(context.Background(), "https://example.com", fetcher, renderer)
	if err == nil {
		t.Fatal("expected error")
	}
	if renderer.called {
		t.Error("rendered-fetch must NOT be called on 404")
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
