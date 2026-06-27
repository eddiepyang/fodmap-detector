// Command menuscan runs the menu-coverage spike from
// docs/plans/menu-coverage-spike-plan.md: a read-only classification of a
// sample of Astoria+LIC restaurants into the taxonomy
// (no-site/no-menu/image-menu/js-text-deeplink/interaction-required/delivery-only)
// using the REAL detector cascade (text → image → JS-render), plus platform
// detection. It writes a JSON results file + prints a histogram.
//
// Prerequisites: GOOGLE_API_KEY (Gemini GoogleSearch for URL discovery),
// vLLM at --llm-url (qwen3-vl for extraction), Google Chrome (chromedp for the
// JS-render probe). No Weaviate, no Python service, no indexing — read-only.
//
// Usage:
//
//	go run ./scripts/menuscan [--sample path] [--llm-url URL] [--llm-model MODEL] [--timeout 60m] [--v]
//
// Output: scripts/menuscan_results.json + a histogram on stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"fodmap/scraper"

	"google.golang.org/genai"
)

// restaurant is one row from the Socrata sample (subset of fields we use).
// Latitude/Longitude are omitted — the spike discovers via street address, not
// geocoding, and Socrata emits them inconsistently as strings/numbers.
type restaurant struct {
	CAMIS              string `json:"camis"`
	DBA                string `json:"dba"`
	Boro               string `json:"boro"`
	Building           string `json:"building"`
	Street             string `json:"street"`
	Zipcode            string `json:"zipcode"`
	Phone              string `json:"phone"`
	CuisineDescription string `json:"cuisine_description"`
	NTA                string `json:"nta"`
}

// classification is the per-restaurant result row.
type classification struct {
	CAMIS     string `json:"camis"`
	DBA       string `json:"dba"`
	Cuisine   string `json:"cuisine"`
	Platform  string `json:"platform"`
	Class     string `json:"class"` // taxonomy bucket
	MenuURL   string `json:"menu_url,omitempty"`
	URLSource string `json:"url_source,omitempty"` // "gemini" | "probe" | ""
	TextItems int    `json:"text_items"`
	ImgItems  int    `json:"img_items"`
	JSItems   int    `json:"js_items"`
	ImgURL    string `json:"img_url,omitempty"`
	Notes     string `json:"notes,omitempty"`
	// FindMenuImage precision signals
	ImgCandidates  int  `json:"img_candidates"`
	ImgGuardFired  bool `json:"img_guard_fired"` // 0 items from an image candidate
	Flaky          bool `json:"flaky,omitempty"`
}

// taxonomy buckets
const (
	classNoSite             = "no-site"
	classNoMenu             = "no-menu"
	classImageMenu          = "image-menu"
	classJSTextDeeplink     = "js-text-deeplink"
	classInteractionRequired = "interaction-required"
	classDeliveryOnly       = "delivery-only"
)

