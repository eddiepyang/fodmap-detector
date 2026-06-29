package menusearch

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"fodmap/pipeline"
	"fodmap/scraper"
)

// menuPathKeywords are path/text substrings that suggest a link leads to a menu
// page.  We check these before doing any network probing so that only plausible
// candidates spend a network round-trip on menuSignalFilter.
var menuPathKeywords = []string{
	"menu", "lunch", "dinner", "food", "brunch", "breakfast", "drink",
	"cocktail", "wine", "beer", "dessert", "appetizer", "entree", "entrée",
	".pdf",
}

// registrableDomain returns a best-effort "registrable domain" from a hostname.
// It strips a leading "www." and returns the last two labels (e.g. "example.com").
// This is deliberately conservative: it may lump some ccTLD second-level domains
// (e.g. "co.uk") together, but that is safe — we only use it to constrain
// directory fanout to the restaurant's own site.
func registrableDomain(hostname string) string {
	h := strings.ToLower(hostname)
	// Strip scheme if accidentally included.
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	// Strip port.
	if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	// Strip leading www.
	h = strings.TrimPrefix(h, "www.")
	// Use the last two dot-separated labels as the registrable domain.
	parts := strings.Split(h, ".")
	if len(parts) <= 2 {
		return h
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// extractMenuSubURLs parses anchor hrefs from rendered HTML and returns a
// filtered, deduplicated list of candidate sub-URLs that are likely to be menu
// pages.  The filtering pipeline is:
//
//  1. Resolve relative hrefs against baseURL.
//  2. Drop non-http(s) schemes, fragment-only links, and the root URL itself.
//  3. Keep only same-registrable-domain URLs (same-domain PDFs are allowed).
//  4. Pre-filter by path/text keyword (menu, lunch, .pdf, …).
//  5. Drop hard-blocked hosts (isNonMenuURL) and delivery platforms (isDeliveryURL).
//  6. Dedup.
//
// No network probing is done here; callers run menuSignalFilter afterward.
func extractMenuSubURLs(rendered []byte, baseURL string) []string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	baseDomain := registrableDomain(base.Hostname())

	doc, parseErr := html.Parse(bytes.NewReader(rendered))
	if parseErr != nil {
		return nil
	}

	// Collect all hrefs from <a> tags.
	var hrefs []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && a.Val != "" {
					hrefs = append(hrefs, a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Normalised root URL for self-loop detection.
	rootNorm := normaliseURL(base)

	var candidates []string
	for _, href := range hrefs {
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)

		// Only http/https.
		if abs.Scheme != "http" && abs.Scheme != "https" {
			continue
		}

		// Drop fragment-only variations of the root (e.g. "#section").
		abs.Fragment = ""

		// Drop self-loops.
		if normaliseURL(abs) == rootNorm {
			continue
		}

		// Same-registrable-domain constraint.
		if registrableDomain(abs.Hostname()) != baseDomain {
			continue
		}

		absStr := abs.String()

		// Pre-filter: path or raw href must contain a menu-ish keyword.
		if !hasMenuPathKeyword(abs.Path, href) {
			continue
		}

		// Drop hard-blocked and delivery hosts.
		if isNonMenuURL(absStr) || isDeliveryURL(absStr) {
			continue
		}

		candidates = append(candidates, absStr)
	}

	return dedup(candidates)
}

// normaliseURL returns a canonical string for self-loop detection: lowercase
// scheme+host, path trimmed of trailing slash, no query or fragment.
func normaliseURL(u *url.URL) string {
	c := *u
	c.Scheme = strings.ToLower(c.Scheme)
	c.Host = strings.ToLower(c.Host)
	c.Path = strings.TrimRight(c.Path, "/")
	c.RawQuery = ""
	c.Fragment = ""
	return c.String()
}

// hasMenuPathKeyword returns true when the URL path or the original href
// contains at least one menu-ish keyword.
func hasMenuPathKeyword(path, href string) bool {
	lower := strings.ToLower(path + " " + href)
	for _, kw := range menuPathKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// webagentMaxConcurrency reads WEBAGENT_MAX_FETCH_CONCURRENCY from the
// environment and returns it, defaulting to 4 if unset or invalid.
func webagentMaxConcurrency() int {
	const defaultConcurrency = 4
	v := os.Getenv("WEBAGENT_MAX_FETCH_CONCURRENCY")
	if v == "" {
		return defaultConcurrency
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultConcurrency
	}
	return n
}

// fanoutSubURLResult holds the outcome of one sub-URL extraction.
type fanoutSubURLResult struct {
	url    string
	result *scraper.MenuExtractionResult
	// rawBody is written to bronze (best-effort); may be nil.
	rawBody []byte
}

// extractSubURLs runs ExtractMenu on each candidate concurrently (semaphore
// capped at webagentMaxConcurrency), tolerating per-URL failures.  It returns
// the successful results plus their raw bodies for bronze writes.
func extractSubURLs(
	ctx context.Context,
	candidates []string,
	fetcher scraper.Fetcher,
	ex scraper.Extractor,
	enableVision bool,
	usePdftotext bool,
	webagentAdapter string,
	logger *slog.Logger,
) []fanoutSubURLResult {
	concurrency := webagentMaxConcurrency()
	results := make([]fanoutSubURLResult, len(candidates))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, u := range candidates {
		wg.Add(1)
		go func(idx int, rawURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res, body, err := pipeline.ExtractMenu(ctx, rawURL, fetcher, ex, enableVision, usePdftotext, webagentAdapter)
			if err != nil {
				logger.Warn("directory fanout: sub-URL extraction error", "url", rawURL, "error", err)
				return
			}
			if res == nil || len(res.Items) == 0 {
				logger.Info("directory fanout: sub-URL yielded 0 items", "url", rawURL)
				return
			}
			logger.Info("directory fanout: sub-URL extracted items", "url", rawURL, "count", len(res.Items))
			results[idx] = fanoutSubURLResult{url: rawURL, result: res, rawBody: body}
		}(i, u)
	}
	wg.Wait()

	// Collect non-nil results in order.
	var out []fanoutSubURLResult
	for _, r := range results {
		if r.result != nil && len(r.result.Items) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// buildDirectoryClient returns an *http.Client for menuSignalFilter probing
// during directory fanout.  We use a short timeout to bound the pre-filter
// network time; non-2xx and timed-out URLs are kept anyway.
func buildDirectoryClient() *http.Client {
	return &http.Client{Timeout: 8 * time.Second}
}
