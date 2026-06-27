package scraper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ChromeRenderedFetcher is a Fetcher that also renders JavaScript via a
// headless Chrome (through chromedp). Fetch uses a plain HTTP client (no JS);
// FetchRendered navigates to the URL, waits for the page to settle, and returns
// the hydrated outerHTML so the caller can re-run the text/image cascade on
// content that only appears after client-side hydration.
//
// It implements RenderedFetcher. The browser is started lazily on the first
// FetchRendered call and shared across calls in the same process; cancel the
// context passed to NewChromeRenderedFetcher (or call Close) to release it.
//
// Browser discovery: chromedp's default allocator finds Chrome/Chromium in the
// standard locations (Google Chrome on macOS at
// /Applications/Google Chrome.app/...). If no browser is found, FetchRendered
// returns a clear error so the caller can fall back to the non-rendered path
// rather than failing the whole scrape.
type ChromeRenderedFetcher struct {
	HTTPFetcher
	allocCancel context.CancelFunc
	browserCtx  context.Context // allocator-derived; per-call task contexts branch off this
}

// NewChromeRenderedFetcher builds a RenderedFetcher. The provided ctx is the
// root for the chromedp task context; canceling it releases the browser. The
// HTTPFetcher (used for the non-rendered Fetch path) honors ignoreRobots.
func NewChromeRenderedFetcher(ctx context.Context, ignoreRobots bool) *ChromeRenderedFetcher {
	// Headless, no sandbox flags needed on macOS; disable GPU/image loading to
	// keep the rendered HTML lean and the run fast.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	return &ChromeRenderedFetcher{
		HTTPFetcher: HTTPFetcher{Client: &http.Client{Timeout: 30 * time.Second}, IgnoreRobots: ignoreRobots},
		allocCancel: cancel,
		browserCtx:  allocCtx,
	}
}

// Close releases the headless browser. Safe to call multiple times.
func (c *ChromeRenderedFetcher) Close() {
	if c.allocCancel != nil {
		c.allocCancel()
		c.allocCancel = nil
	}
}

// FetchRendered navigates to rawURL, waits for network activity to settle,
// then returns the hydrated document's outerHTML as text/html. The wait is
// bounded by renderTimeout; a slow page that never settles returns its
// current DOM rather than hanging forever.
func (c *ChromeRenderedFetcher) FetchRendered(ctx context.Context, rawURL string) (FetchResult, error) {
	const renderTimeout = 30 * time.Second
	// Per-call task context: branch off the shared browser (allocator) context
	// and apply the render timeout so a stuck page can't hang the scrape. The
	// caller's ctx cancellation also propagates via the deadline chain.
	timed, cancel := context.WithTimeout(c.browserCtx, renderTimeout)
	defer cancel()
	taskCtx, taskCancel := chromedp.NewContext(timed)
	defer taskCancel()

	var outerHTML string
	err := chromedp.Run(taskCtx,
		network.Enable(),
		chromedp.Navigate(rawURL),
		// Wait for the body to exist, then sleep briefly to let post-load XHRs
		// settle (chromedp has no built-in networkidle waiter). The page's own
		// hydration is what populates the menu DOM we re-cascade on.
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(800*time.Millisecond),
		chromedp.OuterHTML("html", &outerHTML, chromedp.ByQuery),
	)
	if err != nil {
		// A common failure is "page closed" or a browser launch error; surface
		// it clearly so the caller can fall back to the non-rendered cascade.
		if strings.Contains(err.Error(), "failed to start") || strings.Contains(err.Error(), "exec:") {
			return FetchResult{}, fmt.Errorf("headless browser unavailable: %w", err)
		}
		return FetchResult{}, fmt.Errorf("rendering %s: %w", rawURL, err)
	}
	if outerHTML == "" {
		return FetchResult{}, fmt.Errorf("rendering %s: empty document", rawURL)
	}
	slog.Info("js render done", "url", rawURL, "bytes", len(outerHTML))
	return FetchResult{
		Body:        io.NopCloser(bytes.NewReader([]byte(outerHTML))),
		ContentType: "text/html; charset=utf-8",
	}, nil
}