func main() {
	samplePath := flag.String("sample", "/tmp/spike_sample.json", "Path to the restaurant sample JSON")
	llmURL := flag.String("llm-url", "https://generativelanguage.googleapis.com/v1beta/openai", "OpenAI-compatible LLM endpoint (Gemini's /v1beta/openai by default for speed)")
	llmModel := flag.String("llm-model", "gemini-3-flash-preview", "Vision LLM model name")
	llmAPIKey := flag.String("llm-api-key", os.Getenv("GOOGLE_API_KEY"), "API key for the LLM endpoint (defaults to GOOGLE_API_KEY)")
	timeout := flag.Duration("timeout", 90*time.Minute, "Overall spike deadline")
	verbose := flag.Bool("v", false, "Print one line per restaurant as it runs")
	geminiModel := flag.String("gemini-model", "gemini-2.5-flash", "Gemini model for GoogleSearch discovery")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	// 1. Load sample.
	sample, err := loadSample(*samplePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load sample: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("FODMAP menu-coverage spike — %d restaurants, llm=%s gemini=%s timeout=%s\n",
		len(sample), *llmURL, *geminiModel, *timeout)

	// 2. Build deps.
	ex, err := scraper.NewOpenAICompatExtractor(*llmURL, *llmModel, *llmAPIKey, "none")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build extractor: %v\n", err)
		os.Exit(2)
	}
	fetcher := scraper.NewHTTPFetcher(true) // ignore robots for the probe
	gem, err := genai.NewClient(context.Background(), nil) // uses GOOGLE_API_KEY
	if err != nil {
		fmt.Fprintf(os.Stderr, "gemini client: %v\n", err)
		os.Exit(2)
	}
	cf := scraper.NewChromeRenderedFetcher(context.Background(), true)
	defer func() { cf.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// 3. Classify each restaurant.
	results := make([]classification, 0, len(sample))
	for i, r := range sample {
		fmt.Printf("[%d/%d] %s (%s) ... ", i+1, len(sample), r.DBA, r.CuisineDescription)
		c := classify(ctx, r, gem, *geminiModel, ex, fetcher, cf)
		results = append(results, c)
		// One-line completion log so a long run shows progress + early signal.
		url := c.MenuURL
		if url == "" {
			url = "(no url)"
		} else if len(url) > 60 {
			url = trunc(url, 60)
		}
		fmt.Printf("%-22s txt=%-2d img=%-2d js=%-2d | %s\n",
			c.Class, c.TextItems, c.ImgItems, c.JSItems, url)
		if c.Notes != "" && *verbose {
			fmt.Printf("    notes: %s\n", c.Notes)
		}
	}

	// 4. Write results JSON.
	outPath := "scripts/menuscan_results.json"
	if err := writeResults(outPath, results); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nResults written to %s\n", outPath)

	// 5. Histogram.
	printHistogram(results)
}

// loadSample reads the restaurant sample JSON.
func loadSample(path string) ([]restaurant, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rs []restaurant
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// classify runs the full discovery + cascade classification for one restaurant.
func classify(ctx context.Context, r restaurant, gem *genai.Client, geminiModel string, ex *scraper.OpenAICompatExtractor, fetcher *scraper.HTTPFetcher, cf *scraper.ChromeRenderedFetcher) classification {
	c := classification{
		CAMIS:   r.CAMIS,
		DBA:     r.DBA,
		Cuisine: r.CuisineDescription,
	}
	addr := fmt.Sprintf("%s %s, %s NY", r.Building, r.Street, r.Boro)

	// Step 1: Discover the website URL via Gemini GoogleSearch.
	urls, err := discoverURL(ctx, gem, geminiModel, r.DBA, addr)
	if err != nil {
		c.Class = classNoSite
		c.Notes = fmt.Sprintf("gemini error: %v", err)
		return c
	}
	urls = filterDelivery(urls)
	if len(urls) == 0 {
		c.Class = classNoSite
		c.Notes = "no website found (only delivery/social)"
		return c
	}
	// Pick the first own-domain URL (Gemini grounding is ordered by relevance).
	homeURL := urls[0]
	c.MenuURL = homeURL
	c.URLSource = "gemini"

	// Step 2: Fetch homepage → text path.
	body, ct, err := fetchBytes(ctx, fetcher, homeURL)
	if err != nil {
		c.Class = classNoMenu
		c.Notes = fmt.Sprintf("homepage fetch error: %v", err)
		return c
	}
	c.Platform = detectPlatform(body, homeURL)
	textItems, mdLen := runTextPath(ctx, ex, body, ct)
	c.TextItems = textItems

	// Step 3: Image path — FindMenuImages + ExtractImage (with guard).
	imgCands, _ := scraper.FindMenuImages(body, ct, homeURL)
	c.ImgCandidates = len(imgCands)
	if len(imgCands) > 0 {
		c.ImgURL = imgCands[0]
		imgRes, imgErr := tryImage(ctx, ex, fetcher, imgCands)
		if imgErr != nil {
			c.Notes = fmt.Sprintf("img OCR error: %v", imgErr)
		} else {
			c.ImgItems = len(imgRes.Items)
			if c.ImgItems == 0 {
				c.ImgGuardFired = true
			}
		}
	}

	// Step 4: Probe conventional menu paths for a deep-linkable menu page.
	deepLink := probeMenuPath(ctx, fetcher, homeURL)
	if deepLink != "" {
		// Found a /menu-style page; classify as js-text-deeplink if the homepage
		// text was empty but the deep link has content (rendered or static).
		c.MenuURL = deepLink
		c.URLSource = "probe"
	}

	// Step 5: Decide classification.
	//
	// If the text path on the homepage already found items → image-menu or
	// js-text-deeplink depending on whether it was the homepage text or a
	// deep-link. If image path found items → image-menu. If both empty, try
	// JS-render on the (deep link or homepage) → js-text-deeplink if items
	// appear; else interaction-required.
	if c.TextItems > 0 {
		// Homepage text had a menu → it's reachable as static HTML.
		c.Class = classJSTextDeeplink // still counts as deeplink-able (homepage)
		c.Notes = appendNote(c.Notes, fmt.Sprintf("homepage text: %d items, %d chars", c.TextItems, mdLen))
		return c
	}
	if c.ImgItems > 0 {
		c.Class = classImageMenu
		c.Notes = appendNote(c.Notes, fmt.Sprintf("image OCR: %d items from %s", c.ImgItems, trunc(c.ImgURL, 60)))
		return c
	}

	// Both empty. Try JS-render on the menu URL (deep link if found, else homepage).
	renderURL := c.MenuURL
	rBody, rCT, rErr := fetchRendered(ctx, cf, renderURL)
	if rErr != nil {
		// No JS render available → interaction-required (we can't reach it).
		c.Class = classInteractionRequired
		c.Notes = appendNote(c.Notes, fmt.Sprintf("no items; render failed: %v", rErr))
		return c
	}
	jsItems, _ := runTextPath(ctx, ex, rBody, rCT)
	c.JSItems = jsItems
	if jsItems > 0 {
		c.Class = classJSTextDeeplink
		c.Notes = appendNote(c.Notes, fmt.Sprintf("JS render → text: %d items", jsItems))
		return c
	}
	// JS-render didn't help → interaction-required (menu behind clicks/tabs/location).
	c.Class = classInteractionRequired
	c.Notes = appendNote(c.Notes, "no items from text/image/JS-render")
	return c
}

// runTextPath converts HTML→Markdown and runs the text extractor. Returns
// (itemCount, markdownLen).
func runTextPath(ctx context.Context, ex *scraper.OpenAICompatExtractor, body []byte, ct string) (int, int) {
	md, err := scraper.ConvertHTMLToMarkdown(strings.NewReader(string(body)), ct)
	if err != nil {
		return 0, 0
	}
	if scraper.IsTooNoisy(md) {
		if fb := scraper.TrafilaturaFallback(string(body)); fb != "" {
			md = fb
		}
	}
	res, err := ex.Extract(ctx, md)
	if err != nil {
		return 0, len([]rune(strings.TrimSpace(md)))
	}
	return len(res.Items), len([]rune(strings.TrimSpace(md)))
}

// tryImage fetches + OCRs the top image candidate (bounded to 1 for the spike).
func tryImage(ctx context.Context, ex *scraper.OpenAICompatExtractor, fetcher *scraper.HTTPFetcher, cands []string) (scraper.MenuExtractionResult, error) {
	imgRes, err := fetcher.Fetch(ctx, cands[0])
	if err != nil {
		return scraper.MenuExtractionResult{}, err
	}
	imgBytes, err := io.ReadAll(imgRes.Body)
	_ = imgRes.Body.Close()
	if err != nil {
		return scraper.MenuExtractionResult{}, err
	}
	return ex.ExtractImage(ctx, imgBytes, imgRes.ContentType)
}

// fetchBytes fetches a URL and returns body + content-type.
func fetchBytes(ctx context.Context, f scraper.Fetcher, url string) ([]byte, string, error) {
	res, err := f.Fetch(ctx, url)
	if err != nil {
		return nil, "", err
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return b, res.ContentType, err
}

// fetchRendered fetches a rendered URL via the ChromeRenderedFetcher.
func fetchRendered(ctx context.Context, cf *scraper.ChromeRenderedFetcher, url string) ([]byte, string, error) {
	res, err := cf.FetchRendered(ctx, url)
	if err != nil {
		return nil, "", err
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return b, res.ContentType, err
}

// probeMenuPath tries conventional menu deep-link paths off the homepage host
// and returns the first that returns 200. Returns "" if none found.
func probeMenuPath(ctx context.Context, fetcher *scraper.HTTPFetcher, homeURL string) string {
	// Extract origin.
	origin := originOf(homeURL)
	if origin == "" {
		return ""
	}
	for _, path := range []string{"/menu", "/menus", "/food-menu", "/food", "/drinks"} {
		u := origin + path
		res, err := fetcher.Fetch(ctx, u)
		if err != nil {
			continue
		}
		_ = res.Body.Close()
		// A 200 means the path exists. (We don't check content here; the caller
		// will render+extract from it.)
		return u
	}
	return ""
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// discoverURL uses Gemini GoogleSearch grounding to find the restaurant's
// website. Returns the deduped, filtered URL list (own-domain only).
func discoverURL(ctx context.Context, client *genai.Client, model, dba, addr string) ([]string, error) {
	prompt := fmt.Sprintf(
		"Find the official website URL for the restaurant %q at %s. "+
			"Return only the URL(s), one per line.",
		dba, addr)
	cfg := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
	}
	resp, err := client.Models.GenerateContent(ctx, model, genai.Text(prompt), cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini generate: %w", err)
	}
	var urls []string
	for _, cand := range resp.Candidates {
		if cand.GroundingMetadata == nil {
			continue
		}
		for _, chunk := range cand.GroundingMetadata.GroundingChunks {
			if chunk.Web != nil && chunk.Web.URI != "" {
				urls = append(urls, chunk.Web.URI)
			}
		}
	}
	// GroundingChunks return vertexaisearch.cloud.google.com redirect URLs, not
	// the final destination. Resolve them so we fetch the real restaurant site.
	urls = resolveRedirects(ctx, urls)
	// Fallback: regex URLs from the response text (often the raw domain).
	urlRe := regexp.MustCompile(`https?://[^\s)+"']+`)
	for _, u := range urlRe.FindAllString(resp.Text(), -1) {
		if !strings.Contains(u, "vertexaisearch") && !strings.Contains(u, "google.com") {
			urls = append(urls, u)
		}
	}
	return dedup(urls), nil
}

// resolveRedirects follows HTTP redirects on each URL and returns the final
// destination. URLs that don't redirect pass through unchanged. This converts
// Gemini's vertexaisearch redirect URLs into the real restaurant domains.
func resolveRedirects(ctx context.Context, urls []string) []string {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse // we want the Location header, not the body
	}}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		final := u
		// Follow up to 5 hops manually.
		cur := u
		for i := 0; i < 5; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, cur, nil)
			if err != nil {
				break
			}
			req.Header.Set("User-Agent", "fodmap-spike/0.1")
			resp, err := client.Do(req)
			if err != nil {
				break
			}
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if loc == "" {
				break
			}
			// Resolve relative redirects.
			locURL, err := url.Parse(loc)
			if err != nil {
				break
			}
			if locURL.IsAbs() {
				final = loc
			} else {
				base, _ := url.Parse(cur)
				if base != nil {
					final = base.ResolveReference(locURL).String()
				}
			}
			cur = final
		}
		out = append(out, final)
	}
	return out
}

