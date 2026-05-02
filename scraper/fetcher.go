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
)

// FetchResult holds the raw output of a page fetch.
type FetchResult struct {
	URL         string
	Body        []byte
	ContentType string // "html", "pdf", "image"
	StatusCode  int
}

// HasMenuContent returns true when the page body appears to contain menu data.
// Used to decide whether to fall through to chromedp.
func (f *FetchResult) HasMenuContent() bool {
	if len(f.Body) == 0 {
		return false
	}
	lower := strings.ToLower(string(f.Body))
	keywords := []string{"menu", "appetizer", "entree", "entrée", "dessert", "prix fixe", "dish", "plate"}
	hits := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			hits++
		}
	}
	return hits >= 2
}

// Fetcher handles two-tier page retrieval: fast HTTP (Tier 1) and headless
// Chromium via chromedp (Tier 2) for JavaScript-heavy sites.
type Fetcher struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewFetcher creates a Fetcher with sensible defaults.
func NewFetcher(logger *slog.Logger) *Fetcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fetcher{
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		logger: logger,
	}
}

// Fetch retrieves a URL using Tier 1 (plain HTTP). Falls back to Tier 2
// (chromedp) when the page appears to be a JavaScript SPA.
// If a direct URL to a PDF or image is detected, it downloads it as-is.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	// --- Tier 1: Plain HTTP ---
	result, err := f.fetchHTTP(ctx, rawURL)
	if err != nil {
		f.logger.Warn("tier-1 fetch failed, trying chromedp", "url", rawURL, "error", err)
		return f.fetchChromedp(ctx, rawURL)
	}

	// If the page looks like a rendered SPA skeleton (< 2KB of text, no menu
	// keywords), escalate to chromedp.
	if result.ContentType == "html" && !result.HasMenuContent() && len(result.Body) < 50_000 {
		f.logger.Info("page may be SPA, trying chromedp", "url", rawURL, "bytes", len(result.Body))
		if chromedpResult, err2 := f.fetchChromedp(ctx, rawURL); err2 == nil {
			return chromedpResult, nil
		} else {
			f.logger.Warn("chromedp also failed, using tier-1 result", "url", rawURL, "error", err2)
		}
	}

	return result, nil
}

// fetchHTTP performs a plain HTTP GET and classifies the response.
func (f *Fetcher) fetchHTTP(ctx context.Context, rawURL string) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	// Mimic a real browser User-Agent to reduce bot blocking.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FodmapBot/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,*/*")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MB cap
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	ct := classifyContentType(resp.Header.Get("Content-Type"), body)
	return &FetchResult{
		URL:         resp.Request.URL.String(), // final URL after redirects
		Body:        body,
		ContentType: ct,
		StatusCode:  resp.StatusCode,
	}, nil
}

// fetchChromedp uses headless Chromium to render a JavaScript-heavy page.
// Requires Chrome/Chromium to be installed on the system.
// Returns the rendered HTML as the body.
func (f *Fetcher) fetchChromedp(ctx context.Context, rawURL string) (*FetchResult, error) {
	// chromedp import is kept behind a build tag to keep the binary lean when
	// headless Chrome is not available. The stub below returns an error so the
	// agent falls back gracefully to the Tier 1 result or Ollama vision.
	//
	// To enable: implement fetchChromedpImpl in fetcher_chromedp.go with build
	// tag //go:build chromedp and replace this call.
	return nil, fmt.Errorf("chromedp not enabled in this build (install chromedp tag to enable)")
}

// DiscoverMenuURL attempts to find a menu-specific URL from a restaurant
// homepage by following links that contain menu-related keywords.
func (f *Fetcher) DiscoverMenuURL(ctx context.Context, homepageURL string) (string, error) {
	result, err := f.fetchHTTP(ctx, homepageURL)
	if err != nil {
		return "", err
	}
	if result.ContentType != "html" {
		return homepageURL, nil // It's already a direct menu page.
	}

	// Look for anchor hrefs containing menu keywords.
	body := string(result.Body)
	menuKeywords := []string{"menu", "food", "drinks", "eat", "dine", "cuisine"}

	for _, kw := range menuKeywords {
		// Simple heuristic: find href="..." containing the keyword.
		needle := `href="`
		idx := 0
		for {
			i := strings.Index(body[idx:], needle)
			if i < 0 {
				break
			}
			i += idx + len(needle)
			end := strings.Index(body[i:], `"`)
			if end < 0 {
				break
			}
			href := body[i : i+end]
			if strings.Contains(strings.ToLower(href), kw) && !strings.HasPrefix(href, "#") {
				resolved := resolveURL(homepageURL, href)
				if resolved != "" {
					return resolved, nil
				}
			}
			idx = i
		}
	}

	// No menu link found — use the homepage itself.
	return homepageURL, nil
}

// ---- helpers ----

// classifyContentType returns "html", "pdf", or "image" based on the
// Content-Type header and, if needed, magic bytes.
func classifyContentType(header string, body []byte) string {
	h := strings.ToLower(header)
	if strings.Contains(h, "pdf") {
		return "pdf"
	}
	if strings.Contains(h, "image/") {
		return "image"
	}
	if strings.Contains(h, "html") {
		return "html"
	}
	// Sniff magic bytes.
	if bytes.HasPrefix(body, []byte("%PDF")) {
		return "pdf"
	}
	if bytes.HasPrefix(body, []byte("\x89PNG")) || bytes.HasPrefix(body, []byte("\xFF\xD8\xFF")) {
		return "image"
	}
	return "html" // Default assumption.
}

// resolveURL resolves a possibly-relative href against a base URL.
// Returns an empty string if the href is clearly not an HTTP URL.
func resolveURL(base, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "/") {
		// Extract scheme + host from base.
		for _, scheme := range []string{"https://", "http://"} {
			if strings.HasPrefix(base, scheme) {
				rest := strings.TrimPrefix(base, scheme)
				host := rest
				if idx := strings.Index(rest, "/"); idx >= 0 {
					host = rest[:idx]
				}
				return scheme + host + href
			}
		}
	}
	return ""
}
