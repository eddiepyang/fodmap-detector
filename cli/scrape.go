package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var scrapeCmd = &cobra.Command{
	Use:   "scrape <url>",
	Short: "Scrape a restaurant menu page and index it for FODMAP chat.",
	Args:  cobra.ExactArgs(1),
	RunE:  runScrape,
}

func init() {
	rootCmd.AddCommand(scrapeCmd)

	// Storage backend
	scrapeCmd.Flags().String("store", "weaviate", "Storage backend: weaviate | postgres | pinecone")
	scrapeCmd.Flags().String("weaviate", "localhost:8090", "Weaviate host:port")
	scrapeCmd.Flags().String("weaviate-scheme", "http", "Weaviate scheme (http or https)")
	scrapeCmd.Flags().String("weaviate-api-key", "", "Weaviate API key")
	scrapeCmd.Flags().String("postgres-dsn", "", "PostgreSQL DSN (required if --store=postgres)")
	scrapeCmd.Flags().String("pinecone-api-key", "", "Pinecone API key")
	scrapeCmd.Flags().String("pinecone-index-host", "", "Pinecone index host")

	// Embedding backend
	scrapeCmd.Flags().String("embed-backend", "ollama", "Embedding backend: ollama | vectorizer")
	scrapeCmd.Flags().String("ollama-url", "http://localhost:11434", "Ollama base URL")
	scrapeCmd.Flags().String("ollama-model", "nomic-embed-text", "Ollama embedding model")
	scrapeCmd.Flags().String("vectorizer", "", "HTTP vectorizer host:port")

	// LLM extraction backend — any OpenAI-compatible endpoint.
	// --llm-url must include the version segment:
	//   Ollama:  http://localhost:11434/v1
	//   vLLM:   http://localhost:8000/v1
	//   OpenAI: https://api.openai.com/v1
	//   Gemini: https://generativelanguage.googleapis.com/v1beta/openai
	scrapeCmd.Flags().String("llm-url", "http://localhost:11434/v1", "Base URL for the OpenAI-compatible LLM endpoint (include version segment)")
	scrapeCmd.Flags().String("llm-model", "qwen3.6:35b-mlx", "LLM model name")
	scrapeCmd.Flags().String("llm-api-key", "", "API key for cloud backends (OpenAI, Gemini, etc.)")
	scrapeCmd.Flags().String("llm-reasoning-effort", "none", "Reasoning effort: none | low | medium | high (none = fastest, cost-optimal for Gemini)")

	// Fetch options
	scrapeCmd.Flags().Bool("ignore-robots", false, "Skip robots.txt check")
	scrapeCmd.Flags().Bool("enable-js-render", false, "Tier 3: render JS-only pages via chromedp (currently a no-op; will route to the scraper service's webagent in Phase B)")
	scrapeCmd.Flags().Bool("enable-vision", false, "Send PDFs/images to the vision LLM instead of text extraction (pure-Go fallback; mutually alternative with --extractor-url)")
	scrapeCmd.Flags().Bool("pdftotext", false, "Use system pdftotext (poppler) for PDF text extraction")

	// Scraper service (Phase A): route PDF/OCR extraction to the Python service.
	// When set, PDFs that fail the local text-layer/pdftotext cascade are sent to
	// the service instead of the pure-Go vision path. HTML/text extraction still
	// uses the local --llm-* extractor. PDF structuring is then owned by the
	// service's SCRAPER_LLM_* / OCR backend config — the detector's --llm-model
	// / --llm-url only drive the HTML/text path (embeddings remain on --ollama-*).
	scrapeCmd.Flags().String("extractor-url", "", "Base URL of the Python scraper service for PDF/OCR (e.g. http://localhost:8765); empty = pure-Go default")
	scrapeCmd.Flags().Duration("extractor-page-timeout", 2*time.Minute, "Per-page request timeout when calling the scraper service (OCR VLM is slow)")
	scrapeCmd.Flags().Duration("extractor-pdf-timeout", 10*time.Minute, "Overall PDF deadline when calling the scraper service (multi-page scans can run for minutes)")

	// webagent (Phase B): route JS-rendered pages to the service's webagent
	// endpoint. Requires --extractor-url + a pre-compiled adapter (offline
	// authoring step in the Python repo). --enable-js-render gates this path.
	scrapeCmd.Flags().String("webagent-adapter", "", "webagent adapter ID (site/target) for JS-rendered pages, e.g. 'amc/seats'; requires --enable-js-render + --extractor-url")
}