// filterDelivery drops social/delivery/review platforms, returning own-domain
// URLs only. If only delivery URLs were found, returns empty (caller marks
// delivery-only via the no-site path; a refined version would separate).
func filterDelivery(urls []string) []string {
	var kept []string
	for _, u := range urls {
		l := strings.ToLower(u)
		if strings.Contains(l, "facebook.com") ||
			strings.Contains(l, "instagram.com") ||
			strings.Contains(l, "yelp.com") ||
			strings.Contains(l, "tripadvisor.com") ||
			strings.Contains(l, "google.com/maps") ||
			strings.Contains(l, "ubereats.com") ||
			strings.Contains(l, "doordash.com") ||
			strings.Contains(l, "grubhub.com") ||
			strings.Contains(l, "postmates.com") ||
			strings.Contains(l, "seamless.com") ||
			strings.Contains(l, "delivery.com") {
			continue
		}
		kept = append(kept, u)
	}
	return kept
}

// detectPlatform sniffs CDN hosts, meta generators, and script srcs.
func detectPlatform(body []byte, url string) string {
	s := string(body)
	l := strings.ToLower(s)
	// Squarespace: static.squarespace.com or meta generator
	if strings.Contains(l, "squarespace") || strings.Contains(l, "static1.squarespace.com") {
		return "Squarespace"
	}
	// Wix: wix.com / static.wixstatic.com
	if strings.Contains(l, "wix.com") || strings.Contains(l, "wixstatic") || strings.Contains(l, "parastorage") {
		return "Wix"
	}
	// WordPress: wp-content / wp-includes
	if strings.Contains(l, "wp-content") || strings.Contains(l, "wp-includes") {
		return "WordPress"
	}
	// Toast: toasttab.com / pos.toast
	if strings.Contains(l, "toasttab") || strings.Contains(l, "pos.toast") {
		return "Toast"
	}
	// Square: squareup
	if strings.Contains(l, "squareup") || strings.Contains(l, "square.site") {
		return "Square"
	}
	// Clover: clover.com
	if strings.Contains(l, "clover.com") || strings.Contains(l, "clovercdn") {
		return "Clover"
	}
	// BentoBox: bento.box
	if strings.Contains(l, "bentobox") || strings.Contains(l, "bento.box") {
		return "BentoBox"
	}
	// Popmenu: popmenu
	if strings.Contains(l, "popmenu") {
		return "Popmenu"
	}
	// Shopify
	if strings.Contains(l, "shopify") || strings.Contains(l, "cdn.shopify") {
		return "Shopify"
	}
	// React/Next (custom SPA)
	if strings.Contains(l, "__next") || strings.Contains(l, "_next/static") {
		return "Next.js"
	}
	return "other/unknown"
}

