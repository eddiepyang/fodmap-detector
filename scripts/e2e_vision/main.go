// Command e2e_vision runs the detector's Phase C vision extraction path
// end-to-end against the 9 live NYC restaurants from the vision-extraction
// gaps plan, WITHOUT Weaviate/embed (extraction-only). It exercises the real
// scraper cascade (fetch → HTML→md → FindMenuImages → text Extract → image
// ExtractImage) and asserts the Phase 1 verification criteria:
//
//   - THRIFT N SIP extracts its trifold menu (image-reachable, >0 items).
//   - DOUGHNUTTERY (the prior 26-fabrication failure) returns 0 and indexes nothing.
//   - All non-menu image sites return 0 and index nothing.
//
// Prerequisites: vLLM (or any OpenAI-compatible vision endpoint) running at
// --llm-url with --llm-model loaded. The detector does NOT need Weaviate or
// the Python scraper service for this test. Network access to the restaurant
// sites is required.
//
// Usage:
//
//	go run ./scripts/e2e_vision [--llm-url URL] [--llm-model MODEL] [--timeout 40m]
//
// Exit code 0 = all assertions passed; 1 = at least one failed. A results table
// is printed to stdout. See docs/plans/vision-extraction-gaps-plan.md for the
// background and docs/guides/vision-extraction.md for the design.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"fodmap/scraper"
)

// site is one restaurant under test. expectItems is the expected item count
// range from the image path (min/max inclusive); the assertion enforces it.
// The 3 sites with no website are listed for completeness (skipped, since
// there is nothing to fetch).
type site struct {
	name     string
	url      string
	minItems int
	maxItems int
	// skipReason, if non-empty, means this site is expected to yield 0 items
	// (no menu image candidate, or guard fired on a non-menu photo). The
	// assertion then only checks that the image path did not fabricate.
	skipReason string
}

// sites is the 6 reachable NYC restaurants from the plan's live test. The 3
// no-website sites (LION'S GATE GRILL, UN COFFEE STUDIO, CAFE JEZERO) are
// omitted — there is nothing to fetch. Expected counts come from the
// 2026-06-27 verified run.
var sites = []site{
	{
		name:       "JETBLUE LOUNGE",
		url:        "https://www.jetblue.com/",
		minItems:   0,
		maxItems:   0,
		skipReason: "brand image (guard fires)",
	},
	{
		name:       "WORLD SPA",
		url:        "https://worldspa.com/dining/",
		minItems:   0,
		maxItems:   0,
		skipReason: "no menu image candidate (JS-rendered; Phase B)",
	},
	{
		name:     "THRIFT N SIP",
		url:      "https://thriftnsipcafe.com/",
		minItems: 1, // real menu image; expect a full extraction (verified 105)
		maxItems: 200,
	},
	{
		name:       "CHICKEN AT LAST",
		url:        "https://www.chickenatlast.com/",
		minItems:   0,
		maxItems:   0,
		skipReason: "hero food photos (guard fires)",
	},
	{
		name:       "DOUGHNUTTERY",
		url:        "https://www.doughnuttery.com/",
		minItems:   0,
		maxItems:   0,
		skipReason: "no menu image candidate — prior 26-fabrication failure must stay 0",
	},
	{
		name:       "NICE DAY CHINESE",
		url:        "https://www.eatniceday.com/",
		minItems:   0,
		maxItems:   0,
		skipReason: "hero food photos (guard fires)",
	},
}

func main() {
	llmURL := flag.String("llm-url", "http://localhost:8000/v1", "Base URL for the OpenAI-compatible vision LLM endpoint (include /v1)")
	llmModel := flag.String("llm-model", "qwen3-vl", "Vision LLM model name")
	timeout := flag.Duration("timeout", 40*time.Minute, "Overall test deadline (single-image vision is ~100-180s per site)")
	verbose := flag.Bool("v", false, "Print first 20 extracted items per site")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ex, err := scraper.NewOpenAICompatExtractor(*llmURL, *llmModel, "", "none")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build extractor: %v\n", err)
		os.Exit(2)
	}
	fetcher := scraper.NewHTTPFetcher(true) // ignore robots for the e2e probe

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Printf("FODMAP vision e2e — %d sites, llm=%s model=%s timeout=%s\n",
		len(sites), *llmURL, *llmModel, *timeout)
	fmt.Printf("%-20s %-42s %6s %6s %6s %s\n", "RESTAURANT", "URL", "TXT", "IMG", "OK?", "NOTES")
	fmt.Println(strings.Repeat("-", 122))

	failed := 0
	for _, s := range sites {
		if ok, _ := runOne(ctx, ex, fetcher, s, *verbose); !ok {
			failed++
		}
	}

	fmt.Println(strings.Repeat("-", 122))
	if failed > 0 {
		fmt.Printf("FAILED: %d/%d sites did not meet expectations\n", failed, len(sites))
		os.Exit(1)
	}
	fmt.Printf("PASS: all %d sites met expectations\n", len(sites))
}