func runScrape(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	ctx := context.Background()

	ignoreRobots, _ := cmd.Flags().GetBool("ignore-robots")
	enableVision, _ := cmd.Flags().GetBool("enable-vision")
	enableJSRender, _ := cmd.Flags().GetBool("enable-js-render")
	usePdftotext, _ := cmd.Flags().GetBool("pdftotext")

	llmURL := viper.GetString("llm-url")
	llmModel := viper.GetString("llm-model")
	llmAPIKey := viper.GetString("llm-api-key")
	llmReasoningEffort := viper.GetString("llm-reasoning-effort")

	ollamaURL := viper.GetString("ollama-url")
	ollamaModel := viper.GetString("ollama-model")
	vectorizerHost := viper.GetString("vectorizer")

	weaviateHost := viper.GetString("weaviate")
	weaviateScheme := viper.GetString("weaviate-scheme")
	weaviateAPIKey := viper.GetString("weaviate-api-key")

	extractorURL := viper.GetString("extractor-url")
	pageTimeout := viper.GetDuration("extractor-page-timeout")
	pdfTimeout := viper.GetDuration("extractor-pdf-timeout")
	webagentAdapter := viper.GetString("webagent-adapter")

	// Build embedder.
	var embedder search.Embedder
	if vectorizerHost != "" {
		if _, _, err := net.SplitHostPort(vectorizerHost); err != nil {
			return fmt.Errorf("invalid --vectorizer value %q: must be host:port", vectorizerHost)
		}
		embedder = search.NewVectorizerClient("http://" + vectorizerHost)
		slog.Info("using HTTP vectorizer", "host", vectorizerHost)
	} else {
		embedder = search.NewOllamaEmbedder(ollamaURL, ollamaModel)
		slog.Info("using Ollama embedder", "model", ollamaModel, "url", ollamaURL)
	}
	defer func() { _ = embedder.Close() }()

	// Build MenuStore.
	store, err := buildMenuStore(ctx, weaviateHost, weaviateScheme, weaviateAPIKey, embedder)
	if err != nil {
		return fmt.Errorf("building store: %w", err)
	}
	if err := store.EnsureMenuSchema(ctx); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	// Build extractor. When --extractor-url is set, wrap the OpenAI-compat
	// extractor in a ServiceExtractor so PDFs route to the Python service
	// while HTML/text still uses the local LLM. When empty, the plain
	// OpenAICompatExtractor is used (pure-Go default, no behavior change).
	var ex scraper.Extractor
	oaex, err := scraper.NewOpenAICompatExtractor(llmURL, llmModel, llmAPIKey, llmReasoningEffort)
	if err != nil {
		return fmt.Errorf("building extractor: %w", err)
	}
	if extractorURL != "" {
		ex = scraper.NewServiceExtractor(extractorURL, oaex, pageTimeout, pdfTimeout)
		slog.Info("using scraper service for PDF/OCR", "url", extractorURL,
			"page_timeout", pageTimeout, "pdf_timeout", pdfTimeout)
	} else {
		ex = oaex
	}

	fetcher := scraper.NewHTTPFetcher(ignoreRobots)

	return runScrapeWith(ctx, rawURL, fetcher, ex, store, embedder,
		enableVision, enableJSRender, usePdftotext, webagentAdapter)
}

