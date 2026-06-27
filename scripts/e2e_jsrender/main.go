// Command e2e_jsrender runs the detector's generic JS render-and-re-cascade
// path against JS-rendered NYC restaurants, WITHOUT Weaviate/embed.
//
// It uses a headless Chrome (chromedp) to render each page, then re-runs the
// text/image cascade on the hydrated DOM — the Phase 3 path that needs no
// per-site webagent adapter. Prerequisites: Google Chrome / Chromium
// installed (chromedp finds it automatically), a running OpenAI-compatible
// LLM at --llm-url with --llm-model loaded, and network access to the sites.
//
// Usage:
//
//	go run ./scripts/e2e_jsrender [--llm-url URL] [--llm-model MODEL] [--timeout 40m] [--v]
//
// Exit code 0 = all sites met expectations; 1 = at least one failed.
// See docs/plans/vision-extraction-gaps-plan.md (Phase 3) and
// docs/guides/vision-extraction.md for background.
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

// jsSite is a site expected to need JS rendering (raw HTML is a JS shell or
// boilerplate with no menu). After rendering, the re-cascade should find the
// menu in the hydrated DOM. minItems/maxItems bound the acceptable text-pass
// item count on the rendered HTML; the assertion enforces it.
type jsSite struct {
	name     string
	url      string
	minItems int
	maxItems int
	notes    string
}

// sites is the subset of the 9 NYC restaurants that need JS rendering (the
// plan measured these as 0 via the image path). WORLD SPA is the canonical
// JS-rendered case. Expected counts will be tightened after the first run.
var sites = []jsSite{
	{
		name:     "WORLD SPA",
		url:      "https://worldspa.com/dining/",
		minItems: 1, // menu_highlights lists cold/hot appetizers + breakfast
		maxItems: 200,
		notes:    "JS-rendered dining page; menu hydrates client-side",
	},
}

func main() {
	llmURL := flag.String("llm-url", "http://localhost:8000/v1", "Base URL for the OpenAI-compatible LLM endpoint (include /v1)")
	llmModel := flag.String("llm-model", "qwen3-vl", "LLM model name")
	timeout := flag.Duration("timeout", 40*time.Minute, "Overall test deadline")
	verbose := flag.Bool("v", false, "Print first 20 extracted items per site")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ex, err := scraper.NewOpenAICompatExtractor(*llmURL, *llmModel, "", "none")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build extractor: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Headless Chrome fetcher: HTTPFetcher for the raw fetch + ChromeRenderedFetcher
	// for FetchRendered. Shared across sites (browser launches lazily).
	cf := scraper.NewChromeRenderedFetcher(ctx, true) // ignore robots for the probe
	defer func() { cf.Close() }()

	fmt.Printf("FODMAP js-render e2e — %d sites, llm=%s model=%s timeout=%s\n",
		len(sites), *llmURL, *llmModel, *timeout)
	fmt.Printf("%-20s %-42s %6s %6s %6s %s\n", "RESTAURANT", "URL", "RAW", "RND", "OK?", "NOTES")
	fmt.Println(strings.Repeat("-", 122))

	failed := 0
	for _, s := range sites {
		if ok := runOne(ctx, ex, cf, s, *verbose); !ok {
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

// runOne runs the generic render-and-re-cascade against one site. Returns ok.
func runOne(ctx context.Context, ex *scraper.OpenAICompatExtractor, cf *scraper.ChromeRenderedFetcher, s jsSite, verbose bool) bool {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("%-20s %-42s %6s %6s %6s panic: %v\n", s.name, trunc(s.url, 40), "", "", "FAIL", r)
		}
	}()

	// 1. Raw fetch (HTTPFetcher path) → measure raw text length.
	rawRes, err := cf.Fetch(ctx, s.url)
	if err != nil {
		fmt.Printf("%-20s %-42s %6s %6s %6s raw fetch error: %v\n", s.name, trunc(s.url, 40), "FERR", "", "FAIL", err)
		return false
	}
	rawBytes, _ := io.ReadAll(rawRes.Body)
	_ = rawRes.Body.Close()
	rawMD, _ := scraper.ConvertHTMLToMarkdown(strings.NewReader(string(rawBytes)), rawRes.ContentType)
	rawCount := len([]rune(strings.TrimSpace(rawMD)))

	// 2. Render + re-cascade on the hydrated HTML.
	rRes, err := cf.FetchRendered(ctx, s.url)
	if err != nil {
		fmt.Printf("%-20s %-42s %6d %6s %6s render error: %v\n", s.name, trunc(s.url, 40), rawCount, "RERR", "FAIL", err)
		return false
	}
	rBytes, _ := io.ReadAll(rRes.Body)
	_ = rRes.Body.Close()
	rMD, _ := scraper.ConvertHTMLToMarkdown(strings.NewReader(string(rBytes)), rRes.ContentType)

	// 3. Text pass on the rendered HTML.
	res, err := ex.Extract(ctx, rMD)
	if err != nil {
		fmt.Printf("%-20s %-42s %6d %6d %6s text extract error: %v\n", s.name, trunc(s.url, 40), rawCount, 0, "FAIL", err)
		return false
	}
	itemCount := len(res.Items)

	// 4. Assert.
	ok := itemCount >= s.minItems && itemCount <= s.maxItems
	status := "ok"
	if !ok {
		status = fmt.Sprintf("FAIL(want %d-%d)", s.minItems, s.maxItems)
	}
	notes := s.notes
	if itemCount > 0 {
		notes = fmt.Sprintf("%s; %d items extracted", s.notes, itemCount)
	}

	fmt.Printf("%-20s %-42s %6d %6d %6s %s\n",
		s.name, trunc(s.url, 40), rawCount, itemCount, status, notes)

	if verbose && itemCount > 0 {
		limit := 20
		if len(res.Items) < limit {
			limit = len(res.Items)
		}
		for i := 0; i < limit; i++ {
			it := res.Items[i]
			ing := ""
			if len(it.StatedIngredients) > 0 {
				ing = " [" + strings.Join(it.StatedIngredients, ", ") + "]"
			}
			fmt.Printf("    - %s%s\n", it.DishName, ing)
		}
		if len(res.Items) > 20 {
			fmt.Printf("    ... (%d more)\n", len(res.Items)-20)
		}
		if res.RestaurantName != "" {
			fmt.Printf("    restaurant: %s\n", res.RestaurantName)
		}
	}
	return ok
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