// writeResults writes the classification rows as pretty JSON.
func writeResults(path string, results []classification) error {
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// printHistogram tallies the classification + platform buckets.
func printHistogram(results []classification) {
	classCounts := map[string]int{}
	platformCounts := map[string]int{}
	guardFired := 0
	imgCandTotal := 0
	for _, r := range results {
		classCounts[r.Class]++
		platformCounts[r.Platform]++
		if r.ImgGuardFired {
			guardFired++
		}
		imgCandTotal += r.ImgCandidates
	}
	total := len(results)

	fmt.Println("\n=== Classification histogram ===")
	for _, cls := range []string{classNoSite, classNoMenu, classImageMenu, classJSTextDeeplink, classInteractionRequired, classDeliveryOnly} {
		n := classCounts[cls]
		pct := 100 * float64(n) / float64(total)
		fmt.Printf("  %-22s %3d (%5.1f%%)\n", cls, n, pct)
	}

	fmt.Println("\n=== Platform histogram ===")
	plats := make([]string, 0, len(platformCounts))
	for p := range platformCounts {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	for _, p := range plats {
		fmt.Printf("  %-20s %3d\n", p, platformCounts[p])
	}

	fmt.Println("\n=== FindMenuImage precision signals ===")
	fmt.Printf("  candidates surfaced: %d\n", imgCandTotal)
	fmt.Printf("  guard fired (0 items from candidate): %d\n", guardFired)
	fmt.Printf("  image-menu (real menus via image): %d\n", classCounts[classImageMenu])

	fmt.Println("\n=== Decision ===")
	interaction := classCounts[classInteractionRequired]
	pctInteraction := 100 * float64(interaction) / float64(total)
	if pctInteraction > 15 {
		fmt.Printf("  GO: interaction-required is %.1f%% (>15%% threshold) — green-light the agent\n", pctInteraction)
	} else {
		fmt.Printf("  DEFER: interaction-required is %.1f%% (≤15%% threshold) — platform templates + image OCR win\n", pctInteraction)
	}
}

// dedup removes duplicate strings preserving order.
func dedup(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func appendNote(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "; " + add
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}