// runScrapeWith is the testable core of the scrape command. All dependencies
// are injected so tests can pass stubs directly. webagentAdapter is the
// "site/target" ID for the JS-render path (Phase B); empty disables it.
func runScrapeWith(
	ctx context.Context,
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
	slog.Info("scraping URL", "url", rawURL)

	// ── Tier 0: JSON-LD fast-path ─────────────────────────────────────────────
	fetchRes, err := fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	bodyBytes, err := io.ReadAll(fetchRes.Body)
	_ = fetchRes.Body.Close()
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}

	ct := fetchRes.ContentType
	var result scraper.MenuExtractionResult
	var jsonldMeta scraper.JSONLDMeta
	var usedJSONLD bool

	if !strings.Contains(ct, "pdf") {
		items, meta, ok := scraper.ExtractJSONLD(bytes.NewReader(bodyBytes))
		jsonldMeta = meta
		if ok {
			slog.Info("Tier 0: JSON-LD menu found", "items", len(items))
			result = scraper.MenuExtractionResult{
				RestaurantName: meta.RestaurantName,
				City:           meta.City,
				State:          meta.State,
				SourceURL:      rawURL,
				ScrapedAtUTC:   time.Now().UTC().Format(time.RFC3339),
				Items:          items,
			}
			usedJSONLD = true
		}
	}

	// ── Tier 1: HTML/PDF → LLM extraction ────────────────────────────────────
	if !usedJSONLD {
		var pageText string

		// pdfResult is set when a PDFExtractor (service path) returned a fully
		// structured MenuExtractionResult directly; in that case we skip the
		// second ex.Extract pass below.
		var pdfResult *scraper.MenuExtractionResult

		if strings.Contains(ct, "pdf") {
			text, structured, err := extractPDF(ctx, bodyBytes, usePdftotext, enableVision, ex)
			if err != nil {
				return fmt.Errorf("PDF extraction: %w", err)
			}
			if structured != nil {
				pdfResult = structured
			} else {
				pageText = text
			}
		} else {
			md, err := scraper.ConvertHTMLToMarkdown(bytes.NewReader(bodyBytes), ct)
			if err != nil {
				return fmt.Errorf("HTML conversion: %w", err)
			}
			noisy := scraper.IsTooNoisy(md)
			if noisy {
				slog.Warn("HTML→Markdown output is noisy, falling back to trafilatura", "url", rawURL)
				if fallback := scraper.TrafilaturaFallback(string(bodyBytes)); fallback != "" {
					md = fallback
				} else {
					slog.Warn("trafilatura fallback produced no output, using original conversion", "url", rawURL)
				}
			}
			// If the content is still noisy/empty/too-short after both
			// conversions, try the service-backed fallbacks. The min-content
			// threshold mirrors the PDF text-layer guard — a page with fewer
			// than ~200 chars of real content is likely image- or JS-rendered.
			tooShort := len([]rune(strings.TrimSpace(md))) < 200
			needsFallback := noisy && (scraper.IsTooNoisy(md) || strings.TrimSpace(md) == "" || tooShort)

			if needsFallback {
				// Phase C: check for a menu image embedded in the HTML.
				// Runs before the JS-render check — image-embedded menus are
				// more common than JS-rendered SPAs and don't need an adapter.
				if imgURL, found := scraper.FindMenuImage(bodyBytes, ct, rawURL); found {
					if iex, ok := ex.(scraper.ImageExtractor); ok {
						slog.Info("found menu image; routing to service OCR", "url", rawURL, "img", imgURL)
						imgRes, err := fetcher.Fetch(ctx, imgURL)
						if err != nil {
							return fmt.Errorf("fetching menu image %s: %w", imgURL, err)
						}
						imgBytes, err := io.ReadAll(imgRes.Body)
						_ = imgRes.Body.Close()
						if err != nil {
							return fmt.Errorf("reading menu image %s: %w", imgURL, err)
						}
						imgResult, imgErr := iex.ExtractImage(ctx, imgBytes, imgRes.ContentType)
						if imgErr != nil {
							return fmt.Errorf("service image OCR: %w", imgErr)
						}
						result = imgResult
						result.SourceURL = rawURL
						result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)
						if result.City == "" && jsonldMeta.City != "" {
							result.City = jsonldMeta.City
							result.State = jsonldMeta.State
						}
						if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
							result.RestaurantName = jsonldMeta.RestaurantName
						}
						goto tier1Done
					}
					slog.Warn("page appears to contain a menu image; set --extractor-url to OCR it",
						"url", rawURL, "img", imgURL)
				}

				// Phase B: route JS-only pages to the webagent.
				if enableJSRender && webagentAdapter != "" {
					if jsr, ok := ex.(scraper.JSRenderer); ok {
						slog.Info("HTML too noisy; routing to webagent", "url", rawURL, "adapter", webagentAdapter)
						jsResult, jsErr := jsr.ScrapeJS(ctx, webagentAdapter, map[string]any{
							"url": rawURL,
						})
						if jsErr != nil {
							return fmt.Errorf("webagent JS scrape: %w", jsErr)
						}
						result = jsResult
						result.SourceURL = rawURL
						result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)
						if result.City == "" && jsonldMeta.City != "" {
							result.City = jsonldMeta.City
							result.State = jsonldMeta.State
						}
						if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
							result.RestaurantName = jsonldMeta.RestaurantName
						}
						goto tier1Done
					}
				}
			}
			pageText = md
		}

		if pdfResult != nil {
			// Service path already structured the menu. Skip the LLM pass.
			result = *pdfResult
		} else {
			slog.Info("Tier 1: sending to LLM extractor", "chars", len([]rune(pageText)))
			result, err = ex.Extract(ctx, pageText)
			if err != nil {
				return fmt.Errorf("LLM extraction: %w", err)
			}
		}
		result.SourceURL = rawURL
		result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)

		// Backfill location from JSON-LD metadata when Tier 1 doesn't know it.
		if result.City == "" && jsonldMeta.City != "" {
			result.City = jsonldMeta.City
			result.State = jsonldMeta.State
		}
		if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
			result.RestaurantName = jsonldMeta.RestaurantName
		}

	}

