package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
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
	scrapeCmd.Flags().Bool("enable-js-render", false, "Tier 3: render JS-only pages via chromedp (requires Chrome)")
	scrapeCmd.Flags().Bool("enable-vision", false, "Send PDFs/images to the vision LLM instead of text extraction")
	scrapeCmd.Flags().Bool("pdftotext", false, "Use system pdftotext (poppler) for PDF text extraction")

	// Vision service
	scrapeCmd.Flags().String("extractor-url", "", "Python vision service base URL (e.g. http://localhost:8765); when set, vision is auto-active for scanned PDFs/images")
}

func runScrape(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	ctx := context.Background()

	ignoreRobots, _ := cmd.Flags().GetBool("ignore-robots")
	enableVision, _ := cmd.Flags().GetBool("enable-vision")
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
	storeType := viper.GetString("store")
	postgresDSN := viper.GetString("postgres-dsn")
	store, err := buildMenuStore(ctx, storeType, postgresDSN, weaviateHost, weaviateScheme, weaviateAPIKey, embedder)
	if err != nil {
		return fmt.Errorf("building store: %w", err)
	}
	if err := store.EnsureMenuSchema(ctx); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	// Build extractor.
	var ex scraper.Extractor
	ex, err = scraper.NewOpenAICompatExtractor(llmURL, llmModel, llmAPIKey, llmReasoningEffort)
	if err != nil {
		return fmt.Errorf("building extractor: %w", err)
	}

	// Build VisionExtractor based on flags.
	//   --extractor-url set           → Python microservice (vision auto-active)
	//   --enable-vision, no URL       → Go OpenAI vision adapter
	//   neither                       → no vision (error if PDF has no text layer)
	var visionEx scraper.VisionExtractor
	switch {
	case extractorURL != "":
		visionEx = &scraper.PythonVisionExtractor{
			BaseURL: extractorURL,
			Client:  &http.Client{Timeout: 120 * time.Second},
		}
		slog.Info("using Python vision extractor", "url", extractorURL)
	case enableVision:
		oaex, ok := ex.(*scraper.OpenAICompatExtractor)
		if !ok {
			return fmt.Errorf("vision path requires OpenAI-compat extractor")
		}
		visionEx = &scraper.OpenAIVisionAdapter{Ex: oaex}
		slog.Info("using Go OpenAI vision adapter")
	}
	// visionEx == nil → no vision configured

	fetcher := scraper.NewHTTPFetcher(ignoreRobots)

	return runScrapeWith(ctx, rawURL, fetcher, ex, visionEx, store, embedder, usePdftotext)
}

// runScrapeWith is the testable core of the scrape command. All dependencies
// are injected so tests can pass stubs directly.
func runScrapeWith(
	ctx context.Context,
	rawURL string,
	fetcher scraper.Fetcher,
	ex scraper.Extractor,
	visionEx scraper.VisionExtractor, // nil when no vision configured
	store server.MenuStore,
	embedder search.Embedder,
	usePdftotext bool,
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
	var rawPayload json.RawMessage

	if !usedJSONLD {
		var pageText string

		if strings.Contains(ct, "pdf") {
			// First: try text layer extraction.
			text, textErr := scraper.ExtractPDFText(bodyBytes, usePdftotext)
			if textErr == nil {
				// Text layer succeeded — use Tier-1 LLM extraction below.
				pageText = text
			} else if errors.Is(textErr, scraper.ErrNeedVision) {
				// Needs vision path.
				if visionEx == nil {
					return fmt.Errorf("PDF has no usable text layer; set --enable-vision (Go LLM path) or --extractor-url (Python service)")
				}
				result, rawPayload, err = visionEx.ExtractDocument(ctx, bodyBytes, ct)
				if err != nil {
					return fmt.Errorf("vision extraction: %w", err)
				}
				result.SourceURL = rawURL
				result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)
				// No JSON-LD backfill on vision branch: PDF inputs never carry JSON-LD.
			} else {
				return fmt.Errorf("PDF text extraction: %w", textErr)
			}
		} else {
			// HTML path — convert to markdown then optionally fall back to trafilatura.
			md, err := scraper.ConvertHTMLToMarkdown(bytes.NewReader(bodyBytes), ct)
			if err != nil {
				return fmt.Errorf("HTML conversion: %w", err)
			}
			if scraper.IsTooNoisy(md) {
				slog.Warn("HTML→Markdown output is noisy, falling back to trafilatura", "url", rawURL)
				if fallback := scraper.TrafilaturaFallback(string(bodyBytes)); fallback != "" {
					md = fallback
				} else {
					slog.Warn("trafilatura fallback produced no output, using original conversion", "url", rawURL)
				}
			}
			pageText = md
		}

		// Tier-1 LLM extraction only when the text path was taken.
		// The vision branch sets result directly above and leaves pageText empty.
		if pageText != "" {
			slog.Info("Tier 1: sending to LLM extractor", "chars", len([]rune(pageText)))
			result, err = ex.Extract(ctx, pageText)
			if err != nil {
				return fmt.Errorf("LLM extraction: %w", err)
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
	}

	if len(result.Items) == 0 {
		slog.Warn("no menu items extracted", "url", rawURL)
		fmt.Printf("No menu items found at %s\n", rawURL)
		return nil
	}

	slog.Info("extracted menu items", "count", len(result.Items), "restaurant", result.RestaurantName)

	// ── Embed + upsert ────────────────────────────────────────────────────────
	items, err := toMenuItems(ctx, result, rawURL, rawPayload, embedder)
	if err != nil {
		return fmt.Errorf("embedding menu items: %w", err)
	}

	if err := store.BatchUpsertMenu(ctx, items); err != nil {
		return fmt.Errorf("upserting menu items: %w", err)
	}

	fmt.Printf("Scraped %d menu items from %q (%s)\n", len(items), result.RestaurantName, rawURL)
	return nil
}

// menuCollectionNS is the UUID namespace for deterministic menu item IDs.
var menuCollectionNS = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// toMenuItems converts a MenuExtractionResult to []search.MenuItem, embedding
// each item's text vector. rawPayload is the raw schema-v1 JSON document from
// the vision path; it is nil on text/HTML paths and stored as-is on every item.
func toMenuItems(ctx context.Context, result scraper.MenuExtractionResult, rawURL string, rawPayload json.RawMessage, embedder search.Embedder) ([]search.MenuItem, error) {
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
			Payload:            rawPayload,
		}
	}
	return items, nil
}

// buildMenuStore constructs a MenuStore for the given backend type.
// Supported backends: "postgres" and "weaviate" (default).
func buildMenuStore(_ context.Context, storeType, postgresDSN, host, scheme, apiKey string, embedder search.Embedder) (server.MenuStore, error) {
	switch storeType {
	case "postgres":
		if postgresDSN == "" {
			return nil, fmt.Errorf("--postgres-dsn is required when --store=postgres")
		}
		return search.NewPostgresClient(postgresDSN, embedder)
	default: // "weaviate"
		client, err := search.NewClient(host, scheme, apiKey, embedder)
		if err != nil {
			return nil, fmt.Errorf("weaviate client: %w", err)
		}
		return client, nil
	}
}