// runOne runs the cascade against one site and prints its row. Returns
// (ok, notes). ok is false if the item count is out of [minItems,maxItems].
func runOne(ctx context.Context, ex *scraper.OpenAICompatExtractor, fetcher *scraper.HTTPFetcher, s site, verbose bool) (bool, []string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("%-20s %-42s %6s %6s %6s panic: %v\n", s.name, trunc(s.url, 40), "ERR", "", "FAIL", r)
		}
	}()

	fetchRes, err := fetcher.Fetch(ctx, s.url)
	if err != nil {
		fmt.Printf("%-20s %-42s %6s %6s %6s fetch error: %v\n", s.name, trunc(s.url, 40), "FERR", "", "FAIL", err)
		return false, nil
	}
	bodyBytes, _ := io.ReadAll(fetchRes.Body)
	_ = fetchRes.Body.Close()
	ct := fetchRes.ContentType

	// JSON-LD fast path (mirror runScrapeWith).
	if !strings.Contains(ct, "pdf") {
		if _, _, ok := scraper.ExtractJSONLD(strings.NewReader(string(bodyBytes))); ok {
			fmt.Printf("%-20s %-42s %6s %6s %6s JSON-LD hit (unexpected for these sites)\n",
				s.name, trunc(s.url, 40), "LD", "", "FAIL")
			return false, nil
		}
	}

	md, err := scraper.ConvertHTMLToMarkdown(strings.NewReader(string(bodyBytes)), ct)
	if err != nil {
		fmt.Printf("%-20s %-42s %6s %6s %6s md error: %v\n", s.name, trunc(s.url, 40), "MERR", "", "FAIL", err)
		return false, nil
	}
	if scraper.IsTooNoisy(md) {
		if fb := scraper.TrafilaturaFallback(string(bodyBytes)); fb != "" {
			md = fb
		}
	}
	tooShort := len([]rune(strings.TrimSpace(md))) < 200
	needsFallback := scraper.IsTooNoisy(md) || strings.TrimSpace(md) == "" || tooShort

	candidates, _ := scraper.FindMenuImages(bodyBytes, ct, s.url)

	textRes, textErr := ex.Extract(ctx, md)
	if textErr != nil {
		slog.Warn("text extract failed", "url", s.url, "err", textErr)
	}
	textCount := len(textRes.Items)

	var imgMenu scraper.MenuExtractionResult
	var notes []string
	if needsFallback && len(candidates) > 0 {
		notes = append(notes, "pre-text image path")
		imgMenu = tryImages(ctx, ex, fetcher, candidates, &notes)
	} else if textErr == nil && textCount == 0 && len(candidates) > 0 {
		notes = append(notes, "G1 fix: post-text-empty image path")
		imgMenu = tryImages(ctx, ex, fetcher, candidates, &notes)
	} else if len(candidates) == 0 {
		notes = append(notes, "no menu image candidate")
	} else {
		notes = append(notes, "text had items")
	}
	imgCount := len(imgMenu.Items)

	ok := imgCount >= s.minItems && imgCount <= s.maxItems
	status := "ok"
	if !ok {
		status = fmt.Sprintf("FAIL(want %d-%d)", s.minItems, s.maxItems)
	}
	if s.skipReason != "" {
		notes = append(notes, s.skipReason)
	}

	fmt.Printf("%-20s %-42s %6d %6d %6s %s\n",
		s.name, trunc(s.url, 40), textCount, imgCount, status, strings.Join(notes, "; "))

	if verbose && imgCount > 0 {
		limit := 20
		if len(imgMenu.Items) < limit {
			limit = len(imgMenu.Items)
		}
		for i := 0; i < limit; i++ {
			it := imgMenu.Items[i]
			ing := ""
			if len(it.StatedIngredients) > 0 {
				ing = " [" + strings.Join(it.StatedIngredients, ", ") + "]"
			}
			fmt.Printf("    - %s%s\n", it.DishName, ing)
		}
		if len(imgMenu.Items) > 20 {
			fmt.Printf("    ... (%d more)\n", len(imgMenu.Items)-20)
		}
		if imgMenu.RestaurantName != "" {
			fmt.Printf("    restaurant: %s\n", imgMenu.RestaurantName)
		}
	}
	return ok, notes
}

// tryImages mirrors cli.extractFromImageURL: tries up to 2 candidates in
// score order, returns the first non-empty extraction, else the last empty one.
func tryImages(ctx context.Context, ex *scraper.OpenAICompatExtractor, fetcher *scraper.HTTPFetcher, candidates []string, notes *[]string) scraper.MenuExtractionResult {
	const maxAttempts = 2
	attempts := len(candidates)
	if attempts > maxAttempts {
		attempts = maxAttempts
	}
	var last scraper.MenuExtractionResult
	for i := 0; i < attempts; i++ {
		imgURL := candidates[i]
		imgRes, err := fetcher.Fetch(ctx, imgURL)
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("img fetch err: %v", err))
			return scraper.MenuExtractionResult{}
		}
		imgBytes, err := io.ReadAll(imgRes.Body)
		_ = imgRes.Body.Close()
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("img read err: %v", err))
			return scraper.MenuExtractionResult{}
		}
		res, err := ex.ExtractImage(ctx, imgBytes, imgRes.ContentType)
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("img OCR err: %v", err))
			return scraper.MenuExtractionResult{}
		}
		last = res
		if len(res.Items) > 0 {
			*notes = append(*notes, fmt.Sprintf("OCR'd %s (%d items)", trunc(imgURL, 50), len(res.Items)))
			return res
		}
		*notes = append(*notes, fmt.Sprintf("0 items from %s (guard)", trunc(imgURL, 50)))
	}
	return last
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