tier1Done:
	if len(result.Items) == 0 {
		slog.Warn("no menu items extracted", "url", rawURL)
		fmt.Printf("No menu items found at %s\n", rawURL)
		return nil
	}

	slog.Info("extracted menu items", "count", len(result.Items), "restaurant", result.RestaurantName)

	// ── Embed + upsert ────────────────────────────────────────────────────────
	items, err := toMenuItems(ctx, result, rawURL, embedder)
	if err != nil {
		return fmt.Errorf("embedding menu items: %w", err)
	}

	if err := store.BatchUpsertMenu(ctx, items); err != nil {
		return fmt.Errorf("upserting menu items: %w", err)
	}

	fmt.Printf("Scraped %d menu items from %q (%s)\n", len(items), result.RestaurantName, rawURL)
	return nil
}

// extractPDF runs the PDF cascade: text-layer → pdftotext → (service | vision).
//
// Returns (text, nil, nil) when a text layer was extracted; the caller feeds
// text to ex.Extract for structuring. Returns ("", result, nil) when a
// PDFExtractor (the service path) produced a fully structured result — the
// caller must skip ex.Extract. Returns ("", nil, err) on failure.
//
// Cascade ordering (per the plan):
//   - If the text-layer/pdftotext cascade yields usable text, return it.
//   - On ErrNeedVision, prefer the service path if ex implements PDFExtractor
//     (--extractor-url); fall back to pure-Go ExtractPDFVision on 503, or when
//     the service is not configured.
func extractPDF(ctx context.Context, pdfBytes []byte, usePdftotext, enableVision bool, ex scraper.Extractor) (string, *scraper.MenuExtractionResult, error) {
	text, err := scraper.ExtractPDFText(pdfBytes, usePdftotext)
	if err == nil {
		return text, nil, nil
	}
	if !errors.Is(err, scraper.ErrNeedVision) {
		return "", nil, err
	}

	// Service path: if ex implements PDFExtractor, route there. On a 503 (OCR
	// backend unavailable), fall back to pure-Go vision if enabled.
	if pex, ok := ex.(scraper.PDFExtractor); ok {
		result, sErr := pex.ExtractPDF(ctx, pdfBytes)
		if sErr == nil {
			// An empty menu comes back as a normal result with zero items; the
			// caller's len(result.Items) == 0 check handles it gracefully.
			return "", &result, nil
		}
		if !scraper.IsBackendUnavailable(sErr) {
			return "", nil, fmt.Errorf("service PDF extraction: %w", sErr)
		}
		slog.Warn("service OCR backend unavailable (503), falling back to pure-Go vision", "err", sErr)
		// Fall through to the vision path below if enabled; otherwise hard fail.
		if !enableVision {
			return "", nil, fmt.Errorf("service OCR backend unavailable (503) and --enable-vision is not set: %w", sErr)
		}
	}

	if !enableVision {
		return "", nil, fmt.Errorf("PDF has no usable text layer; set --extractor-url or --enable-vision")
	}

	// Pure-Go vision fallback.
	oaex, ok := ex.(*scraper.OpenAICompatExtractor)
	if !ok {
		// ServiceExtractor wraps OpenAICompatExtractor; reach for it.
		if sex, ok2 := ex.(*scraper.ServiceExtractor); ok2 {
			oaex = sex.Text()
		}
	}
	if oaex == nil {
		return "", nil, fmt.Errorf("vision path requires --llm-backend openai-compat")
	}
	result, vErr := scraper.ExtractPDFVision(ctx, pdfBytes, oaex)
	if vErr != nil {
		return "", nil, vErr
	}
	// Return the dish names as flat text so the caller's normal flow still works.
	var lines []string
	for _, item := range result.Items {
		lines = append(lines, item.DishName+": "+item.Description)
	}
	return strings.Join(lines, "\n"), nil, nil
}

// menuCollectionNS is the UUID namespace for deterministic menu item IDs.
var menuCollectionNS = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// toMenuItems converts a MenuExtractionResult to []search.MenuItem, embedding
// each item's text vector.
func toMenuItems(ctx context.Context, result scraper.MenuExtractionResult, rawURL string, embedder search.Embedder) ([]search.MenuItem, error) {
	businessID := scraper.BusinessID(rawURL)
	section := scraper.MenuSection(rawURL)
	now := result.ScrapedAtUTC

	texts := make([]string, len(result.Items))
	for i, item := range result.Items {
		parts := []string{"Menu item at " + result.RestaurantName + ": " + item.DishName}
		if item.Description != "" {
			parts = append(parts, item.Description)
		}
		if len(item.StatedIngredients) > 0 {
			parts = append(parts, "Stated ingredients: "+strings.Join(item.StatedIngredients, ", "))
		}
		texts[i] = strings.Join(parts, ". ")
	}

	vectors, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embedding batch: %w", err)
	}

	items := make([]search.MenuItem, len(result.Items))
	for i, entry := range result.Items {
		idKey := businessID + entry.DishName
		id := uuid.NewSHA1(menuCollectionNS, []byte(idKey)).String()
		items[i] = search.MenuItem{
			MenuItemID:         id,
			BusinessID:         businessID,
			MenuSection:        section,
			RestaurantName:     result.RestaurantName,
			City:               result.City,
			State:              result.State,
			DishName:           entry.DishName,
			Description:        entry.Description,
			StatedIngredients:  entry.StatedIngredients,
			HasFullIngredients: entry.HasFullIngredients,
			SourceURL:          rawURL,
			ScrapedAtUTC:       now,
			Vector:             vectors[i],
		}
	}
	return items, nil
}

// buildMenuStore constructs a Weaviate-backed MenuStore. Postgres and Pinecone
// paths can be added here following the same pattern as cli/index.go.
func buildMenuStore(_ context.Context, host, scheme, apiKey string, embedder search.Embedder) (server.MenuStore, error) {
	client, err := search.NewClient(host, scheme, apiKey, embedder)
	if err != nil {
		return nil, fmt.Errorf("weaviate client: %w", err)
	}
	return client, nil
}
